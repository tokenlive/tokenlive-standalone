package assemble_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tokenlive/tokenlive-admin/adminapp"
	"github.com/tokenlive/tokenlive-admin/pkg/productversion"
	"github.com/tokenlive/tokenlive-gateway/pkg/versionreport"
)

// This package intentionally uses only public Admin/Gateway packages. A child
// process per case isolates Admin's one-shot config and background module globals.
// Removing the deployment-token bypass, breaking either wire payload, ignoring
// TTL, refetching on reads, or tying reports to external checks breaks this test.
func TestGatewaySenderAdminIntegration(t *testing.T) {
	if mode := os.Getenv("TOKENLIVE_VERSION_INTEGRATION_CHILD"); mode != "" {
		runGatewaySenderAdminIntegration(t, mode == "enabled")
		return
	}
	for _, mode := range []string{"enabled", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGatewaySenderAdminIntegration$", "-test.v")
			cmd.Dir = t.TempDir()
			// Do not inherit credentials or service configuration from the caller.
			cmd.Env = []string{"TOKENLIVE_VERSION_INTEGRATION_CHILD=" + mode, "GIN_MODE=release"}
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "isolated integration process:\n%s", output)
			t.Logf("%s", output)
		})
	}
}

type controlledReleaseTransport struct {
	adminCalls   atomic.Int32
	gatewayCalls atomic.Int32
}

func (r *controlledReleaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "api.github.com" {
		return nil, fmt.Errorf("unexpected outbound request: %s %s", req.Method, req.URL)
	}
	switch req.URL.Path {
	case "/repos/tokenlive/tokenlive-admin/releases/latest":
		r.adminCalls.Add(1)
	case "/repos/tokenlive/tokenlive-gateway/releases/latest":
		r.gatewayCalls.Add(1)
	default:
		return nil, fmt.Errorf("unexpected release path: %s", req.URL.Path)
	}
	if req.Header.Get("Authorization") != "" || req.Header.Get("X-Sync-Token") != "" {
		return nil, fmt.Errorf("internal credentials reached release source")
	}
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header), Request: req,
		Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.2.4","draft":false,"prerelease":false}`)),
	}, nil
}

type versionGroup struct {
	Version   string `json:"version"`
	BuildKind string `json:"build_kind"`
	Count     int    `json:"count"`
}

type versionSummary struct {
	Identity productversion.Identity `json:"identity"`
	Gateway  struct {
		Status string         `json:"status"`
		Scope  string         `json:"scope"`
		Groups []versionGroup `json:"groups"`
	} `json:"gateway"`
	CanManage bool `json:"can_manage_updates"`
}

type versionUpdates struct {
	Enabled    bool `json:"enabled"`
	Components []struct {
		Component string `json:"component"`
		Current   string `json:"current"`
		Latest    string `json:"latest"`
		State     string `json:"state"`
		Count     int    `json:"count"`
		Source    struct {
			Status string `json:"status"`
		} `json:"source"`
	} `json:"components"`
}

func runGatewaySenderAdminIntegration(t *testing.T, enabled bool) {
	t.Setenv("UPDATE_CHECK_ENABLED", strconv.FormatBool(enabled))
	t.Setenv("UPDATE_CHECK_INTERVAL_SECONDS", "21600")
	t.Setenv("GATEWAY_SYNC_TOKEN", "isolated-integration-sync-token")
	t.Setenv("GATEWAY_VERSION_NAMESPACE", "integration")
	redisServer := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	releases := &controlledReleaseTransport{}
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: releases, Timeout: 5 * time.Second}
	t.Cleanup(func() { http.DefaultClient = originalClient })
	dir := t.TempDir()
	config := map[string]any{
		"General": map[string]any{
			"DisablePrintConfig": true, "DisableSwagger": true,
			"Root": map[string]any{"ID": "integration-root", "Username": "integration-admin", "Password": "integration-password"},
		},
		"Logger": map[string]any{"Level": "fatal"},
		"Storage": map[string]any{
			"DB":         map[string]any{"Type": "sqlite3", "DSN": filepath.Join(dir, "admin.db"), "AutoMigrate": true},
			"Cache":      map[string]any{"Type": "memory", "Redis": map[string]any{"Addr": redisServer.Addr()}},
			"EventQueue": map[string]any{"Type": "disabled"},
		},
		"Middleware": map[string]any{
			"Auth":   map[string]any{"SigningKey": "isolated-integration-signing-key", "SkippedPathPrefixes": []string{"/api/v1/login"}},
			"Casbin": map[string]any{"Disable": true},
		},
		"Util": map[string]any{"Captcha": map[string]any{"Disable": true}, "Prometheus": map[string]any{"Enable": false}},
	}
	data, err := json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.json"), data, 0600))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	identity := productversion.Identity{
		Edition: "professional", InstallChannel: "release",
		Build: productversion.Build{Version: "v1.2.4", Kind: "release"},
	}
	app, err := adminapp.New(ctx, adminapp.Options{
		WorkDir: dir, Configs: "test.json", Identity: &identity, DisableStatic: true, DisableSwagger: true,
	})
	require.NoError(t, err)
	defer func() {
		cancel()
		require.NoError(t, app.Shutdown(context.Background()))
	}()
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	client := server.Client()
	var login struct {
		AccessToken string `json:"access_token"`
	}
	versionRequest(t, client, server.URL, http.MethodPost, "/api/v1/login", "",
		[]byte(`{"username":"integration-admin","password":"integration-password"}`), http.StatusOK, &login)
	require.NotEmpty(t, login.AccessToken)
	token := login.AccessToken
	var updates versionUpdates
	if enabled {
		require.Eventually(t, func() bool {
			versionRequest(t, client, server.URL, http.MethodGet, "/api/v1/system/updates", token, nil, http.StatusOK, &updates)
			return len(updates.Components) == 2 && updates.Components[0].State == "current" &&
				updates.Components[1].Source.Status == "ready"
		}, 5*time.Second, 10*time.Millisecond, "startup source check did not finish")
		require.EqualValues(t, 1, releases.adminCalls.Load())
		require.EqualValues(t, 1, releases.gatewayCalls.Load())
	}

	nodes := []versionreport.Node{
		{SchemaVersion: 1, Namespace: "integration", InstanceID: "11111111-1111-4111-8111-111111111111", Version: "v1.2.3", BuildKind: "release"},
		{SchemaVersion: 1, Namespace: "integration", InstanceID: "22222222-2222-4222-8222-222222222222", Version: "v1.2.3", BuildKind: "release"},
		{SchemaVersion: 1, Namespace: "integration", InstanceID: "33333333-3333-4333-8333-333333333333", Version: "v1.2.4", BuildKind: "release"},
	}
	httpSender := versionreport.NewHTTPSender(client, server.URL, "isolated-integration-sync-token")
	redisSender := versionreport.NewRedisSender(rdb)
	require.NotNil(t, httpSender)
	require.NotNil(t, redisSender)
	// Both real transports converge on the same deployed namespace. Reporting a
	// node through both channels must update its record rather than double-count.
	require.NoError(t, httpSender.Send(ctx, nodes[0]))
	require.NoError(t, redisSender.Send(ctx, nodes[1]))
	require.NoError(t, httpSender.Send(ctx, nodes[2]))
	require.NoError(t, redisSender.Send(ctx, nodes[0]))
	for _, node := range nodes {
		key := "tokenlive:gateway-versions:integration:" + node.InstanceID
		require.Equal(t, 3*time.Minute, redisServer.TTL(key))
		t.Cleanup(func() { require.NoError(t, rdb.Del(context.Background(), key).Err()) })
	}
	rejected := nodes[0]
	rejected.InstanceID = "44444444-4444-4444-8444-444444444444"
	badSender := versionreport.NewHTTPSender(client, server.URL, "wrong-token")
	require.ErrorContains(t, badSender.Send(ctx, rejected), "HTTP 401")
	require.False(t, redisServer.Exists("tokenlive:gateway-versions:integration:"+rejected.InstanceID))
	var summary versionSummary
	versionRequest(t, client, server.URL, http.MethodGet, "/api/v1/current/version", token, nil, http.StatusOK, &summary)
	require.Equal(t, identity, summary.Identity)
	require.True(t, summary.CanManage)
	require.Equal(t, "observed", summary.Gateway.Status)
	require.Equal(t, "shared", summary.Gateway.Scope)
	require.Equal(t, []versionGroup{{"v1.2.3", "release", 2}, {"v1.2.4", "release", 1}}, summary.Gateway.Groups)
	versionRequest(t, client, server.URL, http.MethodGet, "/api/v1/system/updates", token, nil, http.StatusOK, &updates)
	require.Equal(t, enabled, updates.Enabled)
	require.Len(t, updates.Components, 3)
	require.Equal(t, "gateway", updates.Components[1].Component)
	require.Equal(t, "v1.2.3", updates.Components[1].Current)
	require.Equal(t, 2, updates.Components[1].Count)
	require.Equal(t, "v1.2.4", updates.Components[2].Current)
	require.Equal(t, 1, updates.Components[2].Count)
	if enabled {
		require.Equal(t, "available", updates.Components[1].State)
		require.Equal(t, "current", updates.Components[2].State)
	} else {
		for _, component := range updates.Components {
			require.Equal(t, "disabled", component.State)
		}
		versionRequest(t, client, server.URL, http.MethodPost, "/api/v1/system/updates/check", token, nil, http.StatusConflict, &updates)
		require.False(t, updates.Enabled)
	}

	redisServer.FastForward(3 * time.Minute)
	versionRequest(t, client, server.URL, http.MethodGet, "/api/v1/current/version", token, nil, http.StatusOK, &summary)
	require.Equal(t, "unknown", summary.Gateway.Status)
	require.Empty(t, summary.Gateway.Groups)
	versionRequest(t, client, server.URL, http.MethodGet, "/api/v1/system/updates", token, nil, http.StatusOK, &updates)
	require.Len(t, updates.Components, 2)
	require.Equal(t, "gateway", updates.Components[1].Component)
	require.Equal(t, "unknown", updates.Components[1].Current)
	require.Empty(t, updates.Components[1].Latest)
	require.Equal(t, 0, updates.Components[1].Count)
	if enabled {
		require.Equal(t, "unknown", updates.Components[1].State)
		require.EqualValues(t, 1, releases.adminCalls.Load())
		require.EqualValues(t, 1, releases.gatewayCalls.Load())
	} else {
		require.Equal(t, "disabled", updates.Components[1].State)
		require.EqualValues(t, 0, releases.adminCalls.Load())
		require.EqualValues(t, 0, releases.gatewayCalls.Load())
	}
	require.NoError(t, rdb.Ping(ctx).Err(), "sender must not close the borrowed Redis client")
}

func versionRequest[T any](t *testing.T, client *http.Client, base, method, path, token string, body []byte, status int, out *T) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, base+path, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, status, resp.StatusCode, "%s %s: %s", method, path, data)
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(data, &envelope))
	require.Equal(t, status < 400, envelope.Success)
	// Decode a fresh value: omitted count/latest fields are zero, not values
	// retained in an earlier response's reused slice elements.
	var decoded T
	require.NoError(t, json.Unmarshal(envelope.Data, &decoded))
	*out = decoded
	// Public version surfaces must never leak report instance identifiers.
	if strings.Contains(path, "version") || strings.Contains(path, "updates") {
		require.NotContains(t, string(data), "instance_id")
		require.NotContains(t, string(data), "11111111-1111-4111-8111-111111111111")
	}
}
