# Codex 官方客户端注入增强 —— 设计草稿

> 状态：草稿 v0.1（2026-07-26）。核心可行性已查证，落地细节待实机验证。
> 参考对象：[pikapikaspeedup/Codex_Plus_Pro](https://github.com/pikapikaspeedup/Codex_Plus_Pro)（macOS 版官方客户端注入增强器）。

## 1. 背景与定位

共飞工作台当前是"自有 UI"路线：自带聊天引擎、账号中心、切换服务。但官方 Codex
桌面客户端的 UI 与功能迭代速度远超自研，用户日常主战场应是**官方客户端**，
共飞自有 UI 退为补充。

本方案让共飞以**注入增强**的方式进入官方客户端：

1. 在官方 UI 内显示共飞基础数据（token 用量、账号池状态、官方账号限额状态）；
2. 监测到官方账号**用量不足 / 触发时间窗限额**时，弹出提醒，引导用户一键切换到
   共飞中转站继续工作；
3. 切换的硬性前提：**聊天记录与记忆必须完整保留**。

## 2. 参考对象分析（Codex_Plus_Pro 机制）

Codex_Plus_Pro 的做法（macOS，思路可全套移植到 Windows）：

- 自身是一个 **launcher**：以调试端口方式启动官方 Electron 应用，然后通过
  CDP（Chrome DevTools Protocol，仅连 `127.0.0.1`）注入 UI；
- 不修改官方应用文件、账户数据、会话记录；
- 功能：主题皮肤、模型/思考强度快捷栏、画中画多任务监控、桌宠通知；
- 提供"打开原版"启动器一键退出注入；
- 公认弱点：官方改 DOM/内部结构即需适配 → 注入层必须做成"挂了也不影响官方
  应用正常使用"的纯增强。

它只做 UI 层增强；共飞方案的差异化在于**打通本地中转的"续命切换"**，这是它
做不到的。

## 3. 已验证的技术事实

### 3.1 Windows 官方客户端是 Electron（可注入）

- Windows 版 Codex 桌面应用基于 Electron，经 Microsoft Store 以 AppX 分发；
  多个官方 issue 佐证（如 [#25188](https://github.com/openai/codex/issues/25188)、
  [#25231](https://github.com/openai/codex/issues/25231)）。
- Electron/Chromium 原生支持 `--remote-debugging-port`，CDP 注入路线与
  Codex_Plus_Pro 完全一致。

### 3.2 官方客户端读取本地 `%USERPROFILE%\.codex\`（可切换）

- 桌面应用遵循 `CODEX_HOME`（默认 `~/.codex`）下的 `config.toml` / `auth.json`，
  支持 `openai_base_url` 指向本地代理
  （[官方 config-advanced 文档](https://developers.openai.com/codex/config-advanced)；
  [#24457](https://github.com/openai/codex/issues/24457) 证实桌面版会把请求发给
  config.toml 配置的本地自定义 provider）。
- **这正是工作台 SwitchService 已经在改写的文件** —— "切换到共飞中转"复用现有
  切换机制，无需新协议。

### 3.3 会话与记忆都在本地（切换后天然保留）

本机 `~/.codex` 实测结构（2026-07-26）：

| 内容 | 位置 | 切换 provider 是否受影响 |
|---|---|---|
| 会话记录 | `~/.codex/sessions/<年>/...`（rollout 文件） | 否，纯本地文件 |
| 记忆 | `~/.codex/memories_1.sqlite`（另有 goals/logs sqlite） | 否 |
| 项目记忆/规则 | `AGENTS.md`（全局 + 各项目） | 否 |
| 接入配置 | `config.toml` / `auth.json` | **是 —— 切换只改这两个** |

结论：**历史与记忆不需要"同步"，因为它们从未离开本地**。切换 = 只动
`config.toml`/`auth.json`，会话与记忆文件原地不动。

### 3.4 本机安装现状

- Codex CLI 引擎已安装：`%LOCALAPPDATA%\OpenAI\Codex\bin\<hash>\codex.exe`。
- **官方 Electron 桌面应用已安装**：AppX 包 `OpenAI.Codex`，版本 26.707.9981.0，
  AUMID `OpenAI.Codex_2p2nqsd0c76g0!App`，主入口 `app/ChatGPT.exe`（进程名
  `ChatGPT`，Electron 多进程），底层 Chromium 150。安装根：
  `C:\Program Files\WindowsApps\OpenAI.Codex_26.707.9981.0_x64__2p2nqsd0c76g0`。

### 3.5 ✅ V1 已实机验证通过（2026-07-26）

**CDP 注入路线在官方 Windows Codex 桌面应用上完整成立。**

- **启动方式**（关键坑，已解决）：
  - ✗ `Start-Process app\ChatGPT.exe` → Access Denied（WindowsApps 目录受保护）；
  - ✗ `Invoke-CommandInDesktopPackage -Args ...` → 本机不拉起进程（不可靠）；
  - ✓ **COM `IApplicationActivationManager::ActivateApplication(AUMID, "--remote-debugging-port=9777")`**
    → 成功，返回 pid，这是 MSIX 应用带命令行参数启动的可靠方式。
    脚本见 `scratchpad/launch-codex-debug.ps1`（C# Add-Type 实现，PowerShell 5.1
    直接转接口会失败，须在 C# 内完成 QueryInterface）。
  - 注意 Electron 单实例锁：带调试端口启动前须先 `Stop-Process ChatGPT`，否则
    新参数被既有实例吞掉。
- **CDP 连通**：`http://127.0.0.1:9777/json/version` 返回 Chrome/150；
  `/json/list` 有一个 `page` target（title `Codex`，`app://-/index.html`）——
  即可注入的渲染进程。
- **读能力**：对 page target 建 WebSocket，`Runtime.evaluate` 成功读取 DOM
  （title / body innerText / 正则匹配限额关键词）——限额哨兵的 DOM 检测路可行。
- **写能力**：`Runtime.evaluate` 成功向页面注入自定义 DOM 元素并返回 true——
  状态条注入可行。脚本见 `scratchpad/cdp-probe.ps1`（纯 .NET `ClientWebSocket`，
  无第三方依赖，可直接移植到 `AiSwitch.Injection`）。

### 3.6 ⚠️ 重大发现：官方登录会整体重写 config.toml 并丢弃中转配置（2026-07-26 实测）

**这是本方案最关键的风险，直接推翻"改了 config.toml 就一直生效"的假设。**

实测时间线（同一台机器、同一天）：

| 时间 | 事件 | config.toml |
|---|---|---|
| 18:59 | 注入验证前 | 5665 字节，含 `model_provider = "sub2api"` + `[model_providers.sub2api]` |
| 19:16 | 用户完成官方 ChatGPT 登录 | `auth.json` 93 → 4224 字节（写入 OAuth 令牌） |
| 19:18 | 应用回写配置 | **2803 字节，`sub2api` 相关配置全部消失** |

**被丢弃的键**（不止中转配置）：`model_provider`、`[model_providers.sub2api]`、
`model = "gpt-5.5"`、`model_reasoning_effort`、`model_context_window`、
`model_auto_compact_token_limit`、约 20 个 `[projects.'...']`、
`[windows] sandbox = "elevated"`、`[env]`、`[agents]`、`[tui.*]`、
`[hooks.state]` 及 4 条 hook 记录、`browser-use` 插件项。

**被保留的键**：`notify`、`[desktop]`、`[mcp_servers.*]`（node_repl 与 omx_*）、
`[marketplaces.openai-bundled]`、`[plugins.visualize/browser]`、`[features]`。

即：**官方桌面应用按自己的键 schema 重写整个文件，schema 外的键（含自定义
provider）直接丢弃**。存活的都是桌面端自己的键（`[desktop]`、`[mcp_servers.*]`、
`[plugins]`、`[marketplaces]`、`[features]`、`notify`），丢弃的都是 CLI 侧的键。

> ⚠️ 注意归因边界：**登录只是我们观测到的触发时机，不能断定是唯一触发点**。
> 结论应表述为"桌面端任何一次配置回写都可能按自身 schema 清理文件"。因此
> 不要只做"登录事件监听器"——必须是持续校验（见下方修正 1），否则会漏掉其他
> 写入路径。

SwitchService 的 `ReadPreservedCodexSections` 是单向的——我们写时会
保留用户段落，但官方写时不会保留我们的。

**对设计的四点修正：**

1. **切换不是"一次性写入"，而需持续保活**：注入层必须把路由状态当作
   *可观测量*而非既定事实，周期性校验 `config.toml` 是否仍指向共飞；
2. **必须监听登录/登出事件**：这是已知的 clobber 触发点，事件后立即复查路由，
   被覆盖则提示用户一键重新应用；
3. **切换前强制快照**：`BackupCurrentFiles` 必须在每次切换前落盘（本机实测
   `backups` 目录为空 → 用户此次丢失的 projects/sandbox/hooks 等设置无从恢复），
   快照应包含完整 config.toml 而非仅关注字段；
   > 与 AGENTS.md 的关系（**勿误删此机制**）：AGENTS.md 的
   > 「Release Installation Policy」禁止的是*打包/安装/升级时备份应用安装目录*，
   > 与此处「切换前快照用户 CLI 配置文件」是两件不同的事，后者不受该条约束，
   > 且 `BackupCurrentFiles` 早已实现。
4. **副作用提示**：提醒卡片需说明"官方重新登录会重置路由"，避免用户以为
   切换失效是共飞的问题。

**恢复路径**：`~/ai-switch-gui/profiles.json`（7/18，未受影响）仍保有
"本机中转" 档位（Codex BaseUrl `http://127.0.0.1:8080/v1`），从工作台重新执行
一次切换即可恢复路由；但上表其余被丢弃的用户设置无备份可恢复。

### 3.7 ✅ V4 已实机验证（2026-07-26）：限额哨兵不能用 Network 拦截，改走渲染进程状态

**否定结论（重要）：CDP 页面 target 的 Network 域看不到官方 API 流量。**

实测：`Network.enable` 后触发一次 `Page.reload`，捕获到 772 条
`requestWillBeSent`——**全部是 `app://-/assets/*` 本地资源**，没有任何
`api.openai.com` / `chatgpt.com` 请求。即 API 调用由 **Electron 主进程**发出，
页面 target 拦不到，因此原设计"用 Network 域读 429 / rate-limit 响应头"**不可行**。
（空闲期抓 35 秒得 0 条响应，一度误判为"应用空闲"；用 reload 才区分开
"域没通"与"窗口内无流量"。）

**正面结论（注意证据强度）：限额状态存在于渲染进程侧，具备可读的接入点。**
⚠️ 需要说清楚的是：**我们尚未实际观测到限额发生时的状态**（无法人为触发限额）。
下面的资源包名与静态字符串是**强证据**，但"读到的字段具体长什么样"仍待
真实限额时验证。M2 不应把它当已测事实。资源包名如下：

- `use-rate-limit-*.js`（限额状态 hook）
- `rate-limit-reset-modal-*.js`、`rate-limit-reset-redemption-*.{js,css}`
- `plan-management-state-*.js`、`upgrade-plan-dialog-launcher-*.js`
- `use-codex-cloud-access-*.js`、`codex-micro-signals-*.js`

可用的注入侧钩子（实测存在）：

| 钩子 | 实测内容 | 用途与稳健性 |
|---|---|---|
| DOM + MutationObserver | 限额时会出现 `rate-limit-reset-modal`（资源包证实其存在） | **首选**：最稳，只依赖"有个限额弹窗"这一事实 |
| `window.electronBridge` | 窄接口，键为 `sendMessageFromView`／`subscribeToWorkerMessages`／**`getSharedObjectSnapshotValue`**／`getFastModeRolloutMetrics` 等；**无任何 rate/limit/usage/account/auth 键** | 主进程→渲染进程的共享状态通道，限额数据很可能经此复制；作为增强路径 |
| `window.__codexRoot._internalRoot` | React 根 fiber | 可遍历 fiber 树读 `use-rate-limit` 状态，数据最全但**最脆弱**，仅作可选增强 |

其他实测事实：`window.codexWindowType === "electron"`；localStorage 无限额相关键
（只有 statsig 特征开关缓存）；未触发限额时 DOM 无 rate-limit 元素、正文无限额
关键词（符合预期）。

#### 3.7.1 静态提取的限额契约（零打扰，已完成）

资源不是散文件，全部打包在
`…\app\resources\app.asar`（199 MB）。**WindowsApps 下的 exe 不能执行**（与 §3.5
同一权限限制），但**文件可读**——用 `.codex` 目录里那份可执行的 `rg.exe`
（`%LOCALAPPDATA%\OpenAI\Codex\bin\<hash>\rg.exe`）以 `-a` 扫二进制即可，无需
解包、无需重启应用。

提取到的标识符（括号为出现次数，可作状态字段与 UI 锚点）：

| 类别 | 标识符 |
|---|---|
| **状态判定** | `rateLimitReached`(130)、`rateLimited`(65)、`rateLimitStatus`(41)、`rateLimitPercent`(65) |
| **重置信息** | `rateLimitReset`(202)、`rateLimitResetMetadata`、`rateLimitResetUnknown`、`rateLimitHours/Minutes/Days/Months/Years` |
| **UI 载体** | `rateLimitResetPromptModal`(1300)、`rateLimitResetModal`(1171)、`rateLimitResetHomeBanner`(260) |
| **文案键** | `rateLimitTitle`、`rateLimitDescription`、`rateLimitFallbackLabel`、`rateLimitSummaryDescription`、`rateLimitUnavailable` |
| **事件名** | `rate_limit_reached`(36)、`rate_limiting`(14) |

提取到的用户可见文案（DOM 文本匹配依据）：

- `Usage limits`
- `…'ve hit your usage limit. Review your workspace…`

即哨兵有三层可用锚点，稳健性递减：**UI 载体（modal / homeBanner）→ 文案文本 →
内部状态字段（`rateLimitReached` / `rateLimitPercent`）**。`rateLimitPercent` 尤其
有价值——它意味着**限额是连续百分比而非布尔量，可以做"接近上限"的提前预警**，
而不必等到彻底用完才提示切换。

**对 §5.2 的修正**：哨兵触发源由"Network 响应 + DOM 双路"改为
**"DOM/MutationObserver 为主 + 共享状态快照为辅"**，并保留 codex CLI 侧限额输出
作为第三方交叉验证（CLI 与桌面端共用 `CODEX_HOME`）。

### 3.8 ✅ V2′ 已实机验证（2026-07-26）：**方案前提成立**

**结论：官方 ChatGPT 账号登录态下，自定义 provider 依然生效，账号凭据会随请求
转发给中转站。** "限额 → 一键切共飞 → 同会话继续"的产品前提**不需要重做**。

证据链（三条独立来源互证）：

| # | 证据 | 说明 |
|---|---|---|
| 1 | **桌面应用会话元数据** | 今日 rollout 文件记录 `"originator":"Codex Desktop"` + **`"model_provider":"sub2api"`** —— 桌面端确实采纳了自定义 provider |
| 2 | **桌面应用 UI** | 写入 `model = "gpt-5.6-sol"` / `model_reasoning_effort = "high"` 后，输入框右下即显示 `5.6 Sol High` —— 配置被真实读取 |
| 3 | **CLI 实际流量** | 同一份 config 下 `codex exec` 向 `127.0.0.1:8080` 发出 **`POST /v1/responses`**，请求头含 `authorization` + **`chatgpt-account-id`** |

**最关键的一条是证据 3 的请求头**：官方账号的 `chatgpt-account-id` 与
`authorization` 被**转发给了自定义 provider**（即 `requires_openai_auth = true`
的行为）。这正是"限额后继续用同一账号身份走中转"所需要的。

**排除的混淆因素**：本机 `HTTP_PROXY=127.0.0.1:7897`，但 `NO_PROXY` 与 WinINET
`ProxyOverride` 均含 `localhost;127.*`，故访问 `127.0.0.1:8080` 会直连而非走代理，
监听器不会漏记。

**遗留的不确定项（不影响结论，但需补测）**：桌面应用在 8080 上只留下两次
**"空连接"**（TCP 建连但探针在 120 ms 单次 read 内没读到请求行），未捕获到完整的
`POST /v1/responses`。最可能是探针过于简陋（单次读、无 HTTP/2、无 TLS）而非桌面端
绕过——因为证据 1 已证明它采纳了该 provider。**补测方式：用真实 Sub2API 后端或
一个正规 HTTP 服务器替换探针监听器，重跑一轮桌面对话。**

> 顺带澄清一个易误判点：桌面端与 CLI 都出现的 `Reconnecting 1/5…5/5` 报错，
> 指向的是 **`https://chatgpt.com/backend-api/ps/mcp`**（MCP 通道），**不是模型请求
> 失败**。CLI 在该报错之后仍正常发出了模型请求。排查限额/路由问题时不要被它误导。

### 3.9 注入层实机集成验证（2026-07-26）

`AiSwitch.Injection` 的四个类已在真实官方应用上跑通（此前只过单测）：

- `CodexAppLauncher`：未授权时正确返回 `BlockedByRunningInstance` 并拒绝强杀；
  显式授权后 COM 激活成功（`Launched`，Chrome/150）；
- `CdpTargetLocator`：`/json/version` 与 `/json/list` 的 `JsonPropertyName` 绑定正常；
- `CdpWebSocketTransport` + `CdpConnection`：连接与命令往返正常；
- `CoflyOverlayInjector`：**共飞状态条成功渲染进官方界面**（截图可见右上角绿色
  「共飞 · V2′ 测量中」），`PushStateAsync` 生效，校验返回
  `{"present":true,"text":"共飞 · V2′ 测量中"}`；
- `CdpScriptException`：抛错脚本被正确识别（修复在实战中有效）。

> ⚠️ **本节曾记录一条错误结论，已更正。** 原文写「官方 UI 内容在 Shadow DOM 中」，
> 依据是 `document.body.innerText` 仅 12 字符且 `querySelectorAll` 找不到输入框。
> **那是应用尚未渲染完成时的时序假象。** 加载完成后复测：`shadowHosts = 0`、
> light DOM 有 1236 个元素、`[contenteditable]` 与 51 个 button 用普通
> `querySelector` 即可找到。**该应用不使用 Shadow DOM。**
> 教训：探测必须在 UI 渲染完成后进行，否则会把"还没渲染"误读成"结构特殊"。
> （探测器仍保留 `shadowRoot` 递归作为向后兼容，但任何逻辑都不依赖它存在。）

### 3.10 ✅ 限额哨兵已实现并实机验证（2026-07-26）

代码：`src/AiSwitch.Injection/Sentinel/`（`CodexLimitSentinel.cs` +
`LimitDetectorScript.cs`），测试 40/40 通过，解决方案 0 警告 0 错误。

**职责切分**：页内 JS 只上报**客观事实**（弹窗在否、命中哪条文案、百分比多少），
判级策略在 C# 侧 `Classify()`——因此"接近上限 vs 已达上限"的规则是可单测的，
不埋在浏览器里。传输用**轮询**而非 CDP binding：页内已有 MutationObserver 保证状态
新鲜（不漏瞬时弹窗），轮询顺带在导航后免费重装探测器。

**隐私**：页面文本不出页——只回传布尔值与一小段"重置时间"片段，绝不回传对话内容。

#### 3.10.1 ⚠️ 最重要的发现：官方 UI 是本地化的，只匹配英文会让哨兵静默失效

实测 `body.innerText` 为**中文**（文件/编辑/视图、新建任务、已安排…）。而 §3.7.1
从 bundle 里提取的是**英文** `hit your usage limit`——**在本机永远不会命中**。
已从 bundle 提取并验证真实多语言文案：

| 语言 | 已达上限文案 |
|---|---|
| zh-CN | `你已达到使用上限。升级套餐或充值额度以继续，…` / `已达到额度上限` |
| zh-TW | `你已達到使用上限。立即升級計劃或加購積分以繼續使用。` / `已達使用上限` |
| ja | `利用上限に達しました` |
| en | `You've hit your usage limit…` |

重置文案：`用量重置` / `用量重設` / `你的额度将于…` / `你的速率限制將於…` /
`resets at|in|on …`。

**必须排除的误报陷阱**（措辞高度相似但语义无关）：
`你的工作區已達邀請上限`（工作区**邀请**上限）、`你已達邀請上限`、`目標已達成`。
naive 的 `/已達.*上限/` 会误判为限额。因此每条 reached 模式都**强制要求限额对象是
使用/用量/额度**，另设 `notUsage` 否决模式。

**验证方式**：文案正则位于 JS、C# 单测触达不到，故新增
`CodexLimitSentinel.MatchTextAsync()` 把样本送进真实 V8 比对。冒烟结果 **8/8 全对**：
四种语言均命中，两个陷阱均正确排除，普通中文文本不误报。

#### 3.10.2 实现中发现并修复的三个缺陷

1. **探测器版本守卫会阻止自身升级**：原逻辑见到已存在探测器就直接 `return`，
   导致更新后的脚本永远装不进长驻页面（实机复现：新加的 `matchText` 拿不到）。
   已改为版本比对 + 旧版 `stop()` 断开其 MutationObserver 后重装。
2. **无轮询超时会永久卡死轮询循环**：一次未被应答的 CDP 命令即可挂死。已加
   `PollTimeout`（默认 10 秒），超时降级为 `Unknown`。
3. `Runtime.evaluate` 的脚本异常曾被当作成功（见 §3.9），已由 `CdpScriptException` 修复。

#### 3.10.3 仍未验证的部分（**不可当作已完成**）

- **从未观测到真实限额状态**（无法人为触发）。因此以下均未验证：
  `rateLimitResetModal` 等 bundle 模块名**是否真的出现在 DOM 属性里**
  （它们是构建产物 chunk 名，很可能**根本不是 DOM class**，此路可能完全无效）；
  `rateLimitPercent` 能否从 DOM 读到（当前用 `role="progressbar"` 的
  `aria-valuenow` 与 `NN%` 文本做启发式猜测）。
- 因此**当前唯一经过验证的信号是文案匹配**。补测窗口：下次真实触发限额时，
  立刻用 `matchText` 与快照记录当时的 DOM 结构。

### 3.11 ✅ 切换编排已实现（2026-07-26）

代码：`src/AiSwitch.Injection/Sentinel/`（`IRelaySwitchGateway.cs` +
`RelaySwitchOrchestrator.cs`），测试 57/57 通过，解决方案 0 警告 0 错误。

**跨层做法**：`ILegacySwitchCoordinator` 及其 `LiveStatus` / `OperationResult`
都是 WPF 程序集的 `internal`，**不能出现在 Injection 的公开签名里**。因此在
Injection 侧定义 `IRelaySwitchGateway`，只用本程序集自有类型
（`RelayRoutingState` / `RelaySwitchOutcome`），由 WPF 侧写适配器把
`LiveStatus.CodexBaseUrl`、`MixedCodexSourceId`、`OperationResult` 映射过来。
Injection 因此保持零 WPF 依赖、可单测。

> **网关契约的硬性要求（后来实现者勿省略）**：`SwitchToRelayAsync` /
> `SwitchToOfficialAsync` **必须在写入前对完整 config.toml 做快照**。§3.6 已证明
> 官方会按自身 schema 整体重写该文件，只保留"关注字段"会丢掉用户其余设置。
> 该要求已写入接口的 XML 文档。

#### 3.11.1 提示策略：以"限额周期"为单位，且 `Unknown` 不是周期边界

- 一个周期内**只提示一次**；周期在等级首次离开 `Normal` 时开启、回到 `Normal` 时关闭。
- **`Unknown` 不关闭周期**——它在应用加载中和轮询超时时都会频繁出现（本机实测常见），
  若当作边界会导致每次抖动都重新弹提示。这是单测覆盖的重点用例
  （`UnknownBetweenReachedDoesNotReopenTheEpisode`）。
- 用户拒绝后，本周期内**不再提示**，包括 Approaching→Reached 的升级。信息不会丢失：
  注入的状态条始终显示当前等级，只是不再打扰。
- 未被拒绝时，Approaching→Reached 的升级**会**再提示一次（等级性质变了）。

#### 3.11.2 路由持续校验：走轮询，不监听登录事件

按 §3.6 的修正 1 实现：定时 `ReadRoutingAsync` 复查路由是否仍指向共飞，
**而不是监听登录事件**——§3.6 已说明登录只是"观测到的"触发点，不能假定唯一。

关键是区分两种"路由离开中转"，二者 UX 不同：

| 情形 | 处理 |
|---|---|
| 用户主动切回官方（当前未限额） | **静默**，不打扰 |
| 仍处于限额中却发现路由丢失 | 提示 `RoutingLost`：「路由被官方重置，是否重新应用？」 |

成功切换后会把 `PointsAtRelay = true` 设为基线，因此即使之前没读过路由，
随后的覆盖也能被检出（单测 `AcceptingEstablishesTheRelayBaselineForTheWatch`）。

#### 3.11.3 已补齐：会话组合根、网关适配器、提醒卡片 ViewModel（2026-07-26）

| 组件 | 位置 | 说明 |
|---|---|---|
| **会话组合根** | `AiSwitch.Injection/CodexInjectionSession.cs` | 一次 `StartAsync` 串起 launcher → CDP 连接 → 状态条 → 哨兵 → 编排器 + 路由守护；WPF 只需构造一个对象并订阅 `PromptRequested` / `LimitStateChanged` |
| **状态条脚本** | `AiSwitch.Injection/CoflyOverlayScript.cs` | 按等级着色（就绪/接近/已用尽/检测中），`pointer-events:none` 完全不干扰官方交互；文案与策略全部由 C# 决定 |
| **网关适配器** | `AiSwitch.Wpf/Services/RelaySwitchGatewayAdapter.cs` | 把 `LiveStatus`/`OperationResult` 映射为 Injection 自有类型；切换走 `ApplySourceAsync(local-machine / cloud-default)` |
| **提醒卡片 VM** | `AiSwitch.Wpf/ViewModels/RelaySwitchPromptViewModel.cs` | 三种场景的文案 + Accept/Dismiss 命令 |

**适配器的路由判定以 base URL 为准**（它才是真正承载请求的东西），source id 仅作
兜底：loopback 与私网地址都算共飞中转（局域网中转同样是中转），公网主机则判为官方。
即使 source id 过期错误，URL 也会纠正它——路由守护的正确性依赖这一点。

`SwitchService.SwitchAsync` 在写入前已调用 `BackupCurrentFiles()`（第 587 行）并复制
**完整** config.toml，故网关契约的快照要求由现有路径满足；适配器注释里标注了
"勿从调用链中移除"。

**卡片文案固定包含两条决策依据**（有单测锁定）：① 本机聊天记录与记忆完整保留；
② 切换后官方账号绑定的云端任务会暂停。`RoutingLost` 场景的文案则解释成因
（"官方客户端重写了配置，通常发生在重新登录之后"），而不是让用户误以为额度用尽。
**切换失败时卡片保持可见并显示原因**——静默关闭会让用户误以为已经切过去了。

#### 3.11.4 实机验证：整栈一次调用跑通

`cofly-smoke --session` 结果：`started=True outcome=Launched`、状态条渲染为
「共飞 · 就绪」、`currentLimit=Normal`、**未限额时 0 条提示**、**网关切换方法 0 次调用**
（证明会话不会擅自改配置）。未授权重启时正确返回
`BlockedByRunningInstance` + `needsConsent=True`。

测试总数 **525 全通过**（Injection 65 / Wpf 397 / Terminal 32 / Core 17 / Chat 14），
解决方案 0 警告 0 错误。

#### 3.11.5 仍未做

- **XAML 卡片视图**与 `MainWindowViewModel` 的接线（VM 已就绪，缺 View 与注册）；
- 注入模式的用户开关（建议挂在中转中心或设置页），含"需要重启官方客户端"的确认流程。

## 4. 总体架构

```
共飞工作台 (WPF)
 ├─ Launcher      以 --remote-debugging-port=127.0.0.1:<port> 启动官方 Codex 桌面应用
 │                （AppX 需定位真实 exe；提供"打开原版"直通方式）
 ├─ 注入器        连 CDP，注入"共飞状态条"到官方 UI
 │    ├─ 共飞侧数据：token 用量 / 账号池状态 ← 本机 Sub2API admin API (127.0.0.1:8080)
 │    └─ 官方侧数据：限额/用量 ← CDP Network 域拦截官方响应（429、rate-limit 头、
 │                    限额提示 DOM 元素）
 ├─ 限额哨兵      监测"usage limit / 需等待至 xx"→ 官方 UI 内弹提醒卡片
 │    └─ [切换共飞中转继续] → 调用现有 SwitchService 改写 config.toml/auth.json
 └─ 恢复哨兵      官方限额窗口刷新后提醒切回（同机制反向执行）
```

设计原则：

1. **纯增强**：注入层任何故障不得影响官方应用正常使用；
2. **只连本机**：注入的 JS 仅访问 `127.0.0.1`（Sub2API），不外发任何聊天内容；
3. **后端唯一调度**：切换只是把上游指到本地 Sub2API，个人账号优先等调度逻辑
   仍全部在后端（AGENTS.md 数据权威约束不变）。

## 5. 功能设计

### 5.1 共飞状态条（注入 UI）

- 固定悬浮条/角标，显示：当前接入方（官方直连 / 共飞中转）、共飞侧今日 token
  用量与费用、账号池健康摘要、官方账号限额状态（若可取得）。
- 数据轮询本机 Sub2API admin API；官方侧数据来自 CDP Network 监听。

### 5.2 限额哨兵与提醒

触发源（并行三路，任一命中即触发）：

1. CDP Network：官方 API 响应 429 / 限额相关字段；
2. DOM 监听：官方 UI 出现 "You've hit your usage limit / 将于 xx 重置" 类提示；
3. （可选）codex CLI 侧的限额状态输出。

提醒卡片文案要点：说明"本地会话与记忆完整保留，可无缝续聊"，同时说明降级项
（见 §6 风险 3：账号绑定的云任务暂停）。

### 5.3 一键切换 / 切回

- 切换：SwitchService 现有流程 → `openai_base_url = http://127.0.0.1:8080/v1`
  + 共飞 API key 写入 `auth.json`；
- 切回：官方限额窗口刷新（哨兵记录重置时间）→ 提醒 → 恢复原 config/auth
  （工作台"退出即恢复"契约已有同类实现）；
- 切换时机：仅在轮次间切换，进行中的对话轮不打断。

### 5.4 后续可借鉴的增强（二期）

来自 Codex_Plus_Pro，价值排序：

1. **画中画多任务监控**（WPF `Topmost` 小窗或注入内实现）；
2. 模型/思考强度快捷栏（注入到输入框底部）；
3. 桌宠/通知（"通知 ID + turnKey" 去重持久化的思路可照搬，叠加 Windows toast）；
4. 主题皮肤（思路可借鉴；宝可梦素材有版权，不可搬用）。

## 6. 风险与应对

| # | 风险 | 应对 |
|---|---|---|
| 1 | config.toml 是启动时读还是每轮读 → 决定切换是否需重启/新会话 | §7 验证项 V2，最优先 |
| 2 | 桌面应用曾有不显示共享 CODEX_HOME 已有会话的问题（[#14389](https://github.com/openai/codex/issues/14389)） | 实测当前版本；若 UI 不列旧会话，历史文件仍在，考虑注入侧提供"续聊入口" |
| 3 | 切到 API-key 模式后，账号绑定的云任务/远程工作区不可用 | 提醒卡片明示"本地会话可续，云任务暂停" |
| 4 | 官方改 DOM/内部结构导致注入失效 | 纯增强原则 + 版本适配层 + 失效自动静默 |
| 5 | AppX 打包影响加启动参数 | 定位包内真实 exe 直启；或 `Get-AppxPackage` 取安装根 |
| 6 | 调试端口本机其他进程可连（Codex_Plus_Pro 同款风险） | 端口随机化 + 仅在用户主动启用注入模式时开启 |
| 7 | **官方登录整体重写 config.toml，丢弃中转配置及用户设置（已实测发生）** | 见 §3.6：路由状态持续校验 + 登录事件后复查 + 切换前强制完整快照 |
| 8 | 强制结束官方应用进程会中断用户进行中的任务 | 注入模式启动前须提示用户；优先复用已运行实例，不可静默 kill |

## 7. 实机验证清单（动手前必做）

- ✅ V1 定位 Electron 主程序 + 带 `--remote-debugging-port` 启动 + CDP 读/写
  能力 —— **已通过**（见 §3.5；启动须用 COM ActivateApplication）。
- ✅ **V2′ 已完成，结论为「前提成立」——详见 §3.8。** 以下为当时的问题陈述与
  测法，保留以备复测：官方 ChatGPT 账号登录态下，桌面应用还认不认
  `[model_providers.sub2api]`？
  §3.6 已观测到登录会移除该配置，加之 [#24457](https://github.com/openai/codex/issues/24457)
  正是"本地自定义 base_url 与远端 auth 混用出错"，因此存在真实可能：
  **账号模式下自定义 provider 根本不生效**。若如此，"限额→一键切共飞→同会话
  继续"的前提退化为"必须先登出官方账号"，产品形态与提醒卡片设计都要重做。
  **测法（已按 §3.7 修正——原先写的"用 `Network.enable` 看请求落到哪"是无效
  仪器，页面 target 根本看不到 API 流量，切勿照旧执行）：**
  1. **主证据：本地 Sub2API 的请求日志。** 写回 sub2api provider 后在桌面应用里
     发起一轮真实对话，看 Sub2API 有没有收到请求——收到即"账号模式仍走自定义
     provider"，没收到即"账号模式绕过自定义 provider"，方案前提需重做。
  2. **第二证据源：codex CLI。** CLI 与桌面端共用 `CODEX_HOME`，同一份
     config.toml 下用 CLI 发一轮，可区分"配置本身无效"与"仅桌面端不认"。
  3. 顺带确认 V3（旧会话可否继续）与 V5（memories 读写）。

  ⚠️ 此测试需要把 sub2api provider 写回 config.toml —— 属于修改用户配置，
  执行前须明示、取得同意，并对**完整文件**做前后快照（§3.6 已证明该文件会被
  官方整体重写，只备份关注字段不够）。

  ⚠️ **顺带做集成冒烟测试，避免多余的一次重启**：`AiSwitch.Injection` 的四个类
  （`CodexAppLauncher` / `CdpWebSocketTransport` / `CdpTargetLocator` /
  `CoflyOverlayInjector`）目前**只过了单测，从未对真实应用跑过**（V1 验证的是
  PoC 脚本的路子，不是这些 C# 代码）。COM 声明、`/json/version` 的
  `JsonPropertyName` 绑定、`addScriptToEvaluateOnNewDocument` 均属首次执行未验证。
  应在 V2′ 这同一次启动里串起来跑一遍：`EnsureDebugPortAsync` →
  `FindPageTargetAsync` → 连接 → `InstallAsync` 注入一个最小 overlay → 确认元素
  存在。一次打扰，四个类同时验证。
- V3 切换到本地 Sub2API 后，旧会话能否在官方 UI 里继续（对应风险 2）；
- ✅ V4 限额信号来源 —— **已通过，但结论是否定的**：Network 域看不到官方 API
  流量（API 走主进程），哨兵改走渲染进程 DOM/共享状态。详见 §3.7。
- V5 记忆（memories_1.sqlite）在中转模式下是否照常读写。

## 8. 分期路线

- ~~**M1 PoC**：launcher + CDP 连通 + 注入一个静态状态条（验证 V1）~~ ✅
  核心链路已验证（COM 启动 + CDP 读写），下一步是把 scratchpad 脚本产品化进
  `AiSwitch.Injection`；
- **M2 监控**：Sub2API 数据接入 + 官方限额哨兵（验证 V4）；
- **M3 切换联动**：提醒卡片 + SwitchService 一键切换/切回（验证 V2/V3/V5）；
- **M4 增强**：画中画 / 快捷栏 / 通知等二期项。

## 9. 与现有代码的落地对照（2026-07-26 实查）

### 9.1 已有能力（可直接复用）

**切换/恢复/校验 —— legacy `SwitchService.cs`（csproj 根，约 2900 行）：**

- `SwitchAsync(ProfileStore, TargetMode)` —— 主切换入口；内部
  `BuildCodexConfigToml` 写 `config.toml` 时通过 `ReadPreservedCodexSections`
  保留用户自有段落（对官方桌面应用共用同一文件至关重要）；
- `ValidateProfileAsync` —— 切换前 HTTP 校验（models 端点探测、状态分类、
  证书名不匹配识别、瞬时错误重试判定）；
- `RestoreSessionSnapshot(SessionConfigSnapshot)` + `BackupCurrentFiles` ——
  "退出即恢复"契约的既有实现，切回机制直接复用；
- 多客户端写入：`WriteCodexFiles` / `WriteClaudeFile` / `WriteGeminiConfig` /
  `WriteGrokConfig` / `UpdateVsCodeTerminalEnvironment` / 用户环境变量
  （`SetUserEnvironmentVariable` + `WM_SETTINGCHANGE` 广播）。

**Sub2API 数据接入 —— `src/AiSwitch.Wpf/Services/`：**

- `LocalSub2ApiRoutingService`（`ApplySourceAsync` / `ApplyRoutingAsync`）——
  维护 Sub2API 原生账号（`共飞工作台` 前缀实体）；
- `Sub2ApiServiceSummaryClient` / `CloudUsageSnapshotCache` —— 用量/摘要数据，
  **状态条的共飞侧数据源基本现成**；
- `EndpointProbeService` / `LocalGatewayEndpointResolver` —— 端点探测与解析；
- `LegacySwitchCoordinator` —— WPF 层对 legacy SwitchService 的编排入口。

**WPF 壳（13 个 View）**：Overview、TransitCenter（中转中心）、AccountCenter、
Connections、Gateway、Projects、ProjectSessions、History、Chat、Terminal、
Stats、Extensions、Settings。注入模式的开关与状态展示可挂在
TransitCenter 或 Settings。

**更新安装器**（commit c6c74f5）：`scripts/install-wpf-update.ps1` +
`install-wpf-update.cmd`（一键升级安装）—— 直接替换 + SHA-256 清单校验、
不留备份（符合 AGENTS.md 安装策略），注入组件将来随同分发即可。

### 9.2 缺口（需新建）

| # | 缺口 | 状态 | 说明 |
|---|---|---|---|
| 1 | Launcher | ✅ 已实现 `CodexAppLauncher` | COM 激活带调试端口；**先探测端口、能连就不重启**；无端口的运行实例默认拒绝强杀（需显式 `AllowTerminateExisting`） |
| 2 | CDP 客户端 | ✅ 已实现 | `ICdpTransport` / `CdpWebSocketTransport` / `CdpConnection` / `CdpTargetLocator`，纯 .NET，协议层与传输层分离以便单测 |
| 3 | 注入资产 | 🔶 骨架已实现 `CoflyOverlayInjector` | 用 `Page.addScriptToEvaluateOnNewDocument` + `Page.frameNavigated` 重注入（`Runtime.evaluate` 单次注入会在刷新后消失）；状态条脚本内容待 V2′ 后定稿 |
| 4 | 限额哨兵 | ✅ 已实现（`Sentinel/`），⚠️ 未经真实限额验证 | DOM/MutationObserver + 轮询；**文案匹配已多语言验证 8/8**（见 §3.10.1），但 bundle 模块名作 DOM 选择器与百分比读取**仍是未验证的猜测**（§3.10.3） |
| 5 | 切换编排 | ✅ 编排链已实现（`RelaySwitchOrchestrator`），⬜ UI 与网关适配器待做 | 周期化提示策略 + 路由持续校验均已实现并单测（见 §3.11）；WPF 卡片 UI、`MainWindowViewModel` 接线、`IRelaySwitchGateway` 适配器（含强制快照）尚未做（§3.11.3） |

### 9.3 归属建议

新建 `src/AiSwitch.Injection` 项目（接口进 `AiSwitch.Core`，沿用现有分层惯例，
配套 `tests/AiSwitch.Injection.Tests`）：Launcher、CDP 客户端、注入资产、哨兵
都放这里；切换仍走 `LegacySwitchCoordinator` → `SwitchService`，Sub2API 数据
复用 `Sub2ApiServiceSummaryClient`。注入层不新增任何调度/账号逻辑
（后端唯一调度原则不变）。
