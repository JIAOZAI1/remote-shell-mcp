package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// connect uses a dedicated connection per operation, isolating cancellation.
func (a *App) connect(ctx context.Context, s Server) (*ssh.Client, func(), error) {
	select {
	case a.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	release := func() { <-a.slots }
	key, err := os.ReadFile(a.config.Credentials[s.CredentialRef])
	if err != nil {
		release()
		return nil, nil, errors.New("credential_unavailable")
	}
	signer, err := parseSigner(key, a.config.CredentialPassphraseEnv[s.CredentialRef])
	if err != nil {
		release()
		return nil, nil, err
	}
	callback, err := knownhosts.New(a.config.KnownHosts)
	if err != nil {
		release()
		return nil, nil, errors.New("invalid known_hosts")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort(s.Host, strconv.Itoa(s.Port)))
	if err != nil {
		release()
		return nil, nil, errors.New("connection_failed")
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		stop()
		conn.Close()
		release()
		return nil, nil, err
	}
	cc, ch, req, err := ssh.NewClientConn(conn, net.JoinHostPort(s.Host, strconv.Itoa(s.Port)), &ssh.ClientConfig{
		User:              s.User,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSA},
		HostKeyCallback:   callback,
	})
	if err != nil {
		stop()
		conn.Close()
		release()
		return nil, nil, fmt.Errorf("ssh_handshake_failed: %w", err)
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		stop()
		cc.Close()
		release()
		return nil, nil, err
	}
	client := ssh.NewClient(cc, ch, req)
	return client, func() { stop(); client.Close(); release() }, nil
}

type capped struct {
	b         bytes.Buffer
	truncated bool
}

func (w *capped) Write(p []byte) (int, error) {
	n := len(p)
	left := (1 << 20) - w.b.Len()
	if len(p) > left {
		w.truncated = true
		p = p[:left]
	}
	w.b.Write(p)
	return n, nil
}

type ExecResult struct {
	Stdout         string `json:"stdout"`
	Stderr         string `json:"stderr"`
	ExitCode       *int   `json:"exit_code"`
	TimedOut       bool   `json:"timed_out"`
	Truncated      bool   `json:"truncated"`
	OutcomeUnknown bool   `json:"outcome_unknown"`
}

func (a *App) exec(ctx context.Context, s Server, command, cwd string, seconds int) (ExecResult, error) {
	var result ExecResult
	if command == "" || len(command) > 1<<20 || strings.ContainsRune(command, 0) {
		return result, errors.New("invalid command")
	}
	if seconds == 0 {
		seconds = 60
	}
	if seconds < 1 || seconds > 600 {
		return result, errors.New("timeout must be between 1 and 600 seconds")
	}
	if cwd == "" {
		cwd = s.Workdir
	}
	cwd, err := remotePath(s, cwd)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	client, closeConn, err := a.connect(ctx, s)
	if err != nil {
		return result, err
	}
	defer closeConn()
	session, err := client.NewSession()
	if err != nil {
		return result, errors.New("session_failed")
	}
	defer session.Close()
	var out, stderr capped
	session.Stdout = &out
	session.Stderr = &stderr
	err = session.Run("cd -- " + quote(cwd) + " && " + command)
	result.Stdout = out.b.String()
	result.Stderr = stderr.b.String()
	result.Truncated = out.truncated || stderr.truncated
	if ctx.Err() != nil {
		result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		result.OutcomeUnknown = true
		return result, nil
	}
	if err == nil {
		code := 0
		result.ExitCode = &code
		return result, nil
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitStatus()
		result.ExitCode = &code
		return result, nil
	}
	result.OutcomeUnknown = true
	return result, nil
}
func requireSuccess(r ExecResult, err error) (ExecResult, error) {
	if err != nil {
		return r, err
	}
	if r.OutcomeUnknown || r.ExitCode == nil {
		return r, errors.New("outcome_unknown: inspect remote state before retrying")
	}
	if *r.ExitCode != 0 {
		return r, fmt.Errorf("remote operation failed: %s", r.Stderr)
	}
	return r, nil
}
