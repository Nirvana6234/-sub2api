CREATE TABLE IF NOT EXISTS latency_subsidy_tasks (
    id text PRIMARY KEY,
    user_id text NOT NULL,
    admin_account_id text NOT NULL DEFAULT '',
    name text NOT NULL,
    threshold_ms integer NOT NULL,
    profit_ratio double precision NOT NULL,
    auto_enabled boolean NOT NULL DEFAULT false,
    auto_time text NOT NULL DEFAULT '23:50',
    last_auto_run_date text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_latency_subsidy_tasks_owner
    ON latency_subsidy_tasks (user_id, admin_account_id, created_at DESC);
-- 调度器每分钟扫一遍所有开了自动执行的任务，这个部分索引让那次扫描不用
-- 碰关掉自动执行的任务。
CREATE INDEX IF NOT EXISTS idx_latency_subsidy_tasks_auto_enabled
    ON latency_subsidy_tasks (auto_enabled) WHERE auto_enabled = true;

-- 记录是哪个任务触发的这笔发放，历史列表据此展示任务名。任务被删除后这两列
-- 仍保留当时的值（不是外键），历史不会因为任务被删而丢失归属信息。
ALTER TABLE latency_compensation_payouts
    ADD COLUMN IF NOT EXISTS task_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS task_name text NOT NULL DEFAULT '';
