-- A room receives an explicit concurrency allocation for each shared account.
-- Existing memberships keep their previous behavior by inheriting the account
-- maximum when this migration first runs.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'contribution_room_accounts'
          AND column_name = 'share_concurrency'
    ) THEN
        ALTER TABLE contribution_room_accounts
            ADD COLUMN share_concurrency INTEGER NOT NULL DEFAULT 1;

        UPDATE contribution_room_accounts AS room_account
        SET share_concurrency = GREATEST(1, account.concurrency)
        FROM accounts AS account
        WHERE account.id = room_account.account_id;
    END IF;
END $$;

ALTER TABLE contribution_room_accounts
    DROP CONSTRAINT IF EXISTS chk_contribution_room_accounts_share_concurrency_positive;
ALTER TABLE contribution_room_accounts
    ADD CONSTRAINT chk_contribution_room_accounts_share_concurrency_positive
    CHECK (share_concurrency > 0);
