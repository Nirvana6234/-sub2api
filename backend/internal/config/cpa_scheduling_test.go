package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCPASchedulingEnvAndSecretRedaction(t *testing.T) {
	resetViperWithJWTSecret(t)
	token := strings.Repeat("cpa-secret-", 4)
	t.Setenv("CPA_SCHEDULING_SYNC_TOKEN", token)
	t.Setenv("CPA_SCHEDULING_SOURCE_ID", "production-stable")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, token, cfg.CPAScheduling.SyncToken)
	require.Equal(t, "production-stable", cfg.CPAScheduling.SourceID)
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NotContains(t, string(raw), token)
	require.NotContains(t, string(raw), "sync_token")
}

func TestCPASchedulingConfigurationValidation(t *testing.T) {
	require.NoError(t, (CPASchedulingConfig{}).Validate())
	require.Error(t, (CPASchedulingConfig{SyncToken: "short", SourceID: "source"}).Validate())
	require.Error(t, (CPASchedulingConfig{SyncToken: strings.Repeat("x", 32)}).Validate())
	require.NoError(t, (CPASchedulingConfig{SyncToken: strings.Repeat("x", 32), SourceID: "source"}).Validate())
}
