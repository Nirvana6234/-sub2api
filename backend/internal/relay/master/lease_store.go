package master

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Micros 是主从之间和额度计算用的整数金额：1 = 10⁻⁸（开发计划 2.4）。
// relay_quota_leases.granted、users.relay_reserved_balance 是 DECIMAL(20,8)，与它一一对应。
type Micros = int64

// MicrosPerUnit 是 1 个余额单位对应的微单位数。
const MicrosPerUnit Micros = 100_000_000

// ToMicros 把浮点金额换成微单位，向下取整（锁定额度宁少勿多）。
// 加一个远小于 1 微单位的量，抵消 5.0 × 10⁸ 算成 499999999.99… 这类浮点误差。
func ToMicros(v float64) Micros {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return Micros(math.Floor(v*float64(MicrosPerUnit) + 1e-4))
}

// FromMicros 把微单位换回浮点金额（交给现有扣费函数、余额判断用）。
func FromMicros(m Micros) float64 { return float64(m) / float64(MicrosPerUnit) }

// LeaseStatus 与 relay_quota_leases.status 一致。
type LeaseStatus string

const (
	LeaseActive   LeaseStatus = "active"
	LeaseReleased LeaseStatus = "released" // 节点退回（闲置、收回、节点报告没有这份租约）
	LeaseExpired  LeaseStatus = "expired"  // 到期未续，主节点单方面收回
	LeaseVoided   LeaseStatus = "voided"   // 作废（停用节点、管理员立即回收）
)

// LeaseScope 标明一项子额度（设计 4.1）：维度 + 归属。
// 订阅窗口的 ScopeID 是分组 ID；Key 维度是 API Key ID；平台配额的 ScopeKey 是平台名；余额都为零值。
type LeaseScope struct {
	Dimension string
	ScopeID   int64
	ScopeKey  string
}

// LeaseKey 标明一份租约：一台节点为一个用户持有的一项子额度。同一个键同时只有一份生效租约。
type LeaseKey struct {
	UserID int64
	NodeID int64
	LeaseScope
}

// Lease 是一份额度租约。Granted 是这份租约当前还锁着、尚未入账的金额（含节点已用、还没回报扣费的部分），
// 入账、退回、收回时减少。
type Lease struct {
	ID int64
	LeaseKey
	Granted Micros
	// ReturnedTotal 是节点至今累计退回的金额（随租约持久化，设计 4.2）：退回只处理比它多出来的部分。
	ReturnedTotal Micros
	Status        LeaseStatus
	ExpiresAt     time.Time
	MasterEpoch   string
	LastUsedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ClosedAt      *time.Time
	CloseReason   string
}

var (
	// ErrLeaseNotFound：没有这份生效中的租约（已关闭或不属于这个用户）。
	ErrLeaseNotFound = errors.New("relay quota lease not found")
	// ErrLeaseUserNotFound：用户不存在（已删除）。
	ErrLeaseUserNotFound = errors.New("relay quota lease user not found")
	// ErrLeaseAmount：减去的金额为负或超过租约当前锁着的金额。
	ErrLeaseAmount = errors.New("relay quota lease amount is out of range")
)

// LeaseStore 是额度租约的持久化（内存实现给测试，SQL 实现在 internal/repository）。实现必须保证：
//   - WithUser 里的所有改动在同一个事务里，并与同一用户的其他 WithUser 串行（SQL 实现锁用户行）；
//   - 余额维度（service.QuotaDimBalance）的锁定金额变化同步写进 users.relay_reserved_balance，
//     两者在任何提交后的时刻都相等；
//   - 同一个 LeaseKey 同时只有一份 active 租约，补充在原租约上加。
type LeaseStore interface {
	WithUser(ctx context.Context, userID int64, fn func(tx LeaseTx) error) error
	// ListActiveByNode 列出一台节点的生效租约（停用节点、纪元核对用）。
	ListActiveByNode(ctx context.Context, nodeID int64) ([]*Lease, error)
	// ListActiveByScope 列出某些维度、某个归属 ID 的生效租约（分组改动时收回这个分组的订阅额度），
	// userID 大于 0 时只列这个用户的。
	ListActiveByScope(ctx context.Context, dimensions []string, scopeID int64, userID int64) ([]*Lease, error)
	// ListExpired 列出到期时间早于 before 的生效租约（到期回收用），最多 limit 条。
	ListExpired(ctx context.Context, before time.Time, limit int) ([]*Lease, error)
	// ReservedBalances 返回所有冻结额大于 0 的用户（主节点启动时载入内存）。
	ReservedBalances(ctx context.Context) (map[int64]Micros, error)
}

// LeaseTx 是一个用户的租约事务。
type LeaseTx interface {
	// Active 返回这个用户的全部生效租约（所有节点、所有维度）。
	Active(ctx context.Context) ([]*Lease, error)
	// Grant 给 key 加锁 amount（> 0），没有生效租约时新建；到期时间更新为 expiresAt。
	Grant(ctx context.Context, key LeaseKey, amount Micros, expiresAt time.Time, epoch string, now time.Time) (*Lease, error)
	// Reduce 从生效租约里减去 amount（入账），不能超过 Granted。
	Reduce(ctx context.Context, leaseID int64, amount Micros, now time.Time) (*Lease, error)
	// ApplyReturned 处理节点的退回：returnedTotal 是节点报告的这份租约累计退回。只减去比已记录的
	// 累计多出来的那一截（不超过 Granted），并把累计记成两者中较大的。返回这次实际减去的金额。
	// 同一个累计值重复到达（重发、回复丢失、主节点重启后）只生效一次。
	ApplyReturned(ctx context.Context, leaseID int64, returnedTotal Micros, now time.Time) (Micros, error)
	// Close 关闭生效租约，剩下的 Granted 全部放回，返回放回的金额。
	Close(ctx context.Context, leaseID int64, status LeaseStatus, reason string, now time.Time) (Micros, error)
	// Renew 续期，并记下节点报告的最后使用时间（闲置判断用）。
	Renew(ctx context.Context, leaseID int64, expiresAt time.Time, lastUsedAt *time.Time, now time.Time) (*Lease, error)
}

// isBalanceDimension 报告这个维度是否计入 users.relay_reserved_balance。
func isBalanceDimension(dim string) bool { return dim == service.QuotaDimBalance }
