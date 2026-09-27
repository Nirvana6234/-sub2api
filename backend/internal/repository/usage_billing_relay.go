package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 主从分流入账（设计 5.2）：在扣费的同一个事务里认领扣费凭证、消耗这台节点为这个用户锁的额度租约。

type relaySettlementRecord struct {
	Consumed []service.RelayLeaseConsumption `json:"consumed"`
}

// claimRelayVoucher 认领凭证：第一次入账返回 true；已入账过返回 false，并把当时的消耗填回 s。
func claimRelayVoucher(ctx context.Context, tx *sql.Tx, s *service.RelaySettlement) (bool, error) {
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO relay_voucher_consumed (voucher_id, issued_at, node_id, user_id, review_status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (voucher_id, issued_at) DO NOTHING
		RETURNING voucher_id`, s.VoucherID, s.IssuedAt, s.NodeID, s.UserID, s.ReviewStatus).Scan(&id)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("claim relay voucher: %w", err)
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT settlement FROM relay_voucher_consumed WHERE voucher_id = $1 AND issued_at = $2`,
		s.VoucherID, s.IssuedAt).Scan(&raw); err != nil {
		return false, fmt.Errorf("load relay voucher settlement: %w", err)
	}
	var rec relaySettlementRecord
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &rec); err != nil {
			return false, fmt.Errorf("decode relay voucher settlement: %w", err)
		}
	}
	s.AlreadySettled, s.Consumed = true, rec.Consumed
	return false, nil
}

// saveRelaySettlement 记下这次入账的消耗和扣费请求 ID。
func saveRelaySettlement(ctx context.Context, tx *sql.Tx, s *service.RelaySettlement, requestID string) error {
	raw, err := json.Marshal(relaySettlementRecord{Consumed: s.Consumed})
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE relay_voucher_consumed SET settlement = $1, usage_request_id = $2
		WHERE voucher_id = $3 AND issued_at = $4`, raw, requestID, s.VoucherID, s.IssuedAt)
	if err != nil {
		return fmt.Errorf("save relay voucher settlement: %w", err)
	}
	return requireRelayRow(res, nil)
}

// relayMicros 把金额换成微单位。扣费金额已量化到 8 位小数（UsageBillingMonetaryScale），这里是精确换算。
func relayMicros(v float64) int64 {
	if v <= 0 {
		return 0
	}
	return int64(math.Round(v * 1e8))
}

// relayLeaseCost 返回一份租约这次要消耗的金额：按租约的维度取这次扣费里对应的那一项，
// 并且只消耗这次请求用到的那一份（订阅窗口看分组，Key 维度看 Key，平台配额看平台）。
func relayLeaseCost(dimension string, scopeID int64, scopeKey string, s *service.RelaySettlement, cmd *service.UsageBillingCommand) int64 {
	switch dimension {
	case service.QuotaDimBalance:
		return relayMicros(cmd.BalanceCost)
	case service.QuotaDimSubscriptionDaily, service.QuotaDimSubscriptionWeekly, service.QuotaDimSubscriptionMonthly:
		if scopeID == s.GroupID {
			return relayMicros(cmd.SubscriptionCost)
		}
	case service.QuotaDimAPIKeyTotal:
		if scopeID == s.APIKeyID {
			return relayMicros(cmd.APIKeyQuotaCost)
		}
	case service.QuotaDimAPIKey5h, service.QuotaDimAPIKey1d, service.QuotaDimAPIKey7d:
		if scopeID == s.APIKeyID {
			return relayMicros(cmd.APIKeyRateLimitCost)
		}
	case service.QuotaDimPlatformDaily, service.QuotaDimPlatformWeekly, service.QuotaDimPlatformMonthly:
		if s.Platform != "" && strings.EqualFold(scopeKey, s.Platform) {
			return relayMicros(s.PlatformQuotaCost)
		}
	}
	return 0
}

// consumeRelayLeases 按实扣金额消耗这台节点为这个用户锁的生效租约，余额维度同步减冻结额（设计 4.3）。
// 每份租约最多消耗到零；超出的部分就是超支（锁定之外多用的，设计 4.5），余额照样已经扣了。
// 已关闭、作废、到期的租约不再消耗（迟到的记录，设计 5.4）。
// 锁顺序与额度服务一致：先用户行、再租约。
func consumeRelayLeases(ctx context.Context, tx *sql.Tx, s *service.RelaySettlement, cmd *service.UsageBillingCommand, now time.Time) error {
	var uid int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, s.UserID).Scan(&uid); err != nil {
		return fmt.Errorf("lock user for relay settlement: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+relayLeaseColumns+` FROM relay_quota_leases
		WHERE user_id = $1 AND node_id = $2 AND status = 'active' ORDER BY id FOR UPDATE`, s.UserID, s.NodeID)
	if err != nil {
		return fmt.Errorf("lock relay leases: %w", err)
	}
	type take struct {
		id        int64
		dimension string
		scopeID   int64
		scopeKey  string
		amount    int64
	}
	var takes []take
	for rows.Next() {
		l, err := scanRelayLease(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		amount := relayLeaseCost(l.Dimension, l.ScopeID, l.ScopeKey, s, cmd)
		if amount > l.Granted {
			amount = l.Granted
		}
		if amount > 0 {
			takes = append(takes, take{id: l.ID, dimension: l.Dimension, scopeID: l.ScopeID, scopeKey: l.ScopeKey, amount: amount})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	lt := &relayLeaseTx{tx: tx, userID: s.UserID}
	s.Consumed = nil
	for _, tk := range takes {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_quota_leases
			SET granted = granted - ($1::numeric / 100000000), last_used_at = $2, updated_at = $2 WHERE id = $3`, tk.amount, now, tk.id); err != nil {
			return fmt.Errorf("consume relay lease: %w", err)
		}
		if err := lt.adjustReserved(ctx, tk.dimension, -tk.amount); err != nil {
			return err
		}
		s.Consumed = append(s.Consumed, service.RelayLeaseConsumption{LeaseID: tk.id, Dimension: tk.dimension, ScopeID: tk.scopeID, ScopeKey: tk.scopeKey, Amount: tk.amount})
	}
	return nil
}

// RecordRelayVoucher 实现 service.RelayVoucherRecorder：入账没走到扣费事务时，把凭证记成零消耗。
func (r *usageBillingRepository) RecordRelayVoucher(ctx context.Context, s *service.RelaySettlement) error {
	if s == nil {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	claimed, err := claimRelayVoucher(ctx, tx, s)
	if err != nil {
		return err
	}
	if claimed {
		s.Consumed = nil
		if err := saveRelaySettlement(ctx, tx, s, ""); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.Handled = true
	return nil
}
