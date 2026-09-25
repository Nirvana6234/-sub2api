package master

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 白名单里的设置名不能像密钥（设计第 6 节：新增下发项必须确认不是密钥）。
func TestForwardingSettingKeysDoNotLookLikeSecrets(t *testing.T) {
	seen := map[string]bool{}
	for _, key := range ForwardingSettingKeys {
		require.NotEmpty(t, key)
		require.Falsef(t, secretName.MatchString(key), "%s looks like a secret and must not be sent to relay nodes", key)
		require.Falsef(t, seen[key], "%s is listed twice", key)
		seen[key] = true
	}
	// 已知含密钥的设置一定不在白名单里。
	for _, secret := range []string{
		service.SettingKeyAdminAPIKey,
		service.SettingKeyWebSearchEmulationConfig,
		service.SettingKeyContentModerationConfig,
		service.SettingKeyTurnstileSecretKey,
	} {
		require.Falsef(t, seen[secret], "%s must not be sent to relay nodes", secret)
	}
}

func TestJSONSecretFieldDetection(t *testing.T) {
	require.True(t, jsonHasSecretField([]byte(`{"a":{"b":[{"api_key":"x"}]}}`)))
	require.True(t, jsonHasSecretField([]byte(`[{"clientSecret":"x"}]`)))
	require.True(t, jsonHasSecretField([]byte(`{"access_token":"x"}`)))
	require.False(t, jsonHasSecretField([]byte(`{"ip":"1.2.3.4","note":"blocked"}`)))
	require.False(t, jsonHasSecretField([]byte(`plain text value`)))
	require.False(t, jsonHasSecretField([]byte(`"a string"`)))
}

type fixedSettings map[string]string

func (f fixedSettings) GetValue(_ context.Context, key string) (string, error) {
	v, ok := f[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return v, nil
}

func (f fixedSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := f[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

// 版本只取决于内容：相同内容相同版本（主节点重启也一致），节点配置不同版本不同。
func TestSnapshotVersionIsAContentHash(t *testing.T) {
	ctx := context.Background()
	settings := fixedSettings{service.SettingKeyOpenAITTFTMode: "strict", service.SettingKeyMinCodexVersion: "0.9.0"}
	a, err := buildGlobal(ctx, settings, nil, []string{"root-1"}, nil)
	require.NoError(t, err)
	b, err := buildGlobal(ctx, settings, nil, []string{"root-1"}, nil)
	require.NoError(t, err)
	require.Equal(t, a.hash, b.hash)

	node1 := &Node{ID: 1, PublicDomain: "r1.example.com"}
	node2 := &Node{ID: 2, PublicDomain: "r2.example.com"}
	require.Equal(t, a.snapshotFor(node1).Version, b.snapshotFor(node1).Version)
	require.NotEqual(t, a.snapshotFor(node1).Version, a.snapshotFor(node2).Version)

	settings[service.SettingKeyOpenAITTFTMode] = "loose"
	c, err := buildGlobal(ctx, settings, nil, []string{"root-1"}, nil)
	require.NoError(t, err)
	require.NotEqual(t, a.hash, c.hash)

	d, err := buildGlobal(ctx, settings, nil, []string{"root-1", "root-2"}, nil)
	require.NoError(t, err)
	require.NotEqual(t, c.hash, d.hash, "publishing a new root fingerprint is a config change")

	e, err := buildGlobal(ctx, settings, map[string]SectionProvider{"error_passthrough_rules": func(context.Context) ([]byte, error) {
		return []byte(`[{"code":429}]`), nil
	}}, []string{"root-1", "root-2"}, nil)
	require.NoError(t, err)
	require.NotEqual(t, d.hash, e.hash)
	require.Equal(t, []byte(`[{"code":429}]`), e.snapshotFor(node1).Sections["error_passthrough_rules"])
}

func TestGeneralConfigDefaultsAndValidation(t *testing.T) {
	g := GeneralConfig{}.WithDefaults()
	require.Equal(t, 10, *g.MasterRatioPercent)
	require.Equal(t, APIKeyNodeRuleAssigned, g.APIKeyNodeRule)
	require.Equal(t, 85, g.LoadThresholdPercent)

	zero := 0
	g = GeneralConfig{MasterRatioPercent: &zero}.WithDefaults()
	require.Equal(t, 0, *g.MasterRatioPercent, "0 means the master does no relaying, not unset")

	bad := 101
	require.Error(t, GeneralConfig{MasterRatioPercent: &bad}.Validate())
	require.Error(t, GeneralConfig{APIKeyNodeRule: "sometimes"}.Validate())
	require.NoError(t, GeneralConfig{APIKeyNodeRule: APIKeyNodeRuleAny}.Validate())
}
