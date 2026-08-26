-- 把客户端下载直链指向修复版 v0.1.1。
--
-- v0.1 的包有两个问题：
--   1. 单实例检测用的是 Global\ 命名的 Mutex，且它的构造发生在崩溃处理器安装
--      之前。重复启动时进程静默退出（ExitCode=0），激活失败也不给任何提示，
--      用户看到的就是「双击没反应」。v0.1.1 改用 Local\、把崩溃处理器提到最前、
--      并在激活失败时弹出说明。
--   2. 打包时只塞了一个裸 exe，漏掉了 csproj 里明确要求与 exe 同级的
--      「注册/删除桌面和开始菜单快捷方式.cmd」和 codex-installer 目录，
--      导致下载页第 9 步教用户双击的脚本根本不存在。
--
-- 为什么换文件名而不是原地覆盖：下载站对该路径发的是
-- Cache-Control: public, max-age=3600，同名覆盖会让一小时内的用户继续拿到
-- 缓存里的旧包，也无法从下载结果判断拿到的是哪一版。
--
-- 幂等性与安全性：WHERE 同时限定 key 和旧值，只覆盖 233 写入的那条 v0.1 地址。
-- 管理员后来手工改过的地址不会被踩掉；重复执行匹配 0 行。

UPDATE settings
SET value = 'https://icode-xtu.cc.cd/downloads/codex-relay-client_v0.1.1_x64.zip',
    updated_at = NOW()
WHERE key = 'client_download_direct_url'
  AND value = 'https://icode-xtu.cc.cd/downloads/codex-relay-client_v0.1_x64.zip';
