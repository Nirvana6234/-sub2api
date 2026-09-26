package node

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"golang.org/x/sync/singleflight"
)

// 从节点本地额度（设计 4.2、开发计划 3.1、WP6）。金额都是整数微单位（1 = 10⁻⁸）。
const (
	// quotaExpiryMargin：租约到期前这么久（再减去测得的时钟偏差）就停用，保证主节点收回时这里已不再用。
	quotaExpiryMargin = 30 * time.Second
	// quotaRefillBelow：本机余量低于上次补充后余量的这个比例时，后台提前补充。
	quotaRefillBelowPercent = 30
	// QuotaIdleAfter：多久没用算闲置（主节点只收回闲置额度时按它判断）。
	QuotaIdleAfter = 5 * time.Minute
)

// ErrQuotaInsufficient：本地额度不够、补充也没补到（返回余额不足）。
var ErrQuotaInsufficient = errors.New("relay local quota is insufficient")

// QuotaScope 是一项子额度（与主节点的 LeaseScope 对应）。
type QuotaScope struct {
	Dimension string
	ScopeID   int64
	ScopeKey  string
}

func scopeFromProto(s *relayv1.QuotaScope) QuotaScope {
	return QuotaScope{Dimension: s.GetDimension(), ScopeID: s.GetScopeId(), ScopeKey: s.GetScopeKey()}
}

type quotaKey struct {
	UserID int64
	QuotaScope
}

// QuotaWantReport 是补充申请里的一项：子额度和这台手里还没用掉的金额。
type QuotaWantReport struct {
	Scope  QuotaScope
	Unused int64
}

// QuotaRefiller 向主节点补充额度。真实实现随选号一起（WP7）；这里抽象出来，测试用假的主节点。
type QuotaRefiller interface {
	Refill(ctx context.Context, userID int64, wants []QuotaWantReport, need int64) ([]*relayv1.QuotaGrant, error)
}

// quotaEntry 是一个用户的一项子额度在本机的状态。余量用原子整数，预扣用 CAS，不加锁。
type quotaEntry struct {
	remaining  atomic.Int64 // 还没用掉、也没被进行中请求预扣的
	peak       atomic.Int64 // 上次补充后的余量（提前补充的阈值按它算）
	leaseID    atomic.Int64
	expiresMs  atomic.Int64
	lastUsedMs atomic.Int64
	// returnedTotal 是这份租约至今累计退回（主节点把它记在租约上，只处理多出来的部分）。
	// 随租约存在，纪元变化不清零；换了新租约才从 0 算。
	returnedTotal atomic.Int64
	// confirmedTotal 是其中主节点已回复确认的。只用来决定要不要重发（钱已从余量取出、
	// 请求没送到时下次一并带上），不参与任何计算；主节点按租约上的累计去重，重发不会多退。
	confirmedTotal atomic.Int64
}

func (e *quotaEntry) tryTake(amount int64) bool {
	for {
		cur := e.remaining.Load()
		if cur < amount {
			return false
		}
		if e.remaining.CompareAndSwap(cur, cur-amount) {
			return true
		}
	}
}

// takeAll 取走全部未用的（收回、闲置退回）；余量为负（超支）时什么也不取。
func (e *quotaEntry) takeAll() int64 {
	for {
		cur := e.remaining.Load()
		if cur <= 0 {
			return 0
		}
		if e.remaining.CompareAndSwap(cur, 0) {
			return cur
		}
	}
}

// LocalQuota 是从节点的本地额度。
type LocalQuota struct {
	refiller QuotaRefiller
	now      func() time.Time
	// skew 返回测得的主从时钟偏差（绝对值），停用时间点按它再提前。
	skew func() time.Duration

	mu      sync.RWMutex
	entries map[quotaKey]*quotaEntry
	byLease map[int64]quotaKey

	sf singleflight.Group
	// refillLocks 让同一用户的补充在本机串行：不同请求用到的子额度组合可能重叠（都含余额），
	// 并发补充会各自报告同一个旧的"未用"，主节点就会在重叠的那项上给两份每台上限。
	refillLocks [64]sync.Mutex
}

// NewLocalQuota 创建本地额度。
func NewLocalQuota(refiller QuotaRefiller, now func() time.Time, skew func() time.Duration) *LocalQuota {
	if now == nil {
		now = time.Now
	}
	if skew == nil {
		skew = func() time.Duration { return 0 }
	}
	return &LocalQuota{refiller: refiller, now: now, skew: skew, entries: map[quotaKey]*quotaEntry{}, byLease: map[int64]quotaKey{}}
}

func (q *LocalQuota) entry(k quotaKey) *quotaEntry {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.entries[k]
}

// usable：离到期还有"30 秒 + 时钟偏差"以上才用。
func (q *LocalQuota) usable(e *quotaEntry) bool {
	deadline := time.UnixMilli(e.expiresMs.Load()).Add(-quotaExpiryMargin - q.skew())
	return q.now().Before(deadline)
}

// Reservation 是一次请求的预扣。
type Reservation struct {
	q       *LocalQuota
	entries []*quotaEntry
	amount  int64
	done    atomic.Bool
}

// Reserve 为一个请求在它用到的每一项子额度上预扣 need（最低预估）。有一项不够就把已扣的退回、
// 合并补充一次再试；仍不够返回 ErrQuotaInsufficient。没有上限的维度不要传进来。
func (q *LocalQuota) Reserve(ctx context.Context, userID int64, scopes []QuotaScope, need int64) (*Reservation, error) {
	for attempt := 0; attempt < 2; attempt++ {
		taken := make([]*quotaEntry, 0, len(scopes))
		ok := true
		for _, s := range scopes {
			e := q.entry(quotaKey{UserID: userID, QuotaScope: s})
			if e == nil || !q.usable(e) || !e.tryTake(need) {
				ok = false
				break
			}
			taken = append(taken, e)
		}
		if ok {
			for _, e := range taken {
				if e.remaining.Load()*100 < e.peak.Load()*quotaRefillBelowPercent {
					go func() { _ = q.refill(context.Background(), userID, scopes, need) }()
					break
				}
			}
			return &Reservation{q: q, entries: taken, amount: need}, nil
		}
		for _, e := range taken {
			e.remaining.Add(need)
		}
		if attempt == 0 {
			if err := q.refill(ctx, userID, scopes, need); err != nil {
				return nil, err
			}
		}
	}
	return nil, ErrQuotaInsufficient
}

// Settle 按实际费用结算：多退少补。实际超出预估时余量可以变成负数（进行中的请求不中途切断，
// 设计 4.5），之后要补充成功才放行下一个请求。只生效一次。
func (r *Reservation) Settle(actual int64) {
	if r == nil || !r.done.CompareAndSwap(false, true) {
		return
	}
	now := r.q.now().UnixMilli()
	for _, e := range r.entries {
		e.remaining.Add(r.amount - actual)
		e.lastUsedMs.Store(now)
	}
}

// Cancel 请求没用上额度（比如上游没开始就失败）：全部退回。
func (r *Reservation) Cancel() { r.Settle(0) }

// refill 合并补充：同一用户、同一组子额度同时只有一次在途。
func (q *LocalQuota) refill(ctx context.Context, userID int64, scopes []QuotaScope, need int64) error {
	keys := make([]string, 0, len(scopes))
	for _, s := range scopes {
		keys = append(keys, s.Dimension+"/"+strconv.FormatInt(s.ScopeID, 10)+"/"+s.ScopeKey)
	}
	sort.Strings(keys)
	key := strconv.FormatInt(userID, 10) + "|" + strings.Join(keys, ",")
	_, err, _ := q.sf.Do(key, func() (any, error) {
		lock := &q.refillLocks[uint64(userID)%uint64(len(q.refillLocks))]
		lock.Lock()
		defer lock.Unlock()
		// 拿到锁之后再读"未用"：前一个补充的结果已经算进去了。
		wants := make([]QuotaWantReport, 0, len(scopes))
		for _, s := range scopes {
			var unused int64
			if e := q.entry(quotaKey{UserID: userID, QuotaScope: s}); e != nil {
				if v := e.remaining.Load(); v > 0 {
					unused = v
				}
			}
			wants = append(wants, QuotaWantReport{Scope: s, Unused: unused})
		}
		grants, err := q.refiller.Refill(ctx, userID, wants, need)
		if err != nil {
			return nil, err
		}
		q.ApplyGrants(grants)
		return nil, nil
	})
	return err
}

// ApplyGrants 应用主节点给的额度（补充的回复、选号回复里顺带的）。
func (q *LocalQuota) ApplyGrants(grants []*relayv1.QuotaGrant) {
	for _, g := range grants {
		k := quotaKey{UserID: g.GetUserId(), QuotaScope: scopeFromProto(g.GetScope())}
		q.mu.Lock()
		e := q.entries[k]
		if e == nil {
			e = &quotaEntry{}
			q.entries[k] = e
		}
		if old := e.leaseID.Load(); old != g.GetLeaseId() {
			// 新租约（旧的已被主节点关闭）：累计退回从 0 算。
			delete(q.byLease, old)
			e.leaseID.Store(g.GetLeaseId())
			e.returnedTotal.Store(0)
			e.confirmedTotal.Store(0)
		}
		q.byLease[g.GetLeaseId()] = k
		q.mu.Unlock()
		after := e.remaining.Add(g.GetAmount())
		e.peak.Store(after)
		e.expiresMs.Store(g.GetExpiresAtUnixMs())
	}
}

// Recall 处理主节点的收回：闲置判断在本机（idle_only 时最近 5 分钟用过就不退）；
// 退回全部未用的，回复里带累计退回。
func (q *LocalQuota) Recall(rc *relayv1.QuotaRecall) []*relayv1.LeaseReturn {
	k := quotaKey{UserID: rc.GetUserId(), QuotaScope: scopeFromProto(rc.GetScope())}
	e := q.entry(k)
	if e == nil || e.leaseID.Load() == 0 {
		return nil
	}
	if rc.GetIdleOnly() && q.now().Sub(time.UnixMilli(e.lastUsedMs.Load())) < QuotaIdleAfter {
		return nil
	}
	return []*relayv1.LeaseReturn{q.giveBack(k.UserID, e)}
}

func (q *LocalQuota) giveBack(userID int64, e *quotaEntry) *relayv1.LeaseReturn {
	n := e.takeAll()
	total := e.returnedTotal.Add(n)
	return &relayv1.LeaseReturn{LeaseId: e.leaseID.Load(), UserId: userID, ReturnedTotal: total}
}

// IdleReturns 取出闲置超过 idleAfter 的额度，交给 ReleaseQuota 退回。
func (q *LocalQuota) IdleReturns(idleAfter time.Duration) []*relayv1.LeaseReturn {
	now := q.now()
	q.mu.RLock()
	defer q.mu.RUnlock()
	var out []*relayv1.LeaseReturn
	for k, e := range q.entries {
		if e.leaseID.Load() == 0 || e.remaining.Load() <= 0 || now.Sub(time.UnixMilli(e.lastUsedMs.Load())) < idleAfter {
			continue
		}
		out = append(out, q.giveBack(k.UserID, e))
	}
	return out
}

// PendingReturns 是已从余量取出、但主节点还没确认的退回（上次没送到），和下一次退回一起重发。
func (q *LocalQuota) PendingReturns() []*relayv1.LeaseReturn {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var out []*relayv1.LeaseReturn
	for id, k := range q.byLease {
		e := q.entries[k]
		if total := e.returnedTotal.Load(); total > e.confirmedTotal.Load() {
			out = append(out, &relayv1.LeaseReturn{LeaseId: id, UserId: k.UserID, ReturnedTotal: total})
		}
	}
	return out
}

// ConfirmReturned 记下主节点已确认的累计退回（ReleaseQuota、AckQuotaRecall 成功后）。
func (q *LocalQuota) ConfirmReturned(rets []*relayv1.LeaseReturn) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	for _, r := range rets {
		k, ok := q.byLease[r.GetLeaseId()]
		if !ok {
			continue
		}
		e := q.entries[k]
		for {
			cur := e.confirmedTotal.Load()
			if r.GetReturnedTotal() <= cur || e.confirmedTotal.CompareAndSwap(cur, r.GetReturnedTotal()) {
				break
			}
		}
	}
}

// Renewals 是续期请求：每份租约和它的最后使用时间。
func (q *LocalQuota) Renewals() []*relayv1.LeaseRenewal {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]*relayv1.LeaseRenewal, 0, len(q.byLease))
	for id, k := range q.byLease {
		out = append(out, &relayv1.LeaseRenewal{LeaseId: id, UserId: k.UserID, LastUsedAtUnixMs: q.entries[k].lastUsedMs.Load()})
	}
	return out
}

// ApplyRenewed 更新续期后的到期时间。
func (q *LocalQuota) ApplyRenewed(renewed []*relayv1.RenewedLease) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	for _, r := range renewed {
		if k, ok := q.byLease[r.GetLeaseId()]; ok {
			q.entries[k].expiresMs.Store(r.GetExpiresAtUnixMs())
		}
	}
}

// Drop 丢弃主节点已不认的租约：不再使用（被作废的额度不能再用，设计 4.4）。
func (q *LocalQuota) Drop(leaseIDs []int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, id := range leaseIDs {
		if k, ok := q.byLease[id]; ok {
			delete(q.byLease, id)
			delete(q.entries, k)
		}
	}
}

// Held 是重连核对时上报的租约。
func (q *LocalQuota) Held() []*relayv1.HeldLease {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]*relayv1.HeldLease, 0, len(q.byLease))
	for id, k := range q.byLease {
		out = append(out, &relayv1.HeldLease{LeaseId: id, UserId: k.UserID})
	}
	return out
}

// Reset 清空（进程重启等价：内存里的额度没了，重连核对时主节点把没上报的全部放回）。
func (q *LocalQuota) Reset() {
	q.mu.Lock()
	q.entries = map[quotaKey]*quotaEntry{}
	q.byLease = map[int64]quotaKey{}
	q.mu.Unlock()
}

// Unused 返回某一项还没用掉的金额（诊断和测试用）。
func (q *LocalQuota) Unused(userID int64, s QuotaScope) int64 {
	if e := q.entry(quotaKey{UserID: userID, QuotaScope: s}); e != nil {
		return e.remaining.Load()
	}
	return 0
}
