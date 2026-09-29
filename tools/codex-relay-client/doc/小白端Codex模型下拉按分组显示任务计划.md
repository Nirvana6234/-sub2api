# 小白端「Codex 模型下拉按分组显示」—— 任务计划

> 状态：**M1–M6 已实现**（2026-09-29，分支 `feat/codex-picker-group-models`），自动化测试全绿
> （客户端 1087、CodexBinding 343、Server 95），其中一条用**真 Codex + 真 relay + 真配置写入器**端到端验证。
> **还没做的（需要真机 / 真账号）**：桌面版（Electron）界面里下拉是否原样显示、是否弹「实验功能」提示；
> Claude 模型条目经桥接发一轮真实对话是否正常；Mac 真机。见 §6。
> 先做 Windows；Mac 共用同一份 Core / CodexBinding 代码，只在「找到 Codex 可执行文件」这一处不同（M6）。

## 1. 需求（已与用户确认）

- 在 Codex 页签选定或切换分组后，Codex 桌面版的模型下拉**只显示这个分组白名单里的模型**。
  例：Codex 页签选了 Claude 分组，下拉里是 Claude 的模型，而不是 gpt-5.x。
- **没有白名单的分组、自动分组、正在用本地代理**：保持 Codex 自带的列表，不下发目录。
- **运行中切换分组**：Codex 正在运行、且新旧分组的模型列表不同时，**弹窗让用户选**「立即重启 Codex」或「稍后」。
  Codex 没在运行时不弹，下次启动直接用新分组。
- **选「稍后」时**：Codex 仍显示旧列表，此时若它发来的模型不在新分组白名单里，由本机 relay **自动换成白名单里的默认模型**
  再转发（方案 A，写一行日志），用户感觉不到报错。
- **Codex 页签上现有的「Claude 模型 / 思考强度」下拉保留**：它决定启动时写进 `config.toml` 的默认模型；
  选中项不在白名单里时，改用白名单第一个。
- **Claude 页签不动**。
- Mac 要做，但先实现 Windows。

## 2. 现状（已核实，2026-09-29）

- 下拉来源：Codex 用 API key 接我们的 provider 时，下拉是它自带的 gpt 列表，不看分组。本机 relay 的日志里从没有过 Codex 来要模型列表的请求
  （relay 目前只接受 `POST /v1/responses` 等，其余一律 404，见 `LocalPawRelay.Reject`）。
- 启动时：Claude 分组下只往 `config.toml` 写一行 `model = "claude-sonnet-5"`（`DashboardViewModel.StartCodexAsync` 的 `preferredModel`）。
- 运行中切分组：`SwitchGroupAsync` → `_codex.SetActiveGroup(...)` 只改 relay 里的分组槽，`config.toml` 的 `model` 不动。
  所以现在从 gpt 分组切到 Claude 分组后，Codex 会继续发 `gpt-5.5`。**这是现有问题，本需求一并解决**（M3 的兜底 + M4 的弹窗）。
- 数据已有：`GroupItemViewModel.AllowedModels`（手动白名单或服务端自动推算的白名单，见 `ApplyAutoModelAllowlists`）。
- 线上 `/api/v1/user/claude-preference` 是 404（只在未合并的 `feat/claude-bridge-preference` 分支）。
  Claude 模型偏好的保存因此一直失败且被吞掉；后端直接用请求里的 `model`。本需求**不依赖它**，也不去合并它。

## 3. Codex 的机制（0.158.0-alpha.2 源码 + 探针实测）

探针：桌面版自带的 `codex.exe app-server`，私有 `CODEX_HOME`，本机假服务器提供目录，脚本在会话临时目录（不入库）。

| 场景 | 结果 |
|---|---|
| provider 配 `model_catalog_url` + `[features] api_key_model_discovery = true` | 请求 `GET <url>?client_version=0.158.0`，带 auth.json 里的 key（`Authorization: Bearer`）。返回的目录成为**唯一**列表，第一项是默认模型 |
| 只配 url、不开开关 | 完全不请求，显示自带 gpt 列表。**开关必须写** |
| 真实配置形状（`requires_openai_auth = true` + auth.json 的 `OPENAI_API_KEY`） | 同样成立 |
| 顶层 `model_catalog_json`（本地文件） | 也成立，但格式错误/为空会让配置加载失败，Codex 可能起不来 |
| 同一进程内目录变了 | 不重取（缓存 TTL 300 秒） |
| 新进程 | 立刻重取。**重启 = 可靠刷新** |
| 接口 404 / 500 / 空 `models[]`（冷启动，无本地缓存） | 回落自带列表；500 会重试约 5 次，404 不会 |
| 接口 404，但 `models_cache.json` 不到 5 分钟 | **仍显示缓存里的旧目录**（2026-09-29 实测，集成测试抓到）。所以「无白名单」不能回 404，要回 Codex 自带列表覆盖它 |

结论：用 **`model_catalog_url`，指向本机 relay**（不经过上下文压缩那一跳）。失败只回落，不会把 Codex 弄崩。
无白名单 / 自动分组 / 本地代理时，relay **回 Codex 自带的完整目录**（`codex debug models --bundled` 的原样输出），
而不是 404——否则从白名单分组切走后立刻重启，Codex 会因为 404 退回它磁盘上 5 分钟内的旧缓存，继续显示上一个分组的模型。
只有读不到自带目录时才回 404。

**目录条目怎么造**：`codex debug models` 会打印**当前安装的这个 Codex 版本**自带的完整目录（JSON）。
运行时取一条作模板，只改 `slug`、`display_name`、`description`、`priority`、`visibility=list`、`minimal_client_version`，
其余字段沿用模板——这样字段随 Codex 版本升级自动对得上，不用在客户端里写死一份会过期的 schema。
取不到（找不到可执行文件、命令失败、输出无法解析）就返回 404，Codex 用自己的列表（此时可能仍有 5 分钟内的旧缓存，属可接受的极端情况）。
该命令用**私有 `CODEX_HOME`** 运行（`AppPaths` 下的 `codex-catalog-probe`）：用户的 config.toml 此时已指向本机 relay，
让它读用户配置会变成「relay 等它、它等 relay」的死锁，也避免碰用户的 `~/.codex`。
Windows 上桌面版的 codex.exe 在 `C:\Program Files\WindowsApps\…`，**该目录不能列出**，所以从
`HKCU\…\AppModel\Repository\Packages` 读 `PackageRootFolder`（实测可读、且拿到完整路径后可以运行）。

## 4. 默认执行、可随时改的决定

| # | 问题 | 默认 |
|---|---|---|
| D1 | 什么时候写 `model_catalog_url` 和开关 | 只要走本机 relay 就写（不管当前分组有没有白名单）；有没有目录由 relay 在请求时决定。这样运行中换分组不用改 `config.toml` |
| D2 | 白名单的默认模型 | 顺序：该分组的 Claude 模型偏好（若在白名单里）→ 白名单里按字母序第一个 |
| D3 | 非 Claude 分组有白名单时的 `model =` | 只有用户 `config.toml` 里现有的 `model` **不在白名单里**才改成 D2；在的话不动（那是用户自己选的） |
| D4 | 方案 A 换模型的范围 | 只在「走中转站、分组有白名单、请求的 `model` 不在白名单」时换；本地代理直发官方的路径不动 |
| D5 | 弹窗触发条件 | Codex 正在运行 **且** 新分组的目录签名（排序后的白名单，无白名单=空）与正在运行的 Codex 启动时加载的不同 |
| D6 | 模板选哪一条 | 选 `visibility=list` 里**行为参数最保守**的一条（避开 `tool_mode=code_mode_only`、`use_responses_lite`、多 agent 等 OpenAI 专有字段）；M0 用真 Claude 分组验证后定死 |
| D7 | 思考强度档位 | 沿用模板的 `supported_reasoning_levels`，M0 确认桥接能接受这些档位 |

## 5. 任务

### M0 验证（阻塞 M2 的模板细节；不改产品代码）——部分完成
已完成：核心侧的行为（§3 的表）和端到端集成测试。**未完成**：下面 1、2、3、4 中的桌面版界面与真 Claude 对话（需要真机/真账号）。
尝试过用隔离的数据目录直接运行桌面版 `Codex.exe`：Store 应用直接运行会立即退出（退出码 1，需要包身份），
要验证只能像正式流程那样激活并改用户的 `~/.codex`，本次没有这样做。
1. **桌面版界面**：手写 config（`model_catalog_url` + 开关）指向一个临时假服务，用真桌面版打开，看下拉是否只显示目录里的模型、有无「实验功能」类提示。
   （建议用备用 `CODEX_HOME` 或先备份 `~/.codex`；需要用户在场，因为要动桌面版。）
2. **端到端**：目录里放 Claude 模型，选它发一轮，走真 Claude 分组，确认对话正常（工具调用、思考、截断）。这一步决定 D6/D7。
3. **旧版本兼容**：Codex 不认识 `api_key_model_discovery` 时，`[features]` 里多这个键是无害还是有告警/失败。
4. **运行中刷新失败**：进程已加载目录后，再遇到 500/404，是保留旧列表还是回落。

### M1 relay 提供目录接口 ✅
- `LocalPawRelay`：允许 `GET /v1/models`（`Reject` 目前对非 POST 一律 404，要放开这一条，其余不变；令牌、Host、Origin 三道闸门照旧）。
  更新钉住「只收 POST」的测试。
- relay 持有当前 Codex 分组的白名单：`SetGroup(long? id, string? name, IReadOnlyList<string>? models)`；`SetActiveGroup` 同步加参数。
- 处理：分组有白名单 → 返回按白名单构造的目录；Codex 在本地代理模式、分组无白名单、自动分组 → 返回 Codex 自带目录（见 §3）；
  读不到自带目录 → 404（`no such endpoint`，Codex 错误体形状）。
- 日志一行：返回了哪个分组、几个模型（或为什么 404），补上「Codex 到底问没问」这个一直没答案的问题。

### M2 目录构造器 ✅
- 新类（Core，纯逻辑）：输入白名单 + 模板目录，输出 `{"models":[...]}`。`priority` 按 D2 的顺序，默认模型排第一。
- 模板来源接口 `ICodexCatalogSource`：Windows 实现调用 `codex debug models`，按 Codex 版本缓存到内存；超时、非零退出、JSON 无法解析都返回 null。
- 测试：用一份真 `codex debug models` 输出作夹具；覆盖白名单为空、含未知模型、模板缺字段。

### M3 请求侧兜底（方案 A） ✅
- relay 转发 `/v1/responses` 时，本来就已把请求体读进内存；在这里按 D4 判断并改写 `model` 字段（只动这一个字段，保留其余字节顺序与空白不做要求，但必须是合法 JSON）。
- 只记录一行日志「Codex 请求 X 不在分组 Y 的白名单，已换成 Z」，同一个 X 不重复刷屏。
- 测试：在白名单 / 不在 / 无白名单 / 本地代理 / 请求体没有 `model` 五种情形。

### M4 配置写入与弹窗 ✅
- `CodexConfigWriter.MergeConfig`：在 `[model_providers.gongfei]` 里加 `model_catalog_url = "<relay 地址>/v1/models"`；
  往用户已有的 `[features]` 表**合并**键 `api_key_model_discovery = true`，没有该表就新建，不能重复出现 `[features]`。
  地址直接用 relay 的（不是上下文压缩的）。
- `CodexRouteGuard` 判断路由是否仍有效时把这两项算进去，官方客户端重写配置后能补回来。
- `StartCodexAsync` 的 `preferredModel` 按 D2/D3 计算，不再只限 Claude 分组。
- `CodexStartup` 记下本次启动时下发的目录签名（D5）。
- `DashboardViewModel.SwitchGroupAsync`：本机链路切换成功后，按 D5 判断，弹窗「立即重启 Codex / 稍后」。
  「立即重启」走 `StartCodexAsync(forceRestart: true)`；「稍后」保留提示到 Codex 页（「模型列表将在重启 Codex 后更新」），重启后消失。
  弹窗用现有的 `confirmRestart` 式回调，两个界面头共用（Avalonia 一个头）。
- Codex 页 Claude 模型下拉：选项仍是 `ClaudePreferenceViewModel.ClaudeModels`；若当前分组有白名单，选项限制为「白名单 ∩ 该列表」，
  交集为空则显示白名单全部（不再限定 sonnet-5 / opus-5）。Claude 页签的同一下拉**不改**。

### M5 测试与真机验证（自动化部分 ✅，真机部分未做）
- 单元：M1–M4 每块；`MergeConfig` 的 `[features]` 合并（已有表 / 无表 / 已有同名键）。
- 集成（有 Codex 才跑，条件跳过）：起 relay + 真 `codex.exe app-server`，`model/list` 断言随分组变化。
- 真机：Claude 分组 ↔ 无白名单分组 ↔ 自动分组来回切，看下拉、弹窗、重启后的列表、稍后场景下第一轮的日志。

### M6 Mac ✅（代码已写，未在 Mac 上运行）
- `ICodexCatalogSource` 的 Mac 实现：定位 `/Applications/Codex.app` 里的 `codex` 可执行文件，取不到就返回 null（退回自带列表，不影响使用）。
- 其余全部共用；`config.toml` 路径与写法平台无关。Mac 不在开发机上编译，出包由现有流水线负责。

## 6. 风险

- **`api_key_model_discovery` 在 Codex 里标着「开发中」**，后续版本可能改名、默认打开或移除。现状是缺了开关就静默回落自带列表，
  所以最坏是「下拉不再按分组」，不会坏掉聊天；但要在日志里能看出「Codex 是否问过目录」，出问题好判断。
- 目录条目里的行为参数会改变 Codex 发出的请求形状（工具类型、截断策略等）。这是 M0-2 存在的原因。
- 端点 500 时 Codex 会重试约 5 次：relay 自己构造目录，失败要回 404 而不是 500。
- 「稍后」场景下 Codex 界面显示的模型与实际转发的可能不同（方案 A 换了）。这是用户选择的取舍，弹窗与 Codex 页提示要写明。
- 自动分组按请求里的 `model` 选分组：下拉保持自带列表，用户选什么就走什么，本需求不改变这一点。

## 7. 不做

- 不改 Claude 页签，不合并 `claude-preference` 后端。
- 不给无白名单分组做目录（含用「模型广场」数据）。
- 不用 `model_catalog_json`（写坏会让 Codex 起不来）。
- 不做 `X-Models-Etag` 运行中刷新（弹窗 + 重启已够，且要多改响应头）。

## 8. 实现记录（2026-09-29）

- 新代码：`Core/Services/CodexGroupModels.cs`（一个分组的模型与默认顺序，picker / 兜底 / `model =` 三处共用）、
  `CodexModelCatalog.cs`（目录构造 + 请求里 `model` 的字节级替换）、`CodexBundledCatalogSource.cs`（`codex debug models --bundled`）。
- 改动：`LocalPawRelay`（`GET /v1/models` 路由、`SetGroup` 带模型、请求兜底）、`CodexConfigWriter`（`model_catalog_url`、
  `[features]` 合并、`keepModelIfIn`）、`CodexStartup` / 路由守护（守护重写配置时一并补回目录地址与开关）、`DashboardViewModel`
  （推模型、弹窗、页面提示、Codex 页下拉的选项）、`ClaudePreferenceViewModel`（不认识的模型不再被存成第一项）。
- 与计划的出入：
  - 无白名单 / 自动分组 / 本地代理**不是 404 而是回自带目录**（§3 的缓存发现）。
  - 兜底替换的日志同一个「分组+模型」只记一次，避免长会话每轮刷屏。
  - 下拉选项：Claude 分组有白名单时，先取「白名单 ∩ 偏好能表示的两个模型」，交集为空才显示白名单全部。
  - 目录地址与开关**只要走本机 relay 就写**（D1），有没有目录由 relay 请求时决定，所以运行中换分组不用改 `config.toml`。
- 已知边界：「稍后」之后，如果 Codex 在 5 分钟缓存到期后自己重新拉了目录，它可能已经显示新列表；这时提示条还在，
  重启一次即消除，无害。
