# 共飞直连客户端（小白版）—— 寄宿 Paseo、手机远程操作方案

> 状态：**可行性分析 + 方案草案，未动代码**。整理于 2026-09-01。
> 参考源码：`C:\Work\Git\paseo-main`（Paseo v0.7.0-beta.3，Apache-2.0）。
> 对应实现落点：`tools/codex-relay-client/`（LanAi.RelayClient.sln）。
> 后续文档：[`共飞客户端 × Paseo 核心封装架构设计.md`](共飞客户端%20×%20Paseo%20核心封装架构设计.md)
> ——"只封装核心"的能力矩阵、强制点分级、窄契约与分层设计。

---

## 0. 先说一件必须先对齐的事：你要的"操作 chatgpt 客户端"有两种读法

这不是措辞问题，两种读法对应**两套完全不同的工程**，选错方向整篇方案作废，所以放在第一段。

| | A 读法（Paseo 能给的） | B 读法（字面意思） |
|---|---|---|
| 手机操作的对象 | 本机 **`codex` CLI 会话**（Paseo 拉起的独立进程） | 本机**正在运行的 ChatGPT 桌面版（Electron）窗口** |
| 靠什么实现 | Paseo daemon + provider 适配器 | 我们自己的 `AiSwitch.Injection`（CDP 注入） |
| Paseo 的贡献 | **全部**：会话管理、时间线流、手机 App、E2EE relay | **零**。Paseo 不接管别人的 Electron 进程 |
| 今天能做到吗 | 能，是 Paseo 的主线功能 | 要自己造一条远程通道接到已有注入层上 |
| 成本 | 集成为主（详见 §11） | 新造轮子为主，且随官方 UI 改版持续腐化 |

**Paseo 官方文档明确写了这条边界**（`public-docs/codex.md`）：

> "The ChatGPT desktop app and the Codex CLI are separate installs. Installing the desktop app does not make the `codex` command available to Paseo."

Paseo 跑的是 `@openai/codex` **CLI**，不是桌面版 GUI。

**本方案按 A 规划。** 理由不只是"A 能做"：A 对共飞其实**更合身**——CLI 读的正是
`~/.codex/config.toml` + `auth.json`，也就是 `LanAi.RelayClient.CodexBinding` 已经在写的那对文件，
所以手机端发起的每一次请求都自动走中转站路由、自动计入配额，不需要额外做任何计费打通。

两者之间**唯一的缝**是 `paseo agent import --provider codex <session-id>`
（桌面版与 CLI 共用 `~/.codex/sessions`），理论上能把桌面版的对话接过来在手机上续聊。
**此项未经验证**（见 V-6），不要写进产品承诺。

> 如果你要的其实是 B（手机点桌面版的按钮 / 看桌面版的界面），说一声，
> 我按 `AiSwitch.Injection` + 远程通道重新出方案，Paseo 在那条路上帮不上忙。

---

## 1. 结论摘要

| 问题 | 结论 |
|---|---|
| 寄宿 Paseo 可行吗？ | ✅ **可行**。Apache-2.0，daemon 是普通 Node 进程，控制面（配对、列会话、跑会话）全部有 `--json` 出口。 |
| 要不要自己写 C# 版协议？ | ❌ **不要**。协议 7054 行 zod 且仍是 `0.7.0-beta`，手写 C# 复刻 = 长期维护税。 |
| 那 SDK 怎么做？ | **Node 边车 + 窄契约**：`cofly-paseo-bridge`（Node，包 `@getpaseo/client`）对外只暴露约 10 个操作；C# 侧只实现这 10 个操作的客户端（§4）。 |
| "受客户端节制"怎么落地？ | Win32 **Job Object + `KILL_ON_JOB_CLOSE`**，客户端被任务管理器强杀时 daemon 一起死（§5）。这是天真实现最容易漏的一条。 |
| 手机端怎么连？ | 分三步：① 官方 Paseo App + `paseo daemon pair --relay`（零基建）→ ② 自建 relay 换掉 `relay.paseo.sh` → ③ 需要品牌时再上自有通道 + `--web-ui`（§7）。 |
| 最大的产品风险 | **不是技术，是授权语义**：配对一次 = 手机拿到这台电脑的**终端与文件读写**，不只是聊天。小白版必须显式告知并显式开关（§6.3）。 |
| 最大的工程风险 | 小白用户装不齐前置：Node 22+、原生模块、**外加一个和桌面版无关的 `codex` CLI 安装与登录**（§8）。 |
| 手机通知怎么做？ | **不用自己接 Codex App Server**——Paseo 本来就是通过 `codex app-server` 驱动 Codex，并已把结果归一成 `agent_attention_required`（`finished`/`error`/`permission`）推给客户端。直接消费它（§7.5）。 |
| 阻塞级验证 | V-1（私有 node 免装 Node 起 daemon）**已实测通过，见 §10.1**；V-2 **已关闭**（官方明确支持 API key 登录，与账号登录无区别）；relay 链路见整体架构文档 §12（V-10a 通过）。**阻塞项已清零。** |
| 工作量 | 约 **18–26 人日**（bridge 3–4、C# SDK+宿主 5–7、UI 与配对 3–4、打包 4–6、验证 3–5），不含手机端自研。 |

---

## 2. Paseo 是什么（核对过的事实，不是宣传语）

- **daemon**：Node.js 进程，监听 WebSocket（默认 `127.0.0.1:6767/ws`），负责拉起/管理各家 agent CLI、
  推时间线流、可选外连 relay。源码 `packages/server`。
- **协议**：`packages/protocol/src/messages.ts`，**7054 行** zod schema；仓库里专门有
  `messages.wire-compat.test.ts`——说明线上协议是会动的。
- **官方客户端库**：`@getpaseo/client`（`daemon-client.ts` 6310 行）。这是唯一被维护的客户端实现。
- **鉴权**：口令即 Bearer。`packages/client/src/daemon-client.ts:1201-1207` ——
  `Authorization: Bearer <password>`，同时把口令塞进 WebSocket 子协议 `paseo.bearer.<password>`。
- **relay**：Curve25519 + NaCl box 端到端加密，中继服务器零知识；新装默认**关闭**，配对即同意闸门。
  生产 relay 是独立仓库 `getpaseo/paseo-relay`（Elixir），可自建。
- **CLI**：`paseo daemon pair --relay --json`、`paseo agent import --provider codex <id>`，
  多数命令支持 `--json`。控制面完全可脚本化。
- **原生依赖**：`node-pty`（终端）、`sherpa-onnx-node`（语音 VAD，体积大，见 V-5）。
- **可选自带 Web UI**：`paseo daemon start --web-ui`，daemon 同端口提供浏览器版界面。

---

## 3. 目标架构

```
        手机 (Paseo App / 浏览器)
                 │  E2EE WebSocket
        ┌────────▼──────────┐
        │ relay（第三方 → 自建）│
        └────────▲──────────┘
                 │ 出站长连（daemon 主动连出，不开入站端口）
╔════════════════╪═════════════════════════════════════════╗
║  共飞小白客户端（LanAi.RelayClient，宿主进程）                ║
║                │                                          ║
║  ┌─────────────┴───────────────┐                          ║
║  │ LanAi.RelayClient.Paseo.Host │ 进程监管 / 配置生成 / 日志  ║
║  │   └ Job Object（kill on close）                         ║
║  └─────────────┬───────────────┘                          ║
║                │ spawn（私有 node.exe）                     ║
║   ┌────────────▼─────────────┐   命名管道 JSONL             ║
║   │ node.exe paseo daemon    │◄──┐（窄契约，约 10 个操作）    ║
║   │  + cofly-paseo-bridge    │   │                         ║
║   └────────────┬─────────────┘   │                         ║
║                │ spawn            │  ┌─────────────────────┐║
║   ┌────────────▼─────────────┐   └──┤ LanAi.RelayClient.   │║
║   │ codex CLI（会话进程）      │       │ Paseo（C# 窄客户端）  │║
║   └────────────┬─────────────┘       └─────────────────────┘║
║                │ 读                                        ║
║        ~/.codex/config.toml ◄── CodexBinding 写入（已有）    ║
╚════════════════╪═════════════════════════════════════════╝
                 │ HTTPS
            共飞中转站（sub2api）
```

一句话：**Paseo 负责"会话与远程"，共飞负责"身份、路由、配额与生命周期"**，
两边的接触面只有 `~/.codex` 那对文件 + 一个窄契约。

---

## 4. SDK 边界设计（这是你问题的核心）

### 4.1 为什么是 Node 边车，不是 C# 复刻协议

一句话理由：**7054 行 zod schema + `0.7.0-beta` 版本号 + 仓库自带 wire-compat 测试**
= 协议还在动。手写 C# 复刻的那一天起，每次 Paseo 升级都变成我们的回归测试在替他们兜底。
换成边车之后，Paseo 升版是**只改 bridge 的一次 npm 升级**，C# 侧的契约测试一行不用动。

代价是多一层 IPC。考虑到 daemon 本来就是 Node 进程、bridge 可以用**同一个 `node.exe`**
起一个入口脚本（脚本内同时 `import` daemon 与 `@getpaseo/client`），这个代价接近于零。

### 4.2 三个新工程

> ✅ **2026-09-01 已落地第一条纵切**，实际工程名与位置见"实现"列。
> 代码落在 **`tools/paseo-adapter/`**（独立 sln），**不在** `codex-relay-client` 里面——
> 它有两个以上消费者（小白客户端、将来完整版 chat 客户端、服务端给 Paw 的适配层），
> 放进任何一个客户端目录，第二个消费者就得先做一次搬迁。
> 实现时还定死了原文没写的两件事：
> ① **管道方向 = C# 当服务端、bridge 当客户端**（.NET 的 `PipeOptions.CurrentUserOnly`
> 能把 ACL 钉到当前用户，Node 建的管道拿不到同等控制）；
> ② **bridge 与 daemon 是兄弟进程**，都由 Host 拉起、同进一个 Job Object，
> 因为失败域不同（bridge 崩溃不该重启带着活跃会话的 daemon）。


| 工程 | 形态 | 约束 | 职责 |
|---|---|---|---|
| `cofly-paseo-bridge` ✅ `tools/paseo-adapter/bridge/` | Node（TS），随客户端分发 | **精确锁定** `@getpaseo/client@0.7.0-beta.3` | 包住 Paseo 客户端，对外只讲窄契约 |
| ~~`LanAi.RelayClient.Paseo`~~ → ✅ **`LanAi.Paseo.Adapter`** | `net8.0` 类库 | **零 NuGet、不引用 WPF/Avalonia**（与现有 `LanAi.RelayClient.Server` 同规） | 窄契约的 C# 客户端（JSONL over 命名管道） |
| ~~`LanAi.RelayClient.Paseo.Host`~~ → ⬜ `LanAi.Paseo.Adapter.Host`（未开工） | `net8.0` 类库 | 平台相关代码走 `Platform/Windows`、`Platform/MacOS`（与 Core 现状一致） | 进程监管、`config.json` 生成、健康探测、退避重启、进程笼 |

**C# 侧永远不解析 Paseo 的任何 schema** —— 这是这个切分最重要的产出。

### 4.3 窄契约（v1，约 10 个操作）

请求/响应用 JSON Lines，事件走同一条管道推送；`id` 关联请求与响应。

| 操作 | 用途 |
|---|---|
| `health` | daemon 是否活、版本、监听地址 |
| `providers.list` | codex 是否就绪（对应"未装 CLI"的引导态） |
| `agents.list` | 会话列表（PC 首页与手机端同源） |
| `workdirs.list` | 可用工作目录：**只回 key 与显示名，不回路径** |
| `agents.create` | `{cwdKey, model?, prompt?}` 建会话 —— **传 key，不传路径** |
| `agents.send` | 追加一轮提问 |
| `agents.stop` / `agents.archive` | 停止 / 归档 |
| `timeline.subscribe` / `timeline.unsubscribe` | 订阅会话流（事件推送） |
| `notifications.subscribe` | 转发 Paseo 的 `agent_attention_required`（`finished`/`error`/`permission`），见 §7.5 |
| `relay.status` / `relay.pair` / `relay.disable` | 配对开关与配对链接（供 `QRCoderRenderer` 出码） |

握手：bridge 启动时由宿主生成一次性 token，经环境变量传入，管道首帧校验，不匹配直接断开。
**管道用命名管道，不用回环 TCP**——回环 TCP 同机任何进程都能连，命名管道可以用 ACL 限定到当前用户。

契约冻结在 C# 侧的契约测试里，做法沿用 `LanAi.RelayClient.Server.Tests` 的"真实响应核对"：
录一份 bridge 的真实输出当基线。

### 4.4 兜底通道

`paseo ... --json` 保留为**排障与一次性运维**的备用路径（例如 bridge 起不来时让用户导日志），
不作为主路径——每次调用起一个 node 进程太贵。

---

## 5. "寄宿、受客户端节制"怎么真正做到

### 5.1 进程笼（最容易漏的一条）

Windows 不会替你回收子进程。客户端被任务管理器强杀或崩溃后，daemon 会**继续活着并保持
relay 长连**——也就是用户以为程序关了，手机却仍能操作这台电脑。必须：

- 宿主创建 Win32 **Job Object**，设 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`，
  把 node 进程及其派生的 codex 进程都放进去；
- macOS/Linux 侧用进程组 + `SIGTERM`（Avalonia 版要用），抽象成 `IProcessCage`，
  实现放 `Core/Platform/{Windows,MacOS}`；
- 验证方式必须是**强杀**，不是优雅退出（V-3）。

### 5.2 与现有退出契约的关系（会打架，必须先定）

现有契约（见 README 与 `ClientExitCoordinator`、`ClientShutdownCoordinator`）：
**真正退出**时把 `~/.codex/config.toml`、`auth.json` 还原成打开前的样子；**最小化到托盘不还原**。

寄宿 daemon 后建议定成：

| 客户端状态 | daemon | 理由 |
|---|---|---|
| 未登录 | 不启动 | 没有中转站身份就没有路由，跑起来也是错的 |
| 已登录、窗口打开 | 运行 | —— |
| 最小化到托盘 | **继续运行** | 与"托盘不还原配置"一致，也正是手机远程要用的状态 |
| 真正退出 | **先停 daemon，再还原配置** | 顺序反了会让最后一轮请求打到已还原的官方路由上 |
| 退出登录 | 停 daemon + 关闭 relay | 会话凭证已失效，relay 不能继续留着 |

`CodexRouteGuard` 目前守的是"配置被官方客户端改掉"，daemon 在跑时它依然要生效——
但**不要在 daemon 有活跃会话时静默改写 `config.toml`**，否则正在跑的 codex 进程会读到半新半旧的状态。
建议给 guard 加一个"有活跃会话时延迟改写并提示"的分支。

### 5.3 端口与实例隔离

daemon 固定端口 `6767` 会和用户自己装的 Paseo 撞。做法：宿主挑一个空闲端口，写进我们**私有的**
`PASEO_HOME`（例如 `%LOCALAPPDATA%\LanAi.RelayClient\paseo-home`），
**绝不复用 `~/.paseo`**，避免和用户自己的 Paseo 配置互相污染。

---

## 6. 安全与授权

### 6.1 本地面

- `daemon.listen` 固定 `127.0.0.1:<随机端口>`，**永不绑 `0.0.0.0`**；
- 每次安装生成随机口令，走 `Authorization: Bearer`。
  ⚠️ **实测默认是不设口令的**：V-1 里 daemon 起来时日志明写 `authRequired:false`，
  且 `/api/health` 在 `auth.ts:125` 里被显式豁免鉴权。口令必须由我们主动写进配置；
- ⚠️ 实测自动生成的 `config.json` 默认把 `https://app.paseo.sh` 放进
  `daemon.cors.allowedOrigins` 和 `app.baseUrl`。我们生成配置时应当清掉，
  除非确实要让官方 Web 客户端连本机；
- 口令存储**复用现成的** `CodexBinding/ISnapshotProtector` + `SnapshotBlobFormat`（DPAPI 那条路），
  不要新造一套密钥存储。

### 6.2 远程面

⚠️ **2026-09-01 更正**：本段原说"relay 端到端加密、中继零知识，这一层不需要我们加固"，
在"自建 relay + 服务端适配层"的最终拓扑下**两点都不成立**：零知识作废（客户端那端在我们服务器上），
且 **relay 路径完全绕过 daemon 口令、一律 owner 权限**（V-9 实测）。
详见 [`共飞 Paw 远程操作整体架构.md`](共飞%20Paw%20远程操作整体架构.md) §8.2 与 §12。
下面这段关于"开关与可见性"的要求仍然有效：
需要我们做的是**开关与可见性**：默认关闭、配对即开、界面上一眼能看到"当前有 N 台手机已配对"，
并且能一键解绑。

### 6.3 产品必须显式告知的一句话

Paseo 的 agent **能开终端、能读写工作目录的文件**。配对一台手机 ≠ 授权聊天，
而是授权"在这台电脑上执行命令"。小白版必须：

- 配对前弹一次明确的告知（不是折叠在协议里）；
- 默认把可访问范围限制到用户选定的**一个工作目录**，而不是整盘；
- 在托盘图标/主界面常驻"远程已开启"的可见状态。

这是**产品决定，不是技术细节**，需要你拍板。

---

## 7. ~~手机端接入路径（分三步，别一步到位）~~ —— **本节已作废**

> 2026-09-01 更新：手机端确定为**共飞 Paw 端**（不含 Paseo 逻辑），
> relay 与共飞服务端**同机自建**，不再有三阶段分期。
> 以 [`共飞 Paw 远程操作整体架构.md`](共飞%20Paw%20远程操作整体架构.md) 为准。
> 下面内容仅作历史留存。

| 阶段 | 做法 | 得到 | 代价 |
|---|---|---|---|
| **MVP** | 官方 Paseo App（iOS/Android）+ C# 调 `paseo daemon pair --relay --json`，用已在用的 `QRCoder` 出二维码 | 零新基建，几天内能演示 | 用户要装第三方 App；依赖 `relay.paseo.sh` |
| **第二步** | 自建 relay（`getpaseo/paseo-relay`，Elixir）部署到 transit-hub 旁，客户端写 `daemon.relay.endpoint` + `useTls` | 去掉第三方依赖，数据面仍用官方 App | 多一套服务要运维 |
| **第三步（品牌需要时）** | `paseo daemon start --web-ui` + 自有隧道，手机用浏览器进 | 无需装 App、完全自有品牌 | **隧道要自己造**；注意 relay 的 E2EE **不覆盖** web-ui 这条路，传输安全要自己保证 |

**不建议**把"按共飞品牌重编 Expo App"当 MVP。Apache-2.0 允许，
但双端 App 的长期维护成本远大于这次要做的集成本身。

---

## 7.5 通知：不必自己接 Codex App Server

结论先行：**Paseo 已经在用 Codex App Server**，而且已经把通知语义做完了，我们只要消费。

已核对的事实：

- Paseo 的 Codex 适配器就是 app-server 客户端——
  `packages/server/src/server/agent/providers/codex-app-server-agent.ts` 拼出的启动参数是
  `[...launchPrefix.args, "app-server"]`（该文件 6862 行），配套的
  `codex/app-server-transport.ts` 负责 JSON-RPC 帧。也就是说 `codex app-server` 是 Paseo 的**内部实现**。
- Paseo 把各家 provider 的信号归一成了一个**通知专用载荷**：
  `packages/protocol/src/agent-attention-notification.ts` 定义
  `AgentAttentionReason = "finished" | "error" | "permission"`，
  payload 含 `title` / `body` / `data{serverId, workspaceId, agentId, reason}`，
  并对 Markdown 做了纯文本化与 220 字截断。
- 它是**协议级消息**，会推给所有客户端：`messages.ts` 里的 `agent_attention_required`
  （4469 行）、`clear_agent_attention`（2722 行），会话快照上还有 `attentionReason` / `attentionTimestamp`。

由此的取舍：

| 做法 | 什么时候选 | 代价 |
|---|---|---|
| **消费 Paseo 的 `agent_attention_required`**（推荐） | 会话由 Paseo 管理（本方案的全部场景） | 零协议新增；PC 端弹 Windows 通知，手机端由 Paseo App 原生收；`permission` 这类"要人点头"的事件天然覆盖 |
| 自己起 `codex app-server` 接 JSON-RPC | 只有**不经 Paseo** 的会话才需要——例如用户在官方桌面版里手动开的对话 | 等于再养一套 provider 适配；且与 Paseo 抢同一个 codex 进程模型 |

落到窄契约上：在 §4.3 基础上加一个 `notifications.subscribe`，
bridge 把 `agent_attention_required` 原样转成 `{agentId, reason, title, body}` 推给 C#，
由客户端决定弹托盘通知还是静默。**不要**在 C# 侧解析 Paseo 的时间线来自己推断"是否需要关注"——
那正是 `agent-attention-policy.ts` 已经在 daemon 里做过的事。

> 若之后要给"官方桌面版里正在跑的对话"也做通知（B 读法那条线），
> 那时才值得单独接 `codex app-server`，或者继续用 `AiSwitch.Injection` 的
> `CodexLimitSentinel` 那套 DOM 观测。两者互不冲突。

---

## 8. 打包与前置依赖（小白版的真正难点）

下表的体积是 **2026-09-01 实测**（V-1 装出来的 `node_modules`，见 §10.1）。

| 依赖 | 实测 | 做法 / 备注 |
|---|---|---|
| 私有 `node.exe` | 解压后约 **80 MB**（zip 35.8 MB） | Node **v24.20.0 LTS**；绝不碰系统 Node、绝不改 PATH |
| `node_modules` 总量 | **415 MB** | 预构建随包，不能指望小白机器跑 `npm install` |
| ├ `@anthropic-ai/claude-agent-sdk-win32-x64` | **239 MB（占 58%）** | **最大的体积杠杆**：只要 codex 的话，评估能否裁掉 claude provider |
| ├ `@getpaseo/server` | 33.9 MB | —— |
| ├ `node-pty` | 26.8 MB | **自带全平台 prebuilds，无需 VS Build Tools**（实测未触发 node-gyp 编译） |
| ├ `sherpa-onnx-win-x64` | 22 MB | 语音 / VAD 原生件，见下条 |
| └ 其余 | 约 93 MB | openai / protocol / esbuild-win32 等 |
| **npm ≥ 11 的 `allowScripts`** | 实测默认**拦截** `esbuild` 与 `node-pty` 的安装脚本 | 打包机上必须显式 approve（会写进 `package.json` 的 `allowScripts`），否则打出来的包缺原生件 |
| **本地语音模型** | 首次启动**自动后台下载** `parakeet-tdt-0.6b-v2-int8` + `kokoro-en-v0_19` | ⚠️ 无人值守的外网下载，小白版**必须在生成的 `config.json` 里先关掉语音** |
| `@openai/codex` CLI | 本机**已安装**（`C:\Users\Borg\bin\codex.cmd`）；早前“未安装”的结论是 PATH 被人为削空导致的误判 | 与桌面版仍是两个独立安装；客户端要负责体检 + 安装，并用绝对路径钉死（§8.3），别依赖 PATH |
| codex 登录 | ✅ 不需要验证 | 中转站路由下走 API key；**官方明确支持，与 ChatGPT 账号登录无区别**（2026-09-01 用户确认） |

体积提醒：小白版刚在 macOS 方案里把包体从 825 MB 压到 66 MB。
按现状直接塞进去是 **+495 MB**；裁掉 claude provider 后约 **+256 MB**，
再关语音去掉 sherpa 约 **+234 MB**。**必须把体积预算写进验收标准**，否则会原地退回去。

---

## 9. 与既有代码的接点

| 既有件 | 关系 |
|---|---|
| `CodexBinding/CodexConfigWriter` | 保持**唯一**写 `~/.codex` 的入口；Paseo 侧只读不写 |
| `Core/Services/CodexRouteGuard` | 继续守路由；新增"有活跃会话时不静默改写"分支（§5.2） |
| `Core/Services/ClientExitCoordinator`、`ClientShutdownCoordinator` | 插入"停 daemon"步骤，且**排在还原配置之前** |
| `Core/Services/CodexInstaller` | 扩展出"检测 / 安装 codex CLI"（当前面向桌面版安装包） |
| `Core/Services/RelayInjectionHost` | **不动**。它是 B 读法那条线（CDP 注入），与本方案并存不冲突 |
| `Core/Services/QRCoderRenderer` | 直接复用来渲染配对二维码 |
| `Core/Platform/SecureStorage`、`CodexBinding/ISnapshotProtector` | 存 daemon 口令 |

---

## 10. 验证清单（动手前必须做，🔴 为阻塞级）

| 编号 | 验证内容 | 级别 | 不成立的后果 |
|---|---|---|---|
| **V-1** | 干净 Windows（无系统 Node）上，私有 `node.exe` + 预构建 `node_modules` 能起 daemon 并响应 `health` | 🔴 阻塞 | ✅ **2026-09-01 通过，见 §10.1** |
| ~~**V-2**~~ | ~~**由 daemon 拉起**的 codex 在中转站路由下跑通完整一轮~~ | ⚪ 关闭 | **2026-09-01 关闭**：认证层官方明确支持 API key，与账号登录无区别。残留的**进程环境继承**问题（codex 继承 daemon 环境而非终端环境）留到 M1 首次集成时排查 |
| **V-3** | 任务管理器**强杀**客户端后，node 与 codex 进程全部被回收 | 🟡 高 | 出现"关了程序但手机还能操作"的安全事故 |
| **V-4** | relay 配对 → 手机连上 → 一轮问答端到端跑通 | 🟡 高 | 产品主路径不通 |
| **V-5** | 关掉语音后，daemon 能否在**不装** `sherpa-onnx-node` 的情况下启动 | 🟢 中 | 只影响体积 |
| **V-6** | `paseo agent import --provider codex <桌面版 session-id>` 能否解析，且**不扰动正在运行的桌面版会话** | 🟢 中 | 只影响"续聊桌面版对话"这个加分项 |

**阻塞级验证已全部清零**（V-1 通过、V-2 关闭、V-10a 通过），可以进入实现分期。

### 10.1 V-1 实测记录（2026-09-01，本机 DESKTOP-9GRMVU2）

**结论：通过。寄宿前提成立。**

做法（全部在 `C:\Work\pv1\`，与仓库隔离）：

1. 从 `nodejs.org` 取 **Node v24.20.0 LTS** 的 win-x64 **zip**（35.8 MB，直连 2 秒，无需走 7897 代理），
   解压成私有 `node\node.exe`，不进 PATH、不装到系统；
2. 用**这个私有 node 自带的 npm** 把 `@getpaseo/cli@0.7.0-beta.3` 装进独立目录（280 个包，22 秒）；
3. 启动脚本把 `PATH` 削成只剩 `C:\Windows\system32` 等系统目录（**验证过 `where node` 找不到任何 node**），
   再用私有 node 跑
   `@getpaseo/cli/dist/index.js daemon start --foreground --listen 127.0.0.1:6799 --home <私有 home> --no-relay`。

结果：

| 观测点 | 结果 |
|---|---|
| PATH 上有没有 node | `NOT FOUND`（干净盘条件成立） |
| daemon 启动 | 67 ms 完成 bootstrap，日志 `Server listening on http://127.0.0.1:6799` |
| `GET /api/health` | **200**，`{"status":"ok","timestamp":"2026-09-01T10:00:28.566Z"}` |
| 控制面可用性 | 用同一个私有 node 跑 `paseo provider ls --json --host 127.0.0.1:6799` 正常返回 6 个 provider 的 JSON |
| 原生模块 | `node-pty` 用自带 prebuilds（含 `conpty.dll`/`OpenConsole.exe`），**没有触发 node-gyp 编译**；`sherpa-onnx-win-x64` 的 `.dll`/`.node` 齐全 |
| 私有 home 写入 | `config.json`、`daemon-keypair.json`、`paseo.pid`、`server-id`、`daemon.log` 均落在指定目录，**没有碰 `~/.paseo`** |

顺带测出来的 5 件事（都已折进前面章节）：

1. **npm 11 默认拦安装脚本**（`allowScripts`）。第一次装完 `esbuild`、`node-pty` 的脚本没跑，
   必须 `npm install-scripts approve` 后才补齐原生件。**打包流程必须显式处理**，否则包是坏的。
2. **默认无鉴权**：`authRequired:false`。
3. **首次启动会自动后台下载本地语音模型**（parakeet + kokoro）。本机下载没成功（只留下 0 字节 `.tmp`），
   反而暴露了问题：小白机器上这是一次无人值守、可能长期挂着的外网下载，**必须默认关掉语音**。
4. ~~**`codex` CLI 本机不存在**~~ —— **2026-09-01 更正：这条结论是错的，且错因本身更有价值。**
   本机其实装了 codex CLI（`C:\Users\Borg\bin\codex.cmd`）。V-1 里 provider 全 `unavailable`，
   是因为**我把 PATH 削成了只剩系统目录**来模拟干净机器——daemon 于是找不到 codex。
   换成继承正常环境启动，同一个 daemon 报的是 `codex=Ready`。
   **真正被验证的是另一件事：provider 可用性完全取决于 daemon 继承到的环境**，
   这正好坐实了 §8.3 那条建议——用 `agents.providers.codex.command` 钉死绝对路径，
   别把可用性交给 PATH。（§0 的"桌面版 ≠ CLI"边界仍然成立，只是这台机器不构成它的证据。）
5. **进程链是 4 层**：`cli → supervisor-entrypoint → daemon-worker → terminal-worker`。
   把最顶层的 cli 进程 `Stop-Process -Force` 之后，6 秒内**整链消失**、health 立刻不通——
   但日志里**没有任何优雅关闭记录**，说明这是管道断裂导致的连锁猝死，不是契约保证的行为，
   而且它只覆盖"直接父进程死"这一种情况：真实场景里 WPF 是 node 的**祖父**进程，
   杀 WPF 不等于杀 node。**§5.1 的 Job Object 仍然必须做**，并且我们自己要先做一次有序 stop。

复现材料保留在 `C:\Work\pv1\`（`run-daemon.ps1`、`daemon.log`、`npm-install.log`），
共约 500 MB，V-2 做完可整个删掉。

---

## 11. 分期

| 阶段 | 内容 | 出口标准 |
|---|---|---|
| ~~**M0**~~（已完成） | ~~V-1~~ ✅、~~V-2~~（关闭）、~~V-10a~~ ✅ | **阻塞项清零，可直接进 M1** |
| **M1**（4–6 天） | `cofly-paseo-bridge` + `LanAi.RelayClient.Paseo` 窄契约 + 契约测试 | C# 能建会话、发消息、收到时间线事件 |
| **M2**（3–5 天） | `Paseo.Host`：进程笼、配置生成、健康探测、退避重启、日志；接入退出契约 | V-3 通过；退出顺序正确 |
| **M3**（3–4 天） | UI：远程开关、配对二维码、已配对设备列表、授权告知文案 | V-4 通过 |
| **M4**（4–6 天） | 打包：私有 node + node_modules、codex CLI 体检与安装、体积预算达标 | 干净机器一键装完可用 |
| **M5**（可选） | 切自建 relay；`agent import` 续聊桌面版会话（视 V-6） | —— |

---

## 12. 明确不做的事

- **不**手写 C# 版 Paseo 协议。
- **不**改 Paseo 源码（保持可直接升级；确需改动走 bridge 层适配）。
- **不**复用用户已有的 `~/.paseo`，也不接管用户自己装的 Paseo daemon。
- **不**把 Paseo 的调度 / 计费能力引进来——**中转站仍是唯一调度方**。
- **不**在 MVP 阶段重编手机 App。

---

## 13. 待拍板的三件事

1. **A 还是 B**（§0）。本方案按 A 写；要 B 我按注入层重新出。
2. **手机端形态**：先用官方 Paseo App（快，但是第三方品牌），还是一开始就自有品牌（慢很多）。
3. **授权范围默认值**：配对后手机默认能碰整机，还是只能碰用户指定的一个工作目录（§6.3）。
   小白版我建议后者。
