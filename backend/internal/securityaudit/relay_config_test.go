package securityaudit

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// 主节点下发给从节点的提示词审计配置：生效配置（含解密后的凭据）和降级状态；从节点照它得出同样的生效模式。
func TestRelayPromptConfigRoundTrip(t *testing.T) {
	cfg := ActiveConfig{RiskControlEnabled: true, Enabled: true, BlockingEnabled: true, ConfigVersion: 3,
		Endpoints: []ActiveEndpoint{{ID: "g", Token: "decrypted-token", Enabled: true}}}
	svc := NewPromptServiceWith(&fakeConfigStore{cfg: cfg, active: true}, nil, nil, nil, nil)
	relay := svc.RelayConfig()
	raw, err := json.Marshal(relay)
	require.NoError(t, err)
	require.Contains(t, string(raw), "decrypted-token", "nodes need the credential to call the guard themselves")

	var got RelayPromptConfig
	require.NoError(t, json.Unmarshal(raw, &got))
	store := NewRelayConfigStore(func() (RelayPromptConfig, bool) { return got, true })
	require.Equal(t, svc.EffectiveMode(), store.EffectiveMode())
	active, ok := store.Active()
	require.True(t, ok)
	require.Equal(t, int64(3), active.ConfigVersion)

	require.Equal(t, ModeOff, NewRelayConfigStore(func() (RelayPromptConfig, bool) { return RelayPromptConfig{}, true }).EffectiveMode())
	require.Equal(t, ModeBlocking, NewRelayConfigStore(func() (RelayPromptConfig, bool) { return RelayPromptConfig{Degraded: true}, true }).EffectiveMode())
}
