-- Tracks latency-compensation payouts triggered from TransitHub against a
-- connected Sub2API site. Sub2API's own usage_logs.actual_cost is never
-- reduced by a payout (compensation is a separate balance credit, not a
-- reversal of the original sale), so nothing about a payout is visible to
-- TransitHub's revenue/cost pipeline unless it's recorded here — this table
-- is that record, read by the daily/weekly report to subtract payouts from
-- profit.
CREATE TABLE IF NOT EXISTS latency_compensation_payouts (
    id text PRIMARY KEY,
    user_id text NOT NULL,
    admin_account_id text NOT NULL DEFAULT '',
    site_base_url text NOT NULL DEFAULT '',
    from_time timestamptz NOT NULL,
    to_time timestamptz NOT NULL,
    threshold_ms integer NOT NULL,
    profit_ratio double precision NOT NULL,
    users_compensated integer NOT NULL,
    amount_usd double precision NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_latency_compensation_payouts_window
    ON latency_compensation_payouts (user_id, admin_account_id, created_at DESC);
