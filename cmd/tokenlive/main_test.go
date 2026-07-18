package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain_FailFastNonEmbedded(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(cfg, []byte(`
http:
  host: 127.0.0.1
  port: 2525
gateway:
  config_source: local
log:
  log_level: error
  mode: console
  encoding: console
models:
  m:
    request_types: [chat_completion]
    endpoints:
      - provider: p
        url: http://127.0.0.1:9
providers:
  p:
    protocol: openai
    api_key: x
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", ".", "-conf", cfg)
	cmd.Dir = filepath.Join(findModuleRoot(t), "cmd", "tokenlive")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success: %s", out)
	}
	// assemble.ValidateAllInOne error text
	if !contains(string(out), "embedded") {
		t.Fatalf("expected embedded validation error, got: %s", out)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
