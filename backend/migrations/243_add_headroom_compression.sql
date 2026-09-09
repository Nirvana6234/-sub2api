-- headroom 上下文压缩开关：按用户维度开启，默认关闭（存量用户不受影响）。
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS headroom_compression_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- 为已初始化过 settings 表的存量安装补种 headroom 代理地址键，默认留空
-- （InitializeDefaultSettings 只在全新安装时写入默认值，见迁移 242 同样的处理）。
INSERT INTO settings (key, value, updated_at)
VALUES ('headroom_base_url', '', NOW())
ON CONFLICT (key) DO NOTHING;
