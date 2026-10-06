package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
)

// relayUserAssignmentRepository 是小白端用户分配的 SQL 存储（relay_user_assignments）。
type relayUserAssignmentRepository struct{ db *sql.DB }

// NewRelayUserAssignmentRepository 创建分配存储。
func NewRelayUserAssignmentRepository(db *sql.DB) master.UserAssignmentStore {
	return &relayUserAssignmentRepository{db: db}
}

func (r *relayUserAssignmentRepository) Get(ctx context.Context, userID int64) (*master.UserAssignment, error) {
	var a master.UserAssignment
	var pinned sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT user_id, node_id, reason, pinned_until, assigned_at, updated_at FROM relay_user_assignments WHERE user_id = $1`, userID).
		Scan(&a.UserID, &a.NodeID, &a.Reason, &pinned, &a.AssignedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, master.ErrAssignmentNotFound
	}
	if err != nil {
		return nil, err
	}
	if pinned.Valid {
		t := pinned.Time
		a.PinnedUntil = &t
	}
	return &a, nil
}

func (r *relayUserAssignmentRepository) Put(ctx context.Context, a *master.UserAssignment) error {
	var pinned any
	if a.PinnedUntil != nil {
		pinned = *a.PinnedUntil
	}
	// 节点变了才更新分配时间。
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO relay_user_assignments (user_id, node_id, reason, pinned_until, assigned_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			assigned_at = CASE WHEN relay_user_assignments.node_id <> EXCLUDED.node_id THEN NOW() ELSE relay_user_assignments.assigned_at END,
			node_id = EXCLUDED.node_id, reason = EXCLUDED.reason, pinned_until = EXCLUDED.pinned_until, updated_at = NOW()`,
		a.UserID, a.NodeID, a.Reason, pinned)
	return err
}

func (r *relayUserAssignmentRepository) Delete(ctx context.Context, userID int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM relay_user_assignments WHERE user_id = $1`, userID)
	return err
}

func (r *relayUserAssignmentRepository) CountByNode(ctx context.Context) (map[int64]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT node_id, COUNT(*) FROM relay_user_assignments GROUP BY node_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]int64{}
	for rows.Next() {
		var node, n int64
		if err := rows.Scan(&node, &n); err != nil {
			return nil, err
		}
		out[node] = n
	}
	return out, rows.Err()
}

func (r *relayUserAssignmentRepository) ListByNode(ctx context.Context, nodeID, afterUserID int64, limit int) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT user_id FROM relay_user_assignments WHERE node_id = $1 AND user_id > $2 ORDER BY user_id LIMIT $3`, nodeID, afterUserID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *relayUserAssignmentRepository) DeleteUnpinned(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM relay_user_assignments WHERE pinned_until IS NULL OR pinned_until <= $1`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
