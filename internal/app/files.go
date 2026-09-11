package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
)

const maxText = 10 << 20
const maxTransfer = 1 << 30

type FileArgs struct {
	ServerID      string `json:"server_id"`
	Path          string `json:"path"`
	Content       string `json:"content,omitempty"`
	Offset        int    `json:"offset,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Overwrite     bool   `json:"overwrite,omitempty"`
	CreateParents bool   `json:"create_parents,omitempty"`
}

func (a *App) files(ctx context.Context, id string) (*sftp.Client, Server, func(), error) {
	s, err := a.lookup(id)
	if err != nil {
		return nil, s, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	client, done, err := a.connect(ctx, s)
	if err != nil {
		cancel()
		return nil, s, nil, err
	}
	fs, err := sftp.NewClient(client)
	if err != nil {
		done()
		cancel()
		return nil, s, nil, errors.New("sftp_unavailable")
	}
	return fs, s, func() { fs.Close(); done(); cancel() }, nil
}

type ReadResult struct {
	Content   string `json:"content"`
	Offset    int    `json:"offset"`
	Lines     int    `json:"lines"`
	Truncated bool   `json:"truncated"`
}

func (a *App) read(ctx context.Context, in FileArgs) (ReadResult, error) {
	var out ReadResult
	if in.Offset == 0 {
		in.Offset = 1
	}
	if in.Limit == 0 {
		in.Limit = 200
	}
	if in.Offset < 1 || in.Limit < 1 || in.Limit > 10000 {
		return out, errors.New("invalid read range")
	}
	fs, s, done, err := a.files(ctx, in.ServerID)
	if err != nil {
		return out, err
	}
	defer done()
	p, err := remotePath(s, in.Path)
	if err != nil {
		return out, err
	}
	f, err := fs.Open(p)
	if err != nil {
		return out, errors.New("file_open_failed")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxText+1))
	if err != nil {
		return out, errors.New("file_read_failed")
	}
	if len(b) > maxText {
		return out, errors.New("file exceeds text limit; use remote_download")
	}
	if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		return out, errors.New("not a UTF-8 text file")
	}
	out.Offset = in.Offset
	scanner := bufio.NewScanner(strings.NewReader(string(b)))
	scanner.Buffer(make([]byte, 4096), maxText+1)
	var lines []string
	line := 0
	for scanner.Scan() {
		line++
		if line < in.Offset {
			continue
		}
		if len(lines) == in.Limit {
			out.Truncated = true
			break
		}
		lines = append(lines, scanner.Text())
	}
	if err = scanner.Err(); err != nil {
		return out, err
	}
	out.Content = strings.Join(lines, "\n")
	out.Lines = len(lines)
	return out, nil
}
func put(fs *sftp.Client, p string, r io.Reader, overwrite, parents bool, max int64) (int64, error) {
	if parents {
		if err := fs.MkdirAll(path.Dir(p)); err != nil {
			return 0, errors.New("parent_creation_failed")
		}
	}
	mode := os.FileMode(0600)
	st, err := fs.Lstat(p)
	if err == nil {
		if !st.Mode().IsRegular() {
			return 0, errors.New("destination is not a regular file")
		}
		if !overwrite {
			return 0, errors.New("destination_exists")
		}
		mode = st.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, errors.New("destination_stat_failed")
	}
	if overwrite {
		if _, ok := fs.HasExtension("posix-rename@openssh.com"); !ok {
			return 0, errors.New("atomic overwrite unsupported")
		}
	} else {
		if _, ok := fs.HasExtension("hardlink@openssh.com"); !ok {
			return 0, errors.New("atomic no-clobber commit unsupported")
		}
	}
	temp := path.Join(path.Dir(p), ".remote-shell-"+rand.Text())
	f, err := fs.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return 0, errors.New("temporary_file_failed")
	}
	defer fs.Remove(temp)
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	closeErr := f.Close()
	if err != nil {
		return n, errors.New("transfer_failed: outcome_unknown")
	}
	if closeErr != nil {
		return n, errors.New("file_close_failed: outcome_unknown")
	}
	if n > max {
		return n, errors.New("file size limit exceeded")
	}
	if err = fs.Chmod(temp, mode); err != nil {
		return n, errors.New("chmod_failed")
	}
	if overwrite {
		err = fs.PosixRename(temp, p)
	} else {
		err = fs.Link(temp, p)
	}
	if err != nil {
		return n, errors.New("commit_failed: destination may exist or outcome_unknown")
	}
	return n, nil
}
func (a *App) write(ctx context.Context, in FileArgs) (any, error) {
	if len(in.Content) > maxText || !utf8.ValidString(in.Content) {
		return nil, errors.New("invalid or oversized content")
	}
	fs, s, done, err := a.files(ctx, in.ServerID)
	if err != nil {
		return nil, err
	}
	defer done()
	p, err := remotePath(s, in.Path)
	if err != nil {
		return nil, err
	}
	n, err := put(fs, p, strings.NewReader(in.Content), in.Overwrite, in.CreateParents, maxText)
	return map[string]any{"bytes": n}, err
}

type Entry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

func (a *App) listDir(ctx context.Context, in FileArgs) (any, error) {
	if in.Limit == 0 {
		in.Limit = 200
	}
	if in.Offset < 0 || in.Limit < 1 || in.Limit > 10000 {
		return nil, errors.New("invalid directory range")
	}
	fs, s, done, err := a.files(ctx, in.ServerID)
	if err != nil {
		return nil, err
	}
	defer done()
	p, err := remotePath(s, in.Path)
	if err != nil {
		return nil, err
	}
	// ReadDirContext cancels on request cancellation; output pagination is a snapshot slice.
	entries, err := fs.ReadDirContext(ctx, p)
	if err != nil {
		return nil, errors.New("directory_read_failed")
	}
	start := min(in.Offset, len(entries))
	end := min(start+in.Limit, len(entries))
	out := make([]Entry, 0, end-start)
	for _, e := range entries[start:end] {
		kind := "file"
		if e.IsDir() {
			kind = "directory"
		} else if e.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
		} else if !e.Mode().IsRegular() {
			kind = "other"
		}
		out = append(out, Entry{e.Name(), kind, e.Size()})
	}
	return map[string]any{"entries": out, "next_offset": end, "has_more": end < len(entries)}, nil
}

type TransferArgs struct {
	ServerID      string `json:"server_id"`
	LocalPath     string `json:"local_path"`
	RemotePath    string `json:"remote_path"`
	Overwrite     bool   `json:"overwrite,omitempty"`
	CreateParents bool   `json:"create_parents,omitempty"`
}

func localName(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", errors.New("invalid local path")
	}
	return filepath.Abs(p)
}
func (a *App) transfer(ctx context.Context, in TransferArgs, upload bool) (any, error) {
	local, err := localName(in.LocalPath)
	if err != nil {
		return nil, err
	}
	fs, s, done, err := a.files(ctx, in.ServerID)
	if err != nil {
		return nil, err
	}
	defer done()
	remote, err := remotePath(s, in.RemotePath)
	if err != nil {
		return nil, err
	}
	if upload {
		f, err := os.Open(local)
		if err != nil {
			return nil, errors.New("local_file_open_failed")
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			return nil, errors.New("source must be a regular file")
		}
		n, err := put(fs, remote, f, in.Overwrite, in.CreateParents, maxTransfer)
		return map[string]any{"bytes": n}, err
	}
	f, err := fs.Open(remote)
	if err != nil {
		return nil, errors.New("remote_file_open_failed")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("source must be a regular file")
	}
	if in.CreateParents {
		if err = os.MkdirAll(filepath.Dir(local), 0700); err != nil {
			return nil, errors.New("local_parent_creation_failed")
		}
	}
	if st, err := os.Lstat(local); err == nil {
		if !st.Mode().IsRegular() {
			return nil, errors.New("destination is not a regular file")
		}
		if !in.Overwrite {
			return nil, errors.New("destination_exists")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("local_stat_failed")
	}
	temp := filepath.Join(filepath.Dir(local), ".remote-shell-"+rand.Text())
	out, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("local_temporary_file_failed")
	}
	defer os.Remove(temp)
	n, err := io.Copy(out, io.LimitReader(f, maxTransfer+1))
	if err != nil {
		out.Close()
		return nil, errors.New("download_failed")
	}
	if err = out.Sync(); err != nil {
		out.Close()
		return nil, errors.New("local_sync_failed")
	}
	if err = out.Close(); err != nil {
		return nil, errors.New("local_close_failed")
	}
	if n > maxTransfer {
		return nil, errors.New("file size limit exceeded")
	}
	if in.Overwrite {
		err = os.Rename(temp, local)
	} else {
		err = os.Link(temp, local)
	}
	if err != nil {
		return nil, errors.New("local_commit_failed")
	}
	return map[string]any{"bytes": n}, nil
}
