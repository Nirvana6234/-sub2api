ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS auto_group_strategy VARCHAR(20) NOT NULL DEFAULT 'balanced';
