package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func fixture(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	known := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(known, nil, 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{KnownHosts: known, Credentials: map[string]string{"test": filepath.Join(dir, "key")}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.json")
	if err = os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, p
}
func TestPersistenceAndLock(t *testing.T) {
	a, p := fixture(t)
	if _, err := Load(p); err == nil {
		t.Fatal("second writer accepted")
	}
	s, err := a.add(Server{Name: "test", Host: "localhost", User: "test", CredentialRef: "test", Workdir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 22 || s.ID == "" {
		t.Fatalf("unexpected server: %+v", s)
	}
	if _, err = a.add(s); err == nil {
		t.Fatal("duplicate accepted")
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(p), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []Server
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != s {
		t.Fatalf("persisted: %+v", got)
	}
}
func TestPathsAndQuotes(t *testing.T) {
	s := Server{Workdir: "/srv/project"}
	for _, tt := range []struct{ in, want string }{{"a b", "/srv/project/a b"}, {"../x", "/srv/x"}, {"/etc/hosts", "/etc/hosts"}} {
		got, err := remotePath(s, tt.in)
		if err != nil || got != tt.want {
			t.Fatalf("%q: %q %v", tt.in, got, err)
		}
	}
	if _, err := remotePath(s, "a\x00b"); err == nil {
		t.Fatal("NUL accepted")
	}
	if got := quote("a'b"); got != "'a'\"'\"'b'" {
		t.Fatalf("quote: %s", got)
	}
	for _, p := range []string{"file.txt", "../file.txt", filepath.Join(t.TempDir(), "file.txt")} {
		want, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := localName(p)
		if err != nil || got != want {
			t.Fatalf("localName(%q): %q, %v; want %q", p, got, err, want)
		}
	}
	for _, p := range []string{"", "a\x00b"} {
		if _, err := localName(p); err == nil {
			t.Fatalf("invalid path accepted: %q", p)
		}
	}
}
func TestOutputCap(t *testing.T) {
	var w capped
	data := strings.Repeat("x", (1<<20)+5)
	n, err := w.Write([]byte(data))
	if err != nil || n != len(data) || w.b.Len() != 1<<20 || !w.truncated {
		t.Fatal("invalid output cap")
	}
	if _, err = w.Write([]byte("more")); err != nil {
		t.Fatal(err)
	}
}
func TestMCP(t *testing.T) {
	a, _ := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := a.MCP().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 14 {
		t.Fatalf("got %d tools", len(tools.Tools))
	}
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "server_add", Arguments: map[string]any{"name": "dev", "host": "localhost", "user": "test", "credential_ref": "test", "workdir": "/tmp"}})
	if err != nil || r.IsError {
		t.Fatalf("add: %+v %v", r, err)
	}
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "server_list", Arguments: map[string]any{}})
	if err != nil || r.IsError {
		t.Fatalf("list: %+v %v", r, err)
	}
	id := a.servers[0].ID
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "server_delete", Arguments: map[string]any{"server_id": id}})
	if err != nil || r.IsError {
		t.Fatalf("delete: %+v %v", r, err)
	}
	if _, err := a.lookup(id); err == nil {
		t.Fatal("deleted server still available")
	}
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "remote_exec", Arguments: map[string]any{"server_id": "missing", "command": "echo hello"}})
	if err != nil || !r.IsError {
		t.Fatalf("tool error: %+v %v", r, err)
	}
	for _, params := range []*mcp.CallToolParams{
		{Name: "unknown_tool", Arguments: map[string]any{}},
		{Name: "server_delete", Arguments: map[string]any{"server_id": 123}},
	} {
		r, err := cs.CallTool(ctx, params)
		if err == nil && !r.IsError {
			t.Fatalf("invalid call accepted: %s", params.Name)
		}
	}
	b, err := os.ReadFile(a.logFile.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"tool_started", "tool_finished", "server_add", "server_delete", "unknown_tool", "protocol_error", "tool_error"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in local log: %s", want, text)
		}
	}
	if strings.Contains(text, "echo hello") || strings.Contains(text, "credential_ref") {
		t.Fatal("tool arguments leaked to local log")
	}
}
