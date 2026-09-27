-- 主从节点分流：已入账凭证记下这次入账消耗了哪些租约多少钱（设计 5.2、开发计划 WP8）。
-- 从节点按它修正本地额度；确认丢了、从节点重发同一条记录时，主节点不再入账，按这里的记录返回同样的数。
-- 结构：{"consumed":[{"lease_id":1,"dimension":"balance","scope_id":0,"scope_key":"","amount":123}]}，金额为微单位（1 = 10⁻⁸）。
ALTER TABLE relay_voucher_consumed
    ADD COLUMN IF NOT EXISTS settlement JSONB NOT NULL DEFAULT '{}'::jsonb;
