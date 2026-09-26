-- 主从节点分流：租约上记"累计退回"（设计 4.2、开发计划 2.3）。
-- 从节点退回未用额度时带的是这份租约至今累计退回了多少，主节点只处理比这里多出来的那一截，
-- 并把更大的值写回。它随租约持久化：主节点重启、回复丢失后重发、传输层幂等记录过期，
-- 都不会让同一笔退回生效两次（重复退回会让主节点少算节点手里的钱、把差额再给别的节点）。
ALTER TABLE relay_quota_leases
    ADD COLUMN IF NOT EXISTS returned_total DECIMAL(20,8) NOT NULL DEFAULT 0;
