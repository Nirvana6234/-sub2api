package websearch

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Redis 计数：单机按次占用、用完拒绝、失败回退；主从分流时主节点把各节点报上来的用量加进同一个计数。
func TestRedisQuotaStore(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	q := NewRedisQuotaStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	cfg := ProviderConfig{Type: "brave", QuotaLimit: 3}

	for i := 0; i < 3; i++ {
		ok, reserved := q.Reserve(ctx, cfg)
		require.True(t, ok)
		require.True(t, reserved)
	}
	ok, _ := q.Reserve(ctx, cfg)
	require.False(t, ok, "exhausted")
	q.Rollback(ctx, cfg)
	used, err := q.Usage(ctx, "brave")
	require.NoError(t, err)
	require.Equal(t, int64(2), used)

	total, err := q.Add(ctx, cfg, 5)
	require.NoError(t, err)
	require.Equal(t, int64(7), total)
	total, err = q.Add(ctx, cfg, -1)
	require.NoError(t, err)
	require.Equal(t, int64(6), total, "a node's rollback after reporting")
	require.Greater(t, mr.TTL(quotaRedisKey("brave")).Seconds(), float64(0), "the counter keeps its monthly reset")

	require.True(t, q.ProxyAvailable(ctx, 9))
	q.MarkProxyUnavailable(ctx, 9)
	require.False(t, q.ProxyAvailable(ctx, 9))
	require.NoError(t, q.Reset(ctx, "brave"))
	used, _ = q.Usage(ctx, "brave")
	require.Zero(t, used)
}
