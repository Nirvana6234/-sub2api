package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 充值余额有效期。
//
// 规则：开关打开后，每笔「充值」（支付订单、卡密，最终都走余额类兑换码）入账时记一个批次，
// 从入账当刻起 N 天内有效，到期时批次里还没用掉的部分由后台清零。开关打开之前的余额、
// 赠送、管理员调整等不在任何批次里，一律视为永久，沿用旧规则。
//
// 实现：users.balance 仍是唯一的总余额，计费热路径（扣费、冻结、退款）不用改。
// 批次的已用量不在每次扣费时维护，而是需要时用总余额倒推：
//
//	总余额 = 永久部分 + 各批次剩余
//
// 扣费先扣先到期的批次，批次扣完才动永久部分，所以「总余额比上次对账时少了多少」
// 就是这段时间的消耗，按到期先后摊给各批次即可（reconcileBalanceLots）。
// 反过来，总余额比对账时多出来而又没有批次记录的部分（管理员加款、返利、赠送……）
// 一律计入永久部分。所以任何会增加余额的路径都必须先对账再加（SyncBalanceLotsBeforeCredit），
// 否则「先花后加」的净变化会把已花掉的批次额度误当成没花。

const (
	SettingKeyBalanceExpiryEnabled = "balance_expiry_enabled"
	SettingKeyBalanceExpiryDays    = "balance_expiry_days"

	BalanceExpiryDefaultDays = 30
	BalanceExpiryMinDays     = 1
	BalanceExpiryMaxDays     = 3650

	balanceLotSourceRecharge = "recharge"

	balanceLotActive   = "active"
	balanceLotDepleted = "depleted"
	balanceLotExpired  = "expired"

	// 余额是 float64，对账时低于这个量级的差值按 0 处理。
	balanceLotEpsilon = 1e-8

	// 单轮清零最多处理多少个用户，剩下的下一轮（每分钟一轮）接着做。
	balanceExpirySweepBatch = 200
)

var ErrBalanceExpiryDaysInvalid = infraerrors.BadRequest("BALANCE_EXPIRY_DAYS_INVALID",
	fmt.Sprintf("balance expiry days must be between %d and %d", BalanceExpiryMinDays, BalanceExpiryMaxDays))

// BalanceExpiryConfig 是管理员在后台设置的开关与天数。
type BalanceExpiryConfig struct {
	Enabled bool `json:"enabled"`
	Days    int  `json:"days"`
}

// BalanceLotInfo 是返回给前端的一个批次。
type BalanceLotInfo struct {
	ID            int64      `json:"id"`
	SourceRef     string     `json:"source_ref,omitempty"`
	Amount        float64    `json:"amount"`
	Remaining     float64    `json:"remaining"`
	ExpiredAmount float64    `json:"expired_amount"`
	Status        string     `json:"status"`
	CreditedAt    time.Time  `json:"credited_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	SettledAt     *time.Time `json:"settled_at,omitempty"`
}

// UserBalanceExpiryView 是用户「余额有效期」页面的数据。
type UserBalanceExpiryView struct {
	Enabled bool `json:"enabled"`
	Days    int  `json:"days"`
	// 永久部分：升级前的存量、赠送等，不会过期。
	PermanentBalance float64 `json:"permanent_balance"`
	// 会过期的部分：各有效批次的剩余之和。
	ExpiringBalance float64 `json:"expiring_balance"`
	// 最早一批会过期的时间，没有有效批次时为空。
	NextExpiresAt *time.Time `json:"next_expires_at,omitempty"`
	// 仍有余额的有效批次，先到期的在前。
	Lots []BalanceLotInfo `json:"lots"`
	// 最近已到期清零的批次。
	Expired []BalanceLotInfo `json:"expired"`
}

// reconcileBalanceLots 把「当前总余额」对齐到「永久部分 + 各批次剩余」，返回新的永久部分
// 和各批次的新剩余（lots 须已按到期先后排好序）。
func reconcileBalanceLots(balance, permanent float64, remaining []float64) (float64, []float64) {
	out := append([]float64(nil), remaining...)
	if permanent < 0 {
		permanent = 0
	}
	available := math.Max(balance, 0)
	var sum float64
	for _, r := range out {
		sum += r
	}
	// consumed > 0：上次对账以来花掉的；< 0：多出来的余额，没有批次认领，归永久部分。
	consumed := permanent + sum - available
	if math.Abs(consumed) < balanceLotEpsilon {
		return permanent, out
	}
	if consumed < 0 {
		return permanent - consumed, out
	}
	for i := range out {
		take := math.Min(out[i], consumed)
		out[i] -= take
		consumed -= take
	}
	return math.Max(permanent-consumed, 0), out
}

// BalanceExpiryService 管理充值余额的有效期。
type BalanceExpiryService struct {
	entClient            *dbent.Client
	settingRepo          SettingRepository
	billingCacheService  *BillingCacheService
	authCacheInvalidator APIKeyAuthCacheInvalidator
	now                  func() time.Time
}

func NewBalanceExpiryService(
	entClient *dbent.Client,
	settingRepo SettingRepository,
	billingCacheService *BillingCacheService,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
) *BalanceExpiryService {
	return &BalanceExpiryService{
		entClient:            entClient,
		settingRepo:          settingRepo,
		billingCacheService:  billingCacheService,
		authCacheInvalidator: authCacheInvalidator,
		now:                  time.Now,
	}
}

// GetConfig 读取开关与天数；读不到或不合法时按「关闭、30 天」处理。
func (s *BalanceExpiryService) GetConfig(ctx context.Context) BalanceExpiryConfig {
	cfg := BalanceExpiryConfig{Days: BalanceExpiryDefaultDays}
	if s == nil || s.settingRepo == nil {
		return cfg
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyBalanceExpiryEnabled, SettingKeyBalanceExpiryDays})
	if err != nil {
		return cfg
	}
	cfg.Enabled = strings.TrimSpace(values[SettingKeyBalanceExpiryEnabled]) == "true"
	if days, convErr := strconv.Atoi(strings.TrimSpace(values[SettingKeyBalanceExpiryDays])); convErr == nil &&
		days >= BalanceExpiryMinDays && days <= BalanceExpiryMaxDays {
		cfg.Days = days
	}
	return cfg
}

// SetConfig 保存开关与天数。天数只影响之后的充值，已入账批次的到期时间不变。
func (s *BalanceExpiryService) SetConfig(ctx context.Context, cfg BalanceExpiryConfig) error {
	if cfg.Days < BalanceExpiryMinDays || cfg.Days > BalanceExpiryMaxDays {
		return ErrBalanceExpiryDaysInvalid
	}
	return s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyBalanceExpiryEnabled: strconv.FormatBool(cfg.Enabled),
		SettingKeyBalanceExpiryDays:    strconv.Itoa(cfg.Days),
	})
}

// balanceLotRow 是对账时读出的批次。
type balanceLotRow struct {
	id        int64
	remaining float64
	expiresAt time.Time
}

func balanceLotDB(ctx context.Context, base *dbent.Client) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return base
}

// withBalanceLotTx 在已有事务里直接执行，否则自己开一个事务。
func withBalanceLotTx(ctx context.Context, base *dbent.Client, fn func(db *dbent.Client) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(tx.Client())
	}
	tx, err := base.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin balance lot tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx.Client()); err != nil {
		return err
	}
	return tx.Commit()
}

func lockBalanceLotUser(ctx context.Context, db *dbent.Client, userID int64) (balance, permanent float64, err error) {
	rows, err := db.QueryContext(ctx,
		`SELECT balance, permanent_balance FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, userID)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	if !rows.Next() {
		if rowsErr := rows.Err(); rowsErr != nil {
			return 0, 0, rowsErr
		}
		return 0, 0, ErrUserNotFound
	}
	if err := rows.Scan(&balance, &permanent); err != nil {
		return 0, 0, err
	}
	return balance, permanent, rows.Err()
}

// balanceLockedOnRelayNodes 返回锁在主从分流从节点上的余额（users.relay_reserved_balance，设计 4.3）。
func balanceLockedOnRelayNodes(ctx context.Context, db *dbent.Client, userID int64) (locked float64, err error) {
	rows, err := db.QueryContext(ctx,
		`SELECT COALESCE(relay_reserved_balance, 0) FROM users WHERE id = $1 AND deleted_at IS NULL`, userID)
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	if !rows.Next() {
		return 0, rows.Err()
	}
	if err := rows.Scan(&locked); err != nil {
		return 0, err
	}
	return locked, rows.Err()
}

func loadActiveBalanceLots(ctx context.Context, db *dbent.Client, userID int64, lock bool) (lots []balanceLotRow, err error) {
	query := `SELECT id, remaining, expires_at FROM balance_expiry_lots
		WHERE user_id = $1 AND status = 'active' ORDER BY expires_at, id`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		var lot balanceLotRow
		if err := rows.Scan(&lot.id, &lot.remaining, &lot.expiresAt); err != nil {
			return nil, err
		}
		lots = append(lots, lot)
	}
	return lots, rows.Err()
}

func hasActiveBalanceLots(ctx context.Context, db *dbent.Client, userID int64) (found bool, err error) {
	rows, err := db.QueryContext(ctx,
		`SELECT 1 FROM balance_expiry_lots WHERE user_id = $1 AND status = 'active' LIMIT 1`, userID)
	if err != nil {
		return false, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	found = rows.Next()
	return found, rows.Err()
}

// persistReconciledLots 把对账结果写回：批次剩余、用完的批次改状态、用户的永久部分。
func persistReconciledLots(ctx context.Context, db *dbent.Client, userID int64, permanent float64, lots []balanceLotRow, remaining []float64) error {
	for i, lot := range lots {
		next := remaining[i]
		if math.Abs(next-lot.remaining) < balanceLotEpsilon {
			continue
		}
		if next < balanceLotEpsilon {
			if _, err := db.ExecContext(ctx,
				`UPDATE balance_expiry_lots SET remaining = 0, status = 'depleted', settled_at = NOW() WHERE id = $1`, lot.id); err != nil {
				return err
			}
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE balance_expiry_lots SET remaining = $1 WHERE id = $2`, next, lot.id); err != nil {
			return err
		}
	}
	_, err := db.ExecContext(ctx, `UPDATE users SET permanent_balance = $1 WHERE id = $2`, permanent, userID)
	return err
}

// BalanceCreditGuard 守住一次「非充值」的加款（赠送、管理员加款、返利、退款回滚……）：
//
//	guard, err := BeginBalanceCredit(ctx, client, userID) // 加款前：把已花掉的部分摊给批次
//	... 把钱加到 users.balance ...
//	err = guard.Done(ctx, amount)                         // 加款后：这笔增量记入永久部分
//
// 两步缺一不可。只做前一步的话，加款之后用户继续消费，「加 5、花 4」会被净成「加 1」，
// 到期时就把花掉的 4 当成没花、多清用户的钱。
type BalanceCreditGuard struct {
	base    *dbent.Client
	userID  int64
	hasLots bool
}

// BeginBalanceCredit 在任何「增加余额」的写入之前调用。
// 用户没有有效批次时什么都不做（只多一次带索引的存在性查询），返回的 guard 的 Done 也是空操作。
func BeginBalanceCredit(ctx context.Context, base *dbent.Client, userID int64) (*BalanceCreditGuard, error) {
	guard := &BalanceCreditGuard{base: base, userID: userID}
	if base == nil {
		return guard, nil
	}
	found, err := hasActiveBalanceLots(ctx, balanceLotDB(ctx, base), userID)
	if err != nil {
		return nil, fmt.Errorf("check balance lots: %w", err)
	}
	if !found {
		return guard, nil
	}
	guard.hasLots = true
	return guard, syncBalanceLots(ctx, base, userID)
}

// Done 在加款成功之后调用：把增量记入永久部分。加款失败时不要调用。
func (g *BalanceCreditGuard) Done(ctx context.Context, amount float64) error {
	if g == nil || !g.hasLots || amount <= 0 {
		return nil
	}
	_, err := balanceLotDB(ctx, g.base).ExecContext(ctx,
		`UPDATE users SET permanent_balance = permanent_balance + $1 WHERE id = $2`, amount, g.userID)
	return err
}

// syncBalanceLots 在一个事务里把用户的消耗摊给各批次并写回。
func syncBalanceLots(ctx context.Context, base *dbent.Client, userID int64) error {
	return withBalanceLotTx(ctx, base, func(db *dbent.Client) error {
		balance, permanent, err := lockBalanceLotUser(ctx, db, userID)
		if err != nil {
			return err
		}
		lots, err := loadActiveBalanceLots(ctx, db, userID, true)
		if err != nil {
			return err
		}
		remaining := make([]float64, len(lots))
		for i, lot := range lots {
			remaining[i] = lot.remaining
		}
		newPermanent, newRemaining := reconcileBalanceLots(balance, permanent, remaining)
		return persistReconciledLots(ctx, db, userID, newPermanent, lots, newRemaining)
	})
}

// RecordRecharge 在一笔充值已经加到 users.balance 之后调用（须在同一个事务里），记下它的到期批次。
// 开关关闭时什么都不做。返回新批次的到期时间，没有记批次时为 nil。
//
// 先对账再加款由 UpdateBalance 保证；这里用「入账后余额 - 本笔 - 其余批次」倒推出永久部分，
// 不依赖 users.permanent_balance 里的旧值（用户没有批次时那个值本来就没人维护）。
func (s *BalanceExpiryService) RecordRecharge(ctx context.Context, userID int64, amount float64, sourceRef string) (*time.Time, error) {
	if s == nil || amount <= 0 {
		return nil, nil
	}
	cfg := s.GetConfig(ctx)
	if !cfg.Enabled {
		return nil, nil
	}
	now := s.now()
	expiresAt := now.AddDate(0, 0, cfg.Days)
	var recorded bool
	err := withBalanceLotTx(ctx, s.entClient, func(db *dbent.Client) error {
		balance, _, err := lockBalanceLotUser(ctx, db, userID)
		if err != nil {
			return err
		}
		lots, err := loadActiveBalanceLots(ctx, db, userID, true)
		if err != nil {
			return err
		}
		var others float64
		for _, lot := range lots {
			others += lot.remaining
		}
		res, err := db.ExecContext(ctx, `
			INSERT INTO balance_expiry_lots (user_id, source, source_ref, amount, remaining, credited_at, expires_at)
			VALUES ($1, $2, $3, $4, $4, $5, $6)
			ON CONFLICT DO NOTHING`, userID, balanceLotSourceRecharge, sourceRef, amount, now, expiresAt)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // 同一张单据已经记过
		}
		recorded = true
		permanent := math.Max(balance-amount-others, 0)
		_, err = db.ExecContext(ctx, `UPDATE users SET permanent_balance = $1 WHERE id = $2`, permanent, userID)
		return err
	})
	if err != nil || !recorded {
		return nil, err
	}
	return &expiresAt, nil
}

// SweepExpired 清零所有已到期批次里还没用掉的余额。返回清零的批次数和金额。
// 开关关闭期间不清零：批次保留，重新打开后已过期的会在下一轮一并清零。
func (s *BalanceExpiryService) SweepExpired(ctx context.Context) (lotsExpired int, amount float64, err error) {
	if s == nil || s.entClient == nil || !s.GetConfig(ctx).Enabled {
		return 0, 0, nil
	}
	now := s.now()
	rows, err := s.entClient.QueryContext(ctx, `
		SELECT DISTINCT user_id FROM balance_expiry_lots
		WHERE status = 'active' AND expires_at <= $1
		LIMIT $2`, now, balanceExpirySweepBatch)
	if err != nil {
		return 0, 0, err
	}
	var userIDs []int64
	for rows.Next() {
		var id int64
		if scanErr := rows.Scan(&id); scanErr != nil {
			_ = rows.Close()
			return 0, 0, scanErr
		}
		userIDs = append(userIDs, id)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return 0, 0, closeErr
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	var firstErr error
	for _, userID := range userIDs {
		n, amt, userErr := s.expireUser(ctx, userID, now)
		if userErr != nil {
			slog.Error("[BalanceExpiry] expire user failed", "user_id", userID, "error", userErr)
			if firstErr == nil {
				firstErr = userErr
			}
			continue
		}
		if n > 0 {
			lotsExpired += n
			amount += amt
			s.invalidateUserCaches(ctx, userID)
		}
	}
	return lotsExpired, amount, firstErr
}

func (s *BalanceExpiryService) expireUser(ctx context.Context, userID int64, now time.Time) (int, float64, error) {
	var count int
	var total float64
	err := withBalanceLotTx(ctx, s.entClient, func(db *dbent.Client) error {
		balance, permanent, err := lockBalanceLotUser(ctx, db, userID)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				// 用户已被删除，批次没有意义，直接作废。
				_, execErr := db.ExecContext(ctx,
					`UPDATE balance_expiry_lots SET status = 'depleted', remaining = 0, settled_at = NOW()
					 WHERE user_id = $1 AND status = 'active'`, userID)
				return execErr
			}
			return err
		}
		// 主从分流：余额有一部分锁在从节点上时这一轮不过期。锁着的钱可能正被节点花掉，过期扣减会让余额低于
		// 锁定额，之后入账时就成了透支；租约最长 10 分钟，下一轮清理再处理。
		locked, err := balanceLockedOnRelayNodes(ctx, db, userID)
		if err != nil {
			return err
		}
		if locked > balanceLotEpsilon {
			return nil
		}
		lots, err := loadActiveBalanceLots(ctx, db, userID, true)
		if err != nil {
			return err
		}
		remaining := make([]float64, len(lots))
		for i, lot := range lots {
			remaining[i] = lot.remaining
		}
		newPermanent, newRemaining := reconcileBalanceLots(balance, permanent, remaining)

		for i, lot := range lots {
			left := newRemaining[i]
			due := !lot.expiresAt.After(now)
			switch {
			case due && left >= balanceLotEpsilon:
				if _, err := db.ExecContext(ctx, `
					UPDATE balance_expiry_lots
					SET remaining = 0, expired_amount = $1, status = 'expired', settled_at = $2
					WHERE id = $3`, left, now, lot.id); err != nil {
					return err
				}
				total += left
				count++
				newRemaining[i] = 0
				lots[i].remaining = 0
			case due: // 到期时恰好用完
				newRemaining[i] = 0
				if _, err := db.ExecContext(ctx, `
					UPDATE balance_expiry_lots
					SET remaining = 0, status = 'depleted', settled_at = $1
					WHERE id = $2`, now, lot.id); err != nil {
					return err
				}
				lots[i].remaining = 0
			}
		}
		// 没到期的批次只写回对账结果；到期的上面已经处理过，这里 remaining 相同会被跳过。
		if err := persistReconciledLots(ctx, db, userID, newPermanent, lots, newRemaining); err != nil {
			return err
		}
		if total > 0 {
			if _, err := db.ExecContext(ctx,
				`UPDATE users SET balance = balance - $1, updated_at = NOW() WHERE id = $2`, total, userID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if count > 0 {
		slog.Info("[BalanceExpiry] expired balance", "user_id", userID, "lots", count, "amount", total)
	}
	return count, total, nil
}

func (s *BalanceExpiryService) invalidateUserCaches(ctx context.Context, userID int64) {
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if s.billingCacheService != nil {
		cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.billingCacheService.InvalidateUserBalance(cacheCtx, userID)
	}
}

// GetUserView 给用户看的余额构成。只读：在内存里对账，不写库。
func (s *BalanceExpiryService) GetUserView(ctx context.Context, userID int64) (*UserBalanceExpiryView, error) {
	cfg := s.GetConfig(ctx)
	view := &UserBalanceExpiryView{
		Enabled: cfg.Enabled,
		Days:    cfg.Days,
		Lots:    []BalanceLotInfo{},
		Expired: []BalanceLotInfo{},
	}

	rows, err := s.entClient.QueryContext(ctx, `
		SELECT u.balance, u.permanent_balance,
		       l.id, l.source_ref, l.amount, l.remaining, l.expired_amount, l.status, l.credited_at, l.expires_at, l.settled_at
		FROM users u
		LEFT JOIN balance_expiry_lots l ON l.user_id = u.id
		     AND (l.status = 'active' OR (l.status = 'expired' AND l.settled_at > NOW() - INTERVAL '90 days'))
		WHERE u.id = $1 AND u.deleted_at IS NULL
		ORDER BY l.expires_at, l.id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var balance, permanent float64
	found := false
	var active []BalanceLotInfo
	for rows.Next() {
		var (
			lotID                   sql.NullInt64
			ref, status             sql.NullString
			amount, rem, expiredAmt sql.NullFloat64
			creditedAt, expiresAt   sql.NullTime
			settledAt               sql.NullTime
		)
		if err := rows.Scan(&balance, &permanent, &lotID, &ref, &amount, &rem, &expiredAmt, &status, &creditedAt, &expiresAt, &settledAt); err != nil {
			return nil, err
		}
		found = true
		if !lotID.Valid {
			continue
		}
		info := BalanceLotInfo{
			ID: lotID.Int64, SourceRef: ref.String, Amount: amount.Float64, Remaining: rem.Float64,
			ExpiredAmount: expiredAmt.Float64, Status: status.String,
			CreditedAt: creditedAt.Time, ExpiresAt: expiresAt.Time,
		}
		if settledAt.Valid {
			t := settledAt.Time
			info.SettledAt = &t
		}
		if info.Status == balanceLotActive {
			active = append(active, info)
		} else {
			view.Expired = append(view.Expired, info)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrUserNotFound
	}

	remaining := make([]float64, len(active))
	for i, lot := range active {
		remaining[i] = lot.Remaining
	}
	newPermanent, newRemaining := reconcileBalanceLots(balance, permanent, remaining)
	if len(active) == 0 {
		newPermanent = math.Max(balance, 0)
	}
	view.PermanentBalance = newPermanent
	for i, lot := range active {
		if newRemaining[i] < balanceLotEpsilon {
			continue
		}
		lot.Remaining = newRemaining[i]
		view.ExpiringBalance += newRemaining[i]
		view.Lots = append(view.Lots, lot)
	}
	if len(view.Lots) > 0 {
		t := view.Lots[0].ExpiresAt
		view.NextExpiresAt = &t
	}
	// 最近到期的排在前面
	for i, j := 0, len(view.Expired)-1; i < j; i, j = i+1, j-1 {
		view.Expired[i], view.Expired[j] = view.Expired[j], view.Expired[i]
	}
	return view, nil
}

// LotsBySourceRefs 按单据号（充值码）查批次，给订单列表标注到期时间。
func (s *BalanceExpiryService) LotsBySourceRefs(ctx context.Context, refs []string) (map[string]BalanceLotInfo, error) {
	out := make(map[string]BalanceLotInfo, len(refs))
	if s == nil || s.entClient == nil || len(refs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(refs)+1)
	args = append(args, balanceLotSourceRecharge)
	holders := make([]string, 0, len(refs))
	for i, ref := range refs {
		args = append(args, ref)
		holders = append(holders, "$"+strconv.Itoa(i+2))
	}
	rows, err := s.entClient.QueryContext(ctx, `
		SELECT id, source_ref, amount, remaining, expired_amount, status, credited_at, expires_at, settled_at
		FROM balance_expiry_lots
		WHERE source = $1 AND source_ref IN (`+strings.Join(holders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var info BalanceLotInfo
		var settledAt sql.NullTime
		if err := rows.Scan(&info.ID, &info.SourceRef, &info.Amount, &info.Remaining, &info.ExpiredAmount,
			&info.Status, &info.CreditedAt, &info.ExpiresAt, &settledAt); err != nil {
			return nil, err
		}
		if settledAt.Valid {
			t := settledAt.Time
			info.SettledAt = &t
		}
		out[info.SourceRef] = info
	}
	return out, rows.Err()
}

// BalanceExpiry 返回充值余额有效期服务（经兑换服务取得），可能为 nil。
func (s *PaymentService) BalanceExpiry() *BalanceExpiryService {
	if s == nil {
		return nil
	}
	return s.redeemService.BalanceExpiry()
}

// BalanceExpiryMarks 按订单号返回「这笔充值的余额什么时候到期」，给订单列表和详情标注。
// 没有批次的订单（开关打开之前的充值、订阅订单、尚未入账的订单）不在结果里。
// 查询失败只记日志、返回空：到期时间是附加信息，不能让订单页打不开。
func (s *PaymentService) BalanceExpiryMarks(ctx context.Context, orders []*dbent.PaymentOrder) map[int64]BalanceLotInfo {
	out := make(map[int64]BalanceLotInfo)
	svc := s.BalanceExpiry()
	if svc == nil {
		return out
	}
	refs := make([]string, 0, len(orders))
	for _, o := range orders {
		if o != nil && o.OrderType == "balance" && o.RechargeCode != "" {
			refs = append(refs, o.RechargeCode)
		}
	}
	if len(refs) == 0 {
		return out
	}
	lots, err := svc.LotsBySourceRefs(ctx, refs)
	if err != nil {
		slog.Warn("[BalanceExpiry] load lots for orders failed", "error", err)
		return out
	}
	for _, o := range orders {
		if o == nil {
			continue
		}
		if lot, ok := lots[o.RechargeCode]; ok {
			out[o.ID] = lot
		}
	}
	return out
}
