package master

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

var protoBalance = LeaseScope{Dimension: service.QuotaDimBalance}

func newProtoQuotas(t *testing.T) (*Quotas, *MemoryLeaseStore, *time.Time) {
	t.Helper()
	now := time.Now()
	store := NewMemoryLeaseStore()
	q, err := NewQuotas(context.Background(), store, "epoch-1", func() time.Time { return now })
	require.NoError(t, err)
	return q, store, &now
}

func grant(t *testing.T, q *Quotas, node int64, headroom float64) QuotaGrant {
	t.Helper()
	g, err := q.Acquire(context.Background(), AcquireRequest{UserID: 1, NodeID: node, Wants: []QuotaWant{{Scope: protoBalance, Headroom: headroom}}})
	require.NoError(t, err)
	require.Len(t, g, 1)
	return g[0]
}

// 退回带"累计值"：同一条重发（包括传输层幂等记录已被淘汰后重发）只生效一次。
func TestApplyReturnIsCumulativeAndIdempotent(t *testing.T) {
	q, store, _ := newProtoQuotas(t)
	ctx := context.Background()
	g := grant(t, q, 10, 100) // 锁 5
	ret := func(total Micros, closed bool) (bool, Micros) {
		dropped, n, err := q.ApplyReturn(ctx, 10, &relayv1.LeaseReturn{LeaseId: g.LeaseID, UserId: 1, ReturnedTotal: total, Closed: closed})
		require.NoError(t, err)
		return dropped, n
	}

	_, n := ret(ToMicros(2), false)
	require.Equal(t, ToMicros(2), n)
	for i := 0; i < 3; i++ {
		_, n = ret(ToMicros(2), false)
		require.Zero(t, n, "a resend changes nothing")
	}
	require.Equal(t, ToMicros(3), store.ReservedBalance(1))
	_, n = ret(ToMicros(2.5), false)
	require.Equal(t, ToMicros(0.5), n, "only the new part is applied")
	_, n = ret(ToMicros(1), false)
	require.Zero(t, n, "an older total never adds back")

	// 另一台节点不能退这份租约。
	dropped, _, err := q.ApplyReturn(ctx, 11, &relayv1.LeaseReturn{LeaseId: g.LeaseID, UserId: 1, ReturnedTotal: ToMicros(5)})
	require.NoError(t, err)
	require.True(t, dropped)
	require.Equal(t, ToMicros(2.5), store.ReservedBalance(1))

	dropped, n = ret(ToMicros(2.5), true)
	require.True(t, dropped)
	require.Equal(t, ToMicros(2.5), n, "closing returns what is still locked")
	require.Zero(t, store.ReservedBalance(1))
	dropped, n = ret(ToMicros(9), true)
	require.True(t, dropped, "a closed lease is dropped")
	require.Zero(t, n)
}

// 主节点重启（新的额度服务、同一个库）后，节点重发同一条退回（回复丢失的情况）什么都不改。
func TestResentReturnAfterMasterRestartAppliesNothing(t *testing.T) {
	q, store, now := newProtoQuotas(t)
	ctx := context.Background()
	g := grant(t, q, 10, 100)
	ret := &relayv1.LeaseReturn{LeaseId: g.LeaseID, UserId: 1, ReturnedTotal: ToMicros(2)}
	_, n, err := q.ApplyReturn(ctx, 10, ret)
	require.NoError(t, err)
	require.Equal(t, ToMicros(2), n)

	restarted, err := NewQuotas(ctx, store, "epoch-2", func() time.Time { return *now })
	require.NoError(t, err)
	_, n, err = restarted.ApplyReturn(ctx, 10, ret)
	require.NoError(t, err)
	require.Zero(t, n, "already applied before the restart")
	require.Equal(t, ToMicros(3), store.ReservedBalance(1))
	require.InDelta(t, 3, restarted.RelayReservedBalance(1), 1e-9)

	_, n, err = restarted.ApplyReturn(ctx, 10, &relayv1.LeaseReturn{LeaseId: g.LeaseID, UserId: 1, ReturnedTotal: ToMicros(3)})
	require.NoError(t, err)
	require.Equal(t, ToMicros(1), n, "the counter carries on across the restart")
}

// 续期记下最后使用时间；已作废的（比如节点离线时被管理员回收）让节点丢掉。
func TestRenewBatchDropsVoidedLeases(t *testing.T) {
	q, store, now := newProtoQuotas(t)
	ctx := context.Background()
	a := grant(t, q, 10, 100)
	*now = now.Add(time.Minute)
	used := now.Add(-30 * time.Second)
	renewed, dropped, err := q.RenewBatch(ctx, 10, []*relayv1.LeaseRenewal{
		{LeaseId: a.LeaseID, UserId: 1, LastUsedAtUnixMs: used.UnixMilli()},
		{LeaseId: 999, UserId: 1},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{999}, dropped)
	require.Len(t, renewed, 1)
	require.Equal(t, now.Add(QuotaLeaseTTL).UnixMilli(), renewed[0].ExpiresAtUnixMs)
	for _, l := range store.All() {
		require.NotNil(t, l.LastUsedAt)
		require.Equal(t, used.UnixMilli(), l.LastUsedAt.UnixMilli())
	}

	_, err = q.VoidNode(ctx, 10, "admin_reclaim")
	require.NoError(t, err)
	_, dropped, err = q.RenewBatch(ctx, 10, []*relayv1.LeaseRenewal{{LeaseId: a.LeaseID, UserId: 1}})
	require.NoError(t, err)
	require.Equal(t, []int64{a.LeaseID}, dropped)
}

// 重连核对：节点没上报的关掉放回，节点上报了但主节点已不认的让它丢掉。
func TestReconcileNodeClosesUnreportedLeases(t *testing.T) {
	q, store, _ := newProtoQuotas(t)
	ctx := context.Background()
	a := grant(t, q, 10, 100)
	b, err := q.Acquire(ctx, AcquireRequest{UserID: 1, NodeID: 10, Wants: []QuotaWant{{Scope: LeaseScope{Dimension: service.QuotaDimAPIKeyTotal, ScopeID: 3}, Headroom: 50}}})
	require.NoError(t, err)
	grant(t, q, 11, 100)
	require.Equal(t, ToMicros(10), store.ReservedBalance(1))

	dropped, err := q.ReconcileNode(ctx, 10, []*relayv1.HeldLease{{LeaseId: b[0].LeaseID, UserId: 1}, {LeaseId: 12345, UserId: 1}})
	require.NoError(t, err)
	require.Equal(t, []int64{12345}, dropped)
	require.Equal(t, ToMicros(5), store.ReservedBalance(1), "node 10's unreported balance lease was returned")
	require.InDelta(t, 5, q.RelayReservedBalance(1), 1e-9)
	for _, l := range store.All() {
		if l.ID == a.LeaseID {
			require.Equal(t, LeaseReleased, l.Status)
		}
	}

	dropped, err = q.ReconcileNode(ctx, 10, nil)
	require.NoError(t, err)
	require.Empty(t, dropped)
	require.Equal(t, ToMicros(5), store.ReservedBalance(1), "node 11 is untouched")
}

func fakeRecallSession(h *EventHub, nodeID int64) chan *relayv1.MasterEnvelope {
	sess := &eventSession{out: make(chan *relayv1.MasterEnvelope, 16), done: make(chan struct{})}
	h.mu.Lock()
	if h.sessions == nil {
		h.sessions = map[int64]map[*eventSession]struct{}{}
	}
	if h.sessions[nodeID] == nil {
		h.sessions[nodeID] = map[*eventSession]struct{}{}
	}
	h.sessions[nodeID][sess] = struct{}{}
	h.mu.Unlock()
	return sess.out
}

// 收回：在线节点收到请求并回复后，申请在同一次调用里拿到额度（回复要进同一把用户锁，不能死锁）；
// 不在线的节点直接跳过。
func TestRecallOverTheEventStream(t *testing.T) {
	q, store, _ := newProtoQuotas(t)
	events := NewEventHub()
	r := NewEventRecaller(q, events)
	ctx := context.Background()
	held := grant(t, q, 11, 2) // 在线节点拿走 1
	grant(t, q, 12, 2)         // 离线节点拿走 0.5
	out := fakeRecallSession(events, 11)

	go func() {
		env := <-out
		rc := env.GetQuotaRecall()
		if rc == nil || rc.GetUserId() != 1 || !rc.GetIdleOnly() {
			return
		}
		_, _ = r.Ack(ctx, 11, &relayv1.AckQuotaRecallRequest{RecallId: rc.GetRecallId(), Returns: []*relayv1.LeaseReturn{
			{LeaseId: held.LeaseID, UserId: 1, ReturnedTotal: ToMicros(1), Closed: true},
		}})
	}()
	done := make(chan struct{})
	var grants []QuotaGrant
	var err error
	go func() {
		grants, err = q.Acquire(ctx, AcquireRequest{UserID: 1, NodeID: 10, Need: ToMicros(0.8), Wants: []QuotaWant{{Scope: protoBalance, Headroom: 2}}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("acquire deadlocked waiting for the recall ack")
	}
	require.NoError(t, err)
	require.Equal(t, ToMicros(0.8), grants[0].Amount)
	require.Equal(t, ToMicros(1.3), store.ReservedBalance(1), "node 12 (offline) keeps its 0.5, node 10 holds 0.8")
}

// 节点不回复：等到时限就放弃；之后晚到的回复照样入账。
func TestRecallTimesOutAndLateAcksStillCount(t *testing.T) {
	q, store, _ := newProtoQuotas(t)
	events := NewEventHub()
	r := NewEventRecaller(q, events)
	ctx := context.Background()
	held := grant(t, q, 11, 2)
	out := fakeRecallSession(events, 11)

	start := time.Now()
	_, err := q.Acquire(ctx, AcquireRequest{UserID: 1, NodeID: 10, Need: ToMicros(1.5), Wants: []QuotaWant{{Scope: protoBalance, Headroom: 2}}})
	require.ErrorIs(t, err, ErrQuotaInsufficient)
	elapsed := time.Since(start)
	require.Less(t, elapsed, quotaRecallBudget+time.Second, "both recall rounds share one budget")

	env := <-out
	_, err = r.Ack(ctx, 11, &relayv1.AckQuotaRecallRequest{RecallId: env.GetQuotaRecall().GetRecallId(), Returns: []*relayv1.LeaseReturn{
		{LeaseId: held.LeaseID, UserId: 1, ReturnedTotal: ToMicros(1)},
	}})
	require.NoError(t, err)
	require.Zero(t, store.ReservedBalance(1), "the late ack is real money returned")
}

func quotaCtx(nodeID int64, epoch string) context.Context {
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: transport.PeerAuthInfo{Identity: transport.PeerIdentity{Class: transport.PeerIssued, NodeID: nodeID}}})
	return metadata.NewIncomingContext(ctx, metadata.Pairs("x-relay-epoch", epoch))
}

// 额度调用动钱：必须是签发证书、带当前纪元，节点只能动自己的租约。
func TestQuotaControlChecksPeerAndEpoch(t *testing.T) {
	q, store, _ := newProtoQuotas(t)
	g := grant(t, q, 10, 100)
	c := NewControl(nil)
	_, err := c.ReleaseQuota(quotaCtx(10, "epoch-1"), &relayv1.ReleaseQuotaRequest{})
	require.Equal(t, codes.Unavailable, status.Code(err), "no quota service attached yet")

	c.AttachQuotas(q, NewEventRecaller(q, NewEventHub()), "epoch-1")
	_, err = c.ReleaseQuota(context.Background(), &relayv1.ReleaseQuotaRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = c.ReleaseQuota(quotaCtx(10, "epoch-0"), &relayv1.ReleaseQuotaRequest{Returns: []*relayv1.LeaseReturn{{LeaseId: g.LeaseID, UserId: 1, ReturnedTotal: ToMicros(5)}}})
	require.Equal(t, codes.FailedPrecondition, status.Code(err), "an old epoch cannot move money")
	require.Equal(t, ToMicros(5), store.ReservedBalance(1))

	resp, err := c.ReleaseQuota(quotaCtx(11, "epoch-1"), &relayv1.ReleaseQuotaRequest{Returns: []*relayv1.LeaseReturn{{LeaseId: g.LeaseID, UserId: 1, ReturnedTotal: ToMicros(5)}}})
	require.NoError(t, err)
	require.Equal(t, []int64{g.LeaseID}, resp.DroppedLeaseIds, "another node's lease")
	require.Equal(t, ToMicros(5), store.ReservedBalance(1))

	_, err = c.ReleaseQuota(quotaCtx(10, "epoch-1"), &relayv1.ReleaseQuotaRequest{Returns: []*relayv1.LeaseReturn{{LeaseId: g.LeaseID, UserId: 1, ReturnedTotal: ToMicros(5)}}})
	require.NoError(t, err)
	require.Zero(t, store.ReservedBalance(1))
}
