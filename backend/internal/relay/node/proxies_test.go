package node

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 加密下发的代理按 ID 取，不在下发范围内的与仓储查不到时一样。
func TestSealedProxies(t *testing.T) {
	cache := NewConfigCache()
	cache.SetOpener(func(sealed, _ []byte) ([]byte, error) { return sealed, nil })
	require.NoError(t, cache.Apply(&relayv1.ConfigSnapshot{Version: "v1", NodeConfig: []byte(`{"node_id":3}`),
		Sealed: []byte(`{"sections":{"proxies":"` + b64(`[{"ID":7,"Host":"p.example","Port":8080,"Password":"pw"}]`) + `"}}`)}))
	proxies := NewSealedProxies(cache)
	px, err := proxies.GetByID(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "p.example", px.Host)
	require.Equal(t, "pw", px.Password)
	_, err = proxies.GetByID(context.Background(), 8)
	require.ErrorIs(t, err, service.ErrProxyNotFound)
	list, err := proxies.ListByIDs(context.Background(), []int64{7, 8})
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
