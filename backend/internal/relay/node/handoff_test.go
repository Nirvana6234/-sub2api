package node

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/stretchr/testify/require"
)

func handoffSnapshot(version string, payload string) *relayv1.ConfigSnapshot {
	return &relayv1.ConfigSnapshot{Version: version, NodeConfig: []byte(`{"node_id":5}`),
		Sealed: []byte(`{"sections":{"handoff":"` + b64(payload) + `"}}`)}
}

// 从节点用主节点加密下发的密钥给"交给主节点转发"的请求签名；还没收到密钥时不带标记。
func TestHandoffSignerUsesTheSealedKey(t *testing.T) {
	cache := NewConfigCache()
	cache.SetOpener(func(sealed, _ []byte) ([]byte, error) { return sealed, nil })
	now := time.Unix(1_700_000_000, 0)
	signer := cache.HandoffSigner(func() int64 { return 5 }, func() time.Time { return now })

	require.Empty(t, signer("POST", "/v1/live"), "no key delivered yet")

	key := []byte("0123456789abcdef0123456789abcdef")
	require.NoError(t, cache.Apply(handoffSnapshot("v1", `{"key":"`+b64(string(key))+`"}`)))
	marker := signer("POST", "/v1/live")
	require.NotEmpty(t, marker)
	id, err := sign.VerifyHandoff(key, marker, "POST", "/v1/live", now)
	require.NoError(t, err)
	require.Equal(t, int64(5), id)

	// 密钥换了（主节点重启）：下一次签名用新的。
	key2 := []byte("ffffffffffffffffffffffffffffffff")
	require.NoError(t, cache.Apply(handoffSnapshot("v2", `{"key":"`+b64(string(key2))+`"}`)))
	_, err = sign.VerifyHandoff(key, signer("POST", "/v1/live"), "POST", "/v1/live", now)
	require.Error(t, err)
	_, err = sign.VerifyHandoff(key2, signer("POST", "/v1/live"), "POST", "/v1/live", now)
	require.NoError(t, err)

	// 下发的内容坏了：不带标记，不 panic。
	require.NoError(t, cache.Apply(handoffSnapshot("v3", `not json`)))
	require.Empty(t, signer("POST", "/v1/live"))
}
