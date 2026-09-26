package mastertest

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// RunQuotaNeverOverGrants：10 台节点、每台 30 个并发请求为同一用户反复申请额度，
// 锁出去的总额任何时候都不超过可用额度，冻结额与租约总和一致（开发计划 WP6 验收）。
// 返回每次申请的耗时（从小到大），供报告 p99。
func RunQuotaNeverOverGrants(t *testing.T, h LeaseHarness) []time.Duration {
	return runQuotaConcurrency(t, h, 30, 5)
}

// RunQuotaOneInFlightPerNode：每台节点同一时刻只有一个申请在途（从节点合并补充，开发计划 3.1），
// 10 台同时为同一用户申请。用来量真实负载下的申请耗时（开发计划 3.3：p99 ≤ 20 毫秒）。
func RunQuotaOneInFlightPerNode(t *testing.T, h LeaseHarness) []time.Duration {
	return runQuotaConcurrency(t, h, 1, 100)
}

func runQuotaConcurrency(t *testing.T, h LeaseHarness, perNode, rounds int) []time.Duration {
	ctx := context.Background()
	s := h.New(t)
	user := h.NewUser(t, s)
	const nodes = 10
	nodeIDs := make([]int64, nodes)
	for i := range nodeIDs {
		nodeIDs[i] = h.NewNode(t, s)
	}
	q, err := master.NewQuotas(ctx, s, "epoch", time.Now)
	require.NoError(t, err)

	headroom := 20.0 // 用户可用 20，期间没有扣费
	scope := master.LeaseScope{Dimension: service.QuotaDimBalance}
	var (
		held      = make([]atomic.Int64, nodes)
		refused   atomic.Int64
		latencyMu sync.Mutex
		latencies []time.Duration
		wg        sync.WaitGroup
	)
	for i := 0; i < nodes; i++ {
		for j := 0; j < perNode; j++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for r := 0; r < rounds; r++ {
					start := time.Now()
					grants, err := q.Acquire(ctx, master.AcquireRequest{UserID: user, NodeID: nodeIDs[i], Need: master.ToMicros(0.01),
						Wants: []master.QuotaWant{{Scope: scope, Headroom: headroom, NodeUnused: held[i].Load()}}})
					d := time.Since(start)
					latencyMu.Lock()
					latencies = append(latencies, d)
					latencyMu.Unlock()
					if errors.Is(err, master.ErrQuotaInsufficient) {
						refused.Add(1)
						continue
					}
					if !assertNoErr(t, err) {
						return
					}
					for _, g := range grants {
						held[i].Add(g.Amount)
					}
				}
			}(i)
		}
	}
	wg.Wait()

	var total master.Micros
	for i := range held {
		total += held[i].Load()
	}
	require.LessOrEqual(t, total, master.ToMicros(headroom), "never lock more than the user has")
	require.Equal(t, total, h.Reserved(t, s, user), "the reserve equals what was granted")
	require.InDelta(t, master.FromMicros(total), q.RelayReservedBalance(user), 1e-9)
	var active master.Micros
	for _, n := range nodeIDs {
		ls, err := s.ListActiveByNode(ctx, n)
		require.NoError(t, err)
		require.LessOrEqual(t, len(ls), 1, "one lease per node and sub-quota")
		for _, l := range ls {
			active += l.Granted
		}
	}
	require.Equal(t, total, active)
	t.Logf("locked %.4f of %.1f across %d nodes; %d requests refused", master.FromMicros(total), headroom, nodes, refused.Load())

	sort.Slice(latencies, func(a, b int) bool { return latencies[a] < latencies[b] })
	return latencies
}

func assertNoErr(t *testing.T, err error) bool {
	t.Helper()
	if err != nil {
		t.Errorf("acquire failed: %v", err)
		return false
	}
	return true
}
