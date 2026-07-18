package confighub

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	gwconfig "github.com/tokenlive/tokenlive-gateway/pkg/config"
)

// SnapshotSource loads gateway runtime data from admin (in-process).
type SnapshotSource interface {
	LoadGatewaySnapshot(ctx context.Context) (*Snapshot, error)
}

// Snapshot holds JSON blobs from admin export (gateway /api/v1/gateway/* shape).
type Snapshot struct {
	ConfigJSON   []byte
	PoliciesJSON []byte
	APIKeysJSON  []byte
}

// Hub is an in-process cache of gateway config/policies/apikeys and a GatewayProvider.
type Hub struct {
	src SnapshotSource

	mu       sync.RWMutex
	config   *gwconfig.GatewayConfig
	policies []gwconfig.HTTPPolicyItem
	apiKeys  map[string]*gwconfig.HTTPApiKeyItem
	version  atomic.Uint64

	// OnReload is called after a successful Refresh (host applies Engine update).
	OnReload func(ctx context.Context, kind string)
}

// New creates a Hub backed by src.
func New(src SnapshotSource) *Hub {
	return &Hub{
		src:     src,
		apiKeys: make(map[string]*gwconfig.HTTPApiKeyItem),
	}
}

// Provider returns a GatewayProvider view of the hub.
func (h *Hub) Provider() gwconfig.GatewayProvider {
	return &provider{hub: h}
}

// Version returns the monotonic snapshot version.
func (h *Hub) Version() uint64 {
	return h.version.Load()
}

// GatewayConfig returns the cached routing config (may be nil before first Refresh).
func (h *Hub) GatewayConfig() *gwconfig.GatewayConfig {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config
}

// Refresh loads a full snapshot from the source and updates the cache.
// kind is passed to OnReload: endpoints | policies | apikeys | all
func (h *Hub) Refresh(ctx context.Context, kind string) error {
	if h.src == nil {
		return fmt.Errorf("confighub: no snapshot source")
	}
	snap, err := h.src.LoadGatewaySnapshot(ctx)
	if err != nil {
		return err
	}
	if err := h.applySnapshot(snap); err != nil {
		return err
	}
	h.version.Add(1)
	if h.OnReload != nil {
		h.OnReload(ctx, kind)
	}
	return nil
}

func (h *Hub) applySnapshot(snap *Snapshot) error {
	if snap == nil {
		return fmt.Errorf("confighub: nil snapshot")
	}

	var cfg gwconfig.GatewayConfig
	if len(snap.ConfigJSON) > 0 {
		if err := json.Unmarshal(snap.ConfigJSON, &cfg); err != nil {
			return fmt.Errorf("confighub: unmarshal config: %w", err)
		}
	}

	var policies []gwconfig.HTTPPolicyItem
	if len(snap.PoliciesJSON) > 0 {
		if err := json.Unmarshal(snap.PoliciesJSON, &policies); err != nil {
			return fmt.Errorf("confighub: unmarshal policies: %w", err)
		}
	}

	var keyList []gwconfig.HTTPApiKeyItem
	if len(snap.APIKeysJSON) > 0 {
		if err := json.Unmarshal(snap.APIKeysJSON, &keyList); err != nil {
			return fmt.Errorf("confighub: unmarshal apikeys: %w", err)
		}
	}
	keys := make(map[string]*gwconfig.HTTPApiKeyItem, len(keyList))
	for i := range keyList {
		item := keyList[i]
		if item.APIKey != "" {
			keys[item.APIKey] = &item
		}
	}

	h.mu.Lock()
	h.config = &cfg
	h.policies = policies
	h.apiKeys = keys
	h.mu.Unlock()
	return nil
}

type provider struct {
	hub *Hub
}

func (p *provider) GetConfig(ctx context.Context, modelCode string) (*gwconfig.GatewayConfig, error) {
	p.hub.mu.RLock()
	defer p.hub.mu.RUnlock()
	if p.hub.config == nil {
		return nil, fmt.Errorf("embedded config not loaded yet")
	}
	return p.hub.config, nil
}

func (p *provider) GetPolicies(ctx context.Context, modelCode, userID, tenantCode string) ([]gwconfig.HTTPPolicyItem, error) {
	p.hub.mu.RLock()
	defer p.hub.mu.RUnlock()
	return p.hub.policies, nil
}

func (p *provider) GetApiKey(ctx context.Context, apiKey string) (*gwconfig.HTTPApiKeyItem, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api key cannot be empty")
	}
	p.hub.mu.RLock()
	defer p.hub.mu.RUnlock()
	item, ok := p.hub.apiKeys[apiKey]
	if !ok {
		return nil, fmt.Errorf("api key not found")
	}
	return item, nil
}

func (p *provider) GetUserModels(ctx context.Context, userID string) ([]string, error) {
	return []string{"*"}, nil
}

func (p *provider) GetTenantModels(ctx context.Context, tenantCode string) ([]string, error) {
	return []string{"*"}, nil
}

func (p *provider) DeductCredits(ctx context.Context, apiKey string, credits int64) (int64, error) {
	p.hub.mu.Lock()
	defer p.hub.mu.Unlock()
	item, ok := p.hub.apiKeys[apiKey]
	if !ok {
		return 0, fmt.Errorf("api key not found")
	}
	if item.Credits == -1 {
		return -1, nil
	}
	item.Credits -= credits
	return item.Credits, nil
}
