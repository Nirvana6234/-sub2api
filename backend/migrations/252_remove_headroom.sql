-- Remove the legacy Headroom compression configuration and telemetry columns.
ALTER TABLE IF EXISTS users DROP COLUMN IF EXISTS headroom_compression_enabled;
ALTER TABLE IF EXISTS usage_logs
    DROP COLUMN IF EXISTS headroom_tokens_before,
    DROP COLUMN IF EXISTS headroom_tokens_saved,
    DROP COLUMN IF EXISTS headroom_savings_usd;
DELETE FROM settings WHERE key = 'headroom_base_url';
