package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsErrorLine(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"2026-08-12 [INFO] Server started", false},
		{"2026-08-12 [ERROR] Failed to start engine", true},
		{`{"level":"error","msg":"connection refused"}`, true},
		{"panic: runtime error: invalid memory address", true},
		{"[FATAL] Database connection failed", true},
		{"Request processed in 20ms", false},
	}

	for _, tt := range tests {
		got := isErrorLine(tt.line)
		if got != tt.want {
			t.Errorf("isErrorLine(%q) = %v; want %v", tt.line, got, tt.want)
		}
	}
}

func TestReadLastNLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	lines := readLastNLines(logPath, 3)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "line3" || lines[1] != "line4" || lines[2] != "line5" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestCheckErrors(t *testing.T) {
	dir := t.TempDir()
	cleanLog := filepath.Join(dir, "clean.log")
	errLog := filepath.Join(dir, "error.log")

	_ = os.WriteFile(cleanLog, []byte("info line 1\ninfo line 2\n"), 0o644)
	_ = os.WriteFile(errLog, []byte("info line 1\n[ERROR] test error occurred\n"), 0o644)

	logsClean := []LogFileInfo{
		{Tag: "app", Path: cleanLog, Exists: true},
	}
	if code := checkErrors(logsClean); code != 0 {
		t.Errorf("expected exit code 0 for clean logs, got %d", code)
	}

	logsErr := []LogFileInfo{
		{Tag: "app", Path: cleanLog, Exists: true},
		{Tag: "err", Path: errLog, Exists: true},
	}
	if code := checkErrors(logsErr); code != 1 {
		t.Errorf("expected exit code 1 for error logs, got %d", code)
	}
}

func TestDiscoverLogs(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "config.yml")
	dataDir := filepath.Join(dir, "data")

	info := Info{
		Version:           "1.0.0",
		DefaultConfigPath: confPath,
		DefaultDataDir:    dataDir,
	}

	logs := DiscoverLogs(info, confPath, dataDir)
	if len(logs) == 0 {
		t.Fatal("expected at least 1 discovered log path")
	}

	foundApp := false
	for _, l := range logs {
		if strings.Contains(l.Path, "data") && strings.Contains(l.Path, "tokenlive.log") {
			foundApp = true
		}
	}
	if !foundApp {
		t.Errorf("expected data log path in discovered logs, got: %v", logs)
	}
}

// appendLine appends a line to path, failing the test on error.
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// rotateFile renames path -> path+".1" and creates a fresh empty file at path,
// mimicking lumberjack's rotation (rename + recreate, new inode).
func rotateFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("rename %s: %v", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	f.Close()
}

// runFollow starts followLogs in a goroutine writing to buf, returning a
// cancel func and a done channel. The ticker is 250ms, so tests sleep ~400ms
// between mutations to let a tick fire.
func runFollow(t *testing.T, path string) (buf *bytes.Buffer, cancel context.CancelFunc, done chan struct{}) {
	t.Helper()
	buf = &bytes.Buffer{}
	var ctx context.Context
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	targets := []LogFileInfo{{Tag: "app", Path: path, Exists: true}}
	go func() {
		_ = followLogs(ctx, buf, targets, 10, false, false)
		close(done)
	}()
	// Give the goroutine time to open the file and seed initial lines.
	time.Sleep(150 * time.Millisecond)
	return
}

func TestFollowLogs_PlainAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte("seed1\nseed2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf, cancel, done := runFollow(t, path)
	defer cancel()

	appendLine(t, path, "live-A")
	appendLine(t, path, "live-B")
	time.Sleep(500 * time.Millisecond)

	cancel()
	<-done

	out := buf.String()
	for _, want := range []string{"seed1", "seed2", "live-A", "live-B"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// Reproduces the original bug: after lumberjack-style rotation (rename +
// recreate), -f must keep streaming new lines instead of going silent.
func TestFollowLogs_SurvivesRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf, cancel, done := runFollow(t, path)
	defer cancel()

	appendLine(t, path, "pre-rotate")
	time.Sleep(400 * time.Millisecond)

	rotateFile(t, path)
	time.Sleep(400 * time.Millisecond) // let a tick observe the new inode

	appendLine(t, path, "post-rotate-1")
	appendLine(t, path, "post-rotate-2")
	time.Sleep(500 * time.Millisecond)

	cancel()
	<-done

	out := buf.String()
	for _, want := range []string{"seed", "pre-rotate", "post-rotate-1", "post-rotate-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("after rotation, output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestFollowLogs_SurvivesTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf, cancel, done := runFollow(t, path)
	defer cancel()

	appendLine(t, path, "before-trunc")
	time.Sleep(400 * time.Millisecond)

	// Truncate in place (size shrinks below our offset).
	if err := os.WriteFile(path, []byte("fresh-start\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)

	appendLine(t, path, "after-trunc")
	time.Sleep(500 * time.Millisecond)

	cancel()
	<-done

	out := buf.String()
	if !strings.Contains(out, "fresh-start") {
		t.Errorf("output missing truncated content\n--- output ---\n%s", out)
	}
	if !strings.Contains(out, "after-trunc") {
		t.Errorf("output missing post-truncation line\n--- output ---\n%s", out)
	}
}

func TestFollowLogs_SurvivesRemovalAndRecreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf, cancel, done := runFollow(t, path)
	defer cancel()

	appendLine(t, path, "before-rm")
	time.Sleep(400 * time.Millisecond)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)

	// Recreate and write — follow should pick it back up.
	if err := os.WriteFile(path, []byte("reborn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)

	appendLine(t, path, "after-reborn")
	time.Sleep(500 * time.Millisecond)

	cancel()
	<-done

	out := buf.String()
	if !strings.Contains(out, "reborn") {
		t.Errorf("output missing recreated content\n--- output ---\n%s", out)
	}
	if !strings.Contains(out, "after-reborn") {
		t.Errorf("output missing post-recreate line\n--- output ---\n%s", out)
	}
}
