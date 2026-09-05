-- 清除 playground 候选分组设置里指向已删除分组的悬挂 ID。
--
-- 背景（一次真实的线上死锁）：分组 `plus-专线` 被删除后，
-- playground_default_chat_group_ids 里仍留着它的 ID。系统设置保存是整文档校验，
-- validatePlaygroundDefaultGroupIDs 会对这份列表逐个 GetByID，于是：
--
--   * 保存**任何**系统设置都失败，报 "playground group N does not exist"——
--     包括与 playground 毫无关系的邀请返利、SMTP、注册开关；
--   * 该字段是指针类型，客户端不提交时后端会从存量设置回填再校验，所以"不碰
--     playground"并不能绕开；omitted 机制只作用于写入，不作用于校验；
--   * 管理台的候选列表只渲染活跃分组，那个已删除的 ID 在界面上不可见、无法取消
--     勾选，但保存时仍被原样提交。
--
-- 三者叠加的结果是：管理员在 UI 上无论怎么操作都无法解开，只能改库。
--
-- 根因是分组删除时没有清理引用它的设置（settings 表是 KV 结构，没有外键保护）。
-- 该来源已由 adminService.DeleteGroup 调用 SettingService.RemoveGroupReferences
-- 修复；本迁移负责清理修复之前已经产生的存量悬挂引用。
--
-- 幂等：只重写确实包含悬挂 ID 的行；不含悬挂引用的部署上是空操作。
-- 保序：用 WITH ORDINALITY 保留管理员原本的分组顺序，不因清理而重排候选优先级。

WITH exploded AS (
    SELECT
        s.key,
        elem.value::bigint AS group_id,
        elem.ordinality    AS position
    FROM settings s
    CROSS JOIN LATERAL jsonb_array_elements_text(s.value::jsonb) WITH ORDINALITY AS elem(value, ordinality)
    WHERE s.key IN ('playground_default_chat_group_ids', 'playground_default_image_group_ids')
      AND jsonb_typeof(s.value::jsonb) = 'array'
),
kept AS (
    SELECT
        e.key,
        COALESCE(
            jsonb_agg(to_jsonb(e.group_id) ORDER BY e.position)
                FILTER (WHERE g.id IS NOT NULL),
            '[]'::jsonb
        ) AS cleaned,
        COUNT(*) FILTER (WHERE g.id IS NULL) AS dangling
    FROM exploded e
    LEFT JOIN groups g ON g.id = e.group_id AND g.deleted_at IS NULL
    GROUP BY e.key
)
UPDATE settings s
SET value = k.cleaned::text,
    updated_at = NOW()
FROM kept k
WHERE s.key = k.key
  AND k.dangling > 0;
