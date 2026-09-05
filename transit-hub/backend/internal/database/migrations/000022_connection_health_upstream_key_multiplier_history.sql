-- Hourly, auditable upstream API-key multiplier observations for group health.
CREATE TABLE IF NOT EXISTS connection_health_upstream_key_multiplier_history (
    user_id text NOT NULL,
    admin_account_id text NOT NULL,
    target_id text NOT NULL,
    site_id text NOT NULL,
    key_id text NOT NULL,
    group_id text NOT NULL DEFAULT '',
    group_name text NOT NULL DEFAULT '',
    multiplier double precision NOT NULL CHECK (multiplier >= 0),
    source text NOT NULL CHECK (source IN ('detected', 'manual')),
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, admin_account_id, target_id, observed_at)
);

CREATE INDEX IF NOT EXISTS idx_connection_health_multiplier_history_target_time
    ON connection_health_upstream_key_multiplier_history (user_id, admin_account_id, target_id, observed_at DESC);
