-- Records which pool served each request, relative to the requesting user:
--   pool: administrator pool (including contributed accounts merged into it)
--   own:  the requester's own contributed account
--   room: another member's account shared through a contribution room
-- Constant default keeps the ADD COLUMN a metadata-only change.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS account_source VARCHAR(16) NOT NULL DEFAULT 'pool';

COMMENT ON COLUMN usage_logs.account_source IS
    'Account source relative to the requester: pool | own | room';

-- Only "own" is backfilled: the requester being the account's contributor holds
-- regardless of when the request happened. Historical room usage is not
-- reconstructable (contributed accounts used to be reachable through the group
-- pool before rooms existed), so those rows stay 'pool'; new rows record the
-- route exactly. The join reaches only the few contributed accounts through
-- idx_usage_logs_account_id.
UPDATE usage_logs u
SET account_source = 'own'
FROM accounts a
WHERE a.id = u.account_id
  AND a.extra->>'import_source' = 'user_contribution'
  AND (a.extra->>'submitted_by_user_id') ~ '^[0-9]+$'
  AND (a.extra->>'submitted_by_user_id')::bigint = u.user_id
  AND u.account_source <> 'own';
