package master_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

var (
	balanceScope = master.LeaseScope{Dimension: service.QuotaDimBalance}
	dailyScope   = master.LeaseScope{Dimension: service.QuotaDimSubscriptionDaily, ScopeID: 5}
)

func unit(v float64) master.Micros { return master.ToMicros(v) }

func newQuotas(t *testing.T, now *time.Time) (*master.Quotas, *master.MemoryLeaseStore) {
	t.Helper()
	store := master.NewMemoryLeaseStore()
	q, err := master.NewQuotas(context.Background(), store, "epoch-1", func() time.Time { return *now })
	require.NoError(t, err)
	return q, store
}

func acquire(t *testing.T, q *master.Quotas, node int64, headroom float64, unused master.Micros) master.Micros {
	t.Helper()
	grants, err := q.Acquire(context.Background(), master.AcquireRequest{UserID: 1, NodeID: node, Need: unit(0.01),
		Wants: []master.QuotaWant{{Scope: balanceScope, Headroom: headroom, NodeUnused: unused}}})
	require.NoError(t, err)
	require.Len(t, grants, 1)
	return grants[0].Amount
}

// 设计 4.2 的例子：可用 100，一台最多持有 5；剩余少时按一半给；不足 0.1 全给。
func TestAcquireFollowsTheHalfRuleAndTheNodeCap(t *testing.T) {
	now := time.Now()
	q, store := newQuotas(t, &now)

	require.Equal(t, unit(5), acquire(t, q, 10, 100, 0), "capped at 5 per node")
	require.Equal(t, master.Micros(0), acquire(t, q, 10, 100, unit(5)), "the node already holds 5 unused")
	require.Equal(t, unit(1.5), acquire(t, q, 10, 100, unit(3.5)), "used 1.5, topped back up to 5")
	require.Equal(t, unit(5), acquire(t, q, 11, 100, 0), "another node gets its own 5")
	require.InDelta(t, 11.5, q.RelayReservedBalance(1), 1e-9)
	require.Equal(t, unit(11.5), store.ReservedBalance(1), "memory and store agree")

	now2 := time.Now()
	q2, _ := newQuotas(t, &now2)
	require.Equal(t, unit(4), acquire(t, q2, 10, 8, 0), "half of 8")
	require.Equal(t, unit(1), acquire(t, q2, 10, 8, unit(4)), "half of the remaining 4, cut to the node's room of 1")
	require.Equal(t, unit(1.5), acquire(t, q2, 11, 8, 0), "half of the 3 still unlocked")
	require.Equal(t, unit(0.75), acquire(t, q2, 12, 8, 0))

	now3 := time.Now()
	q3, _ := newQuotas(t, &now3)
	require.Equal(t, unit(0.09), acquire(t, q3, 10, 0.09, 0), "below 0.1 everything is given")
}

// 所有项都够才给；有一项不够，一项都不给（不为要被拒绝的请求锁钱）。
func TestAcquireIsAllOrNothing(t *testing.T) {
	now := time.Now()
	q, store := newQuotas(t, &now)
	_, err := q.Acquire(context.Background(), master.AcquireRequest{UserID: 1, NodeID: 10, Need: unit(0.05), Wants: []master.QuotaWant{
		{Scope: balanceScope, Headroom: 100},
		{Scope: dailyScope, Headroom: 0.02},
	}})
	var short *master.QuotaInsufficientError
	require.ErrorAs(t, err, &short)
	require.ErrorIs(t, err, master.ErrQuotaInsufficient)
	require.Equal(t, dailyScope, short.Scope)
	require.Empty(t, store.All(), "nothing was locked")
	require.Zero(t, q.RelayReservedBalance(1))

	_, err = q.Acquire(context.Background(), master.AcquireRequest{UserID: 1, NodeID: 10, Need: unit(0.05), Wants: []master.QuotaWant{
		{Scope: balanceScope, Headroom: -3}, // 已透支
	}})
	require.ErrorIs(t, err, master.ErrQuotaInsufficient)
}

func TestAcquireCapsExpiryAtTheSubQuotaExpiry(t *testing.T) {
	now := time.Now()
	q, _ := newQuotas(t, &now)
	subEnds := now.Add(3 * time.Minute)
	grants, err := q.Acquire(context.Background(), master.AcquireRequest{UserID: 1, NodeID: 10, Wants: []master.QuotaWant{
		{Scope: dailyScope, Headroom: 10, ExpiresAt: &subEnds},
		{Scope: balanceScope, Headroom: 10},
	}})
	require.NoError(t, err)
	require.Equal(t, subEnds, grants[0].ExpiresAt, "never outlives the subscription")
	require.Equal(t, now.Add(master.QuotaLeaseTTL), grants[1].ExpiresAt)
}

// 退回、续期、关闭只对这台节点自己的租约有效；冻结额跟着变。
func TestReleaseRenewAndCloseOwnLeasesOnly(t *testing.T) {
	now := time.Now()
	q, store := newQuotas(t, &now)
	ctx := context.Background()
	grants, err := q.Acquire(ctx, master.AcquireRequest{UserID: 1, NodeID: 10, Wants: []master.QuotaWant{{Scope: balanceScope, Headroom: 100}}})
	require.NoError(t, err)
	leaseID := grants[0].LeaseID

	require.ErrorIs(t, q.Release(ctx, 11, 1, leaseID, unit(1)), master.ErrLeaseNotFound, "another node's lease")
	require.NoError(t, q.Release(ctx, 10, 1, leaseID, unit(2)))
	require.Equal(t, unit(3), store.ReservedBalance(1))
	require.NoError(t, q.Release(ctx, 10, 1, leaseID, unit(9)), "clamped to what is still locked")
	require.Zero(t, q.RelayReservedBalance(1))

	grants, err = q.Acquire(ctx, master.AcquireRequest{UserID: 1, NodeID: 10, Wants: []master.QuotaWant{{Scope: balanceScope, Headroom: 100}}})
	require.NoError(t, err)
	used := now.Add(time.Minute)
	now = now.Add(2 * time.Minute)
	expires, err := q.Renew(ctx, 10, 1, grants[0].LeaseID, &used, nil)
	require.NoError(t, err)
	require.Equal(t, now.Add(master.QuotaLeaseTTL), expires)

	returned, err := q.CloseLease(ctx, 10, 1, grants[0].LeaseID, master.LeaseReleased, "node_lost_lease")
	require.NoError(t, err)
	require.Equal(t, unit(5), returned)
	require.Zero(t, store.ReservedBalance(1))
}

// 失联节点的租约到期后收回；停用节点作废它的全部租约，冻结额回到 0。
func TestExpiryAndVoidingBringTheReserveBackToZero(t *testing.T) {
	now := time.Now()
	q, store := newQuotas(t, &now)
	ctx := context.Background()
	acquire(t, q, 10, 100, 0)
	acquire(t, q, 11, 100, 0)
	require.Equal(t, unit(10), store.ReservedBalance(1))

	n, err := q.ExpireDue(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "nothing is due yet")
	now = now.Add(master.QuotaLeaseTTL + time.Second)
	n, err = q.ExpireDue(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Zero(t, store.ReservedBalance(1))
	require.Zero(t, q.RelayReservedBalance(1))

	acquire(t, q, 10, 100, 0)
	returned, err := q.VoidNode(ctx, 10, "node_disabled")
	require.NoError(t, err)
	require.Equal(t, unit(5), returned)
	require.Zero(t, store.ReservedBalance(1))
	for _, l := range store.All() {
		require.NotEqual(t, master.LeaseActive, l.Status)
	}
}

// 重启后从存储载入冻结额。
func TestQuotasLoadTheReserveOnStart(t *testing.T) {
	now := time.Now()
	q, store := newQuotas(t, &now)
	acquire(t, q, 10, 100, 0)
	q2, err := master.NewQuotas(context.Background(), store, "epoch-2", func() time.Time { return now })
	require.NoError(t, err)
	require.InDelta(t, 5, q2.RelayReservedBalance(1), 1e-9)
}

type recallStub struct {
	q        *master.Quotas
	store    *master.MemoryLeaseStore
	calls    []bool
	idleOnly map[int64]bool // 哪些节点的额度算闲置
}

func (r *recallStub) Recall(ctx context.Context, userID, exceptNode int64, scope master.LeaseScope, idleOnly bool) (master.Micros, error) {
	r.calls = append(r.calls, idleOnly)
	var total master.Micros
	for _, l := range r.store.All() {
		if l.Status != master.LeaseActive || l.UserID != userID || l.NodeID == exceptNode || l.LeaseScope != scope {
			continue
		}
		if idleOnly && !r.idleOnly[l.NodeID] {
			continue
		}
		n, err := r.q.CloseLease(ctx, l.NodeID, userID, l.ID, master.LeaseReleased, "recalled")
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// 申请不到时先收回其他节点闲置的；剩余低于 1 时再收回其他节点未使用的（设计 4.2）。
func TestAcquireRecallsFromOtherNodesBeforeRefusing(t *testing.T) {
	now := time.Now()
	q, store := newQuotas(t, &now)
	rs := &recallStub{q: q, store: store, idleOnly: map[int64]bool{11: true}}
	q.SetRecaller(rs)
	ctx := context.Background()
	need := unit(0.8)
	req := func(node int64, headroom float64) (master.Micros, error) {
		g, err := q.Acquire(ctx, master.AcquireRequest{UserID: 1, NodeID: node, Need: need, Wants: []master.QuotaWant{{Scope: balanceScope, Headroom: headroom}}})
		if err != nil {
			return 0, err
		}
		return g[0].Amount, nil
	}

	// 可用 2、每次至少要 0.8：节点 11 拿走 1、节点 12 拿走 0.8，节点 10 只剩 0.2 可给 → 收回闲置的节点 11 后够了。
	_, err := req(11, 2)
	require.NoError(t, err)
	_, err = req(12, 2)
	require.NoError(t, err)
	got, err := req(10, 2)
	require.NoError(t, err)
	require.Equal(t, []bool{true}, rs.calls, "only idle quota was recalled")
	require.Equal(t, need, got)

	// 剩余 2 ≥ 1：节点 12 不闲置，不强行收回，拒绝。
	rs.calls = nil
	_, err = req(13, 2)
	require.ErrorIs(t, err, master.ErrQuotaInsufficient)
	require.Equal(t, []bool{true}, rs.calls, "above 1, active nodes keep their quota")

	// 剩余低于 1 时收回其他节点未使用的部分再算；一半（0.45）不够这次请求（0.8）时给到够为止。
	rs.calls = nil
	got, err = req(13, 0.9)
	require.NoError(t, err)
	require.Equal(t, []bool{true, false}, rs.calls)
	require.Equal(t, need, got)
}

// 一半不够这次请求时给到够为止，但不超过未锁定额度和每台上限。
func TestAcquireGivesAtLeastWhatThisRequestNeeds(t *testing.T) {
	now := time.Now()
	q, _ := newQuotas(t, &now)
	g, err := q.Acquire(context.Background(), master.AcquireRequest{UserID: 1, NodeID: 10, Need: unit(3), Wants: []master.QuotaWant{{Scope: balanceScope, Headroom: 4}}})
	require.NoError(t, err)
	require.Equal(t, unit(3), g[0].Amount, "half would be 2")
	_, err = q.Acquire(context.Background(), master.AcquireRequest{UserID: 1, NodeID: 11, Need: unit(6), Wants: []master.QuotaWant{{Scope: balanceScope, Headroom: 100}}})
	require.ErrorIs(t, err, master.ErrQuotaInsufficient, "a single request larger than the per-node cap cannot be locked")
}

func TestMicrosRoundTrip(t *testing.T) {
	require.Equal(t, master.Micros(500_000_000), master.ToMicros(5))
	require.Equal(t, master.Micros(7), master.ToMicros(0.00000007))
	require.Equal(t, master.Micros(0), master.ToMicros(0.000000009), "rounded down")
	require.Equal(t, master.Micros(-300_000_000), master.ToMicros(-3))
	require.InDelta(t, 0.075, master.FromMicros(master.ToMicros(0.075)), 1e-12)
	require.Zero(t, master.ToMicros(errNaN()))
}

func errNaN() float64 {
	var zero float64
	return zero / zero
}

var _ = errors.New
