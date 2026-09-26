package master

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 额度规则（设计 4.2、第 19 节）。金额都是微单位。
const (
	// quotaFullGrantBelow：未锁定额度低于它时全部给出（设计：最小锁定单位 0.05，小于 0.1 时全部给）。
	quotaFullGrantBelow Micros = MicrosPerUnit / 10
	// QuotaPerNodeCap：一台节点为同一用户、同一项子额度持有的还没用掉的金额上限。
	QuotaPerNodeCap Micros = 5 * MicrosPerUnit
	// quotaLowRemaining：剩余低于它时，申请不到就直接收回其他节点上未使用的部分（不等闲置）。
	quotaLowRemaining Micros = MicrosPerUnit
	// QuotaLeaseTTL：租约有效期，节点在线时续期。
	QuotaLeaseTTL = 10 * time.Minute
	// QuotaIdleAfter：某台上这个用户多久没有请求算闲置，闲置额度会被收回。
	QuotaIdleAfter = 5 * time.Minute
)

// ErrQuotaInsufficient：额度不够这次请求（返回余额不足）。
var ErrQuotaInsufficient = errors.New("relay quota is insufficient for this request")

// QuotaInsufficientError 带上不够的那一项。errors.Is(err, ErrQuotaInsufficient) 成立。
type QuotaInsufficientError struct {
	Scope     LeaseScope
	Available Micros // 节点手里没用掉的 + 这次最多能给的
	Need      Micros
}

func (e *QuotaInsufficientError) Error() string {
	return fmt.Sprintf("%s: %s scope=%d/%q available=%d need=%d", ErrQuotaInsufficient.Error(), e.Scope.Dimension, e.Scope.ScopeID, e.Scope.ScopeKey, e.Available, e.Need)
}

func (e *QuotaInsufficientError) Unwrap() error { return ErrQuotaInsufficient }

// QuotaWant 是一次申请里的一项子额度。
type QuotaWant struct {
	Scope LeaseScope
	// Headroom 是这一项现在的剩余（service.QuotaHeadroom），主节点在选号时算出。
	Headroom float64
	// ExpiresAt 是这一项的有效期上限（订阅、Key 到期），没有为 nil。
	ExpiresAt *time.Time
	// NodeUnused 是节点报告的、它手里这一项还没用掉的金额（每台上限按它算）。
	NodeUnused Micros
}

// AcquireRequest 是一次额度申请（选号时顺带，设计 4.2）。
type AcquireRequest struct {
	UserID int64
	NodeID int64
	Wants  []QuotaWant
	// Need 是这次请求的最低预估：每一项"节点手里没用掉的 + 这次给的"都要不少于它，否则一项都不给。
	Need Micros
}

// QuotaGrant 是给出的一项。
type QuotaGrant struct {
	LeaseID int64
	Scope   LeaseScope
	// Amount 是这次新给的；Granted 是这份租约现在总共锁着的（含节点已用、未入账的）。
	Amount    Micros
	Granted   Micros
	ExpiresAt time.Time
}

// QuotaRecaller 从在线节点收回某个用户未使用的额度（同步调用，协议见 WP6 第 3 步）。
// idleOnly 为 true 时只收回闲置的。返回收回的金额；节点离线时返回 0（等租约到期）。
type QuotaRecaller interface {
	Recall(ctx context.Context, userID int64, exceptNode int64, scope LeaseScope, idleOnly bool) (Micros, error)
}

// Quotas 是主节点的额度服务：按维度把额度锁给从节点（设计第 4 节）。
//
// 同一用户的申请在进程内串行（按用户分片的锁），存储层再用一个短事务锁用户行；
// 余额维度锁着的总额同时记在内存里，给余额预检用（service.RelayReservedBalanceReader）。
type Quotas struct {
	store    LeaseStore
	epoch    string
	now      func() time.Time
	recaller QuotaRecaller

	locks [64]sync.Mutex

	reservedMu sync.RWMutex
	reserved   map[int64]Micros
}

// NewQuotas 创建额度服务并载入所有用户当前的冻结额。
func NewQuotas(ctx context.Context, store LeaseStore, epoch string, now func() time.Time) (*Quotas, error) {
	if now == nil {
		now = time.Now
	}
	reserved, err := store.ReservedBalances(ctx)
	if err != nil {
		return nil, fmt.Errorf("load relay reserved balances: %w", err)
	}
	return &Quotas{store: store, epoch: epoch, now: now, reserved: reserved}, nil
}

// SetRecaller 挂上跨节点收回（协议层建好后）。
func (q *Quotas) SetRecaller(r QuotaRecaller) { q.recaller = r }

// RelayReservedBalance 实现 service.RelayReservedBalanceReader：这个用户锁在所有从节点上的余额。
func (q *Quotas) RelayReservedBalance(userID int64) float64 {
	q.reservedMu.RLock()
	defer q.reservedMu.RUnlock()
	return FromMicros(q.reserved[userID])
}

var _ service.RelayReservedBalanceReader = (*Quotas)(nil)

func (q *Quotas) lockUser(userID int64) func() {
	m := &q.locks[uint64(userID)%uint64(len(q.locks))]
	m.Lock()
	return m.Unlock
}

// withUser 在进程内锁和存储事务里改一个用户的租约，提交后刷新内存里的冻结额。
func (q *Quotas) withUser(ctx context.Context, userID int64, fn func(tx LeaseTx) error) error {
	unlock := q.lockUser(userID)
	defer unlock()
	var reserved Micros
	err := q.store.WithUser(ctx, userID, func(tx LeaseTx) error {
		if err := fn(tx); err != nil {
			return err
		}
		active, err := tx.Active(ctx)
		if err != nil {
			return err
		}
		reserved = 0
		for _, l := range active {
			if isBalanceDimension(l.Dimension) {
				reserved += l.Granted
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	q.reservedMu.Lock()
	if reserved > 0 {
		q.reserved[userID] = reserved
	} else {
		delete(q.reserved, userID)
	}
	q.reservedMu.Unlock()
	return nil
}

// grantAmount 按设计 4.2 算这次给多少：未锁定额度的一半，不足 0.1 全给；
// 但至少给到够这次请求（need − 节点手里没用掉的），否则未锁定额度明明够也会因为"一半"被拒；
// 最后受未锁定额度和每台上限约束。
func grantAmount(unlocked, nodeUnused, need Micros) Micros {
	if unlocked <= 0 {
		return 0
	}
	give := unlocked
	if unlocked >= quotaFullGrantBelow {
		give = unlocked / 2
	}
	if short := need - nodeUnused; give < short {
		give = short
	}
	if give > unlocked {
		give = unlocked
	}
	if room := QuotaPerNodeCap - nodeUnused; give > room {
		give = room
	}
	if give < 0 {
		return 0
	}
	return give
}

type wantPlan struct {
	want       QuotaWant
	remaining  Micros
	give       Micros
	lockedHere *Lease
}

// plan 在事务里算每一项给多少；有一项不够 Need 就返回 QuotaInsufficientError。
func (q *Quotas) plan(ctx context.Context, tx LeaseTx, req AcquireRequest) ([]wantPlan, error) {
	active, err := tx.Active(ctx)
	if err != nil {
		return nil, err
	}
	plans := make([]wantPlan, 0, len(req.Wants))
	var short *QuotaInsufficientError
	for _, w := range req.Wants {
		p := wantPlan{want: w, remaining: ToMicros(w.Headroom)}
		var lockedAll Micros
		for _, l := range active {
			if l.LeaseScope != w.Scope {
				continue
			}
			lockedAll += l.Granted
			if l.NodeID == req.NodeID {
				p.lockedHere = l
			}
		}
		p.give = grantAmount(p.remaining-lockedAll, w.NodeUnused, req.Need)
		if w.NodeUnused+p.give < req.Need && short == nil {
			short = &QuotaInsufficientError{Scope: w.Scope, Available: w.NodeUnused + p.give, Need: req.Need}
		}
		plans = append(plans, p)
	}
	if short != nil {
		return plans, short
	}
	return plans, nil
}

// Acquire 为一台节点申请一组子额度（设计 4.2）。所有项都够这次请求才一起给；
// 不够时先收回其他节点上这个用户闲置的额度，剩余低于 1 时再收回其他节点上未使用的部分，然后重算，
// 仍不够返回 QuotaInsufficientError（余额不足）。
func (q *Quotas) Acquire(ctx context.Context, req AcquireRequest) ([]QuotaGrant, error) {
	grants, err := q.tryAcquire(ctx, req)
	if !errors.Is(err, ErrQuotaInsufficient) || q.recaller == nil {
		return grants, err
	}
	// 收回在进程内锁之外进行（节点的回复要进同一把锁入账），两轮收回共用一个时限。
	rctx, cancel := context.WithTimeout(ctx, quotaRecallBudget)
	defer cancel()
	var short *QuotaInsufficientError
	errors.As(err, &short)
	if n, rerr := q.recaller.Recall(rctx, req.UserID, req.NodeID, short.Scope, true); rerr == nil && n > 0 {
		if grants, err = q.tryAcquire(ctx, req); !errors.Is(err, ErrQuotaInsufficient) {
			return grants, err
		}
		errors.As(err, &short)
	}
	for _, w := range req.Wants {
		if w.Scope == short.Scope && ToMicros(w.Headroom) < quotaLowRemaining {
			if n, rerr := q.recaller.Recall(rctx, req.UserID, req.NodeID, short.Scope, false); rerr == nil && n > 0 {
				return q.tryAcquire(ctx, req)
			}
		}
	}
	return nil, err
}

func (q *Quotas) tryAcquire(ctx context.Context, req AcquireRequest) ([]QuotaGrant, error) {
	var grants []QuotaGrant
	err := q.withUser(ctx, req.UserID, func(tx LeaseTx) error {
		plans, err := q.plan(ctx, tx, req)
		if err != nil {
			return err
		}
		now := q.now()
		grants = grants[:0]
		for _, p := range plans {
			expires := now.Add(QuotaLeaseTTL)
			if p.want.ExpiresAt != nil && p.want.ExpiresAt.Before(expires) {
				expires = *p.want.ExpiresAt
			}
			if p.give <= 0 {
				// 这次不给钱，但这台已有这份租约：顺带续期（节点可能因为快到期才来申请）。
				if p.lockedHere != nil {
					l, err := tx.Renew(ctx, p.lockedHere.ID, expires, nil, now)
					if err != nil {
						return err
					}
					grants = append(grants, QuotaGrant{LeaseID: l.ID, Scope: l.LeaseScope, Granted: l.Granted, ExpiresAt: l.ExpiresAt})
				}
				continue
			}
			key := LeaseKey{UserID: req.UserID, NodeID: req.NodeID, LeaseScope: p.want.Scope}
			l, err := tx.Grant(ctx, key, p.give, expires, q.epoch, now)
			if err != nil {
				return err
			}
			grants = append(grants, QuotaGrant{LeaseID: l.ID, Scope: l.LeaseScope, Amount: p.give, Granted: l.Granted, ExpiresAt: l.ExpiresAt})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return grants, nil
}

// Release 节点退回一份租约里未使用的金额（闲置、收回确认）。amount 不超过租约现在锁着的。
func (q *Quotas) Release(ctx context.Context, nodeID, userID, leaseID int64, amount Micros) error {
	return q.withUser(ctx, userID, func(tx LeaseTx) error {
		l, err := q.ownLease(ctx, tx, nodeID, leaseID)
		if err != nil {
			return err
		}
		if amount > l.Granted {
			amount = l.Granted
		}
		if amount <= 0 {
			return nil
		}
		_, err = tx.Reduce(ctx, leaseID, amount, q.now())
		return err
	})
}

// Renew 续期一份租约，记下节点报告的最后使用时间。有效期不超过 limit（订阅、Key 到期）。
func (q *Quotas) Renew(ctx context.Context, nodeID, userID, leaseID int64, lastUsedAt *time.Time, limit *time.Time) (time.Time, error) {
	var expires time.Time
	err := q.withUser(ctx, userID, func(tx LeaseTx) error {
		if _, err := q.ownLease(ctx, tx, nodeID, leaseID); err != nil {
			return err
		}
		now := q.now()
		expires = now.Add(QuotaLeaseTTL)
		if limit != nil && limit.Before(expires) {
			expires = *limit
		}
		_, err := tx.Renew(ctx, leaseID, expires, lastUsedAt, now)
		return err
	})
	return expires, err
}

// CloseLease 关闭一份租约（节点报告没有这份租约、收回确认全部退回），剩余金额全部放回。
func (q *Quotas) CloseLease(ctx context.Context, nodeID, userID, leaseID int64, status LeaseStatus, reason string) (Micros, error) {
	var returned Micros
	err := q.withUser(ctx, userID, func(tx LeaseTx) error {
		if _, err := q.ownLease(ctx, tx, nodeID, leaseID); err != nil {
			return err
		}
		var err error
		returned, err = tx.Close(ctx, leaseID, status, reason, q.now())
		return err
	})
	return returned, err
}

func (q *Quotas) ownLease(ctx context.Context, tx LeaseTx, nodeID, leaseID int64) (*Lease, error) {
	active, err := tx.Active(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range active {
		if l.ID == leaseID && l.NodeID == nodeID {
			return l, nil
		}
	}
	return nil, ErrLeaseNotFound
}

// VoidNode 作废一台节点的全部租约（停用节点、管理员按节点立即回收，设计 4.4）。
// 被作废的租约节点不能再用；之后补报的已发生用量照常扣（设计 5.4）。
func (q *Quotas) VoidNode(ctx context.Context, nodeID int64, reason string) (Micros, error) {
	leases, err := q.store.ListActiveByNode(ctx, nodeID)
	if err != nil {
		return 0, err
	}
	return q.closeLeases(ctx, leases, LeaseVoided, reason)
}

// ExpireDue 回收到期未续的租约（节点失联，设计 4.2）。返回回收的份数。
// 一次取 500 份，取满就接着取（一台失联节点可能持有很多用户的租约），最多 20 批，
// 防止关不掉的租约（用户已删除）让这一轮停不下来。
func (q *Quotas) ExpireDue(ctx context.Context) (int, error) {
	const batch, maxBatches = 500, 20
	total := 0
	for i := 0; i < maxBatches; i++ {
		leases, err := q.store.ListExpired(ctx, q.now(), batch)
		if err != nil {
			return total, err
		}
		if _, err := q.closeLeases(ctx, leases, LeaseExpired, "lease_expired"); err != nil {
			return total, err
		}
		total += len(leases)
		if len(leases) < batch {
			break
		}
	}
	return total, nil
}

func (q *Quotas) closeLeases(ctx context.Context, leases []*Lease, status LeaseStatus, reason string) (Micros, error) {
	var total Micros
	for _, l := range leases {
		err := q.withUser(ctx, l.UserID, func(tx LeaseTx) error {
			n, err := tx.Close(ctx, l.ID, status, reason, q.now())
			if errors.Is(err, ErrLeaseNotFound) {
				return nil // 已经被别的路径关掉
			}
			total += n
			return err
		})
		if errors.Is(err, ErrLeaseUserNotFound) {
			continue
		}
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// RunExpiry 每 30 秒回收一次到期租约，直到 ctx 结束。
func (q *Quotas) RunExpiry(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := q.ExpireDue(ctx); err != nil {
				slog.Warn("relay lease expiry failed", "error", err)
			} else if n > 0 {
				slog.Info("relay leases expired", "count", n)
			}
		}
	}
}

// Proto 把给出的额度转成下发给节点的消息（选号回复里带上，WP7）。
func (g QuotaGrant) Proto(userID int64) *relayv1.QuotaGrant {
	return &relayv1.QuotaGrant{
		LeaseId: g.LeaseID, UserId: userID, Amount: g.Amount, Granted: g.Granted, ExpiresAtUnixMs: g.ExpiresAt.UnixMilli(),
		Scope: &relayv1.QuotaScope{Dimension: g.Scope.Dimension, ScopeId: g.Scope.ScopeID, ScopeKey: g.Scope.ScopeKey},
	}
}
