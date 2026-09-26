package node_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type quotaWorld struct {
	m      *testMaster
	n      *testNode
	leases *master.MemoryLeaseStore
	quotas *master.Quotas
	local  *node.LocalQuota
	sync   *node.QuotaSync
	mu     sync.Mutex
	now    time.Time
}

func (w *quotaWorld) clock() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.now
}

func (w *quotaWorld) advance(d time.Duration) {
	w.mu.Lock()
	w.now = w.now.Add(d)
	w.mu.Unlock()
}

// startQuotaWorld：真实 TLS 上的一主一从，主节点挂好额度服务。
func startQuotaWorld(t *testing.T) *quotaWorld {
	t.Helper()
	w := &quotaWorld{m: startMaster(t), now: time.Now()}
	w.n = startNode(t, w.m)
	w.leases = master.NewMemoryLeaseStore()
	q, err := master.NewQuotas(context.Background(), w.leases, w.m.srv.Epoch(), time.Now)
	require.NoError(t, err)
	w.quotas = q
	w.m.control.AttachQuotas(q, master.NewEventRecaller(q, w.m.events), w.m.srv.Epoch())
	w.local = node.NewLocalQuota(nil, w.clock, nil)
	w.sync = node.NewQuotaSync(w.local, w.n.client)
	return w
}

// grantTo 主节点给这台节点锁额度（真实流程随选号下发，WP7），节点收下。
func (w *quotaWorld) grantTo(t *testing.T, headroom float64) master.QuotaGrant {
	t.Helper()
	g, err := w.quotas.Acquire(context.Background(), master.AcquireRequest{UserID: 42, NodeID: w.m.nodeID,
		Wants: []master.QuotaWant{{Scope: master.LeaseScope{Dimension: service.QuotaDimBalance}, Headroom: headroom, NodeUnused: w.local.Unused(42, balance)}}})
	require.NoError(t, err)
	protos := make([]*relayv1.QuotaGrant, 0, len(g))
	for _, x := range g {
		protos = append(protos, x.Proto(42))
	}
	w.local.ApplyGrants(protos)
	return g[0]
}

// 核对、续期、闲置退回（重发只生效一次）经真实连接和纪元校验走通。
func TestQuotaSyncOverTheWire(t *testing.T) {
	ctx := context.Background()
	w := startQuotaWorld(t)
	g := w.grantTo(t, 100)
	require.Equal(t, master.ToMicros(5), w.leases.ReservedBalance(42))

	require.NoError(t, w.sync.Report(ctx), "the first call learns the epoch and retries")
	r, err := w.local.Reserve(ctx, 42, []node.QuotaScope{balance}, unit)
	require.NoError(t, err)
	r.Settle(unit)
	require.NoError(t, w.sync.Renew(ctx))
	for _, l := range w.leases.All() {
		require.NotNil(t, l.LastUsedAt, "the node's last use reached the master")
	}

	w.advance(node.QuotaIdleAfter + time.Second)
	require.NoError(t, w.sync.ReleaseIdle(ctx, node.QuotaIdleAfter))
	require.Equal(t, master.ToMicros(1), w.leases.ReservedBalance(42), "4 unused returned; the 1 spent stays locked until billing")
	require.Empty(t, w.local.PendingReturns(), "confirmed")
	require.NoError(t, w.sync.ReleaseIdle(ctx, node.QuotaIdleAfter))
	require.Equal(t, master.ToMicros(1), w.leases.ReservedBalance(42), "nothing new to return")

	// 同一个累计值直接重发（绕过本地的确认记录）：主节点只处理一次。
	_, _, err = w.quotas.ApplyReturn(ctx, w.m.nodeID, &relayv1.LeaseReturn{LeaseId: g.LeaseID, UserId: 42, ReturnedTotal: 4 * unit})
	require.NoError(t, err)
	require.Equal(t, master.ToMicros(1), w.leases.ReservedBalance(42))
}

// 另一台节点申请不到时，主节点经事件流收回这台闲置的额度；这台在自己协程里回复确认。
func TestRecallReachesTheNodeAndIsAcknowledged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := startQuotaWorld(t)
	w.grantTo(t, 2) // 这台拿 1
	go node.RunEvents(ctx, w.n.client, w.n.syncer, node.EventHandlers{OnQuotaRecall: func(rc *relayv1.QuotaRecall) {
		_ = w.sync.HandleRecall(ctx, rc)
	}})
	require.Eventually(t, func() bool { return len(w.m.events.ConnectedNodes()) == 1 }, 5*time.Second, 10*time.Millisecond)
	w.advance(node.QuotaIdleAfter + time.Second)

	grants, err := w.quotas.Acquire(ctx, master.AcquireRequest{UserID: 42, NodeID: w.m.nodeID + 1000, Need: master.ToMicros(1.5),
		Wants: []master.QuotaWant{{Scope: master.LeaseScope{Dimension: service.QuotaDimBalance}, Headroom: 2}}})
	require.NoError(t, err)
	require.Equal(t, master.ToMicros(1.5), grants[0].Amount, "the idle node's 1 came back in time")
	require.Zero(t, w.local.Unused(42, balance))
	require.Eventually(t, func() bool { return len(w.local.PendingReturns()) == 0 }, 5*time.Second, 10*time.Millisecond,
		"the node records the master's confirmation once the ack reply arrives")
}

// 节点重启后内存里的额度没了：核对时上报空，主节点把它的租约全部放回。
func TestReportAfterNodeRestartReturnsEverything(t *testing.T) {
	ctx := context.Background()
	w := startQuotaWorld(t)
	w.grantTo(t, 100)
	w.local.Reset()
	require.NoError(t, w.sync.Report(ctx))
	require.Zero(t, w.leases.ReservedBalance(42))
	require.Zero(t, w.quotas.RelayReservedBalance(42))
}
