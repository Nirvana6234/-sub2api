-- 201_add_schedulability_source.sql
-- 增加「可调度来源」权威字段，用于严格区分管理员手动关闭与系统自动停用。
--
-- 背景：此前 schedulable=false 既可能来自管理员手动关闭，也可能来自系统自动隔离
-- （SetError / Grok 凭据错误 / 过期自动暂停）。外部调度方（TransitHub）无法区分，
-- 只能靠状态组合推断，导致系统自动停用的账号被误判为管理员手动关闭而永远无法自动恢复。
--
-- 语义：
--   manual    = 管理员明确关闭可调度。任何系统错误处理、自动恢复、后台任务都不得覆盖。
--   automatic = 系统基于错误/余额/凭据/过期等原因自动停止调度，允许外部定期验证并恢复。
--   none      = 当前没有停用来源（正常可调度账号）。不得被解释为 automatic。

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS schedulability_source VARCHAR(16) NOT NULL DEFAULT 'none';
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS schedulability_reason VARCHAR(64);
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS schedulability_changed_at timestamptz;

-- 限定取值，避免非法来源被写入后被下游当成可恢复。
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'accounts_schedulability_source_check'
    ) THEN
        ALTER TABLE accounts
            ADD CONSTRAINT accounts_schedulability_source_check
            CHECK (schedulability_source IN ('manual', 'automatic', 'none'));
    END IF;
END $$;

-- 历史数据回填：老行没有任何来源信息，无法事后区分当初是管理员手动关闭还是系统自动隔离，
-- 因此一律按失败关闭回填成 manual——宁可不自动恢复，也不能误开管理员手动关闭的账号。
-- 管理员重新开启（→ none）或系统再次报错（→ automatic）时会自然转成正确来源。
--
-- 该 UPDATE 幂等：仅命中「不可调度且来源仍是初始 none」的行。已经是 manual/automatic 的行、
-- 以及管理员后来重新开启（schedulable=TRUE）的行都不会被重复回填冲掉。
UPDATE accounts
SET schedulability_source = 'manual',
    schedulability_reason = 'legacy_backfill_manual',
    schedulability_changed_at = COALESCE(schedulability_changed_at, NOW())
WHERE deleted_at IS NULL
    AND schedulable = FALSE
    AND schedulability_source = 'none';

-- 恢复候选查询索引：外部只会挑 source='automatic' 且 status='error' 的账号做恢复检查。
CREATE INDEX IF NOT EXISTS idx_accounts_schedulability_source_status
    ON accounts(schedulability_source, status)
    WHERE deleted_at IS NULL;

COMMENT ON COLUMN accounts.schedulability_source IS '可调度状态的来源所有权：manual=管理员手动关闭（永久优先，系统不得覆盖）；automatic=系统自动停用（允许定期验证后恢复）；none=无停用来源';
COMMENT ON COLUMN accounts.schedulability_reason IS '可调度状态变更的稳定原因码：admin_disabled / credential_error / balance_error / expired / upstream_error / legacy_backfill_manual 等';
COMMENT ON COLUMN accounts.schedulability_changed_at IS '本次来源与可调度状态变更时间，供外部恢复接口做 compare-and-set 乐观校验';
