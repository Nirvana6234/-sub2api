ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS auto_group_ids JSONB NOT NULL DEFAULT '[]'::jsonb;
