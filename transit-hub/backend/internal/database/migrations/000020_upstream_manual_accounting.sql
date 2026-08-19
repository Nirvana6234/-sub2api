-- Keep manual recharge history and Sub2API daily usage snapshots auditable.
CREATE TABLE IF NOT EXISTS upstream_site_recharges (
    id text PRIMARY KEY,
    user_id text NOT NULL,
    admin_account_id text NOT NULL,
    site_id text NOT NULL REFERENCES upstream_sites(id) ON DELETE CASCADE,
    amount double precision NOT NULL CHECK (amount > 0),
    note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_upstream_site_recharges_site_created
ON upstream_site_recharges (site_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS upstream_site_daily_usage (
    site_id text NOT NULL REFERENCES upstream_sites(id) ON DELETE CASCADE,
    usage_date date NOT NULL,
    group_name text NOT NULL,
    raw_amount double precision NOT NULL CHECK (raw_amount >= 0),
    multiplier double precision NOT NULL CHECK (multiplier >= 0),
    adjusted_amount double precision NOT NULL CHECK (adjusted_amount >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, usage_date, group_name)
);
