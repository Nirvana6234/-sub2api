package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheStickySessionFailureIncrementsAndSetsTTLOnFirstFailure(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	tracker, ok := NewGatewayCache(client).(service.StickySessionFailureTracker)
	require.True(t, ok)

	ctx := context.Background()
	count, err := tracker.IncrementStickySessionFailure(ctx, 14, "session-a", time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	require.Greater(t, server.TTL(buildSessionFailureKey(14, "session-a")), time.Duration(0))

	count, err = tracker.IncrementStickySessionFailure(ctx, 14, "session-a", time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
}

func TestGatewayCacheStickySessionFailureIsScopedPerGroupAndSession(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	tracker, ok := NewGatewayCache(client).(service.StickySessionFailureTracker)
	require.True(t, ok)

	ctx := context.Background()
	_, err := tracker.IncrementStickySessionFailure(ctx, 14, "session-a", time.Minute)
	require.NoError(t, err)

	countOtherGroup, err := tracker.IncrementStickySessionFailure(ctx, 20, "session-a", time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(1), countOtherGroup, "same session hash under a different group must not share the counter")

	countOtherSession, err := tracker.IncrementStickySessionFailure(ctx, 14, "session-b", time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(1), countOtherSession, "different session hash under the same group must not share the counter")
}

func TestGatewayCacheResetStickySessionFailureClearsCounter(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	tracker, ok := NewGatewayCache(client).(service.StickySessionFailureTracker)
	require.True(t, ok)

	ctx := context.Background()
	_, err := tracker.IncrementStickySessionFailure(ctx, 14, "session-a", time.Minute)
	require.NoError(t, err)

	require.NoError(t, tracker.ResetStickySessionFailure(ctx, 14, "session-a"))

	count, err := tracker.IncrementStickySessionFailure(ctx, 14, "session-a", time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "counter should restart from zero after a reset")
}
