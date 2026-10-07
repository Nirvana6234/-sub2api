-- 充值余额有效期：每笔充值入账时记一个「到期批次」，到期未用完的部分由后台定时清零。
--
-- 设计要点（详见 internal/service/balance_expiry.go）：
--   * users.balance 仍是唯一的总余额，计费热路径（扣费、冻结、退款）一行都不用改。
--   * 批次表只记「会过期的那部分」；不在批次里的余额（升级前的存量、赠送、管理员调整）
--     一律视为永久，沿用旧规则。
--   * 批次的已用量不在每次扣费时维护，而是在需要时用「总余额」倒推（先到期的先用），
--     见 service.reconcileBalanceLots。permanent_balance 记的是倒推时的永久部分。
--   * 本迁移只建结构、不动任何用户余额，功能开关（balance_expiry_enabled）默认关闭。

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS permanent_balance DECIMAL(20, 8) NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS balance_expiry_lots (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT          NOT NULL,
    source          VARCHAR(32)     NOT NULL DEFAULT 'recharge',
    -- 来源单据：充值走兑换码，这里存兑换码本身（支付订单的 recharge_code 与之相同）
    source_ref      VARCHAR(128)    NOT NULL DEFAULT '',
    amount          DECIMAL(20, 8)  NOT NULL,
    remaining       DECIMAL(20, 8)  NOT NULL,
    expired_amount  DECIMAL(20, 8)  NOT NULL DEFAULT 0,
    -- active：仍在有效期内；depleted：用完了；expired：到期时还有余额，已清零
    status          VARCHAR(16)     NOT NULL DEFAULT 'active',
    credited_at     TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ     NOT NULL,
    settled_at      TIMESTAMPTZ
);

-- 同一张单据只能产生一个批次，重复入账（重试、幂等回放）不会多记
CREATE UNIQUE INDEX IF NOT EXISTS balance_expiry_lots_source_ref_uq
    ON balance_expiry_lots (source, source_ref)
    WHERE source_ref <> '';

CREATE INDEX IF NOT EXISTS balance_expiry_lots_user_active_idx
    ON balance_expiry_lots (user_id, expires_at)
    WHERE status = 'active';

-- 定时清零只扫「已到期且仍 active」的批次
CREATE INDEX IF NOT EXISTS balance_expiry_lots_due_idx
    ON balance_expiry_lots (expires_at)
    WHERE status = 'active';

INSERT INTO settings (key, value) VALUES
    ('balance_expiry_enabled', 'false'),
    ('balance_expiry_days', '30')
ON CONFLICT (key) DO NOTHING;
