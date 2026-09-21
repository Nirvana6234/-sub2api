-- Playground history is one shared snapshot per user, independent of the
-- currently selected chat/image API key.
CREATE TABLE IF NOT EXISTS playground_histories (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    state_payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS playground_histories_updated_at_idx
    ON playground_histories (updated_at DESC);
