package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func tool[I any](server *mcp.Server, name, description string, fn func(context.Context, I) (any, error)) {
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: description}, func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, any, error) {
		out, err := fn(ctx, in)
		return nil, out, err
	})
}

type ExecArgs struct {
	ServerID string `json:"server_id"`
	Command  string `json:"command"`
	Cwd      string `json:"cwd,omitempty"`
	Timeout  int    `json:"timeout_seconds,omitempty"`
}
type AddArgs struct {
	Name          string `json:"name"`
	Host          string `json:"host"`
	Port          int    `json:"port,omitempty"`
	User          string `json:"user"`
	CredentialRef string `json:"credential_ref"`
	Workdir       string `json:"workdir"`
}
type DeleteArgs struct {
	ServerID string `json:"server_id"`
}
type ListArgs struct {
	Query string `json:"query,omitempty"`
}
type ReadArgs struct {
	ServerID string `json:"server_id"`
	Path     string `json:"path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}
type WriteArgs struct {
	ServerID      string `json:"server_id"`
	Path          string `json:"path"`
	Content       string `json:"content"`
	Overwrite     bool   `json:"overwrite,omitempty"`
	CreateParents bool   `json:"create_parents,omitempty"`
}
type TerminalArgs struct {
	ServerID   string `json:"server_id"`
	TerminalID string `json:"terminal_id,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Text       string `json:"text,omitempty"`
	Submit     bool   `json:"submit,omitempty"`
	Key        string `json:"key,omitempty"`
	Lines      int    `json:"lines,omitempty"`
}

// MCP constructs the stdio-compatible server and registers its tools.
func (a *App) MCP() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "remote-shell-mcp", Version: "0.1.0"}, nil)
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return toolLogging(a.logger, next)
	})
	tool(s, "server_delete", "Delete local server configuration by server_id. Unknown IDs fail. Does not delete credentials, remote files or tmux sessions, or cancel in-flight operations.", func(ctx context.Context, in DeleteArgs) (any, error) {
		if err := a.deleteServer(in.ServerID); err != nil {
			return nil, err
		}
		return map[string]any{"server_id": in.ServerID, "deleted": true}, nil
	})
	tool(s, "server_list", "Find configured servers. Connection credentials are never returned.", func(ctx context.Context, in ListArgs) (any, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		out := []map[string]any{}
		for _, v := range a.servers {
			if in.Query != "" && !strings.Contains(strings.ToLower(v.Name+" "+v.Host), strings.ToLower(in.Query)) {
				continue
			}
			out = append(out, map[string]any{"server_id": v.ID, "name": v.Name, "host": v.Host, "user": v.User, "workdir": v.Workdir})
		}
		return map[string]any{"servers": out}, nil
	})
	tool(s, "server_add", "Persist a server using an existing local credential_ref. Does not test connectivity or trust unknown host keys.", func(ctx context.Context, in AddArgs) (any, error) {
		v, err := a.add(Server{Name: in.Name, Host: in.Host, Port: in.Port, User: in.User, CredentialRef: in.CredentialRef, Workdir: in.Workdir})
		if err != nil {
			return nil, err
		}
		return map[string]any{"server_id": v.ID, "connection_verified": false}, nil
	})
	tool(s, "remote_exec", "Execute an independent remote POSIX shell command. No persistent state. Cancellation may leave remote processes running; never automatically retry an unknown outcome.", func(ctx context.Context, in ExecArgs) (any, error) {
		v, err := a.lookup(in.ServerID)
		if err != nil {
			return nil, err
		}
		return a.exec(ctx, v, in.Command, in.Cwd, in.Timeout)
	})
	tool(s, "remote_read_file", "Read UTF-8 text up to 10 MiB. Offset is 1-based line number. Relative paths use remote workdir.", func(ctx context.Context, in ReadArgs) (any, error) {
		return a.read(ctx, FileArgs{ServerID: in.ServerID, Path: in.Path, Offset: in.Offset, Limit: in.Limit})
	})
	tool(s, "remote_write_file", "Create or explicitly overwrite remote UTF-8 text. Atomic commit requires SFTP extensions. Parents must exist unless create_parents is true.", func(ctx context.Context, in WriteArgs) (any, error) {
		return a.write(ctx, FileArgs{ServerID: in.ServerID, Path: in.Path, Content: in.Content, Overwrite: in.Overwrite, CreateParents: in.CreateParents})
	})
	tool(s, "remote_list_dir", "List remote directory entries. Offset is 0-based; pagination may change if directory changes.", func(ctx context.Context, in ReadArgs) (any, error) {
		return a.listDir(ctx, FileArgs{ServerID: in.ServerID, Path: in.Path, Offset: in.Offset, Limit: in.Limit})
	})
	tool(s, "remote_upload", "Upload one local file from the MCP Server machine. Relative local paths use the process working directory. Maximum 1 GiB. No overwrite by default.", func(ctx context.Context, in TransferArgs) (any, error) { return a.transfer(ctx, in, true) })
	tool(s, "remote_download", "Download one file to the MCP Server machine. Relative local paths use the process working directory. Maximum 1 GiB. No overwrite by default.", func(ctx context.Context, in TransferArgs) (any, error) { return a.transfer(ctx, in, false) })
	var terminalMu sync.Mutex
	for _, operation := range []string{"create", "list", "send", "read", "close"} {
		tool(s, "remote_terminal_"+operation, terminalDescription(operation), func(ctx context.Context, in TerminalArgs) (any, error) {
			terminalMu.Lock()
			defer terminalMu.Unlock()
			return a.terminal(ctx, operation, in)
		})
	}
	return s
}
func terminalDescription(op string) string {
	switch op {
	case "create":
		return "Create a persistent remote tmux terminal; requires tmux installed. Returns terminal_id."
	case "list":
		return "List this project's persistent terminals on a server."
	case "send":
		return "Send literal text, optionally submit with Enter, or key Enter/C-c/C-d/Tab/Escape. Success does not mean command completion; never replay automatically."
	case "read":
		return "Read bounded tmux screen/history snapshot, not independent stdout or command exit status."
	default:
		return "Destroy a project terminal and terminate its tasks. Explicit destructive action."
	}
}
func (a *App) terminal(ctx context.Context, op string, in TerminalArgs) (any, error) {
	s, err := a.lookup(in.ServerID)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(s.ID))
	base := "tmux -L " + quote("remote-shell-"+hex.EncodeToString(hash[:8]))
	if op != "create" && op != "list" {
		if !strings.HasPrefix(in.TerminalID, "term_") || len(in.TerminalID) > 80 {
			return nil, errors.New("invalid terminal_id")
		}
		for _, r := range strings.TrimPrefix(in.TerminalID, "term_") {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				return nil, errors.New("invalid terminal_id")
			}
		}
	}
	target := quote("=" + in.TerminalID)
	var command string
	switch op {
	case "create":
		in.TerminalID = "term_" + rand.Text()
		cwd := in.Cwd
		if cwd == "" {
			cwd = s.Workdir
		}
		cwd, err = remotePath(s, cwd)
		if err != nil {
			return nil, err
		}
		command = base + " new-session -d -s " + quote(in.TerminalID) + " -c " + quote(cwd)
	case "list":
		command = base + " list-sessions -F '#{session_name}'"
	case "read":
		if in.Lines == 0 {
			in.Lines = 200
		}
		if in.Lines < 1 || in.Lines > 10000 {
			return nil, errors.New("lines must be between 1 and 10000")
		}
		command = base + " capture-pane -p -t " + target + fmt.Sprintf(" -S -%d", in.Lines)
	case "close":
		command = base + " kill-session -t " + target
	case "send":
		if len(in.Text) > 65536 || strings.ContainsRune(in.Text, 0) {
			return nil, errors.New("invalid terminal input")
		}
		if in.Key != "" {
			if in.Text != "" || in.Submit {
				return nil, errors.New("key cannot be combined with text or submit")
			}
			switch in.Key {
			case "Enter", "C-c", "C-d", "Tab", "Escape":
			default:
				return nil, errors.New("unsupported key")
			}
			command = base + " send-keys -t " + target + " " + quote(in.Key)
		} else {
			command = base + " send-keys -t " + target + " -l -- " + quote(in.Text)
			if in.Submit {
				command += " && " + base + " send-keys -t " + target + " Enter"
			}
		}
	default:
		return nil, errors.New("unknown terminal operation")
	}
	r, err := requireSuccess(a.exec(ctx, s, "command -v tmux >/dev/null 2>&1 || { printf 'tmux_not_found' >&2; exit 127; }; "+command, "", 30))
	if err != nil {
		return nil, err
	}
	if op == "list" {
		ids := []string{}
		for _, id := range strings.Fields(r.Stdout) {
			if strings.HasPrefix(id, "term_") {
				ids = append(ids, id)
			}
		}
		return map[string]any{"terminals": ids}, nil
	}
	return map[string]any{"terminal_id": in.TerminalID, "output": r.Stdout, "truncated": r.Truncated}, nil
}
