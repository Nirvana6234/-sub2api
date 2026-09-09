-- Per-user breakdown for each latency-compensation payout batch. The
-- 000035 table only recorded batch aggregates (total amount, user count);
-- an admin reviewing history needs to see who specifically was refunded how
-- much, not just a batch total, so this stores the full per-user summary
-- that was shown as the preview at the moment the batch was paid.
ALTER TABLE latency_compensation_payouts
    ADD COLUMN IF NOT EXISTS users_json jsonb NOT NULL DEFAULT '[]'::jsonb;
