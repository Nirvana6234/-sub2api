-- 撤回标记：只影响 TransitHub 自己这份历史记录（NULL = 仍生效，非 NULL = 已撤回）。
-- 撤回时不会在 Sub2API 那边留下任何记录，这是唯一能看到"这批补贴被撤销过"的地方。
ALTER TABLE latency_compensation_payouts
    ADD COLUMN IF NOT EXISTS revoked_at timestamptz NULL;
