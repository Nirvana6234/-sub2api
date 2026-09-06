# 打入桌面会话 —— codex app-server 调研

> 状态：**调研，已在本机做过只读实测；P-1/P-2/P-3 已在桌面版开着时跑完**。整理于 2026-09-04。
> 起因：你判断"从 codex CLI 源码看，应该可以与 codex-app-server 通信"。**判断成立，而且比预想的更好**。
> 相关：[`小白客户端本地中转模式调研.md`](小白客户端本地中转模式调研.md)（base_url 转发那条路）、
> [`共飞 Paw 远程操作整体架构.md`](共飞%20Paw%20远程操作整体架构.md)。
> 本机 codex：**codex-cli 0.144.2**。

---

## 0. 一句话结论

**"另起一个 app-server 就能看见并接管桌面版的会话"——这一条已经实测成立。**
本机跑一个全新的 `codex app-server` 进程，`thread/list` **直接列出了桌面版的 20 个对话**，
包括桌面版当时正在写的那一个。会话身份是**通过 `~/.codex` 共享的，不是通过进程**。

而且官方协议里**已经有一整套远程控制与配对**（`remoteControl/*`），
形状和我们围绕 Paseo relay 手工搭的那套几乎一样。

~~剩下唯一的未知是：桌面版在 Windows 上跑的那个 app-server，第二个客户端能不能连进去。~~
**2026-09-04 已验完（§3）：Windows 上"同席"不成立，"离线接管"成立。**
桌面版的 app-server 是它自己的 **stdio 私有子进程**，没有任何监听面可供第二个客户端接入。

---

## 1. 实测证据（只读，没有起会话、没有发模型请求）

### 1.1 另一个 app-server 直接看见桌面版的对话

用私有 node 起一个 `codex app-server`（stdio、JSON-RPC、换行分帧），
只发 `initialize` + `initialized` + `thread/list`：

```
initialize OK: {"userAgent":"cofly-probe/0.144.2 (Windows 10.0.26200; x86_64)…","codexHome":"C:\\Users\\Borg\\.codex", …}
thread/list OK: 20 thread(s)
  - id=01a06070-8b9… cwd=C:\Work\Git\codex              named=true
  - id=01a0605a-d67… cwd=C:\Work\Git\AI-Fly\-sub2api    named=false
  - id=01a05664-1cd… cwd=C:\Work\Git\AI-Fly\-sub2api    named=false   ← 桌面版当时正在写的那条
```

thread 记录的字段（协议原样）：

```
id, extra, sessionId, forkedFromId, parentThreadId, preview, ephemeral, historyMode,
modelProvider, createdAt, updatedAt, recencyAt, status, path, cwd, cliVersion,
source, threadSource, agentNickname, agentRole, gitInfo, name, turns
```

注意 `modelProvider` 和 `path`：**能直接看出哪些对话是走中转站路由的、rollout 文件在哪**。

### 1.2 `thread/resume` 明写着"会重新加入正在跑的线程"

协议生成物 `ThreadResumeParams` 的注释（`codex app-server generate-ts --experimental` 生成，原文）：

> There are three ways to resume a thread: by thread_id / by history / by path.
> **If thread_id identifies a running thread, app-server rejoins that thread** and treats a
> non-empty path as a consistency check against the active rollout path.

也就是说"接管一个**正在进行**的会话"是协议**明确支持**的语义，不是我们钻空子。
前提是：得跟那个线程在**同一个 app-server 实例**里（§3）。

### 1.3 官方已经有远程控制与配对

`ClientRequest` 里的完整远程控制面：

```
remoteControl/enable          { ephemeral?: boolean }
                              → { status, serverName, installationId, environmentId }
remoteControl/disable
remoteControl/status/read     → { status, serverName, installationId, environmentId }
remoteControl/pairing/start   { manualCode?: boolean }
                              → { pairingCode, manualPairingCode, environmentId, expiresAt }
remoteControl/pairing/status  { pairingCode | manualPairingCode }
remoteControl/client/list     { environmentId, … }  → RemoteControlClient[]
remoteControl/client/revoke
```

`RemoteControlClient` 带 `clientId, displayName, deviceType, platform, osVersion, deviceModel, appVersion, lastSeenAt`
——**已配对设备列表**，正是我们在 relay 那条路上做不到、只能写"daemon 不跟踪配对"的东西。

本机实测（只读）：

```
remoteControl/status/read  => {"status":"disabled","serverName":"DESKTOP-9GRMVU2",
                               "installationId":"e3685692-…","environmentId":null}
account/read               => {"account":{"type":"apiKey"},"requiresOpenaiAuth":true}
thread/loaded/list         => {"data":[],"nextCursor":null}
```

远程控制在 Windows 上**可查询、当前是 disabled**。

### 1.4 但是：daemon 生命周期在 Windows 上不支持

```
> codex app-server daemon version
Error: codex app-server daemon lifecycle is only supported on Unix platforms
```

CLI 那套 `codex app-server daemon start|restart|stop`、`codex app-server proxy --sock`、
`codex remote-control start|stop|pair` 都是围绕**Unix 域套接字的托管守护进程**做的。
Windows 上**没有这条"连到已经在跑的那个 app-server"的现成通道**。

这条是本次调研最关键的一条限制，它把问题变成了 §3。

---

## 2. 完整能力面（可用的官方 RPC）

除了 thread/turn，还有一大票我们自己正在造或将要造的东西：

| 面 | 方法 | 对我们的意义 |
|---|---|---|
| 会话 | `thread/list` `thread/read` `thread/items/list` `thread/turns/list` `thread/search` | 手机端读桌面对话，**不用解析 rollout 文件** |
| 会话 | `thread/resume` `thread/fork` `thread/rollback` `thread/archive` | 接管 / 分叉 / 回滚 |
| 轮次 | `turn/start` `turn/steer` `turn/interrupt` | 发起、**中途插话**、打断 |
| 远程 | `remoteControl/*`（§1.3） | 官方配对与设备管理 |
| 账号 | `account/read` `account/usage/read` `account/rateLimits/read` | 余额/限额直接有，不用另做 |
| 配置 | `config/read` `config/value/write` `config/batchWrite` | **改路由不用手写 config.toml** |
| 环境 | `environment/info` `environment/add` `app/list` `model/list` | —— |
| 系统 | `fs/*` `process/*` `command/exec` | ⚠️ 同样是"owner 级"的大面，见 §5 |

`config/value/write` 值得单独说：现在切换中转站路由靠 `CodexConfigWriter` 直接改
`~/.codex/config.toml`，而官方登录会整体重写这个文件（注入方案文档里实测过）。
走 RPC 改配置是不是更稳，值得单独验一次。

---

## 3. 已验完：Windows 上是"离线接管"，不是"同席"

**P-1：桌面版的 app-server 长什么样**（桌面版开着时抓的进程树）

```
PID 2608  ChatGPT.exe  …\app\ChatGPT.exe                    ← Electron 主进程
PID 16432 codex.exe    …\app\resources\codex.exe -c features.code_mode_host=true app-server --analytics-default-enabled
                       ↑ 父进程就是 2608
```

关键在于**它没有 `--listen`**：`app-server` 的默认传输是 `stdio://`，
也就是这个 app-server 只跟它的 Electron 父进程通过标准输入输出讲话。

补充两条：

- 这些进程**没有任何 TCP 监听**（按 PID 查 `Get-NetTCPConnection -State Listen`，空）。
- 有命名管道 `\\.\pipe\codex-ipc`、`codex-browser-use-<uuid>`、`codex-computer-use-<uuid>`。
  但 `codex-ipc` 这个字面量在 `codex.exe` 里与 `code_mode_host` 相邻出现，
  对应的正是桌面版命令行里那个 `features.code_mode_host=true`——
  **是 code-mode 的宿主通道，不是 app-server 的控制面**。

**P-2：两个 app-server 不共享"已加载"状态**

桌面版开着、明明有对话开着的同时，我们自己那个实例：

```
thread/loaded/list => 0 row(s)
```

**P-3：`thread/resume` 成功，但是从磁盘加载，不是 rejoin**

```
thread/resume => OK
  thread.status   = {"type":"idle"}
  liveResume      = null                     ← 关键：没有活的会话可接
  modelProvider   = "custom"                 ← 这条对话确实走的中转站路由
  path            = C:\Users\Borg\.codex\sessions\2026\09\02\rollout-….jsonl
  返回还带 model / sandbox / approvalPolicy / reasoningEffort / initialTurnsPage
```

§1.2 里那句"rejoins that thread"是**同一个 app-server 实例内**的语义。
跨进程时 `liveResume` 为 null，我们拿到的只是磁盘上的那份。

### 结论

| | 结论 |
|---|---|
| **A 同席**（手机发的轮次桌面版当场可见） | ❌ **Windows 上不成立**。桌面版的 app-server 无监听面，`daemon`/`proxy` 又是 Unix-only |
| **B 离线接管**（共享 `~/.codex`） | ✅ **成立**。能列、能读、能 resume、能续、能 fork |

也就是说：**能"打入"，但打进去的是同一份磁盘状态，不是同一个活进程。**

### 由此产生的新风险：两个 app-server 写同一个 rollout

B 路线下，如果桌面版**正开着某条对话**，而我们对同一条 `thread/resume` 再 `turn/start`，
两个进程会往**同一个 rollout 文件**追加。轻则桌面版看不到、重则互相覆盖。
所以真要做，第一版必须**只对桌面版没有打开的对话动手**，
并且要有一个"这条对话正被桌面版占用"的判据（`recencyAt`？文件锁？——本身也要验）。

## 4. 待验清单（都要你点头，且大多需要桌面版开着）

| 编号 | 内容 | 需要什么 | 风险 |
|---|---|---|---|
| ~~P-1~~ | ✅ **已跑**：桌面版的 app-server 是 stdio 私有子进程，无 `--listen`、无 TCP 监听；`codex-ipc` 属于 code-mode 而非控制面（§3） | —— | —— |
| ~~P-2~~ | ✅ **已跑**：`thread/loaded/list` 返回 0 行 → 两个实例不共享已加载状态（§3） | —— | —— |
| ~~P-3~~ | ✅ **已跑**：`thread/resume` 成功但 `liveResume=null` → 是磁盘加载而非 rejoin（§3） | —— | —— |
| **P-3b** | 判断"某条对话此刻是否被桌面版占用"的可靠依据（避免两个进程写同一个 rollout） | 桌面版开着 | 只读 |
| **P-4** | 对一条**已结束**的桌面对话 `thread/resume` + `turn/start` 发一句话，然后在桌面版重开这条对话，看新轮次在不在 | 消耗真实额度、写入 rollout | 中：会改动你的一条真实对话 |
| **P-5** | `remoteControl/enable {ephemeral:true}` 在 Windows + **apiKey 账号**下是否可用；成功则 `pairing/start` 看配对码，然后 `disable` | 会向 OpenAI 注册一个 environment | 中：涉及账号侧状态，需明确同意 |
| **P-6** | 用 `config/value/write` 改 `model_provider`/`base_url`，看是否比直接写 config.toml 更稳（尤其官方登录重写之后） | —— | 低 |

P-1/P-2/P-3 已完成，§3 的 A/B 已定。**下一步是 P-3b 与 P-4**：
前者决定"什么时候可以安全地接管一条对话"，后者决定"接管之后桌面版看不看得见"。
P-5（remoteControl）与它们独立，但它可能让整条路线换一个更好的形态，值得早点问。

---

## 5. 三件必须先说的话

1. **app-server 是 owner 级的面。** 它带 `fs/*`、`process/*`、`command/exec`。
   我们在 Paseo 那条路上花了大力气把窄契约做成"手机端够不到终端和文件"，
   这里同样适用：**小白客户端可以连 app-server，但不能把 app-server 原样转给手机**。
   窄契约那套（`tools/paseo-adapter`）正好可以直接复用——换个后端而已。
2. **它是 experimental。** `app-server`、`remote-control` 在 CLI help 里都标着
   `[experimental]`，生成协议还要 `--experimental` 才全。
   OpenAI 随时可能改语义；这跟 Paseo 那条"协议还在动"的判断是同一类风险，
   处理方式也一样：**只让一层（bridge）认识它**。
3. **`remoteControl` 可能是官方正在做的"手机连电脑"。** 如果它成熟，
   我们要想清楚是"自己造通道"还是"当官方通道里的一个配对客户端"——
   这会直接影响 [`共飞 Paw 远程操作整体架构.md`](共飞%20Paw%20远程操作整体架构.md) 里
   自建 relay 那条路值不值得继续投入。**P-5 就是回答它的。**

---

## 6. 和另外两条路的关系

| 路线 | 能拿到桌面版的会话吗 | 代价 |
|---|---|---|
| **app-server（本篇）** | ✅ 离线接管（已验）；同席在 Windows 上不可能（已验） | experimental；owner 级面要自己收窄；两进程写同一 rollout 的风险（§3） |
| base_url 本地转发（[另一篇](小白客户端本地中转模式调研.md)） | 只能**旁观**（看流量），无法接管 | 单点故障：客户端不开＝桌面版不能用 |
| Paseo | ❌ 看不到桌面版的会话，它跑的是自己的 codex 会话 | 私有 node + 400 MB；但 agent 能力最完整 |

P-1/P-2/P-3 跑完之后可以这样定：**app-server 依然是"打入桌面会话"最好的一条**——
不劫持流量、不依赖 CDP、用官方明确支持的语义，而且**读**这一侧（列表/内容/续聊）完全够用。
它做不到的是"桌面版当场看见"，那需要 CDP 注入或者官方的 remoteControl（P-5）。

base_url 转发那条路的价值因此退回到"密钥不落盘 + 路由集中 + 实时旁观"：
它是唯一能**实时**看到桌面版正在发生什么的方式（app-server 只能事后读盘），
代价是 §单点故障。两条路解决的是不同的时间点。
