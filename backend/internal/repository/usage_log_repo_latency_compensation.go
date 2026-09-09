package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// FetchPendingLatencyCompensationRows returns every usage_logs row in
// [startTime, endTime) whose first_token_ms is at or above thresholdMs and
// that has never been marked compensated. account_cost uses the same
// COALESCE(account_stats_cost, total_cost) * account_rate_multiplier formula
// the admin dashboard's model-distribution "成本" column uses (see
// usage_log_repo_stats.go), so a payout refunds exactly the margin the
// dashboard already shows for these rows — no separate cost model to drift
// out of sync with what an admin sees before clicking pay.
//
// Rows served by an apikey-type contributed account are excluded: those
// accounts default to self-use only (never pooled to other room members, see
// contribution_room_routing_repo.go), so the only traffic that can ever hit
// one is the contributor's own — and the platform shouldn't subsidize a
// contributor's own usage of their own account.
//
// The exclusion predicate is wrapped in COALESCE(..., '') on both sides
// (acc.type and acc.extra->>'import_source'). Without it, any ordinary
// (non-contributed) apikey account — which has no "import_source" key in
// extra at all — makes `acc.extra ->> 'import_source' = 'user_contribution'`
// evaluate to SQL NULL rather than false, and NOT NULL is NULL, so the whole
// row silently fails the WHERE clause. 2026-09-09 production incident: this
// exact bug excluded every apikey-type account's traffic, not just the
// self-use-contributed ones, zeroing out a full compensation run.
func (r *usageLogRepository) FetchPendingLatencyCompensationRows(ctx context.Context, startTime, endTime time.Time, thresholdMs int) ([]service.LatencyCompensationRow, error) {
	query := `
		SELECT
			ul.id,
			ul.user_id,
			COALESCE(us.email, '') AS email,
			COALESCE(ul.first_token_ms, 0) AS first_token_ms,
			ul.actual_cost,
			COALESCE(ul.account_stats_cost, ul.total_cost) * ul.account_rate_multiplier AS account_cost
		FROM usage_logs ul
		LEFT JOIN users us ON us.id = ul.user_id
		LEFT JOIN accounts acc ON acc.id = ul.account_id
		WHERE ul.created_at >= $1
		  AND ul.created_at < $2
		  AND ul.first_token_ms >= $3
		  AND ul.latency_compensated_at IS NULL
		  AND ul.user_id IS NOT NULL
		  AND NOT (
		    COALESCE(acc.type, '') = 'apikey'
		    AND COALESCE(acc.extra ->> 'import_source', '') = 'user_contribution'
		  )
		ORDER BY ul.id ASC
	`
	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, thresholdMs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make([]service.LatencyCompensationRow, 0)
	for rows.Next() {
		var row service.LatencyCompensationRow
		if err := rows.Scan(&row.ID, &row.UserID, &row.Email, &row.FirstTokenMs, &row.ActualCost, &row.AccountCost); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// MarkLatencyCompensated stamps latency_compensated_at on exactly the rows
// whose IDs were captured by a prior FetchPendingLatencyCompensationRows
// call. Marking by explicit ID list (rather than re-running the same
// time/threshold filter) means a row created after the fetch — even one that
// would otherwise match — can never be marked without actually having been
// paid for.
func (r *usageLogRepository) MarkLatencyCompensated(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.sql.ExecContext(ctx, `
		UPDATE usage_logs SET latency_compensated_at = NOW()
		WHERE id = ANY($1) AND latency_compensated_at IS NULL
	`, pq.Array(ids))
	return err
}

// UnmarkLatencyCompensated clears latency_compensated_at for every row in
// [startTime, endTime) at or above thresholdMs, reopening them for a future
// payout. Matches by the same time/threshold filter FetchPendingLatencyCompensationRows
// uses rather than an explicit ID list — a revoke only has the original
// payout's window and threshold, not its row IDs.
func (r *usageLogRepository) UnmarkLatencyCompensated(ctx context.Context, startTime, endTime time.Time, thresholdMs int) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE usage_logs SET latency_compensated_at = NULL
		WHERE created_at >= $1 AND created_at < $2 AND first_token_ms >= $3 AND latency_compensated_at IS NOT NULL
	`, startTime, endTime, thresholdMs)
	return err
}
