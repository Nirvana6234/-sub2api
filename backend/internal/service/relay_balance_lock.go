package service

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ErrBalanceLockedOnRelay：这次扣减会动到锁在主从分流从节点上的余额（设计 4.3、4.4）。
// 仓储层在"减完后低于冻结额"时返回它；服务层先从在线节点收回再试，仍不够就给管理员 409。
var ErrBalanceLockedOnRelay = errors.New("balance is locked on relay nodes")

// RelayBalanceReclaimer 从在线从节点同步收回某个用户锁着的余额（主从分流运行时实现）。
// 返回收回后仍锁着的金额（在离线节点上），以及最晚什么时候会因租约到期放回。
type RelayBalanceReclaimer interface {
	ReclaimBalance(ctx context.Context, userID int64) (stillLocked float64, releaseBy time.Time, err error)
}

type relayBalanceReclaimerHolder struct{ r RelayBalanceReclaimer }

// relayBalanceReclaimer 是进程级的挂接点：主节点只有一个进程，主从分流运行时开着时挂上、
// 关闭时摘下。管理员改余额、退款的服务没有共同的依赖，挂在这里不用改一串构造函数。
var relayBalanceReclaimer atomic.Pointer[relayBalanceReclaimerHolder]

// SetRelayBalanceReclaimer 挂上（nil 表示摘下）余额收回。
func SetRelayBalanceReclaimer(r RelayBalanceReclaimer) {
	if r == nil {
		relayBalanceReclaimer.Store(nil)
		return
	}
	relayBalanceReclaimer.Store(&relayBalanceReclaimerHolder{r: r})
}

// relayBalanceLockedConflict 是给管理员的 409：锁着多少、最晚什么时候放回。
// 管理员可以等，也可以在从节点管理页 / 用户管理页"立即回收"（设计 4.4）。
func relayBalanceLockedConflict(locked float64, releaseBy time.Time) error {
	md := map[string]string{"locked_amount": strconv.FormatFloat(locked, 'f', -1, 64)}
	if !releaseBy.IsZero() {
		md["release_by"] = releaseBy.UTC().Format(time.RFC3339)
	}
	return infraerrors.Conflict("RELAY_BALANCE_LOCKED",
		"part of the balance is locked on relay nodes that are offline; wait until release_by or reclaim it now").
		WithMetadata(md).WithCause(ErrBalanceLockedOnRelay)
}

// withRelayBalanceReclaim 执行一次会减余额的操作；因为余额锁在从节点上而失败时，
// 先从在线节点收回再试一次，仍然不够返回 409。
func withRelayBalanceReclaim(ctx context.Context, userID int64, op func() error) error {
	err := op()
	if !errors.Is(err, ErrBalanceLockedOnRelay) {
		return err
	}
	h := relayBalanceReclaimer.Load()
	if h == nil {
		return relayBalanceLockedConflict(0, time.Time{})
	}
	locked, releaseBy, rerr := h.r.ReclaimBalance(ctx, userID)
	if rerr != nil {
		return relayBalanceLockedConflict(locked, releaseBy)
	}
	if err = op(); errors.Is(err, ErrBalanceLockedOnRelay) {
		return relayBalanceLockedConflict(locked, releaseBy)
	}
	return err
}

// reclaimRelayBalanceFor 在"余额本身够、只是被锁住"时先收回，返回是否收回过（调用方据此重读用户）。
func reclaimRelayBalanceFor(ctx context.Context, userID int64) (stillLocked float64, releaseBy time.Time, reclaimed bool) {
	h := relayBalanceReclaimer.Load()
	if h == nil {
		return 0, time.Time{}, false
	}
	locked, by, err := h.r.ReclaimBalance(ctx, userID)
	return locked, by, err == nil
}
