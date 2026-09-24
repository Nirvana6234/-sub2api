-- A user can opt into several rooms at once. The room/account assignment
-- remains one-to-one, so every routed account still has one unambiguous rate.
ALTER TABLE user_contribution_room_preferences
    DROP CONSTRAINT IF EXISTS user_contribution_room_preferences_user_id_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_contribution_room_preferences_user_room
    ON user_contribution_room_preferences (user_id, room_id);
