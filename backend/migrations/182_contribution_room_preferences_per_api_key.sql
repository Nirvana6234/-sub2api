ALTER TABLE user_contribution_room_preferences
    ADD COLUMN IF NOT EXISTS api_key_id BIGINT REFERENCES api_keys(id) ON DELETE CASCADE;

DROP INDEX IF EXISTS idx_user_contribution_room_preferences_user_room;

UPDATE user_contribution_room_preferences AS preference
SET api_key_id = (
    SELECT MIN(api_key.id)
    FROM api_keys AS api_key
    WHERE api_key.user_id = preference.user_id
      AND api_key.deleted_at IS NULL
)
WHERE preference.api_key_id IS NULL;

INSERT INTO user_contribution_room_preferences (
    user_id,
    api_key_id,
    room_id,
    allow_pool_fallback,
    fallback_group_id,
    created_at,
    updated_at
)
SELECT
    preference.user_id,
    api_key.id,
    preference.room_id,
    preference.allow_pool_fallback,
    preference.fallback_group_id,
    preference.created_at,
    preference.updated_at
FROM user_contribution_room_preferences AS preference
JOIN api_keys AS api_key
  ON api_key.user_id = preference.user_id
 AND api_key.deleted_at IS NULL
WHERE preference.api_key_id IS NOT NULL
  AND api_key.id <> preference.api_key_id;

DELETE FROM user_contribution_room_preferences
WHERE api_key_id IS NULL;

ALTER TABLE user_contribution_room_preferences
    ALTER COLUMN api_key_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_user_contribution_room_preferences_user_id
    ON user_contribution_room_preferences (user_id);

CREATE INDEX IF NOT EXISTS idx_user_contribution_room_preferences_api_key_id
    ON user_contribution_room_preferences (api_key_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_contribution_room_preferences_api_key_room
    ON user_contribution_room_preferences (api_key_id, room_id);
