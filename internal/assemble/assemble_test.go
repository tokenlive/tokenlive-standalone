package assemble_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"github.com/tokenlive/tokenlive-standalone/internal/assemble"
)

func TestValidateAllInOne(t *testing.T) {
	v := viper.New()
	require.Error(t, assemble.ValidateAllInOne(v))

	v.Set("gateway.config_source", "http")
	require.Error(t, assemble.ValidateAllInOne(v))

	v.Set("gateway.config_source", "embedded")
	require.NoError(t, assemble.ValidateAllInOne(v))
}

func TestStandalonePipelinesCalculateCostBeforeCollectingMetrics(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, configName := range []string{"all-in-one.example.yml", "brew.yml", "linux.yml"} {
		t.Run(configName, func(t *testing.T) {
			v := viper.New()
			v.SetConfigFile(filepath.Join(root, "config", configName))
			require.NoError(t, v.ReadInConfig())

			for _, pipeline := range []string{"chat_completion", "responses", "messages"} {
				filters := v.GetStringSlice("pipelines." + pipeline + ".outbound_filters")
				settlementIndex := slices.Index(filters, "token_settlement")
				collectorIndex := slices.Index(filters, "status_collector")
				require.NotEqual(t, -1, settlementIndex, "%s pipeline %s must calculate cost", configName, pipeline)
				require.NotEqual(t, -1, collectorIndex, "%s pipeline %s must collect metrics", configName, pipeline)
				require.Less(t, settlementIndex, collectorIndex, "%s pipeline %s must settle before collection", configName, pipeline)
			}
		})
	}
}
