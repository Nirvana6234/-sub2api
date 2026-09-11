-- 把「一条探针都没打通」的历史任务从 succeeded 改判为 failed。
--
-- 背景：探针全部失败时（上游 404、连不上、被限流拒绝），检测器仍然会返回
-- operational_status=complete、verdict_available=true，并给出
-- 「Juice证据不足；指纹证据不明确」。那是它在如实描述"没有证据"，
-- 但界面上把它显示成一次已完成的检测，会让人以为"测过了，这家有点可疑"，
-- 而实际情况是"根本没测到"。这两件事对运维决策的含义完全相反。
--
-- 新逻辑在 worker 里判：successful == 0 且 logical_tasks > 0 就落 failed。
-- 这条迁移把上线前已经产生的记录按同一口径纠正过来。
--
-- 只动 status/error_*，不删报告：报告里的 network_error_details 是排障主要依据。

UPDATE purity_check_jobs AS j
SET status = 'failed',
    error_key = 'admin.purityCheck.errors.upstreamUnreachable',
    error_detail = coalesce(
        nullif(r.payload->'network_error_details'->0->>'safe_message', ''),
        '上游未返回任何成功响应'
    ) || '（' || (r.payload->'network_summary'->>'logical_tasks') || ' 条探针全部失败）',
    updated_at = now()
FROM purity_check_reports AS r
WHERE r.job_id = j.id
  AND j.status = 'succeeded'
  AND coalesce((r.payload->'network_summary'->>'successful')::int, -1) = 0
  AND coalesce((r.payload->'network_summary'->>'logical_tasks')::int, 0) > 0;
