package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
)

// relayMetricsRepository 保存从节点心跳的分钟汇总（relay_node_minute_metrics，设计 11.4）。
type relayMetricsRepository struct{ db *sql.DB }

// NewRelayMetricsRepository 创建分钟汇总存储。
func NewRelayMetricsRepository(db *sql.DB) master.MetricsSink { return &relayMetricsRepository{db: db} }

func (r *relayMetricsRepository) SaveMinutes(ctx context.Context, rows []master.MinuteMetrics) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO relay_node_minute_metrics (node_id, minute, rx_bps, tx_bps, client_connections, inflight_requests,
			requests, errors, active_users, active_keys, reserved_total, billing_backlog, vouchers_issued, vouchers_consumed,
			cpu_percent, memory_bytes, disk_free_bytes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (node_id, minute) DO UPDATE SET
			rx_bps = EXCLUDED.rx_bps, tx_bps = EXCLUDED.tx_bps, client_connections = EXCLUDED.client_connections,
			inflight_requests = EXCLUDED.inflight_requests, requests = EXCLUDED.requests, errors = EXCLUDED.errors,
			active_users = EXCLUDED.active_users, active_keys = EXCLUDED.active_keys, reserved_total = EXCLUDED.reserved_total,
			billing_backlog = EXCLUDED.billing_backlog, vouchers_issued = EXCLUDED.vouchers_issued,
			vouchers_consumed = EXCLUDED.vouchers_consumed, cpu_percent = EXCLUDED.cpu_percent,
			memory_bytes = EXCLUDED.memory_bytes, disk_free_bytes = EXCLUDED.disk_free_bytes`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, m := range rows {
		// 冻结额用 10^-8 美元整数传输，库里是 DECIMAL(20,8)。
		if _, err := stmt.ExecContext(ctx, m.NodeID, m.Minute, m.RxBps, m.TxBps, m.ClientConnections, m.InflightRequests,
			m.Requests, m.Errors, m.ActiveUsers, m.ActiveKeys, master.FromMicros(m.ReservedMicros), m.BillingBacklog,
			m.VouchersIssued, m.VouchersConsumed, m.CPUPercent, m.MemoryBytes, m.DiskFreeBytes); err != nil {
			return err
		}
	}
	return tx.Commit()
}
