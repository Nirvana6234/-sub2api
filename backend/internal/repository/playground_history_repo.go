package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type playgroundHistoryRepository struct {
	db *sql.DB
}

func NewPlaygroundHistoryRepository(db *sql.DB) service.PlaygroundHistoryRepository {
	return &playgroundHistoryRepository{db: db}
}

func (r *playgroundHistoryRepository) Get(ctx context.Context, userID int64) (json.RawMessage, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("playground history database is unavailable")
	}

	var state []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT state_payload
		FROM playground_histories
		WHERE user_id = $1
			AND updated_at >= NOW() - INTERVAL '30 days'
	`, userID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(state), nil
}

func (r *playgroundHistoryRepository) Save(
	ctx context.Context,
	userID int64,
	state json.RawMessage,
	updatedAt time.Time,
) error {
	if r == nil || r.db == nil {
		return errors.New("playground history database is unavailable")
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO playground_histories (user_id, state_payload, created_at, updated_at)
		VALUES ($1, $2::jsonb, $3, $3)
		ON CONFLICT (user_id) DO UPDATE
		SET state_payload = EXCLUDED.state_payload,
			updated_at = EXCLUDED.updated_at
	`, userID, string(state), updatedAt)
	return err
}
