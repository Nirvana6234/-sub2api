-- Kiro-backed groups require Codex-specific protocol compatibility handling.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS kiro_compat BOOLEAN NOT NULL DEFAULT FALSE;
