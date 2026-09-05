CREATE TABLE IF NOT EXISTS upstream_group_multiplier_events (
    id text PRIMARY KEY,
    user_id text NOT NULL,
    admin_account_id text NOT NULL DEFAULT '',
    site_id text NOT NULL,
    site_name text NOT NULL DEFAULT '',
    group_id text NOT NULL DEFAULT '',
    group_name text NOT NULL,
    previous_multiplier double precision NOT NULL,
    current_multiplier double precision NOT NULL,
    mapped boolean NOT NULL,
    notified boolean NOT NULL DEFAULT false,
    observed_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_upstream_group_multiplier_events_window
    ON upstream_group_multiplier_events (user_id, admin_account_id, observed_at DESC);
