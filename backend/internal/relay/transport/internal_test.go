package transport

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/stretchr/testify/require"
)

// 幂等结果按字节上限淘汰最旧的：内存不随调用量无限增长。
func TestIdempotencyStoreStaysWithinItsByteBudget(t *testing.T) {
	const budget = 64 << 10
	s := newIdempotencyStore(IdempotencyOptions{TTL: time.Hour, MaxBytesPerPeer: budget})
	payload := make([]byte, 1024)
	executions := 0
	call := func(key string) {
		_, err := s.do(context.Background(), "node:1", key, func() (any, error) {
			executions++
			return &relayv1.PingResponse{Payload: payload}, nil
		})
		require.NoError(t, err)
	}
	for i := 0; i < 1000; i++ {
		call("k" + strconv.Itoa(i))
		require.LessOrEqual(t, s.bytesFor("node:1"), budget)
	}
	require.Equal(t, 1000, executions)

	// 最新的还在：重发不执行。
	call("k999")
	require.Equal(t, 1000, executions)
	// 最旧的已被淘汰：重发会重新执行。
	call("k0")
	require.Equal(t, 1001, executions)
	// 别的节点不占这台的预算。
	require.Zero(t, s.bytesFor("node:2"))
}

func TestIdempotencyStoreExpiresEntries(t *testing.T) {
	now := time.Now()
	s := newIdempotencyStore(IdempotencyOptions{TTL: time.Minute})
	s.now = func() time.Time { return now }
	executions := 0
	call := func() {
		_, err := s.do(context.Background(), "node:1", "k", func() (any, error) {
			executions++
			return &relayv1.PingResponse{}, nil
		})
		require.NoError(t, err)
	}
	call()
	call()
	require.Equal(t, 1, executions)
	now = now.Add(2 * time.Minute)
	call()
	require.Equal(t, 2, executions)
}

// 失败不缓存：过载等失败之后的重发要重新执行。
func TestIdempotencyStoreDoesNotCacheFailures(t *testing.T) {
	s := newIdempotencyStore(IdempotencyOptions{})
	executions := 0
	fail := true
	call := func() error {
		_, err := s.do(context.Background(), "node:1", "k", func() (any, error) {
			executions++
			if fail {
				return nil, context.DeadlineExceeded
			}
			return &relayv1.PingResponse{}, nil
		})
		return err
	}
	require.Error(t, call())
	fail = false
	require.NoError(t, call())
	require.NoError(t, call())
	require.Equal(t, 2, executions)
}

func TestLimiterCapsInflightPerPeer(t *testing.T) {
	l := newLimiter(map[CallClass]Limit{ClassControl: {MaxInflight: 2}})
	r1, _, ok := l.acquire("node:1", ClassControl)
	require.True(t, ok)
	r2, _, ok := l.acquire("node:1", ClassControl)
	require.True(t, ok)
	_, retry, ok := l.acquire("node:1", ClassControl)
	require.False(t, ok)
	require.Equal(t, time.Second, retry)

	_, _, ok = l.acquire("node:2", ClassControl)
	require.True(t, ok, "another node has its own budget")
	_, _, ok = l.acquire("node:1", ClassLogs)
	require.True(t, ok, "unconfigured classes are not limited")

	r1()
	r1() // 重复释放无害
	_, _, ok = l.acquire("node:1", ClassControl)
	require.True(t, ok)
	r2()
}

func TestBackoffGrowsWithJitterAndCaps(t *testing.T) {
	b := &Backoff{Base: 500 * time.Millisecond, Max: 30 * time.Second, Factor: 1.6, Jitter: 0.2}
	prevUpper := time.Duration(0)
	for i := 0; i < 20; i++ {
		d := b.Next()
		require.LessOrEqual(t, d, 30*time.Second)
		require.GreaterOrEqual(t, d, 400*time.Millisecond)
		if i == 0 {
			require.LessOrEqual(t, d, 600*time.Millisecond)
		}
		prevUpper = d
	}
	require.GreaterOrEqual(t, prevUpper, 24*time.Second, "reaches the cap")
	b.Reset()
	require.LessOrEqual(t, b.Next(), 600*time.Millisecond)
}
