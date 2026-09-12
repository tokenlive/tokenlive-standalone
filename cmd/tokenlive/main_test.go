package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tokenlive/tokenlive-admin/pkg/productversion"
)

func TestMainVersionIdentity(t *testing.T) {
	if os.Getenv("TOKENLIVE_MAIN_IDENTITY_HELPER") == "1" {
		version, buildKind = "v1.2.3-host-fixture", "release"
		os.Args = []string{"tokenlive", "-conf", "gateway.yml", "-admin-workdir", ".",
			"-admin-config", "admin.json", "-data-dir", "."}
		main()
		return
	}
	root := t.TempDir()
	// A local empty fixture stops Gateway's upward .env discovery.
	if err := os.WriteFile(filepath.Join(root, ".env"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	gatewayConfig := fmt.Sprintf(`
http: {host: 127.0.0.1, port: %d}
gateway: {config_source: embedded, state_store: memory}
models: {}
log: {mode: console, log_level: error}
`, addr.Port)
	adminConfig := fmt.Sprintf(`{
		"General": {"Version": "v99.0.0-admin-fixture", "DisablePrintConfig": true,
			"DisableSwagger": true, "Root": {"Password": "test-only-password"}},
		"Logger": {"Level": "fatal"},
		"Storage": {"DB": {"Type": "sqlite3", "DSN": %q, "AutoMigrate": true}},
		"Middleware": {
			"Auth": {"SkippedPathPrefixes": ["/api/v1/pub/", "/api/v1/login"]},
			"Casbin": {"Disable": true}
		},
		"Util": {"Captcha": {"Disable": true}, "Prometheus": {"Enable": false}}
	}`, filepath.Join(root, "admin.db"))
	for name, content := range map[string]string{"gateway.yml": gatewayConfig, "admin.json": adminConfig} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	executable := filepath.Join(root, "bin", "tokenlive")
	if err := os.MkdirAll(filepath.Dir(executable), 0755); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.OpenFile(executable, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	writeInstallMarker(t, root, "homebrew\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestMainVersionIdentity$")
	cmd.Dir = root
	cmd.Env = []string{"TOKENLIVE_MAIN_IDENTITY_HELPER=1", "UPDATE_CHECK_ENABLED=false", "OTEL_SDK_DISABLED=true"}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		if err := cmd.Wait(); err != nil {
			t.Errorf("isolated main failed: %v\n%s", err, output.String())
		}
	}()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	baseURL := "http://" + addr.String()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	ready := false
	for !ready {
		select {
		case <-ctx.Done():
			t.Fatal("isolated main did not become ready")
		case <-ticker.C:
			resp, err := client.Get(baseURL + "/api/v1/pub/version")
			if err == nil {
				ready = resp.StatusCode == http.StatusOK
				resp.Body.Close()
			}
		}
	}
	request := func(method, path, token, body string, into any) {
		t.Helper()
		req, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s returned %d", method, path, resp.StatusCode)
		}
		var envelope struct {
			Success bool            `json:"success"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if !envelope.Success {
			t.Fatalf("%s %s was unsuccessful", method, path)
		}
		if into != nil {
			if err := json.Unmarshal(envelope.Data, into); err != nil {
				t.Fatal(err)
			}
		}
	}
	var public struct {
		Version string `json:"version"`
	}
	request("GET", "/api/v1/pub/version", "", "", &public)
	if public.Version != "v1.2.3-host-fixture" {
		t.Fatalf("main public version = %q, not the executable version", public.Version)
	}
	var login struct {
		Token string `json:"access_token"`
	}
	request("POST", "/api/v1/login", "", `{"username":"admin","password":"test-only-password"}`, &login)
	defer request("POST", "/api/v1/current/logout", login.Token, "", nil)
	var current struct {
		Identity productversion.Identity `json:"identity"`
	}
	request("GET", "/api/v1/current/version", login.Token, "", &current)
	if want := (productversion.Identity{
		Edition: "standalone", InstallChannel: "homebrew",
		Build: productversion.Build{Version: "v1.2.3-host-fixture", Kind: "release"},
	}); current.Identity != want {
		t.Fatalf("main current identity = %+v, want %+v", current.Identity, want)
	}
}

func TestMainVersionUsesHostBuild(t *testing.T) {
	if os.Getenv("TOKENLIVE_MAIN_VERSION_HELPER") == "1" {
		version = "v1.2.3-host-fixture"
		if buildKind != "dev" {
			t.Fatalf("local build kind = %q, want dev", buildKind)
		}
		os.Args = []string{"tokenlive", "-version"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainVersionUsesHostBuild$")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"TOKENLIVE_MAIN_VERSION_HELPER=1", "UPDATE_CHECK_ENABLED=false"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version command failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "v1.2.3-host-fixture\n") {
		t.Fatalf("version command did not expose the executable version: %s", out)
	}
}

func TestMain_FailFastNonEmbedded(t *testing.T) {
	if os.Getenv("TOKENLIVE_MAIN_INVALID_HELPER") == "1" {
		os.Args = []string{"tokenlive", "-conf", "bad.yml"}
		main()
		return
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), nil, 0600); err != nil {
		t.Fatal(err)
	}
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

	cmd := exec.Command(os.Args[0], "-test.run=^TestMain_FailFastNonEmbedded$")
	cmd.Dir = dir
	cmd.Env = []string{"TOKENLIVE_MAIN_INVALID_HELPER=1", "UPDATE_CHECK_ENABLED=false"}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success: %s", out)
	}
	// assemble.ValidateAllInOne error text
	if !strings.Contains(string(out), "embedded") {
		t.Fatalf("expected embedded validation error, got: %s", out)
	}
}
