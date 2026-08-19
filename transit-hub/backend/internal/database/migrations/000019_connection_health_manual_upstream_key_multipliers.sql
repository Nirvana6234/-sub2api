-- Persist the manual display fallback used when an upstream API-key group multiplier cannot be discovered.
CREATE TABLE IF NOT EXISTS connection_health_manual_upstream_key_multipliers (
    user_id text NOT NULL,
    admin_account_id text NOT NULL DEFAULT '',
    target_id text NOT NULL,
    multiplier double precision NOT NULL CHECK (multiplier >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, admin_account_id, target_id)
);
