-- Reconcile columns required by the current Ent schema after restoring older production data.
ALTER TABLE users ADD COLUMN IF NOT EXISTS register_ip VARCHAR(45);
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS schedulability_reason VARCHAR(64);
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS schedulability_changed_at TIMESTAMPTZ;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS auto_group BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS auto_group_strategy VARCHAR(20) NOT NULL DEFAULT 'price';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS model_allowlist JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS codex_models_manifest_config JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS models_list_config JSONB NOT NULL DEFAULT '{}'::jsonb;
