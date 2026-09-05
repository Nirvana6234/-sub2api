ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS fallback_pool_used boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS fallback_source_group_id bigint,
    ADD COLUMN IF NOT EXISTS fallback_source_group_name text,
    ADD COLUMN IF NOT EXISTS fallback_target_group_id bigint,
    ADD COLUMN IF NOT EXISTS fallback_target_group_name text;

CREATE INDEX IF NOT EXISTS idx_usage_logs_fallback_pool_used_created_at
    ON usage_logs (created_at DESC)
    WHERE fallback_pool_used = true;

