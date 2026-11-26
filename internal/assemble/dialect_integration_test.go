package assemble

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"github.com/tokenlive/tokenlive-gateway/pkg/log"
	"go.uber.org/zap"
)

func TestEmbeddedModelDialectLifecycle(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "admin.db")
	adminConfig := fmt.Sprintf(`{
		"General": {"DisablePrintConfig": true, "DisableSwagger": true, "Root": {"Password": "test-only-password"}},
		"Logger": {"Level": "fatal"},
		"Storage": {"DB": {"Type": "sqlite3", "DSN": %q, "AutoMigrate": true}},
		"Middleware": {"Auth": {"SkippedPathPrefixes": ["/api/v1/login"]}, "Casbin": {"Disable": true}},
		"Util": {"Captcha": {"Disable": true}, "Prometheus": {"Enable": false}}
	}`, dbPath)
	require.NoError(t, os.WriteFile(filepath.Join(root, "test.json"), []byte(adminConfig), 0600))

	outgoing := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		outgoing <- string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer upstream.Close()

	gatewayConfig := viper.New()
	gatewayConfig.Set("gateway.config_source", "embedded")
	gatewayConfig.Set("gateway.state_store", "memory")
	gatewayConfig.Set("llm.enable_auth", false)
	gatewayConfig.Set("models", map[string]any{})
	gatewayConfig.Set("pipelines.chat_completion", map[string]any{
		"name": "chat_completion", "request_types": []string{"chat_completion"},
		"invoker": map[string]any{"type": "failover", "routers": []string{"capability", "priority"}},
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	app, err := New(ctx, Options{
		GatewayConf: gatewayConfig, Logger: &log.Logger{Logger: zap.NewNop()},
		AdminWorkDir: root, AdminConfigs: "test.json",
	})
	require.NoError(t, err)
	defer app.Close(context.Background())

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()
	for _, statement := range []string{
		`INSERT INTO model (id, model_name, model_code, space_code, request_types, enabled)
			VALUES ('test-model', 'Test model', 'm', 'default', '["chat_completion"]', 1)`,
		`INSERT INTO provider (id, name, code, protocol, enabled) VALUES ('test-provider', 'Test provider', 'test-provider', 'openai', 1)`,
		`INSERT INTO policy_invocation (id, model_id, name, enabled) VALUES ('test-invocation', 'test-model', 'Test invocation', 1)`,
	} {
		_, err := db.ExecContext(ctx, statement)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO endpoint (id, code, model_id, provider_id, url, protocol, enabled)
		VALUES ('test-endpoint', 'test-endpoint', 'test-model', 'test-provider', ?, 'openai', 1)`, upstream.URL)
	require.NoError(t, err)

	request := func(method, path, token, body string, status int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		app.Engine.ServeHTTP(res, req)
		require.Equal(t, status, res.Code, "%s %s: %s", method, path, res.Body.String())
		return res.Body.Bytes()
	}
	var login struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(request(http.MethodPost, "/api/v1/login", "",
		`{"username":"admin","password":"test-only-password"}`, http.StatusOK), &login))
	require.NotEmpty(t, login.Data.AccessToken)
	request(http.MethodPost, "/api/v1/models/test-model/sync", login.Data.AccessToken, "", http.StatusOK)
	dialectPath := "/api/v1/policy/policy-dialects"
	var created struct {
		Data struct {
			ID      string `json:"id"`
			Enabled int    `json:"enabled"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(request(http.MethodPost, dialectPath, login.Data.AccessToken,
		`{"model_id":"test-model","implementation":"chat_store_unsupported"}`, http.StatusOK), &created))
	require.NotEmpty(t, created.Data.ID)
	require.Zero(t, created.Data.Enabled)

	const requestBody = `{"model":"m","messages":[{"role":"user","content":"hi"}],"store":true}`
	request(http.MethodPost, "/v1/chat/completions", "", requestBody, http.StatusOK)
	require.Len(t, outgoing, 1)
	require.Contains(t, <-outgoing, `"store":true`)

	request(http.MethodPut, dialectPath+"/"+created.Data.ID+"/enabled", login.Data.AccessToken, `{"enabled":1}`, http.StatusOK)
	response := request(http.MethodPost, "/v1/chat/completions", "", requestBody, http.StatusBadRequest)
	require.Contains(t, string(response), "store=true")
	require.Empty(t, outgoing)
	request(http.MethodPost, "/v1/chat/completions", "", strings.ReplaceAll(requestBody, "true", "false"), http.StatusOK)
	require.Len(t, outgoing, 1)
	require.NotContains(t, <-outgoing, `"store"`)

	request(http.MethodDelete, dialectPath+"/"+created.Data.ID, login.Data.AccessToken, "", http.StatusOK)
	request(http.MethodPost, "/v1/chat/completions", "", requestBody, http.StatusOK)
	require.Len(t, outgoing, 1)
	require.Contains(t, <-outgoing, `"store":true`)
}
