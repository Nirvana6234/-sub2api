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

// 部署文件（deploy/docker-compose.relay-node.yml）里给从节点用的环境变量都要能读到：指纹用逗号分隔。
func TestLoadRelayNodeSettingsFromDeployEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("NODE_ROLE", "relay")
	t.Setenv("RELAY_NODE_MASTER_ADDR", "master.example.com:7443")
	t.Setenv("RELAY_NODE_ROOT_FINGERPRINTS", "aa11,bb22")
	t.Setenv("RELAY_NODE_MASTER_URL", "https://api.example.com")
	t.Setenv("RELAY_NODE_DISPLAY_NAME", "relay-1")
	t.Setenv("RELAY_NODE_TLS_ADDR", ":8443")
	t.Setenv("RELAY_NODE_ACME_EMAIL", "ops@example.com")
	t.Setenv("RELAY_NODE_CERT_FILE", "/app/data/tls/fullchain.pem")
	t.Setenv("RELAY_NODE_KEY_FILE", "/app/data/tls/privkey.pem")
	t.Setenv("SERVER_IDLE_TIMEOUT", "90")
	t.Setenv("SERVER_MAX_HEADER_BYTES", "32768")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "master.example.com:7443", cfg.Relay.NodeMasterAddr)
	require.Equal(t, []string{"aa11", "bb22"}, cfg.Relay.NodeRootFingerprints)
	require.Equal(t, "https://api.example.com", cfg.Relay.NodeMasterURL)
	require.Equal(t, "relay-1", cfg.Relay.NodeDisplayName)
	require.Equal(t, ":8443", cfg.Relay.NodeTLSAddr)
	require.Equal(t, "ops@example.com", cfg.Relay.NodeACMEEmail)
	require.Equal(t, "/app/data/tls/fullchain.pem", cfg.Relay.NodeCertFile)
	require.Equal(t, "/app/data/tls/privkey.pem", cfg.Relay.NodeKeyFile)
	require.Equal(t, 90, cfg.Server.IdleTimeout)
	require.Equal(t, 32768, cfg.Server.MaxHeaderBytes)
}
