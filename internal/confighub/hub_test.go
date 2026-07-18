package confighub_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	gwconfig "github.com/tokenlive/tokenlive-gateway/pkg/config"
	"github.com/tokenlive/tokenlive-standalone/internal/confighub"
)

type staticSrc struct {
	snap *confighub.Snapshot
}

func (s *staticSrc) LoadGatewaySnapshot(ctx context.Context) (*confighub.Snapshot, error) {
	return s.snap, nil
}

func TestHub_RefreshAndProvider(t *testing.T) {
	cfg := gwconfig.GatewayConfig{
		Models: map[string]gwconfig.ModelConfig{
			"m1": {RequestTypes: []string{"chat_completion"}},
		},
		Providers: map[string]gwconfig.ProviderConfig{
			"p1": {Protocol: "openai"},
		},
	}
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)

	keys := []gwconfig.HTTPApiKeyItem{{
		APIKey:  "sk-test",
		Status:  1,
		Credits: 100,
	}}
	keyJSON, err := json.Marshal(keys)
	require.NoError(t, err)

	hub := confighub.New(&staticSrc{snap: &confighub.Snapshot{
		ConfigJSON:  cfgJSON,
		APIKeysJSON: keyJSON,
		PoliciesJSON: []byte("[]"),
	}})

	var reloaded string
	hub.OnReload = func(ctx context.Context, kind string) { reloaded = kind }

	require.NoError(t, hub.Refresh(context.Background(), "all"))
	require.Equal(t, "all", reloaded)
	require.Equal(t, uint64(1), hub.Version())

	p := hub.Provider()
	got, err := p.GetConfig(context.Background(), "")
	require.NoError(t, err)
	require.Contains(t, got.Models, "m1")

	item, err := p.GetApiKey(context.Background(), "sk-test")
	require.NoError(t, err)
	require.Equal(t, int64(100), item.Credits)

	bal, err := p.DeductCredits(context.Background(), "sk-test", 10)
	require.NoError(t, err)
	require.Equal(t, int64(90), bal)

	_, err = p.GetApiKey(context.Background(), "missing")
	require.Error(t, err)
}
