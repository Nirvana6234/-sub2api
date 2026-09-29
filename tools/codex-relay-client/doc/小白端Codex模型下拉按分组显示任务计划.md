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
- **运行中切换分组**：~~弹窗让用户选「立即重启 / 稍后」~~ **（2026-09-29 晚改）不重启、不弹窗，切换后正在运行的 Codex 自己刷新。**
  切分组的提示语里补一句「Codex 的模型下拉会在下次打开或发送消息后更新」（仅当 Codex 在运行且新旧列表不同）。
  真机验证时发现原来的弹窗没出现，并且用户希望不重启就生效；下面 §9 是新的做法。
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

### M6 Mac（代码已写但**未在 Mac 上运行**；`ChatGPT.app` 里 `codex` 的位置是**猜的**，找不到时退回 Codex 自带列表）
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
- 提交后自查发现并修掉的四个问题（都是最初的测试没覆盖的）：
  1. **目录条目改变了 Codex 发出的请求**。用真 Codex 抓 `/responses` 请求体，对比「有目录」和「没目录（Codex 对未知模型的兜底元数据）」：
     照抄 gpt-5.5 的条目会多出 `reasoning.effort`（桥接会把它变成扩展思考）、`text.verbosity`、自由格式 `apply_patch` 工具、`tool_search` 工具。
     现在条目里这些全部按 Codex 兜底元数据置回，再抓一次：`tools`（列表和内容，除了 `multi_agent_v1` 描述里的可用模型清单）、
     `reasoning`、`text`、`include` 与不下发目录时完全一致。**仍然不同的只有 `instructions`（系统提示词）**：
     条目带 gpt-5.5 的提示词，而未列出的模型用 Codex 内置的通用提示词。上下文窗口两边都是 272000，没有差异。
     副作用：这些模型在 Codex 下拉里没有「思考强度」选项——与未列出时相同。
  2. **重启后仍显示旧列表**。Codex 先用磁盘上 `models_cache.json`（5 分钟内）回答第一次列表请求，后台再刷新，重启后偶尔来不及刷新。
     现在写配置（带目录地址时）会顺手删掉这个缓存文件，重启后必然重新请求。集成测试因此从「4/6 失败」变成「8/8 通过」。
  3. **Codex 会在每个对话里发「Under-development features enabled: api_key_model_discovery」警告**（抓 app-server 通知实测）。
     现在同时写顶层 `suppress_unstable_features_warning = true`；再抓一次，警告消失。副作用：用户自己开的其他实验功能也不再提示。
  4. **不在共享偏好里的模型不写进共享偏好**。白名单与两个已知 Claude 模型没有交集时，在 Codex 页选的模型只记在 Codex 一侧
     （不持久，重启客户端后回到白名单第一个），不会出现在 Claude 页或 Claude Code 的设置里。
- 已知边界：路由守护重写配置时会补回**启动时**选的默认模型；用户选「稍后」之后如果配置被重写，会回到上一个分组的默认模型。
  relay 的兜底替换会遮住这个影响，所以只是低风险。
- 已知边界：「稍后」之后，如果 Codex 在 5 分钟缓存到期后自己重新拉了目录，它可能已经显示新列表；这时提示条还在，
  重启一次即消除，无害。

## 9. 不重启的刷新（2026-09-29 晚，取代 M4 里的弹窗）

用户真机反馈：切分组没弹窗；并且期望不重启 Codex 就生效。用真 Codex 的 app-server 实测后（探针，不入库），做法如下。

Codex 刷新模型列表有三条路：

| 路径 | 触发 | 实测 |
|---|---|---|
| 回复头 `X-Models-Etag` | `/responses` 的回复头带的版本号和它手里目录的 `ETag` 不同 → 当场重新拉目录 | 发下一条消息后 **0.6 秒内**，同一进程里的 `model/list` 就是新列表；不带该头则 10 秒内不变 |
| 缓存文件 | `model/list` 用「缓存有效就不联网」的策略；删掉 `models_cache.json` 后下一次 `model/list` 立即重新拉 | 打开下拉（如果界面会请求列表）就是新的，不需要发消息 |
| 后台刷新 | app-server 每 4 分 30 秒刷新一次 | 兜底，不需要做 |

实现（`feat/codex-picker-group-models` 上的后续提交）：
- `LocalPawRelay.ModelsEtag`：目录的版本号，随「分组的模型列表 / 无白名单 / 本地代理」变化；目录响应的 `ETag` 和每个回复给 Codex 的
  `X-Models-Etag`（含走本地代理直连官方的回复）取**同一个值，逐字节一致，含引号**——不一致就会每轮多刷一次。没有目录来源（读不到
  Codex 自带目录）时不盖章，避免 Codex 每轮都来问一个回答不了的问题。
- `CodexStartup.SetActiveGroup` / `SetLocalProxy`：版本号变了才删 Codex 的 `models_cache.json`（同一个列表换分组不删）。
- 去掉弹窗、`LoadedCatalogSignature`、页面上的"重启后更新"提示；「重启 Codex」仍是手动兜底。
- 测试：relay 版本号（一致 / 随列表变 / 默认模型变也算 / 无来源不盖章）、缓存删除（变才删）、**真 Codex 端到端**：
  改分组 + 删缓存后下一次 `model/list` 是新列表；再换一个分组，只发一轮对话后同一进程里列表变成新的。
  把盖章那一行去掉后这条测试确实失败（等待 11 秒超时），所以它测到了这条路径。

**真机验证结果（2026-09-29，r2 包，日志在 `artifacts/…-r2/…/logs/client.log`）：切分组后下拉没有变。原因（读桌面版 app.asar 里的界面代码确认）：**
桌面版界面里模型下拉的数据是一个前端查询缓存：`staleTime = 5 分钟`、`refetchOnWindowFocus = true`（只有过期后窗口重新获得焦点才重新请求），
查询的 key 由 主机 / 登录方式 /（自定义 provider 时）`model_provider` /（配了的话）`model_catalog_json` 组成。日志里 Codex 只在启动时请求了两次目录
（13:46:02、13:46:03），此后 13:48–13:49 的三次切分组都发生在这 5 分钟之内，所以界面根本没有再向 Codex 要列表——
relay 的版本号和删缓存都作用在 Codex 后端（已实测有效），但界面自己的缓存要等 5 分钟过期。
**结论：不重启的情况下，界面最多约 5 分钟后（且需要窗口重新获得焦点）才会更新；重启 Codex 立即更新。**
外部无法让界面的缓存立刻失效（key 里的三项我们都不该在切换时改：改 `model_provider` 会让会话按 provider 过滤时看不到历史，
`model_catalog_json` 会让 Codex 改用静态文件）。要立即更新只有驱动界面本身（调试端口注入，曾因不稳定被移除）或重启 Codex。
切换提示语已改成如实的「最多约 5 分钟后更新，重启 Codex 可立即更新」。

同一份日志还暴露了一个自己引入的 bug：Codex 中途断开目录请求时，错误处理试图在已提交的响应上再写 502，抛出没人观察的异常，
被记成「未观察的任务异常」。已修（目录处理返回「是否已开始响应」，且错误写入也接住这个异常）。

## 10. 弹窗恢复 + 分组只在启动时决定（2026-09-29 晚）

**弹窗恢复（用户要求）：** 切分组后，若 Codex 在运行且新旧分组的模型列表不同，弹窗让用户选「立即重启」或「等待」。
- 判断不再依赖「Codex 启动时加载的签名」（真机上就是因为这个没弹出来），改为直接比较**切换前后两个分组的模型列表**；
  「Codex 是否在运行」现场问一次 `CheckAsync`（界面上的运行标志最多滞后一分钟）。
- 等待：不重启，界面最多约 5 分钟后更新（见 §9 的真机结论）；期间 relay 把不在白名单的请求模型换成默认模型。立即重启：`StartCodexAsync(forceRestart: true)`。
- 两个分组都无白名单、或白名单相同 → 不弹。切到无白名单分组时文案说明「恢复为 Codex 自带的列表」。

**分组只在启动时决定（用户要求）：** 「Codex 分组」这块，启动时用本机上次的选择，本机没有才用服务器的默认（托管 key 的分组，并立即写进本地偏好）；
此后只有本客户端自己的切换才会改变它，**轮询不再读偏好文件、也不再读服务器的记录**。
- 起因：同一台机器开两份客户端时，它们共用同一个偏好文件，原来每次轮询（约每分钟）都重新读一遍，一份的切换会把另一份拽过去。
- 同时修了「两个使用中」：自动分组模式下，本地偏好里仍记着的固定分组和「自动分组」同时被标成使用中。现在自动分组在用时其余全部清除。
- 退出登录（`Reset`）后重新决定；非本机链路（托管 key 模式）保持原来的行为，因为那时分组本来就存在共享的 key 上。
- 已知边界：另一份客户端之后改了偏好文件，这份要到下次启动才会用到它——这正是要求的行为。

## 11. 白名单里带中文的条目一律剔除（2026-09-29 晚，用户要求）

白名单里含中文（含全角标点）的条目是运营写的备注，不是模型 id，出现在下拉里只会是一个必然失败的选择。
在 `GroupItemViewModel` 读取白名单的**唯一入口**过滤（`IsSelectableModelId`），所以「模型」提示、Codex 下拉、默认模型、Claude 下拉的选项、
弹窗的比较全部一致。过滤后白名单为空，就当作**没有白名单**（Codex 保持自带列表）。
判断范围：CJK 统一表意文字（含扩展 A）、兼容表意文字、CJK 标点、全角形式；只含 ASCII 的 `gpt-5*` 这类通配符**不动**（是否也该剔除待定）。
