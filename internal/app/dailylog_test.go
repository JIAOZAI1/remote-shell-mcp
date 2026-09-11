package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDailyLogRotationAndRetention(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 8, 23, 59, 0, 0, time.FixedZone("local", -5*60*60))
	w, err := openDailyLog(dir, LogConfig{RetentionDays: 2}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	write := func(text string) {
		t.Helper()
		if _, err := w.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	write("first\n")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w, err = openDailyLog(dir, LogConfig{RetentionDays: 2}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	write("second\n")
	p := filepath.Join(dir, "logs", "remote-shell-mcp-2026-03-08.jsonl")
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "first\nsecond\n" {
		t.Fatalf("append: %q %v", b, err)
	}
	unrelated := filepath.Join(dir, "logs", "notes.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(0, 0, 1)
	write("day two\n")
	if _, err := os.Stat(p); err != nil {
		t.Fatal("deleted within retention", err)
	}
	now = now.AddDate(0, 0, 1)
	write("day three\n")
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old file remains: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("closed")); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}

func TestDailyLogStartupCleanupAndConfig(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	old := filepath.Join(dir, "remote-shell-mcp-2026-01-01.jsonl")
	keep := filepath.Join(dir, "remote-shell-mcp-2026-01-02.jsonl")
	for _, p := range []string{old, keep} {
		if err := os.WriteFile(p, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	w, err := openDailyLog("ignored", LogConfig{Directory: dir}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.days != 30 {
		t.Fatal(w.days)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("startup cleanup failed", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal(err)
	}
	for _, days := range []int{-1, 36501} {
		if _, err := openDailyLog(dir, LogConfig{RetentionDays: days}, time.Now); err == nil {
			t.Fatal("invalid retention accepted")
		}
	}
	if _, err := openDailyLog(dir, LogConfig{Directory: keep}, time.Now); err == nil {
		t.Fatal("file accepted as directory")
	}
}

func TestDailyLogConcurrentWrites(t *testing.T) {
	w, err := openDailyLog(t.TempDir(), LogConfig{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := w.Write([]byte("entry\n")); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(w.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "entry\n") != 400 {
		t.Fatal("lost or interleaved writes")
	}
}

func TestDailyLogRotationFailureRecovery(t *testing.T) {
	now := time.Date(2026, 1, 1, 23, 59, 0, 0, time.UTC)
	w, err := openDailyLog(t.TempDir(), LogConfig{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	now = now.AddDate(0, 0, 1)
	blocked := filepath.Join(w.dir, "remote-shell-mcp-2026-01-02.jsonl")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("failed\n")); err == nil {
		t.Fatal("expected rotation failure")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("recovered\n")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(blocked)
	if err != nil || string(b) != "recovered\n" {
		t.Fatalf("recovery: %q %v", b, err)
	}
}

func TestLogFallback(t *testing.T) {
	w, err := openDailyLog(t.TempDir(), LogConfig{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	fallback := logFallback{primary: w, fallback: &stderr}
	if _, err := fmt.Fprintln(fallback, "entry"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "local log write failed") || !strings.Contains(stderr.String(), "entry") {
		t.Fatal(stderr.String())
	}
}
