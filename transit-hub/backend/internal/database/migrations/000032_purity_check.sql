-- GPT-5.6 纯度检测：任务队列与报告。
--
-- 检测器旁路服务同一时刻只允许一个会话，所以这张表同时承担「队列」职责：
-- 排队中的任务是 status='queued' 的行，worker 按 created_at 顺序一次领一条。
--
-- 【这里绝不存 API key】。上游凭据由 upstream.ResolveProbeCredential 在
-- worker 内存中临时解析后直接交给检测器，既不落这张表也不写日志。
-- base_url 会存，因为它不是秘密，而且报告要能对得上是哪个上游。

CREATE TABLE IF NOT EXISTS purity_check_jobs (
    id               text PRIMARY KEY,
    user_id          text NOT NULL,
    admin_account_id text NOT NULL DEFAULT '',

    -- 目标账号（sub2api admin account）。名称与 base_url 是提交时的快照：
    -- 账号后来改名或换地址，历史报告仍应显示当时检测的是什么。
    account_id       text NOT NULL,
    account_name     text NOT NULL DEFAULT '',
    account_platform text NOT NULL DEFAULT '',
    base_url         text NOT NULL DEFAULT '',

    tier             text NOT NULL,            -- low / medium / high
    claimed_model    text NOT NULL,            -- gpt-5.6-sol / terra / luna
    request_model    text NOT NULL,            -- 中转别名，默认同 claimed_model

    -- queued / running / succeeded / failed / cancelled
    status           text NOT NULL DEFAULT 'queued',

    -- 批量提交时同一批共享一个 batch_id，便于前端按批展示进度。
    batch_id         text NOT NULL DEFAULT '',

    -- 检测器侧的会话 id，用于取消与拉报告；排队阶段为空。
    detector_session_id text NOT NULL DEFAULT '',

    planned_requests   integer NOT NULL DEFAULT 0,
    completed_requests integer NOT NULL DEFAULT 0,
    failed_requests    integer NOT NULL DEFAULT 0,

    -- 失败时的 i18n key + 明细。明细来自检测器/凭据解析，写入前已确保不含 key。
    error_key    text NOT NULL DEFAULT '',
    error_detail text NOT NULL DEFAULT '',

    created_at   timestamptz NOT NULL DEFAULT now(),
    started_at   timestamptz NULL,
    finished_at  timestamptz NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- worker 领任务：WHERE status='queued' ORDER BY created_at。
CREATE INDEX IF NOT EXISTS idx_purity_check_jobs_queue
    ON purity_check_jobs (created_at)
    WHERE status = 'queued';

-- 列表页按 workspace + 时间倒序翻页。
CREATE INDEX IF NOT EXISTS idx_purity_check_jobs_workspace
    ON purity_check_jobs (user_id, admin_account_id, created_at DESC);

CREATE TABLE IF NOT EXISTS purity_check_reports (
    job_id text PRIMARY KEY REFERENCES purity_check_jobs (id) ON DELETE CASCADE,

    -- 检测器返回的完整报告原文。存原文而不是只存摘要，是因为检测器版本会迭代、
    -- 结论字段会增减；只要留着原文，以后新增展示维度不用重跑检测。
    -- 检测器承诺报告内 auth_values_persisted=false、不含 key（已实测验证）。
    payload jsonb NOT NULL,

    -- 从 payload 里抽出来的结论摘要，仅供列表页排序/筛选，不作为唯一事实来源。
    overall_verdict          text NOT NULL DEFAULT '',
    outcome_code             text NOT NULL DEFAULT '',
    juice_verdict_state      text NOT NULL DEFAULT '',
    fingerprint_model        text NOT NULL DEFAULT '',
    fingerprint_verdict_state text NOT NULL DEFAULT '',
    -- 指纹强指向的型号与申报型号不一致：列表页要用红色标出来的那个信号。
    fingerprint_claim_mismatch boolean NOT NULL DEFAULT false,
    -- official=false 表示这次跑的不是官方档位，结论只能当参考值看。
    official boolean NOT NULL DEFAULT false,

    created_at timestamptz NOT NULL DEFAULT now()
);
