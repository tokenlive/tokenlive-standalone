package assemble_test

import (
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
