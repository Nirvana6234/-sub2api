-- 主从节点分流（docs/MASTER_RELAY_NODES.md）的数据模型。
--
-- 只加表、加列（带默认值或可空），不改不删现有列。主从分流开关关闭时，这些表
-- 没有任何写入，新加的列保持默认值，行为与单机版一致。
--
-- 节点编号的约定，所有表一致：
--   api_keys.relay_node_id、relay_user_assignments.node_id、relay_node_minute_metrics.node_id：
--     0 = 主节点，> 0 = relay_nodes.id。
--     api_keys.relay_node_id 为 NULL 表示还没分配（开关关闭时一直是 NULL）。
--   usage_logs.node_id、ops_error_logs.node_id、ops_system_logs.node_id：
--     NULL = 主节点自己写的，> 0 = 由这台从节点上报。
-- 0 不是 relay_nodes 里的行，所以这些列不加外键。
--
-- 金额一律 DECIMAL(20,8)，与 users.balance 一致；主从之间用 10^-8 的整数传输，
-- 入库时不需要换算精度。
-- Keep this migration idempotent, like the others.

-- 从节点。注册即建行（pending），管理员核对指纹后激活（active）。
-- rejected 的行保留不删：同一把长期密钥不能再次注册。
CREATE TABLE IF NOT EXISTS relay_nodes (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL DEFAULT '',
    hostname VARCHAR(255) NOT NULL DEFAULT '',
    region VARCHAR(50) NOT NULL DEFAULT '',
    public_domain VARCHAR(255),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    identity_public_key TEXT NOT NULL,
    identity_fingerprint VARCHAR(128) NOT NULL,
    encryption_public_key TEXT NOT NULL DEFAULT '',
    registered_ip VARCHAR(64) NOT NULL DEFAULT '',
    program_version VARCHAR(64) NOT NULL DEFAULT '',
    system_info JSONB NOT NULL DEFAULT '{}'::jsonb,
    bandwidth_limit_mbps INTEGER NOT NULL DEFAULT 0,
    config_overrides JSONB NOT NULL DEFAULT '{}'::jsonb,
    allow_multi_ip BOOLEAN NOT NULL DEFAULT FALSE,
    billing_batch_seq BIGINT NOT NULL DEFAULT 0,
    activated_at TIMESTAMPTZ,
    activated_by BIGINT,
    last_seen_at TIMESTAMPTZ,
    last_seen_ip VARCHAR(64) NOT NULL DEFAULT '',
    last_ip_changed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT chk_relay_nodes_status
        CHECK (status IN ('pending', 'active', 'draining', 'disabled', 'rejected')),
    CONSTRAINT chk_relay_nodes_bandwidth_nonnegative
        CHECK (bandwidth_limit_mbps >= 0),
    CONSTRAINT chk_relay_nodes_billing_batch_seq_nonnegative
        CHECK (billing_batch_seq >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_nodes_fingerprint
    ON relay_nodes (identity_fingerprint)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_nodes_public_domain
    ON relay_nodes (LOWER(public_domain))
    WHERE deleted_at IS NULL AND public_domain IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_relay_nodes_status
    ON relay_nodes (status)
    WHERE deleted_at IS NULL;

-- 主从通信证书（24 小时有效，续签换密钥）。按序列号吊销。
-- certificate_der 保存签发的证书原文：续签回复丢失后，从节点用同一把新公钥重发时
-- 原样返回这张证书（幂等），不把正常的重试当成"同一证书续签两次"。
CREATE TABLE IF NOT EXISTS relay_node_certificates (
    id BIGSERIAL PRIMARY KEY,
    node_id BIGINT NOT NULL REFERENCES relay_nodes(id) ON DELETE CASCADE,
    serial VARCHAR(64) NOT NULL,
    public_key TEXT NOT NULL,
    certificate_der BYTEA NOT NULL DEFAULT ''::bytea,
    not_before TIMESTAMPTZ NOT NULL,
    not_after TIMESTAMPTZ NOT NULL,
    renewed_from_serial VARCHAR(64) NOT NULL DEFAULT '',
    revoked_at TIMESTAMPTZ,
    revoke_reason VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_relay_node_certificates_validity
        CHECK (not_after > not_before)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_node_certificates_serial
    ON relay_node_certificates (serial);
CREATE INDEX IF NOT EXISTS idx_relay_node_certificates_node
    ON relay_node_certificates (node_id, not_after DESC);
-- 一张证书只能被续签出一张新证书。用同一把新公钥重发是幂等重放；换了公钥再续签
-- 就是"同一把旧密钥被续签两次"（设计 7.2），按身份重复处理。
CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_node_certificates_renewed_from
    ON relay_node_certificates (renewed_from_serial)
    WHERE renewed_from_serial <> '';

-- 节点相关的审计（注册、激活、拒绝、吊销、立即回收额度、改配置……）。
CREATE TABLE IF NOT EXISTS relay_node_audit_logs (
    id BIGSERIAL PRIMARY KEY,
    node_id BIGINT REFERENCES relay_nodes(id) ON DELETE SET NULL,
    actor_user_id BIGINT,
    action VARCHAR(64) NOT NULL,
    source_ip VARCHAR(64) NOT NULL DEFAULT '',
    detail JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_relay_node_audit_logs_node
    ON relay_node_audit_logs (node_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_relay_node_audit_logs_created
    ON relay_node_audit_logs (created_at DESC);

-- 锁给从节点的额度（设计第 4 节）。一行 = 一台节点为一个用户持有的一项子额度。
-- granted 是这份租约当前还锁着、尚未入账的金额；入账、收回时减少。
-- scope_id / scope_key 标明子额度的归属：余额为 0 / ''；订阅为订阅 ID；分组为分组 ID；
-- 平台配额为平台名；Key 维度为 API Key ID（小白端记在内部 Key 上）。
CREATE TABLE IF NOT EXISTS relay_quota_leases (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id BIGINT NOT NULL REFERENCES relay_nodes(id) ON DELETE CASCADE,
    dimension VARCHAR(32) NOT NULL,
    scope_id BIGINT NOT NULL DEFAULT 0,
    scope_key VARCHAR(64) NOT NULL DEFAULT '',
    granted DECIMAL(20,8) NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    expires_at TIMESTAMPTZ NOT NULL,
    master_epoch VARCHAR(64) NOT NULL DEFAULT '',
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ,
    close_reason VARCHAR(32) NOT NULL DEFAULT '',
    CONSTRAINT chk_relay_quota_leases_dimension
        CHECK (dimension IN (
            'balance',
            'subscription_daily', 'subscription_weekly', 'subscription_monthly',
            'group_daily', 'group_weekly', 'group_monthly',
            'platform_daily', 'platform_weekly', 'platform_monthly',
            'api_key_total', 'api_key_5h', 'api_key_1d', 'api_key_7d'
        )),
    CONSTRAINT chk_relay_quota_leases_status
        CHECK (status IN ('active', 'released', 'expired', 'voided')),
    CONSTRAINT chk_relay_quota_leases_granted_nonnegative
        CHECK (granted >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_quota_leases_active_scope
    ON relay_quota_leases (user_id, node_id, dimension, scope_id, scope_key)
    WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_relay_quota_leases_node_active
    ON relay_quota_leases (node_id)
    WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_relay_quota_leases_expiry
    ON relay_quota_leases (expires_at)
    WHERE status = 'active';

-- 已入账的扣费凭证（设计 5.2～5.4）：每张凭证只入账一次。
-- 按凭证的签发时间分区（签名里带着，同一张凭证永远落在同一分区），唯一键必须
-- 包含分区键，所以是 (voucher_id, issued_at)。签发超过 60 天的凭证不再入账，
-- 分区保留 90 天，去重记录一定比可入账的凭证活得久。
-- 迁移只建父表和默认分区，保证任何时候插入都不会因为缺分区失败；按月分区由
-- 主从分流开关打开后的后台任务预建、到期删除。
CREATE TABLE IF NOT EXISTS relay_voucher_consumed (
    voucher_id UUID NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL,
    node_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    usage_request_id VARCHAR(128) NOT NULL DEFAULT '',
    review_status VARCHAR(16) NOT NULL DEFAULT '',
    consumed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (voucher_id, issued_at),
    CONSTRAINT chk_relay_voucher_consumed_review_status
        CHECK (review_status IN ('', 'pending_review', 'reviewed', 'refunded'))
) PARTITION BY RANGE (issued_at);

CREATE TABLE IF NOT EXISTS relay_voucher_consumed_default
    PARTITION OF relay_voucher_consumed DEFAULT;

CREATE INDEX IF NOT EXISTS idx_relay_voucher_consumed_review
    ON relay_voucher_consumed (node_id, review_status)
    WHERE review_status <> '';

-- 入账失败、被隔离的扣费记录（设计 5.1）。坏记录不堵后面的记录。
CREATE TABLE IF NOT EXISTS relay_billing_quarantine (
    id BIGSERIAL PRIMARY KEY,
    node_id BIGINT NOT NULL,
    batch_seq BIGINT NOT NULL DEFAULT 0,
    voucher_id UUID,
    payload BYTEA NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ,
    resolution VARCHAR(32) NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_relay_billing_quarantine_open
    ON relay_billing_quarantine (node_id, created_at)
    WHERE resolved_at IS NULL;

-- 小白端用户分到哪台节点（设计 10.1、10.4）。没有行 = 还没分配。
CREATE TABLE IF NOT EXISTS relay_user_assignments (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    node_id BIGINT NOT NULL,
    reason VARCHAR(32) NOT NULL DEFAULT '',
    pinned_until TIMESTAMPTZ,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_relay_user_assignments_node_nonnegative
        CHECK (node_id >= 0)
);

CREATE INDEX IF NOT EXISTS idx_relay_user_assignments_node
    ON relay_user_assignments (node_id);

-- 心跳按分钟汇总（设计 11.4、11.5 的曲线）。node_id 0 = 主节点。
CREATE TABLE IF NOT EXISTS relay_node_minute_metrics (
    node_id BIGINT NOT NULL,
    minute TIMESTAMPTZ NOT NULL,
    rx_bps BIGINT NOT NULL DEFAULT 0,
    tx_bps BIGINT NOT NULL DEFAULT 0,
    client_connections INTEGER NOT NULL DEFAULT 0,
    inflight_requests INTEGER NOT NULL DEFAULT 0,
    requests INTEGER NOT NULL DEFAULT 0,
    errors INTEGER NOT NULL DEFAULT 0,
    active_users INTEGER NOT NULL DEFAULT 0,
    active_keys INTEGER NOT NULL DEFAULT 0,
    reserved_total DECIMAL(20,8) NOT NULL DEFAULT 0,
    billing_backlog INTEGER NOT NULL DEFAULT 0,
    vouchers_issued INTEGER NOT NULL DEFAULT 0,
    vouchers_consumed INTEGER NOT NULL DEFAULT 0,
    cpu_percent REAL NOT NULL DEFAULT 0,
    memory_bytes BIGINT NOT NULL DEFAULT 0,
    disk_free_bytes BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (node_id, minute)
);

CREATE INDEX IF NOT EXISTS idx_relay_node_minute_metrics_minute
    ON relay_node_minute_metrics (minute);

-- 现有表加列。常量默认值 / 可空列在 PostgreSQL 11+ 只改元数据，不重写表。

-- 锁在所有从节点上的余额总额（设计 4.3）。balance 只在真正扣费时减少。
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS relay_reserved_balance DECIMAL(20,8) NOT NULL DEFAULT 0;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_users_relay_reserved_balance_nonnegative'
    ) THEN
        ALTER TABLE users
            ADD CONSTRAINT chk_users_relay_reserved_balance_nonnegative
            CHECK (relay_reserved_balance >= 0);
    END IF;
END $$;

-- API Key 分配的节点（设计 10.2）：NULL 未分配，0 主节点，> 0 从节点。
-- relay_node_changed_at 用于 Key 页面提示"地址已变更"。
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS relay_node_id BIGINT;
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS relay_node_changed_at TIMESTAMPTZ;

-- 上游账号"仅主节点使用"（设计第 9 节）。
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS master_only BOOLEAN NOT NULL DEFAULT FALSE;

-- 记录由哪台节点处理（设计 12.4、16.6）：NULL 主节点，> 0 从节点。
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS node_id BIGINT;
ALTER TABLE ops_error_logs
    ADD COLUMN IF NOT EXISTS node_id BIGINT;
ALTER TABLE ops_system_logs
    ADD COLUMN IF NOT EXISTS node_id BIGINT;

COMMENT ON COLUMN users.relay_reserved_balance IS
    'Balance locked on relay nodes (master/relay split); spendable = balance - relay_reserved_balance';
COMMENT ON COLUMN api_keys.relay_node_id IS
    'Assigned node: NULL unassigned, 0 master, >0 relay_nodes.id';
COMMENT ON COLUMN accounts.master_only IS
    'Upstream account is only scheduled for requests served by the master node';
COMMENT ON COLUMN usage_logs.node_id IS
    'Relay node that served the request; NULL means the master';
