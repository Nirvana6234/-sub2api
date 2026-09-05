-- Keep financial settlement history when a stale contributed account is
-- automatically deleted after 30 days of permanent unavailability.
ALTER TABLE account_share_settlements
    ALTER COLUMN account_id DROP NOT NULL;

ALTER TABLE account_share_settlements
    DROP CONSTRAINT IF EXISTS account_share_settlements_account_id_fkey;

ALTER TABLE account_share_settlements
    ADD CONSTRAINT account_share_settlements_account_id_fkey
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;
