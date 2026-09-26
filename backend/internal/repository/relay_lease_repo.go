package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// relayLeaseRepository 是 master.LeaseStore 的 SQL 实现（relay_quota_leases、users.relay_reserved_balance）。
// 金额在库里是 DECIMAL(20,8)，这里一律按整数微单位（1 = 10⁻⁸）读写，没有浮点误差。
type relayLeaseRepository struct {
	db *sql.DB
}

// NewRelayLeaseRepository 创建额度租约仓储。
func NewRelayLeaseRepository(db *sql.DB) master.LeaseStore {
	return &relayLeaseRepository{db: db}
}

const relayLeaseColumns = `id, user_id, node_id, dimension, scope_id, scope_key, (granted * 100000000)::bigint,
	(returned_total * 100000000)::bigint, status, expires_at, master_epoch, last_used_at, created_at, updated_at, closed_at, close_reason`

func scanRelayLease(row rowScanner) (*master.Lease, error) {
	var (
		l          master.Lease
		status     string
		lastUsedAt sql.NullTime
		closedAt   sql.NullTime
	)
	if err := row.Scan(&l.ID, &l.UserID, &l.NodeID, &l.Dimension, &l.ScopeID, &l.ScopeKey, &l.Granted,
		&l.ReturnedTotal, &status, &l.ExpiresAt, &l.MasterEpoch, &lastUsedAt, &l.CreatedAt, &l.UpdatedAt, &closedAt, &l.CloseReason); err != nil {
		return nil, err
	}
	l.Status = master.LeaseStatus(status)
	if lastUsedAt.Valid {
		t := lastUsedAt.Time
		l.LastUsedAt = &t
	}
	if closedAt.Valid {
		t := closedAt.Time
		l.ClosedAt = &t
	}
	return &l, nil
}

func (r *relayLeaseRepository) WithUser(ctx context.Context, userID int64, fn func(tx master.LeaseTx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// 锁住用户行：同一用户的额度改动在库里串行，冻结额与租约在同一事务里改。
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return master.ErrLeaseUserNotFound
	}
	if err != nil {
		return err
	}
	if err := fn(&relayLeaseTx{tx: tx, userID: userID}); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *relayLeaseRepository) ListActiveByNode(ctx context.Context, nodeID int64) ([]*master.Lease, error) {
	return r.query(ctx, `SELECT `+relayLeaseColumns+` FROM relay_quota_leases WHERE node_id = $1 AND status = 'active' ORDER BY id`, nodeID)
}

func (r *relayLeaseRepository) ListExpired(ctx context.Context, before time.Time, limit int) ([]*master.Lease, error) {
	if limit <= 0 {
		limit = 1000
	}
	return r.query(ctx, `SELECT `+relayLeaseColumns+` FROM relay_quota_leases
		WHERE status = 'active' AND expires_at < $1 ORDER BY id LIMIT $2`, before, limit)
}

func (r *relayLeaseRepository) ReservedBalances(ctx context.Context) (map[int64]master.Micros, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, (relay_reserved_balance * 100000000)::bigint FROM users
		WHERE relay_reserved_balance > 0 AND deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]master.Micros{}
	for rows.Next() {
		var id int64
		var v master.Micros
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}

func (r *relayLeaseRepository) query(ctx context.Context, q string, args ...any) ([]*master.Lease, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*master.Lease
	for rows.Next() {
		l, err := scanRelayLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

type relayLeaseTx struct {
	tx     *sql.Tx
	userID int64
}

func (t *relayLeaseTx) Active(ctx context.Context) ([]*master.Lease, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT `+relayLeaseColumns+` FROM relay_quota_leases
		WHERE user_id = $1 AND status = 'active' ORDER BY id`, t.userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*master.Lease
	for rows.Next() {
		l, err := scanRelayLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// lockActive 锁住这个用户的一份生效租约。
func (t *relayLeaseTx) lockActive(ctx context.Context, leaseID int64) (*master.Lease, error) {
	l, err := scanRelayLease(t.tx.QueryRowContext(ctx, `SELECT `+relayLeaseColumns+` FROM relay_quota_leases
		WHERE id = $1 AND user_id = $2 AND status = 'active' FOR UPDATE`, leaseID, t.userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, master.ErrLeaseNotFound
	}
	return l, err
}

// adjustReserved 同步改冻结额（只有余额维度）。
func (t *relayLeaseTx) adjustReserved(ctx context.Context, dimension string, delta master.Micros) error {
	if dimension != service.QuotaDimBalance || delta == 0 {
		return nil
	}
	res, err := t.tx.ExecContext(ctx, `UPDATE users SET relay_reserved_balance = relay_reserved_balance + ($1::numeric / 100000000)
		WHERE id = $2`, delta, t.userID)
	if err != nil {
		return fmt.Errorf("update relay_reserved_balance: %w", err)
	}
	return requireRelayRow(res, nil)
}

func (t *relayLeaseTx) Grant(ctx context.Context, key master.LeaseKey, amount master.Micros, expiresAt time.Time, epoch string, now time.Time) (*master.Lease, error) {
	if amount <= 0 || key.UserID != t.userID {
		return nil, master.ErrLeaseNotFound
	}
	l, err := scanRelayLease(t.tx.QueryRowContext(ctx, `UPDATE relay_quota_leases
		SET granted = granted + ($1::numeric / 100000000), expires_at = $2, master_epoch = $3, updated_at = $4
		WHERE user_id = $5 AND node_id = $6 AND dimension = $7 AND scope_id = $8 AND scope_key = $9 AND status = 'active'
		RETURNING `+relayLeaseColumns,
		amount, expiresAt, epoch, now, key.UserID, key.NodeID, key.Dimension, key.ScopeID, key.ScopeKey))
	if errors.Is(err, sql.ErrNoRows) {
		l, err = scanRelayLease(t.tx.QueryRowContext(ctx, `INSERT INTO relay_quota_leases
			(user_id, node_id, dimension, scope_id, scope_key, granted, status, expires_at, master_epoch, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6::numeric / 100000000, 'active', $7, $8, $9, $9)
			RETURNING `+relayLeaseColumns,
			key.UserID, key.NodeID, key.Dimension, key.ScopeID, key.ScopeKey, amount, expiresAt, epoch, now))
	}
	if err != nil {
		return nil, err
	}
	if err := t.adjustReserved(ctx, key.Dimension, amount); err != nil {
		return nil, err
	}
	return l, nil
}

func (t *relayLeaseTx) Reduce(ctx context.Context, leaseID int64, amount master.Micros, now time.Time) (*master.Lease, error) {
	cur, err := t.lockActive(ctx, leaseID)
	if err != nil {
		return nil, err
	}
	if amount < 0 || amount > cur.Granted {
		return nil, master.ErrLeaseAmount
	}
	l, err := scanRelayLease(t.tx.QueryRowContext(ctx, `UPDATE relay_quota_leases
		SET granted = granted - ($1::numeric / 100000000), updated_at = $2
		WHERE id = $3 RETURNING `+relayLeaseColumns, amount, now, leaseID))
	if err != nil {
		return nil, err
	}
	if err := t.adjustReserved(ctx, cur.Dimension, -amount); err != nil {
		return nil, err
	}
	return l, nil
}

func (t *relayLeaseTx) ApplyReturned(ctx context.Context, leaseID int64, returnedTotal master.Micros, now time.Time) (master.Micros, error) {
	cur, err := t.lockActive(ctx, leaseID)
	if err != nil {
		return 0, err
	}
	delta := returnedTotal - cur.ReturnedTotal
	if delta < 0 {
		delta = 0
	}
	if delta > cur.Granted {
		delta = cur.Granted
	}
	if _, err := t.tx.ExecContext(ctx, `UPDATE relay_quota_leases
		SET granted = granted - ($1::numeric / 100000000),
			returned_total = GREATEST(returned_total, $2::numeric / 100000000),
			updated_at = $3
		WHERE id = $4`, delta, returnedTotal, now, leaseID); err != nil {
		return 0, err
	}
	if err := t.adjustReserved(ctx, cur.Dimension, -delta); err != nil {
		return 0, err
	}
	return delta, nil
}

func (t *relayLeaseTx) Close(ctx context.Context, leaseID int64, status master.LeaseStatus, reason string, now time.Time) (master.Micros, error) {
	cur, err := t.lockActive(ctx, leaseID)
	if err != nil {
		return 0, err
	}
	if _, err := t.tx.ExecContext(ctx, `UPDATE relay_quota_leases
		SET granted = 0, status = $1, close_reason = $2, closed_at = $3, updated_at = $3
		WHERE id = $4`, string(status), reason, now, leaseID); err != nil {
		return 0, err
	}
	if err := t.adjustReserved(ctx, cur.Dimension, -cur.Granted); err != nil {
		return 0, err
	}
	return cur.Granted, nil
}

func (t *relayLeaseTx) Renew(ctx context.Context, leaseID int64, expiresAt time.Time, lastUsedAt *time.Time, now time.Time) (*master.Lease, error) {
	if _, err := t.lockActive(ctx, leaseID); err != nil {
		return nil, err
	}
	var used any
	if lastUsedAt != nil {
		used = *lastUsedAt
	}
	return scanRelayLease(t.tx.QueryRowContext(ctx, `UPDATE relay_quota_leases
		SET expires_at = $1, updated_at = $2,
			last_used_at = CASE WHEN $3::timestamptz IS NULL THEN last_used_at
				ELSE GREATEST(COALESCE(last_used_at, $3::timestamptz), $3::timestamptz) END
		WHERE id = $4 RETURNING `+relayLeaseColumns, expiresAt, now, used, leaseID))
}
