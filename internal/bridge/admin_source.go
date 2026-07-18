package bridge

import (
	"context"

	"github.com/tokenlive/tokenlive-admin/adminapp"
	"github.com/tokenlive/tokenlive-standalone/internal/confighub"
)

// AdminSnapshotSource adapts adminapp.App to confighub.SnapshotSource.
type AdminSnapshotSource struct {
	Admin *adminapp.App
}

func (s *AdminSnapshotSource) LoadGatewaySnapshot(ctx context.Context) (*confighub.Snapshot, error) {
	if s == nil || s.Admin == nil {
		return nil, context.Canceled
	}
	snap, err := s.Admin.LoadGatewaySnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &confighub.Snapshot{
		ConfigJSON:   snap.ConfigJSON,
		PoliciesJSON: snap.PoliciesJSON,
		APIKeysJSON:  snap.APIKeysJSON,
	}, nil
}
