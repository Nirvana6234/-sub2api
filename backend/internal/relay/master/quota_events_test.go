package master

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// drainRecalls 取出一条事件流上收到的全部收回请求。
func drainRecalls(ch chan *relayv1.MasterEnvelope) []*relayv1.QuotaRecall {
	var out []*relayv1.QuotaRecall
	for {
		select {
		case env := <-ch:
			if rc := env.GetQuotaRecall(); rc != nil {
				out = append(out, rc)
			}
		default:
			return out
		}
	}
}

func grantScope(t *testing.T, q *Quotas, user, node int64, scope LeaseScope) {
	t.Helper()
	_, err := q.Acquire(context.Background(), AcquireRequest{UserID: user, NodeID: node, Wants: []QuotaWant{{Scope: scope, Headroom: 100}}})
	require.NoError(t, err)
}

// 订阅、分组、平台配额改动与用户停用各自只收回对应的额度；只发给在线节点。
func TestQuotaEventsRecallTheRightLeases(t *testing.T) {
	q, _, _ := newProtoQuotas(t)
	events := NewEventHub()
	r := NewEventRecaller(q, events)
	e := newQuotaEvents(q, r)
	ctx := context.Background()
	online := fakeRecallSession(events, 10) // 节点 11 离线

	sub5 := LeaseScope{Dimension: service.QuotaDimSubscriptionDaily, ScopeID: 5}
	sub6 := LeaseScope{Dimension: service.QuotaDimSubscriptionDaily, ScopeID: 6}
	plat := LeaseScope{Dimension: service.QuotaDimPlatformDaily, ScopeKey: "anthropic"}
	grantScope(t, q, 1, 10, sub5)
	grantScope(t, q, 1, 11, sub5)
	grantScope(t, q, 2, 10, sub5)
	grantScope(t, q, 1, 10, sub6)
	grantScope(t, q, 1, 10, plat)
	grantScope(t, q, 1, 10, protoBalance)

	run := func(fn func()) []*relayv1.QuotaRecall {
		fn()
		for len(e.queue) > 0 {
			(<-e.queue)(ctx)
		}
		return drainRecalls(online)
	}
	scopes := func(rcs []*relayv1.QuotaRecall) map[[2]int64]string {
		out := map[[2]int64]string{}
		for _, rc := range rcs {
			require.False(t, rc.GetIdleOnly(), "event recalls are not limited to idle quota")
			out[[2]int64{rc.GetUserId(), rc.GetScope().GetScopeId()}] = rc.GetScope().GetDimension()
		}
		return out
	}

	got := run(func() {
		e.OnAccessChange(service.AccessChange{Kind: service.AccessChangeSubscription, UserID: 1, GroupID: 5})
	})
	require.Len(t, got, 1, "user 1's group-5 lease on the online node only")
	require.Equal(t, map[[2]int64]string{{1, 5}: service.QuotaDimSubscriptionDaily}, scopes(got))

	got = run(func() { e.OnAccessChange(service.AccessChange{Kind: service.AccessChangeGroup, GroupID: 5}) })
	require.Equal(t, map[[2]int64]string{{1, 5}: service.QuotaDimSubscriptionDaily, {2, 5}: service.QuotaDimSubscriptionDaily}, scopes(got))

	got = run(func() { e.OnAccessChange(service.AccessChange{Kind: service.AccessChangePlatformQuota, UserID: 1}) })
	require.Len(t, got, 1)
	require.Equal(t, "anthropic", got[0].GetScope().GetScopeKey())

	got = run(func() { e.OnAccessChange(service.AccessChange{Kind: service.AccessChangeUser, UserID: 1}) })
	require.Empty(t, got, "an ordinary user change (a top-up) recalls nothing")

	got = run(func() { e.OnUserInactive(1) })
	require.Len(t, got, 4, "all of user 1's leases on the online node")
}

// 票据吊销器查到用户停用时顺带通知收回额度。
func TestInactiveUserAlsoTriggersQuotaRecall(t *testing.T) {
	rv := newTicketRevoker(userStatusStub{2: {ID: 2, Status: service.StatusDisabled}, 3: {ID: 3, Status: service.StatusActive}}, NewEventHub(), time.Now)
	var got []int64
	rv.onInactive = func(id int64) { got = append(got, id) }
	for _, id := range []int64{2, 3, 4} {
		rv.check(context.Background(), id)
	}
	require.Equal(t, []int64{2, 4}, got, "disabled and deleted, not active")
}

// 管理员立即回收：在线节点发收回（已用未入账的等扣费），离线节点的租约当场作废放回。
func TestAdminReclaimVoidsOfflineAndRecallsOnline(t *testing.T) {
	q, store, now := newProtoQuotas(t)
	events := NewEventHub()
	rr := &runningRelay{quotas: q, events: events, recaller: NewEventRecaller(q, events)}
	rt := &Runtime{deps: RuntimeDeps{Store: NewMemoryStore()}, running: rr, now: func() time.Time { return *now }}
	ctx := context.Background()
	online := fakeRecallSession(events, 10)
	grant(t, q, 10, 100)
	grant(t, q, 11, 100)
	require.Equal(t, ToMicros(10), store.ReservedBalance(1))

	res, err := rt.ReclaimUser(ctx, 7, 1)
	require.NoError(t, err)
	require.Equal(t, 1, res.RecallSent)
	require.Equal(t, 1, res.Voided)
	require.Equal(t, ToMicros(5), res.VoidedAmount)
	require.Equal(t, "5", res.VoidedBalance)
	require.Equal(t, ToMicros(5), store.ReservedBalance(1), "the offline node's 5 is back; the online node was asked to return")
	require.Len(t, drainRecalls(online), 1)

	res, err = rt.ReclaimNode(ctx, 7, 10)
	require.NoError(t, err)
	require.Equal(t, 1, res.RecallSent)
	require.Zero(t, res.Voided, "an online node is asked, not voided")

	mem, ok := rt.deps.Store.(*MemoryStore)
	require.True(t, ok)
	audits := mem.Audits()
	require.Len(t, audits, 2)
	require.Equal(t, AuditQuotaReclaimed, audits[0].Action)
	require.Equal(t, int64(7), audits[0].ActorUserID)
}
