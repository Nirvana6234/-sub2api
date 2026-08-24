-- Repair installations where the Channel Monitor V2 migration history and
-- physical schema drifted apart. This is deliberately a new migration: older
-- migration files are checksum-locked once deployed.

CREATE TABLE IF NOT EXISTS channel_monitor_v2_config (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    version INTEGER NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    refresh_interval_seconds INTEGER NOT NULL DEFAULT 300
        CHECK (refresh_interval_seconds IN (60, 300)),
    platforms JSONB NOT NULL DEFAULT '[{"platform":"anthropic","enabled":true,"models":[]},{"platform":"openai","enabled":true,"models":[]},{"platform":"grok","enabled":true,"models":[]},{"platform":"kiro","enabled":true,"models":[]},{"platform":"gemini","enabled":true,"models":[]},{"platform":"antigravity","enabled":true,"models":[]}]'::jsonb,
    group_ids BIGINT[] NOT NULL DEFAULT '{}',
    ignored_error_categories TEXT[] NOT NULL DEFAULT '{}',
    health_thresholds JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_by BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- V2 was introduced in several migrations. Keep upgrades from partially
-- applied histories readable by the current repository query.
ALTER TABLE channel_monitor_v2_config
    ADD COLUMN IF NOT EXISTS ignored_error_categories TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE channel_monitor_v2_config
    ADD COLUMN IF NOT EXISTS health_thresholds JSONB NOT NULL DEFAULT '{}'::jsonb;

INSERT INTO channel_monitor_v2_config (id)
VALUES (1)
ON CONFLICT (id) DO NOTHING;
