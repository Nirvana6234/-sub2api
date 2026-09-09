-- 补贴任务从"从当天 00:00 到执行时刻"改成"每天固定的一段时间"（比如晚高峰
-- 18:00~22:00），而不是整个自然日。auto_time 语义上就是这段窗口的结束时刻
-- （调度器到点触发），直接改名成 window_end；新增 window_start 补上窗口起点，
-- 已有任务默认从 00:00 算起，等价于原来"整天"的行为，不改变它们已经配置好的
-- 执行时刻和当天覆盖范围。
ALTER TABLE latency_subsidy_tasks RENAME COLUMN auto_time TO window_end;
ALTER TABLE latency_subsidy_tasks ADD COLUMN IF NOT EXISTS window_start text NOT NULL DEFAULT '00:00';
