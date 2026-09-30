package master

import (
	"context"
	"crypto/ecdh"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 加密下发清单只放转发功能要用的配置（设计 6 第二类），与转发无关的密钥绝不进去，也不和白名单重叠。
func TestSealedSettingKeysOnlyCarryForwardingSecrets(t *testing.T) {
	forwarding := map[string]bool{}
	for _, k := range ForwardingSettingKeys {
		forwarding[k] = true
	}
	sealed := map[string]bool{}
	for _, k := range SealedSettingKeys {
		require.Falsef(t, forwarding[k], "%s is both whitelisted and sealed", k)
		require.Falsef(t, sealed[k], "%s is listed twice", k)
		sealed[k] = true
	}
	for _, secret := range []string{service.SettingKeyAdminAPIKey, service.SettingKeyTurnstileSecretKey, service.SettingKeySMTPPassword} {
		require.Falsef(t, sealed[secret], "%s has nothing to do with forwarding and must never reach relay nodes", secret)
	}
}

// 加密部分只有这台节点能解开，附加数据绑定节点和版本；内容变了、节点换了加密密钥，版本都变；
// 普通设置里照旧没有密钥。
func TestSealedSnapshotIsBoundToTheNode(t *testing.T) {
	ctx := context.Background()
	settings := fixedSettings{
		service.SettingKeyMinClaudeCodeVersion:     "1.0.0",
		service.SettingKeyContentModerationConfig:  `{"api_key":"sk-mod-secret","proxy_id":7}`,
		service.SettingKeyWebSearchEmulationConfig: `{"providers":[{"api_key":"sk-search-secret"}]}`,
		service.SettingKeyAdminAPIKey:              "admin-secret",
	}
	store := NewMemoryStore()
	n1, err := store.CreatePending(ctx, &Node{IdentityFingerprint: "fp-1", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	priv1, err := sealbox.GenerateKey()
	require.NoError(t, err)
	priv2, err := sealbox.GenerateKey()
	require.NoError(t, err)
	current := priv1
	p := NewConfigPublisher(settings, store, NewEventHub(), func() Trust { return Trust{} })
	p.RegisterSealedSection(SealedSectionProxies, func(context.Context) ([]byte, error) {
		return []byte(`[{"ID":7,"Host":"proxy.example","Password":"proxy-secret"}]`), nil
	})
	p.SetEncryptionKeys(func(id int64) (*ecdh.PublicKey, bool) {
		if id != n1.ID {
			return nil, false
		}
		return current.PublicKey(), true
	})
	require.NoError(t, p.Rebuild(ctx))

	snap, err := p.SnapshotFor(ctx, n1.ID)
	require.NoError(t, err)
	for k, v := range snap.Settings {
		require.NotContains(t, v, "secret", k)
	}
	require.NotEmpty(t, snap.Sealed)
	require.NotContains(t, string(snap.Sealed), "sk-mod-secret")
	plain, err := sealbox.Open(priv1, snap.Sealed, SealedAAD(n1.ID, snap.Version))
	require.NoError(t, err)
	var payload SealedPayload
	require.NoError(t, json.Unmarshal(plain, &payload))
	require.Equal(t, settings[service.SettingKeyContentModerationConfig], payload.Settings[service.SettingKeyContentModerationConfig])
	require.Equal(t, settings[service.SettingKeyWebSearchEmulationConfig], payload.Settings[service.SettingKeyWebSearchEmulationConfig])
	require.NotContains(t, payload.Settings, service.SettingKeyAdminAPIKey)
	require.Contains(t, string(payload.Sections[SealedSectionProxies]), "proxy-secret")

	_, err = sealbox.Open(priv2, snap.Sealed, SealedAAD(n1.ID, snap.Version))
	require.Error(t, err, "another node's key cannot open it")
	_, err = sealbox.Open(priv1, snap.Sealed, SealedAAD(n1.ID+1, snap.Version))
	require.Error(t, err, "bound to the node id")
	_, err = sealbox.Open(priv1, snap.Sealed, SealedAAD(n1.ID, "other-version"))
	require.Error(t, err, "bound to the version")

	v1, err := p.VersionFor(ctx, n1.ID)
	require.NoError(t, err)
	require.Equal(t, snap.Version, v1)
	current = priv2
	v2, err := p.VersionFor(ctx, n1.ID)
	require.NoError(t, err)
	require.NotEqual(t, v1, v2, "a new node encryption key means a new version")

	settings[service.SettingKeyContentModerationConfig] = `{"api_key":"sk-mod-rotated"}`
	require.NoError(t, p.Rebuild(ctx))
	v3, err := p.VersionFor(ctx, n1.ID)
	require.NoError(t, err)
	require.NotEqual(t, v2, v3, "a changed sealed value means a new version")

	// 没有加密公钥的节点拿不到快照（不能带着缺了审核配置的快照去转发）。
	n2, err := store.CreatePending(ctx, &Node{IdentityFingerprint: "fp-2", IdentityPublicKey: []byte{2}}, 20)
	require.NoError(t, err)
	_, err = p.SnapshotFor(ctx, n2.ID)
	require.Error(t, err)
}
