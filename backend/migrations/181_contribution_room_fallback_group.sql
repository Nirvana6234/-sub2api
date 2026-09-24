ALTER TABLE user_contribution_room_preferences
    ADD COLUMN IF NOT EXISTS fallback_group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL;
