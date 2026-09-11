package app

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Server struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	CredentialRef string `json:"credential_ref"`
	Workdir       string `json:"workdir"`
}
type Config struct {
	Logging                 LogConfig         `json:"logging,omitempty"`
	CredentialPassphraseEnv map[string]string `json:"credential_passphrase_env,omitempty"`
	KnownHosts              string            `json:"known_hosts"`
	Credentials             map[string]string `json:"credentials"`
}
type App struct {
	config  Config
	dir     string
	mu      sync.Mutex
	servers []Server
	lock    *os.File
	slots   chan struct{}
	logger  *slog.Logger
	logFile *dailyLog
}

func Load(filename string) (*App, error) {
	b, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var c Config
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(c.KnownHosts) {
		return nil, errors.New("known_hosts must be an absolute local path")
	}
	if _, err = os.Stat(c.KnownHosts); err != nil {
		return nil, fmt.Errorf("known_hosts: %w", err)
	}
	for name, p := range c.Credentials {
		if name == "" || !filepath.IsAbs(p) {
			return nil, errors.New("credentials require names and absolute key paths")
		}
	}
	for ref, env := range c.CredentialPassphraseEnv {
		if _, ok := c.Credentials[ref]; !ok {
			return nil, errors.New("passphrase configuration references unknown credential")
		}
		if env == "" || strings.ContainsAny(env, "=\x00") {
			return nil, errors.New("invalid passphrase environment variable name")
		}
	}
	dir := filepath.Dir(filename)
	lock, err := os.OpenFile(filepath.Join(dir, "servers.lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("configuration already locked; remove stale servers.lock only after confirming no process is running: %w", err)
	}
	a := &App{config: c, dir: dir, lock: lock, slots: make(chan struct{}, 4)}
	ok := false
	defer func() {
		if !ok {
			a.Close()
		}
	}()
	b, err = os.ReadFile(filepath.Join(dir, "servers.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(b, &a.servers); err != nil {
			return nil, err
		}
	}
	ids := map[string]bool{}
	for _, s := range a.servers {
		if err = a.validate(s); err != nil {
			return nil, err
		}
		if s.ID == "" || ids[s.ID] {
			return nil, errors.New("invalid or duplicate server id")
		}
		ids[s.ID] = true
	}
	a.logFile, err = openDailyLog(dir, c.Logging, time.Now)
	if err != nil {
		return nil, err
	}
	a.logger = slog.New(slog.NewJSONHandler(logFallback{primary: a.logFile, fallback: os.Stderr}, nil))
	ok = true
	return a, nil
}
func (a *App) Close() {
	if a.logFile != nil {
		if err := a.logFile.Close(); err != nil {
			slog.Error("close local log failed")
		}
	}
	if a.lock != nil {
		a.lock.Close()
		os.Remove(filepath.Join(a.dir, "servers.lock"))
	}
}
func (a *App) validate(s Server) error {
	if strings.TrimSpace(s.Name) == "" || s.Host == "" || strings.ContainsAny(s.Host, "\x00\r\n /\\") || s.User == "" || strings.ContainsAny(s.User, "\x00\r\n") || s.Port < 1 || s.Port > 65535 || !path.IsAbs(s.Workdir) || strings.ContainsRune(s.Workdir, 0) {
		return errors.New("invalid server configuration")
	}
	if _, ok := a.config.Credentials[s.CredentialRef]; !ok {
		return errors.New("unknown credential_ref")
	}
	return nil
}
func (a *App) add(s Server) (Server, error) {
	if s.Port == 0 {
		s.Port = 22
	}
	if err := a.validate(s); err != nil {
		return Server{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, existing := range a.servers {
		if existing.Name == s.Name {
			return Server{}, errors.New("server name already exists")
		}
	}
	s.ID = "srv_" + rand.Text()
	next := append(append([]Server{}, a.servers...), s)
	if err := a.persistServers(next); err != nil {
		return Server{}, err
	}
	return s, nil
}

// deleteServer removes only local configuration; already-started operations continue.
func (a *App) deleteServer(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, s := range a.servers {
		if s.ID == id {
			next := make([]Server, 0, len(a.servers)-1)
			next = append(next, a.servers[:i]...)
			next = append(next, a.servers[i+1:]...)
			return a.persistServers(next)
		}
	}
	return errors.New("server_not_found")
}

// persistServers requires a.mu and updates memory only after the atomic rename.
func (a *App) persistServers(next []Server) error {
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(a.dir, ".servers-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(a.dir, "servers.json")); err != nil {
		return err
	}
	a.servers = next
	return nil
}
func (a *App) lookup(id string) (Server, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.servers {
		if s.ID == id {
			return s, nil
		}
	}
	return Server{}, errors.New("server_not_found")
}
func remotePath(s Server, p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", errors.New("invalid path")
	}
	if !path.IsAbs(p) {
		p = path.Join(s.Workdir, p)
	}
	return path.Clean(p), nil
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
