package app

import (
	"errors"
	"os"

	"golang.org/x/crypto/ssh"
)

// parseSigner never includes key material or passphrases in errors.
func parseSigner(key []byte, passphraseEnv string) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(key)
	if err == nil {
		return signer, nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, errors.New("invalid_private_key: expected an SSH private key, not a public key")
	}
	if passphraseEnv == "" {
		return nil, errors.New("private_key_passphrase_required: configure credential_passphrase_env for this credential")
	}
	passphrase, ok := os.LookupEnv(passphraseEnv)
	if !ok || passphrase == "" {
		return nil, errors.New("private_key_passphrase_missing: configured environment variable is unset or empty")
	}
	secret := []byte(passphrase)
	defer clear(secret)
	signer, err = ssh.ParsePrivateKeyWithPassphrase(key, secret)
	if err != nil {
		return nil, errors.New("private_key_decryption_failed: incorrect passphrase or unsupported or damaged key")
	}
	return signer, nil
}
