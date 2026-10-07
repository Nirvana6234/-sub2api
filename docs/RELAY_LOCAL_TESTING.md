# 本机启动主从服务器（用 Codex 测试）

在一台 Windows 机器上起一个主节点和一个从节点，让 Codex 的请求经从节点转发到 OpenAI（或本地假上游）。**这不是生产部署**：从节点用明文 HTTP、没有域名和证书、外部探测关掉。真机部署看 `docs/RELAY_NODE_OPS.md`，设计看 `docs/MASTER_RELAY_NODES.md`。

> 这份流程已经在一个全新的库上完整跑通过（合并最新 main 之后）：主节点起来、从节点注册并激活、Key 分给从节点、经从节点发请求成功、用量行带节点、排空与取消排空、日志查询。**没有用 Codex 本身验证过**（跑通的请求是脚本发的 `/v1/responses`），Codex 这一步按第 6 节做，出了问题看第 9 节。

## 1. 会起哪些东西

| 进程 | 地址 | 说明 |
|---|---|---|
| 主节点 | `http://127.0.0.1:18080`（VM 客户端用 `192.168.216.1:18080`） | 管理后台 API、数据库和 Redis 都连它；对外还有主从通信 `127.0.0.1:17443`（TLS，双向认证） |
| 从节点 | `http://127.0.0.1:18081`（VM 客户端用 `192.168.216.1:18081`） | **Codex 连这个**。不连数据库和 Redis，只经 17443 连主节点 |
| 假上游（可选） | `http://127.0.0.1:18090` | 假的 OpenAI 接口，没有真实账号时用来冒烟 |

脚本都在 `tools/relay-local-e2e/`，运行时的数据、日志、凭据放在仓库根目录下被 git 忽略的 `.local/relay-e2e/`。

## 2. 前置条件

- 本机 PostgreSQL（默认 `127.0.0.1:5432`）和 Redis（`127.0.0.1:6379`）在运行。
- Go（`backend/go.mod` 要求的版本，`go version` 能用）、Python 3、PowerShell 5.1。
- 一个**空的**测试库，名字建议 `sub2api_relay_e2e`；Redis 用一个单独的库号（下面用 5）。**不要指向有真实数据的库**。
- 要测真实上游：一个可用的 OpenAI 账号（API Key 或 OAuth）。本机访问 OpenAI 要走代理的话看第 9 节。

## 3. 一次性准备

在仓库根目录打开 PowerShell：

```powershell
# 1) 建空库
psql -h 127.0.0.1 -U postgres -c "CREATE DATABASE sub2api_relay_e2e"

# 2) 凭据文件（里面的值只用于这个临时库）
New-Item -ItemType Directory -Force .local\relay-e2e | Out-Null
Copy-Item tools\relay-local-e2e\secrets.example.ps1 .local\relay-e2e\secrets.ps1
notepad .local\relay-e2e\secrets.ps1     # 填数据库密码、管理员密码、两个 64 位十六进制随机值

# 3) 编译（主节点和从节点是同一个程序，用 NODE_ROLE=relay 区分）
powershell -File tools\relay-local-e2e\build.ps1
```

随机值生成：`-join ((1..32) | ForEach-Object { '{0:x2}' -f (Get-Random -Maximum 256) })`。`RELAY_KEY_ENCRYPTION_KEY` 加密主节点的私钥文件，丢了要重新激活从节点。

## 4. 启动顺序

```powershell
# (a) 假上游（只在不用真实账号时需要）
powershell -File tools\relay-local-e2e\start-upstream.ps1

# (b) 主节点，等 15–20 秒
powershell -File tools\relay-local-e2e\start-master.ps1

# (c) 打开主从分流，建测试分组 / 账号 / Key（账号默认指向假上游）
python tools\relay-local-e2e\drive.py setup

# (d) 取主节点的根证书指纹，起从节点
python tools\relay-local-e2e\drive.py fingerprint
powershell -File tools\relay-local-e2e\start-node.ps1

# (e) 激活从节点（等它注册上来，约 10 秒后）
python tools\relay-local-e2e\drive.py activate

# (f) 关掉外部探测、主节点分配比例设为 0，并把测试 Key 分给从节点
python tools\relay-local-e2e\drive.py route
```

几点说明：

- **激活**：`drive.py activate` 直接取从节点注册时报的指纹去激活，省掉了"在从节点本机读指纹再核对"这一步，**只有本机联调这样做**；真机部署必须照手册核对。管理页的对外地址支持域名、域名加端口、IPv4、IPv4 加端口；直接填写 IP 时不做 DNS 一致性核对；IPv6 加端口写成 `[2001:db8::1]:8443`。本地演示可填 `relay1.localhost`，本机解析不到，所以脚本选了"仍然激活"。
- **外部探测**：主节点要从外面握手验证从节点的 HTTPS 才开始分配，本机没有，所以 `route` 把它关了（通用配置里的 `probe_enabled`）。不关的话新 Key 永远分不到从节点。
- **主节点比例 0**：新分配全给从节点；这时直接请求主节点 18080 的网关接口会被拒（403，提示分配的地址），要测"从节点之外"的路径把比例调回大于 0。
- 起来后 `python tools\relay-local-e2e\drive.py status` 能看到运行状态 `running`；`python tools\relay-local-e2e\smoke.py` 会跑一遍健康、请求、排空、使用记录、日志的检查。

### 虚拟机客户端（VMware VMnet8/NAT）

如果客户端在 VMware 的 NAT 网络（VMnet8）里，从节点必须监听宿主机的 VMnet8 地址，不能只监听 `127.0.0.1`。本机宿主机地址是 `192.168.216.1`，启动时使用：

```powershell
powershell -ExecutionPolicy Bypass -File tools\relay-local-e2e\start-node.ps1 -BindHost 192.168.216.1
```

虚拟机客户端的主节点地址填 `http://192.168.216.1:18080/`，从节点地址填 `http://192.168.216.1:18081/v1`。在虚拟机里先确认连通性：

```bash
curl http://192.168.216.1:18081/health
```

如果虚拟机连接超时，请在宿主机的管理员 PowerShell 放行端口：

```powershell
New-NetFirewallRule -DisplayName 'sub2api relay node 18081' -Direction Inbound -Action Allow -Protocol TCP -LocalAddress 192.168.216.1 -LocalPort 18081 -Profile Private
```

启动脚本在执行策略受限的机器上需要带 `-ExecutionPolicy Bypass`；这只对本次 PowerShell 生效。

## 5. 用假上游先冒烟

```powershell
python tools\relay-local-e2e\smoke.py
```

看到 `via node: 200`、`usage node_id=1` 有记录、`user relay state` 里有锁定额度，就说明主从链路是通的。测试 Key 在 `.local\relay-e2e\state.json` 里（`api_key` 字段），管理员账号的余额被设成了 5 美元。

## 6. 用真实 OpenAI 账号 + Codex 测试

### 6.1 把真实账号放进测试分组

`drive.py setup` 建了分组 `relay-e2e`（OpenAI 平台）和一个指向假上游的账号。要走真实上游：

1. 打开管理后台（见第 7 节），**账号管理 → 添加账号**，平台选 OpenAI：
   - **API Key**：Key 填你自己的，基础地址 `https://api.openai.com`；
   - **OAuth**：走后台自带的授权流程。**OAuth 账号经从节点是还没在真机验证过的路径**（凭据快照、WebSocket 会话抢占修复），这正是值得测的地方。
2. 账号所属分组选 `relay-e2e`，并把假上游那个账号**停用或移出分组**，否则请求可能还是落到假上游。
3. 本机直连不了 OpenAI 时，在**代理管理**里加 `http://127.0.0.1:7897`，绑到这个账号上。从节点的代理由主节点加密下发。
4. 余额不够时在用户管理里给管理员账号充值（测试 Key 属于它）。

真实模型名要有价格：没有价格的模型会被入口拒绝（503 `Pricing is not configured for this model`），这是新合入的行为，主节点选号时也会检查。

### 6.2 先用 curl 确认从节点这条路通

```powershell
$key = (Get-Content .local\relay-e2e\state.json | ConvertFrom-Json).api_key
curl.exe -s http://127.0.0.1:18081/v1/responses `
  -H "Authorization: Bearer $key" -H "Content-Type: application/json" `
  -d '{"model":"gpt-5.5","input":"say hi"}'
```

### 6.3 让 Codex 用从节点

**用独立的 `CODEX_HOME`，不要动你真实的 `~/.codex`**：

```powershell
New-Item -ItemType Directory -Force C:\temp\codex-relay | Out-Null
@'
model_provider = "relaytest"
model = "gpt-5.5"

[model_providers.relaytest]
name = "relaytest"
base_url = "http://127.0.0.1:18081/v1"
wire_api = "responses"
env_key = "RELAYTEST_KEY"
'@ | Set-Content -Encoding utf8 C:\temp\codex-relay\config.toml

$env:CODEX_HOME = "C:\temp\codex-relay"
$env:RELAYTEST_KEY = (Get-Content .local\relay-e2e\state.json | ConvertFrom-Json).api_key
codex exec "用一句话介绍你自己"
```

- `base_url` 里**不要**写后台 Key 页上显示的 `https://relay1.localhost`：那是演示域名，本机没有。
- 模型名换成你账号能用的；Codex 的模型目录、推理强度等其他配置照你平时的写。
- Codex 桌面版如果要测，它读的是 `%USERPROFILE%\.codex\config.toml`：**先备份**，测完还原。

### 6.4 确认请求真的走了从节点

- 管理后台 **使用记录**：有"节点"列，应显示 `local-node`；筛选"节点"可以只看从节点转发的。
- 管理后台 **从节点管理 → 日志**：实时向从节点查日志（只显示，不入库）。
- `python tools\relay-local-e2e\drive.py status` 之外，`smoke.py` 末尾的 `usage node_id=1` 也能看到。
- 对照：把比例调回大于 0 再把 Key 移回主节点（**分配 → 转移 Key → 主节点**），同一个请求打 `http://127.0.0.1:18080/v1/responses`，用量行除了 `node_id` 应当一致。

## 7. 管理后台怎么打开

主节点默认编译里**没有内嵌前端**（`build.ps1` 没加 `-tags embed`）。两个办法：

- **开发服务器（推荐）**：

  ```powershell
  cd frontend
  pnpm install
  $env:VITE_DEV_PROXY_TARGET = "http://127.0.0.1:18080"
  pnpm dev          # http://localhost:3000
  ```

  用 `secrets.ps1` 里的管理员邮箱和密码登录，侧边栏 **从节点管理**（路径 `/admin/relay-nodes`）。
- **内嵌前端**：`cd frontend; pnpm build` 后，把 `build.ps1` 里的 `go build` 加上 `-tags embed` 重新编译，再重启主节点，直接访问 `http://127.0.0.1:18080`。

本机的"二次验证"开关默认关闭，改动类操作不需要 TOTP。

## 8. 常用测试场景

| 想测什么 | 怎么做 |
|---|---|
| 排空 / 取消排空 | 从节点管理 → 节点 → 排空；排空中新请求仍可用，但不再分配新 Key；取消排空恢复 |
| 从节点掉线 | `Get-NetTCPConnection -LocalPort 18081 -State Listen \| ForEach-Object { Stop-Process -Id $_.OwningProcess -Force }`；约 15 秒后后台显示离线并发通知；用户额度的锁定在"用户管理 → 中转分配"里能看到；再 `start-node.ps1` 起来会自动恢复在线 |
| 回收额度 | 用户管理 → 中转分配 → 立即回收；或节点页的"回收额度" |
| 转移 Key / 分配比例 | 分配页签；通用配置里改主节点比例（改到 0 前会弹确认） |
| 吊销节点 | 节点页 → 吊销证书，会弹出要更换的密钥提醒 |
| 日志 | 从节点管理 → 日志（程序日志 / 错误记录 / 审核记录） |
| 通知 | 通知页签配飞书机器人后"发送测试消息"；没配时事件只进后台的运维告警 |

## 9. 常见问题

| 现象 | 原因和处理 |
|---|---|
| 从节点日志卡在注册 / 后台没有待激活节点 | 根证书指纹不对：重新 `drive.py fingerprint` 再起从节点；换过主节点密钥目录或重建了库时，先删 `.local\relay-e2e\node` 再起 |
| 激活返回 409 `RELAY_DOMAIN_MISMATCH` | 域名没解析到从节点的注册 IP；本机用 `drive.py activate`（带"仍然激活"）或在页面上勾选 |
| 从节点起来后去访问 `:443` 或向 Let's Encrypt 申请证书 | 没设 `RELAY_NODE_TLS_DISABLED=true`；用 `start-node.ps1` 起就没有这个问题 |
| 请求返回 403，提示分配的地址 | 直接请求了主节点，而主节点分配比例是 0：改请求从节点 18081，或把比例调大于 0 |
| 请求 503 `server_busy` / `Service temporarily unavailable` | 主节点转发上限、或从节点没连上主节点（看 `node.err.log`）、或没有可用账号（账号状态、分组） |
| 请求 503 `Pricing is not configured for this model` | 这个模型名没有价格：用有价格的模型，或在后台给它配价格 |
| 余额不足 / 额度锁定 | 给管理员账号充值；从节点上锁着的额度 10 分钟租约到期会自动回收，也可以手动回收 |
| 新 Key 分不到从节点 | 外部探测没关（`drive.py route` 会关）、节点没激活、或比例设成了 100：用 `drive.py route`，或在分配页手动"转移 Key" |
| 本机连不上 OpenAI | 在后台加本地代理 `http://127.0.0.1:7897` 并绑到账号 |
| 跑完后 `git status` 里 `data/model_pricing.*` 有改动 | 主节点运行时会更新价格文件，提交前 `git checkout -- data/model_pricing.json data/model_pricing.sha256` |
| 主节点日志里有 Redis `value is not an integer` | Redis 库 5 里有旧数据：`FLUSHDB` 清掉（`SELECT 5` 之后） |

## 10. 停止和清理

```powershell
powershell -File tools\relay-local-e2e\stop.ps1                      # 停主节点、从节点、假上游
# 要从头来：
psql -h 127.0.0.1 -U postgres -c "DROP DATABASE sub2api_relay_e2e" -c "CREATE DATABASE sub2api_relay_e2e"
Remove-Item -Recurse -Force .local\relay-e2e\master, .local\relay-e2e\node, .local\relay-e2e\state.json, .local\relay-e2e\root-fingerprint.txt
# 再清 Redis 库 5（SELECT 5 → FLUSHDB），然后回到第 4 节
```

测 Codex 用的 `C:\temp\codex-relay` 和 `CODEX_HOME` 环境变量只在当前终端有效，删目录即可。
