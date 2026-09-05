-- Shared-account contribution wallet and immutable settlement ledger.
CREATE TABLE IF NOT EXISTS user_contribution_wallets (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    balance NUMERIC(20, 10) NOT NULL DEFAULT 0 CHECK (balance >= 0),
    earned_total NUMERIC(20, 10) NOT NULL DEFAULT 0 CHECK (earned_total >= 0),
    spent_total NUMERIC(20, 10) NOT NULL DEFAULT 0 CHECK (spent_total >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS account_share_settlements (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(255) NOT NULL,
    api_key_id BIGINT NOT NULL,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    contributor_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    consumer_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    actual_cost NUMERIC(20, 10) NOT NULL CHECK (actual_cost >= 0),
    wallet_paid NUMERIC(20, 10) NOT NULL DEFAULT 0 CHECK (wallet_paid >= 0),
    cash_paid NUMERIC(20, 10) NOT NULL DEFAULT 0 CHECK (cash_paid >= 0),
    reward_rate NUMERIC(8, 6) NOT NULL,
    reward_amount NUMERIC(20, 10) NOT NULL CHECK (reward_amount >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (request_id, api_key_id)
);

CREATE INDEX IF NOT EXISTS idx_account_share_settlements_contributor_created
    ON account_share_settlements (contributor_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_account_share_settlements_consumer_created
    ON account_share_settlements (consumer_user_id, created_at DESC);
