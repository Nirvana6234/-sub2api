-- 兜底账号池标记：由其他分组通过 fallback_group_id 指定，用户不可直接选择或绑定。
--
-- 与 fallback_group_id 不是一回事：fallback_group_id 是「某个分组自己指定的
-- 降级目标」，可链式跳转；is_fallback_pool 表示该分组是否可作为其它分组的
-- 兜底账号池。兜底池可以有多个，由每个源分组逐个通过 fallback_group_id 指定，
-- 因此这里只建普通查询索引，不加全局唯一约束。
--
-- 池中账号不因入池而获得任何特权：被选中兜底某分组时，仍要通过「被兜底那个
-- 分组」的利润门（账号倍率 ≤ 分组倍率 ×(1−利润率−缓冲)）。成本从未声明过的
-- 账号一律不参与兜底。

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS is_fallback_pool BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_groups_is_fallback_pool
    ON groups (is_fallback_pool)
    WHERE deleted_at IS NULL;
