-- A contribution budget belongs to a room membership, not to the underlying
-- account. Existing memberships start at zero so no existing contribution is
-- silently exposed with an unlimited allowance.
ALTER TABLE contribution_room_accounts
    ADD COLUMN IF NOT EXISTS share_budget_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS share_used_usd NUMERIC(20, 8) NOT NULL DEFAULT 0;

ALTER TABLE contribution_account_verifications
    ADD COLUMN IF NOT EXISTS model_family VARCHAR(32) NOT NULL DEFAULT 'unknown',
    ADD COLUMN IF NOT EXISTS source_kind VARCHAR(32) NOT NULL DEFAULT 'unknown';

ALTER TABLE contribution_room_accounts
    DROP CONSTRAINT IF EXISTS chk_contribution_room_accounts_share_budget_nonnegative;
ALTER TABLE contribution_room_accounts
    ADD CONSTRAINT chk_contribution_room_accounts_share_budget_nonnegative
    CHECK (share_budget_usd >= 0 AND share_used_usd >= 0);

CREATE INDEX IF NOT EXISTS idx_contribution_room_accounts_room_budget
    ON contribution_room_accounts (room_id, enabled, share_budget_usd, share_used_usd);
