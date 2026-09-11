package app

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

func TestSFTPAtomicWrite(t *testing.T) {
	local, remote := net.Pipe()
	server, err := sftp.NewServer(remote)
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { defer close(exited); defer remote.Close(); server.Serve() }()
	client, err := sftp.NewClientPipe(local, local)
	if err != nil {
		local.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); local.Close(); server.Close(); <-exited })
	dest := filepath.ToSlash(filepath.Join(t.TempDir(), "file.txt"))
	if _, err = put(client, dest, strings.NewReader("first"), false, false, 100); err != nil {
		t.Fatal(err)
	}
	if _, err = put(client, dest, strings.NewReader("bad"), false, false, 100); err == nil {
		t.Fatal("overwrite without permission")
	}
	if _, err = put(client, dest, strings.NewReader("too large"), true, false, 2); err == nil {
		t.Fatal("size limit ignored")
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "first" {
		t.Fatalf("failed write changed file: %q", b)
	}
	if _, err = put(client, dest, strings.NewReader("second"), true, false, 100); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "second" {
		t.Fatalf("overwrite: %q", b)
	}
	files, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("temporary files leaked: %v", files)
	}
}
