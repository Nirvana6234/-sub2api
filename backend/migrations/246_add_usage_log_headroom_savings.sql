-- 记录每条请求经 headroom 压缩代理实际节省的 token 数及其标准美金等值（倍率固定为 1，
-- 与实际计费无关，只用于用户仪表盘展示"大概省了多少"）。未启用/未生效的请求为 0。
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS headroom_tokens_saved INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS headroom_savings_usd DOUBLE PRECISION NOT NULL DEFAULT 0;
