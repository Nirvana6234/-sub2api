-- 记录用户注册时的客户端 IP，用于「同一 IP 最多注册 N 个账号」的配额限制。
--
-- 存量用户没有这个信息，保持 NULL：NULL 不参与配额计数，即历史账号不占用配额，
-- 也不会因为无法回溯而误伤某个 IP。配额从本迁移上线后的新注册开始累计。
--
-- 长度 45 足够容纳 IPv6 完整形式（39）与 IPv4-mapped IPv6（45）。
ALTER TABLE users ADD COLUMN IF NOT EXISTS register_ip varchar(45);

COMMENT ON COLUMN users.register_ip IS '注册时的客户端 IP；NULL 表示未记录（迁移前的存量用户），不计入 IP 注册配额';

-- 配额查询是 WHERE register_ip = $1 AND deleted_at IS NULL 的等值统计，
-- 部分索引只覆盖真正参与计数的行，避免为大量 NULL 建无用条目。
CREATE INDEX IF NOT EXISTS idx_users_register_ip
    ON users (register_ip)
    WHERE register_ip IS NOT NULL AND deleted_at IS NULL;
