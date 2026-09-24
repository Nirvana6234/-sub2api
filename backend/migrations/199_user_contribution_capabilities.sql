-- User self-service contribution features are opt-in. Existing users retain
-- no capability until an administrator explicitly enables it.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS account_management_enabled BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS contribution_rooms_enabled BOOLEAN NOT NULL DEFAULT FALSE;
