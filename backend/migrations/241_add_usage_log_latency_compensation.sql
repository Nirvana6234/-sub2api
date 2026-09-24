-- Marks a usage_log row as already covered by an admin-issued latency
-- compensation payout, so re-running a payout for an overlapping date range
-- skips rows that were already refunded instead of double-crediting them.
--
-- Nullable with no default: on PostgreSQL 11+ this is a metadata-only change
-- and does not rewrite the (potentially large, partitioned) usage_logs table.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS latency_compensated_at TIMESTAMPTZ;

-- Partial index: only slow, not-yet-compensated rows are ever scanned by the
-- preview/apply queries, so indexing the full table would waste space for no
-- benefit — first_token_ms and latency_compensated_at are both selective only
-- together with created_at, and most rows never qualify.
CREATE INDEX IF NOT EXISTS idx_usage_logs_latency_compensation_pending
  ON usage_logs (created_at, first_token_ms)
  WHERE latency_compensated_at IS NULL;
