package master

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// quotaRecallBudget：一次申请里所有收回（先闲置、再全部）共用的等待时间。
// 收回发生在别的请求的选号里，不能让它久等；等不到的节点按离线处理，等租约到期。
const quotaRecallBudget = 1500 * time.Millisecond

// ---- 退回：节点报告这份租约至今累计退回了多少，主节点只处理比已记录的多出来的部分 ----

// ApplyReturn 处理节点对一份租约的退回：累计值记在租约上（持久化），只处理比已记录的多出来的
// 那一截（不超过租约还锁着的），所以重发、回复丢失、主节点重启后再发都只生效一次，不依赖传输层的
// 幂等记录。closed 时关闭整份租约。租约已不在（关闭、作废、不是这台的）时返回 dropped=true。
func (q *Quotas) ApplyReturn(ctx context.Context, nodeID int64, ret *relayv1.LeaseReturn) (dropped bool, returned Micros, err error) {
	err = q.withUser(ctx, ret.GetUserId(), func(tx LeaseTx) error {
		l, err := q.ownLease(ctx, tx, nodeID, ret.GetLeaseId())
		if errors.Is(err, ErrLeaseNotFound) {
			dropped = true
			return nil
		}
		if err != nil {
			return err
		}
		now := q.now()
		delta, err := tx.ApplyReturned(ctx, l.ID, ret.GetReturnedTotal(), now)
		if err != nil {
			return err
		}
		returned += delta
		if ret.GetClosed() {
			n, err := tx.Close(ctx, l.ID, LeaseReleased, "node_returned", now)
			if err != nil {
				return err
			}
			returned += n
			dropped = true
		}
		return nil
	})
	if errors.Is(err, ErrLeaseUserNotFound) {
		return true, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return dropped, returned, nil
}

// RenewBatch 续期节点报告的租约，记下最后使用时间；已不在的租约放进 dropped（节点丢弃，
// 包括节点离线期间被管理员作废的）。
func (q *Quotas) RenewBatch(ctx context.Context, nodeID int64, leases []*relayv1.LeaseRenewal) (renewed []*relayv1.RenewedLease, dropped []int64, err error) {
	for _, r := range leases {
		var used *time.Time
		if ms := r.GetLastUsedAtUnixMs(); ms > 0 {
			t := time.UnixMilli(ms)
			used = &t
		}
		expires, err := q.Renew(ctx, nodeID, r.GetUserId(), r.GetLeaseId(), used, nil)
		if errors.Is(err, ErrLeaseNotFound) || errors.Is(err, ErrLeaseUserNotFound) {
			dropped = append(dropped, r.GetLeaseId())
			continue
		}
		if err != nil {
			return renewed, dropped, err
		}
		renewed = append(renewed, &relayv1.RenewedLease{LeaseId: r.GetLeaseId(), ExpiresAtUnixMs: expires.UnixMilli()})
	}
	return renewed, dropped, nil
}

// ReconcileNode 重连或纪元变化后按节点上报核对（设计 4.2）：节点没上报的生效租约关闭放回
// （节点重启丢了内存里的额度），节点上报了但主节点已不认的让它丢弃。
func (q *Quotas) ReconcileNode(ctx context.Context, nodeID int64, held []*relayv1.HeldLease) (dropped []int64, err error) {
	active, err := q.store.ListActiveByNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	reported := make(map[int64]bool, len(held))
	for _, h := range held {
		reported[h.GetLeaseId()] = true
	}
	known := make(map[int64]bool, len(active))
	var lost []*Lease
	for _, l := range active {
		known[l.ID] = true
		if !reported[l.ID] {
			lost = append(lost, l)
		}
	}
	if _, err := q.closeLeases(ctx, lost, LeaseReleased, "node_lost_lease"); err != nil {
		return nil, err
	}
	for _, h := range held {
		if !known[h.GetLeaseId()] {
			dropped = append(dropped, h.GetLeaseId())
		}
	}
	return dropped, nil
}

// activeForUser 返回这个用户的生效租约（收回时找持有的节点）。
func (q *Quotas) activeForUser(ctx context.Context, userID int64) ([]*Lease, error) {
	var out []*Lease
	err := q.store.WithUser(ctx, userID, func(tx LeaseTx) error {
		var err error
		out, err = tx.Active(ctx)
		return err
	})
	return out, err
}

// ---- 收回：主节点经事件流发给节点，节点用 AckQuotaRecall 回报 ----

// EventRecaller 是经事件流的跨节点收回（QuotaRecaller）。
type EventRecaller struct {
	quotas *Quotas
	events *EventHub

	mu      sync.Mutex
	waiters map[string]chan Micros
}

// NewEventRecaller 创建收回器并挂到额度服务上。
func NewEventRecaller(q *Quotas, events *EventHub) *EventRecaller {
	r := &EventRecaller{quotas: q, events: events, waiters: map[string]chan Micros{}}
	q.SetRecaller(r)
	return r
}

func newRecallID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Recall 实现 QuotaRecaller：同时发给除 exceptNode 外所有持有这一项的在线节点，等到都回复或
// ctx 结束。没有事件流的节点算离线，直接跳过（等租约到期）。回复晚到的照样入账，只是不再计入。
func (r *EventRecaller) Recall(ctx context.Context, userID, exceptNode int64, scope LeaseScope, idleOnly bool) (Micros, error) {
	leases, err := r.quotas.activeForUser(ctx, userID)
	if err != nil {
		return 0, err
	}
	online := map[int64]bool{}
	for _, id := range r.events.ConnectedNodes() {
		online[id] = true
	}
	targets := map[int64]bool{}
	for _, l := range leases {
		if l.LeaseScope == scope && l.NodeID != exceptNode && l.Granted > 0 && online[l.NodeID] {
			targets[l.NodeID] = true
		}
	}
	if len(targets) == 0 {
		return 0, nil
	}
	results := make(chan Micros, len(targets))
	ids := make([]string, 0, len(targets))
	r.mu.Lock()
	for nodeID := range targets {
		id := newRecallID()
		ids = append(ids, id)
		r.waiters[id] = results
		r.events.SendTo(nodeID, &relayv1.MasterEnvelope{Body: &relayv1.MasterEnvelope_QuotaRecall{QuotaRecall: &relayv1.QuotaRecall{
			RecallId: id, UserId: userID, IdleOnly: idleOnly,
			Scope: &relayv1.QuotaScope{Dimension: scope.Dimension, ScopeId: scope.ScopeID, ScopeKey: scope.ScopeKey},
		}}})
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		for _, id := range ids {
			delete(r.waiters, id)
		}
		r.mu.Unlock()
	}()

	var total Micros
	for pending := len(targets); pending > 0; pending-- {
		select {
		case n := <-results:
			total += n
		case <-ctx.Done():
			return total, nil
		}
	}
	return total, nil
}

// Ack 处理节点的收回回复：先把退回入账（无论还有没有人在等），再叫醒等待的申请。
func (r *EventRecaller) Ack(ctx context.Context, nodeID int64, req *relayv1.AckQuotaRecallRequest) ([]int64, error) {
	var total Micros
	var dropped []int64
	for _, ret := range req.GetReturns() {
		gone, n, err := r.quotas.ApplyReturn(ctx, nodeID, ret)
		if err != nil {
			return dropped, err
		}
		total += n
		if gone {
			dropped = append(dropped, ret.GetLeaseId())
		}
	}
	r.mu.Lock()
	ch, ok := r.waiters[req.GetRecallId()]
	delete(r.waiters, req.GetRecallId())
	r.mu.Unlock()
	if ok {
		ch <- total // 缓冲区等于目标节点数，不会阻塞
	}
	return dropped, nil
}

// ---- RelayControl 上的额度调用 ----

// quotaControl 是 Control 里额度调用的依赖（运行时启动后挂上）。
type quotaControl struct {
	quotas   *Quotas
	recaller *EventRecaller
	epoch    string
}

// AttachQuotas 挂上额度服务（运行时启动时）。
func (c *Control) AttachQuotas(q *Quotas, r *EventRecaller, epoch string) {
	c.quota = &quotaControl{quotas: q, recaller: r, epoch: epoch}
}

// quotaPeer 取调用方节点：必须是签发证书，额度服务已就绪，且带着当前纪元。
func (c *Control) quotaPeer(ctx context.Context) (int64, error) {
	peer, ok := transport.PeerFromContext(ctx)
	if !ok || peer.Class != transport.PeerIssued {
		return 0, status.Error(codes.PermissionDenied, "quota calls require an issued certificate")
	}
	if c.quota == nil {
		return 0, status.Error(codes.Unavailable, "relay quota service is not available")
	}
	if transport.IncomingEpoch(ctx) != c.quota.epoch {
		return 0, transport.EpochMismatch(ctx)
	}
	return peer.NodeID, nil
}

// ReleaseQuota 节点退回未使用的额度。
func (c *Control) ReleaseQuota(ctx context.Context, req *relayv1.ReleaseQuotaRequest) (*relayv1.ReleaseQuotaResponse, error) {
	nodeID, err := c.quotaPeer(ctx)
	if err != nil {
		return nil, err
	}
	resp := &relayv1.ReleaseQuotaResponse{}
	for _, ret := range req.GetReturns() {
		dropped, _, err := c.quota.quotas.ApplyReturn(ctx, nodeID, ret)
		if err != nil {
			return nil, status.Error(codes.Unavailable, "relay quota release failed")
		}
		if dropped {
			resp.DroppedLeaseIds = append(resp.DroppedLeaseIds, ret.GetLeaseId())
		}
	}
	return resp, nil
}

// RenewLeases 批量续期。
func (c *Control) RenewLeases(ctx context.Context, req *relayv1.RenewLeasesRequest) (*relayv1.RenewLeasesResponse, error) {
	nodeID, err := c.quotaPeer(ctx)
	if err != nil {
		return nil, err
	}
	renewed, dropped, err := c.quota.quotas.RenewBatch(ctx, nodeID, req.GetLeases())
	if err != nil {
		return nil, status.Error(codes.Unavailable, "relay lease renewal failed")
	}
	return &relayv1.RenewLeasesResponse{Renewed: renewed, DroppedLeaseIds: dropped}, nil
}

// ReportLeases 重连后核对。
func (c *Control) ReportLeases(ctx context.Context, req *relayv1.ReportLeasesRequest) (*relayv1.ReportLeasesResponse, error) {
	nodeID, err := c.quotaPeer(ctx)
	if err != nil {
		return nil, err
	}
	dropped, err := c.quota.quotas.ReconcileNode(ctx, nodeID, req.GetLeases())
	if err != nil {
		return nil, status.Error(codes.Unavailable, "relay lease reconciliation failed")
	}
	return &relayv1.ReportLeasesResponse{DroppedLeaseIds: dropped}, nil
}

// AckQuotaRecall 收回回复。
func (c *Control) AckQuotaRecall(ctx context.Context, req *relayv1.AckQuotaRecallRequest) (*relayv1.AckQuotaRecallResponse, error) {
	nodeID, err := c.quotaPeer(ctx)
	if err != nil {
		return nil, err
	}
	if c.quota.recaller == nil {
		return nil, status.Error(codes.Unavailable, "relay quota recall is not available")
	}
	dropped, err := c.quota.recaller.Ack(ctx, nodeID, req)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "relay quota recall ack failed")
	}
	return &relayv1.AckQuotaRecallResponse{DroppedLeaseIds: dropped}, nil
}
