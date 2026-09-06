# 共飞 AI 工作台 —— 总体规划

> 状态：规划草案 v1（取代 `共飞AI工作台-需求分析与架构边界.md`，那份作废）
> 日期：2026-09-02
> 拓扑与职责以 [`共飞 Paw 远程操作整体架构.md`](../../codex-relay-client/doc/共飞%20Paw%20远程操作整体架构.md) 为准，本篇不另立一套。

---

## 0. 要做的是什么

**在用户自己的 PC 上，把 codex 变成一个能被对话驱动、能自己跑完一轮、并且能从手机接管继续的 agent。**

### 主体是 Chat

**工作台的主体就是 [`tools/chat`](../../chat)：Next.js 前端 + Tauri 桌面壳 + PWA，三形态同一份代码。**
桌面版是这个壳，**agent 宿主就装在这个壳里**（§4 D-4）；手机版是同一份 PWA，**暂不发原生 App**，能力只有简单对话 + 共联操作 PC（§4 D-5）。

**它和小白端是两个产品，不是同一条线的两个版本。**

| | 小白端 | 工作台 |
|---|---|---|
| 定位 | 刻意做窄：只为"能用上 codex" | **完整**：账号、对话、agent、远程一整套 |
| 技术栈 | WPF / .NET | **Chat：Next.js + Tauri** |
| 与本项目的关系 | **参考实现**（§1.2） | 主体 |

工作台**不继承小白端的任何代码**——包括宿主那部分（§4 D-3）。共享的只有契约和踩过的坑。

整合对象：

- **共飞服务端**（`backend/`，Go）：账号、鉴权、模型路由、计费；已有 Paw 对话网关；
- **Chat**（`tools/chat/`）：主体，已有与服务端的完整通讯层（§1.1）；
- **Paseo 适配层**（`tools/paseo-adapter/`）：写好了、测过了、还没有消费者。

不做通用 Agent 平台，不做可插拔 Runtime 市场。**codex 是唯一运行时，Paseo 是唯一远程通道**，这两条是前提不是选项。

并且：**codex 与 Paseo 都是上游开源项目，我们只写适配器、跟随版本、按版本打包**，不开发它们本身（§2.2）。

---

## 1. 现状盘点：一块能直接用、一块能跟着走、两块要重写

| 组件 | 位置 | 到了什么程度 | 缺什么 |
|---|---|---|---|
| 小白客户端 | [`tools/codex-relay-client/src`](../../codex-relay-client/src) | **参考实现，不是演进基座**（见 §1.2）。WPF + 零 NuGet 的 `Server` HTTP 客户端 + `CodexBinding`（`~/.codex` 写入、加密快照、恢复）；246 项测试全绿，已对真实中转站跑通 | 对工作台而言它**一行都不直接继承**——要继承的是它踩过的坑和它证明过的契约 |
| Paseo 适配层 | [`tools/paseo-adapter`](../../paseo-adapter) | 窄契约会话面完整（握手/健康/目录/会话生命周期/时间线/通知/relay 三操作）；C# 82 + bridge 13 项测试；2026-09-01 用**真实 codex 会话**验过时间线与通知；`PaseoRuntime.StartAsync` 一行拉起私有 daemon + bridge + 进程笼。**它同时是我们关于 Paseo 的知识库**（§2.1） | **没有任何消费者**。写完了，挂在那里 |
| Chat / Paw | [`tools/chat`](../../chat) | **与服务端的通讯层已经打通**（见 §1.1）：登录/刷新/401 重放、信封解包、错误分类、配置与模型目录、SSE 流式解析、附件与图片；桌面 Tauri + Web + PWA 三形态同一份代码 | 缺的是**会话语义**，不是通讯：不认识"设备"，没有长连通道，会话历史只在 localStorage、换台设备就没了 |
| **Tauri 壳** | [`tools/chat/src-tauri`](../../chat/src-tauri) | Tauri 1.5，`main.rs` **只有 7 行**——一个纯 webview 包装；allowlist 里只开了 http 与窗口控制 | 要当 agent 宿主，缺的东西是实打实的：进程启动（sidecar / shell.execute）、文件系统、托盘、进程监管。**这是 M0 的主战场** |
| 共飞服务端 | [`backend/internal/server/routes/paw.go`](../../../backend/internal/server/routes/paw.go) | `/v1/paw` 下只有 `config`、`config/defaults`、`files`、`images/*`、`chat/completions` —— 一个纯对话网关 | **没有设备、没有绑定、没有 agent 转发、没有 Paseo 客户端**。这是最大的一块新代码 |

### 1.1 Chat 已经有的服务端通讯层（agent 面**复用**，不重写）

这一层是现成资产，M3 直接站在上面：

| 已有 | 位置 | 复用价值 |
|---|---|---|
| JWT 登录 / 刷新 / 401 自动重放 / 刷新锁 | [`api.ts`](../../chat/src/client/paw/api.ts) `pawRequest` `refreshPawSession` | agent 面直接挂上去，不再写一遍鉴权 |
| 会话持久化与过期标记 | [`auth.ts`](../../chat/src/client/paw/auth.ts) | 同上 |
| 服务端错误 → 人话（含 HTML 响应识别、`code: message` 拼装） | `parsePawFailure` | 远程链路错误分类（PC 离线 / 设备未绑定 / 会话忙）挂同一套 |
| SSE 分帧与增量累积 | [`sse.ts`](../../chat/src/client/paw/sse.ts)、`sendPawChat` | 时间线增量渲染是同一个形状的问题 |
| 配置与模型目录、默认值存服务端 | `/v1/paw/config`、`/config/defaults` | agent 面选模型直接用 |
| 服务地址解析（三形态） | [`config.ts`](../../chat/src/client/paw/config.ts)、[`build.ts`](../../chat/src/config/build.ts) | 见下 |

**部署形态也已经定型**，M3 不需要新客户端：

- **内嵌**：Go 二进制带 `embed` tag 时把 `paw_dist` 挂在 `/paw`（[`paw_static.go`](../../../backend/internal/web/paw_static.go)）；
- **桌面**：Tauri，构建期钉死 `PAW_SERVICE_URL`，界面内不可改；
- **手机**：同一份 PWA（`manifest.webmanifest` + `service-worker.js`）。

**唯一的真空白是会话模型**：`usePawClient` 把会话存在 localStorage（`paw-conversations:v2`），服务端每次调用无状态。agent 会话必须反过来——状态在 daemon 时间线里，换设备也能接上。

### 1.2 小白客户端：参考什么，不搬什么

**定位：参考实现。** 工作台自己重做账号面，不是把 WPF 那套搬过来接着改。

**必须自己有的（后端接口都已存在，所以这是纯客户端工作）**，对齐 [`IRelayServerClient`](../../codex-relay-client/src/LanAi.RelayClient.Server/IRelayServerClient.cs) 这张面：

| 能力 | 后端 | Chat 现状 |
|---|---|---|
| 公开设置（决定注册入口是否显示、是否要 Turnstile） | `/api/v1/settings/public` | ❌ |
| **注册** + 邮箱验证码 | `POST /api/v1/auth/register`、验证码下发 | ❌ 只有登录页 |
| 登录 / **2FA** / 刷新 | `/auth/login`、`/auth/login/2fa`、`/auth/refresh` | ⚠️ 登录与刷新有，2FA 没有 |
| **个人信息** | `GET /api/v1/user/profile` | ❌ |
| 用量与额度（dashboard、用量趋势、模型用量） | 已有 | ❌ |
| 分组、专属倍率、高峰时段标签 | 已有 | ⚠️ 只取了分组与模型目录 |
| API key（列表 / 创建 / 续期 / 换组） | 已有 | ❌ |
| 订阅汇总、充值下单与订单校验 | `/payment/*` | ❌ |
| 公告 | 已有 | ❌ |

**明确不搬**：`RelayInjectionHost` / `CodexStartup` / `CodexHosts` / `MacCodexAppLauncher` 这条**注入官方 Codex 客户端**的路线。工作台不注入任何别人的界面——它通过 paseo daemon 起自己的 codex 会话。
（**注意区分**：`~/.codex/config.toml` 的**路由写入**不是"注入"，那条必须有，daemon 起的 codex 就是靠它走中转站计费。`CodexBinding` 的快照/恢复语义值得照抄。）

**值得照抄的三个坑**（都是它用测试钉死过的，重做时不重踩）：

1. **换分组只发 `group_id`**：`PUT /keys/:id` 的 `expires_at` 是三态语义，带默认值的请求体会把"1 天租约"变成"永不过期"，用户完全无感；
2. **401 分两义**：登录端点上是"密码错"，其他端点上是"会话过期"——合并会让自然过期的用户看到"密码不正确"；
3. **高峰倍率只对订阅类分组生效，且不在客户端算"当前是否高峰"**（窗口按服务器时区判定，客户端本机时区算会和实际扣费对不上）；令牌响应缺 `access_token` 必须当场失败，别绑成空对象。

三句话概括：

1. **PC 端是重写，不是接线**：能原样带走的只有 [`bridge`](../../paseo-adapter/bridge)；C# 的窄契约客户端与 Host 都不跟着来（D-3），Tauri 壳今天只有 7 行。**那 82 项 C# 测试不会跟过来**，Rust 侧要重新建覆盖；
2. **手机端缺的不是通讯，是通道语义**——鉴权、流式、错误分类、三形态部署都在了，缺一条长连通道和一套设备/会话概念；
3. **服务端缺的是一整块新代码**——不只是量大，而是**唯一没有现成东西可倚**的一段：PC 有适配层、客户端有传输层，服务端既没有 Paseo 客户端也没有设备概念。

排期因此不该按"层"排，该按"谁解锁谁"排（见 §7）。

---

## 2. 目标形态

四跳，每跳的协议和实现方都定死：

| 跳 | 两端 | 协议 | 谁实现 | 现在有没有 |
|---|---|---|---|---|
| ① | Paw ↔ 共飞服务端 | **共飞自有**（共联模式，JWT 会话） | 我们 | ❌ 全新 |
| ② | 共飞业务 ↔ Paseo 客户端适配层 | 进程内 / 本机 IPC | 我们 | ❌ 全新（Node，复用 bridge） |
| ③ | 适配层 ↔ relay ↔ daemon | Paseo 官方协议 + E2EE | Paseo，我们只当客户端 | ✅ 2026-09-01 V-10a 实测通过 |
| ④ | PC daemon → relay | Paseo 出站长连 | Paseo | ✅ 同上 |

三条不能记错的事实：

- **Paw 不说 Paseo 协议。** 全链路唯一的 Paseo 客户端是**服务端**那个 Node 适配层。
- **PC 只出站。** daemon 仅监听 `127.0.0.1`，主动连出到 relay，不开入站端口、不做端口映射。
- **paseo daemon 是运行时宿主，不是传输层。** 是它 spawn codex app-server、持有会话生命周期、时间线与通知。所以"以后换掉 Paseo"= 换掉运行时宿主，不是换根网线 —— 别在设计里假装它可插拔。

`cwd` 永远由 PC 侧决定：服务端只能引用 PC 登记过的**目录键**，不能传路径。

---

### 2.1 Paseo 的真实形状（`paseo-adapter` 已经替我们试出来的）

规划里凡是涉及 Paseo 的地方，都以这一节为准。**这些不是读文档读来的，是 [`paseo-adapter`](../../paseo-adapter) 用真实 daemon 和真实 codex 会话测出来的**，写进规划是为了让服务端那一版不再踩一遍。

**daemon 是什么**：一个会 spawn codex app-server 的常驻进程，持有会话、时间线、通知。我们把它裁成"只有 codex"的形态靠 [`DaemonConfigComposer`](../../paseo-adapter/src/LanAi.Paseo.Adapter.Host/DaemonConfigComposer.cs) 每次启动**重写** `config.json`（**不合并**——daemon 自己会改这个文件，一个新 home 回来自带 `listen 127.0.0.1:6767`、`cors: app.paseo.sh` 和没人要的 `app.baseUrl`；合并的话一句 `"claude": {"enabled": true}` 就能悄悄把一个 provider 打开）。关掉的东西：claude / copilot / opencode / pi / omp（顺带省 ~239 MB）、MCP、CORS 允许来源清空、`listen` 只回环、relay 默认关。

**四个会静默失败的地方**（每一个都值一次线上事故）：

| 坑 | 症状 | 正确做法 |
|---|---|---|
| **facade 的 `timeline.subscribe` 是个假订阅** | 连上、报成功、**一条都不来**。因为现在的 daemon 都开了 `selectiveAgentTimeline`，只发给显式订阅过的会话，而 facade 没有提供发起订阅的方法 | 用底层 `DaemonClient` driver 的 `setAgentTimelineSubscription` —— bridge 已经这么做了 |
| **attention（完成/出错/待审批）需要先开 agent-updates 订阅** | 时间线全都在，**通知一条没有**（实测过一整轮） | 拿 `fetchAgents({subscribe})` 当副作用开订阅，没有专门的 RPC |
| **每条连接的时间线订阅上限约 5 个 agent** | 超了以后最老的被悄悄淘汰，UI 还开着一个不再推送的窗口 | 订阅接口回**整个订阅集**，调用方拿它跟自己以为在看的对比 |
| **`shouldNotify` 实测恒为 `false`** | 拿它当开关就永远不弹通知 | 当建议值用；daemon 是按心跳在场状态挑接收者的，适配器目前不上报心跳 |

**几个直接影响服务端设计的事实**：

- **daemon 不记录配对关系，没有"已配对设备列表"**。持有 offer 就是全部授权（呼应 §6 的 V-9）。
- **关掉 relay 不会作废已经发出去的 offer**（`offerRevoked` 永远是 `false`）。作废只有换 `PASEO_HOME` 这一条路。
- **时间线事件带 `seq`**（可选字段）——§5/§6 要的游标，原语在 Paseo 侧是有的。
- **批量投递**：100ms 窗口聚合，超过上限丢弃并报 `dropped` 计数；消费者收到 `dropped>0` 应当回查权威列表，而不是假装流是完整的。
- **目录键 → 路径的表由启动 bridge 的那个进程用环境变量交进来**（`COFLY_WORKDIRS`），契约里够不到。所以**消费者无法自行扩大爆炸半径**，最坏也只能报一个已经存在的键。服务端那份适配层同样受这条约束。
- **driver 的自动重连是故意关掉的**：开着的话，连一个死掉的 daemon 会一直重试，消费者看到的是"请求超时"（`TRANSPORT_DOWN`）而不是真相（`DAEMON_DOWN`）。

**结论：服务端那一版适配层直接复用 [`bridge`](../../paseo-adapter/bridge)，不要重新拿 `@getpaseo/client` 写一遍。** 上面四个静默失败里，至少前两个新写一遍必然重踩，而且症状都是"看起来连上了，就是什么都没有"。

### 2.2 依赖与版本策略：**只做适配器，跟着上游走**

一条贯穿全文的硬约束：**我们不开发 codex（agent 本体），也不开发 Paseo。** 这两个都是**按版本引入的上游开源项目**，我们只写适配器、跟随它们的最新版本，并把它们**按版本打包进自己的安装包**。

**三个上游件，版本要一起管**：

| 上游件 | 性质 | 在哪 | 现在钉在哪 |
|---|---|---|---|
| codex（app-server） | 开源项目，**不改源码** | 用户机器上 / 随包 | 待定（与 V-8 相关） |
| paseo daemon（含私有 node、`PASEO_HOME`） | 开源项目，**不 fork** | 随 Chat 桌面版分发 | v0.7.0-beta.3 |
| `@getpaseo/client` | 上游 SDK | [`bridge/package.json`](../../paseo-adapter/bridge/package.json)，**精确版本，不用 `^`** | `0.7.0-beta.3` |

**升级 Paseo 是三处联动**：PC 上的 daemon、服务端适配层用的 `@getpaseo/client`、relay 的协议版本（`CURRENT_RELAY_PROTOCOL_VERSION`）必须同一大版本。写进发布清单，不是升级时临场想。

**因为不 fork，跟住上游只能靠三样东西**：

1. **单一缝**：[`bridge/src/daemon.ts`](../../paseo-adapter/bridge/src/daemon.ts) 是唯一 import Paseo 类型的地方。升一次版 = type-check 一个模块，不是审一遍全栈。
2. **契约一致性测试（升级闸门）**：§2.1 那四个坑全是「连上了、报成功、什么都不来」型的**静默失败**——类型检查抓不到，只有跑真 daemon 的冒烟能抓到。**每次 bump 上游版本必须过这套**：真实 daemon 起会话 → 时间线到货 → attention 到货 → 订阅淘汰可见 → 错误分类（口令错 / daemon 不在 / codex 缺失 / 目录键非法）。现有 [`tests/smoke`](../../paseo-adapter/tests/smoke) 是它的起点。
3. **投影分两档**（和 §5.0 一样按链路分，否则「跟随最新版本」与「窄契约」会互相打架）：
   - **远程链路：投影，不透传。** 未知事件落 `other`，是 **no-op 而不是泄漏**——远程上「自动冒出来的新能力」就是「没人审过的新攻击面」。
   - **本机链路：投影 + 原样带上 `raw`。** `TimelineEvent` 本来就有 `raw` 字段，桌面端据此可以显示上游的新事件类型，**上游加个东西不需要改契约、不需要动三处代码**。这才是「用它们最新的功能」在实现上的落点。

**打包与更新**（M0 就要定形，见 §8）：

- 装机包按**版本清单**装：node runtime + daemon + bridge（+ codex，若随包）；
- 每件都有版本号与校验值，**能回滚**；
- 升级与「正在跑的会话」要协调——不能在一轮 codex 跑到一半时把 daemon 换掉；
- 更新通道（稳定 / 测试）与 Chat 自身的版本检查复用同一套。

**明确不做**：不 fork Paseo、不改 codex 源码、不给它们加功能、不自己实现 agent 内核。上游缺的东西（例如 §2.1 里恒为 `false` 的 `shouldNotify`，要适配器上报心跳才准）——**要么等上游支持，要么不做**，不在我们这边造一个平行实现。

### 2.3 语言环境与包体（实测数字）

**先纠一个前提：codex 不是 Python，是 Rust。** [`codex-rs`](file:///C:/Work/Git/codex/codex-main/codex-rs) 是个 Cargo workspace（`app-server` / `app-server-daemon` 都在里面），发出来是原生可执行文件。仓库里那个 `ruff.toml` 是它自己仓库工具链用的，**Python 不进我们的包**。

**整套要发的语言环境**：

| 件 | 语言 / 运行时 | 形态 |
|---|---|---|
| Chat 前端 | TypeScript / Next.js 静态导出 | 随包，几 MB |
| Tauri 壳 + 宿主 | **Rust**，编译进 app | ~10–15 MB（估算；WebView2 用系统的） |
| bridge | TypeScript，跑在私有 node | < 1 MB |
| paseo daemon | **Node** | node.exe + node_modules，见下 |
| codex | **Rust**，原生 exe | 见下 |

**本机实测（2026-09-01 那套 V-10 环境，Windows x64）**：

| 项 | 大小 |
|---|---|
| `node.exe` | **93 MB** |
| paseo `node_modules` | **415 MB** / 13 624 文件 |
| ├ `@anthropic-ai`（claude provider） | **250 MB** |
| ├ `@getpaseo/*` | 47 MB |
| ├ `node-pty`（终端） | 27 MB |
| ├ `sherpa-onnx-win-x64`（语音） | 22 MB |
| └ 其余（openai 12 / esbuild 10 / …） | ~59 MB |
| `PASEO_HOME` | 13 MB |
| **`codex.exe`** | **341 MB** |

**全随包 ≈ 850 MB+。** 不可接受，所以有两条降体积路径，其中一条是**必须先验证的**：

1. **codex 不随包，首启按版本清单下载 + 校验**（−341 MB）。代价：首启要网络、要处理下载失败与续传。顺带好处：路径由我们自己管，`agents.providers.codex.command` 钉绝对路径这件事（V-8）反而更干净。
2. **打包时裁剪 `node_modules`**（−250 MB claude，−22 MB 语音）。
   ⚠️ **这条不能想当然**：`@anthropic-ai/claude-agent-sdk` 与 `sherpa-onnx-node` 是 [`@getpaseo/server`](file:///C:/Work/pv1/runtime/node_modules/%40getpaseo/server/package.json) 的**硬依赖，不是 optionalDependencies**。配置里 `enabled:false` 只是**运行时不启用，文件照样要装**。真要省这 272 MB，只能装完再删，然后**实测 daemon 还起不起得来**（server 若在顶层 `import` 了它就直接崩）。→ **V-12**。
3. `node.exe` 那 93 MB 基本省不掉——daemon 就是 Node 程序。

**两种结局差 5 倍，所以 V-12 要早做**：

| | 安装包 | 首启后占盘 |
|---|---|---|
| 乐观（codex 下载 + 裁剪成立） | **~150 MB** | ~490 MB |
| 悲观（都不成立） | **~850 MB** | ~850 MB |

### 2.4 codex：必须是 exe，但不必是官方那个 341 MB 的

**为什么必须是可执行文件**：只要运行时宿主还是 paseo daemon，接法就是**进程 + stdio**——
daemon 用 `spawn(command, [...args, "app-server"])` 起进程走 JSON-RPC
（`codex-app-server-agent.js:5464`），还会先跑一次 `--version` 做能力门控
（`codexVersionAtLeast`，比如 auto-review）。**没有 in-process 的接法**，除非连 Paseo 一起换掉。

**但"官方 341 MB"不是唯一选项。** 源码是 **Apache-2.0**，自己构建并随包分发是允许的（保留 LICENSE/NOTICE）。而且官方 release profile 写着 `strip = false`、`debug = "line-tables-only"`，注释是"打包时再 strip"——**我们量到的 341 MB 是没 strip 的**。

三条路，按"是否仍属于适配器工作"排序：

| | 做法 | 只带核心？ | 代价 |
|---|---|---|---|
| **A** | 自建官方 `codex` bin，开 `strip = true` | 否（含 TUI、cloud-tasks、mcp-server、login…） | 最省事；入口与版本号天然对得上 Paseo 的门控 |
| **B** | 自建 **`codex-app-server`** slim bin（workspace 里本来就有这个 `[[bin]]`）+ 我们自己的**几十行 shim** | **是** | 需要 shim：Paseo 硬追加 `app-server` 位置参数，而 slim bin 是 flat clap parser，会当成非法参数拒掉。shim 只做两件事：吞掉那个参数、转发 `--version` |
| **C** | 把 `codex_app_server` 当 **lib 链进**我们自己的 Rust 宿主 | 是，最彻底 | **要 vendor 整个 workspace**，还得跟着它的 `[patch.crates-io]`（crossterm / tungstenite 指向 openai-oss-forks 的 git 版）。每次上游发版重新编译 + 处理 API 漂移，我们就从"跟版本"变成"维护一个下游分支"——**和 §2.2 直接冲突** |

**倾向 B**：它是"我只要 agent 核心"的最小落地方式，shim 是我们自己的几十行代码（适配器，不是改 codex），并且不 vendor、不跟 patch。
**A 是保底**：如果 strip 之后体积本来就能接受，就没必要为 shim 花这份心思。
**C 只在 A/B 体积都下不去时再考虑**，且要明确认下"我们成了 codex 的下游维护者"这个代价。

**「我们要的 agent」到底是哪个 crate**（分两层，别混）：

- **`codex-app-server`＝协议门面**：stdio 上的 JSON-RPC，**Paseo 驱动的就是它**——所以它是我们的接入点；
- **`codex-core`＝真正的 agent 循环**：`app-server/Cargo.toml:44` 依赖它。**带上 app-server 就带上了 core**，这就是方案 B 要构建的东西。

它**少了**完整 `codex` 里的 `codex-tui`（终端界面）与 `codex-cloud-tasks`——那两个只有 `cli` crate 才拉。
它**仍然带着** `codex-login`、`codex-mcp`、`codex-exec-server`、`codex-execpolicy`、沙箱、模型 provider…… 一共 **101 个 workspace 依赖**。所以它不是个小 bin，只是比 cli 小；**小多少必须量**。

顺带澄清一条**容易写错的事**：app-server 里带着 `codex-login`（ChatGPT OAuth），但**我们不用它**——认证走的是 §2.6 那条「登录授权 + 托管 key」，不是让用户去 ChatGPT 登录，也不是让用户自己填 API key。

→ **V-13**：分别构建 `codex` 与 `codex-app-server`（release + `strip = true`），量出两个体积。**这个数字决定选 A 还是 B**，也决定 codex 到底随包发还是首启下载（§2.3）。

### 2.5 macOS：exe 不是问题，签名链和进程笼才是

codex 是 Rust，官方本来就发 macOS 版（arm64 / x64），我们自建也能在 mac 上构建——**"要 exe"这件事本身不构成 mac 障碍**。真正的三件事是：

**① 构建机：小白端那条路在这里走不通。**
现有 [`client-release.yml`](../../../.github/workflows/client-release.yml) 是在 **windows-latest 上交叉编译出 macOS .app**，再用 `rcodesign` ad-hoc 签名——那是 .NET 才有的待遇（`dotnet publish -r osx-arm64`）。
**Rust 交叉编译 macOS 需要 Apple SDK，Tauri 打 `.app` / `.dmg` 也要 mac 工具链**，所以工作台必须加 **macOS runner**（私有仓库 10× 分钟计费，成本要提前算）。

**② 签名面比小白端大得多。**
Hardened runtime 下，`.app` 里**每一个 Mach-O 都要签**，而我们要塞进去的是一堆：我们的 app、`node`、codex（或 §2.4 的 shim + slim bin）、`node-pty` 的原生 `.node`、（若 V-12 裁不掉）sherpa-onnx 的 dylib。
两条从小白端那份 macOS 方案继承过来、**不因换栈而改变**的事实：

- **Apple Silicon 上未签名的 arm64 可执行文件会被内核直接杀掉**，不是弹警告；ad-hoc 签名是强制步骤，不是优化。
- **公证必须 Developer Program 账号**，没有例外。没账号 = 用户得走终端命令绕 quarantine；且 **TCC 授权绑定签名标识**，ad-hoc 每次重签可能让已授权失效、重新弹窗。

**由此产生一条平台不对称的建议**：**mac 上倾向"首启下载官方公证版 codex"**——官方产物已签名公证，我们不必替它背这一层；Windows 上则可以自建（§2.4 V-13）。两平台分发策略不同是可以接受的，但要写在发布清单里，别让它变成一次"为什么 mac 包里没有那个文件"的考古。

**③ 进程笼在 macOS 没有等价物——这是 D-4 的已知缺口。**
Windows 靠 Job Object 保证"关掉 Chat = daemon 死 = relay 断"。macOS 没有 Job Object，[`NullProcessCage`](../../paseo-adapter/src/LanAi.Paseo.Adapter.Host/IProcessCage.cs) 的注释已经点名过这种情况：**静默缺席的笼子是最坏的结果——代码读起来像有保护，实际孤儿 daemon 还活着**。
mac 上必须另做（进程组 + 父进程死亡监控 + 启动时孤儿清理，或等价方案），并且**不许用 NullProcessCage 降级发布**——否则 mac 版就是"用户以为关了，手机还能连"的那个洞。→ **V-14**。

**排期建议**：Windows 先行，mac 作为独立分期。但有**两个决定现在就得做，否则要返工**：CI 加 macOS runner 的成本，以及**要不要买 Developer Program 账号**（不买的话 mac 版的安装体验从第一天起就是打折的）。

### 2.6 认证：**登录授权直连中转站**，不是让用户填 API key

产品上只有一件事：**用户登录共飞账号并授权**。之后 codex **直连中转站**的 OpenAI 兼容端点，中间没有我们自己的代理进程。
后台确实是 key 机制，但那是**客户端托管**的实现细节，用户既不看见也不粘贴。

落到 `~/.codex` 上是两处写入（语义照抄 [`CodexConfigWriter`](../../codex-relay-client/src/LanAi.RelayClient.CodexBinding/CodexConfigWriter.cs)）：

- `config.toml`：`model_provider` + `[model_providers.<name>]` 的 `base_url`，**取服务端下发的 `api_base_url`**，不是从客户端拨号地址推出来的；
- `auth.json`：托管凭据。

**这里有一个必须原样继承的坑**（[`CodexAuthSnapshot`](../../codex-relay-client/src/LanAi.RelayClient.CodexBinding/CodexAuthSnapshot.cs) 的注释写得很清楚）：
codex 是**按 `auth.json` 里有什么来选凭据**的——一个 OAuth `tokens` 对象意味着"用已登录的 ChatGPT 账号"，并且**优先于**旁边的 key。
所以只加不删会得到一个"看起来正常、报告成功、却在悄悄扣用户 ChatGPT 套餐而不是共飞余额"的配置。
正确做法是**先把用户原有的账号材料快照收好，再替换**，登出/解绑时**还回去**。工作台必须整套继承，不能只写不还。

由此，工作台要自己拥有一条**租约生命周期**（不是一次性写死）：

```
登录授权 → 申请/复用托管 key → 写 auth.json + config.toml
        → 到期前续租 → 切分组时只发 group_id（§1.2 那个坑）
        → 登出/解绑 → 还原用户原有 auth.json
```

→ **V-15**：**会话跑到一半时续租或换分组会发生什么？** daemon 起的 codex 进程在启动时读了 `~/.codex`，中途重写文件、轮换凭据，对**正在进行的一轮**是无害、失败、还是静默走了旧凭据？这条不验，第一次线上遇到就是"跑了一半忽然 401"。

## 3. 组件职责

| 组件 | 负责 | 明确不负责 |
|---|---|---|
| **Paw（chat）** | 会话 UI、发提问、看时间线、收通知、选"操作哪台电脑" | 不认识 Paseo、不持 offer、不选工作目录 |
| **共飞服务端** | 鉴权、绑定关系、共联通道、把共联请求翻成窄契约、在线态 | 不直接说 Paseo 协议、不参与 codex 计费 |
| **Paseo 客户端适配层（服务端 Node）** | 唯一说 Paseo 协议的地方；连 relay、建会话、订阅时间线与通知、连接池 | 不做业务判断（不选目录、不定沙箱） |
| **PC 工作台** | daemon 生命周期与进程笼、`~/.codex` 路由与沙箱地板、**目录白名单与用户同意**、本机 UI、relay 开关 | 不承载手机业务逻辑 |
| **paseo daemon** | 拉起并管理 codex 会话、时间线、通知 | 只听 `127.0.0.1`，永不对外 |
| **codex app-server** | 真正干活 | —— |

---

## 4. 两个我先替你定了的决定（可推翻，写明代价）

### D-1：普通对话与 agent 会话是**两个面**，共用一个 UI 壳

理由是持久化模型不同，不是审美：

- 普通对话：状态在客户端本地（`usePawClient` + localStorage），服务端每次调用无状态；
- agent 会话：状态在 **daemon 的时间线**里，跨设备可见、可恢复、PC 关掉界面还在跑。

硬合成一套历史 = 两边都得重写。**推荐**：同一个侧栏里分两类会话，公用登录态、模型选择、附件上传。
反悔成本：低（前期分开，后期想合再合；反过来很贵）。

### D-2：**复用 Chat 现有的鉴权与传输层，另开一条会话通道**（上游 Q-2）

分两句话，别读成一句：

- **复用**：JWT 登录/刷新/401 重放、错误分类、SSE 解析、服务地址解析、三形态部署（§1.1）——这些不重写，agent 面挂上去就用。
- **另开**：`/v1/paw/chat/completions` 是每次调用独立的请求，**没有设备亲和、没有长连、没有可恢复的会话标识**。远程操控要按设备路由、要断线接回、要在 PC 干活时手机能随时挂上来看，塞进这个端点等于把它改成另一个东西。

所以新增的是 `/v1/paw/devices/*` 与一条会话通道，不是新增一个客户端。
反悔成本：中（通道形态定了以后改传输要动两端；但客户端那半是复用的，代价主要在服务端）。

---

### D-3：宿主用 **Rust（Tauri）+ 复用 Node bridge**，C# 那一半降级为参考

主体换成 Tauri 之后，窄契约多了第三个消费者，而现有的两个实现里只有一个还能用：

| 现有资产 | 去向 |
|---|---|
| [`bridge`](../../paseo-adapter/bridge)（TypeScript，唯一允许 import Paseo 类型的地方） | **原样复用**。它是我们对 Paseo 的全部知识（§2.1） |
| [`LanAi.Paseo.Adapter`](../../paseo-adapter/src/LanAi.Paseo.Adapter)（C# 窄契约客户端） | 降为**参考实现**：Rust 侧照着它的契约与错误模型重写 |
| [`LanAi.Paseo.Adapter.Host`](../../paseo-adapter/src/LanAi.Paseo.Adapter.Host)（进程笼、config 生成、健康探测、退避） | **移植结论、不移植代码**。它踩出来的东西（config 每次重写不合并、关闭 driver 自动重连、bridge 与 daemon 失败域分开）必须原样带过去 |

**关键的成本事实：Node 运行时反正要随包发**——paseo daemon 本身就是私有 node + 私有 `PASEO_HOME`。所以 bridge 搭同一个 node 的车是**免费**的，Rust 侧**不需要写任何 Paseo 客户端**，只说我们自己的 JSONL 窄契约。

**Rust 侧要薄**：只做进程监管 + 说我们自己的 JSONL 契约，**不做第二次投影**（bridge 给什么就往上送什么，含 `raw`）。任何「顺手在 Rust 里也理解一下 Paseo」的念头都要挡回去——那会把唯一的缝撕成两条，升级成本翻倍（§2.2）。

反悔成本：低（真要回到 .NET 宿主，bridge 和契约都不动）。
**代价要写明**：C# 那 82 项测试不会跟着过来，Rust 侧的宿主是新代码、要重新建立同等覆盖。

### D-4：宿主**放在 Chat 的 Tauri 壳里**，不另起外壳平台（2026-09-02 已定）

**daemon 的生命周期 = Chat 桌面版的生命周期。关掉 Chat，这台电脑手机就够不着。** 这是被接受的语义，不是缺陷。

定这个的三条依据，都是代码里的事实：

1. **[`IProcessCage`](../../paseo-adapter/src/LanAi.Paseo.Adapter.Host/IProcessCage.cs) 的进程笼是安全机制**：Windows 不自动回收，客户端被强杀后没被笼住的 daemon 继续活着、**relay 连接继续开着，手机仍能操作一台主人以为已经关掉的电脑**。独立常驻宿主天生就是这个状态——那不是"多一个进程"，是主动放弃这个属性再用托盘图标去补。
2. **进程笼在 Node 里没有现成路子**（要原生插件）。Rust 有 `windows` crate，C# 已经写好。所以宿主语言只能在 Rust 与 C# 里选 → **选 Rust**：同栈、笼是原生的、生命周期只有一个主人。
3. **`tauri.conf.json` 现在是 `dangerousUseHttpScheme: true` + `csp: null`**。独立宿主要走回环 HTTP，等于把工作区同意记录暴露给这个 webview 里的任何页面、以及本机任何能连上那个端口的东西。管道 / stdio 没有这个面。

**代价，认下来**（另有一条 macOS 上的缺口见 §2.5 ③：那里没有 Job Object，笼要另做，否则这条保证在 mac 上不成立）：

- 桌面版更新会打断在跑的会话（§2.2 的"升级与在跑会话协调"因此变成"升级前必须让用户看到有几个会话在跑"）；
- 本机 web / PWA 形态**不能**驱动 agent，只有桌面版能——手机走的是服务端那条路，不受影响；
- C# 的 119 项测试不跟过来（D-3 已计）。

**翻盘条件**（任一成立就得重开这题）：要求 Chat 退出后手机仍能操作、要求本机 web/PWA 也能驱动 agent、要求桌面版更新不打断会话。

### D-5：手机端只有 PWA，能力就两件事

**暂不发原生 App。** 手机端 = 现有那份 PWA（`manifest.webmanifest` + `service-worker.js`），能力范围就两件：

1. **简单对话**（走已有的 `/v1/paw/chat/completions`）；
2. **共联操作 PC**（§5.1 那张表）。

不做手机端的工作区管理、不做文件浏览、不做本地 agent。这与 §5.0 的"远程能力集定死"是同一句话的两面。

**要验的一件事**：PWA 的完成通知。`notifications.subscribe` 在 PC 上落托盘没问题，落到手机要走 Web Push——**iOS 必须先"添加到主屏幕"、且 16.4+ 才有**。如果这条不成立，手机端的"任务跑完了"就只能在前台看到，那会直接影响"发完任务把手机揣兜里"这个核心场景（见 §8 V-11）。

---

## 5. 共联协议要能表达什么

### 5.0 先分清两个能力集

工作台是**完整**产品，小白端是刻意做窄的产品——这两句话在能力集上的落点必须写清楚，否则会互相污染：

| | 本机（Tauri 壳直接对 bridge） | 远程（手机 → 服务端 → relay → daemon） |
|---|---|---|
| 定谁 | **产品说了算**，可以宽：多工作区、会话历史、diff/文件查看、更多 codex 选项 | **安全边界说了算**，只能是下面那张表 |
| 加东西的代价 | 加一个本机 UI + 一条契约操作 | 加一条**所有人都能远程发出的**指令 |
| 目前状态 | 待定（见 §8） | 本节这张表，**定死** |

下面这张表**只约束远程链路**。本机要不要有终端、要不要开 MCP、要不要放开别的 provider，是另一个问题，答它之前先看 §2.1 里 `config.json` 那几个开关的含义。

### 5.1 远程能力集（定死）

不定报文格式，只定**能力集**。一份 schema 生成两个消费者的类型（Tauri 壳的 Rust / 服务端的 Node），否则两边能力会慢慢漂开。

```
devices.list                 # 我有哪些已绑定电脑、在线否
devices.select <deviceId>    # 本次会话操作哪一台
agents.list / create / send / stop / archive
timeline.subscribe / unsubscribe <cursor>   # 断线后按游标续订，不重放整轮
notifications.subscribe      # finished / error / permission
workdirs.list                # 只回目录键与显示名，不回真实路径
```

**在远程链路上，不在这张表里的东西不是"暂时不做"，是协议上不存在**：终端、文件浏览、git、schedule、插件、语音。
这正是"手机端不说 Paseo"换来的唯一硬边界 —— 手机发不出 `create_terminal_request`，不是因为 daemon 拒绝，而是**我们的栈里没有任何一处能发出这条消息**。把它写成"默认关闭"就等于把这个边界白白扔掉。

---

## 6. 服务端新增工作（安全前提写在这里，不是附录）

这是全项目最大的一块新代码。两个安全事实**先于**表结构存在，不是做完再补：

> **V-9 实测（2026-09-01）：经 relay 时 daemon 口令被完全绕过，offer 本身就是完整的 owner 凭据 —— 等价于那台电脑的 root 会话。** 口令轮换对 relay 路径没有任何效果。

> **零知识在本部署下不成立**：E2EE 的客户端那一端在我们服务器上，会话明文过共飞服务端内存。Paseo 官方那句"代码不离开你的机器"**不能照抄进宣传**。

由此推出的必须项：

| 要做的 | 要求 |
|---|---|
| 绑定表 | `用户 ↔ (serverId, 机器名, offer, daemon 口令, 最后在线时间)`；**offer 与口令按用户加密存储**，任何日志/工单/客服系统都不得出现 |
| 作废 | PC 端一键作废（换 `PASEO_HOME` → `serverId` 变）；两端都能发起解绑 |
| 审计 | 每次适配层建连记 `用户 / serverId / 时间 / 来源`。**"我这台电脑正在被谁操作"只能由服务端的绑定表回答**——daemon 不记录配对关系（§2.1），PC 端自己查不出来，这条信息必须服务端下发给 PC |
| 连接池 | 按 `serverId` 复用，空闲回收；**绝不为每个手机请求新建 relay 连接**。注意 §2.1 那条上限：**每条连接的时间线订阅只能挂住约 5 个 agent**，超了最老的被淘汰——池的容量模型是"连接数 × 5"，不是"一个用户一条连接看全部" |
| 跨用户隔离 | 一个用户的连接绝不被另一个用户的请求复用；`serverId` 必须校验归属 |
| 在线态 | PC 离线时 Paw 显示"这台电脑不在线"，而不是超时报错 |
| **事件游标** | 每个 agent 的时间线事件必须有单调游标，重连时问"给我 N 之后的"。**不能沿用 `pawRequest` 那套 401 重放**——请求可以重放，跑了一半的会话流不行；重放 = 时间线重复。适配层已经在批次里报 `dropped` 计数，游标语义要在共联通道上接住 |
| 并发 | 同一台 PC 可能被 Paw 与本机同时操作；daemon 侧是串行的，UI 要能表达"正在忙" |
| 计费 | 仍在中转站（codex 请求经 `~/.codex` 路由过去），**适配层不参与计费** |
| 落盘策略 | 时间线是否落库、留多久 —— 见 Q-1，**必须先有答案再写存储层** |

绑定不需要扫码：PC 客户端本来就持有已登录的共飞会话，点"允许手机操作这台电脑" → 弹一次授权告知 → 开 relay 取 offer → 用现有会话上报服务端 → Paw 侧多出一台电脑。QRCoder 那条路可以删。

---

## 7. 分期：按解锁关系排

### M0 —— PC 本机 agent 闭环（**无服务端依赖，先做这个**）

打通这条链：**Tauri 壳（Rust）↔ bridge（Node）↔ daemon ↔ codex**（D-3）。

这一期的重量在 Rust 侧——现在的 `main.rs` 只有 7 行，宿主能力从零开始：

- **扩 Tauri 壳**：进程启动（sidecar / Command）、文件系统、托盘；随包发私有 node + `PASEO_HOME`；
- **进程笼绑到 Chat 进程本身**（不是绑到某个中间 sidecar）——绑错了就等于 D-4 依据 1 那个洞；
- **移植宿主**：按 `PaseoRuntime` / `DaemonSupervisor` / `DaemonConfigComposer` 的**结论**重写（config 每次重写不合并、进程笼、退避重启、健康探测、有序停止）；
- **窄契约客户端**：Rust 侧照 C# 那份的操作与错误模型重写，跑一遍它的用例；
- **前端**：Next.js 里新增 agent 会话面（选目录键 → 建会话 → 发提问 → 看时间线 → 停止 / 归档），走 Tauri IPC 而不是 HTTP；
- 通知（`finished` / `error` / `permission`）落到托盘；
- `~/.codex` 路由写入（照 `CodexBinding` 的语义重写，不搬注入宿主）。

**出口**（happy path 不够，必须包含下面三条）：

1. 一台干净机器上，装完 Chat 桌面版能完成一轮真实 codex 任务并看到结果，全程不碰服务端新代码；
2. **一个应当被沙箱地板挡住的任务确实被挡住**（写白名单外目录 / 越权命令）——V-7 只有这样才会现形；
3. **一次审批请求真的弹到了用户面前**并且拒绝生效。
**携带验证**：V-7（`config.toml` 的 `sandbox_mode`/`approval_policy` 对 app-server 会话是否生效）、V-8（`agents.providers.codex.command` 钉绝对路径）、daemon 进程环境继承（PATH / `USERPROFILE` / 代理变量）。这三个**只有在这一期能便宜地发现**。
**省时间的走法**：现成的 `PaseoRuntime.StartAsync`（C#）可以**当一次性验证工装**，本机跑，先把 V-7 / V-8 / 环境继承这三条验掉，再动 Rust 宿主。**但它不进安装包**——被 Tauri 拉起时笼绑在它自己身上而不是 Chat 上，发出去就是带着已知的错误生命周期上线。

**新增前置**：Tauri 1 还是升 2（sidecar 与权限模型 2 更顺手），以及 node 与 daemon 怎么随包分发、装机体积多大——见 §8。

### M1 —— 工作台化（含账号面，**与 M0 无依赖，可并行**）

两条独立的线：

**M1a 账号与个人中心**（不碰 agent 链路，后端接口全都现成，纯客户端工作）：

- 公开设置驱动的注册入口 + 注册 + 邮箱验证码 + Turnstile；
- 2FA 登录；个人信息；
- 用量与额度、分组与倍率（高峰只显示时段标签）、API key 租约与换组、订阅与充值、公告；
- 参考 §1.2，把那三个坑当验收用例写进去。

**M1b 本机工作台**：

- 目录白名单与**用户同意**流程（爆炸半径告知）；
- 会话与任务的本机持久化，重启后可恢复；
- 崩溃分类与降级：daemon 不在 / codex 缺失 / 目录键非法 / 会话忙，各自的界面表达；
- `~/.codex` 路由写入与快照恢复（照抄 `CodexBinding` 的语义，**不搬注入宿主**）；
- **认证按 §2.6 做整条租约生命周期**（登录授权 → 托管 key → 续租 → 还原），照抄 `CodexAuthSnapshot` 的快照/恢复语义；
- **`~/.codex/config.toml` 现在有两个利益方，必须定谁最后写、崩溃后怎么恢复**：账号线要把 codex 路由到中转站（计费靠它），agent 线 daemon 起的 codex 也读同一个文件。（daemon 自己的 `config.json` 是另一个文件，由 Host 每次重写，见 §2.1——别把两者混为一谈。）这是账号线和 agent 线唯一真正打架的地方。

**出口**：一个不写代码的用户能自己注册、看懂自己的额度、跑一轮 agent 任务；出错时看得懂发生了什么。

### M2 —— 服务端骨架（可与 M1 并行，但不能早于 M0）

- 设备登记与绑定表（含 §6 全部安全要求）；
- 服务端 Paseo 客户端适配层（Node，复用 `paseo-adapter/bridge`）+ 连接池 + 隔离；
- **relay 自建部署**（Elixir 官方镜像，与 sub2api 同机）。

**出口**（"一个用户一个 agent 能连上"不算通过）：多用户 × **每人 5 个以上并发 agent** 下连接池行为正确——订阅淘汰是可见的（拿回订阅集比对），`dropped>0` 时确实回查了权威列表，跨用户隔离有测试。§2.1 那类"看起来连上了、就是什么都不来"的 bug **只有在这个规模下才现形**，漏到 M3 就变成手机上一个不动的时间线面板。
**携带验证**：V-10b（relay 容器与 sub2api 同机共存、TLS 终结在谁那里、`daemon.relay.endpoint` / `publicUseTls` 取值组合）—— 这项要服务器，**别拖到 M3 才发现部署不通**。

### M3 —— Paw 共联

- 共联通道（D-2）+ 设备列表 / 选设备；**客户端复用 `pawRequest` 那一层，只加会话语义**；
- 手机发起会话、增量渲染时间线（复用 SSE 分帧与累积）、收完成通知；
- **agent 会话历史从服务端/daemon 取，不落 localStorage**——这是与普通对话最大的实现差异（§1.1 末段）；
- **PC 上能看到手机发起的会话**（两端订阅同一个 daemon，这是白送的，不要为它再造同步机制）；
- 断线重连、超时、取消；
- **手机端范围就是 D-5 那两件事**：简单对话 + 共联操作。完成通知走 Web Push，**先过 V-11**。

**出口**：手机上发一句"把这个测试修好"，PC 干活，手机看到过程和结果，中途断网能接回来。

### M4 —— 收口

审计视图、一键作废、离线排队策略（Q-3 定了才做）。

**加上 §2.2 的版本工程收口**：Paseo 三件套（daemon / `@getpaseo/client` / relay 协议版本）联动发布清单；版本清单 + 校验 + 回滚；升级闸门（一致性冒烟不过就不许发）；升级与在跑会话的协调。
这块**不是 M4 才开始做**，M0 就得有雏形——否则第一次跟上游升版就会变成一次考古。

---

## 8. 待验证与待决（不预设答案）

| 编号 | 内容 | 承接 | 类型 |
|---|---|---|---|
| V-7 | `config.toml` 的 `sandbox_mode` / `approval_policy` 对 app-server 会话是否生效 | M0 | 技术验证 |
| V-8 | `agents.providers.codex.command` 钉绝对路径，能否让 PATH 上没有 codex 的机器起会话 | M0 | 技术验证 |
| — | daemon 拉起的 codex 继承 daemon 环境而非终端环境（PATH / `USERPROFILE` / 代理） | M0 | 技术验证 |
| V-10b | 自建 relay 与 sub2api 同机共存、TLS 终结位置 | M2 | 技术验证 |
| Q-1 | 零知识作废怎么对用户表述？服务端是否落盘会话内容、留多久 | **M2 之前**必须定——M3 的 agent 历史以服务端为准，没有保留策略就没有它要读的那张表 | 产品决策 |
| Q-3 | PC 离线时 Paw 的表现：纯提示 vs 排队等上线 | M3 / M4 | 产品决策 |
| — | **桌面端形态：已定 —— Chat 的 Tauri 壳**（你 2026-09-02 拍的）。工作台与小白端是两个产品，不共代码 | —— | ✅ 已定 |
| — | **宿主位置：已定 —— 装在 Tauri 壳里，笼绑 Chat 进程**（D-4，你 2026-09-02 拍的） | —— | ✅ 已定 |
| — | **手机端形态：已定 —— 只发 PWA，能力＝对话 + 共联**（D-5） | —— | ✅ 已定 |
| **V-11** | **PWA 完成通知**：Web Push 在 iOS（须添加到主屏、16.4+）与 Android 上到底能不能用。不成立的话「发完任务把手机揣兜里」这个核心场景就没了 | **M3 之前**，越早越好 | 技术验证 |
| — | **Tauri 1 → 2**：sidecar 与权限模型 2 更顺手，越晚升越贵 | **M0 开工前** | 技术决策 |
| — | 私有 node + paseo daemon 怎么随 Chat 桌面版分发？装机体积、更新通道、macOS 签名 | M0 | 技术决策 |
| — | **codex 随包发还是首启下载**？取决于 V-13 量出来的自建体积（§2.4）。官方那个 341 MB 是未 strip 的，不是判决书 | M0 | 技术决策 |
| **V-13** | **自建 codex 的体积**：`codex` 与 `codex-app-server` 各自 release + strip 后多大。决定走 §2.4 的 A 还是 B | **M0**，和 V-12 一起做 | 技术验证 |
| **V-15** | **会话中途续租/换分组**：codex 进程启动时读过 `~/.codex`，中途重写凭据对正在跑的那一轮是无害、失败、还是静默用旧凭据（§2.6） | M0/M1b | 技术验证 |
| **V-14** | **macOS 进程笼替代方案**：没有 Job Object 的情况下怎么保证「关掉 Chat = daemon 死 = relay 断」（§2.5）。**不许用 NullProcessCage 降级发布** | mac 分期开工前 | 技术验证 |
| — | **要不要买 Apple Developer Program**：不买则 mac 版无法公证，安装体验从第一天起打折，且 TCC 授权可能每次更新重弹 | mac 分期立项时 | 产品决策 |
| — | **CI 加 macOS runner** 的成本（私有仓库 10× 计费）；Rust/Tauri 无法沿用小白端「windows 交叉编译 + rcodesign」那条路 | mac 分期立项时 | 工程决策 |
| **V-12** | **paseo `node_modules` 能不能裁**：claude provider 250 MB + 语音 22 MB 是 `@getpaseo/server` 的硬依赖，删掉后 daemon 还起不起得来 | **M0**，决定安装包是 150 MB 还是 400 MB | 技术验证 |
| — | 跟上游版本的节奏：钉死一个 beta 到几时、多久 bump 一次、谁跑升级闸门 | M1 | 工程决策 |
| — | **本机能力集边界**（§5.0）：本机要不要终端 / 文件与 diff 查看 / 多工作区 / 放开别的 provider。远程那半已定死，本机这半没定 | M1b 之前 | 产品决策 |
| — | 一个用户是否支持多台已绑定 PC（协议里已留 `devices.*`，但 UI 与计费口径未定） | M3 | 产品决策 |

已经**不再是问题**的（旧文档里还列着，删掉）：自建 relay 与业务同机（定了）、手机远程必经服务端（定了，因为唯一 Paseo 客户端在服务端）、服务端是否复用 sub2api（复用，`/v1/paw` 已在跑）。

---

## 9. 明确不做

- **不开发 codex，不开发 Paseo**：不 fork、不改源码、不加功能、不做平行实现；只写适配器 + 跟版本 + 按版本打包（§2.2）；
- **不做注入**：不注入官方 Codex 客户端、不做 CDP 增强（`RelayInjectionHost` 那条线只当历史资料）；
- 不做 Agent 市场、不做多 Runtime 抽象；
- 不做手机端终端 / 文件浏览 / 任意命令执行（§5：协议上不存在）；
- 不把本地工作区、绝对路径、完整日志上传服务端；
- 不做团队 / 多人共享 —— 单用户跑顺了再说。
