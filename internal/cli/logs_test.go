package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
