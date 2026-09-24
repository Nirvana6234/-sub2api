-- Independently managed contribution rooms and their account assignment and preference state.
CREATE TABLE IF NOT EXISTS contribution_rooms (
    id BIGSERIAL PRIMARY KEY,
    owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    consumer_rate_multiplier NUMERIC(10, 4) NOT NULL DEFAULT 1.0000,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    visibility VARCHAR(20) NOT NULL DEFAULT 'private',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_contribution_rooms_consumer_rate_multiplier_nonnegative
        CHECK (consumer_rate_multiplier >= 0)
);

CREATE TABLE IF NOT EXISTS contribution_account_verifications (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL UNIQUE REFERENCES accounts(id) ON DELETE CASCADE,
    platform VARCHAR(50) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    tested_model VARCHAR(200),
    tested_at TIMESTAMPTZ,
    redacted_error_summary TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS contribution_room_accounts (
    id BIGSERIAL PRIMARY KEY,
    room_id BIGINT NOT NULL REFERENCES contribution_rooms(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL UNIQUE REFERENCES accounts(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_contribution_room_preferences (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    room_id BIGINT NOT NULL REFERENCES contribution_rooms(id) ON DELETE CASCADE,
    allow_pool_fallback BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_contribution_rooms_owner_user_id
    ON contribution_rooms (owner_user_id);
CREATE INDEX IF NOT EXISTS idx_contribution_rooms_visibility_status
    ON contribution_rooms (visibility, status);
CREATE INDEX IF NOT EXISTS idx_contribution_account_verifications_platform_status
    ON contribution_account_verifications (platform, status);
CREATE INDEX IF NOT EXISTS idx_contribution_room_accounts_room_id_enabled
    ON contribution_room_accounts (room_id, enabled);
CREATE INDEX IF NOT EXISTS idx_user_contribution_room_preferences_room_id
    ON user_contribution_room_preferences (room_id);
