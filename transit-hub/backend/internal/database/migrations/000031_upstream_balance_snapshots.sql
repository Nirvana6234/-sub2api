-- 上游站点余额的每日快照，用于计算真实成本（余额差）。
-- 每天固定时间（如凌晨）采集一次，成本 = 昨日余额 - 今日余额 + 今日充值。
CREATE TABLE IF NOT EXISTS upstream_site_balance_snapshots (
    site_id text NOT NULL REFERENCES upstream_sites(id) ON DELETE CASCADE,
    snapshot_date date NOT NULL,
    balance_usd double precision NOT NULL,
    balance_cny double precision NOT NULL,
    recharge_rate double precision NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, snapshot_date)
);

CREATE INDEX IF NOT EXISTS idx_upstream_balance_snapshots_date
ON upstream_site_balance_snapshots (snapshot_date DESC);
