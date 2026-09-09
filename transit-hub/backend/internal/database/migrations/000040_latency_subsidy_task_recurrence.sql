-- 补贴任务的自动执行只支持"每天"一种周期，现在加上按周(选星期几)、仅工作日
-- (周一到周五)两种，以及一个跟周期正交的可选有效期(起止日期)——不勾有效期
-- 就跟现在一样一直生效。recurrence_days_of_week 用 Go time.Weekday 的数值
-- (0=周日..6=周六)，只在 recurrence_type='weekly' 时读取。
ALTER TABLE latency_subsidy_tasks
    ADD COLUMN IF NOT EXISTS recurrence_type text NOT NULL DEFAULT 'daily',
    ADD COLUMN IF NOT EXISTS recurrence_days_of_week smallint[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS valid_from date NULL,
    ADD COLUMN IF NOT EXISTS valid_until date NULL;
