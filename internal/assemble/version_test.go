package assemble

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/tokenlive/tokenlive-admin/pkg/productversion"
	"github.com/tokenlive/tokenlive-gateway/pkg/log"
	"go.uber.org/zap"
)

func TestAdminIdentityUsesStandalonePackageVersion(t *testing.T) {
	for _, tc := range []struct {
		version, kind, channel string
		want                   productversion.Identity
	}{
		{"v1.2.3", "release", "homebrew", productversion.Identity{
			Edition: "standalone", InstallChannel: "homebrew",
			Build: productversion.Build{Version: "v1.2.3", Kind: "release"},
		}},
		{"dev", "dev", "unknown", productversion.Identity{
			Edition: "standalone", InstallChannel: "unknown",
			Build: productversion.Build{Version: "dev", Kind: "dev"},
		}},
		{"v1.2.3", "release", "unknown", productversion.Identity{
			Edition: "standalone", InstallChannel: "unknown",
			Build: productversion.Build{Version: "v1.2.3", Kind: "release"},
		}},
	} {
		t.Run(tc.version+"/"+tc.channel, func(t *testing.T) {
			if got := adminIdentity(tc.version, tc.kind, tc.channel); got != tc.want {
				t.Fatalf("identity = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Admin configuration is process-wide. Every case gets a fresh test process,
// temporary SQLite database and explicit environment, not an internal reset hook.
func TestStandaloneVersionAPI(t *testing.T) {
	testCase := os.Getenv("TOKENLIVE_VERSION_TEST_CASE")
	if testCase == "" {
		for _, name := range []string{"homebrew", "tarball", "development"} {
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStandaloneVersionAPI$", "-test.v")
				cmd.Dir = t.TempDir()
				cmd.Env = []string{
					"TOKENLIVE_VERSION_TEST_CASE=" + name,
					"UPDATE_CHECK_ENABLED=false",
					"OTEL_SDK_DISABLED=true",
				}
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("isolated version API test: %v\n%s", err, output)
				}
			})
		}
		return
	}
	t.Setenv("UPDATE_CHECK_ENABLED", "false")
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	fixture := fmt.Sprintf(`{
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
	if err := os.WriteFile(filepath.Join(root, "test.json"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}

	var reports atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/gateway/version" {
			reports.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	v := viper.New()
	v.Set("gateway.config_source", "embedded")
	v.Set("gateway.state_store", "memory")
	v.Set("gateway.admin_url", receiver.URL)
	v.Set("gateway.sync_token", "test-only-sync-token")
	v.Set("models", map[string]any{})

	version, kind, channel := "v1.2.3", "release", "homebrew"
	switch testCase {
	case "tarball":
		channel = "unknown"
	case "development":
		version, kind, channel = "dev", "dev", "unknown"
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, err := New(ctx, Options{
		GatewayConf: v, Logger: &log.Logger{Logger: zap.NewNop()},
		AdminWorkDir: root, AdminConfigs: "test.json",
		Version: version, BuildKind: kind, InstallChannel: channel,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())

	request := func(method, path, token, body string) json.RawMessage {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d: %s", method, path, rec.Code, rec.Body.String())
		}
		var response struct {
			Success bool            `json:"success"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if !response.Success {
			t.Fatalf("%s %s failed: %s", method, path, rec.Body.String())
		}
		return response.Data
	}
	var public struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(request("GET", "/api/v1/pub/version", "", ""), &public); err != nil {
		t.Fatal(err)
	}
	if public.Version != version {
		t.Fatalf("public version = %q, want standalone host version %q (not Admin config version)", public.Version, version)
	}
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(request("POST", "/api/v1/login", "", `{"username":"admin","password":"test-only-password"}`), &login); err != nil {
		t.Fatal(err)
	}
	if login.AccessToken == "" {
		t.Fatal("login did not issue an access token")
	}
	defer request("POST", "/api/v1/current/logout", login.AccessToken, "")
	var summary struct {
		Identity productversion.Identity `json:"identity"`
		Gateway  struct {
			Status string            `json:"status"`
			Groups []json.RawMessage `json:"groups"`
		} `json:"gateway"`
	}
	if err := json.Unmarshal(request("GET", "/api/v1/current/version", login.AccessToken, ""), &summary); err != nil {
		t.Fatal(err)
	}
	if want := (productversion.Identity{
		Edition: "standalone", InstallChannel: channel,
		Build: productversion.Build{Version: version, Kind: kind},
	}); summary.Identity != want {
		t.Fatalf("current identity = %+v, want %+v", summary.Identity, want)
	}
	if summary.Gateway.Status != "not_applicable" || len(summary.Gateway.Groups) != 0 {
		t.Fatalf("standalone must not display professional Gateway nodes: %+v", summary.Gateway)
	}
	var updates struct {
		Enabled    bool `json:"enabled"`
		Components []struct {
			Component string `json:"component"`
			Current   string `json:"current"`
			State     string `json:"state"`
		} `json:"components"`
	}
	if err := json.Unmarshal(request("GET", "/api/v1/system/updates", login.AccessToken, ""), &updates); err != nil {
		t.Fatal(err)
	}
	if updates.Enabled || len(updates.Components) != 1 ||
		updates.Components[0].Component != "standalone" ||
		updates.Components[0].Current != version || updates.Components[0].State != "disabled" {
		t.Fatalf("expected only the disabled standalone component: %+v", updates)
	}
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	if reports.Load() != 0 {
		t.Fatalf("embedded Gateway sent %d professional version reports", reports.Load())
	}
}
