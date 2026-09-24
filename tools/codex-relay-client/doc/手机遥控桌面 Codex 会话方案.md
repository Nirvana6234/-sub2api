# 手机遥控桌面 Codex 会话方案

版本：草稿 v0.6（2026-09-24）
范围：共飞助手（小白端，`tools/codex-relay-client`）+ 共飞服务端（`backend`）+ 手机端（Paw，`tools/chat` 的 PWA）
状态：方案阶段，未写代码。§3 的实测结论是本方案的地基；§10 是已定和待定的事项。

v0.2 变更（用户 2026-09-23 定）：手机可见的会话**由用户在小白端逐条勾选，最多 5 条**；小白端新增「**同步会话**」页签，v1 只支持 Codex，Claude Code 后续再接；**v1 不做跑完通知**。

v0.3 变更：新增 §6「会话内容同步」（数据来源、投影格式、同步协议、按需取大内容、手机缓存），各章相应调整。

v0.6 变更：R0 的 12 项实测全部完成，结果见 §12；据此改了发送排队（§5.2b）、运行中步骤的显示（§6.3、§6.8）、审批状态的获取（§6.4）、调用方会话的选法（D-3）、管道发现（§3.1、§5.1）。

v0.5 变更：新增 §3.5「会话权限模式对遥控的影响」；勾选区标注每条会话的权限模式。

v0.4 变更（审核后修订）：更正「服务端被攻破也无法执行任意命令」的错误结论，新增 D-8「指令端到端签名」；补手机配对后的身份凭据、长连接的 token 过期、多实例路由、取消勾选时的通知；修正委派消息的识别规则、接口与数据表的前后矛盾。

---

## 1. 目标

用户在小白端勾选要同步的 Codex 会话（最多 5 条）。手机上能看到这几条会话，并能**接着在桌面会话里发指令**。核心体验对齐官方 remote control：

- 电脑上的 Codex 界面**当场**出现这条消息，并接着跑；
- 手机能看到这一轮的进展（读了什么、跑了什么命令、改了哪些文件）以及最终回复。

v1 不做的：

- 跑完通知，放 v2；
- 手机上批准审批（只能显示「等待电脑批准」）、打断正在跑的一轮、逐字流式输出，原因见 §3.3，后续怎么补见 §11；
- Claude Code 会话，但架构上预留接入点，见 §5.7。

## 2. 为什么不用前几条路

| 路线 | 问题 |
|---|---|
| 官方 remoteControl | 必须 ChatGPT 登录，API key 直接拒绝；而且要把 `chatgpt_base_url` 指向助手，再把其余 ChatGPT 后端请求原样透传，工程量大（§3.4） |
| 另起一个 `codex app-server` 接管会话 | Windows 上和桌面版**不是同一个实例**，做不到"同席"。桌面版开着同一条会话时，两个进程会往同一个 rollout 文件追加内容 |
| base_url 指向助手、做转发 | 只能看到流量，不能往桌面会话里发消息；桌面界面也不会显示手机发的那几轮 |
| CDP 注入：往输入框填字再点发送 | 能做，但依赖 DOM 结构，界面一改就失效，还必须带调试端口重启桌面版 |
| **桌面版自带的 app-tools 管道（本方案）** | 桌面版自己执行，界面当场显示，不改 config，不要求 ChatGPT 登录。缺点是未公开接口，且没有审批、打断 |

## 3. 实测结论（2026-09-23，Codex 桌面版 26.903.8094.0）

### 3.1 管道是什么

- 桌面版启动 app-server 时注入了一个内置 MCP 服务 `codex_app`，这个服务通过环境变量 `CODEX_APP_TOOLS_PIPE_PATH` 连回 Electron 主进程，管道名形如 `\\.\pipe\codex-browser-use-<uuid>`，**每次启动随机**。
- 本机有**若干个** `codex-browser-use-*` 管道（实测 2～7 个，新建会话前后还会增减），其中**只有一个能回应 `tools/list`**，它才是 app-tools，其余回 `No handler registered`。开第二个窗口不会多出 app-tools 管道，也不会多出 app-server 进程（V-7）；但**桌面版每次重启，app-tools 管道都会换名**。
- 帧格式：**4 字节小端长度 + UTF-8 JSON-RPC 2.0**，单帧上限 8 MB。
- 方法只有两个：
  - `tools/list {threadStartKind:"all"}`：返回 27 个工具；
  - `tools/call {arguments, callId, namespace:"codex_app", threadId, tool, turnId}`。
- 参考实现：`…\WindowsApps\OpenAI.Codex_*\app\resources\plugins\openai-bundled\plugins\codex-app-tools\server.mjs`。
- **没有任何身份校验**：一个普通的外部 Python 进程就能直接连上、直接调用。

### 3.2 已验证可用的工具

| 工具 | 用途 | 实测 |
|---|---|---|
| `list_threads` | 会话列表，含 `status`（`idle` / `notLoaded` / `systemError` / 运行中）、`cwd`、标题、摘要 | ✅ |
| `read_thread` | 按轮读取用户消息、回复、命令，带翻页游标 | ✅（委派进来的那一轮读出来 `items` 为空，见下文） |
| `send_message_to_thread` | 往已有会话里发消息 | ✅ 0.2–0.4 s 返回，桌面版随即开新一轮 |
| `wait_threads` | 等最多 8 条会话里的任意一条跑完，或需要人处理 | 调得通；**不能等调用方自己那条** |

端到端实测过两次：

1. 在已开着的会话里发「只回复 OK」：6–8 秒后回复。用户确认**桌面界面当场出现、看起来像自己发的**。
2. 在新建的会话里发「在《教师Agent专用智能体需求评审表.md》末尾加第 5 条」：25 秒内完成读文件、`apply_patch`、核对、一句话回复，磁盘上的 diff 恰好只多了那一行。模型**没有**因为消息是委派来的而拒绝或反问。

### 3.3 必须知道的几个坑

1. **`threadId`（调用方）必须是真实存在的会话 id。** 传全 0 会回 `-32000 Codex app tool request failed`。实测**调用方会话里不会被写入任何记录**，借用它的 id 没有副作用；只有目标会话的模型能在 `<codex_delegation>` 里看到这个 id。
2. **消息在 rollout 里不是 `userMessage`。** 它记成 `function_call_output name=send_message_to_thread`，内容是 `<codex_delegation><source_thread_id>调用方</source_thread_id><input>…</input></codex_delegation>`。所以 `read_thread` 汇总这一轮时是空的，**进展和回复必须直接读 rollout 文件**（§5.3）。
3. **MCP 配置里的 `approval_mode: prompt` 对直连管道不生效。** 发消息不会在桌面上弹确认框。
4. **会话按它自己的权限设置执行。** 实测的会话都是 `approval_policy=never` + `danger-full-access`，也就是说手机发出的指令会以完全权限在电脑上执行，中间不经过任何确认。
5. **老会话的模型问题。** 9 月 13 日用 `gpt-6-astra` 建的老会话，在「本地代理（ChatGPT 订阅直连）」下每轮都报 `'gpt-6-astra' model is not supported when using Codex with a ChatGPT account`。会话设置显示 `gpt-5.5`、或者调用时显式传 `model:"gpt-5.5"`，都不起作用；新建会话就正常。R0 查清了原因（V-5）：ChatGPT 订阅不接受 `gpt-6-astra`、`gpt-5.6-sol` 这类模型，每一轮用的是会话自己的模型，`model` 参数不生效。这不是管道的问题，手机端要把错误原文转给用户。
6. **管道没有打断、没有审批响应、没有 token 级增量。**

### 3.4 官方 remoteControl 的实现（读 codex 源码所得，供后备方案参考）

- 这是 app-server 内置的一个 transport：app-server 主动用 WebSocket 连 `{chatgpt_base_url}/wham/remote/control/server`，信封 `{type, client_id, stream_id, seq_id, message}` 里包的是普通 JSON-RPC。手机等于是桌面版**同一个 app-server** 的又一个客户端。
- URL 校验放行 `localhost` 的 http；开关持久化在 state sqlite 里。
- `load_remote_control_auth` 要求 ChatGPT 登录，API key 直接拒绝。

### 3.5 会话权限模式对遥控的影响

同步**不要求**会话处于任何特定模式：管道在哪种模式下都能发消息，消息按那条会话自己的权限设置执行。手机端**改不了**会话的权限模式（管道里没有这个工具），只能在桌面上切换。

本机未归档会话的实际分布（2026-09-23，读 `state_*.sqlite` 的 `threads.sandbox_policy` 与 `threads.approval_mode`）：

| 桌面上的模式 | 会话库里的记录 | 条数 |
|---|---|---|
| 完全访问 | `sandbox_policy={"type":"disabled"}`，`approval_mode=never` | 79 |
| 自动（默认） | `sandbox_policy={"type":"managed",…}`：全盘可读，只有项目目录和临时目录可写，`network=restricted`；`approval_mode=on-request` | 约 18 |
| 沙箱、从不审批 | `sandbox_policy={"type":"managed",…}`，`approval_mode=never` | 约 15 |

从手机发起后，各模式的行为：

| 模式 | 手机发起后 | 被恶意指令利用时的损失（§9） |
|---|---|---|
| 完全访问 | 一路跑完，永远不会卡住 | 等于在电脑上执行任意命令 |
| 自动 | 改项目内代码、跑测试都能自动完成；需要联网（如 `npm install`、`git push`）或写项目外文件时，**这一轮停在电脑上等人确认**。v1 手机上显示「等待电脑批准：<命令>」，但批准只能在电脑上点；实测等了 4.5 分钟后批准，这一轮照常跑完（V-6） | 能读全盘文件、改项目目录；联网和越界操作要电脑上点确认 |
| 沙箱、从不审批 | 不会卡住；越界的步骤直接失败，模型会说明原因 | 同上，但越界操作直接失败，不会弹确认 |

结论：想完全靠手机把事情做完，会话得是「完全访问」；「自动」能覆盖大部分改代码的活，但遇到联网或越界会卡住，等用户回到电脑前。**不强制模式，而是在勾选时标清楚**（§5.6），由用户自己选择。

## 4. 总体架构

```
 手机（Paw PWA）                共飞服务端（主节点）                    电脑
┌──────────────┐ HTTPS+SSE  ┌────────────────────┐   WSS 出站长连   ┌───────────────────────────┐
│ 电脑页签      │──────────▶│ RemoteHub           │◀──────────────│ 共飞助手                   │
│ 会话列表/详情 │◀──────────│ · 设备注册与在线状态 │               │ · RemoteLink（长连）       │
│ 输入框       │   事件推送  │ · 命令路由 + 白名单   │               │ · RemoteCommandPolicy     │
└──────────────┘            │ · 只转发，不落内容    │               │ · DesktopAppToolsClient ──┼─▶ \\.\pipe\codex-browser-use-*
                            └────────────────────┘               │ · SessionContentSync ─────┼─▶ ~/.codex/sessions/**.jsonl
                                                                  │ · 审计日志 / 总开关        │        │
                                                                  └───────────────────────────┘        ▼
                                                                                          Codex 桌面版（自己执行）
```

要点：

- **电脑只出站。** 助手主动连服务端，不开任何监听端口，不需要内网穿透。
- **服务端只做路由。** 不理解 Codex 协议，也不保存会话内容。
- **真正执行的是桌面版。** 助手只负责"递话"和"读记录"，不自己起 codex 进程。
- **长连接落在主节点。** 按 [主从节点方案](../../../docs/MASTER_RELAY_NODES.md)，主节点域名负责网页、登录和小白端控制；遥控属于控制面，不经过从节点。

## 5. 助手侧设计（C#）

新代码放在 `LanAi.RelayClient.CodexBinding`（与 Codex 本机文件打交道）和 `LanAi.RelayClient.Core`（传输与视图模型）。

### 5.1 `DesktopAppToolsClient`：管道客户端

- **发现管道**：枚举 `\\.\pipe\` 下 `codex-browser-use-*`，逐个发送 `tools/list`，谁能正常回应就用谁；应该恰好有一个，多于一个时当作异常上报。桌面版重启后管道名会变，所以**每次连接失败都要重新发现**，不能缓存名字。
- **版本闸门**：连上后对 `tools/list` 的结果做签名比对，比对范围是本方案用到的工具名及其 `inputSchema` 的必填字段。签名不在已知清单里时，进入**只读降级**：列表和阅读照常，发送关闭；手机端提示「桌面版已更新，遥控暂不可用」。桌面版本号从 AppX 包读取，一起上报。
- **接口**：v1 包装四个工具：`ListThreads` / `SendMessage` / `NavigateTo` / `GetThreadStatus`。最后一个用 `read_thread`（`turnLimit:1`）取 `thread.status`：它和 `wait_threads` 都能给出 `activeFlags:["waitingOnApproval"]`，`list_threads` 只说 `active`（V-6）；选 `read_thread` 是因为空闲、等审批、未加载三种状态它都实测过，`wait_threads` 只见过等审批这一种返回形状。内容仍走 rollout（§6）。**不提供通用的 `CallTool(name, args)`**，白名单写死在类型里，想绕也绕不过去。版本闸门只比对这四个工具的签名，其余工具怎么变都不影响 v1。
- **调用方会话**：见 §10 的 D-3。
- **测试**：做一个假管道服务端（同样的帧格式），录制真实报文作为 fixture，和 chat 工作台 `codex-adapter` 的做法一致。

### 5.2 `RemoteCommandPolicy`：命令闸门

手机来的每条命令都要先过这一层，**全部在助手本地判定**，服务端说了不算：

1. 总开关没开 → 拒绝。
2. 命令带的配对 id 不在助手本地的「已批准配对」名单里，或已被吊销 → 拒绝。这份名单只有在电脑上点过确认（§8.2）才会增加，服务端无法替助手批准。
2a. 发送类命令的签名验不过 → 拒绝（D-8）。
3. 命令不在白名单 → 拒绝。v1 的白名单：会话列表、`session.open` / `history` / `subscribe` / `unsubscribe` / `detail`、发送消息、在电脑上打开；不含新建任务。
4. **目标会话不在「已勾选」集合里 → 拒绝。** 手机拿到的会话列表也由助手在本地过滤，只返回已勾选的那几条，手机根本看不到其他会话的 id 和标题。
5. 频率限制，例如每分钟 6 条发送。
6. 每条命令写一行本地审计：时间、设备、会话、指令摘要、结果。

### 5.2a 已勾选会话

- 存在助手本地：`%LOCALAPPDATA%\LanAi.RelayClient\synced-sessions.json`，内容是 `{provider:"codex", threadId, 勾选时的标题, 勾选时间}` 的列表。**上限 5 条**，写成常量 `MaxSyncedSessions = 5`，界面与闸门共用这一个值。
- 服务端不保存这份名单。它只是助手本地的放行规则，服务端没有能力扩大这个范围。
- 已勾选的会话如果在桌面上被归档或删除（`list_threads` 里找不到了），在页签里标成「已失效」，并且不占名额，由用户自己移除。不自动删，免得用户以为勾选丢了。
- 取消勾选**立即生效**：助手停止跟读这条会话，向正在订阅的手机推 `session.revoked`，之后针对它的命令一律拒绝。手机收到后关闭页面并删掉本地缓存。

### 5.2b 发送排队

实测（V-1）：会话**正在跑**时再发一条，不报错、也不开新一轮，而是在当前这一轮的下一个步骤点**被插进去**（这次隔了 21 秒），模型随即按新消息收尾，**上一条指令里没做完的部分被直接放弃**。所以：

- 会话运行中时，手机发出的消息**默认由助手在本地排队**，这一轮 `task_complete` 之后再真正发送；手机上显示「排队中 · 本轮结束后发送」，可以撤回。
- 另外提供一个明确的「插入当前这一轮」选项，并说明它可能让上一条指令半途而废。
- 排队只在助手内存里，助手退出则丢弃，并在手机上标「未发送」。

### 5.3 `SessionContentSync`：会话内容

负责读 rollout、建轮次索引、投影成手机格式、跟读增量、按需取大内容。设计单独成章，见 §6。

### 5.4 `RemoteLink`：连服务端的长连接

- `wss://<主节点域名>/api/v1/remote/agent`，用账号会话的 JWT 鉴权，并带上 `X-Remote-Device-Id`（助手首次启动时生成并落盘）。
- 帧为 JSON：`{id, kind:"cmd"|"result"|"event", ...}`。服务端下发 `cmd`，助手回 `result`，进展主动上推 `event`。
- 断线按 1/2/5/15/30 秒退避重连；重连后补发 `hello`（桌面版在不在、版本签名是否通过、总开关状态）。
- 注意 `SESSION_BINDING_MISMATCH`：后端把会话绑在 IP + UA 上，长连接的 UA 必须和助手登录时用的一致（这是 chat 工作台接后端时踩过的坑）。
- **token 过期**：长连接只在建立时校验 JWT，而 access token 是短期的。服务端记住它的过期时间，到期主动断开；助手在刷新 token 后重连。不能出现 token 已被吊销、长连接却还活着的情况。

### 5.5 桌面版没开时

- 管道不存在时，手机上显示「电脑上的 Codex 未运行」，并给一个「在电脑上启动」按钮。
- 按钮复用 `67b49ea2` 里「开启本地代理时顺带启动 ChatGPT」的逻辑：只在没运行时启动，**绝不重启正在运行的桌面版**。启动后等管道出现，再继续。
- v1 **不做**「另起 app-server 接管离线会话」，列入 §11。

### 5.6 界面：「同步会话」页签

在左侧页签新增「同步会话」，从上到下依次是：

1. **状态行**：例如「Codex 桌面版 26.903 · 管道正常 · 已连服务端」；哪一环断了，就直接写哪一环。
2. **总开关**「允许手机同步」，默认关。
3. **会话勾选区**，顶部显示「已选 3 / 5」：
   - 列出 `list_threads` 返回的最近会话（先取 30 条），每行显示标题、项目目录、最近更新时间、状态、**权限模式**，前面是勾选框；
   - 权限模式从 `state_*.sqlite` 的 `threads.sandbox_policy` / `approval_mode` 读（§3.5），显示为：
     - 「**完全访问 · 手机指令将直接执行**」：醒目的警示色；勾选时弹一次确认，说明服务端被攻破时的后果（§9）；
     - 「自动 · 越界操作需在电脑上确认」；
     - 「沙箱 · 越界操作会直接失败」；
   - 模式读不出来或者不认识时，按「完全访问」对待，宁可多提醒；
   - 会话在桌面上改了模式，助手在下一次刷新列表时更新标注。已勾选会话被改成「完全访问」时，页签里给出提示，不自动取消勾选；
   - 已经选满 5 条时，其余行的勾选框置灰，悬停提示「最多同步 5 个会话，请先取消一个」；
   - 已勾选但在桌面上已经不存在的会话，单独列在最上面，标「已失效」，只能移除；
   - 顶部有一个来源切换「Codex / Claude Code」，v1 只有 Codex 可选，Claude Code 置灰并注明「即将支持」。
4. **已配对手机**：设备名、最近在线时间、吊销按钮，以及「配对新手机」按钮（流程见 §8.2）。
5. **最近 50 条同步记录**（审计）。

### 5.7 为 Claude Code 预留

v1 只实现 Codex，但要把「会话来源」抽象出来，免得以后接 Claude 时重写一遍：

- 接口 `ISyncedSessionSource`：`ListSessions` / `OpenSession` / `ReadHistory` / `Subscribe` / `ReadDetail` / `SendMessage`，与 §6.4 的协议一一对应。Codex 的实现由 `DesktopAppToolsClient`（列表、发送）和 `SessionContentSync`（内容）组合而成。
- §6.3 的统一投影格式（`SyncItem`）与来源无关，Claude 的实现只需把自己的记录投影成同一套格式，手机端不用改。
- 已勾选名单、手机协议里的会话，都带 `provider` 字段（`codex` / `claude`）。
- 5 条上限是**所有来源合计**，还是每个来源各 5 条，等接 Claude 时再定。v1 只有一个来源，不影响。
- Claude Code 那边怎么往运行中的会话里送消息，目前没有调研。它和 Codex 桌面版的机制完全不同，不能假设有对应的管道。

## 6. 会话内容同步

### 6.1 同步什么

对每条已勾选的会话，手机要能看到：

- **完整对话**：历史轮次 + 正在跑的这一轮；
- **双向**：用户在电脑上亲手发的消息、桌面版的回复，手机也能看到；手机发的消息，电脑上也能看到（这一点由桌面版自己保证，§3.2）；
- **会话状态**：运行中 / 空闲 / 出错，以及标题、模型、项目目录。

### 6.2 数据来源：只以 rollout 为准

内容**全部从 rollout 文件读**；`list_threads` 只用来拿状态和标题。原因：

- rollout 是桌面版自己写的**只追加日志**，用户亲手发的、委派进来的、模型回复、命令、改文件全在里面，是唯一完整的来源；
- `read_thread` 读不出委派进来的那一轮（§3.3-2），输出还被它截断。

对本机最近 40 个 rollout 文件做的统计（2026-09-23）：

| 指标 | 数值 | 对设计的影响 |
|---|---|---|
| 文件大小 | 中位数 335 KB；最近 40 个里最大 18.6 MB，全部 127 个里最大 **93.6 MB** | 不能每次全量发，要建轮次索引、分页 |
| 单行最大 | **2.3 MB**（一条命令的输出） | 命令输出必须截断，全文按需另取 |
| `item_started` | **0 条** | `item_completed` 只在步骤完成时写；但**发起工具调用的那一行是调用一开始就写的**（V-10），所以「正在执行哪条命令」看得到，逐字输出看不到（§6.3、§6.8） |
| 同一内容的两份记录 | `event_msg/item_completed`（给界面的）和 `response_item`（给模型的，含加密推理） | 以 `item_completed` 为准；`response_item` 只读工具调用那一行，用来显示运行中的步骤 |

读文件的规则：

- 路径从 `~/.codex/state_*.sqlite`（只读打开，取编号最大的那个）的 `threads.rollout_path` 查，路径带 `\\?\` 前缀，要去掉。
- 以 `FileShare.ReadWrite` 打开，**只处理以换行结尾的完整行**；末尾写了一半的行留到下次再读。
- 按 **turn_id** 归并，不能按时间窗。实测中，用户自己在桌面上发的那一轮先跑完，按时间窗等 `task_complete` 会误判。

### 6.3 投影：手机看到的统一格式

助手把 rollout 记录投影成 `SyncItem`，手机只认这一套格式。每条都带 `{threadId, turnId, itemId, seq}`，其中 `seq` 是这一行在 rollout 里的**字节偏移**：单调递增、全局唯一，直接拿来当同步游标用（§6.4）。

| rollout 记录 | `SyncItem.kind` | 给手机的内容 | 大小处理 |
|---|---|---|---|
| `UserMessage` | `user` | 文本；图片以引用形式给出 | 图片按需取（§6.5） |
| `FunctionCallOutput`，`name=send_message_to_thread`，**且 `output` 以 `<codex_delegation>` 开头** | `user` | 取 `<codex_delegation>` 里的 `<input>`；能和本地审计记录对上的，标「来自手机」，对不上的标「委派消息」 | — |
| `AgentMessage`，`phase=commentary` | `progress` | 进展说明 | — |
| `AgentMessage`，`phase=final_answer` | `reply` | 最终回复，手机用 Markdown 渲染 | — |
| `Reasoning` | `thinking` | 只给 `summary_text`，默认折叠；加密内容**永不外发** | — |
| `CommandExecution` | `command` | 命令（优先用 `parsed_cmd.cmd`，去掉 `pwsh.exe -Command` 这层外壳）、状态、退出码、耗时、输出的最后 20 行 | 预览最多 2 KB；全文按需取，上限 64 KB（取尾部） |
| `FileChange` | `fileChange` | 每个文件：相对 `cwd` 的路径、增 / 改 / 删、增删行数 | diff 按需取，上限 200 KB |
| 其余的 `FunctionCallOutput`（例如这条会话里的 agent 自己调工具得到的返回值） | `tool` | 一行摘要：工具名 | 结果不外发 |
| `McpToolCall` / `WebSearch` / `Extension` | `tool` | 一行摘要：`server.tool` 或搜索词，外加状态 | 结果不外发 |
| `ImageView` | `image` | 图片引用 | 按需取 |
| `response_item` · `function_call`（`name=exec_command` 或 `shell_command`）或 `custom_tool_call`（`name=exec`，code mode 的会话） | `running` | 「正在执行：<命令>」卡片。命令取 `arguments.cmd` / `arguments.command`；code mode 的会话从 `input` 的 JS 里取 `cmd:` 或 `command:` 字段，取不到就显示「正在执行脚本」 | 只取命令文本 |
| `response_item` · `function_call_output`，且对应一条 `shell_command` 调用 | `command` | **`shell_command` 从不产生 `CommandExecution`**（本机 9,432 次调用里 0 次，当前 0.153.4 仍在用它），结果只能由调用行加这一行拼出来：`Exit code: N` / `Wall time` / `Output:` 之后是输出；`exec command rejected by user` 记为拒绝，`execution error:` 记为失败。跟读时调用和输出常在两次读里，助手保留跟读状态，换一个游标就从这一轮开头重放 | 同 `CommandExecution` |
| `SubAgentActivity` | `tool` | 「子代理 <kind>：<agent_path>」 | — |
| `ContextCompaction` / `compacted` | `notice` | 分隔线「上下文已压缩」 | — |
| `task_started` / `task_complete` / `turn_aborted` | 轮次边界 | 开始 / 结束、耗时、`error` **原文** | — |
| `token_count`、`token_usage_record`、`turn_context`、`world_state`、`session_meta`、`thread_settings_applied` | 不同步 | — | — |
| 不认识的类型 | `unknown` | 占位卡片「此类内容请在电脑上查看」，带上类型名 | **不能静默丢弃**；出现就计数，作为桌面版升级闸门的信号 |

### 6.4 同步协议

所有交互都经服务端转发，服务端不解析内容。

| 消息 | 方向 | 作用 |
|---|---|---|
| `session.open {threadId, turns:10}` | 手机 → 助手 | 返回会话头信息、最近 10 轮的投影，以及 `cursor = {rollout 文件标识, 字节偏移}` |
| `session.history {threadId, beforeTurnId, turns:10}` | 手机 → 助手 | 往前翻 10 轮 |
| `session.subscribe {threadId, cursor}` | 手机 → 助手 | 从 `cursor` 开始跟读 |
| `session.delta {threadId, seq, item}` | 助手 → 手机 | 每解析出一条完整记录就推一次 |
| `session.status {threadId, state, elapsed}` | 助手 → 手机 | 运行中 / 空闲 / 出错 |
| `session.resync {threadId}` | 助手 → 手机 | 游标失效，手机需要重新 `open` |
| `session.unsubscribe {threadId}` | 手机 → 助手 | 离开页面；SSE 断开时由服务端代发 |
| `session.revoked {threadId}` | 助手 → 手机 | 这条会话在电脑上被取消勾选，手机关闭页面、删掉缓存 |

要点：

- **轮次索引**：助手第一次打开某条会话时，把整个文件扫一遍，建立「turnId → 起止偏移」的索引并缓存在内存里，之后只做增量更新。`open` 和 `history` 都靠索引直接跳到对应位置，不必每次重扫。实测 93.6 MB、51 轮的文件，Python 按行预筛只要 **0.47 秒**（V-9），C# 只会更快，所以 `indexing` 状态基本用不上。
- **一轮结束之后还可能有这一轮的记录**：实测 `task_complete` 之后，又写进来一条属于同一轮的 `CommandExecution`（后台命令这时才跑完，V-1）。按 `turn_id` 归并时**不能在 `task_complete` 处把这一轮封死**。
- **断线续传不需要任何一方缓存事件**：rollout 只追加，手机重连时带上最后收到的 `seq`，助手从那个字节偏移接着读就行。服务端因此完全无状态。
- **什么时候需要重新同步**：rollout 路径变了，或者文件长度比游标还小，就回 `resync`，手机丢掉这条会话的本地缓存，重新 `open`。实测这两种情况都不会自然发生：每个会话 id 只对应一个文件，压缩上下文是往同一个文件追加，fork 会另建一个新会话（`forked_from_id` 指回原会话），原文件不动（V-9）。`resync` 只是兜底。
- **跟读方式**：`FileSystemWatcher` 负责即时触发，每次触发都读到文件末尾；另有 2 秒轮询兜底。实测连续几次追加可能只触发一次，但因为每次都读到末尾，最后读到的长度与实际一致（V-11）。只跟读有手机订阅的会话，最多 5 条；没有订阅者 60 秒后停止跟读。
- **运行状态**：

| 手机显示 | 判据 |
|---|---|
| 运行中 · 已 42 秒 | rollout 里出现 `task_started`，且这一轮还没有 `task_complete` / `turn_aborted` |
| **等待电脑批准：<命令>** | `GetThreadStatus` 返回的 `activeFlags` 含 `waitingOnApproval`（V-6）。rollout 里**没有任何审批记录**，只是停在那条工具调用之后，命令就取自那条调用 |
| 空闲 | `task_complete` 且没有 `error` |
| 出错：<原文> | `task_complete` 带 `error`；之后 `list_threads` 会一直显示 `systemError`，直到下一轮成功（V-2） |
| 未加载 | `list_threads` 为 `notLoaded`；照样能发，桌面版会自动加载（V-2） |

  运行中每 5 秒调一次 `GetThreadStatus`，用来发现审批等待。实测等批准等了 4.5 分钟，这一轮没有超时，批准后正常跑完。
- **多台手机**：各自订阅、各自的游标，互不影响。
- **单次返回的上限**：`open` / `history` 除了按轮数，还按条数封顶（200 条 `SyncItem`）。一轮里跑了几百条命令也不会撑爆一帧，超出的部分用 `hasMoreInTurn` 标记，手机再往前翻。
- **超时**：`cmd` 类请求服务端等 15 秒；第一次打开大文件要建索引，可能超时，助手先回 `indexing`，手机稍后重试。

### 6.5 按需取大内容

`session.detail {threadId, itemId, part: "output" | "diff" | "image", index}`

- **只能按 `itemId` 取，手机永远不能传文件路径。** 助手在自己解析出的记录里查这个 `itemId`，必须属于已勾选的会话，并且确实在 rollout 里出现过；需要读图片时，路径也由助手从这条记录里取。否则 `detail` 就成了「远程读电脑上任意文件」的后门。
- **图片**：只读 `UserMessage.local_image` 和 `ImageView` 引用的文件，只接受 png / jpg / jpeg / gif / webp；缩到长边 1280、1 MB 以内再发。文件已经不存在就回「图片已删除」（剪贴板截图多在 `%TEMP%`，常被清掉）。
- **上限**：命令输出 64 KB（取尾部），diff 200 KB，单帧 1 MB，超出部分注明「已截断，请在电脑上查看完整内容」。

### 6.6 手机本地缓存

- Paw 已经有 IndexedDB 附件缓存（`7472c3f8`）。在此基础上新增：按 `threadId` 缓存投影后的轮次和最后的 `cursor`。再次打开会话时先显示缓存，再从 `cursor` 补增量。
- 大内容（命令输出、diff、图片）不进缓存，每次按需取。
- **清理**：会话在电脑上被取消勾选（下次拿到的列表里没有它）→ 删掉这条会话的缓存；配对被吊销或退出登录 → 清空全部。

### 6.7 流量估算

- 打开一条会话（10 轮，输出已截断）：通常 < 50 KB。
- 每条增量 < 2 KB；一轮改文件的任务，大约十几条。
- 服务端：纯转发，WebSocket 开 `permessage-deflate` 压缩。
- 助手：最多跟读 5 个文件，轮询开销可以忽略；轮次索引每轮只占几十字节。

### 6.8 已知边界

- **能看到正在执行哪条命令，但看不到它的实时输出**：命令发起时就能显示「正在执行：<命令>」（§6.3），输出要等它跑完才有。`read_thread` 也看不到正在跑的命令（V-10），补不上这一块。运行中的卡片在这一轮出现下一条完成记录、或这一轮结束时收起，不靠 id 配对：code mode 会话里完成记录的 id 是 `exec-…`，和 `call_id` 对不上。
- **推理只有摘要**，加密部分没法外发，也不该外发。
- **「来自手机」的标注靠匹配审计记录**：rollout 里只有 `<codex_delegation>` 的原文，没有来源设备，只能按「发送时间 + 原文」去对本地审计。审计记录被清掉以后，只能标成「委派消息」。

## 7. 服务端设计（Go）

已实现（R3）：`backend/internal/service/remote_hub.go`（长连接与转发）、`remote_sync_service.go`（配对）、`repository/remote_pairing_repo.go`（raw SQL，不走 Ent）、`handler/remote_handler.go`、`server/routes/remote.go`，迁移 `258_remote_pairings.sql`。在线状态在进程内存；配对关系落 `remote_pairings` 表（D-5）。

### 7.1 接口

全部挂在账号会话（JWT）之下。访问某台电脑还要带 `X-Remote-Pairing: <配对令牌>`：只登录同一账号不够。

| 接口 | 调用方 | 作用 |
|---|---|---|
| `POST /api/v1/remote/pair/start` `{device_id, device_name}` | 助手 | 发一个 6 位码（5 分钟有效），同时作废这台电脑之前未完成的码和认领 |
| `POST /api/v1/remote/pair/claim` `{code, phone_label, public_key}` | 手机 | 认领。返回 `pairing_id` 与**配对令牌**（明文只出现这一次，库里只存 SHA-256）。此时状态是 `claimed`，还不能用 |
| `GET  /api/v1/remote/pairings/:id` | 手机 | 等电脑确认：`claimed` → `active` |
| `DELETE /api/v1/remote/pairings/:id` | 手机 / 助手 | 吊销；在线的电脑会收到 `pair.revoked` |
| `GET  /api/v1/remote/devices` | 手机 | 已配对的电脑、是否在线、助手最近上报的状态 |
| `POST /api/v1/remote/devices/:device_id/cmd` | 手机 | 请求 / 应答类命令，原样转给助手，原样返回；15 秒超时（504），电脑不在线 503 |
| `GET  /api/v1/remote/devices/:device_id/sessions/:thread_id/stream?cursor=`（SSE） | 手机 | 跟读一条会话；连上时服务端向助手发 `subscribe`，断开时发 `unsubscribe` |
| `GET  /api/v1/remote/agent?device_id=`（WebSocket） | 助手 | 助手的长连接 |

服务端对 `cmd` 只检查信封：`type` 必须是 `sessions.list` / `session.open` / `session.history` / `session.detail` / `message.send` / `thread.navigate` 之一，体积 ≤ 64 KB。这是第二道防线，第一道在助手本地（§5.2）。

### 7.2 与助手之间的帧

WebSocket 文本帧，JSON：

| 方向 | 帧 | 含义 |
|---|---|---|
| 服务端 → 助手 | `{"kind":"cmd","id":"r12","pairing_id":5,"body":{…手机原文…}}` | 手机的命令；`pairing_id` 让助手按**自己确认过的**配对名单和公钥判定 |
| 助手 → 服务端 | `{"kind":"result","id":"r12","body":{…}}` | 应答，原样回给手机 |
| 服务端 → 助手 | `{"kind":"subscribe","subscription":"s13","pairing_id":5,"body":{"type":"session.subscribe","thread_id":…,"cursor":…}}` | 开始跟读 |
| 助手 → 服务端 | `{"kind":"event","subscription":"s13","body":{…}}` | 一条增量，写成一行 SSE `data:` |
| 服务端 → 助手 | `{"kind":"unsubscribe","subscription":"s13"}` | 手机离开，或手机跟不上被切断（缓冲 64 条满即切断，手机凭游标续传） |
| 服务端 → 助手 | `{"kind":"event","type":"pair.request","body":{"pairing_id","phone_label","public_key"}}` | 有手机认领了码。助手**重连时会补发**所有未确认的认领 |
| 助手 → 服务端 | `{"kind":"pair.confirm","pairing_id":5}` / `{"kind":"pair.reject",…}` | 用户在电脑上确认或拒绝 |
| 服务端 → 助手 | `{"kind":"event","type":"pair.revoked","body":{"pairing_id"}}` | 配对被吊销 |
| 助手 → 服务端 | `{"kind":"hello","body":{…}}` | 状态（桌面版在不在、管道能力），`devices` 列表里原样给手机看 |
| 双向 | `{"kind":"ping"}` / `pong`，外加 WebSocket 自己的 ping（30 秒） | 保活；75 秒收不到任何东西断开 |

### 7.3 规则

- **只能访问自己账号下、且配对已确认的电脑。** 令牌必须属于这个账号、这台电脑、状态 `active` 的配对，三者缺一返回 403。同一个设备 id 在不同账号下互不相干。
- **配对码防猜**：6 位数字；同一账号累计 5 次错码，该账号所有未完成的码全部作废。码一次性，认领即清除。
- **长连接不会比 token 活得久**：JWT 中间件新写入 `token_expires_at`，助手的 WebSocket 到期即以 `1008 token expired` 关闭；助手刷新 token 后重连。
- **浏览器打不开助手的 WebSocket**：带 `Origin` 头的握手一律拒绝（助手是桌面程序，不发 `Origin`）。
- **不落内容**：命令和事件只在内存里转发；日志只记元数据，不记 `prompt`、回复、命令输出和 diff。续传靠 rollout 的字节偏移（§6.4），服务端不暂存事件。
- **限流**：配对与命令接口走面板限流；两条长连接（助手 WebSocket、手机 SSE）不计入。
- **SSE 过反向代理**：`X-Accel-Buffering: no`，每 15 秒一行心跳注释。
- **多实例（未实现）**：主节点若多实例部署，手机请求可能落到没持有该助手长连接的实例上，需要 Redis 发布订阅转发。v1 按单实例部署。

### 7.4 手机与助手之间的内容（R5 照此实现）

服务端对下面这些只看 `type`、原样转发。所有应答都是 `{"ok":true,…}` 或 `{"ok":false,"error":"<code>","message":"<中文说明>"}`。

**命令**（`POST /api/v1/remote/devices/:device_id/cmd`，请求体即命令）：

| `type` | 其余字段 | 成功时的应答 |
|---|---|---|
| `sessions.list` | — | `desktop_running`；`sessions[]`：`thread_id`、`provider`、`title`、`cwd`、`status`（`idle` / `active` / `notLoaded` / `systemError` / `unknown` / `missing`）、`permission`（`full_access` / `auto` / `sandboxed`）、`updated_at` |
| `session.open` | `thread_id`，`turns`（默认 10） | `session`（`thread_id`、`title`、`cwd`、`model`、`permission`、`open_turn_id`）、`items[]`、`has_older`、`truncated_turn_id`、`cursor` |
| `session.history` | `thread_id`，`before_turn_id`，`turns` | `items[]`、`has_older`、`truncated_turn_id` |
| `session.detail` | `thread_id`，`turn_id`，`item_id`，`part`（`output` / `diff` / `image`），`index` | `text`，或 `media_type` + `data`（base64）；`truncated` |
| `message.send` | `thread_id`，`text`（≤ 8000 字），`mode`（`queue` 默认 / `insert`），`ts`（毫秒），`nonce`，`sig` | `queued`：`true` 表示会话正忙，助手会在这一轮结束后再发 |
| `thread.navigate` | `thread_id` | — |

`items[]` 每项：`seq`、`turn_id`、`item_id`、`kind`（`user` / `progress` / `reply` / `thinking` / `command` / `file_change` / `tool` / `image` / `running` / `notice` / `turn_started` / `turn_ended` / `unknown`），按需带 `text`、`origin`（`desktop` / `phone` / `delegated`）、`image_count`、`command`、`exit_code`、`status`、`duration_ms`、`output_preview`、`output_truncated`、`files[]`（`path`、`change`、`added`、`removed`）、`outcome`（`completed` / `failed` / `aborted`）。`running` 卡片在同一轮出现任何后续条目时由手机收起。

常见错误码：`disabled`（电脑上关着）、`not_approved`（这台手机没在电脑上确认）、`not_selected`（会话没勾选）、`bad_signature`、`rate_limited`（每分钟 6 条）、`desktop_unavailable`（桌面版没开）、`missing`、`refused`。

**跟读**（SSE，`?cursor=` 取自 `session.open` 的 `cursor`）：每行 `data:` 是一个事件：

| `type` | 含义 |
|---|---|
| `items` | 新条目 `items[]` 与新的 `cursor`，手机保存它，重连时带上 |
| `status` | `status` 与 `waiting_on_approval`；为 `true` 时显示「等待电脑批准」 |
| `resync` | 游标失效，重新 `session.open` |
| `revoked` | 这个会话在电脑上被取消勾选，或这台手机被解除配对：关闭页面、删缓存 |

另有服务端自己的 `event: end`（电脑断开或手机跟不上），带着最后的 `cursor` 重连即可。

**游标** 形如 `123456.ab12cd34`：字节偏移加上 rollout 路径哈希的前 8 位。手机当它不透明的字符串用。

**签名（D-8）**：配对时手机用 WebCrypto 生成 **ECDSA P-256** 密钥对（私钥设为不可导出），把公钥的 **SPKI 的 base64** 作为 `public_key` 提交。每条 `message.send` 对下面这个 UTF-8 字符串签名（`
` 为换行，各段之间没有空格），签名格式是 WebCrypto 的原生 r‖s（64 字节），base64 后放进 `sig`：

```
cofly-remote/1
message.send
{pairing_id}
{thread_id}
{mode}
{text 的 UTF-8 做 SHA-256 后的小写十六进制}
{ts}
{nonce}
```

`nonce` 为 16～128 个字符的随机串（建议 18 字节随机数的 base64）。助手拒绝 5 分钟以前的 `ts`，以及用过的 `nonce`。

**指纹**：对 `public_key` 这个 base64 **字符串本身**的 UTF-8 做 SHA-256，取前 3 字节，写成大写十六进制并在第 3 位后空一格（例如 `A1B 2C3`）。手机配对后显示它，电脑的确认框也显示它，两边一致才点确认。

## 8. 手机端（Paw）

### 8.1 页面

新增「电脑」页签：

1. **设备列表**：在线 / 离线，桌面版是否在运行。
2. **会话列表**：只显示电脑上已勾选的那几条（最多 5 条），状态用色点区分（运行中 / 空闲 / 出错），并显示权限模式的简短标签（完全访问 / 自动 / 沙箱），让用户在手机上发指令前就知道会不会卡在审批上。列表为空时，提示「请在电脑的共飞助手 →『同步会话』里勾选要同步的会话」。
3. **会话详情**：
   - 按 §6.3 的 `SyncItem` 渲染：用户消息（手机发的带「来自手机」标记）、进展说明、步骤卡片（命令、改文件、工具）、最终回复（Markdown）；
   - 命令卡片点开看输出，改文件卡片点开看 diff，图片点开加载，都是按需取（§6.5）；
   - 运行中顶部显示「运行中 · 已 42 秒」；
   - 上滑加载更早的轮次（`session.history`）；再次打开时先显示本地缓存（§6.6）。
4. **输入框**：v1 只发纯文本，不带附件，单条上限 8000 字。空闲时发送后立刻显示「已送达电脑」，随后进入进展流；运行中发送则默认排队，显示「排队中 · 本轮结束后发送」，另有「插入当前这一轮」选项（§5.2b）。
5. **「在电脑上打开」**：调用 `navigate_to_codex_page`，把这条会话切到电脑前台。

### 8.2 配对流程

1. 助手上点「配对新手机」，显示 6 位码和二维码。
2. 手机的「电脑」页签扫码或手动输入；同时在手机上生成签名密钥对，公钥随认领请求提交（D-8）。
3. 助手弹出确认：「允许 iPhone（Safari）遥控本机 Codex？」，同时显示公钥指纹的前 6 位（手机上也显示，两边对得上才点）。**必须在电脑上点确认**。
4. 配对完成后，这台手机就出现在助手的设备列表里。

之所以要在电脑上确认一次：同一账号登录的任意手机都能下发完全权限的指令，账号密码一旦泄露，影响就是整台电脑。

## 9. 安全

| 风险 | 处置 |
|---|---|
| 手机指令以完全权限执行（§3.3-4、§3.5） | 勾选区标注权限模式，「完全访问」会话勾选时需要确认；总开关默认关；配对必须在电脑上确认；只有逐条勾选的会话（最多 5 条）可被操作；本地审计；电脑端随时一键关闭 |
| 账号被盗后被远程执行命令 | 配对是设备级的，光有账号密码不够；吊销立即生效；服务端对新设备配对发站内通知 |
| **服务端被攻破后被下发恶意指令** | **这等于能在电脑上执行任意命令**：往已勾选的会话里发一句「帮我运行 xxx」，会话本身是完全权限。助手本地的白名单只能挡住「调别的工具」，挡不住「发一条恶意消息」。唯一的根治办法是 D-8：手机配对时生成密钥，每条发送都签名，助手验签，服务端伪造不了。不做 D-8，就得在帮助页明确写出「信任共飞服务端」这个前提 |
| 会话内容经过服务端 | 只在内存里转发，不落库、不进日志，也不做续传缓存；方案文档和帮助页写明 |
| 手机借「取详情」读电脑上的任意文件 | 手机只能传 `itemId`，不能传路径；路径由助手从已勾选会话的 rollout 记录里取；图片限定扩展名和大小（§6.5） |
| 会话内容留在手机上 | 只缓存投影后的文本，大内容不缓存；取消勾选、吊销配对、退出登录时清除（§6.6） |
| 推理内容外泄 | 只外发 `summary_text`，加密推理永不外发 |
| 模型看到调用方会话 id 后自行回消息过去 | 实测 4 次都没有发生，包括明确写了「把结果告诉我」的那次（V-12）。D-3 借的是已归档会话，万一发生，影响也在用户看不到的地方 |
| 管道本身没有鉴权（本机任何程序都能用） | 这是桌面版原有的问题，不是我们引入的；在帮助页如实说明，不宣称"只有共飞能控制" |
| 未公开接口随桌面版升级变化 | 签名闸门 + 只读降级（§5.1）；每次桌面版升级先跑冒烟 |

## 10. 决策

| 编号 | 问题 | 结论 | 状态 |
|---|---|---|---|
| D-1 | v1 是否允许手机「新建任务」（`create_thread`） | 不允许，只能在已勾选的会话里继续 | 默认（用户未表态） |
| D-2 | 手机能操作哪些会话 | **在小白端逐条勾选，最多 5 条**；新增「同步会话」页签 | **用户已定** |
| D-3 | 调用方会话用哪条 | **借一条已归档会话的 id**：实测归档会话可以当调用方（V-3），它不在用户列表里，调用方会话里也不会被写入任何东西（§3.3-1）。没有归档会话时，退回用目标会话之外最近活跃的一条；已删除的会话 id 会失败 | 默认（据 R0 调整） |
| D-4 | 手机发的消息要不要加前缀 | 不加；来源记在审计日志里 | 默认 |
| D-5 | 配对关系存在哪里 | 服务端一张小表 `remote_pairings(user_id, device_id, phone_label, created_at, revoked_at)` | 默认 |
| D-6 | 跑完通知 | **v1 不做**，放 v2 | **用户已定** |
| D-7 | 支持哪些会话来源 | **v1 只支持 Codex**，Claude Code 后续再接（§5.7） | **用户已定** |
| D-8 | 手机发的指令要不要端到端签名 | **建议要**。配对时手机用 WebCrypto 生成 ECDSA P-256 密钥对，公钥在电脑上确认配对时存进助手；每条发送都签 `{pairingId, threadId, 正文哈希, 时间戳, 随机数}`，助手验签，并拒绝 5 分钟以前的和重复的。代价：Paw 与助手各多写约一百行代码，换一台手机要重新配对。不做的话，服务端被攻破就等于能在电脑上执行任意命令（§9） | **用户已定：做** |

## 11. 后续（不在 v1）

- **跑完通知**：助手用 `wait_threads` 等已勾选的会话（最多 5 条，在它 8 条的上限之内），或者直接用 `SessionContentSync` 看到 `task_complete`，然后经服务端推送；手机端做 Web Push。
- **Claude Code 会话**：先调研 Claude Code 怎么往运行中的会话里送消息，再实现 `ISyncedSessionSource`（§5.7）。
- **打断**：管道里没有这个工具。可选做法：CDP 点桌面上的停止按钮（复用 `AiSwitch.Injection` 那套），或者等官方把 interrupt 加进 app-tools。
- **审批**：v1 已经能在手机上显示「等待电脑批准：<命令>」（§6.4）。真正在手机上批准，只能走 CDP 或官方 remoteControl。
- **桌面版没开时接管会话**：另起 app-server 做 `thread/resume` + `turn/start`，此前已测通（见 `codex-app-server` 调研）。要先解决「桌面版随后打开同一会话」的冲突判定。
- **官方 remoteControl 后备**：只对 ChatGPT 登录用户可用（§3.4）。

## 12. R0 实测结果（2026-09-23 ～ 24）

12 项全部完成。消耗额度的几项都在测试会话里跑，只有 V-2 在一条旧的「hi」会话里留下了一条测试消息和一条报错。

| 编号 | 验证什么 | 结果 | 对设计的影响 |
|---|---|---|---|
| V-1 | 会话运行中再发一条 | 不报错、不开新一轮，在下一个步骤点**插进当前这一轮**；模型按新消息收尾，**上一条没做完的部分被放弃**。`task_complete` 之后还可能写入同一轮的记录 | 默认本地排队（§5.2b）；一轮不能在结束处封死（§6.4） |
| V-2 | 往 `notLoaded` 的会话发 | 能发，桌面版自动加载并开跑；**界面不会跳过去，但会弹通知** | `notLoaded` 的会话可以直接勾选使用 |
| V-3 | 调用方会话已归档或已删除 | 已归档的**可以**用；不存在的 id 失败（`-32000`） | D-3 改为借已归档会话 |
| V-4 | 委派消息在界面上能否区分 | 用户确认：看起来和自己发的一样 | D-4 维持不加前缀 |
| V-5 | 老会话报模型不支持 | 走本地代理（ChatGPT 订阅）时，官方不接受 `gpt-6-astra`、`gpt-5.6-sol`；每一轮用的是**会话自己的模型**，`send_message_to_thread` 的 `model` 参数没有生效 | 不是管道的问题；手机原样显示报错，并提示「请在电脑上给这条会话换个模型」；v1 不传 `model` 参数 |
| V-6 | 「自动」会话遇到审批 | rollout 里**没有任何审批记录**；`list_threads` 只说 `active`；`read_thread` / `wait_threads` 给出 `activeFlags:["waitingOnApproval"]`，`wait_threads` 立即以 `actionableStatus` 返回。等了 4.5 分钟后在电脑上批准，这一轮正常跑完 | 新增 `GetThreadStatus`（§5.1）；手机显示「等待电脑批准：<命令>」（§6.4） |
| V-7 | 多个窗口 | 只有一个 app-tools 管道、一个 app-server 进程；`codex-browser-use-*` 管道总共 2～7 个且会增减；桌面版重启后 app-tools 管道换名 | 每次逐个试探，不缓存名字（§3.1、§5.1） |
| V-8 | 新会话多久进 state sqlite | 会话出现在 `list_threads` 的同一秒，数据库里已有记录，rollout 文件也已存在 | 不需要重试逻辑 |
| V-9 | 大文件建索引；路径会不会变 | 最大 93.6 MB、51 轮，扫一遍 0.47 秒；每个会话 id 恰好一个文件，数据库里的路径全部存在；压缩往同一文件追加，fork 另建新会话 | 首次打开无需等待；`resync` 只是兜底 |
| V-10 | 运行中能否看到正在执行的步骤 | `read_thread` 看不到；但 rollout 里**发起工具调用的那一行在命令启动时就写入**，带完整命令。code mode 的会话走 `custom_tool_call`，完成记录的 id 是 `exec-…`，与 `call_id` 对不上 | 新增 `running` 卡片（§6.3）；运行中的卡片不靠 id 配对（§6.8） |
| V-11 | `FileSystemWatcher` 是否可靠 | 11 次追加触发了 17 次事件，只看到 9 种长度（连续追加会合并），但最后一次读到的长度与实际一致 | Watcher + 读到末尾 + 2 秒轮询兜底 |
| V-12 | 模型会不会往调用方回消息 | 4 次都没有，包括写了「把结果告诉我」的那次 | 维持 D-3 |

## 13. 任务拆分

| 编号 | 内容 | 依赖 |
|---|---|---|
| R0 | 跑完 V-1 ~ V-12，把结论回填本文。**已完成**（§12） | — |
| R1 | `DesktopAppToolsClient`：管道发现、帧格式、签名闸门、假管道服务端与 fixture 测试。**已完成**，并对真桌面版实测通过 | R0 |
| R2 | **已完成。** `SessionContentSync`：找 rollout 文件、轮次索引、`SyncItem` 投影、跟读与游标、按需取详情（含路径防护）；用真实 rollout 片段做 fixture，覆盖每一种 item 类型、半行、`resync` | R0 |
| R3 | 服务端 `remote` 模块：WS hub、配对（含令牌与公钥转交）、命令路由、SSE、白名单、配对表、token 过期断开。**已完成**（§7） | D-5 D-8 |
| R4 | 助手 `RemoteLink` + `RemoteCommandPolicy`（含已批准配对名单、验签）+ 已勾选名单（上限 5）+ 审计 + 「同步会话」页签。**已完成**（`Core/DesktopSync/`、`DesktopSyncPage`） | R1 R2 R3 |
| R5 | Paw「电脑」页签：设备列表、已同步会话列表、会话详情（`SyncItem` 渲染、按需详情、上滑翻历史）、增量订阅与续传、IndexedDB 缓存与清理、输入框、配对（含密钥生成与签名） | R3 |
| R6 | 端到端：真手机 → 真服务端 → 真助手 → 真桌面版，走一遍改文件；做一次安全自查 | R4 R5 |
