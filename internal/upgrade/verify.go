package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// HealthClient performs the local success handshake against this instance's
// own /health endpoint. A bare HTTP 200 is not success (research §4): the
// response must report the target version and an executable that resolves
// inside the target keg of this very installation.
type HealthClient struct {
	Client *http.Client
}

func NewHealthClient() *HealthClient {
	return &HealthClient{Client: &http.Client{Timeout: 3 * time.Second}}
}

type healthReport struct {
	Status         string `json:"status"`
	Mode           string `json:"mode"`
	Version        string `json:"version"`
	InstallChannel string `json:"install_channel"`
	Executable     string `json:"executable"`
}

// Check returns (true, "") when the service is provably the confirmed target.
// Every mismatch returns (false, reason) so the worker can keep waiting or
// record needs_attention at budget end. kegVersion names the Cellar directory
// the confirmed Formula installs into. servicePID is the launchd PID observed
// after restart; it must differ from the PID captured before stop, otherwise
// a still-running old process can report the target version and pass.
func (h *HealthClient) Check(ctx context.Context, port int, targetVersion, kegVersion, prefix, channel string, servicePID, previousPID int) (bool, string) {
	if port <= 0 {
		return false, "unknown_port"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/health", port), nil)
	if err != nil {
		return false, "bad_request"
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return false, "unreachable"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, "status_" + fmt.Sprint(resp.StatusCode)
	}
	var report healthReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return false, "bad_body"
	}
	if report.Status != "ok" {
		return false, "not_ok"
	}
	if normalizeVersion(report.Version) != normalizeVersion(targetVersion) {
		return false, "version_" + sanitize(report.Version)
	}
	if channel != "" && report.InstallChannel != channel {
		return false, "channel_mismatch"
	}
	if servicePID <= 0 {
		return false, "no_service_pid"
	}
	if previousPID > 0 && servicePID == previousPID {
		return false, "service_pid_unchanged"
	}
	// The binary must be the freshly installed keg of this install, not some
	// stale path or a second prefix pretending to be us.
	if report.Executable == "" {
		return false, "no_executable"
	}
	resolved, err := filepath.EvalSymlinks(report.Executable)
	if err != nil {
		return false, "executable_unresolved"
	}
	kegRoot := filepath.Join(prefix, "Cellar", "tokenlive", kegVersion)
	if resolved != kegRoot+"/bin/tokenlive" && !strings.HasPrefix(resolved, kegRoot+string(filepath.Separator)) {
		return false, "executable_mismatch"
	}
	return true, ""
}

// normalizeVersion strips the display-only leading v so a running binary
// reporting 1.2.0 matches the canonical candidate v1.2.0.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return -1
		}
		return r
	}, s)
}
