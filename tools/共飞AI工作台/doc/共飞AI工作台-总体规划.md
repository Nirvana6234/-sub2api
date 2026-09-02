# 共飞 AI 工作台 —— 总体规划

> 状态：规划草案 **v2**
> 日期：2026-09-02
> v2 的变化：**Paseo 退出运行时链路**，改为直接驱动 codex。v1 存档在 `共飞AI工作台-总体规划.v1-paseo.md`。

---

## 0. 要做的是什么

**Chat 是产品，codex 是引擎。** 用 codex 的 agent 逻辑补上 Chat 缺的那一块：能读代码、能改文件、能跑命令、能自己迭代到收敛，并且能从手机接管继续。

- **主体**：[`tools/chat`](../../chat) —— Next.js + Tauri 桌面壳 + PWA，三形态同一份代码。agent 宿主就装在桌面壳里（D-4）。
- **引擎**：codex（开源，Rust）。**我们只要它的 agent 逻辑**，通过它的 app-server 协议驱动（§2.1）。
- **服务端**：`backend/`（Go），已有账号、鉴权、模型路由、计费与 Paw 对话网关。
- **手机**：同一份 PWA，暂不发原生（D-5）。

**不做通用 Agent 平台，不做可插拔 Runtime 市场，不开发 codex 本身。** codex 是按版本引入的上游件，我们只写适配器、跟版本、按版本打包（§2.3）。

**Paseo 不在运行时链路里**（v2 的主要变化，理由见 §1.3）。它和小白端一样，是**参考资产**。

---

## 1. 现状盘点

| 组件 | 位置 | 到了什么程度 | 缺什么 |
|---|---|---|---|
| Chat / Paw | [`tools/chat`](../../chat) | **与服务端的通讯层已经打通**（§1.1）：登录/刷新/401 重放、错误分类、配置与模型目录、SSE 流式解析、附件与图片；桌面 Tauri + Web + PWA 三形态同源 | 缺**会话语义与 agent 能力**：不认识设备、没有长连通道，会话历史只在 localStorage |
| **Tauri 壳** | [`tools/chat/src-tauri`](../../chat/src-tauri) | Tauri 1.5，`main.rs` **只有 7 行**，纯 webview 包装；allowlist 只开了 http 与窗口控制 | 要当 agent 宿主：进程启动、文件系统、托盘、进程监管**全部从零**。**M0 的主战场** |
| 共飞服务端 | [`backend/.../paw.go`](../../../backend/internal/server/routes/paw.go) | `/v1/paw` 下只有 `config`、`config/defaults`、`files`、`images/*`、`chat/completions`——一个纯对话网关 | **没有设备、没有绑定、没有 agent 转发、没有长连**。最大的一块新代码 |
| 小白客户端 | [`tools/codex-relay-client`](../../codex-relay-client) | **参考实现**（§1.2）。WPF + 零 NuGet 的 HTTP 客户端 + `CodexBinding`；246 项测试全绿 | 工作台**一行都不继承**——继承的是它踩过的坑 |
| paseo-adapter | [`tools/paseo-adapter`](../../paseo-adapter) | 窄契约完整、C# 82 + bridge 13 项测试、真实 codex 会话验过 | **v2 起退出运行时链路**（§1.3），留作参考与后手 |

三句话：

1. **PC 端是新代码**：Tauri 壳今天 7 行，Rust 宿主与 codex 客户端都要写。
2. **手机端缺的不是通讯，是通道语义**——鉴权、流式、错误分类、三形态部署都在了。
3. **服务端是唯一没有现成东西可倚的一段**：既没有设备概念，也没有长连转发。

### 1.1 Chat 已有的服务端通讯层（agent 面复用，不重写）

| 已有 | 位置 | 复用价值 |
|---|---|---|
| JWT 登录 / 刷新 / 401 自动重放 / 刷新锁 | [`api.ts`](../../chat/src/client/paw/api.ts) `pawRequest` | agent 面直接挂上去 |
| 会话持久化与过期标记 | [`auth.ts`](../../chat/src/client/paw/auth.ts) | 同上 |
| 服务端错误 → 人话（含 HTML 响应识别） | `parsePawFailure` | 远程链路错误分类挂同一套 |
| SSE 分帧与增量累积 | [`sse.ts`](../../chat/src/client/paw/sse.ts) | 时间线增量渲染是同形状的问题 |
| 配置与模型目录、默认值存服务端 | `/v1/paw/config` | agent 面选模型直接用 |
| 服务地址解析（三形态） | [`config.ts`](../../chat/src/client/paw/config.ts) | 内嵌 `/paw`、Tauri 构建期钉死、PWA |

**唯一的真空白是会话模型**：`usePawClient` 把会话存在 localStorage（`paw-conversations:v2`），服务端每次调用无状态。agent 会话必须反过来——状态在 PC 上权威、换设备也能接上。

### 1.2 小白客户端：参考什么，不搬什么

**必须自己有的账号面**（后端接口都已存在，纯客户端工作），对齐 [`IRelayServerClient`](../../codex-relay-client/src/LanAi.RelayClient.Server/IRelayServerClient.cs)：公开设置驱动的注册入口、注册 + 邮箱验证码 + Turnstile、2FA、个人信息、用量与额度、分组与倍率、API key 租约与换组、订阅与充值、公告。Chat 目前只有登录 + 刷新 + paw config。

**明确不搬**：`RelayInjectionHost` / `CodexStartup` / `MacCodexAppLauncher` 这条**注入官方 Codex 客户端**的路线。

**值得照抄的坑**（都用测试钉死过）：

1. **换分组只发 `group_id`**：`PUT /keys/:id` 的 `expires_at` 是三态语义，带默认值的请求体会把「1 天租约」变成「永不过期」，用户无感；
2. **401 分两义**：登录端点是「密码错」，其他端点是「会话过期」；
3. **高峰倍率只对订阅类分组生效，且不在客户端算「当前是否高峰」**（窗口按服务器时区判定）；令牌响应缺 `access_token` 必须当场失败。

### 1.3 Paseo 为什么退出运行时链路

Paseo 的 daemon 是个**完整的 agent 产品**：它 spawn codex、管会话、管时间线与通知，还带终端 / 文件 / 多 provider / 语音。我们要的只有「agent 逻辑」，而那部分**本来就是 codex 自己的**——Paseo 驱动 codex 的方式就是 `spawn(codex, [..., "app-server"])` 走 stdio JSON-RPC（`codex-app-server-agent.js:5464`）。**我们直接做同样的事，就少了一整层。**

它给的货与收的价：

| 它给的 | 我们的实际处境 |
|---|---|
| codex 进程与会话模型 | **与 codex app-server 重复**——app-server 本来就是给 IDE / 客户端用的 |
| 时间线与通知投影 | 等于把 codex 的事件翻译两次 |
| relay 远程通道（NAT 穿透、E2EE） | **NAT 穿透我们不需要**（有公网服务端，PC 出站长连即可）；**E2EE 在我们的拓扑下本来就不成立**（客户端那端在我们服务器上） |
| 多 provider / 终端 / 文件 / 语音 | 全部关掉，纯负担 |

代价则是实打实的：**node 93 MB + node_modules 415 MB**、三处版本联动（daemon / client / relay 协议）、四个「连上了、报成功、什么都不来」的静默失败坑、每连接约 5 个时间线订阅的上限，以及最难受的一条——**`offer` 就是完整 owner 凭据、relay 路径绕过 daemon 口令**（实测 V-9），意味着服务端要集中保管一批「等价于用户电脑 root」的凭据。

**去掉 Paseo，这些代价连同那个凭据模型一起消失**（§6）。真正要自己补的是**长连上的重连与会话续接**——而那半本来就在服务端的工作清单里。

**[`paseo-adapter`](../../paseo-adapter) 的价值转为**：① 它测出来的方法论（怎么抓静默失败）；② 万一自研通道走不通时的后手。**不删、不继续投入。**

---

## 2. 引擎：codex

### 2.0 我们只用它的一块：agent 循环

**Chat 什么都有了，就是没有 agent 能力。** codex 补的就是这一块，别的都是多余的。

| 我们要的（就这些） | 我们不要的 |
|---|---|
| 起会话 / 续跑 / 停止 | TUI 界面、cloud tasks |
| 发提问、拿流式回答与推理过程 | MCP 服务端与客户端扩展 |
| 工具调用：读代码、改文件、跑命令 | plugins / marketplace / skills |
| 沙箱地板 | review、memories、goals |
| 审批往返 | `fs/*` 文件服务、独立终端 `command/exec` |
| 会话事件流 | ChatGPT 登录流程、语音 realtime、模糊搜索…… |

**量化一下这个「只用一块」有多小**：协议面 `ClientRequest` 有 **87 个方法，我们只用 9 个左右**；`ServerRequest` 10 个里用 3 个（三类审批）；`ServerNotification` 68 个里用十几个。**九成的协议面我们根本不碰。**

这条边界怎么落地：

- **适配层只实现最小子集，其余方法根本不暴露**——这同时就是本机能力集的地板（§5.0）：宿主没实现的操作，前端与远程都调不到；
- 能用配置关的顺手关掉（app-server 的 analytics 默认就是关的；其余走 `-c` / `--disable`）；
- **但要认清一件事：不需要 ≠ 能删掉。** codex 是一个二进制，多余的能力关得掉、删不掉——它们仍然占体积。这就是 V-13 那笔账的来源，也是选 slim 的 `codex-app-server`（不含 TUI 与 cloud-tasks）而不是完整 `codex` 的理由（§2.4）。

### 2.1 接法：直接说 app-server 协议

三条路都读过源码，结论：

| | ① 官方 SDK（`codex exec`） | **② 直接说 app-server（选它）** | ③ 经 Paseo daemon |
|---|---|---|---|
| 拿到的 | 纯 agent 循环，官方封装 | 纯 agent 循环，协议级 | 完整 agent 产品 |
| **交互审批** | **没有**：事件只有 `thread/turn/item/error`，`approval_policy` 是单向配置，用户的「允许」没有回程 | **有**：`ExecCommandApprovalParams/Response`、`ApplyPatchApprovalParams/Response`、v2 `CommandExecutionRequestApprovalParams` | 有 |
| 协议稳定性 | `--experimental-json`（名字自己说了） | 正式的 `app-server-protocol` crate（v1/v2），还能**导出 TS 类型与 JSON schema**（`export.rs`） | 同 ② |
| 体积 | codex bin + npm 包 + node | **codex bin**（Rust 直连，**不需要 node**） | codex + node + 415 MB |
| 跟版本 | bump 官方包 | 跟 protocol crate | 三处联动 |

**已定选 ②**（2026-09-02）：同时满足「只要 agent 逻辑」和「要人工审批」，且是 Rust 直连——宿主本来就是 Rust。① 在功能上是 ② 的子集（没有审批回程），已排除。

**我们要的 crate 分两层**：`codex-app-server` 是协议门面（stdio JSON-RPC），`codex-core` 才是 agent 循环（`app-server/Cargo.toml:44` 依赖它）。带上 app-server 就带上了 core。

**观察项，不建**：上游有 `codex app-server daemon` + `remote_control`，README 自己写着「实验中、生命周期契约可能变」，而且**目前 Unix-only，不支持 Windows**。它的方向与我们的远程通道重叠，值得盯着，但现在不能建在上面（V-18）。

### 2.1b 我们自己的封装层（"共飞 codex 适配层"）

**核心逻辑归上游，封装归我们。** codex 二进制原样用、不改；我们写一层**薄的 Rust 适配层**把它的协议包起来，给 Tauri 宿主与前端一个稳定的面。**不带 node。**

| | |
|---|---|
| 位置 | 独立 Rust crate（建议 `tools/codex-adapter/`，被 `chat/src-tauri` 引用） |
| 职责 | **唯一说 codex 协议的地方**：JSON-RPC 编解码、会话生命周期、事件投影、审批往返、错误分类 |
| **不负责** | 进程的拉起与监管（宿主的事）、选目录、定沙箱策略、凭据从哪来——这些**由宿主按调用传进来** |
| 类型 | 优先用上游 `export.rs` 导出的 schema 生成，少手写、少漂移 |
| 测试 | 契约测试 + **对真进程的冒烟**（升级闸门那套，§2.3） |

两条从 [`paseo-adapter`](../../paseo-adapter) 学来的纪律（**学经验，不搬代码**）：

1. **单一缝**：只有这一个 crate 认识 codex 的协议类型。升一次版 = 改一个模块，不是审一遍全栈。
2. **失败域分开**：**适配层不负责 spawn**。宿主管进程、适配层管协议——适配层崩了不该顺手重启一个正跑着任务的 codex，codex 重启也不该要求换一个适配层实例。

**结论一句话**：`Chat（产品） + 共飞 codex 适配层（我们封装，薄）+ codex（上游维护，原样用）+ 自研手机互联（§6）`。

### 2.2 认证：每次调用传参，不碰 `~/.codex`

**产品上只有一件事：用户登录共飞账号并授权。** 之后 codex **直连中转站**的 OpenAI 兼容端点。后台是 key 机制，但那是客户端托管的实现细节，用户既不看见也不粘贴。

> ✅ **V-7' 已实测通过（2026-09-02，codex-cli 0.144.2，真实中转站 + 真实模型）**。做法与结论见下，探针脚本 `scratchpad/v7_v17_probe.py`。

**三件事一起成立**：

1. **私有 `CODEX_HOME`**：环境变量指到我们自己的目录，app-server 起来后 `initialize` 回的 `codexHome` 就是它。**用户的 `~/.codex` 全程没被读也没被写**。codex 把 `auth.json`、`sessions/`、几个 sqlite 都放进这个私有目录——**它们是我们的，不是用户的**。
2. **`-c` 覆盖生效**：`model_provider` / `model_providers.custom.base_url` / `wire_api` / `requires_openai_auth` / `model` 全部由命令行传入，线程回执里 `modelProvider: "custom"` 证明它认了。
3. **沙箱与审批策略按调用传**：`thread/start` 直接吃 `cwd` + `sandbox: "read-only"` + `approvalPolicy: "on-request"`，`turn/start` 还能再覆盖一次（`sandboxPolicy` / `approvalPolicy` / `cwd` / `model` / `effort`）。

> ⚠️ **一个必须记住的坑：`CODEX_API_KEY` 环境变量对 app-server 无效。**
> `app-server/src/lib.rs:510,759` 把 `enable_codex_api_key_env` **硬编码成 `false`**——那个开关只对「把 app-server 当库嵌进去」的用法有效。
> 第一次探针就是这么撞上 401（中转站回 `API_KEY_REQUIRED`）。
>
> **正确姿势是在协议里传**：`initialize` 之后调 `account/login/start`，参数 `{"type":"apiKey","apiKey":"<托管 key>"}`。实测一次就通，随后整轮真实对话正常。
> 这反而更好：凭据是**一次会话内的一条 RPC**，不是环境变量（不会被子进程继承、不会出现在进程列表里）。

**于是整类问题消失**：不碰用户的 `~/.codex`，就不会顶掉他自己的 ChatGPT 登录，也就不需要小白端那套「快照 → 替换 → 还回去」的机器，更不会出现「会话跑一半续租把凭据换掉」。

> 小白端为什么要那套：codex 是**按 `auth.json` 里有什么**选凭据的，OAuth `tokens` 对象优先于旁边的 key，只加不删会得到一个「看起来正常、却在扣用户 ChatGPT 套餐」的配置。**私有 `CODEX_HOME` + 协议内登录**绕开了整个雷区。

**小提醒**：把 `CODEX_HOME` 放在系统临时目录时，codex 会警告 `Refusing to create helper binaries under temporary dir`（不建 PATH 别名）。正式实现要放在应用数据目录，不是 temp。

**与 Chat 现有能力的关系**：分组 / 模型 / 推理强度的联动是**共飞侧已经做好的**（`/v1/paw/config` 下发目录，客户端级联与校验），agent 面直接复用。**codex 不认识「分组」**，它只要模型名、reasoning 和一把 key——选择结果翻译过去即可。切分组 = 换 key，新会话用新 key。

工作台仍然要拥有**托管 key 的租约生命周期**（登录授权 → 申请/复用 → 续租 → 换分组只发 `group_id` → 登出作废），只是它落在**内存与进程参数**里，不落在用户的配置文件里。

### 2.3 版本与打包：只跟一个上游

现在上游只剩 **codex** 一个（Apache-2.0，不 fork、不改源码）。

- **钉精确版本**；随包或首启下载都要有**版本清单 + 校验值 + 可回滚**；
- **升级闸门**：每次 bump 跑一遍端到端冒烟——起会话、事件到货、**审批弹出并且拒绝生效**、错误分类。协议是 JSON-RPC，类型检查抓不到语义漂移，只有真进程能抓；
- **升级与在跑会话要协调**：不能在一轮跑到一半换掉 codex；
- 更新通道（稳定 / 测试）与 Chat 自身的版本检查复用同一套。

### 2.4 用哪个二进制

workspace 里有两个相关 `[[bin]]`：完整的 `codex`，和独立的 **`codex-app-server`**。

**我们自己 spawn，所以可以直接用 slim 的 `codex-app-server`**——不需要任何 shim（shim 原本只是因为 Paseo 硬追加 `app-server` 位置参数才需要）。它比完整 `codex` 少掉 `codex-tui` 与 `codex-cloud-tasks`（那两个只有 `cli` crate 才拉），仍带着 `codex-login`、`codex-mcp`、`codex-exec-server`、`codex-execpolicy`、沙箱与模型 provider，共 **101 个 workspace 依赖**。

→ **V-13**：`codex` 与 `codex-app-server` 各自 release + `strip = true` 后多大。官方那个 **341 MB 的 `codex.exe` 是未 strip 的**（`[profile.release]` 明确写 `strip = false`，注释说打包时再 strip），别拿它当判决书。

### 2.5 体积（v2 的账）

| 项 | v1（经 Paseo） | **v2（直连 codex）** |
|---|---|---|
| `node.exe` | 93 MB | **0** |
| paseo `node_modules` | 415 MB | **0** |
| `PASEO_HOME` | 13 MB | **0** |
| codex 二进制 | 待测（V-13） | 待测（V-13） |
| Tauri app + 前端 | ~15 MB（估） | ~15 MB（估） |

**去掉 Paseo 直接省掉约 520 MB**，并且 v1 那个「node_modules 能不能裁掉 272 MB」的验证项（V-12）**整个作废**——那 250 MB 的 claude provider 本来就是 `@getpaseo/server` 的硬依赖，现在根本不装。

剩下唯一的体积未知数是 codex 二进制（V-13），它同时决定**随包发还是首启下载**。

### 2.6 macOS

codex 有 mac 版（arm64 / x64），自建也能出——「要 exe」本身不是障碍。真正的三件事：

1. **构建机**：小白端那条「windows-latest 交叉编译 + `rcodesign`」是 .NET 才有的待遇。**Rust 交叉编译 macOS 要 Apple SDK，Tauri 打 `.app`/`.dmg` 要 mac 工具链** → 必须加 macOS runner（私有仓库 10× 计费）。
2. **签名**：hardened runtime 下 `.app` 里每个 Mach-O 都要签。**v2 这里轻多了**——不再有 node、`node-pty` 原生模块、sherpa dylib，只剩我们的 app + codex 二进制。两条不变的事实：**Apple Silicon 上未签名的 arm64 可执行文件会被内核直接杀掉**（ad-hoc 签名是强制步骤）；**公证必须 Developer Program 账号**，且 TCC 授权绑签名标识，ad-hoc 重签可能让已授权失效。
3. **进程笼没有等价物**：Windows 靠 Job Object 保证「关掉 Chat = codex 全死」。macOS 要另做（进程组 + 父进程死亡监控 + 启动时孤儿清理），**不许用 NullProcessCage 静默降级**（V-14）。

**建议**：Windows 先行，mac 单独分期；但**买不买 Developer Program**、**CI 加 macOS runner 的成本**这两件现在就要定，否则返工。

---

## 3. 组件职责

| 组件 | 负责 | 明确不负责 |
|---|---|---|
| **Chat 前端（三形态）** | 会话 UI、发提问、看时间线、审批交互、选「操作哪台电脑」 | 不认识 codex 协议、不碰工作目录的真实路径 |
| **Tauri 宿主（Rust）** | codex 进程生命周期与进程笼、app-server JSON-RPC 客户端、**目录白名单与用户同意**、沙箱与审批策略地板、托管凭据注入、出站长连 | 不承载手机业务逻辑、不做第二套 agent 语义 |
| **codex app-server** | agent 循环、工具调用、沙箱、审批请求 | —— |
| **共飞服务端** | 鉴权、设备绑定、长连转发、在线态、审计 | 不直接跑 agent、不参与 codex 计费 |
| **PWA** | 对话 + 共联操作 PC（D-5） | 不做工作区管理、不做本地 agent |

`cwd` 永远由 PC 侧决定：服务端与手机只能引用 PC 登记过的**目录键**，不能传路径。

---

## 4. 决定

### D-1：普通对话与 agent 会话是两个面，共用一个 UI 壳

持久化模型不同（Paw 本地 state vs agent 会话在 PC 上权威、跨设备可见）。硬合成一套历史 = 两边重写。反悔成本：低。

### D-2：复用 Chat 现有鉴权与传输层，另开一条会话通道

**复用**：登录 / 刷新 / 401 重放、错误分类、SSE 解析、三形态部署。
**另开**：`/v1/paw/chat/completions` 没有设备亲和、没有长连、没有可恢复的会话标识。新增的是 `/v1/paw/devices/*` 与一条会话通道，**不是新增一个客户端**。

### D-3（v2 改写）：宿主用 Rust，直连 codex，**不带 Node**

v1 的「复用 paseo bridge」随 §1.3 作废。宿主负责 spawn `codex-app-server` 与进程笼；协议由**我们自己的薄适配层**（§2.1b）说，类型尽量从上游 schema 生成。**核心逻辑归上游，封装归我们，全程不带 node。**

**代价**：C# 那 119 项测试不跟过来，Rust 宿主是新代码，要重新建立覆盖。

### D-4：宿主放在 Chat 的 Tauri 壳里，笼绑 Chat 进程

**codex 的生命周期 = Chat 桌面版的生命周期。关掉 Chat，这台电脑手机就够不着。**

依据：进程笼是安全机制（强杀后未被笼住的子进程继续活着、通道继续开着，手机仍能操作一台主人以为已关闭的电脑）；`tauri.conf.json` 现在是 `dangerousUseHttpScheme: true` + `csp: null`，独立宿主走回环 HTTP 会把同意记录暴露给这个 webview 里的任何页面。

**代价**：桌面版更新会打断在跑的会话；本机 web / PWA 不能驱动 agent；macOS 上笼要另做（§2.6 ③）。

**翻盘条件**：要求 Chat 退出后手机仍能操作、要求本机 web/PWA 也能驱动 agent、要求更新不打断会话。

### D-5：手机端只有 PWA，能力就两件事

暂不发原生。① 简单对话；② 共联操作 PC（§5.1）。

**要验的**：完成通知走 Web Push，**iOS 必须先「添加到主屏幕」且 16.4+**（V-11）。这条不成立，「发完任务把手机揣兜里」这个核心场景就没了。

### D-6（v2 新增）：Paseo 退出运行时链路，留作参考

理由见 §1.3。`paseo-adapter` 不删、不继续投入。

**翻盘条件**：自研长连在 NAT / 企业网环境下反复失败，且我们不想自己解决——那时可以把 Paseo relay 单独捡回来**只当传输**（注意它的 `offer = owner` 模型要重新评估）。

---

## 5. 能力集

### 5.0 本机与远程是两个集合

| | 本机（Tauri 宿主直连 codex） | 远程（手机 → 服务端 → PC） |
|---|---|---|
| 谁定 | **产品说了算**，可以宽：多工作区、diff / 文件查看、更多 codex 选项 | **安全边界说了算**：只有 §5.1 那张表 |
| 加一条的代价 | 一个本机 UI + 一次本地调用 | 一条**所有人都能远程发出**的指令 |
| 目前状态 | 待定（§8） | 定死 |

v1 里「Paseo 栈里发不出那条消息」的论证不再适用，但**结论更强了**：远程链路上能做什么，完全由**我们自己写的宿主**决定——宿主不实现的操作，协议上就不存在。

### 5.1 远程能力集（定死）

```
devices.list                               # 我有哪些已绑定电脑、在线否
devices.select <deviceId>
agents.list / create / send / stop / archive
timeline.subscribe / unsubscribe <cursor>  # 断线后按游标续订，不重放整轮
approvals.list / respond                   # 手机上处理审批
notifications.subscribe                    # finished / error / permission
workdirs.list                              # 只回目录键与显示名，不回真实路径
```

**不在这张表里的，远程链路上就不存在**：终端、文件浏览、git、schedule、插件、语音。

### 5.2 人工审批（**已定要做**，2026-09-02）

走 app-server 的 `item/commandExecution/requestApproval` / `item/fileChange/requestApproval` / `item/permissions/requestApproval`。这不是可选项，它同时是 M0 的出口标准之一和 §5.1 远程能力集里的一条。

> ✅ **V-17 已实测通过（2026-09-02，真实模型跑了一整轮）**：
> `sandbox=read-only` + `approvalPolicy=on-request` 下，让 agent 写一个文件，收到
> `item/commandExecution/requestApproval`（带 `reason`、完整 `command`、`threadId`/`turnId`/`itemId`），
> 回 `{"decision":"decline"}` 后——**命令没有执行、文件没有生成**，codex 侧日志 `exec command rejected by user`，
> 模型自己收尾说「The command was not run because permission was denied.」，`turn/completed` 正常收束。
> **拒绝真的生效，而且不是把整轮打断，是让 agent 知道被拒了继续往下走。**

实测顺带捡到两个对 UI 很有用的东西：

- **`thread/status/changed` 会带 `activeFlags: ["waitingOnApproval"]`** —— 「正在等你点头」这个状态不用我们自己推断；
- **`serverRequest/resolved` 通知** —— 一个审批被响应后服务端会广播，**这正好是 §5.2「一处响应、另一处消失」的现成原语**，PC 与手机的队列一致不需要我们发明机制。

要一起定下来的四件事，**别等到实现时才想**：

| | |
|---|---|
| **策略默认值** | codex 的 `approval_policy` 有 `never` / `on-request` / `on-failure` / `untrusted`。默认取哪个、用户能不能改、能不能按工作区分别设——**待定（§8）**。这决定了用户是「每步都被问」还是「几乎从不被问」 |
| **超时与无人应答** | 一个审批悬着的时候那轮会话是停住的。PC 锁屏、手机没人看、Chat 被关掉——**必须有超时**，且超时的结果要明确（默认拒绝，并把这一轮标成「因未审批而中止」，不是静默失败） |
| **两端一致** | PC 与手机看到的是**同一个待审批队列**；一处响应，另一处立刻消失。这也意味着审批状态是 PC 侧权威，服务端只转发 |
| **防重放** | 「在手机上点允许」= 在用户电脑上执行一条命令。请求与响应必须绑定 `会话 + 请求 id + 一次性 nonce`，过期与已响应的一律拒绝。这是远程链路上**最该被攻击的一个点** |

沙箱地板与审批是**两层**，不是二选一：沙箱决定「根本做不到的事」，审批决定「能做但要人点头的事」。审批做了不等于沙箱可以松。

---

## 6. 服务端新增工作

### 6.1 自研长连替代 relay

PC 上的 Chat 桌面版**出站长连**到共飞服务端（WSS），服务端按设备转发给 PWA。不需要 NAT 穿透（出站连接本来就穿透），不需要额外部署——**Elixir relay 那份运维负担与三处版本联动一起消失**。

| 要做的 | 要求 |
|---|---|
| 绑定表 | `用户 ↔ 设备(deviceId, 机器名, 设备令牌, 最后在线时间)` |
| **设备令牌** | **我们自己的凭据：按设备签发、最小权限、可单独撤销、可轮换**。v1 那个「一份 offer 泄露 = 一台电脑的 root 会话」的模型**不存在了**——这是去掉 Paseo 最大的安全收益 |
| 事件游标 | 每个会话的事件有单调游标，重连时问「给我 N 之后的」。**不能沿用 `pawRequest` 的 401 重放**：请求可重放，跑了一半的会话流不行，重放 = 时间线重复 |
| 在线态与心跳 | PC 离线时 PWA 显示「这台电脑不在线」，而不是超时报错 |
| 跨用户隔离 | 一个用户的连接绝不被另一个用户的请求复用；`deviceId` 必须校验归属 |
| 并发 | 同一台 PC 可能被 PWA 与本机同时操作，UI 要能表达「正在忙」 |
| 审计 | 每次远程连接与每个远程操作记 `用户 / 设备 / 时间 / 来源` |
| 计费 | 仍在中转站（codex 直连过去），服务端转发层不参与计费 |

### 6.2 仍然成立的安全事实

**零知识不成立**：会话内容经共飞服务端转发，技术上可读。Paseo 官方那句「代码不离开你的机器」**不能照抄进宣传**。落盘策略（是否保存会话内容、留多久）必须在写存储层之前定 → **Q-1**。

---

## 7. 分期

### M0 —— 本机 agent 闭环（无服务端依赖，先做）

链路：**Tauri 壳（Rust）↔ `codex-app-server`（stdio JSON-RPC）**。

- 扩 Tauri 壳：进程启动、文件系统、托盘；**进程笼绑到 Chat 进程本身**；
- Rust 侧 app-server 客户端：会话生命周期、事件流、**审批往返**、错误分类；
- 凭据与沙箱按调用传参（§2.2），不碰 `~/.codex`；
- 前端新增 agent 会话面，走 Tauri IPC；
- 目录键 → 真实路径的映射由宿主持有，**契约里够不到**。

**出口**（happy path 不够）：

1. 干净机器上装完 Chat 桌面版能跑完一轮真实 codex 任务；
2. **一个应当被沙箱地板挡住的任务确实被挡住**；
3. **一次审批真的弹到用户面前，拒绝生效**；
4. 强杀 Chat 后**没有残留的 codex 进程**。

**携带验证**：V-7'、V-13、V-16、V-17。

### M1 —— 工作台化（与 M0 可并行）

**M1a 账号与个人中心**（不碰 agent 链路，后端接口全现成）：注册 + 验证码 + Turnstile、2FA、个人信息、用量与额度、分组与倍率、API key 租约与换组、订阅与充值、公告。§1.2 那三个坑当验收用例。

**M1b 本机工作台**：目录白名单与用户同意（爆炸半径告知）、会话本机持久化与恢复、崩溃分类与降级、托管凭据的租约生命周期（§2.2）。

**出口**：不写代码的用户能自己注册、看懂额度、跑一轮 agent 任务，出错时看得懂。

### M2 —— 服务端骨架（不早于 M0）

设备登记与绑定表、设备令牌签发与撤销、长连转发与事件游标、在线态、跨用户隔离。

**出口**（「一个用户一个会话能连上」不算通过）：多用户 × 每人多个并发会话下转发正确，断线按游标接回不重复，跨用户隔离有测试。

### M3 —— PWA 共联

设备列表 / 选设备、手机发起会话、增量渲染时间线（复用 SSE 分帧）、**手机处理审批**、完成通知（先过 V-11）、断线重连。
**agent 会话历史从服务端 / PC 取，不落 localStorage。**

### M4 —— 收口

审计视图、一键撤销设备、离线排队策略（Q-3 定了才做）、版本工程收口（清单 + 校验 + 回滚 + 升级闸门）。**版本工程 M0 就要有雏形**，否则第一次跟上游升版就是一次考古。

---

## 8. 待验证与待决

| 编号 | 内容 | 承接 | 类型 |
|---|---|---|---|
| ~~V-7'~~ | ✅ **2026-09-02 通过**：私有 `CODEX_HOME` + `-c` 覆盖 + 按调用传沙箱/审批全部成立；**但 `CODEX_API_KEY` 环境变量对 app-server 无效，凭据要走 `account/login/start`**（§2.2） | 已完成 | 技术验证 |
| **V-13** | `codex` 与 `codex-app-server` 各自 release + strip 后的体积；决定随包还是首启下载 | M0 | 技术验证 |
| **V-16** | 🟡 **一半已答**：`codex app-server generate-ts` / `generate-json-schema` **直接从二进制导出**类型与 schema（实测 39 个 JSON schema + 全套 TS）。面已知：ClientRequest 87 个方法、ServerRequest 10 个、ServerNotification 68 个。**剩下的是挑我们要用的最小子集** | M0 | 技术验证 |
| ~~V-17~~ | ✅ **2026-09-02 通过**：审批请求到货、`decline` 后命令确实没执行、文件没生成、turn 正常收束（§5.2） | 已完成 | 技术验证 |
| **V-11** | PWA 完成通知：Web Push 在 iOS（须添加到主屏、16.4+）与 Android 上是否可用 | M3 之前，越早越好 | 技术验证 |
| **V-14** | macOS 进程笼替代方案；**不许用 NullProcessCage 降级发布** | mac 分期前 | 技术验证 |
| **V-18** | 盯住上游 `codex app-server daemon` + `remote_control`（实验、Unix-only），它与我们的远程通道重叠 | 持续 | 观察 |
| **Q-1** | 零知识作废怎么对用户表述？服务端是否落盘会话内容、留多久——**M2 的存储层等这个答案** | M2 之前 | 产品决策 |
| **Q-3** | PC 离线时 PWA 的表现：纯提示 vs 排队等上线 | M3 / M4 | 产品决策 |
| — | **审批策略默认值**（`never`/`on-request`/`on-failure`/`untrusted`）、用户可否修改、可否按工作区分设（§5.2） | M0 出口之前 | 产品决策 |
| — | **审批超时时长**与超时后的表现（默认拒绝 + 本轮标为「因未审批而中止」） | M0 | 产品决策 |
| — | 本机能力集边界（§5.0）：终端 / 文件与 diff 查看 / 多工作区 | M1b 之前 | 产品决策 |
| — | **Tauri 1 → 2**（sidecar 与权限模型 2 更顺手，越晚升越贵） | M0 开工前 | 技术决策 |
| — | 买不买 Apple Developer Program；CI 加 macOS runner 的成本 | mac 分期立项 | 产品 / 工程决策 |
| — | 一个用户是否支持多台已绑定 PC | M3 | 产品决策 |

**已定**（不再讨论）：桌面形态＝Chat 的 Tauri 壳；宿主位置＝壳内、笼绑 Chat 进程（D-4）；宿主语言＝Rust；手机端＝只发 PWA（D-5）；**引擎接法＝app-server 协议，封装层我们自己写、不带 node（§2.1 / §2.1b）**；**人工审批要做（§5.2）**；**Paseo 退出运行时链路（D-6）**。

**随 v2 作废的验证项**：V-9（offer = owner）、V-10a / V-10b（relay 链路与部署）、V-12（node_modules 能否裁）、V-15（会话中途换凭据）、V-2 / V-8（codex 由谁拉起、PATH 问题——现在路径由我们自己钉）。

---

## 9. 明确不做

- **不开发 codex**：不 fork、不改源码、不加功能、不自己实现 agent 内核；只写适配器 + 跟版本 + 按版本打包；
- **不做注入**：不注入官方 Codex 客户端、不做 CDP 增强；
- **不把 Paseo 拉回运行时链路**（除非 D-6 的翻盘条件成立）；
- 不做手机端终端 / 文件浏览 / 任意命令执行（§5.1：协议上不存在）；
- 不把本地工作区、绝对路径、完整日志上传服务端；
- 不做团队 / 多人共享——单用户跑顺了再说。
