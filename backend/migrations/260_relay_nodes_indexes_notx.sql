-- 主从节点分流：大表上的索引，并发创建不锁表（见 259_relay_nodes.sql）。
-- 两个索引都是部分索引，开关关闭时列全是 NULL，索引为空。
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_api_keys_relay_node_id
    ON api_keys (relay_node_id)
    WHERE relay_node_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_node_id_created_at
    ON usage_logs (node_id, created_at)
    WHERE node_id IS NOT NULL;
