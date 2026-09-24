-- 分组是否允许用户贡献的账号并入号池。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS allow_contribution_pool BOOLEAN NOT NULL DEFAULT FALSE;

-- 用户自建代理：与管理员维护的全局代理池共用一张表，owner_user_id 为空即为
-- 管理员维护的代理；非空则只对该用户自己贡献的账号可见。
ALTER TABLE proxies
    ADD COLUMN IF NOT EXISTS owner_user_id BIGINT;

CREATE INDEX IF NOT EXISTS idx_proxies_owner_user_id
    ON proxies(owner_user_id)
    WHERE deleted_at IS NULL;

-- 账号管理/贡献房间是用户自助功能，需管理员逐个开启；存量用户默认不具备。
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS account_management_enabled BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS contribution_rooms_enabled BOOLEAN NOT NULL DEFAULT FALSE;
