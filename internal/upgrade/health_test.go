package upgrade

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestHealthServer serves the /health payload the worker's handshake
// consumes. Tests point task.Port at the returned server.
func newTestHealthServer(t *testing.T, version, executable string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":          "ok",
			"mode":            "all-in-one",
			"version":         version,
			"install_channel": "homebrew",
			"executable":      executable,
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func portOf(t *testing.T, server *httptest.Server) int {
	t.Helper()
	var port int
	_, err := fmt.Sscanf(server.URL, "http://127.0.0.1:%d", &port)
	if err != nil {
		t.Fatal(err)
	}
	return port
}
