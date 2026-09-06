package bridge

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	adminmetrics "github.com/tokenlive/tokenlive-admin/pkg/metrics"
	"github.com/tokenlive/tokenlive-gateway/pkg/filters/outbound"
)

func TestAdminMetricsSink_ReportMetricsWritesInProcessStore(t *testing.T) {
	original := adminmetrics.GlobalStore
	adminmetrics.GlobalStore = adminmetrics.NewMemoryStore()
	t.Cleanup(func() { adminmetrics.GlobalStore = original })

	now := time.Now().Unix()
	sink := AdminMetricsSink{}
	sink.ReportMetrics(outbound.MetricsBatch{
		Metrics: []outbound.RequestMetric{{
			Time:        now,
			Model:       "gpt-4",
			Provider:    "JoyCode",
			Success:     true,
			InputTokens: 10,
			Attempts: []outbound.AttemptMetric{{
				EndpointID: "ep-1",
				Success:    true,
			}},
		}},
		OpenEndpoints: []string{"ep-open"},
		OpenServices:  []string{"JoyCode:gpt-4"},
	})

	minute := now / 60
	perf := adminmetrics.GlobalStore.GetModelMinutePerf("gpt-4", minute)
	require.Equal(t, int64(1), perf.Success)
	require.ElementsMatch(t, []string{"ep-open"}, adminmetrics.GlobalStore.GetOpenEndpoints())
	require.ElementsMatch(t, []string{"JoyCode:gpt-4"}, adminmetrics.GlobalStore.GetOpenServices())
}
