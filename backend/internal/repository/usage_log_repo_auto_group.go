package repository

import (
	"context"
	"time"

	"github.com/lib/pq"
)

// ListRecentGroupFirstTokenSamples returns at most limit recent successful
// first-token samples per group for one user's requested model. Automatic
// routing must not use another user's latency as a proxy for this user's
// experience.
func (r *usageLogRepository) ListRecentGroupFirstTokenSamples(ctx context.Context, userID int64, groupIDs []int64, model string, since time.Time, limit int) (map[int64][]int64, error) {
	result := make(map[int64][]int64, len(groupIDs))
	if r == nil || r.sql == nil || len(groupIDs) == 0 || limit <= 0 {
		return result, nil
	}
	rows, err := r.sql.QueryContext(ctx, `
WITH ranked AS (
  SELECT group_id, first_token_ms,
         ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY created_at DESC, id DESC) AS rn
  FROM usage_logs
  WHERE user_id = $1
    AND group_id = ANY($2)
    AND COALESCE(NULLIF(requested_model, ''), model) = $3
    AND created_at >= $4
    AND actual_cost > 0
    AND first_token_ms IS NOT NULL
    AND first_token_ms > 0
)
SELECT group_id, first_token_ms
FROM ranked
WHERE rn <= $5
ORDER BY group_id, rn`, userID, pq.Array(groupIDs), model, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var groupID, firstTokenMs int64
		if err := rows.Scan(&groupID, &firstTokenMs); err != nil {
			return nil, err
		}
		result[groupID] = append(result[groupID], firstTokenMs)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// ListRecentPeerGroupFirstTokenSamples returns a shared group/model sample set
// partitioned by user. The service excludes the current user after reading the
// shared cache, so concurrent 15-minute reviews do not repeat the same global
// usage-log scan for every API key owner.
func (r *usageLogRepository) ListRecentPeerGroupFirstTokenSamples(ctx context.Context, groupIDs []int64, model string, since time.Time, limit int) (map[int64]map[int64][]int64, error) {
	result := make(map[int64]map[int64][]int64, len(groupIDs))
	if r == nil || r.sql == nil || len(groupIDs) == 0 || limit <= 0 {
		return result, nil
	}
	rows, err := r.sql.QueryContext(ctx, `
WITH per_user AS (
  SELECT group_id, user_id, first_token_ms, created_at, id,
         ROW_NUMBER() OVER (PARTITION BY group_id, user_id ORDER BY created_at DESC, id DESC) AS user_rn
  FROM usage_logs
  WHERE group_id = ANY($1)
    AND COALESCE(NULLIF(requested_model, ''), model) = $2
    AND created_at >= $3
    AND actual_cost > 0
    AND first_token_ms IS NOT NULL
    AND first_token_ms > 0
), ranked AS (
  SELECT group_id, user_id, first_token_ms,
         ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY created_at DESC, id DESC) AS rn
  FROM per_user
  WHERE user_rn <= 3
)
SELECT group_id, user_id, first_token_ms
FROM ranked
WHERE rn <= $4
ORDER BY group_id, rn`, pq.Array(groupIDs), model, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var groupID, userID, firstTokenMs int64
		if err := rows.Scan(&groupID, &userID, &firstTokenMs); err != nil {
			return nil, err
		}
		if result[groupID] == nil {
			result[groupID] = make(map[int64][]int64)
		}
		result[groupID][userID] = append(result[groupID][userID], firstTokenMs)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
