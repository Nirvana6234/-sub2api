-- 全局兜底池：任何分组无可用账号时，从这个池里挑账号顶上。
--
-- 与已有的 fallback_group_id 不是一回事，两者可以共存：
--   fallback_group_id —— 某个分组自己指定的降级目标，管理员逐组配置，可链式跳转，
--                        目前只在通用/Anthropic 那套调度里生效。
--   is_fallback_pool  —— 全站唯一的兜底账号池，对所有分组自动生效，
--                        在 OpenAI 兼容调度里生效。
-- 名字必须能区分，否则排查线上调度问题时分不清是哪一层兜底把号选出去的。
--
-- 池中账号不因入池而获得任何特权：被选中兜底某分组时，仍要通过「被兜底那个分组」
-- 的利润门（账号倍率 ≤ 分组倍率 ×(1−利润率−缓冲)）。成本从未声明过的账号一律
-- 不参与兜底 —— 兜底是额外的救济路径，宁可不兜，也不能拿不知道成本的号去顶，
-- 那等于用未知成本换未知收益。

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS is_fallback_pool BOOLEAN NOT NULL DEFAULT false;

-- 全站至多一个兜底池。调度侧按「唯一存在」取用，出现第二个会让不同请求落到
-- 不同的池，行为不可预期。用条件唯一索引在库层面钉死，应用层校验挡不住直接改库。
CREATE UNIQUE INDEX IF NOT EXISTS idx_groups_fallback_pool_singleton
    ON groups (is_fallback_pool)
    WHERE is_fallback_pool = true AND deleted_at IS NULL;

-- 播种系统兜底池。
--
-- 建成 is_exclusive = true：兜底池不应出现在用户可选分组列表里，也不该被直接
-- 绑定使用。它只是一个账号容器，入口由调度在「原分组无号」时自行打开。
--
-- rate_multiplier 保持默认 1.0 且没有实际意义：兜底发生时按用户原本所属分组
-- 计费，池自身的倍率不参与任何定价。
--
-- 幂等：已存在兜底池（含手工改过名字的）就不再插入。
INSERT INTO groups (name, platform, is_fallback_pool, is_exclusive, status, sort_order)
SELECT '系统兜底池', 'openai', true, true, 'active', 9999
WHERE NOT EXISTS (
    SELECT 1 FROM groups WHERE is_fallback_pool = true AND deleted_at IS NULL
);
