package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestSSHExecutionAndHostVerification(t *testing.T) {
	a, _ := fixture(t)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(a.config.Credentials["test"], pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if string(k.Marshal()) != string(signer.PublicKey().Marshal()) {
			return nil, fmt.Errorf("unknown key")
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer raw.Close()
				conn, channels, requests, err := ssh.NewServerConn(raw, cfg)
				if err != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					ch, reqs, err := incoming.Accept()
					if err != nil {
						return
					}
					for req := range reqs {
						if req.Type != "exec" {
							req.Reply(false, nil)
							continue
						}
						var payload struct{ Command string }
						if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
							req.Reply(false, nil)
							break
						}
						req.Reply(true, nil)
						ch.Write([]byte("remote-output\n"))
						ch.Stderr().Write([]byte("remote-error\n"))
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{7}))
						ch.Close()
						break
					}
				}
			}()
		}
	}()
	host, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err = fmt.Sscan(portText, &port); err != nil {
		t.Fatal(err)
	}
	s := Server{Host: host, Port: port, User: "test", CredentialRef: "test", Workdir: "/tmp"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = a.exec(ctx, s, "echo hello", "", 2); err == nil {
		t.Fatal("unknown host was accepted")
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(ln.Addr().String())}, signer.PublicKey()) + "\n"
	if err = os.WriteFile(filepath.Clean(a.config.KnownHosts), []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := a.exec(ctx, s, "echo hello", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode == nil || *result.ExitCode != 7 || result.Stdout != "remote-output\n" || result.Stderr != "remote-error\n" {
		t.Fatalf("unexpected result: %+v", result)
	}
	ln.Close()
	<-finished
}
