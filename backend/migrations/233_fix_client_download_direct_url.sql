-- 把客户端下载直链从 Gitee raw 换成站点入口机上的静态地址。
--
-- 为什么必须换：Gitee 的 raw 通道对大文件强制登录。实测 66MB 的安装包匿名
-- 请求返回 403，响应体是 55 字节的 "large file require login for access."，
-- 而同仓库 9KB 的 README.md 匿名 200 正常——所以这是文件大小触发的限制，
-- 不是仓库私有。231 播种的那条地址因此是一条死链：用户点下载按钮，浏览器
-- 存下来的是一个改了扩展名的报错文本，双击解压必然失败。
--
-- 新地址是 icode-xtu.cc.cd（154.9.26.202），由该机 nginx 以静态文件直接返回。
-- 应用服务器上不存在该文件，/api/v1/download/client 会走 302 分支跳到这里，
-- 下载带宽不经过应用服务器。这里必须是签了证书的域名而不是 IP：下载页是
-- HTTPS，跳到证书名不匹配的 IP 会被浏览器拦，跳到明文 HTTP 会被 Chrome 的
-- 「阻止不安全下载」拦。
--
-- 别和 icode-xtu.ccwu.cc 混淆：那个域名解析到应用服务器 13.212.118.49，
-- 用它会被 SPA 的 fallback 吃掉，返回 index.html 而不是安装包。
--
-- 这里是新增迁移而不是改 231：已应用的迁移受 checksum 锁定，且 231 用的是
-- ON CONFLICT DO NOTHING，对已有库不会重新播种。
--
-- 幂等性与安全性：WHERE 同时限定 key 和旧值，只覆盖 231 播种的那条死链。
-- 管理员后来在后台手工填过的地址不会被踩掉；重复执行匹配 0 行。

UPDATE settings
SET value = 'https://icode-xtu.cc.cd/downloads/codex-relay-client_v0.1_x64.zip',
    updated_at = NOW()
WHERE key = 'client_download_direct_url'
  AND value = 'https://gitee.com/borg_zhou/co-fly--chat-gpt-assistant/raw/master/Release/codex-relay-client_v0.1_x64.zip';
