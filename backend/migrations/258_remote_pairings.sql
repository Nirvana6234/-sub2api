-- Phone ↔ desktop pairings for syncing Codex desktop conversations to the phone.
--
-- One row per pairing attempt. It starts as a code shown on the computer
-- (pending), is claimed by a phone of the same account (claimed), and only
-- becomes usable once the user confirms it on that computer (active). The code
-- and the phone's bearer token are stored as SHA-256 hashes only.
--
-- The phone's public key is kept for display; the computer verifies signatures
-- against the copy it saw when the user confirmed, never against this column.
-- Keep this migration idempotent, like the others.
CREATE TABLE IF NOT EXISTS remote_pairings (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id VARCHAR(64) NOT NULL,
    device_name VARCHAR(100) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    code_hash VARCHAR(64),
    code_expires_at TIMESTAMPTZ,
    claim_attempts INTEGER NOT NULL DEFAULT 0,
    phone_label VARCHAR(100) NOT NULL DEFAULT '',
    phone_public_key TEXT NOT NULL DEFAULT '',
    token_hash VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    confirmed_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    CONSTRAINT chk_remote_pairings_status
        CHECK (status IN ('pending', 'claimed', 'active', 'revoked')),
    CONSTRAINT chk_remote_pairings_claim_attempts_nonnegative
        CHECK (claim_attempts >= 0)
);

CREATE INDEX IF NOT EXISTS idx_remote_pairings_user_status
    ON remote_pairings (user_id, status);
CREATE INDEX IF NOT EXISTS idx_remote_pairings_user_device
    ON remote_pairings (user_id, device_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_remote_pairings_token_hash
    ON remote_pairings (token_hash)
    WHERE token_hash IS NOT NULL;
