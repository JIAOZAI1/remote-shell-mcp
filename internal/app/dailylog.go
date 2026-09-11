package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogConfig controls local daily JSON logs. Relative directories use the config directory.
type LogConfig struct {
	Directory     string `json:"directory,omitempty"`
	RetentionDays int    `json:"retention_days,omitempty"`
}

// dailyLog serializes writes and rotates lazily on the first write of each local day.
type dailyLog struct {
	mu     sync.Mutex
	dir    string
	days   int
	now    func() time.Time
	file   *os.File
	day    string
	closed bool
}

func openDailyLog(base string, c LogConfig, now func() time.Time) (*dailyLog, error) {
	if c.RetentionDays == 0 {
		c.RetentionDays = 30
	}
	if c.RetentionDays < 1 || c.RetentionDays > 36500 {
		return nil, errors.New("logging.retention_days must be between 1 and 36500 (0 uses 30)")
	}
	if c.Directory == "" {
		c.Directory = "logs"
	}
	if !filepath.IsAbs(c.Directory) {
		c.Directory = filepath.Join(base, c.Directory)
	}
	if err := os.MkdirAll(c.Directory, 0700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	w := &dailyLog{dir: c.Directory, days: c.RetentionDays, now: now}
	if err := w.rotate(now()); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *dailyLog) rotate(now time.Time) error {
	day := now.Format(time.DateOnly)
	if w.file != nil && w.day == day {
		return nil
	}
	// Close before cleanup so expired files can also be removed on Windows.
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		if err != nil {
			return fmt.Errorf("close daily log: %w", err)
		}
	}
	if err := w.cleanup(now); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(w.dir, "remote-shell-mcp-"+day+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open daily log: %w", err)
	}
	w.file, w.day = f, day
	return nil
}

func (w *dailyLog) cleanup(now time.Time) error {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return fmt.Errorf("list daily logs: %w", err)
	}
	cutoff := now.AddDate(0, 0, 1-w.days).Format(time.DateOnly)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		date := strings.TrimSuffix(strings.TrimPrefix(name, "remote-shell-mcp-"), ".jsonl")
		if name != "remote-shell-mcp-"+date+".jsonl" {
			continue
		}
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			continue
		}
		if date < cutoff {
			if err := os.Remove(filepath.Join(w.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove expired daily log: %w", err)
			}
		}
	}
	return nil
}

func (w *dailyLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if err := w.rotate(w.now()); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

func (w *dailyLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// logFallback makes write failures visible: slog itself discards writer errors.
// Diagnostics are fixed strings to avoid exposing paths or sensitive content.
type logFallback struct {
	primary  io.Writer
	fallback io.Writer
}

func (w logFallback) Write(p []byte) (int, error) {
	n, err := w.primary.Write(p)
	if err == nil && n == len(p) {
		return n, nil
	}
	if _, fallbackErr := io.WriteString(w.fallback, "remote-shell-mcp: local log write failed; falling back to stderr\n"); fallbackErr != nil {
		return 0, fallbackErr
	}
	return w.fallback.Write(p)
}
