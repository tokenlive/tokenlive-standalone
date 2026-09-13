package bridge

import (
	adminmetrics "github.com/tokenlive/tokenlive-admin/pkg/metrics"
	"github.com/tokenlive/tokenlive-gateway/pkg/filters/outbound"
)

// AdminMetricsSink writes gateway dashboard metrics into the co-hosted admin store.
type AdminMetricsSink struct{}

func (AdminMetricsSink) ReportMetrics(batch outbound.MetricsBatch) {
	for _, m := range batch.Metrics {
		adminmetrics.GlobalStore.Record(toAdminMetric(m))
	}
	adminmetrics.GlobalStore.UpdateCircuitBreakers(batch.OpenEndpoints, batch.OpenServices)
}

func toAdminMetric(m outbound.RequestMetric) adminmetrics.RequestMetric {
	out := adminmetrics.RequestMetric{
		Time:                m.Time,
		Model:               m.Model,
		Provider:            m.Provider,
		Success:             m.Success,
		InputTokens:         m.InputTokens,
		OutputTokens:        m.OutputTokens,
		CachedTokens:        m.CachedTokens,
		CacheCreationTokens: m.CacheCreationTokens,
		Cost:                m.Cost,
		EndpointID:          m.EndpointID,
		TTFTMs:              m.TTFTMs,
		DurationMs:          m.DurationMs,
	}
	if len(m.Attempts) == 0 {
		return out
	}
	out.Attempts = make([]struct {
		EndpointID   string `json:"endpoint_id"`
		Provider     string `json:"provider,omitempty"`
		ProviderCode string `json:"provider_code,omitempty"`
		Success      bool   `json:"success"`
	}, len(m.Attempts))
	for i, a := range m.Attempts {
		out.Attempts[i].EndpointID = a.EndpointID
		out.Attempts[i].Provider = a.Provider
		out.Attempts[i].ProviderCode = a.ProviderCode
		out.Attempts[i].Success = a.Success
	}
	return out
}
