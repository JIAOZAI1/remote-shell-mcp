package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDeletePersistence(t *testing.T) {
	a, _ := fixture(t)
	s, err := a.add(Server{Name: "test", Host: "localhost", User: "test", CredentialRef: "test", Workdir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	original := a.dir
	a.dir = filepath.Join(original, "missing")
	if err := a.deleteServer(s.ID); err == nil {
		t.Fatal("expected persistence failure")
	}
	if _, err := a.lookup(s.ID); err != nil {
		t.Fatal("failed deletion changed memory")
	}
	a.dir = original
	if err := a.deleteServer(s.ID); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(original, "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var servers []Server
	if err := json.Unmarshal(b, &servers); err != nil || len(servers) != 0 {
		t.Fatalf("persisted: %s, %v", b, err)
	}
	if err := a.deleteServer(s.ID); err == nil {
		t.Fatal("unknown id accepted")
	}
	if len(a.config.Credentials) != 1 {
		t.Fatal("credentials changed")
	}
}
