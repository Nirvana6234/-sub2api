package node_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/stretchr/testify/require"
)

const unit = int64(100_000_000)

var balance = node.QuotaScope{Dimension: "balance"}

// fakeMaster 按剩余预算发额度：每次给 min(剩余, 每次上限 − 节点未用)，和主节点规则的边界一致。
type fakeMaster struct {
	mu       sync.Mutex
	budget   int64
	perGrant int64
	delay    time.Duration
	calls    atomic.Int64
	leaseID  int64
	expires  time.Time
	err      error
}

func (m *fakeMaster) Refill(_ context.Context, userID int64, wants []node.QuotaWantReport, need int64) ([]*relayv1.QuotaGrant, error) {
	m.calls.Add(1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	var out []*relayv1.QuotaGrant
	for _, w := range wants {
		give := m.perGrant - w.Unused
		if give > m.budget {
			give = m.budget
		}
		if give < 0 {
			give = 0
		}
		m.budget -= give
		out = append(out, &relayv1.QuotaGrant{LeaseId: m.leaseID, UserId: userID, Amount: give,
			Scope:           &relayv1.QuotaScope{Dimension: w.Scope.Dimension, ScopeId: w.Scope.ScopeID, ScopeKey: w.Scope.ScopeKey},
			ExpiresAtUnixMs: m.expires.UnixMilli()})
	}
	return out, nil
}

func newLocal(m *fakeMaster, now *time.Time) *node.LocalQuota {
	if m.expires.IsZero() {
		m.expires = now.Add(10 * time.Minute)
	}
	if m.leaseID == 0 {
		m.leaseID = 1
	}
	return node.NewLocalQuota(m, func() time.Time { return *now }, nil)
}

// 30 个并发请求反复预扣、结算：花出去的永远不超过从主节点拿到的（开发计划 3.1、WP6 验收）。
func TestLocalQuotaNeverOverspends(t *testing.T) {
	now := time.Now()
	m := &fakeMaster{budget: 3 * unit, perGrant: unit}
	q := newLocal(m, &now)
	var spent, refused atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r, err := q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/100)
				if errors.Is(err, node.ErrQuotaInsufficient) {
					refused.Add(1)
					continue
				}
				if err != nil {
					t.Error(err)
					return
				}
				r.Settle(unit / 100)
				spent.Add(unit / 100)
			}
		}()
	}
	wg.Wait()
	granted := 3*unit - m.budget
	require.LessOrEqual(t, spent.Load(), granted, "never spend more than was granted")
	require.Equal(t, granted-spent.Load(), q.Unused(1, balance), "what is left locally is exactly granted minus spent")
	require.Equal(t, 3*unit, spent.Load(), "the whole budget is used before anyone is refused")
	require.Positive(t, refused.Load())
}

// 30 个请求同时发现本地不够：只向主节点申请一次（合并补充）。
func TestConcurrentShortfallsTriggerOneRefill(t *testing.T) {
	now := time.Now()
	m := &fakeMaster{budget: 100 * unit, perGrant: 5 * unit, delay: 50 * time.Millisecond}
	q := newLocal(m, &now)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/10)
			if err == nil {
				r.Settle(unit / 10)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int64(1), m.calls.Load(), "one refill for 30 concurrent requests")
	require.Equal(t, 2*unit, q.Unused(1, balance), "5 granted, 30 × 0.1 spent")
}

// 多项子额度：有一项不够就把已预扣的都退回。
func TestReserveIsAllOrNothingAcrossScopes(t *testing.T) {
	now := time.Now()
	m := &fakeMaster{budget: unit, perGrant: unit}
	q := newLocal(m, &now)
	daily := node.QuotaScope{Dimension: "subscription_daily", ScopeID: 5}
	q.ApplyGrants([]*relayv1.QuotaGrant{{LeaseId: 7, UserId: 1, Amount: unit, Scope: &relayv1.QuotaScope{Dimension: "balance"}, ExpiresAtUnixMs: now.Add(time.Hour).UnixMilli()}})
	m.budget = 0 // 订阅窗口一分也补不到
	_, err := q.Reserve(context.Background(), 1, []node.QuotaScope{balance, daily}, unit/2)
	require.ErrorIs(t, err, node.ErrQuotaInsufficient)
	require.Equal(t, unit, q.Unused(1, balance), "the balance reservation was rolled back")
}

// 离到期不到"30 秒 + 时钟偏差"就停用，改为申请补充（主节点收回时这里已不再用）。
func TestLeaseNearExpiryIsNotUsed(t *testing.T) {
	now := time.Now()
	m := &fakeMaster{budget: 0, perGrant: unit}
	skew := 5 * time.Second
	q := node.NewLocalQuota(m, func() time.Time { return now }, func() time.Duration { return skew })
	q.ApplyGrants([]*relayv1.QuotaGrant{{LeaseId: 1, UserId: 1, Amount: unit, Scope: &relayv1.QuotaScope{Dimension: "balance"}, ExpiresAtUnixMs: now.Add(34 * time.Second).UnixMilli()}})
	_, err := q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/10)
	require.ErrorIs(t, err, node.ErrQuotaInsufficient, "34s left is inside 30s + 5s skew")
	require.Equal(t, int64(1), m.calls.Load(), "asked the master instead")

	m.mu.Lock()
	m.budget, m.expires = unit, now.Add(10*time.Minute)
	m.mu.Unlock()
	r, err := q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/10)
	require.NoError(t, err, "a refill also extends the lease")
	r.Settle(unit / 10)
}

// 低于上次补充后余量的 30% 时后台提前补充。
func TestRefillsEarlyBelowThirtyPercent(t *testing.T) {
	now := time.Now()
	m := &fakeMaster{budget: 100 * unit, perGrant: unit}
	q := newLocal(m, &now)
	r, err := q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/2) // 首次：补 1，扣 0.5
	require.NoError(t, err)
	r.Settle(unit / 2)
	require.Equal(t, int64(1), m.calls.Load())
	r, err = q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/4) // 剩 0.25 < 30%
	require.NoError(t, err)
	r.Settle(unit / 4)
	require.Eventually(t, func() bool { return m.calls.Load() == 2 }, 2*time.Second, 5*time.Millisecond, "topped up in the background")
}

// 实际费用超出预估：多扣的部分让余量变负，下一个请求要补充成功才放行。
func TestOvershootMustBeRefilledBeforeTheNextRequest(t *testing.T) {
	now := time.Now()
	m := &fakeMaster{budget: unit, perGrant: unit}
	q := newLocal(m, &now)
	r, err := q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/10)
	require.NoError(t, err)
	r.Settle(2 * unit) // 上游实际花了 2
	r.Settle(0)        // 只结算一次
	require.Equal(t, -unit, q.Unused(1, balance))
	_, err = q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/10)
	require.ErrorIs(t, err, node.ErrQuotaInsufficient, "the master has nothing left to cover the overshoot")

	r2 := func() *node.Reservation {
		m.mu.Lock()
		m.budget, m.perGrant = 10*unit, 2*unit
		m.mu.Unlock()
		r, err := q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/10)
		require.NoError(t, err)
		return r
	}()
	r2.Cancel()
	require.Equal(t, unit, q.Unused(1, balance), "refilled by 2 on top of the -1 overshoot")
}

// 收回：只收回闲置时最近用过就不退；累计退回随租约增长、跨纪元不清零，换了新租约从 0 算。
func TestRecallIdleAndCumulativeReturns(t *testing.T) {
	now := time.Now()
	m := &fakeMaster{}
	q := node.NewLocalQuota(m, func() time.Time { return now }, nil)
	grant := func(lease, amount int64) {
		q.ApplyGrants([]*relayv1.QuotaGrant{{LeaseId: lease, UserId: 1, Amount: amount, Scope: &relayv1.QuotaScope{Dimension: "balance"}, ExpiresAtUnixMs: now.Add(time.Hour).UnixMilli()}})
	}
	recall := func(idle bool) []*relayv1.LeaseReturn {
		return q.Recall(&relayv1.QuotaRecall{UserId: 1, Scope: &relayv1.QuotaScope{Dimension: "balance"}, IdleOnly: idle})
	}
	grant(7, 2*unit)
	r, err := q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/2)
	require.NoError(t, err)
	r.Settle(unit / 2)

	require.Empty(t, recall(true), "used just now: not idle")
	rets := recall(false)
	require.Len(t, rets, 1)
	require.Equal(t, int64(7), rets[0].LeaseId)
	require.Equal(t, unit+unit/2, rets[0].ReturnedTotal)
	require.Zero(t, q.Unused(1, balance))

	grant(7, unit)
	now = now.Add(node.QuotaIdleAfter + time.Second)
	rets = recall(true)
	require.Len(t, rets, 1)
	require.Equal(t, 2*unit+unit/2, rets[0].ReturnedTotal, "the total keeps growing on the same lease")

	grant(8, unit) // 主节点换了新租约（旧的已关闭）
	rets = recall(false)
	require.Equal(t, int64(8), rets[0].LeaseId)
	require.Equal(t, unit, rets[0].ReturnedTotal, "a new lease counts from zero")

	grant(8, unit)
	idle := q.IdleReturns(node.QuotaIdleAfter)
	require.Len(t, idle, 1, "idle quota can also be released by the node itself")
	require.Equal(t, 2*unit, idle[0].ReturnedTotal)
}

// 主节点不认的租约丢掉不再用；续期带最后使用时间；核对上报手里的租约；进程重启清空。
func TestDropRenewHeldAndReset(t *testing.T) {
	now := time.Now()
	q := node.NewLocalQuota(&fakeMaster{}, func() time.Time { return now }, nil)
	q.ApplyGrants([]*relayv1.QuotaGrant{
		{LeaseId: 7, UserId: 1, Amount: unit, Scope: &relayv1.QuotaScope{Dimension: "balance"}, ExpiresAtUnixMs: now.Add(time.Minute).UnixMilli()},
		{LeaseId: 9, UserId: 2, Amount: unit, Scope: &relayv1.QuotaScope{Dimension: "balance"}, ExpiresAtUnixMs: now.Add(time.Minute).UnixMilli()},
	})
	r, err := q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/10)
	require.NoError(t, err)
	r.Settle(unit / 10)
	ren := q.Renewals()
	require.Len(t, ren, 2)
	for _, x := range ren {
		if x.LeaseId == 7 {
			require.Equal(t, now.UnixMilli(), x.LastUsedAtUnixMs)
		} else {
			require.Zero(t, x.LastUsedAtUnixMs)
		}
	}
	q.ApplyRenewed([]*relayv1.RenewedLease{{LeaseId: 7, ExpiresAtUnixMs: now.Add(time.Hour).UnixMilli()}})
	now = now.Add(2 * time.Minute)
	_, err = q.Reserve(context.Background(), 1, []node.QuotaScope{balance}, unit/10)
	require.NoError(t, err, "renewed lease is still usable")

	q.Drop([]int64{9})
	held := q.Held()
	require.Len(t, held, 1)
	require.Equal(t, int64(7), held[0].LeaseId)
	require.Zero(t, q.Unused(2, balance), "a dropped lease is gone")

	q.Reset()
	require.Empty(t, q.Held())
}
