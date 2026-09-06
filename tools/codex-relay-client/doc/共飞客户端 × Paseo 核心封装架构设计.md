# 共飞直连客户端（小白版）× Paseo —— 核心封装架构设计

> 状态：**架构设计草案，未动代码**。整理于 2026-09-01。
> 前置文档：[`共飞客户端寄宿 Paseo 手机远程方案.md`](共飞客户端寄宿%20Paseo%20手机远程方案.md)
> （可行性、V-1 实测记录、体积实测、通知选型都在那一篇，本篇不重复）。
> 参考源码：`C:\Work\Git\paseo-main`（Paseo v0.7.0-beta.3）。
>
> ⚠️ **2026-09-01 拓扑更新**：手机端确定为**共飞 Paw 端**（不含 Paseo 逻辑），
> relay 与共飞服务端同机自建。拓扑以
> [`共飞 Paw 远程操作整体架构.md`](共飞%20Paw%20远程操作整体架构.md) 为准。
> 影响本篇两处：**§0 的结论**（"官方 App 下封装只是 UI 级"）与
> **§2 表格里三行 🔴**——在新拓扑下它们从"不可强制"升级为"**不可达**"，
> 因为链路上唯一的 Paseo 客户端是我们自己写的适配层。
> 本篇其余内容（能力矩阵、爆炸半径、窄契约、生命周期状态机、配置生成、工程结构）继续有效，
> 且窄契约现在有**两个消费者**（PC 的 C# + 服务端适配层）。

---

## 0. 一句话结论：封装边界能不能"硬"，取决于手机端是谁的

设计过程中查到两条互相咬合的事实，它们直接决定这篇架构怎么写：

1. **每个通过鉴权的会话都是 owner。**
   `packages/server/src/server/websocket-server.ts:502` 把 `OWNER_SESSION_ADMISSION` 定死为
   `permissions: OWNER_PERMISSIONS`（即 `DAEMON_PERMISSIONS` 全集）。
   Paseo 有一套完整的权限模型（`authorization/operation-permissions.ts`，
   `create_terminal_request` 需要 `workspace.write` 等），但**目前只有 Hub 走缩权路径**，
   本地口令与 relay 客户端一律 owner。
2. **客户端选的 mode 会盖掉 `config.toml` 的沙箱默认值。**
   `codex-app-server-agent.ts:4009-4019`：`sandbox_mode` 优先取 provider options，
   其次取 mode preset；两者都没有时才不下发、由 codex 自己回落。

合起来是一句不太好听但必须先说的话：

> **只要手机端是官方 Paseo App，"只封装核心"就只是界面级的——
> 手机拿的是 owner 权限，可以开终端、读写文件、自己挑更宽的沙箱模式。**

因此本设计的取向是：

- **窄契约（§5）是产品边界，不是官方 App。**
- **MVP 阶段 relay 保持关闭**，官方 App 只当**本机/局域网的调试与预览客户端**；
- **出货形态的手机端是我们自己的界面**（走我们的服务端 + 窄契约），
  那时候"只封装核心"才是**可强制**的。
- 如果产品上决定第一天就开 relay + 官方 App，可以，但文档必须写明：
  那个配置下封装是 UI 级的，安全边界靠 §3 的爆炸半径和用户告知兜底。

---

## 1. 本篇范围

| 在范围内 | 不在范围内 |
|---|---|
| "核心"的定义与能力分级（§2） | 可行性、体积、V-1 结论（见前置文档） |
| 强制点分级：可强制 / 仅默认值 / 不可强制（§2） | 手机端 UI 设计 |
| 爆炸半径与工作目录策略（§3） | 中转站后端改动（预期为零） |
| 分层、依赖规则、工程结构（§4、§11） | B 读法（CDP 远程操控桌面版） |
| 窄契约 v1：操作、错误模型、版本协商、背压（§5） | 计费与配额（中转站已有） |
| 生命周期状态机与关键时序（§6、§7） | |
| 配置生成规范与 `~/.codex` 写入者升级（§8、§9） | |

---

## 2. 核心能力矩阵（本篇的主交付物）

"核心" = **手机远程用 Codex 干活**所必需的最小集合。其余一律关掉或不暴露。

判定"关得掉吗"用三个等级，**不允许模糊**：

- **🟢 可强制**：daemon 层面就没有这个东西，客户端想用也用不了。
- **🟡 仅默认值**：我们能设一个地板，但客户端显式指定时会被盖掉。
- **🔴 不可强制**：只要会话是 owner 就拦不住，只能靠 UI 不暴露 + 用户告知。

| 能力 | 是否核心 | 关闭 / 收敛手段 | 能否强制 |
|---|---|---|---|
| Codex 会话（建、续、停、归档） | ✅ 核心 | —— | —— |
| 会话时间线订阅 | ✅ 核心 | —— | —— |
| 关注通知（`agent_attention_required`） | ✅ 核心 | —— | —— |
| relay 配对 / 解绑 | ✅ 核心 | 默认 `daemon.relay.enabled=false`，配对才开 | 🟢 可强制 |
| Claude / Copilot / OpenCode / Pi / OMP | ❌ | `agents.providers.<id>.enabled=false` | 🟢 可强制（同时省下 239 MB，见前置文档 §8） |
| 语音听写 / 语音模式 | ❌ | `features.dictation.enabled=false`、`features.voiceMode.enabled=false` | 🟢 可强制（同时避免首启自动下模型） |
| 自带 Web UI | ❌（第三阶段才用） | `features.webUi.enabled=false` | 🟢 可强制 |
| MCP 端点 / 向 agent 注入 MCP | ❌ | `daemon.mcp.enabled=false`、`injectIntoAgents=false`（等价 CLI `--no-mcp --no-inject-mcp`） | 🟢 可强制 |
| codex 可执行体来源 | ✅ 核心（要钉死） | `agents.providers.codex.command` 指向我们私有安装的 codex | 🟢 可强制（顺带解决"小白 PATH 里没有 codex"，见 §8.3） |
| provider 工具裁剪（如 WebSearch） | ⚠️ 视策略 | `agents.providers.codex.disallowedTools` | 🟢 可强制 |
| CORS / 允许的 Origin | ❌ | `daemon.cors.allowedOrigins` 清空（默认会写 `https://app.paseo.sh`） | 🟢 可强制 |
| 监听地址 | ✅ 核心 | 固定 `127.0.0.1:<随机端口>` | 🟢 可强制 |
| 口令鉴权 | ✅ 核心 | `PASEO_PASSWORD` 注入（默认 `authRequired:false`，见前置文档 §10.1） | 🟢 可强制 |
| codex 沙箱模式 / 审批策略 | ✅ 核心 | `~/.codex/config.toml` 的 `sandbox_mode` / `approval_policy` | 🟡 **仅默认值**——客户端选了 mode 就被盖掉（`codex-app-server-agent.ts:4009-4019`）；且"config.toml 对 app-server 会话是否生效"**待验 V-7** |
| 终端（创建 / 输入 / 抓屏） | ❌ | 无配置开关 | 🔴 **不可强制**：`create_terminal_request` 只要 `workspace.write`，而会话是 owner |
| 文件读写 / 上传下载 | ❌ | 无配置开关 | 🔴 不可强制，同上 |
| workspace / worktree / git 检出与 PR | ❌ | 无配置开关 | 🔴 不可强制，同上 |
| 计划任务（schedule）、插件、Hub | ❌ | 插件有全局开关；schedule/Hub 不主动配置 | 🟡 部分可关，但同属 owner 面 |

**读法**：🟢 那些行是我们生成配置时就该定死的，写进 §8 的模板；
🟡 只能当"地板"，不能当"天花板"；🔴 那三行是 §0 结论的来源，
也是"出货形态必须是我们自己的手机端"的全部理由。

---

## 3. 爆炸半径：工作目录是唯一在两种形态下都成立的收敛

无论手机端是官方 App 还是我们自己的界面，**agent 干活的 `cwd` 是我们给的**。
所以把爆炸半径钉在一个目录上，是唯一"在 🔴 那几行成立的前提下依然有效"的收敛手段。

设计：

- **默认工作根**：`%LOCALAPPDATA%\LanAi.RelayClient\workspaces\`，由客户端在首次开启远程时创建。
  **不是** `%USERPROFILE%`，**不是**整盘，**不是**用户的代码仓库目录。
- **一个会话一个子目录**：`workspaces\<会话短 id>\`，会话归档后保留（用户可能要取产物），
  由客户端提供"清理"入口，不自动删。
- **用户要用真实项目目录时**：必须是一次**显式的、逐目录的授权**——
  在 PC 端选目录、确认告知文案后写入白名单，白名单持久化在客户端自己的配置里，
  由客户端在建会话时决定允许哪个 `cwd`。**不做"记住并默认全盘"**。
- **沙箱地板**：`~/.codex/config.toml` 写 `sandbox_mode = "workspace-write"`、
  `approval_policy = "on-request"`，`writable_roots` 指向上面的工作根（🟡 级，见 §2 与 V-7）。

推论（要写进代码注释）：`cwd` 的取值**只能由 C# 侧决定并经窄契约下发**，
bridge 不得接受任意路径——否则这条收敛就形同虚设。

---

## 4. 分层与依赖规则

```
┌──────────────────────────────────────────────────────────────┐
│ UI 层  LanAi.RelayClient（WPF） / LanAi.RelayClient.App（Avalonia）│
│  远程开关 · 配对二维码 · 会话列表 · 通知气泡 · 目录授权对话框        │
└───────────────┬──────────────────────────────────────────────┘
                │ 只依赖 ViewModel / 服务接口
┌───────────────▼──────────────────────────────────────────────┐
│ 编排层  LanAi.RelayClient.Core（已有）                          │
│  会话态 · 退出协调 · 路由守卫 · 通知呈现 · 目录白名单             │
└───────┬───────────────────────────────┬──────────────────────┘
        │                               │
┌───────▼────────────────┐   ┌──────────▼─────────────────────┐
│ LanAi.RelayClient.Paseo │   │ LanAi.RelayClient.Paseo.Host    │
│  窄契约客户端（纯数据）    │   │  进程监管 / 配置生成 / 进程笼      │
│  net8.0，零 NuGet，无 UI  │   │  net8.0，平台代码分 Windows/MacOS │
└───────┬────────────────┘   └──────────┬─────────────────────┘
        │ 命名管道 JSONL                   │ spawn（私有 node.exe）
┌───────▼───────────────────────────────▼─────────────────────┐
│ cofly-paseo-bridge（Node，锁定 @getpaseo/client 精确版本）       │
│  唯一允许 import Paseo 类型的地方                               │
└───────────────┬─────────────────────────────────────────────┘
                │ WebSocket 127.0.0.1
        ┌───────▼────────┐   spawn   ┌──────────────┐
        │ paseo daemon    ├──────────►│ codex app-server │
        └─────────────────┘           └──────────────┘
```

**依赖规则（评审时按这几条卡）：**

1. `LanAi.RelayClient.Paseo` **零 NuGet、不引用 WPF/Avalonia**——
   沿用现有 `LanAi.RelayClient.Server` 已经证明可行的形态。
2. 窄客户端 **不得引用** Host。
   > ✅ **2026-09-01 实现时修订**：原文写的是"反向也不得引用"，实现时改成
   > **允许 Host → Adapter**。要保住的其实只有一半——窄客户端必须能独立使用，
   > 因为服务端那份适配层连的是它没拉起过的 bridge，根本没有进程监管。
   > 而禁止 Host 依赖 Adapter 什么也换不来：每个消费者都得重写
   > "建管道、发 token、拉起 bridge、握手" 那三十行，正是这层要消灭的重复。
   > 现在 `PaseoRuntime.StartAsync` 一行返回连好的客户端。
3. **任何 C# 工程都不得出现 Paseo 的 schema 类型**。
   契约里的字段是我们自己定义的 record，与 Paseo 的 zod 类型同名也只是巧合。
4. bridge **不做业务判断**：不选目录、不决定沙箱、不决定什么时候起会话。
   它只是"把窄契约翻译成 Paseo 调用"。业务在 Core。
5. `CodexBinding` 仍是**唯一**写 `~/.codex` 的地方（§9）。

---

## 5. 窄契约 v1

### 5.1 传输与帧

- **命名管道**（Windows：`\\.\pipe\lanai-paseo-<installId>`；macOS：Unix domain socket），
  ACL / 权限限定当前用户。**不用回环 TCP**——同机任意进程都能连。
- **JSON Lines**，UTF-8，一行一帧，`\n` 分隔。
- 三种帧：`req`（C#→bridge）、`res`（bridge→C#，带 `id`）、`evt`（bridge→C#，无 `id`）。

### 5.2 握手与版本协商

宿主启动 bridge 时经环境变量传入一次性 `token` 与期望的契约版本。
首帧必须是：

```json
{"t":"req","id":"1","op":"hello","token":"<一次性>","contract":"1"}
```

bridge 回：

```json
{"t":"res","id":"1","ok":true,"contract":"1","paseo":"0.7.0-beta.3","daemon":"running"}
```

规则：**契约主版本不一致时 bridge 直接拒绝并退出**，由宿主呈现"客户端需要升级"。
Paseo 版本只作为诊断信息回传，**不参与判定**——这正是这层存在的意义。

### 5.3 操作集（v1 冻结）

| op | 入参 | 出参 / 事件 |
|---|---|---|
| `hello` | token, contract | 见上 |
| `health` | —— | `{daemon, listen, providerCodex}` |
| `agents.list` | `{limit?, cursor?}` | 会话摘要数组 |
| `agents.create` | `{model, cwdKey, prompt}` —— **`cwdKey` 是白名单键，不是路径**（§3） | `{agentId}` |
| `agents.send` | `{agentId, text}` | `{ok}` |
| `agents.stop` | `{agentId}` | `{ok}` |
| `agents.archive` | `{agentId}` | `{ok}` |
| `timeline.subscribe` | `{agentId, from?}` | `evt: timeline` |
| `timeline.unsubscribe` | `{agentId}` | `{ok}` |
| `notifications.subscribe` | —— | `evt: attention`（`finished`/`error`/`permission`） |
| `relay.status` | —— | `{enabled, pairedDevices}` |
| `relay.pair` | —— | `{pairingUrl, expiresAt}` |
| `relay.disable` | —— | `{ok}` |

不在表里的一律不做：终端、文件、git、schedule、插件、语音。
**加操作要改契约主版本或次版本，不能"顺手加个字段"。**

### 5.4 错误模型（C# 侧必须分开渲染的四种）

| code | 含义 | C# 侧期望表现 |
|---|---|---|
| `TRANSPORT_DOWN` | 管道断了 / bridge 进程没了 | 静默重连 + 退避；连续失败才提示 |
| `DAEMON_DOWN` | bridge 活着但连不上 daemon | 触发宿主的 daemon 重启流程 |
| `CODEX_MISSING` | provider 不可用（codex 没装 / 没登录） | 引导安装与登录，**不要**报"网络错误" |
| `PERMISSION_REQUIRED` | 会话在等人点头 | 弹通知并引导进入会话，不是错误弹窗 |
| `CONTRACT_MISMATCH` | 版本不匹配 | 提示升级客户端 |
| `INTERNAL` | 其余 | 记日志 + 通用提示 |

把这四种混成一个"操作失败"是最常见的坏实现——`CODEX_MISSING` 在小白机器上是**最高频**的一种
（V-1 实测：装了官方桌面版但 PATH 里没有 codex，provider 全是 `unavailable`）。

### 5.5 背压

一轮 codex 输出的事件量可以很大。约定：

- bridge 对同一 `agentId` 的时间线事件做**时间窗合并**（默认 100 ms），只推增量；
- 管道写入拥塞时**丢中间态、保留末态**（时间线本身可用 `timeline.refetch` 补齐——
  这与 Paseo 自己的"实时流求即时、`fetch_agent_timeline_request` 求权威"一致）；
- `notifications` 事件**永不丢**，与时间线走不同的合并策略。

---

## 6. 关键时序

### 6.1 开启远程（首次）

```
用户点"开启手机远程"
  → Core 校验已登录 + 已选工作根
  → Core 弹授权告知（终端/文件权限说明）  ← 用户必须显式同意
  → Host 生成随机口令与端口，写私有 PASEO_HOME/config.json（§8）
  → Host 创建 Job Object，spawn 私有 node（daemon + bridge）
  → Paseo.hello / health 通过
  → Core 调 relay.pair → 拿到配对链接 → QRCoderRenderer 出码
  → 手机扫码 → relay.status 显示已配对设备 +1
  → 托盘常驻"远程已开启"
```

### 6.2 手机发起一轮

```
手机 → relay → daemon → codex app-server → 一轮输出
                    ↓
        agent_attention_required(finished)
                    ↓ bridge 转发
        Core → 托盘通知（PC 端）；手机端由其客户端自行提示
```

### 6.3 退出（顺序不能反）

```
用户真正退出
  → Core 广播"即将停止"，等待活跃会话到达安全点（有上限，超时则强停）
  → Paseo.Host 有序停 daemon（先 stop，再等，再 Job Object 兜底）
  → 确认 daemon 已退出
  → CodexBinding 还原 ~/.codex 的开机前快照      ← 必须在 daemon 停掉之后
  → 进程退出
```

前置文档 §10.1 实测过：强杀顶层进程时**整链猝死且没有任何优雅关闭日志**。
所以"有序 stop"必须由我们自己做，Job Object 只是兜底。

---

## 7. 状态机

**daemon 生命周期**（`Paseo.Host` 持有）：

```
Stopped ──start──► Starting ──health ok──► Running
   ▲                  │                      │
   │                  └──health fail/超时────┤
   │                                          │
   └──Stopped◄──Stopping◄──stop───────────────┘
                    ▲
      Crashed ──退避重启──┘      （连续 N 次失败 → Faulted，停止自动重启并提示）
```

约束：
- `Starting` 有硬超时（建议 30 s），超时按 `Crashed` 处理；
- 退避：1s / 2s / 5s / 15s / 30s，封顶；**连续 5 次失败进 `Faulted`**，不再自动重启；
- 未登录、退出登录、真正退出 → 一律走 `Stopping`；
- `Faulted` 时 UI 必须能一键"导出日志"（私有 home 下的 `daemon.log`）。

**远程开关状态**：`Off → Pairing → On(N 台) → Off`。
`Off` 时 `daemon.relay.enabled=false`，并且**解绑所有设备**。

---

## 8. 配置生成规范

### 8.1 我们生成的 `config.json`（私有 PASEO_HOME）

```json
{
  "version": 1,
  "daemon": {
    "listen": "127.0.0.1:<随机端口>",
    "cors": { "allowedOrigins": [] },
    "relay": { "enabled": false },
    "mcp": { "enabled": false, "injectIntoAgents": false }
  },
  "features": {
    "dictation": { "enabled": false },
    "voiceMode": { "enabled": false },
    "webUi": { "enabled": false }
  },
  "agents": {
    "providers": {
      "claude":   { "enabled": false },
      "copilot":  { "enabled": false },
      "opencode": { "enabled": false },
      "pi":       { "enabled": false },
      "codex":    { "command": ["<私有安装>/codex.exe"] }
    }
  }
}
```

要点：

- **口令不写进这个文件。** `daemon.auth.password` 的 schema 是 **bcrypt 摘要**
  （`^\$2[aby]\$\d{2}\$[./A-Za-z0-9]{53}$`）。C# 侧不引 NuGet 就没有 bcrypt，
  所以走 **`PASEO_PASSWORD` 环境变量**（`config.ts:478` 读取，且属于 override 控制路径），
  由 `Paseo.Host` 生成随机口令注入 daemon 与 bridge 两侧。
  ⚠️ 顺带避开一个已知坑：bcrypt 串里的 `$` 在 PowerShell 里会被吃掉。
- `app.baseUrl` 与 `cors.allowedOrigins` 默认会被 Paseo 写成 `https://app.paseo.sh`（V-1 实测），
  生成时显式清掉。
- 这份文件由 `Paseo.Host` **每次启动前重写**，不做增量合并——
  用户手改私有 home 不是支持路径。

### 8.2 `~/.codex/config.toml` 上我们要加的键

在现有路由键（`model_provider`、`[model_providers.sub2api]`）之外，追加沙箱地板：

```toml
sandbox_mode = "workspace-write"
approval_policy = "on-request"
```

⚠️ 🟡 级：只是地板，不是天花板（§2）。**且是否对 app-server 会话生效待验（V-7）**。

### 8.3 provider 钉死：小白不再需要 PATH 上有 codex

`agents.providers.codex.command` 直接指向我们私有安装的 codex 可执行体。
这条把前置文档 §8 里"用户要自己装 CLI 并加进 PATH"的风险**从阻塞降成安装步骤**：
客户端把 codex 装到自己的目录里，Paseo 按绝对路径拉起，用户全程无感。
（V-1 实测里 provider 全 `unavailable`，正是因为没有这条。）

---

## 9. `CodexConfigWriter` 的定位升级（容易静默回归的一处）

一旦沙箱地板写进 `config.toml`，`CodexBinding/CodexConfigWriter` 就**不再只是路由写入器，
而是一个安全相关组件**。随之而来的两条硬要求：

1. `CodexRouteGuard` 检测到配置被官方客户端整体重写后，**恢复时必须把沙箱键一起补回来**，
   不能只补 `base_url` / `model_provider`。
   （官方客户端登录后整体重写、丢掉不认识的键，这个行为在注入方案文档里已经实测过。）
2. **daemon 有活跃会话时不得静默改写 `config.toml`**——正在跑的 codex 会读到半新半旧的状态。
   改写要么延后到会话结束，要么提示用户。

建议在 `CodexConfigWriter` 上加一个显式的 `SecurityRelevantKeys` 常量表，
并让守卫的对比逻辑以它为准，避免以后有人加键时忘了同步守卫。

---

## 10. 失败与降级

| 场景 | 表现 | 处理 |
|---|---|---|
| daemon 起不来（端口占用/文件损坏） | `health` 超时 | 换端口重试一次 → 重建私有 home → 仍失败进 `Faulted` + 导出日志 |
| bridge 崩溃 | 管道断 | 宿主重启 bridge（daemon 不动）；会话不受影响 |
| daemon 崩溃 | bridge 报 `DAEMON_DOWN` | 退避重启；重启后自动重订阅时间线与通知 |
| codex 缺失 / 未登录 | `CODEX_MISSING` | 引导安装/登录，**不当网络错误报** |
| relay 断线 | 手机连不上，PC 侧无影响 | UI 显示"远程连接中断"，daemon 自行重连 |
| 中转站会话过期 | 路由 key 失效 | 停 daemon + 关 relay（§7），提示重新登录 |
| 磁盘写满 | daemon 日志/会话写失败 | 进 `Faulted`，明确提示磁盘原因 |

---

## 11. 工程结构与测试策略

```
tools/codex-relay-client/
├─ src/
│  ├─ LanAi.RelayClient.Paseo/          # 窄契约客户端（net8.0, 零 NuGet, 无 UI）
│  │   ├─ Contract/                     # record 定义 + JSON 源生成上下文
│  │   ├─ PipeTransport.cs
│  │   └─ PaseoNarrowClient.cs
│  ├─ LanAi.RelayClient.Paseo.Host/     # 进程监管
│  │   ├─ DaemonConfigComposer.cs       # §8.1 模板生成
│  │   ├─ DaemonSupervisor.cs           # §7 状态机
│  │   └─ Platform/Windows/JobObjectCage.cs
│  └─ （现有工程不动）
├─ bridge/cofly-paseo-bridge/           # Node 边车（TS）
└─ tests/
   ├─ LanAi.RelayClient.Paseo.Tests/        # 契约测试（对着录制的真实 bridge 输出）
   └─ LanAi.RelayClient.Paseo.Host.Tests/   # 状态机 / 配置生成 / 进程笼
```

测试策略，按现有仓库的习惯（真实响应核对、可替身化的边界）：

1. **契约测试**用**录制的真实 bridge 输出**当基线——
   与 `LanAi.RelayClient.Server.Tests` 用真实服务器响应核对的做法一致。
   这是唯一能在 Paseo 升级时报警的机制。
2. **状态机测试**用假的 `IProcessCage` + 假时钟，覆盖退避、`Faulted`、有序退出顺序。
3. **进程笼测试**必须是**强杀**（模拟任务管理器），不能只测优雅退出。
4. **配置生成测试**逐键断言 §2 里所有 🟢 行都真的写进去了——
   这张表一旦漏一行，就是一个静默放开的能力。
5. bridge 侧至少要有一条 e2e：起真 daemon → 建会话 → 收到 attention 事件。

---

## 12. 升级策略

- bridge 对 `@getpaseo/client` 用**精确版本**（不是 `^`），随客户端版本一起走。
- 升级 Paseo 的标准动作：改 bridge 的依赖版本 → 跑契约测试 →
  **C# 侧一行不改**。契约测试红了，才说明这次升级触到了我们用的面。
- 契约版本独立于 Paseo 版本，`major.minor`；
  加操作 → minor，改语义/删字段 → major，major 不匹配直接拒绝握手（§5.2）。
- 私有 node 与 codex 的版本也钉在客户端版本里，**不跟随用户机器**。

---

## 13. 开放问题与新增验证项

| 编号 | 内容 | 级别 |
|---|---|---|
| **V-7** | `~/.codex/config.toml` 里的 `sandbox_mode`/`approval_policy` 对 **app-server 会话**是否真的生效？（目前只核对了 Paseo 侧"不下发就回落"的逻辑，codex 侧是推断） | 🟡 影响 §2 表格里那一行的等级。**V-2 已关闭，所以这条要单独做**——装上 codex 后跑一轮 app-server 会话，看 `config.toml` 的沙箱键有没有生效 |
| **V-8** | provider 钉死（`agents.providers.codex.command` 给绝对路径）能否让 PATH 上没有 codex 的机器正常起会话 | 🟡 §8.3 的前提 |
| Q-1 | 出货形态的手机端：官方 App（快、但封装是 UI 级）还是自研界面（慢、但封装可强制）？ | **产品决策，见 §0** |
| Q-2 | 用户想在真实项目目录里干活时，授权粒度是"每目录一次"还是"每次会话一次"？ | 产品决策，见 §3 |
| Q-3 | 是否要把 Paseo 的 owner 权限问题反馈给上游 / 或在自研手机端阶段考虑给 daemon 打一个缩权补丁？ | 打补丁与"不改 Paseo 源码"的原则冲突，需权衡 |
