package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 现有部署什么都不配时是主节点，主从通信不启用。
func TestLoadRelayDefaultsToMasterWithoutRelayPort(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, RelayNodeRoleMaster, cfg.Relay.NodeRole)
	require.Empty(t, cfg.Relay.MasterListenAddr)
	require.Empty(t, cfg.Relay.KeyEncryptionKey)
}

func TestLoadRelaySettingsFromEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("NODE_ROLE", "Relay")
	t.Setenv("RELAY_MASTER_LISTEN_ADDR", ":7443")
	t.Setenv("RELAY_KEY_DIR", "/data/relay-keys")
	t.Setenv("RELAY_KEY_ENCRYPTION_KEY_FILE", "/run/secrets/relay-kek")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, RelayNodeRoleRelay, cfg.Relay.NodeRole)
	require.Equal(t, ":7443", cfg.Relay.MasterListenAddr)
	require.Equal(t, "/data/relay-keys", cfg.Relay.KeyDir)
	require.Equal(t, "/run/secrets/relay-kek", cfg.Relay.KeyEncryptionKeyFile)
}

func TestLoadRelayRejectsUnknownRole(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("NODE_ROLE", "worker")
	_, err := Load()
	require.ErrorContains(t, err, "NODE_ROLE")
}

func TestLoadRelayRejectsTwoKeyEncryptionKeySources(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("RELAY_KEY_ENCRYPTION_KEY", "00")
	t.Setenv("RELAY_KEY_ENCRYPTION_KEY_FILE", "/x")
	_, err := Load()
	require.ErrorContains(t, err, "mutually exclusive")
}
