-- 将 235 的「全站唯一兜底池」语义调整为「每个分组可指定自己的兜底池」。
-- 235 保留历史兼容：本迁移只移除唯一约束，并清理未使用的旧默认池。

DROP INDEX IF EXISTS idx_groups_fallback_pool_singleton;

CREATE INDEX IF NOT EXISTS idx_groups_is_fallback_pool
    ON groups (is_fallback_pool)
    WHERE deleted_at IS NULL;

DELETE FROM groups g
WHERE g.name = '系统兜底池'
  AND g.is_fallback_pool = true
  AND NOT EXISTS (
      SELECT 1
      FROM groups source
      WHERE source.fallback_group_id = g.id
        AND source.deleted_at IS NULL
  )
  AND NOT EXISTS (
      SELECT 1
      FROM account_groups ag
      WHERE ag.group_id = g.id
  );
