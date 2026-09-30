package node

import (
	"context"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type usageMaster struct {
	relayv1.RelayControlClient
	mu      sync.Mutex
	totals  map[string]int64
	keys    []string
	fail    error
	perNode int64
}

func (m *usageMaster) ReportWebSearchUsage(ctx context.Context, in *relayv1.WebSearchUsageReport, _ ...grpc.CallOption) (*relayv1.WebSearchUsageShares, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys = append(m.keys, transport.IdempotencyKey(ctx))
	if m.fail != nil {
		err := m.fail
		m.fail = nil
		return nil, err
	}
	for k, v := range in.GetUsed() {
		m.totals[k] += v
	}
	return &relayv1.WebSearchUsageShares{Shares: []*relayv1.WebSearchShare{{Provider: "brave", Limit: 100, Used: m.totals["brave"], Allowance: m.perNode}}}, nil
}

// 从节点的联网搜索配额：没拿到份额前放行；在份额内本地计数、用完换别的服务；定期上报拿回新份额；
// 上报失败用同一个幂等键重发（主节点只加一次）。
func TestWebSearchQuotaOnTheNode(t *testing.T) {
	ctx := context.Background()
	m := &usageMaster{totals: map[string]int64{}, perNode: 2}
	q := newWebSearchQuota(m)
	cfg := websearch.ProviderConfig{Type: "brave", QuotaLimit: 100}

	ok, reserved := q.Reserve(ctx, cfg)
	require.True(t, ok, "no share yet: allowed, like a single server without Redis")
	require.True(t, reserved)
	require.NoError(t, q.Report(ctx))
	require.Equal(t, int64(1), m.totals["brave"])

	for i := 0; i < 2; i++ {
		ok, _ = q.Reserve(ctx, cfg)
		require.True(t, ok)
	}
	ok, _ = q.Reserve(ctx, cfg)
	require.False(t, ok, "this node's share is used up")
	used, _ := q.Usage(ctx, "brave")
	require.Equal(t, int64(3), used, "global usage plus what this node has not reported yet")

	// 上报失败：同一份用同一个键重发。
	m.fail = status.Error(codes.Unavailable, "down")
	require.Error(t, q.Report(ctx))
	require.NoError(t, q.Report(ctx))
	require.Equal(t, m.keys[len(m.keys)-2], m.keys[len(m.keys)-1])
	require.Equal(t, int64(3), m.totals["brave"], "counted once")

	// 报完之后失败退回的，下次报成负数。
	ok, _ = q.Reserve(ctx, cfg)
	require.True(t, ok)
	require.NoError(t, q.Report(ctx))
	q.Rollback(ctx, cfg)
	require.NoError(t, q.Report(ctx))
	require.Equal(t, int64(3), m.totals["brave"])

	require.True(t, q.ProxyAvailable(ctx, 5))
	q.MarkProxyUnavailable(ctx, 5)
	require.False(t, q.ProxyAvailable(ctx, 5))
	require.Error(t, q.Reset(ctx, "brave"), "resetting the quota is a master operation")
}
