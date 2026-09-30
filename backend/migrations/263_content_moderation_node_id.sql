-- 主从节点分流：审核记录加"节点"（设计 3.4、第 12 节）。
-- 从节点上的审核记录留在从节点本机；主节点只为从节点上报的违规记一条最小记录（不含输入内容），
-- 让累计违规次数（CountFlaggedByUserSince）把主节点和所有从节点的加在一起算。
-- NULL 表示主节点自己的记录（与 usage_logs.node_id、ops_error_logs.node_id 同一约定）。
ALTER TABLE content_moderation_logs
    ADD COLUMN IF NOT EXISTS node_id BIGINT;
