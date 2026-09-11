package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestParseSigner(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := ssh.MarshalPrivateKeyWithPassphrase(key, "", []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	const env = "REMOTE_SHELL_TEST_PASSPHRASE"
	for _, tt := range []struct {
		name             string
		data             []byte
		ref, value, want string
	}{
		{"plain", pem.EncodeToMemory(plain), "", "", ""},
		{"encrypted", pem.EncodeToMemory(encrypted), env, "test-secret", ""},
		{"no reference", pem.EncodeToMemory(encrypted), "", "", "private_key_passphrase_required"},
		{"empty environment", pem.EncodeToMemory(encrypted), env, "", "private_key_passphrase_missing"},
		{"wrong passphrase", pem.EncodeToMemory(encrypted), env, "wrong-secret", "private_key_decryption_failed"},
		{"invalid key", []byte("invalid"), "", "", "invalid_private_key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(env, tt.value)
			signer, err := parseSigner(tt.data, tt.ref)
			if tt.want == "" {
				if err != nil || signer == nil {
					t.Fatalf("parse signer: %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
			if strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "wrong-secret") {
				t.Fatal("passphrase leaked")
			}
		})
	}
}
