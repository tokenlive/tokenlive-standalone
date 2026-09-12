package assemble_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"github.com/tokenlive/tokenlive-standalone/internal/assemble"
)

func TestVersionUpdatesMenuResources(t *testing.T) {
	type resource struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	type menu struct {
		Code      string     `json:"code"`
		Type      string     `json:"type"`
		Status    string     `json:"status"`
		Resources []resource `json:"resources"`
	}
	for _, name := range []string{"menu.json", "menu_cn.json"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "configs", "admin", name))
			require.NoError(t, err)
			var roots []struct {
				Code     string `json:"code"`
				Children []menu `json:"children"`
			}
			require.NoError(t, json.Unmarshal(data, &roots))
			var matches []menu
			for _, root := range roots {
				if root.Code == "system" {
					for _, child := range root.Children {
						if child.Code == "versionUpdates" {
							matches = append(matches, child)
						}
					}
				}
			}
			require.Len(t, matches, 1, "system.versionUpdates must be available for role authorization")
			require.Equal(t, "button", matches[0].Type)
			require.Equal(t, "enabled", matches[0].Status)
			require.Equal(t, []resource{
				{Method: "GET", Path: "/api/v1/system/updates"},
				{Method: "POST", Path: "/api/v1/system/updates/check"},
			}, matches[0].Resources)
		})
	}
}

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
