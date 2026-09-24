-- 为客户端下载页与备用支付通道播种默认设置。
--
-- 为什么需要这条迁移：InitializeDefaultSettings 只在全新安装时执行
-- （它发现 registration_enabled 已存在就直接返回），所以后来新增的设置项
-- 在已有库里永远读不到默认值。若不播种，下载页两个按钮都会因地址为空而隐藏。
--
-- 播种之后「空值」才恢复它本来的语义：管理员主动清空 = 隐藏该入口，
-- 而不是「还没配过」。因此这里用 DO NOTHING，绝不覆盖管理员已填的值。

INSERT INTO settings (key, value)
VALUES ('client_download_enabled', 'true')
ON CONFLICT (key) DO NOTHING;

INSERT INTO settings (key, value)
VALUES (
    'client_download_direct_url',
    'https://gitee.com/borg_zhou/co-fly--chat-gpt-assistant/raw/master/Release/codex-relay-client_v0.1_x64.zip'
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO settings (key, value)
VALUES ('client_download_netdisk_url', 'https://pan.baidu.com/s/5PT50-jTaOtR8D28OfYnbQQ')
ON CONFLICT (key) DO NOTHING;

INSERT INTO settings (key, value)
VALUES ('backup_payment_enabled', 'false')
ON CONFLICT (key) DO NOTHING;

INSERT INTO settings (key, value)
VALUES ('backup_payment_url', 'https://pay.ldxp.cn/shop/IJBZUZDE')
ON CONFLICT (key) DO NOTHING;
