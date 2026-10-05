package master

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// apiKeyDimensions 是 Key 维度的额度（总额、5 小时、日、周窗口，归属 ID 是 API Key ID）。
var apiKeyDimensions = []string{
	service.QuotaDimAPIKeyTotal, service.QuotaDimAPIKey5h, service.QuotaDimAPIKey1d, service.QuotaDimAPIKey7d,
}

var subscriptionDimensions = []string{
	service.QuotaDimSubscriptionDaily, service.QuotaDimSubscriptionWeekly, service.QuotaDimSubscriptionMonthly,
}

// RecallAsync 让一台节点退回某个用户一项子额度里未使用的部分，不等回复：
// 事件触发的收回不在任何请求的路径上，回复到了就入账（EventRecaller.Ack）。
func (r *EventRecaller) RecallAsync(nodeID, userID int64, scope LeaseScope) {
	r.events.SendTo(nodeID, &relayv1.MasterEnvelope{Body: &relayv1.MasterEnvelope_QuotaRecall{QuotaRecall: &relayv1.QuotaRecall{
		RecallId: newRecallID(), UserId: userID,
		Scope: &relayv1.QuotaScope{Dimension: scope.Dimension, ScopeId: scope.ScopeID, ScopeKey: scope.ScopeKey},
	}}})
}

// recallLeases 对在线节点上的每份租约发收回，返回发出的份数；离线节点上的等租约到期
// （或管理员"立即回收"），设计 4.4。
func (r *EventRecaller) recallLeases(leases []*Lease) int {
	online := map[int64]bool{}
	for _, id := range r.events.ConnectedNodes() {
		online[id] = true
	}
	sent := 0
	for _, l := range leases {
		if online[l.NodeID] && l.Granted > 0 {
			r.RecallAsync(l.NodeID, l.UserID, l.LeaseScope)
			sent++
		}
	}
	return sent
}

// quotaEventQueueSize：待处理的收回事件，满了丢弃并记日志（租约最长 10 分钟后到期，
// 且每个请求都要经主节点选号，选号时按新配置复查）。
const quotaEventQueueSize = 1024

// quotaEvents 把业务层的改动接成额度收回（设计 4.4）：
//   - 订阅改动：收回这个用户在这个分组的订阅额度；
//   - 平台配额改动：收回这个用户的平台配额额度；
//   - 分组改动：收回所有用户在这个分组的订阅额度；
//   - 用户被停用、删除：收回这个用户的全部额度（由票据吊销器查到状态后通知）。
//
// 修改倍率、价格不收回（额度与倍率无关）；用量窗口重置不处理；充值、兑换等普通用户改动不收回。
//   - Key 被删除、停用、额度用尽：收回这个 Key 维度的额度。
//
// 改密码没有事件，租约闲置 5 分钟后被收回。
type quotaEvents struct {
	quotas   *Quotas
	recaller *EventRecaller
	queue    chan func(context.Context)
}

func newQuotaEvents(q *Quotas, r *EventRecaller) *quotaEvents {
	return &quotaEvents{quotas: q, recaller: r, queue: make(chan func(context.Context), quotaEventQueueSize)}
}

func (e *quotaEvents) enqueue(what string, fn func(context.Context)) {
	select {
	case e.queue <- fn:
	default:
		slog.Warn("relay quota recall queue is full; leases will expire on their own", "event", what)
	}
}

// OnAccessChange 订阅业务层改动（在改动方的协程里执行，只排队）。
func (e *quotaEvents) OnAccessChange(c service.AccessChange) {
	switch c.Kind {
	case service.AccessChangeSubscription:
		e.enqueue("subscription", func(ctx context.Context) {
			e.recallScope(ctx, subscriptionDimensions, c.GroupID, c.UserID)
		})
	case service.AccessChangeGroup:
		e.enqueue("group", func(ctx context.Context) {
			e.recallScope(ctx, subscriptionDimensions, c.GroupID, 0)
		})
	case service.AccessChangeAPIKey:
		e.enqueue("api_key", func(ctx context.Context) {
			e.recallScope(ctx, apiKeyDimensions, c.KeyID, 0)
		})
	case service.AccessChangePlatformQuota:
		e.enqueue("platform_quota", func(ctx context.Context) {
			e.recallUser(ctx, c.UserID, func(l *Lease) bool { return strings.HasPrefix(l.Dimension, "platform_") })
		})
	}
}

// OnUserInactive 用户被停用或删除：收回全部额度。
func (e *quotaEvents) OnUserInactive(userID int64) {
	e.enqueue("user_inactive", func(ctx context.Context) {
		e.recallUser(ctx, userID, func(*Lease) bool { return true })
	})
}

func (e *quotaEvents) recallScope(ctx context.Context, dims []string, scopeID, userID int64) {
	leases, err := e.quotas.store.ListActiveByScope(ctx, dims, scopeID, userID)
	if err != nil {
		slog.Warn("relay quota recall: list leases failed", "scope_id", scopeID, "error", err)
		return
	}
	e.recaller.recallLeases(leases)
}

func (e *quotaEvents) recallUser(ctx context.Context, userID int64, match func(*Lease) bool) {
	leases, err := e.quotas.activeForUser(ctx, userID)
	if err != nil {
		slog.Warn("relay quota recall: list user leases failed", "user_id", userID, "error", err)
		return
	}
	var picked []*Lease
	for _, l := range leases {
		if match(l) {
			picked = append(picked, l)
		}
	}
	e.recaller.recallLeases(picked)
}

func (e *quotaEvents) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case fn := <-e.queue:
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			fn(cctx)
			cancel()
		}
	}
}

// ---- 管理员"立即回收"（设计 4.4、11.5）----

// ReclaimResult 是一次立即回收的结果。
type ReclaimResult struct {
	// RecallSent：在线节点上发出收回的份数（节点退回未使用的部分，已用未入账的等扣费）。
	RecallSent int `json:"recall_sent"`
	// Voided：离线节点上当场作废的租约份数和放回的金额（微单位）。
	Voided        int    `json:"voided"`
	VoidedAmount  Micros `json:"voided_amount"`
	VoidedBalance string `json:"voided_amount_display"`
}

// reclaim 在线节点发收回，离线节点的租约当场作废（节点恢复后作废的租约不能再用，
// 补报的已发生用量照常扣，设计 5.4）。
func (r *Runtime) reclaim(ctx context.Context, rr *runningRelay, leases []*Lease, reason string) (ReclaimResult, error) {
	online := map[int64]bool{}
	for _, id := range rr.events.ConnectedNodes() {
		online[id] = true
	}
	var onlineLeases, offline []*Lease
	for _, l := range leases {
		if online[l.NodeID] {
			onlineLeases = append(onlineLeases, l)
		} else {
			offline = append(offline, l)
		}
	}
	res := ReclaimResult{Voided: len(offline)}
	if rr.recaller != nil {
		res.RecallSent = rr.recaller.recallLeases(onlineLeases)
	}
	amount, err := rr.quotas.closeLeases(ctx, offline, LeaseVoided, reason)
	res.VoidedAmount = amount
	res.VoidedBalance = formatMicros(amount)
	return res, err
}

func formatMicros(m Micros) string {
	return strconv.FormatFloat(FromMicros(m), 'f', -1, 64)
}

// ReclaimNode 管理员按节点立即回收：这台在线时发收回，离线时它的全部租约当场作废。
func (r *Runtime) ReclaimNode(ctx context.Context, actor, nodeID int64) (ReclaimResult, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return ReclaimResult{}, err
	}
	if rr.quotas == nil {
		return ReclaimResult{}, ErrRelayNotRunning
	}
	leases, err := rr.quotas.store.ListActiveByNode(ctx, nodeID)
	if err != nil {
		return ReclaimResult{}, err
	}
	res, err := r.reclaim(ctx, rr, leases, "admin_reclaim_node")
	if err != nil {
		return res, err
	}
	r.audit(ctx, actor, AuditQuotaReclaimed, map[string]any{"node_id": nodeID, "recall_sent": res.RecallSent, "voided": res.Voided, "voided_amount": res.VoidedBalance})
	return res, nil
}

// ReclaimUser 管理员按用户立即回收：这个用户在所有离线节点上的额度当场作废，在线节点上的发收回。
func (r *Runtime) ReclaimUser(ctx context.Context, actor, userID int64) (ReclaimResult, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return ReclaimResult{}, err
	}
	if rr.quotas == nil {
		return ReclaimResult{}, ErrRelayNotRunning
	}
	leases, err := rr.quotas.activeForUser(ctx, userID)
	if err != nil {
		return ReclaimResult{}, err
	}
	res, err := r.reclaim(ctx, rr, leases, "admin_reclaim_user")
	if err != nil {
		return res, err
	}
	r.audit(ctx, actor, AuditQuotaReclaimed, map[string]any{"user_id": userID, "recall_sent": res.RecallSent, "voided": res.Voided, "voided_amount": res.VoidedBalance})
	return res, nil
}

// ---- 管理员减余额、退款前先收回（设计 4.4；锁在离线节点上的返回 409）----

// relayBalanceReclaimTimeout：管理员操作里同步收回的最长等待。
const relayBalanceReclaimTimeout = 3 * time.Second

// balanceReclaimer 实现 service.RelayBalanceReclaimer。
type balanceReclaimer struct {
	quotas   *Quotas
	recaller *EventRecaller
}

// ReclaimBalance 从所有在线节点收回这个用户锁着的余额（同步，等回复），返回收回后仍锁着的金额
// （在离线节点上，或节点已用、未入账的）和最晚的租约到期时间。
func (b *balanceReclaimer) ReclaimBalance(ctx context.Context, userID int64) (float64, time.Time, error) {
	rctx, cancel := context.WithTimeout(ctx, relayBalanceReclaimTimeout)
	defer cancel()
	if _, err := b.recaller.Recall(rctx, userID, 0, LeaseScope{Dimension: service.QuotaDimBalance}, false); err != nil {
		return b.quotas.RelayReservedBalance(userID), time.Time{}, err
	}
	leases, err := b.quotas.activeForUser(ctx, userID)
	if err != nil {
		return b.quotas.RelayReservedBalance(userID), time.Time{}, err
	}
	var releaseBy time.Time
	for _, l := range leases {
		if isBalanceDimension(l.Dimension) && l.Granted > 0 && l.ExpiresAt.After(releaseBy) {
			releaseBy = l.ExpiresAt
		}
	}
	return b.quotas.RelayReservedBalance(userID), releaseBy, nil
}
