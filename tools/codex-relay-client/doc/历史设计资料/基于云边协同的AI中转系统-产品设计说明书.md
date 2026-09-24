# 基于云边协同的AI中转系统——产品设计说明书

## 1. 文档信息、版本、适用范围与状态标记

本说明书是“基于云边协同的AI中转系统”的产品设计基线，同时充当架构与开发协作基线。它统一产品定位、三端分工、云边协同机制、身份与数据边界、安全约束、近期开发优先级和中长期演进方向，供产品负责人、架构师、桌面端开发、网关开发、测试、运维及后续合作方共同使用。

| 项目 | 内容 |
| --- | --- |
| 文档名称 | 基于云边协同的AI中转系统——产品设计说明书 |
| 版本 | v2.0（Claude Code 主笔、Codex 多路事实/架构/安全/API/产品交付/信任协议及陌生读者复审后的收敛修订；候选设计基线，发布结论仍为 No-Go） |
| 适用范围 | 本地管理 App、云端中转站、本地中转站三端协同体系 |
| 事实来源 | 当前本地工作区代码、测试结果与既有设计文档 |
| 最近复审 | 2026-07-19 |
| 明确排除 | 未访问、未推断任何远端环境、远端数据库或线上运行状态；本文不证明线上部署状态 |

全文使用四种状态标记，读者应据此判断每条陈述的落地程度：

- 【已实现】：已由当前本地代码或现有文档确认，可视为既成事实；它也可能描述已确认的现存缺陷，不代表安全认可、生产可用或发布批准。
- 【已确认设计】：本说明书确定的目标设计，后续开发按此执行，但可能尚未全部落地。
- 【待决策】：存在业务、技术、法律或运营层面的选择，尚不能擅自定案，需由指定决策人拍板。
- 【远期方向】：有价值但排在近中期之后的增强方向，需要排期或进一步验证后才能进入开发。

四态覆盖顺序为“已落地 → 已定案待实现 → 尚需拍板 → 远期候选”。约束原则：任何新增 API、设备授权、自动路由、凭据迁移、能力契约、适配器、统一诊断、幂等结算等能力，只能标为【已确认设计】、【待决策】或【远期方向】，不得标为【已实现】。文档区分“App 现有能力”与“后续目标设计”，不夸大完成度。凡无状态标签的能力性陈述，一律默认视为原则或目标，不得当作实现事实。

## 2. 执行摘要

本产品是一套以本地管理 App 为统一入口、以云端中转为持续服务底座、以本地中转为可选边缘执行节点的 AI 访问与协同系统，为 AI 开发者提供多客户端、多接口、多账号、多项目的一体化访问与协同工作能力。

三端分工可以概括为：**App 负责体验、策略选择和编排；云端负责公共服务、产品级平台身份、云端财务权威和运营治理；本地中转负责边缘执行、私有账号池和低延迟自主。** local-edge 仍可在自身 Deployment 范围签发用户会话与数据面凭据，但不因此成为产品级平台身份或云端财务权威。真正处理请求的节点负责执行并持有请求级原始用量事实，App 本身不越权造账。

一个必须澄清的架构事实：**App 并不始终代理每一次官方 CLI 请求。** App 参与请求的方式分三种链路，它们的数据面位置各不相同，架构图与时序图必须据此区分，不能用一个笼统的“桥”节点混为一谈：

1. **配置驱动路径**：App 只在连接应用、项目启动或任务启动时做能力预检与路由选择，把结果受控写入官方 CLI 配置或注入到新进程的参数/环境变量。未叠加 C 时，官方 CLI 自行请求 local/cloud/官方端点，App 不在逐请求数据面内；叠加 C 时，配置指向稳定本地代理入口，由代理观察请求并请求实际目标。这是当前的主路径（对应 `CliTerminalCommandFactory` 注入 `OPENAI_BASE_URL`/`ANTHROPIC_BASE_URL`/`GEMINI_API_KEY` 等）。
2. **托管交互路径**：`ChatSessionController` 通过 `CodexAppServerEngine`（Codex app-server）、`ClaudeStreamJsonEngine`（Claude stream-json）、`GeminiAcpEngine`（Gemini ACP）把官方 CLI 拉起为子进程，经结构化 stdio 协议交换工具事件、审批、取消、用户输入。**模型 HTTP 请求仍由官方 CLI 按 RouteBinding 发出**，可以直达中转/上游，也可以在配置指向本地代理时进入第 3 类链路；这些托管引擎本身不转发模型 HTTP。
3. **协议桥路径**：只有 `ClaudeGptBridgeServer` 等本地协议转换代理真正进入请求数据面——它在 `127.0.0.1` 上监听、把 Anthropic Messages 转换成 OpenAI Responses、再转发到执行端点。官方 CLI 的 `ANTHROPIC_BASE_URL` 被指向这个本地桥。

A 与 B 是二选一的主控制/交互模式：A 由 App 落地配置后交还给 CLI，B 由 App 持续托管 CLI 交互；B 内部使用配置或进程参数不另记为 A+B。C 是可叠加在 A 或 B 上的数据面代理，因此受支持组合固定为 A、B、A+C、B+C。只有 C 中的 App 侧代理组件进入逐请求数据面；请求若经过 local/cloud，中转节点本身始终属于数据面。

当前状态：本地 App（.NET 8 WPF“共飞AI工作台”）已具备连接管理、跨客户端配置协同、图形聊天、终端、项目工作区、Sub2API 生命周期与账号治理集成等大量【已实现】能力；云端网关 Sub2API 已具备鉴权、计费、账号池调度、贡献市场与贡献房间等【已实现】能力。二者尚缺一层清晰、版本化的能力契约，App 对 Sub2API 后台接口耦合偏深，云边身份与数据边界需在产品层面固化。

近期最应优先做的（P0）是补齐安全与协同地基：Key 级用量字段白名单、桌面端凭据迁移、中转凭据加密与贡献删除语义、能力契约 v1、本地网络与 SSRF 安全、结算幂等、测试与日志隔离、CLI 配置事务、跨协议兼容基线、生产运维/灾备/密钥与事件响应基线、可信发布与升级供应链基线。这些是让现有能力可以安全对外、可持续演进的发布门槛。

截至 2026-07-19，本地审计基线结论为 **No-Go**：本文能够定义发布门槛，但不能证明当前构建已经发布就绪。P0-1、P0-2、P0-3a、P0-3b、P0-4～P0-10 与 P0-11a 的受签证据 manifest、P0-11b 的独立 attestation、连续 `issued → active` 状态及 activation 后的新鲜 checkpoint，连同产品/架构/安全/测试四方 Go 决策全部齐备并绑定同一候选后，才可转为 Go；部署前还必须重新 challenge。

## 3. 背景、现状、问题与机会

### 3.1 背景

AI 开发者日常同时使用多个官方 CLI 客户端（Codex、Claude Code、Gemini、Grok），面对多个上游接口、多个账号、多个项目，配置分散、切换成本高、用量和成本不透明。既有的“客户端配置管理”类工具（如 CC Switch）解决了配置切换，但没有覆盖云端服务、边缘中转和统一工作台。

### 3.2 现状

本地代码已在两条主线上形成可运行的能力组合：本地 App 侧的统一控制面和工作台，以及 Sub2API 侧的服务底座和贡献市场。本次审查只确认本地实现与测试结果，不确认任何远端部署当前是否可用。

### 3.3 问题

- App 与 Sub2API 后台接口耦合较深，缺少版本化契约和适配层，难以接入第三方或本地节点。
- 云边身份、凭据、数据权威边界尚未在产品层面固化，存在越权同步和字段泄漏隐患。
- 部分安全默认值（公网 HTTP、`0.0.0.0` 监听、明文密钥兼容）需要收敛。
- 用量账本在流式中断、重试、故障切换场景下的一致性保障尚未系统化。

### 3.4 机会

用一层清晰的云边协同架构和能力契约把已积累的能力串起来，形成“云端服务 + 边缘中转 + 本地 AI 工作台”一体化控制面。这是与纯配置管理工具的根本差异，也是本产品核心价值所在。

## 4. 产品定位、价值主张、目标与非目标

### 4.1 一句话定位

以本地管理 App 为统一用户入口，以云端中转为持续服务底座，以本地中转为可选边缘执行节点，为 AI 开发者提供多客户端、多接口、多账号、多项目的一体化访问与协同工作系统。

### 4.2 核心主张

1. 本地管理 App 是用户控制面、工作台与协同编排中心，但不是所有请求的强制数据面。
2. 云端中转站是公共 AI 服务、产品级平台身份、云端 API Key、云端计费、共享资源及运营治理中心；local-edge 的 Deployment 范围身份与本地账本仍归本地节点权威。
3. 本地中转站是本地 App 的可选附加能力，是本地或局域网内的边缘数据面，服务于本地自主、私有账号池、低延迟和可控执行。
4. App 负责体验、策略选择和编排；真正处理请求的中转节点负责执行、用量原始记录和账本。
5. 云边共享能力契约和必要的聚合状态，不共享数据库、加密根密钥、OAuth 凭据、账号池或对话正文。
6. 项目、会话、连接和执行节点应解耦；用户可以按客户端、项目和任务选择路由。
7. 未叠加 C 的 A/B 路径通过绑定切换协调重连/重发；叠加 C 的路径或 CLI 原生路由能力才可能执行逐请求回退。两类行为都必须透明、可审计、可关闭；自动行为以语义等价和用户预先授权为前提，不得静默改变模型、费用来源、账号/协议族或隐私级别。

### 4.3 产品目标

- 让用户在一个入口内完成多客户端、多账号、多项目的访问、切换与编排。
- 提供云端持续服务与本地自主执行两条腿，用户可按成本、时延、隐私自由选择。
- 建立版本化能力契约，使云端、本地、第三方节点可以按契约接入。
- 保证用户对路由、成本和隐私拥有最终控制权。
- 建立可审计的用量账本与安全边界。

### 4.4 非目标（防止范围膨胀）

- 不做通用云盘、通用团队 IM 或与 AI 中转无关的协作套件。
- 不在 App 侧伪造或重算权威账本；中转请求归实际执行节点，direct 请求的最终费用与官方用量归上游服务商。
- 不追求对话正文云端集中存储，对话正文默认留在本地和官方客户端。
- 近期不承诺完整多租户组织管理，该能力属于平台化阶段。
- 不强制所有请求经 App 数据面代理；未叠加 C 的配置驱动路径由 CLI 直接请求端点。

## 5. 用户角色与核心场景

| 角色 | 关注点 | 典型诉求 |
| --- | --- | --- |
| 个人 AI 开发者 | 成本、切换效率、隐私 | 一个入口切换多客户端与账号，控制花费 |
| 团队/小组负责人 | 共享、治理、成本可见 | 共享账号池、贡献房间、用量对账 |
| 私有部署使用者 | 自主、低延迟、数据边界 | 脱离本产品云端运行、本地账号池、局域网共享 |
| 运营/管理员 | 治理、合规、风控 | 贡献治理、字段脱敏、限额与审计 |
| API Key 独立调用用户 | 无需 App 的程序化访问 | 脱离 App，持 API Key 调用中转数据面 |
| 后续合作方 | 接入契约、稳定性 | 通过版本化契约接入自有节点 |

核心场景：

- 多客户端统一入口：在 App 中为各客户端分别或统一选择来源、地址、密钥、默认模型。
- 项目工作环境切换：按项目保存和恢复“连接来源 + MCP + Prompt + Skill”。
- 云端服务与本地自主并行：既能用云端持续服务，也能启用本地中转做私有、低延迟或脱离本产品云端的执行。
- 贡献与共享：显式把账号贡献到所选目标 Sub2API 部署的房间，或使用其共享池；只有目标部署为云端时才发生云边凭据跨域。
- 成本与用量透明：查看云端真实账单与本地隐私统计的双视图。
- 脱离 App 的数据面调用：API Key 用户不经 App 即可调用中转数据面；“direct”专指绕过本产品中转直连上游。

### 5.1 首要用户与首发主路径

【已确认设计】首要用户是已经使用一个或多个 AI CLI、希望降低配置切换成本并获得稳定云服务的个人开发者与小型开发团队。在 release profile 尚未冻结时，产品探索与原型评审可暂用“云端优先、本地中转可选”的最短成功路径；该暂行入口不构成发行默认或首发承诺：

1. 用户安装 App 并登录云端。
2. App 获取或录入用户可用的云端 API Key，并完成安全存储。
3. 用户选择目标客户端和模型，App 应用连接配置或按新进程注入环境。
4. 官方 CLI 或 App 托管会话完成首个请求。
5. App 展示当前 RouteBinding；在节点回执或聚合快照可用时，再展示可确认的实际来源、有效模型和用量，缺少请求级事实时不做推断。

【已确认设计】profile 一经 G0 冻结，§5.2 表内该 profile 的默认 CTA 即成为规范入口，并覆盖上述探索默认：`R-LOCAL-EDGE-WIN-v1` 默认发现本地节点，`R-DIRECT-DESKTOP-v1` 默认直连上游，CLOUD/HYBRID 分别使用其表内动作。同一发行渠道若包含多个 profile，安装或首次向导必须先让用户显式选择发布封套，再呈现对应 CTA；未选择、选择失效或目标不可用时都不得静默回落到云端。

【已确认设计】本地自主路径保留为首发候选 profile 的可选入口：用户可跳过云端登录，启用本地中转并使用本地账号或私有接口。本地入口是否在同一安装包中作为首发 CTA 暴露，由 D-REL-001 冻结。在 profile 未批准前，文档不得把本地自主描述成已承诺的默认发行能力。这里的“本地自主”表示不依赖本产品云端；若所选上游仍是互联网服务，运行仍需要网络。只有上游本身也在本机或局域网时，系统才具备真正断网运行能力。

### 5.2 发布范围封套与候选 profile（【待决策】）

一次发布必须绑定一个不可变 `release_profile_id` 与 `applicability_revision`，先冻结目标 persona、默认 CTA、允许的部署拓扑、A/B/C 组合、数据面表面、P0/WP 适用性和明确关闭的能力，再生成候选制品与证据。profile 不是运行时开关：运行时仍按 §9 的 `user_mode`、`route_target` 和 §13 的 capabilities 协商执行。以下是用于明早决策的候选封套，尚未选择即不得对外宣称“首发支持”。

| profile_id | applicability_revision | 目标用户与默认 CTA | 允许拓扑/主链 | P0-3a | P0-3b | Required（必须有逐项证据） | Disabled / N/A | 单一 DRI / 审批与证据 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `R-CLOUD-DESKTOP-v1` | `applicability-1` | 个人开发者；“登录云端并完成首个请求” | App + cloud-control-data-plane；A/B，C 仅显式启用 | R | C（服务端关闭闸门与负向证据） | P0-1、P0-2、P0-4～P0-11；WP-01～WP-07 | local-edge 安装/本地自主 N；贡献正向托管 Disabled | `D-REL-001`；产品 DRI，架构/安全/测试审批；云端候选 manifest |
| `R-HYBRID-DESKTOP-v1` | `applicability-1` | 小团队；“连接云端，再添加本地节点” | App + cloud + Windows local-edge；A/B，C 仅显式启用 | R | R | P0-1～P0-11；WP-01～WP-07 | 容器编排 N；未在 profile 中声明的企业组织能力 N | `D-REL-001`、`D-OPS-001`；产品 DRI，架构/安全/测试/运维审批；双 Deployment 证据包 |
| `R-LOCAL-EDGE-WIN-v1` | `applicability-1` | 私有部署用户；“发现本地节点并完成本地请求” | App + Windows 原生 local-edge；A/B，C 仅显式启用 | R | C（服务端关闭闸门与负向证据） | P0-1、P0-2、P0-4～P0-11；WP-01～WP-07 | 云端管理/云端结算 N；贡献正向托管 Disabled，必须写明不提供的界面和 API | `D-REL-001`、`D-OPS-001`；运维 DRI，安全/桌面审批；本地安装与恢复证据 |
| `R-DIRECT-DESKTOP-v1` | `applicability-1` | 只想管理 CLI 配置的用户；“直连上游” | App + direct；A，C 可选 | N | N | P0-2、P0-4*、P0-5*、P0-7～P0-9*、P0-10*、P0-11*；WP-01*、WP-02、WP-03*、WP-04、WP-05*、WP-06*、WP-07 | P0-1、P0-6 及中转/账本子集 N；不得因此放宽桌面安全和更新门槛 | `D-REL-001`；桌面 DRI，架构/安全/测试审批；direct 能力与凭据隔离证据 |

适用性符号固定为：`R`（Required）表示整项必须通过该 profile 的二值验收；`R*` 表示只取表中明确的 profile 子集，但子集内仍是发布阻断；`C`（Contained）表示代码或契约表面存在但首发关闭，必须有服务端拒绝、客户端不可达和负向测试，未经重新发布不得转成正向能力；`N`（Not applicable）表示该 profile 根本不发布该表面，只能以构建清单、路由矩阵和用户文案证明。`P0-3` 是总闸门，拆为 `P0-3a`（上游/中转凭据加密、迁移、轮换、最小解密）和 `P0-3b`（贡献凭据托管、转移、撤回与删除）；profile 矩阵必须分别登记两者。任何适用性变更都必须提升 `applicability_revision`，并在 §21.1 同次回写 §5.2、§19.4、§20.5 和候选 fingerprint，不能以“当前没用到”临时跳过。未完成 profile 选择、DRI/审批人和证据清单前，所有 profile 均保持 No-Go。

## 6. 产品原则与关键术语

### 6.1 产品原则

1. 用户最终控制：路由、成本、隐私级别的最终决定权在用户。
2. 边界清晰：能力可共享，敏感数据强隔离。
3. 透明可审计：自动行为（尤其回退）必须可见、可审计、可关闭。
4. 默认安全：HTTP 只允许真实 loopback，LAN/公网强制 TLS，默认 loopback 监听、默认不回退公池、默认字段最小化。
5. 解耦演进：通过版本化契约与适配层降低耦合。
6. 事实归位：谁执行谁记账，App 不越权造账。
7. 数据面可旁路：App 是控制面，可通过配置注入让 CLI 直连端点，不强制逐请求代理。

### 6.2 关键术语

- 账号（Account）：接入上游 AI 服务的凭据实体（含 OAuth token、PAT、代理密码等），是敏感资产。
- 凭据（Credential）：账号内用于认证的秘密材料，需信封加密、脱敏。
- 产品平台会话：用户登录 cloud Deployment 后形成的产品级登录状态；线缆上仍由该 Deployment 的 access/refresh JWT 表达，只用于云端产品管理面。
- Deployment 用户会话令牌：目标 cloud 或 local-edge Deployment 签发的 access/refresh JWT，issuer、audience 与撤销范围都绑定该 Deployment；不得用“平台 Token”笼统代称所有 JWT。
- API Key：调用中转数据面的鉴权密钥，可分组、限额、限期、限 IP。
- 本地数据面 Token：local-edge 对本机或获准局域网调用方签发的数据面凭据，不包含管理员权限。
- 连接（Connection）：App 中一条指向某来源（本机/局域网/云端）的可用配置元数据。
- 接口/端点（Endpoint）：可调用目标的地址、协议与鉴权方式，是目标路由选择粒度；当前 App 仍主要把它内嵌在 ConnectionProfile 中，完整一等实体属于【已确认设计】。
- 部署（Deployment）：具有稳定 `deployment_id`、独立权威数据与信任边界的一套中转部署；地址变化不应改变其身份，证书或 `deployment_id` 突变必须触发重新信任。
- 桌面安装（DesktopInstallation）：本地管理 App 的一个可信安装实例，首次可信安装生成并在受保护 trust state 中持久化 `installation_id`。它不是用户身份、设备凭证或 Deployment；同一机器上的 local-edge 仍有独立 `deployment_id`，两者不得复制或隐式转换。
- 实例（Instance）：某个 Deployment 的一次进程或容器运行实体，具有短生命周期 `instance_id`；扩缩容、重启或滚动升级可产生多个实例，不能拿它替代部署身份。
- 渠道（Channel）：【已实现】节点内部聚合多个 Group 并提供自定义模型定价的配置实体（`channels`、`channel_groups`、`channel_model_pricing` 三表，每个 Group 最多归属一个 Channel）。它是定价与分组聚合层，不是上游协议适配器；实际上游选择当前按 `account.Platform + account.Type` 在网关代码内分支判定，没有独立的“渠道适配注册表”。
- 模型（Model）：可请求的具体 AI 模型标识，分 `requested_model` 与 `effective_model` 两层。
- 账号池调度：节点在已选端点内部对多账号做的选路，由节点负责，App 不介入。
- 路由（Route）：在 local/cloud/direct 之间选择目标及其决策依据；direct 表示绕过本产品中转直连上游，当前尚未成为 App 完整一等实体。
- 路由绑定（RouteBinding）：App 侧客户端、项目、进程、配置与目标之间的绑定事实，不能代表请求已经实际执行。
- 请求记录（RequestRecord）：数据面入口在单一账本域内为一次逻辑请求建立的幂等与查询对象，包含稳定 `request_id`、主体、请求指纹、查询句柄及一个或多个 RequestAttempt；同键同指纹重放引用同一记录。
- 请求尝试（RequestAttempt）：执行节点内部一次真实上游尝试及其状态、模型和可计费用量，是请求级账本事实。
- 项目（Project）：一组工作环境（连接、MCP、Prompt、Skill）的集合。
- 会话（Session）：一次官方客户端或图形聊天的对话上下文。
- 节点（Node）：面向用户的统称；本文精确语义默认等同 Deployment，涉及进程、容器或副本时使用 Instance。
- 控制面（Control Plane）：负责管理、编排、决策的部分（App 与云端管理接口）。
- 数据面（Data Plane）：负责实际转发请求、执行调用的部分，包括云端/本地中转，以及启用时 C 中的 App 侧协议桥/本地代理；官方上游属于产品边界外的数据面。
- 本地自主（Local Autonomy）：不依赖本产品云端即可完成编排与执行；是否需要互联网取决于实际选择的上游。
- 工作上下文：当前 Project、Session 与受管客户端，回答“用户正在做什么”。
- 执行上下文：期望策略、已应用 RouteBinding、配置事务状态及最近可确认的实际来源，回答“新任务准备走哪里、已发生请求走过哪里”。
- 治理上下文：当前管理的 Deployment、登录身份、权限与数据新鲜度，回答“正在管理哪一套中转、以什么身份操作”。

### 6.3 概念维度表（三个正交维度）

系统中“三”这个数字出现在多处，容易互相串味。以下三个维度彼此正交，必须分开表达，任何一处都不能用一套“三种模式”覆盖另外两个维度：

| 维度 | 名称 | 取值 | 回答的问题 | 主要出现章节 |
| --- | --- | --- | --- | --- |
| 维度一 | 交互与数据面链路形态 | A 配置驱动、B 托管交互、C 协议桥/本地代理 | App 以何种方式参与请求，谁真正处于逐请求数据面 | §2、§7、§9.2、§12 |
| 维度二 | 用户模式与单请求路由来源 | 派生展示值 `user_mode`：none、local、cloud、hybrid；`none` 配套 `user_mode_reason`；单请求 `route_target`：local、cloud、direct | 用户当前具备哪侧本产品中转能力、无可用中转的原因；本次请求走向哪个来源、账本权威在哪 | §9、§11.3、§13.3、§17.1 |
| 维度三 | 部署拓扑 | App+direct、App+原生 local-edge、App+容器 local-edge、App+LAN/cloud Deployment | 三端进程如何组合落地、网络与数据落点如何 | §7.1 |

- 维度一是“控制/数据面链路形态”：A 与 B 二选一，C 是可叠加层，受支持组合固定为 A、B、A+C、B+C，不产生 A+B。只有 C 中的 App 侧代理进入逐请求数据面。
- 维度二分为两层：`user_mode` 是只读派生展示值。无本产品中转满足可用条件时为 `none`，只有 local 时为 local，只有 cloud 时为 cloud，两者都满足时为 hybrid；`none` 不构成第四种中转模式，也不得单独参与自动路由或策略门控。`user_mode=none` 时必须同时给出 `user_mode_reason=no_enabled_relay|temporarily_unavailable|credential_unready|feature_unsupported|unknown`。单请求 `route_target` 只有 local/cloud/direct，direct 表示绕过本产品中转直连上游；direct-only 固定表达为 `user_mode=none + user_mode_reason=no_enabled_relay + route_target=direct`，而配置过中转但暂不可用时可以出现 `none + temporarily_unavailable + direct`，两者不可混同；不存在 `route_target=hybrid`。
- 维度三是“部署拓扑”：描述逻辑三端的物理组合与网络/数据边界，端口号属于安装包配置，不进入跨版本能力契约。
- 三个维度在概念和 Schema 上正交；具体可用组合仍受已安装拓扑、凭据、信任绑定、协议能力与 readiness 约束。例如“B+C 链路 / cloud 路由来源 / App+LAN Deployment 拓扑”只有在相应端点和能力均满足时才合法。链路形态不替代路由来源，路由来源也不替代部署拓扑。

## 7. 云边协同总体架构

架构分控制面与数据面两层。App 是编排中心，但并不始终位于官方请求的数据面。当前代码要求把三类链路分开建模：

- **A 配置驱动链路**：App 建立或应用连接绑定，在项目/任务启动前做能力预检与路由选择，把结果受控写入官方 CLI 配置，或注入新进程的参数与环境变量。未叠加 C 时，CLI 直接请求本地中转、云端中转或官方上游，App 不在逐请求数据面内；A+C 时，CLI 请求稳定本地代理入口，由代理观察逐请求事实并选择其内部目标。
- **B 托管交互链路**：Codex app-server、Claude stream-json、Gemini ACP 等由 App 管理进程、输入、工具事件、审批和取消，实际模型请求仍由相应 CLI 按 RouteBinding 发出。B 描述交互控制方式；其模型数据面可以直达中转/上游，也可以叠加 C 类本地代理。
- **C 协议桥/本地代理链路**：`ClaudeGptBridgeServer` 等 App 侧代理组件真正处于请求数据面，调用方先进入本地代理，再由代理请求中转节点或官方上游。C 可叠加在 A 或 B 上；只有叠加 C 的路径才具备 App 侧逐请求观察、ID 传播和代理级受控回退的技术前提。

C 的现状边界必须说清（本次审计确认）：当前 `ClaudeGptBridgeServer` 仅绑定 loopback、面向单一可配置上游、专做 Anthropic Messages → OpenAI Responses 的协议转换（【已实现】）；它**尚未**实现通用 C 链路、请求 ID 传播、RouteBinding revision 绑定或跨目标回退（这些均为【已确认设计】，不得当作已落地能力）。因此“逐请求观察、ID 传播、受控回退”目前只是 C 提供的**技术前提**，不是现成功能。

```mermaid
graph TD
    subgraph USER_CONTROL["用户、客户端与控制交互层"]
        APP[本地管理 App<br/>desktop-orchestrator<br/>编排与 RouteBinding]
        MANAGED[托管交互层 B<br/>Codex app-server / Claude stream-json / Gemini ACP]
        CLI[官方 CLI<br/>Codex / Claude Code / Gemini / Grok]
    end

    subgraph APP_DATA["App 侧可选数据面"]
        PROXY[协议桥/本地代理 C<br/>ClaudeGptBridgeServer 等]
    end

    subgraph PRODUCT_DATA["产品数据面"]
        LOCAL[本地中转站<br/>local-edge]
        CLOUD[云端中转站<br/>cloud-control-data-plane]
    end

    UPSTREAM[官方或其他上游 AI 服务<br/>也可作为 direct 目标]

    APP -. A: 写入配置/注入参数 .-> CLI
    APP -. B: 管理交互、审批与取消 .-> MANAGED
    MANAGED -. 管理 CLI 进程和事件 .-> CLI
    APP -. 能力预检/主动查询快照 .-> LOCAL
    APP -. 能力预检/主动查询快照 .-> CLOUD

    CLI ==>|A/B 未经 C: 请求本地端点| LOCAL
    CLI ==>|A/B 未经 C: 请求云端端点| CLOUD
    CLI ==>|A/B 未经 C: direct 请求| UPSTREAM
    CLI ==>|A+C 或 B+C: 请求进入代理| PROXY
    PROXY ==>|请求本地端点| LOCAL
    PROXY ==>|请求云端端点| CLOUD
    PROXY ==>|direct 请求| UPSTREAM
    LOCAL ==>|上游请求| UPSTREAM
    CLOUD ==>|上游请求| UPSTREAM

    UPSTREAM -->|响应| LOCAL
    UPSTREAM -->|响应| CLOUD
    UPSTREAM -->|direct 响应| CLI
    UPSTREAM -->|direct 响应| PROXY
    LOCAL -->|响应| CLI
    CLOUD -->|响应| CLI
    LOCAL -->|响应| PROXY
    CLOUD -->|响应| PROXY
    PROXY -->|响应| CLI
```

图例说明：虚线表示控制面编排、能力预检和 App 主动查询聚合快照；粗实线表示请求方向；细实线表示响应方向。经中转执行时，响应遵循 `UPSTREAM → NODE → CLI/PROXY`；direct 时由上游直接返回 CLI/PROXY。请求级原始用量事实只存在于实际执行节点或上游账单域，App 不把绑定事实当作执行事实。

三个核心对象贯穿三类链路（【已确认设计】）：

- **RouteBinding（路由绑定）**：App 侧连接、配置、进程、客户端与项目之间的绑定事实，回答“当前把谁绑定到哪个目标”。它不能证明每次请求实际执行成功，也不能代替账本记录。
- **RequestRecord（请求记录）**：数据面入口为逻辑请求原子登记的幂等与状态查询事实，稳定关联一个 `request_id` 与零个或多个 RequestAttempt；查询返回的是该记录的聚合状态。
- **RequestAttempt（请求尝试）**：执行节点内部的一次真实上游尝试与账本事实，记录 `attempt_id`、实际目标、模型、结果状态和可计费用量。其权威在执行节点；direct 路径的最终权威在上游服务商。

当前 App 的来源枚举主要是 Local/Lan/Cloud，`ConnectionProfile` 内嵌多个客户端 URL；Endpoint、Deployment 和 direct 尚未成为 App 侧完整一等实体（【已实现】现状）。direct 是绕过本产品中转、直连官方或其他上游的目标路由概念（【已确认设计】）。现有 Cloud Profile 既可能指云端中转，也可能指官方接口；迁移完成前 UI 必须标为 `legacy-cloud-ambiguous`，不得据此自动判断费用权威、隐私级别或回退策略。只有 P0-4 与 P0-8 落地显式 `route_target`、endpoint 和事务迁移后，才能可靠区分 cloud 与 direct。

建议的部署角色命名（【已确认设计】）：

- cloud-control-data-plane：云端，兼具平台控制面与公共数据面。
- local-edge：本地中转，边缘数据面。
- desktop-orchestrator：本地 App，桌面编排控制面。

### 7.1 部署拓扑、进程边界与数据落点

【已确认设计】逻辑三端可以组合为四类受支持部署拓扑。端口号属于安装包配置，不进入跨版本能力契约；监听、TLS、身份、数据落点与健康语义必须进入部署清单。

| 拓扑 | 进程与请求路径 | 网络基线 | 权威数据与持久化 | 适用范围 |
| --- | --- | --- | --- | --- |
| App + direct | App/托管 CLI；A/B 未经 C 时 CLI 直连上游，A+C/B+C 时先进入 App 侧 loopback 代理 | App 侧代理仅绑定 `127.0.0.1`/`::1`；上游强制 TLS | App 保存本地编排状态；官方上游掌握请求与账单权威 | 无本产品中转、只做本地编排 |
| App + Windows 原生 local-edge | App、受管 CLI、本地 Sub2API、PostgreSQL、Redis 为独立进程 | 新装只发布 loopback 数据面/管理面；LAN 需显式 TLS、独立 Token 与防火墙规则 | App 数据与 local-edge 数据库/日志/备份分离；服务账户最小权限 | `R-LOCAL-EDGE-WIN-v1` 的候选拓扑；是否进入默认首发由 D-REL-001 冻结 |
| App + 容器 local-edge | App/CLI 在宿主机，Sub2API 与依赖在受控容器网络 | 宿主发布端口默认 loopback；镜像固定版本与 digest；容器间端口不等同对外暴露 | 数据库、Redis 和节点数据使用显式持久卷；升级不得替换本地权威卷 | Docker 回退与可复制环境 |
| App + LAN/cloud Deployment | App/CLI 经 LAN 或公网调用 Deployment；C 可选 | LAN 与公网一律 TLS；绑定证书/指纹与 `deployment_id`；管理、协同、数据面分权 | 每个 Deployment 独立数据库、Redis、日志、备份与根密钥 | 局域网共享、公共云与企业私有部署 |

进程边界也是权限边界：托管 CLI 只取得本次运行所需的最小配置投影；C 类代理只取得当前绑定所需的目标与数据面授权；App 正常路由不读取节点内部上游凭据。云端与 local-edge 可以复用代码和契约，不能共享数据库句柄、Redis 命名空间、根密钥或备份。

【已实现】当前本地原生启动脚本包含独立 PostgreSQL、Redis、backend 与 frontend 进程；通用 Compose 和 systemd 示例仍存在 `latest` 或 `0.0.0.0` 默认值，`/health` 当前固定返回进程存活 200。因此这些通用部署资产不能直接作为本产品 local-edge 的生产安全默认值，P0-5、P0-10、P0-11 完成前保持 No-Go。

### 7.2 Deployment 与 Instance 生命周期

【已确认设计】App 对 local-edge 可以执行生命周期控制，对 cloud Deployment 只做受权观察与协同；任何一方都不能用控制面状态改写账本事实。

```text
unconfigured → discovered → validating → ready ↔ degraded
ready/degraded → draining → stopped
ready/degraded → draining → upgrading → verifying → ready
                                      ↘ rollback_required → rolling_back → verifying
任一校验失败且无法安全恢复 → recovery_required
```

- `live` 只说明进程存活；`ready` 还需确认数据库、必要缓存、迁移、密钥、端口、契约版本及关键依赖可服务；`degraded` 表示仍可提供明确子集能力。
- `draining` 停止接收新请求并等待在途 RequestAttempt 收敛；最大等待时间与强制终止阈值为【待决策】，但所有升级都必须先进入排空阶段。
- `upgrading` 绑定 `app_version`、`server_version`、`contract_version`、`db_schema_version`、组件化 `config_schema_versions` 与适配器集合版本；`verifying` 未通过时只能按已验证路径回滚或前滚，不能假设镜像回滚可以撤销不可逆数据库迁移。
- 本地升级、停止或恢复必须展示目标 Deployment、当前身份、在途任务、数据快照与回滚点。云端 Deployment 的实例级滚动升级由其运维域负责，App 只消费 readiness、degradation 与兼容状态。
- 【远期方向】企业节点 enrollment 增加 `pending/active/draining/revoked` 证书与租约状态；首版不把它误写为已经实现的节点注册系统。

## 8. 三端职责、共享能力与强隔离边界

下表区分现状职责（含状态标签）与目标职责，避免把目标误读为实现事实。

### 8.1 云端中转站

| 职责 | 现状 | 目标 |
| --- | --- | --- |
| 平台用户身份、登录 | 【已实现】登录/账号查询 | 【已确认设计】设备授权 |
| 云端 API Key/Token 签发、撤销、最小权限 | 【已实现】API Key 分组/限额/限期/限 IP | 【已确认设计】access/refresh 与设备令牌生命周期 |
| 套餐、余额、计费、价格版本、对账 | 【已实现】Token 级计费、服务摘要 | 【已确认设计】带版本号/ETag 的价格接口 |
| 公共模型服务、渠道聚合、调度 | 【已实现】多平台适配、账号池调度 | 【远期方向】统一路由决策对象 |
| 贡献账号、房间、共享资源、运营治理 | 【已实现】贡献市场/房间/治理 | 【远期方向】放开单房间/单账号约束 |
| 能力契约、兼容窗口、服务状态、公告 | 【已实现】已有分散的登录、状态和业务接口，尚无统一客户端契约 | 【已确认设计】能力契约 v1 |

### 8.2 本地管理 App

| 职责 | 现状 | 目标 |
| --- | --- | --- |
| 主要交互控制入口与工作台 | 【已实现】 | 【已确认设计】API Key 脱离 App 的数据面调用仍可独立使用 |
| 连接、接口、账号、模型、路由策略管理 | 【已实现】连接与来源管理 | 【已确认设计】分层路由策略 |
| 官方 CLI 配置协同、项目、会话、终端、聊天、扩展 | 【已实现】 | 【已确认设计】CLI 配置事务与崩溃恢复 |
| 本地中转安装/启停/健康/后台入口与能力发现 | 【已实现】启停检测、打开后台 | 【已确认设计】能力发现走契约 |
| 云端登录、服务摘要、用量聚合、诊断 | 【已实现】登录/用量/摘要 | 【远期方向】统一诊断视图 |
| 用户确认后的路由执行与故障回退 | 【已实现】跨客户端连接/来源切换，已形成 RouteBinding 的行为基础；统一、持久化的 RouteBinding 对象尚未落地，未叠加 C 的配置驱动路径需重连、重启或重发任务 | 【已确认设计】未叠加 C 的 A/B 路径失败关闭或交互式协调重启/重发；叠加 C 的路径或 CLI 原生能力可在语义等价与预授权下做逐请求受控回退 |
| 本地敏感凭据保护、状态展示、操作审计 | 【已实现】本地管理员授权已使用 Windows Credential Manager；连接配置仍兼容明文 | 【已确认设计】全部连接凭据引用化与迁移 |

### 8.3 本地中转站

| 职责 | 现状 | 目标 |
| --- | --- | --- |
| 可选边缘数据面（非 App 前置条件） | 【已实现】可选启停 | — |
| 本地账号池、转发、协议适配、模型映射、并发、健康调度 | 【已实现】 | — |
| 本机/局域网共享、低延迟、私有接口、本地自主 | 【已实现】本机和局域网来源、本地中转控制 | 【已确认设计】默认 loopback，显式开启局域网；断网能力取决于上游是否也在本机/LAN |
| 本地调用原始用量记录、节点级诊断 | 【已实现】UsageLog | 【已确认设计】request_id 全链路 |
| 身份面：与云端同一份 Sub2API，同时暴露管理员通道、用户会话与数据面 Key | 【已实现】admin x-api-key/管理员 JWT、用户 JWT、数据面 API Key 三类服务身份并存（`router.go` 同时注册；不含节点持有的上游提供商凭据，完整四族系见 §10.1）；App 持有的“本地管理员授权”即该 Deployment 的 admin x-api-key | 【已确认设计】本地管理授权、用户会话与数据面调用 Token 明确分权、互不兑换，详见 §10.1 |

### 8.4 共享能力与强隔离边界

| 维度 | 可共享（复用） | 必须隔离（禁止共享） |
| --- | --- | --- |
| 代码 | 协议适配、模型映射、调度、错误分类、UsageLog 公共字段、前端组件 | — |
| 数据存储 | — | 数据库、Redis、日志、备份 |
| 密钥与身份 | — | 加密根密钥、平台身份、云端 Key、本地凭据 |
| 资源 | 能力契约；第 10.4、13.4、17.1 节定义且经过字段白名单裁剪的必要聚合状态 | 账号池、OAuth 凭据、对话正文、Deployment 内部拓扑与逐账号状态 |

原则：能力和契约可以共享，秘密和权威数据必须分离。

### 8.5 四客户端能力矩阵

下表记录当前本地 App 对四类官方客户端的已确认覆盖。未在代码审计中确认的 Grok 细粒度能力不写成既成事实。

| 能力 | Codex | Claude Code | Gemini | Grok |
| --- | --- | --- | --- | --- |
| 连接管理（来源与绑定） | 【已实现】 | 【已实现】 | 【已实现】 | 【已实现】 |
| 配置协同（官方配置/进程注入） | 【已实现】 | 【已实现】 | 【已实现】 | 【已实现】 |
| 客户端路由与终端 | 【已实现】 | 【已实现】 | 【已实现】 | 【已实现】 |
| 图形会话 | 【已实现】 | 【已实现】 | 【已实现】 | 【远期方向】 |
| 会话索引、继续与删除 | 【已实现】 | 【已实现】 | 【已实现】 | 【远期方向】 |
| MCP/Prompt/Skill 扩展同步 | 【已实现】 | 【已实现】 | 【已实现】 | 【远期方向】 |
| 项目工作区 Capture/Apply | 【已实现】 | 【已实现】 | 【已实现】 | 【远期方向】 |

Grok 当前已覆盖连接、配置、路由和终端；图形会话、会话管理、扩展同步和项目工作区 Capture/Apply 尚未确认完整覆盖，作为后续兼容补齐项进入路线图与验收矩阵。

### 8.6 操作责任矩阵与权威边界（【已确认设计】）

三端协同必须同时回答“谁发起、谁执行、谁记账、谁批准、谁承担恢复责任”。下表使用 RACI：`R`=执行责任，`A`=最终负责/拥有权威，`C`=协同咨询，`I`=知会；一个活动只能有一个 `A`。App 可以代用户发起控制面操作，但不会因为代发而取得目标 Deployment 的数据或账本权威。

| 活动 | 用户/产品 | desktop-orchestrator | local-edge Deployment | cloud Deployment | 发布/运维/安全 | 权威与不可越界 |
| --- | --- | --- | --- | --- | --- | --- |
| 首次入口、路由与费用确认 | A | R | C | C | I | 用户确认 `route_target`、模型、费用与隐私边界；App 不替用户默选高风险回退 |
| CLI 配置投影与恢复 | A | R/A | I | I | C | 本地 App 的配置与项目状态为权威；官方 CLI 只持最小投影 |
| local-edge 安装、启停、升级、排空 | A | R（编排） | R/A（实例与本地数据） | I | C/R（发布制品） | local-edge 自己对 Deployment readiness、请求和本地备份负责；云端不得覆盖本地权威 |
| 云端 Deployment 服务、Key、余额与结算 | C | R（展示/调用） | I | R/A | R（运营） | 云端平台身份、Key 元数据、云端账单和结算以 cloud 为唯一权威 |
| 数据面请求执行与用量记录 | I | C（仅 C 代理时进入数据面） | R/A（local 路由） | R/A（cloud 路由） | I | 实际执行节点写 RequestRecord/Attempt；direct 的最终费用由上游服务商负责 |
| 上游账号录入、轮换与最小解密 | A（显式授权） | C | R/A（local 目标） | R/A（cloud 目标） | C | 执行节点拥有凭据运行权；App 正常路由不得读取明文上游凭据 |
| 贡献托管、转移、撤回 | A（用户授权） | R（编排/展示） | R/A（目标为 local） | R/A（目标为 cloud） | C（闸门/审计） | 专用凭据或显式转移的所有权边界单独记录；跨 Deployment 协调不宣称原子事务 |
| 请求故障、unknown 与补偿 | C | R（提示/查询） | R/A（local attempt） | R/A（cloud attempt） | C | 无法证明未执行时不得静默重试；补偿以追加事件表达，不改写历史事实 |
| 备份、恢复与数据删除 | A（批准破坏性操作） | R（本地编排） | R/A（local 数据） | R/A（cloud 数据） | C/R（演练与保留） | 恢复必须固定目标 Deployment、身份、版本和数据范围；远端来源不得覆盖本地权威 |
| 可信发布、签署与 Go/No-Go | I | C（安装验证） | R（部署验证） | R（部署验证） | A/R（发布工程、安全、运维）+ 产品/架构/测试 Go | manifest、active attestation、activation 状态链和评审 checkpoint 绑定同一候选；部署前另取 fresh checkpoint；当前结论保持 No-Go |
| 安全遏制与事件响应 | I | R（本地隔离） | R/A（节点遏制） | R/A（云端遏制） | A（安全运营） | break-glass 只允许降权、吊销、隔离和停止，并写入第二耐久审计域 |

任何跨 Deployment 操作都必须在界面和审计中同时显示目标、主体、授权范围、数据类别、费用影响、可恢复性与操作 ID。App 只保存最小编排状态和观察快照；它不能以“同步”“健康”“绑定”字段替代执行、账本或权威恢复事实。

## 9. 用户模式与单请求路由来源（local / cloud / hybrid + direct）

本节描述 §6.3 的**维度二：用户模式与单请求路由来源**。`user_mode` 是由 local/cloud 当前可用性计算的只读派生展示值：`none|local|cloud|hybrid`；每次请求的 `route_target` 只有 local、cloud、direct。`none` 表示当前没有本产品中转满足可用条件，不是第四种中转模式；它必须带 `user_mode_reason`，且不得被当作持久化开关或自动改写 RouteBinding 的策略输入。该维度与维度一（链路形态 A/B/C）和维度三（部署拓扑）正交。云端数据面调用只需要有效 API Key；云端登录只影响余额、用量聚合、Key 签发/撤销、贡献治理和服务摘要等管理面能力，不构成数据面请求的前置条件。

`App + direct` 是与用户模式正交的直连旁路目标：只需上游凭据，不要求云端或本地中转可用，界面明确显示“直连上游/未使用本产品中转”，用量与账单以上游为权威。用户模式由可用的本产品中转决定：只有 cloud 时为 cloud，只有 local 时为 local，两者都具备时为 hybrid；二者都不具备时为 `none`。direct 可在任一非空用户模式中作为额外 `route_target`。从未启用任何 local/cloud Endpoint/Binding 的 direct-only 拓扑使用 `user_mode=none + user_mode_reason=no_enabled_relay + route_target=direct`；已配置中转但因健康、凭据或能力暂不可用时使用相应 reason，不能显示成“从未启用”。

| 模式 | 前置条件 | 请求路径 | 权威数据 | 适用场景 | 故障行为 | 用户可见提示 |
| --- | --- | --- | --- | --- | --- | --- |
| 无可用本产品中转（`user_mode=none`） | local/cloud 均不满足可用条件；必须给出 `user_mode_reason`；可有 direct 端点 | direct-only 或中转暂不可用时，已确认的 direct 绑定可由 CLI/代理请求官方上游；无 active direct 绑定时不发请求 | direct 最终以上游为权威；无请求时无账本 | 只使用官方上游，尚未配置中转，或已配置中转当前不可用 | direct 不可用时保持失败关闭，不静默启用/切换中转 | `no_enabled_relay` 显示“未启用本产品中转”；其他 reason 显示“本产品中转当前不可用”及原因；direct active 时另显“当前直连上游” |
| 云端优先/云端模式 | 有可用云端 API Key；管理能力另需登录 | 未叠加 C 的 A/B 路径由 CLI 请求云端节点；A+C 或 B+C 经本地代理请求云端节点 | 云端节点账本 | 低配置成本、持续服务 | 云端不可用时提示；未经 C 的路径切换 RouteBinding 后重连/重发 | “当前绑定云端；实际执行端见用量来源” |
| 本地自主/本地模式 | 已启用本地中转、有可用本地账号 | CLI 或本地代理 → 本地中转 → 上游 | 本地节点账本 | 私有账号、低延迟、不依赖本产品云端控制面 | 本地节点异常时提示，不静默转云端 | “当前绑定本地；实际执行端见用量来源” |
| 云边混合/协同模式 | 云端与本地至少各具备条件 | 按四层策略选择 local/cloud/direct；direct 为目标路由概念；C 可叠加于 A/B | 中转请求由各执行节点记账；direct 最终以上游为权威 | 按成本、时延、隐私和健康选择 | 未叠加 C 的 A/B 路径切换绑定后重连/重发；叠加 C 后才具备代理级逐请求回退前提 | 显示 RouteBinding、实际来源与回退原因 |

用户模式是根据 local/cloud 可用条件计算出的展示分类，不是独立持久化开关。某侧“可用”至少要求：`TargetEndpointHead.administrative_state=enabled`，TargetEndpoint 已完成信任绑定，RouteBinding 生命周期为 active，`endpoint_reference_state=matches_head`、`compatibility_state=compatible`、`config_integrity_state=in_sync`，数据面凭据存在且未被已知撤销或判定无效，且 `runtime_health_state=ready` 或契约明确允许该请求在 `degraded` 下执行；只有配置草稿、过期缓存或匿名发现不能算可用。用户实际修改的是 Endpoint 启用状态、默认路由策略或 RouteBinding。节点瞬时健康只改变 `runtime_health_state` 与模式徽标，不得静默改写 active RouteBinding；Endpoint revision、信任/身份、静态契约或外部配置变化分别进入对应观测轴。界面同时显示 `user_mode`、`user_mode_reason`、“当前 RouteBinding”与“最近一次可确认的实际执行来源”。

### 9.1 登录、来源枚举与 direct 边界

- 【已实现】持有效 API Key 的调用方可以脱离 App 和平台登录会话，直接调用云端数据面。
- 【已实现】当前 App 来源枚举主要是 Local/Lan/Cloud，`ConnectionProfile` 内嵌各客户端 URL；Cloud Profile 既可能指云中转，也可能指官方接口。
- 【已确认设计】迁移前将这类 Profile 标为 `legacy-cloud-ambiguous`，禁止据此自动判断费用、隐私或回退；P0-4/P0-8 完成显式路由字段与事务迁移后再解除歧义标记。
- 【已确认设计】Endpoint、Deployment 与 direct 逐步成为显式路由对象；`route_target=direct` 表示绕过本产品中转直连上游，费用与官方用量以上游为权威。
- 【已确认设计】平台登录失效时，已保存且未撤销的 API Key 仍可用于数据面；管理面显示“暂不可用”，不臆造余额、额度或实时用量。

### 9.2 模式切换与回退边界

- 未叠加 C 的 A 配置驱动路径下，App 在 CLI 发出请求后通常看不到单次 HTTP 结果。当前只能切换 RouteBinding 后重连、重启或重新发起任务，不承诺逐请求跨端点自动回退；A+C 按代理能力与第 13、14 节约束判断。
- B 托管交互链路可利用运行时事件通道向用户解释失败并协调重连/重发，但 B 本身不转发模型请求，不能仅凭事件通道宣称透明的逐请求切换；若 B 同时叠加 C 类代理，则按 C 类能力判断。
- C 协议桥/本地代理链路或具备原生路由能力的 CLI，才有条件在请求链路内执行逐请求回退；自动回退仍须同时满足语义等价、费用边界、隐私级别与用户预授权。
- 任何链路都不得静默改变模型、计费来源、账号族/协议族、工具语义或数据敏感级别；任一属性变化都必须提示确认。

## 10. 身份、账号、凭据与权限模型

### 10.1 身份与令牌分类

必须区分以下身份与令牌，明确使用面、签发方、存储方与禁止复用关系。现状与目标分别成行，避免用一个状态标签掩盖未完成的安全迁移。

| 身份/令牌 | 使用面 | 签发方 | 存储方 | 禁止复用关系 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 官方 CLI 本地身份 | 官方客户端本机登录 | 各官方厂商 | 官方 CLI 配置目录 | 不得被 App 挪作平台身份 | 【已实现】 |
| Deployment 用户会话 access token | 访问目标 Deployment 的用户管理接口；cloud 场景同时承载产品平台登录会话 | 目标 Deployment | App 内存会话；不持久化，过期即丢弃 | 不得当作数据面 API Key，不得跨 Deployment 复用 | 【已实现】 |
| Deployment 用户会话 refresh token | 刷新同一 Deployment 的 access token | 目标 Deployment（cloud 或 local-edge 同一 Sub2API 服务，issuer/audience 均绑定该 Deployment） | DPAPI 保护后持久化并轮换；登出时本地必定清理，并向目标 Deployment 尽力发起 `POST /api/v1/auth/logout` 撤销 | 不得用于数据面调用、不得下发执行节点、不得跨 Deployment 复用 | 【已实现】 |
| 云端调用 API Key（当前） | 调用云端数据面 | 云端 | 仍可能出现在 `profiles.json`、`.bak`、官方客户端配置、VS Code 设置、用户/子进程环境及 App 普通备份中 | 不得用于管理接口 | 【已实现】 |
| 云端调用 API Key（目标） | 调用云端数据面 | 云端 | 安全凭据存储作为唯一源；兼容投影按需、最小、可清理 | 不得进入 App 自建备份、日志、遥测或支持包 | 【已确认设计】 |
| 本地中转管理员授权 | App 管理固定本地 Sub2API | 本地部署/用户 | Windows Credential Manager 或显式环境变量 | 不得复用任何 Deployment 用户会话令牌 | 【已实现】 |
| 本地中转数据面调用 Token | 本机或获准局域网调用本地数据面 | 本地中转 | 本地安全存储 | 不得拥有管理员权限 | 【已确认设计】 |
| 上游 OAuth/PAT/mobile refresh token/代理密码（当前） | 节点调用上游 | 上游/用户 | 执行节点 `accounts.credentials` JSONB 字段以明文子字段存储（本次审计确认无字段级信封加密，`account_repo.go` 直接写 `credentials = $1::jsonb`） | 不得随聚合数据、公开目录或治理视图外泄 | 【已实现】 |
| 上游账号凭据（目标保护） | 节点调用上游 | 上游/用户 | 节点端信封加密、最小权限解密、密钥轮换 | App 正常路由时不得读取，根密钥不得跨节点复用 | 【已确认设计】 |
| 贡献授权与托管凭据 | 用户显式向目标 Sub2API 部署贡献账号 | 用户/上游 | 当前由目标 Sub2API Web/API 接收并形成该节点可调度账号；桌面 App 一键贡献尚未实现 | 不得由“添加本地账号”隐式触发，不得进入公开房间 DTO；目标为云端时按跨域凭据处理 | 【已实现】 |
| 贡献专用凭据与撤回闭环 | 生产级贡献托管 | 用户/目标执行节点 | 专用可撤销凭据、节点端信封加密、删除墓碑与备份清理 | 未达到发布闸门前不得开放真实生产凭据托管；云端目标还须满足跨域传输与托管要求 | 【已确认设计】 |
| 设备凭证 | 设备级授权 | 云端 | 设备安全区 | 不得替代用户身份 | 【远期方向】 |

四个身份族系必须严格区分签发方（issuer）与受众（audience），不得混同（【已确认设计】口径，基于当前 Sub2API 鉴权代码）：

| 身份族系 | issuer | audience | scope | 当前存储 | 撤销语义 |
| --- | --- | --- | --- | --- | --- |
| Deployment 用户会话（access/refresh JWT，`Authorization: Bearer`） | 目标 Sub2API Deployment（云端或 local-edge 同一份服务） | 该 Deployment 的用户管理面 | 本人资源与获准治理，按 `client:*`/`catalog:*`/`usage:read:self` 目标 scope | access 仅内存；refresh DPAPI 持久化 | 本地清理确定；远端 `POST /api/v1/auth/logout` 尽力撤销，远端不可达时撤销未确认 |
| 本机管理员 x-api-key（`x-api-key`） | 该 Deployment 的管理配置（`SettingService.GetAdminAPIKey`，常量时间比较） | 该 Deployment 的 `/admin` 管理面 | 管理员级操作，受 step-up 约束 | Windows Credential Manager 或显式环境变量 | 轮换/更换管理员 Key 后旧值失效；无独立在线撤销端点 |
| 数据面 API Key（`Authorization: Bearer` / `x-api-key` / `x-goog-api-key`） | 该 Deployment | 该 Deployment 的 `/v1` 数据面转发 | 仅数据面调用与同主体自省子集 | 兼容链多载体（迁移债），目标迁入安全存储 | 云端为权威，签发方标记撤销后本地缓存作废 |
| 上游提供商凭据 | 各上游厂商/用户 | 上游 API | 由账号类型决定 | 执行节点 `accounts.credentials` 明文 JSONB | 由上游或用户在上游侧处理，节点侧删除见 §15 撤回闭环 |

一个关键事实：local-edge 与云端运行的是同一份 Sub2API 服务端，因此 **local-edge 同时具备管理员通道（admin x-api-key 或管理员角色 JWT）与普通用户会话 JWT，以及数据面 API Key**（见 `router.go` 同时注册 user/admin/gateway 路由）。不能笼统写成“本地中转只接受本地授权”；准确表述是“App 持有的‘本地管理员授权’即该 Deployment 的 admin x-api-key，与 Deployment 用户会话令牌、云端数据面 API Key、本地数据面 Token 分权、互不兑换”。代码中不存在独立命名的“Deployment JWT”类型，它就是该 Deployment 的标准用户会话 JWT；“产品平台会话”只是在 cloud Deployment 上的产品语义，不是第五类令牌。

【已确认设计】P0-4/P0-5 必须冻结本地数据面 Token 的工程契约：由目标 Deployment 签发或生成，绑定 `deployment_id`、主体、scope、有效期和可选网络约束；只保存 hash 或受保护材料，支持并行轮换、立即吊销与审计；loopback 与 LAN 使用不同凭据，LAN Token 不能因回到本机而自动获得管理权限。原值不进入 URL、日志、遥测或支持包，管理授权、Deployment 用户会话令牌、云端数据面 API Key 与本地数据面 Token 之间不存在隐式兑换。

【已确认设计】登出的精确语义（消除“只写清理”的含糊）：本地 access/refresh 会话必定清理；同时向目标 Deployment 尽力发起 `POST /api/v1/auth/logout` 撤销 refresh。远端可达且成功时为“已撤销”；远端不可达、超时或失败时为“本地已清理、服务端撤销未确认”，界面必须区分这两个状态级别，不得笼统显示“已登出”。为此设计**离线撤销队列**：未确认的撤销请求进入本地队列，在下次可达时重试；配合**短 TTL 与 refresh 轮换**降低撤销未确认窗口内的风险；对长期未能确认撤销的会话给出显式双状态提示与手动强制清理入口。

### 10.2 数据权威归属（【已确认设计】）

| 数据类别 | 权威方 |
| --- | --- |
| 平台账号、套餐/余额、云端 API Key、云端订单、贡献结算 | 云端 |
| 连接元数据、项目工作区、用户偏好、会话索引、本地编排状态 | 本地 App |
| 中转路径的请求级原始用量、上游映射、结果状态元数据、节点健康 | 实际执行中转节点 |
| 直连上游的最终费用与官方用量 | 上游服务商；App 仅保存观察值或估算值 |
| 官方客户端原生会话正文、官方配置格式 | 官方客户端（App 索引与受控写入） |

### 10.3 禁止自动同步的内容

以下内容默认禁止在云边之间自动同步，只能在用户显式授权下、按明确语义进行（【已确认设计】）：

- 上游账号凭据（OAuth access/refresh token、PAT、mobile refresh token、代理密码等）。
- 本地数据库、云端数据库及各自备份文件。
- 对话正文、完整提示词、工具调用参数等会话内容。
- 云端管理员 Token、加密根密钥。
- 本地连接、项目工作区与用户偏好的整体覆盖式回灌。

唯一的有意例外是**显式贡献**：用户在条款告知下主动把账号提交到目标 Sub2API 部署的贡献房间，由目标节点加密托管和调用。目标为云端时，必要凭据会跨越本地—云端安全域；目标为本地 Sub2API 时，提交仍留在本地节点边界。该动作必须单独、显式、可撤销，不能由“添加本地账号”隐式触发，也不等于无凭据共享。贡献凭据的传输、存储、访问控制与撤回删除语义见第 15 节。

### 10.4 数据同步与冲突矩阵

下表固定每类数据的权威源、App 缓存策略、同步方向、云端不可用时的行为与冲突处理（【已确认设计】）。核心原则：平台与账务类数据以云端为权威，本地编排类数据以本地为权威，请求级事实以执行节点为权威，任何一方都不得静默覆盖另一方的权威数据。

| 数据类别 | 权威源 | App 缓存 | 同步方向 | 云端不可用时 | 冲突处理 |
| --- | --- | --- | --- | --- | --- |
| 平台身份/余额 | 云端 | 只读短缓存 | 云端 → App | 标记“暂不可用”，不臆造余额 | 以云端为准，本地缓存作废 |
| 云端 API Key 元数据与密钥值 | 云端 | 元数据可缓存；密钥值当前仍可能进入 legacy profiles 与兼容投影，目标迁入安全存储 | 云端 → App；签发/录入时进入本地兼容链 | 已保存且未撤销的 Key 可继续尝试数据面调用 | Key 状态以云端为准；本地迁移不得改变云端撤销事实 |
| 模型与价格 | 云端 | 带版本号/ETag 缓存 | 云端 → App | 用上次缓存并提示时效 | 以更高 pricing_version 为准 |
| 聚合用量 | 云端/节点聚合或 direct 上游观察 | 带 `source`、可空 `deployment_id`、`provider/billing_scope`、主体范围、`snapshot_at`、`cache_state` 与版本的只读快照 | App 主动向节点/上游适配器查询 | 展示最近缓存、TTL 与时间，不伪装实时数据 | 同一来源与 billing scope 内按新快照覆盖；跨来源保留边界 |
| 请求级原始用量 | 实际执行节点 | App 不将其缓存副本视为权威 | 节点自留；必要诊断按最小字段查询 | 由节点本地记账 | 各节点独立，聚合时保留来源 |
| 本地连接/项目/偏好 | 本地 App | 本地权威存储 | 本地内部 | 完全可用 | 本地为准，云端不得覆盖 |
| App 连接与客户端投影凭据 | 本地 App/官方客户端各自持有必要载体 | 当前兼容链可含 `profiles.json`、`.bak`、官方配置、环境变量和 App 备份；目标由引用化安全源生成最小投影 | 仅在本机兼容层传播，不自动上云 | 已保存投影可按其有效性使用 | 本地迁移器事务处理；远端不得覆盖或回灌 |
| 执行节点上游账号凭据 | 实际执行节点 | App 不缓存正常路由所需的上游秘密 | 节点 → 上游；默认不跨云边 | 节点按本地能力继续运行 | 节点权威；字段加密与轮换不得改变账号所有权 |
| 贡献凭据 | 由用户选择的贡献模式决定，不预设来源端与目标部署始终双副本 | App 不额外复制；当前由目标 Sub2API Web/API 接收 | 用户显式授权后进入目标部署；目标为云端时跨越云边安全域 | 目标部署不可用时停止新的贡献操作 | 默认推荐专用凭据；若选择转移，目标部署成为唯一运行副本，来源端立即停用；撤回按删除 SLA 执行 |
| 官方会话正文 | 官方客户端 | 只建索引，不搬正文 | 客户端本地 | 完全可用 | 客户端为准 |
| 云端策略 | 云端 | 带版本缓存 | 云端 → App（建议/约束） | 用缓存策略 | 仅作建议或版本化约束，不静默改本地配置 |

明确边界：云端策略进入 App 时只能作为建议或受版本控制的约束，不能直接改写用户已保存的本地路由与偏好；本地项目与偏好不接受云端静默覆盖；云端备份不得覆盖本地数据库。

### 10.5 凭据传播与落盘矩阵

凭据安全不能只描述“存在哪里”，还要列清全部传播与残留载体。以下矩阵把当前事实与目标约束拆开表达：

| 凭据 | 当前处理 | 必要传播 | 目标约束 | 状态 |
| --- | --- | --- | --- | --- |
| 云端登录密码（当前） | 登录时提交，不作为 App 会话凭据持久化 | App → 云端登录 API | 仅驻留请求生命周期 | 【已实现】 |
| 云端登录密码（目标验证） | 以敏感金丝雀覆盖登录、错误和崩溃路径 | 不传播到日志、遥测或支持包 | 任一 sink 均无密码；非真实 loopback HTTP 登录直接拒绝 | 【已确认设计】 |
| Deployment 用户会话 access token | App 内存会话 | App → 对应 Deployment 用户管理 API | 只驻留内存，过期即丢弃；cloud 产品平台会话亦遵循此规则 | 【已实现】 |
| Deployment 用户会话 refresh token | DPAPI 保护后持久化、轮换，登出时撤销/清理 | App → 同一 Deployment 刷新 API | 不下发数据面或其他 Deployment | 【已实现】 |
| 云端数据面 API Key（当前） | 可能存在于 `profiles.json` 及 `.bak`，并进入官方 CLI 配置、VS Code 设置、用户/子进程环境和 App 普通备份 | App/官方 CLI/托管 CLI → 云端数据面 | 仅说明兼容现状，不视为安全引用化已经完成 | 【已实现】 |
| 云端数据面 API Key（目标） | 安全凭据存储作为唯一源 | 按客户端要求生成最小临时投影 | 投影事务化、最小字段、受限 ACL、有清单；切换/退出清理并做残留扫描；App 自建备份不含秘密 | 【已确认设计】 |
| 官方 CLI 投影、用户/子进程环境与本地备份（当前） | `profiles.json.bak`、普通备份目录、官方认证/设置文件和环境变量可能复制或保留秘密 | App → 官方客户端兼容层 | 所有载体纳入迁移清单，不能只清理 `profiles.json` | 【已实现】 |
| 官方 CLI 投影、用户/子进程环境与本地备份（目标） | 兼容投影保留必要最小字段；App 自建普通备份一律不含秘密 | 仅在本机受控传播 | 原子写入、权限收敛、退出恢复、过期清理和残留扫描可验证 | 【已确认设计】 |
| 本地管理员 API Key | Windows Credential Manager 或显式环境变量 | App → 固定本地 Sub2API 管理 API | 仅用于管理面，不进入客户端数据面配置 | 【已实现】 |
| 本地数据面 Token | 尚未形成独立统一契约 | 官方 CLI/托管进程 → 本地中转 | 与管理员授权分权，默认随机生成，可轮换、可撤销 | 【已确认设计】 |
| 上游 OAuth/PAT/代理密码（当前） | 存于 `accounts.credentials` 明文 JSONB，本次审计确认无字段级加密 | 执行节点 → 上游；当前贡献由目标 Sub2API Web/API 接收 | 治理视图与日志保持无凭据/脱敏 | 【已实现】 |
| 上游 OAuth/PAT/代理密码（目标） | 节点端信封加密、最小解密、轮换与备份策略 | 显式贡献时经 TLS 进入目标 Sub2API 部署的托管边界；目标为云端时跨越云边安全域；正常执行时仅由节点最小解密并发送给上游 | 正常路由时 App 不读取；录入/贡献仅短暂处理，不进入日志、遥测、公开 DTO 或支持包 | 【已确认设计】 |

官方客户端可能要求把密钥投影到原生配置或进程环境，因此文档不承诺“秘密绝不落盘”。目标是让安全凭据存储成为唯一源，把必要投影限制在最小范围，并通过配置事务、文件权限、清单、退出恢复、备份排除和残留扫描降低暴露面。若未来需要备份或迁移凭据，应另行设计用户显式授权、独立加密、独立口令/密钥与可审计导入的“安全导出”功能，不得复用 App 普通备份通道。

## 11. 连接、端点、渠道、模型、项目、会话与路由领域模型

本节把控制面意图和数据面事实分开：RouteBinding 记录“打算走哪里”，RequestRecord 记录“入口接受了哪一个逻辑请求”，RequestAttempt 记录“为该请求实际做过哪些上游尝试”。三者不能互相冒充，也通常不是一一对应关系。

### 11.1 领域对象与当前实现边界

| 对象 | 定义 | 当前实现边界 | 状态 |
| --- | --- | --- | --- |
| Source/ConnectionProfile | App 中的来源与连接配置 | 来源枚举主要为 Local/Lan/Cloud；一个 Profile 内嵌多个客户端 URL | 【已实现】 |
| RouteBinding | 客户端、项目、进程、配置与目标端点之间的绑定事实 | 现有连接应用与项目启动能力提供基础，尚未形成统一持久化对象 | 【已确认设计】 |
| TargetEndpoint | 统一的可调用目标入口，含地址、协议、TLS 身份与鉴权方法，是 App 选择粒度；用 `owner_kind` 区分归属（见 §11.1.1） | 当前只有 `Sub2ApiEndpointTarget(ProfileId, Uri, ...)`，`endpoint_id`/`owner_kind`/`deployment_id` 均无独立字段，内嵌在 ConnectionProfile | 【已确认设计】 |
| Deployment | 具有稳定 `deployment_id`、独立权威数据和信任边界的一套本地/云端部署 | 当前 App 与 Sub2API 后端均无 `deployment_id`/`node_id` 字段（本次审计确认），尚未形成统一部署身份与信任绑定模型 | 【已确认设计】 |
| Instance | Deployment 内的一次进程/容器运行实体，负责实际接收请求 | 后端无 `instance_id`（仅支付渠道有 `provider_instance_id`）；健康检查停留在地址/进程级，尚无实例租约与 readiness 模型 | 【已确认设计】 |
| direct | 绕过本产品中转直连官方或其他上游 | 当前 Cloud Profile 也可能表达官方接口，语义尚未独立 | 【已确认设计】 |
| Channel/Group/Account | Channel 是聚合多个 Group 的定价配置层；Group 是准入分组，持有 API Key/订阅/用量边；Account 是账号池，经 `account_groups` 与 Group 多对多。上游协议适配不由 Channel 承担，而按 `account.Platform + account.Type` 在网关分支判定 | 均为 Deployment 级共享配置，由 Sub2API 持有与调度；App 只管理获准的治理视图，不介入内部账号选路 | 【已实现】 |
| RequestRecord | 节点内部一次逻辑请求的原子幂等登记与状态查询对象，可关联零个或多个 RequestAttempt | 现有 request_id/指纹提供基础；稳定逻辑请求与查询对象尚未落地 | 【已确认设计】 |
| RequestAttempt | 节点内部一次真实上游尝试与账本事实 | 现有 UsageLog/request_id 提供基础；独立 attempt 语义待落地 | 【已确认设计】 |
| Project/Session | 工作环境与官方/图形会话上下文 | 项目、会话索引、终端与图形聊天已有实现 | 【已实现】 |

#### 11.1.1 TargetEndpoint 统一模型与不变量（【已确认设计】）

早期草案把 Endpoint 定义为“Deployment 的网络入口”，却又要求 direct 绑定 `endpoint_id` 而 `deployment_id` 为空——两者互相矛盾：若端点必然从属于 Deployment，direct 就不该有端点却没有部署。本文选定**统一 TargetEndpoint** 方案（对比 direct 单独拆 `UpstreamEndpoint` 的方案，统一模型更利于 RouteBinding、诊断和用量在同一字段轴上贯通），用 `owner_kind` 表达归属，在 Schema、API 与不变量三处保持一致：

| 字段 | 含义 |
| --- | --- |
| `endpoint_id` | TargetEndpoint 全局唯一 UUID（所有路由目标都有，含 direct） |
| `endpoint_revision` | 不可变 revision；地址、证书指纹、鉴权方式或信任绑定变化即新建 revision |
| `owner_kind` | `deployment` \| `upstream_provider`，区分“本产品中转部署”与“外部上游提供商” |
| `issuer_kind / issuer_id` | 逻辑端点签发主体；Deployment 端点为 `deployment/deployment_id`，direct 端点为 `desktop_installation/installation_id` |
| `deployment_id?` | 仅 `owner_kind=deployment` 时非空；`upstream_provider`（即 direct）时必为空 |
| `deployment_role?` | 仅 Deployment 端点使用，固定为认证能力契约中的 `local-edge` 或 `cloud-control-data-plane` |
| `provider_id? / billing_scope?` | 仅 `owner_kind=upstream_provider` 时用于标注上游账单域 |
| `origin`, `base_path`, `protocol`, `tls_identity?`, `auth_method` | 按 §16.5.2 分离并规范化的来源与路径前缀、协议、TLS 身份（指纹/CA/mTLS）、鉴权方式；整段 URL 形态的 `base_uri`/`normalized_base_uri` 仅可作为 legacy 迁移**输入**，迁移器解析后一律拆为 `origin + base_path`，规范对象、签名 payload 与任何输出都禁止再出现这两个 legacy 字段 |
| `trust_class` | `loopback` \| `lan_private` \| `public`（见 §16.1 信任分级） |
| `administrative_state` | 存放在 TargetEndpointHead 的 `enabled` \| `disabled` 管理状态；通过 Head 的 `strong_etag` + 必填 `If-Match` CAS 修改（与 §11.4 RouteBindingHead 同一 `head_version`/`strong_etag` 机制），不写入不可变 revision |

TargetEndpoint 不变量：

1. `owner_kind=deployment` ⇒ `deployment_id` 非空且 `provider_id/billing_scope` 为空；`owner_kind=upstream_provider` ⇒ `deployment_id` 为空且必须给出 `provider_id/billing_scope`。二者不可兼有，也不可两空。
2. RouteBinding 的 `route_target=local|cloud` 只能引用 `owner_kind=deployment` 的 TargetEndpoint，并分别要求认证后冻结的 `deployment_role=local-edge|cloud-control-data-plane`；`route_target=direct` 只能引用 `owner_kind=upstream_provider` 的 TargetEndpoint。App 不得把同一 Deployment 任意重标为 local 或 cloud。据此，“direct 有 `endpoint_id` 但无 `deployment_id`”被 `owner_kind` 合法表达，不再矛盾。
3. RouteBinding 固定 `endpoint_id + endpoint_revision`（可选 `trust_binding_version`）；历史 revision 按当时快照解释，禁止原地更新端点让旧绑定静默指向新地址或新证书。
4. `trust_class` 由地址与信任绑定共同决定，不能仅凭“私网地址”判为可信（见 §16.1）；`public` 与 `lan_private` 强制 TLS，仅 `loopback` 允许非贡献场景使用 HTTP。
5. 账本与用量按 `owner_kind` 归属：`deployment` 目标的请求级事实权威在该 Deployment；`upstream_provider`（direct）目标的最终费用权威在上游，App 只存观察值/估算值。

持久化键与签发权作为 P0-4/P0-8 实现不变量：`TargetEndpoint(endpoint_id UUID PRIMARY KEY, owner_kind, issuer_kind, issuer_id, created_at)` 是逻辑主表；不可变 revision 表使用 `PRIMARY KEY (endpoint_id, endpoint_revision)`，并以外键回指逻辑主表。`TargetEndpointHead(endpoint_id PRIMARY KEY, current_revision, administrative_state, head_version, strong_etag)` 通过复合外键指向 current revision，创建新 revision 本身不改变 current；`head_version` 为单调递增整数，`strong_etag` 由 `head_version` 派生，二者与 §11.4 `RouteBindingHead(scope_key PRIMARY KEY, current_binding_id, current_revision, head_version, strong_etag)` 采用同一并发原语。切换 Head 或启停 Endpoint 必须携带必填 `If-Match: <strong_etag>` 做 CAS，数据库以 `affected_rows=1` 为唯一成功判据并返回递增后的 `head_version` 与新 `strong_etag`；缺 `If-Match` 返回 428，格式错误 400，过期/不匹配 412（口径与 §11.4 一致）。`current` 只表示该逻辑端点的推荐 revision，`active` 只属于 RouteBinding 生命周期，两者不得混用。Head 切换后，仍引用旧 revision 的绑定保持原生命周期，但 `endpoint_reference_state=head_advanced`，供用户确认后创建新 revision，不改写历史解释。

`owner_kind=deployment` 的 `endpoint_id` 在 Deployment 初始化或发布一个可路由协议入口时由目标 Deployment 生成全局唯一 UUID并持久化，逻辑主表记录 `issuer_kind=deployment` 与该 `deployment_id`。App 先以同一数据面或管理身份取得受限 self capabilities 并登记其中的 `subject_scope_hash`，再生成一次性、至少 128 bit 的高熵 `client_nonce`，调用 §13.2 的 `POST /api/v1/client/endpoint-descriptor`；请求只携带 nonce、目标协议和 App 已连接的规范化 origin/base path，服务端不得把调用方提供的 URL 原样回显为可信入口。App 只有在响应 nonce 与本次未消费挑战精确匹配，受签 `subject_scope_hash` 与 self view 一致，且 descriptor 与当前 TLS/mTLS/loopback 传输证据、认证主体实际可调用入口、`deployment_id` 和 `server_role` 全部一致后，才把 EndpointCandidate 签发为可被 RouteBinding 引用的 TargetEndpoint revision；响应处理后立即消费该 nonce，历史或跨主体响应不得重放。

EndpointDescriptor 的稳定 `descriptor_body` 与挑战 envelope 分离：body 只描述一个 TargetEndpoint revision 的端点身份及单一规范入口，`descriptor_body_digest` 不包含 nonce 或调用主体，避免每次挑战/主体变化产生新的 endpoint revision；envelope 另含 `subject_scope_hash`，它是 Deployment、principal、audience 与有效 scopes 的版本化规范摘要，不含凭据或秘密。body digest 与 envelope signature 均使用 §16.5.1 的统一 object helper，签名通过 body digest 绑定完整 body，并直接绑定 subject scope、`client_nonce`、签发/失效时间、签名 keyset/吊销版本和当前传输。认证前仅保存 `EndpointCandidate(candidate_id UUID PRIMARY KEY, normalized_origin, normalized_base_path, protocol, auth_method, observed_tls_identity?, discovery_source, discovered_at, legacy_classification?)`，候选对象不保存秘密，也不能被 active RouteBinding 引用。`owner_kind=upstream_provider` 的 direct 端点由 App 以 `installation_id` 为 issuer 生成全局唯一 UUID。`trust_binding` 至少固定 `trust_binding_version`、candidate/endpoint 引用、稳定 descriptor body digest、descriptor signing-key hash、TLS 证据 hash、可空 `deployment_id`、确认主体与时间；subject scope 与挑战结果进入授权/审计证据，不改变稳定 endpoint 身份。证书、descriptor 稳定 body、签名主体或 Deployment 身份变化必须新建 trust binding/endpoint revision。Deployment 首次初始化生成并持久保存唯一 `deployment_id`；同一 Deployment 的灾备恢复必须保留该 ID，“恢复为新部署”必须显式重签发 ID 并使旧 trust binding 失效；`instance_id` 每次进程/容器启动新建。

当前实现只有 `Sub2ApiEndpointTarget`（ProfileId + Uri + Kind），没有 `endpoint_id`/`owner_kind`/`deployment_id`；统一 TargetEndpoint 与其不变量随 P0-4（契约）与 P0-8（显式 `route_target` 迁移）落地。旧 ConnectionProfile 的一个 BaseUrl 与多个 per-CLI URL 不强行合并：迁移器先把旧 URL 解析为规范化 `(origin, base_path, protocol, auth_method, trust_binding)`，每个不同元组创建独立 endpoint revision；同一 Deployment 的多协议入口可以共享 `deployment_id`，每个客户端 RouteBinding 指向实际 revision。无法判定 cloud/direct 的条目保持 `EndpointCandidate + legacy-cloud-ambiguous`，用户明确确认前不得 active。

### 11.2 领域对象关系

```mermaid
graph LR
    subgraph App控制面
        Global[全局默认] --> Policy[路由策略合并]
        Client[客户端策略] --> Policy
        Project[项目策略] --> Policy
        Task[单次任务覆盖] --> Policy
        Policy --> Binding[RouteBinding]
        Profile[ConnectionProfile] --> Binding
        Session[会话] --> Binding
        Project --> Workspace[MCP / Prompt / Skill]
        Binding --> Target[route_target + Endpoint]
    end

    Target -->|local/cloud| Deployment[Deployment]
    Target -->|direct| Direct[官方或其他上游]

    subgraph 节点内部调度
        Deployment --> Instance[Instance]
        Deployment --> Channel[Channel]
        Instance -.使用.-> Channel
        Channel --> Group[Group]
        APIKey[API Key] --> Group
        Group <--> Account[Account]
        Account --> Credential[Credential]
        Deployment --> Request[RequestRecord]
        Instance -.接收.-> Request
        Request --> Attempt[RequestAttempt / UsageLog]
        Instance -.执行.-> Attempt
    end

    subgraph 贡献域
        Room[ContributionRoom] --> Membership[ContributionRoomAccount]
        Membership --> Account
        User[User] --> Preference[RoomPreference]
        APIKey --> Preference
        Preference --> Room
    end
```

节点内部关系至少遵循以下事实：`Deployment → Instance`、`Deployment → Channel`、`Instance 使用 Channel`、`APIKey → Group`、`Channel → Group`、`Group ↔ Account`、`Deployment → RequestRecord → RequestAttempt`；Instance 接收逻辑请求并执行尝试，但 RequestRecord、RequestAttempt、Channel/Group/Account 都属于 Deployment 级权威数据，不能随实例重启丢失。Channel/Group/Account 是 Deployment 级共享配置，Instance 是运行实体，不拥有独立账号池。贡献域遵循 `ContributionRoom → ContributionRoomAccount → Account` 以及 `User/APIKey → RoomPreference → ContributionRoom`。App 选择 `route_target` 与 TargetEndpoint；Deployment 内部按 Group/Account 完成账号选路，上游协议与端点由 `account.Platform + account.Type` 分支判定，Channel 仅承担分组聚合与定价；App 不直接选择节点内部账号，也不读取其上游凭据。

### 11.3 路由策略优先级（【已确认设计】）

路由决策按以下优先级合并，高优先级覆盖低优先级：

**单次任务覆盖 > 项目策略 > 客户端策略 > 全局默认。**

- 单次任务覆盖：本次任务临时指定目标端点或模型，最高优先级。
- 项目策略：项目工作环境内的默认路由。
- 客户端策略：按 Codex、Claude Code、Gemini、Grok 分别设定。
- 全局默认：兜底。

合并结果落为 RouteBinding，只表达 `route_target`（local/cloud/direct）、具体 TargetEndpoint、客户端/项目关联和配置落地状态。当前节点在 UsageLog/request_id 基础上按 Group/Account 完成账号选路与用量记录，上游协议和端点由 `account.Platform + account.Type` 分支判定，Channel 仅承担分组聚合与定价；目标状态下，入口先为逻辑请求原子创建或复用 RequestRecord，再为每次真实上游尝试创建独立 RequestAttempt。未叠加 C 的配置驱动路径切换 RouteBinding 后必须重连、重启或重发任务；A+C 若保持稳定代理入口，可由代理在既定授权与语义等价边界内切换内部目标。任何切换都不能改写已经发生或正在执行的请求事实。

### 11.4 RouteBinding 正式模型与不变量

【已确认设计】RouteBinding 采用不可变 revision，保存的是“某一作用域在某一时刻准备如何执行”的控制面事实。最小字段如下：

| 字段组 | 最小字段 | 不变量 |
| --- | --- | --- |
| 身份与并发 | `binding_id`, `revision`, `scope_key`, `scope_type`, `scope_id` | revision 表使用 `PRIMARY KEY (binding_id, revision)`；revision 本身不携带并发 ETag；`scope_key` 非空且规范化；同一 scope 同时最多一个 current/active revision；更新创建新 revision，不原地篡改旧绑定 |
| 工作上下文 | `client`, `project_id?`, `session_id?`, `task_id?` | 高层为空表示继承；显式禁用使用独立枚举，不能用空值混同 |
| 链路方式 | `interaction_mode=A\|B`, `proxy_mode=none\|C` | 可表达组合固定为 A、B、A+C、B+C，不产生 A+B |
| 目标 | `route_target`, `endpoint_id`, `endpoint_revision`, `endpoint_owner_kind`, `deployment_id?`, `trust_binding_version?`, `requested_model`, `expected_effective_model?` | local/cloud 只能引用 `owner_kind=deployment` 且角色匹配的 TargetEndpoint（`deployment_id` 非空）；direct 只能引用 `owner_kind=upstream_provider` 的 TargetEndpoint（`deployment_id` 为空）；`endpoint_owner_kind` 只是不可变审计快照，必须与所引 revision 相同，调用方不能独立指定矛盾值；历史绑定不能把旧 `endpoint_revision` 重新解析成新地址或证书 |
| 策略与安全 | `fallback_policy`, `cost_limit?`, `data_sensitivity`, `credential_ref`, `user_confirmation_required` | `credential_ref` 只能引用安全存储，不保存秘密；回退不得越过费用、协议、模型族或敏感级别边界 |
| 生命周期 | `lifecycle_state=draft\|applying\|active\|apply_failed\|restoring\|restored\|superseded\|retired`, `activated_at?`, `superseded_at?`, `retired_at?` | 只表达配置提交与恢复历史；新 revision 只能在 §12.1 的同一元数据 commit point 与所属 AtomicityGroup/ConfigTransaction `committed` 同时进入 `active`，不能先后分步成立；终止状态不可被原地复活 |
| 观测状态分轴 | `endpoint_reference_state=matches_head\|head_advanced\|trust_revoked\|identity_changed\|unknown`；`runtime_health_state=ready\|degraded\|draining\|unavailable\|unknown`；`compatibility_state=compatible\|incompatible\|unknown`；`config_integrity_state=in_sync\|externally_modified\|conflict\|unknown` | 四轴互不代替，也不改写不可变路由 payload；观测恢复时只更新对应轴，RouteBinding 生命周期可继续保持 active，路由变化仍须创建新 revision |
| 落地状态 | `atomicity_group_id?`, `desired_state`, `applied_state`, `reconnect_action`, `runtime_activation_state` | 受影响的 ConfigTransaction 通过 `ConfigTransactionBinding` 关联，不在 RouteBinding 保存单一事务 ID；`active` 只说明配置与 Head 已提交；运行时是否已经读取新配置使用 `not_required\|pending_user_action\|starting\|effective\|failed\|unknown` 独立表达，不证明请求成功，也不产生费用 |
| 诊断 | `trace_id`, `reason_code`, `created_at`, `supersedes_revision?` | 可解释、可追踪；历史 revision 与在途会话不可被新绑定重写 |

Endpoint 配置同样采用不可变 revision；地址、证书指纹、授权方式或信任绑定变化时创建新 revision。TargetEndpointHead 推进时，旧 active binding 的 `endpoint_reference_state` 变为 `head_advanced`；信任吊销或身份突变分别变为 `trust_revoked|identity_changed`。这些变化不改写 binding 固定的 endpoint revision，也不自动切换目标。节点健康只更新 `runtime_health_state`，能力主版本或必需 feature 不兼容只更新 `compatibility_state`，外部配置变化只更新 `config_integrity_state`。用户确认新目标后创建新 RouteBinding revision，并在 commit point 将旧 revision 置为 `superseded`。

`scope_key` 由版本化规范元组生成，至少覆盖必填 `client` 以及实际存在的 project/session/task 层级；v1 冻结为 `object_type=route-scope-key`，其 unsigned preimage 为 `JCS({"scope_version":"1","client":<规范字符串>,"project":<值或"absent">,"session":<值或"absent">,"task":<值或"absent">})`，各字符串先按 §16.5.2 的 ASCII/Unicode 规则规范化，缺省层级使用字面量 `absent`，禁止把 null、空串和缺省混为一类，再按 §16.5.1 helper 计算 `sha256:<base64url-no-pad>`。`scope_type/scope_id` 等描述字段必须重新规范化后与同一个 `scope_key` 校验，不得依赖含 NULL 的多列唯一索引推断作用域。单独的 `RouteBindingHead(scope_key PRIMARY KEY, current_binding_id, current_revision, head_version, strong_etag)` 通过 `(current_binding_id, current_revision)` 复合外键引用 RouteBinding revision；激活使用必填 `If-Match` 做 CAS，数据库必须以 `affected_rows=1` 作为唯一成功判据并返回新的强 ETag。缺少 `If-Match` 返回 428 `PRECONDITION_REQUIRED`，格式错误返回 400，过期或不匹配返回 412 `PRECONDITION_FAILED`；409 仅保留给幂等指纹或业务冲突。该 CAS 与新旧 revision 生命周期及 AtomicityGroup commit 的原子顺序见 §12.1。

检测到官方配置被用户或其他工具外部修改时，进入冲突状态并展示 diff；App 只有在当前文件 hash 仍等于自己最近一次写入 hash 时，才可以自动恢复或覆盖。RouteBinding 与治理上下文分离，切换执行目标不会连带切换账号删除、贡献或中转停止等管理操作的目标 Deployment。

### 11.5 贡献房间领域语义

以下为当前 Sub2API 代码确认的事实（【已实现】）：

- 房间 owner 由当前登录用户隐式确定；`owner_user_id` 唯一，当前一个用户最多拥有一个房间。
- 房间创建时固定为 active、public，创建后才能修改可见性和其他治理属性。
- API Key 可以选择多个他人的公开房间，不能选择自己拥有的房间；`ContributionRoomAccount.account_id` 唯一，同一贡献账号当前只能加入一个房间。
- 房间共享预算按被贡献账号的原始 Token 成本 USD 计量，不等于消费者最终计费金额。

是否放开“一用户一房间、一账号一房间”属于【待决策】。公开目录字段最小化、贡献凭据形态与删除 SLA 见第 15、16、21 节。

### 11.6 模型层级与跨协议兼容

`requested_model` 表达用户或客户端请求的模型；`effective_model` 是节点在渠道和账号调度后实际生效的模型。二者差异必须可见、可审计，回退或映射造成的模型变化不得对用户隐藏。

【已实现】本地协议桥与跨客户端路由已形成差异化基础，包括 Claude Code → OpenAI Responses 本地桥及 Codex → Claude/Grok 路由。后续兼容基线必须覆盖协议完整性、模型族、流式、工具调用、审批、取消、超时、重试、断连、错误语义和回退；不兼容上下文不得跨账号族或协议族静默切换。

## 12. 核心流程与关键时序

系统存在三类链路，必须分开描述，不能把 App 画成所有 CLI 请求的代理：

- **A 配置驱动**：App 做能力预检与路由选择，将 RouteBinding 受控写入官方 CLI 配置，或注入新进程参数/环境。未叠加 C 时 CLI 直接请求 local/cloud/direct 目标，App 不在逐请求数据面内；A+C 时配置指向稳定本地代理入口，由代理请求实际目标。
- **B 托管交互**：Codex app-server、Claude stream-json、Gemini ACP 等由 App 管理进程、输入、工具事件、审批和取消，实际模型请求仍由相应 CLI 按 RouteBinding 发出；B 可以未经 C 直接请求目标，也可以叠加 C 类代理。
- **C 协议桥/本地代理**：`ClaudeGptBridgeServer` 等 App 侧代理组件位于数据面，调用方先进入本地代理，再由代理请求中转节点或上游；C 可叠加在 A 或 B 上。

正式 RouteBinding 是 App 侧连接、项目、进程和配置的绑定事实；正式 RequestRecord 是执行节点入口的逻辑请求、幂等与查询事实；正式 RequestAttempt 是该记录下的真实上游尝试与账本事实，三者均属于【已确认设计】。当前分别以等价的连接/来源切换行为和 UsageLog/request_id 为实现基础。切换绑定不会改变已发生或正在执行的请求事实，App 也不能根据绑定状态推断每个请求已经成功执行。

核心流程覆盖：

1. **首次使用**：安装 App。用户可登录云端使用管理面，也可直接录入有效 API Key 使用数据面；能力契约属于【已确认设计】。
2. **添加云端连接**：签发或录入最小权限 API Key。当前按兼容链写入 profiles、官方 CLI 配置或环境；P0 迁移完成后由安全凭据存储作为唯一源并生成最小临时投影。
3. **启用本地中转**：按需安装启动本地 Sub2API（Windows 原生优先，Docker Compose 回退），导入本地账号。此步可选，非前置条件。
4. **建立/应用 RouteBinding**：按 11.3 优先级选择 route_target 与 endpoint。A 落地客户端配置或进程参数；B 绑定托管 CLI 进程及交互通道；若叠加 C，再把数据面目标绑定到本地代理入口。当前已有等价的连接/来源切换行为，正式统一、持久化的 RouteBinding 对象属于目标设计。
5. **配置事务与退出恢复**：配置写入采用事务与崩溃恢复（【已确认设计】），启动恢复工作台状态，退出还原应用启动前的官方配置（【已实现】的恢复能力，事务化为目标增强）。
6. **请求执行与用量**：未叠加 C 的 A/B 路径由 CLI 直接发出模型请求；叠加 C 时请求先进入本地代理。当前中转以 UsageLog/request_id 记录请求事实；目标状态下，节点先创建或复用 RequestRecord，再为每次真实上游尝试创建独立 RequestAttempt 并记录原始用量。direct 的最终账单权威在上游，App 事后主动查询带来源与时间的聚合快照。
7. **异常回退**：节点内部账号池回退由节点负责。跨端点切换必须遵守链路能力：未叠加 C 的 A/B 路径主要切换 RouteBinding 后重连/重启或重发；叠加 C 的路径或 CLI 原生能力才具备逐请求回退前提。任何自动回退均须语义等价且已预授权。
8. **显式贡献/撤回**：【已实现】当前由目标 Sub2API Web/API 接收凭据并形成目标节点可调度账号；目标为云端时该动作跨越云边安全域。【已确认设计】桌面 App 增加一键贡献、目标部署与条款确认、进度和房间管理。上传失败不得改变来源账号状态；撤回先停止新调度，再按贡献模式与删除 SLA 清理托管副本并保留无秘密审计结果。
9. **云端不可用本地继续**：已启用本地中转时可继续本地模式，App 明确提示降级，云端权威数据标记暂不可用；恢复联网后只刷新云端数据，不覆盖本地项目与偏好。

未叠加 C 的配置驱动路径存在明确边界：App 在 CLI 发出请求后通常看不到单次 HTTP 失败，也无法可靠为每个官方 CLI 请求注入 `request_id`。当前只能在用户确认后切换 RouteBinding，随后重连、重启或重新发起任务。A+C 将 CLI 固定指向本地代理入口，代理因处于请求链内可观察请求、传播 ID，并在用户预授权、语义等价和结果状态已解析时切换内部目标；CLI 原生路由/重试能力按同样边界处理。

`request_id` 的生成服从实际链路：支持稳定逻辑请求标识时由客户端提供或由数据面入口在原子登记 RequestRecord 时生成；当前客户端自带但不具备稳定幂等语义的 ID 只能作为外部关联值保存，不能直接升级为账本唯一键。每个 RequestAttempt 关联同一 RequestRecord 的 `request_id`，并拥有独立 `attempt_id`。App 可以记录绑定事实或任务启动级 `trace_id`，但在不处于数据面时不得声称掌握每个请求的 ID。

未叠加 C 的配置驱动（A 类）经 local/cloud 中转节点的目标关键时序：

```mermaid
sequenceDiagram
    participant USER as 用户
    participant APP as App(控制面)
    participant DEPLOYMENT as Deployment(经 Endpoint/Instance)
    participant CLI as 官方 CLI
    participant UP as 上游
    USER->>APP: 建立/应用连接绑定，启动项目
    APP->>DEPLOYMENT: 经 Endpoint 预检 capabilities
    DEPLOYMENT-->>APP: 能力、Deployment 身份与版本
    APP->>APP: 生成/更新 RouteBinding（选定 endpoint）
    APP->>CLI: 事务写入配置或注入参数/环境
    Note over APP,CLI: 目标 RouteBinding 已落地；此后 App 不在逐请求数据面内
    CLI->>DEPLOYMENT: 经 Endpoint 直接请求
    DEPLOYMENT->>DEPLOYMENT: Instance 接收请求；原子创建/复用 RequestRecord(request_id)
    DEPLOYMENT->>DEPLOYMENT: 每次真实上游调用创建 RequestAttempt(attempt_id)
    DEPLOYMENT->>UP: 调用上游(Deployment 账号池内部调度)
    UP-->>DEPLOYMENT: 返回响应
    DEPLOYMENT-->>CLI: 转发响应
    DEPLOYMENT->>DEPLOYMENT: 当前写 UsageLog；目标按 RequestRecord 聚合各 RequestAttempt
    APP->>DEPLOYMENT: 主动查询聚合用量快照
    DEPLOYMENT-->>APP: 返回字段白名单快照（含来源与时间）
    USER->>APP: 需要切换端点
    APP->>CLI: 更新 RouteBinding 后重连/重启或重发
```

B 在上述链路旁增加 App 与托管 CLI 之间的事件通道，未叠加 C 时模型请求与响应仍沿 `CLI → DEPLOYMENT/UPSTREAM → CLI`。叠加 C 后，CLI/调用方先请求本地代理：经中转时请求/响应为 `CLI → PROXY → DEPLOYMENT → UPSTREAM → DEPLOYMENT → PROXY → CLI`；direct 时为 `CLI → PROXY → UPSTREAM → PROXY → CLI`。此时代理可以承担逐请求 ID 传播、协议映射与受控回退。所有组合的聚合用量均由 App 主动查询；当前请求级事实以节点 UsageLog/request_id 为基础，正式 RequestRecord 与 RequestAttempt 属于目标设计，direct 最终以上游账单域为权威。

### 12.1 CLI 配置操作、事务与崩溃恢复状态机

【已确认设计】P0-2 与 P0-8 共用统一的配置应用协议，多客户端批次不使用一个全局 ConfigTransaction。配置应用采用严格三层结构：`ConfigApplyOperation → AtomicityGroup → ConfigTransaction`。一次用户操作创建一个 `ConfigApplyOperation`；操作下辖一个或多个 `AtomicityGroup`，原子组是提交、激活与崩溃恢复边界；每个原子组包含一个或多个 `ConfigTransaction`，事务是单一适配器或可独立寻址配置目标的变更单元。默认每个受管客户端/RouteBinding revision 建立一个原子组及一个事务；共享同一文件、凭据投影或绑定不变量且必须共同生效的多个事务和 RouteBinding revision 可以进入同一 `atomicity_group`。`AtomicityGroupBinding(atomicity_group_id, binding_id, binding_revision, scope_key, expected_head_etag)` 记录参与绑定，每个 binding revision 只属于一个原子组；`ConfigTransactionBinding` 记录事务影响的一个或多个 binding revision，禁止依赖 RouteBinding 上的单一事务字段推断基数。纯凭据迁移组可以没有 RouteBinding，其他组至少一个；多绑定组必须在同一个 App 元数据事务内完成全体 Head CAS，不能逐 Head 提交。同组全部事务完成验证后才统一提交，组内不得形成已声明成功的部分状态；独立原子组可以分别提交，因此操作级结果可以是 `completed|partial_success|failed|cancelled|recovery_required`。

```text
planned → running → completed
               ↘ partial_success
               ↘ failed
               ↘ cancelled
               ↘ recovery_required
```

每个原子组遵循以下提交与恢复状态机；组内 ConfigTransaction 记录各自进度，但 `committed` 只在全组验证通过并完成唯一 commit point 后统一成立。单个文件的原子替换不能代替该原子组内跨事务、跨文件、秘密投影与 App 绑定状态的一致收敛。

```text
prepared → applying → verifying → committing → committed
   ↘ rejected     ↘          ↙
     cancelled      rolling_back → rolled_back
                                   ↘ conflict
                                   ↘ recovery_required
committing ──metadata/CAS failure──> rolling_back
```

AtomicityGroup 提供串行化、崩溃可恢复和最终全有或全无的**逻辑结果**，不承诺多个外部文件、凭据投影与进程环境在任意观察瞬间具有跨资源 ACID。进入 `applying` 前，App 必须阻止新的受管客户端启动，并让可控制的现有客户端进入 `quiesced`；新子进程只能在元数据 commit 后启动。无法阻止外部读取、无法补偿进程启动或不具备稳定恢复语义的目标，不得与其他资源放入同一原子组并宣称原子提交，应拆组或在预检阶段 `rejected`，向用户显示边界。

资源边界固定如下：文件使用同目录暂存、刷新落盘和原子替换；凭据安全存储中的规范秘密始终是唯一源，回滚只清理本组新建且已证明无引用的最小投影，不删除或降版规范秘密；轮换、吊销等不可逆外部动作不得进入配置原子组；进程启动、停止、重启与用户重连属于 commit 后的 runtime activation action，不作为可回滚资源。进程激活失败保留已提交配置并写 `runtime_activation_state=failed|unknown`，由单独操作重试，不能倒写原子组为未提交。

操作与事务 journal 至少记录：`apply_operation_id`、操作聚合状态、`atomicity_group_id`、`atomicity_group_state`、`config_transaction_id`、`transaction_state`、适配器/配置目标、参与 RouteBinding revision 与 expected Head ETag 集合、参与文件清单、每个文件的规范化路径标识、`file_phase`（pending/temp_written/replaced/verified/restored/conflict）、事务前是否存在、原 hash、目标 hash、App 实际写入 hash、临时路径、ACL 快照、备份引用、步骤时间与错误。journal 的状态变化先于或与对应副作用原子持久化，恢复器不得只凭文件是否存在猜测事务阶段。执行顺序如下：

并发与耐久不变量如下：每个 ConfigTransaction 只属于一个 AtomicityGroup，同组可以包含多个事务，并以 `(apply_operation_id, atomicity_group_id, transaction_target_key)` 防止重复目标；每个参与 binding revision 在 `AtomicityGroupBinding` 中恰有一行，`scope_key` 与 revision 自身必须一致。操作接受稳定 `client_operation_key` 与版本化 `operation_fingerprint`，同一身份下同键同指纹只返回既有操作，同键异指纹返回 409 `OPERATION_KEY_REUSED` 并审计。原子组开始前按规范化路径、credential projection 与 `scope_key` 排序取得耐久资源锁，重叠操作返回冲突并携带占有的 `operation_id`；多 Head expected ETag 集合在 prepared 后冻结，不能临时刷新并继续提交。journal 使用单调 `state_version` 做 CAS；每个副作用前先持久化并刷新 write-ahead intent，文件替换后再记录 observed hash。取消只允许在 `planned|prepared` 直接进入 `cancelled`，发生副作用后取消等价于进入 `rolling_back`。

操作聚合固定为：全部组 `committed`=`completed`；至少一组 `committed`、其余均为 `rolled_back|rejected|cancelled`=`partial_success`；零组 `committed` 且全部 `cancelled`=`cancelled`；零组 `committed` 且至少一组 `rolled_back|rejected`、其余均为安全终态=`failed`；任一组 `conflict|recovery_required`=`recovery_required`。`RouteBindingHead` CAS 失败时相关原子组不得 committed。

1. `prepared`：按 `atomicity_group` 计算 diff，完成能力预检，向用户展示将修改的客户端、文件、进程、秘密投影和重连动作；此时不修改官方配置。
2. `applying`：先落 journal 与旧态快照，再对每个参与文件写同目录临时文件、刷新落盘并原子替换；秘密只从安全存储生成最小投影，不进入 App 普通备份。
3. `verifying`：重新读取并校验 hash、schema、ACL 与客户端可解析性；全部 ConfigTransaction 和参与项通过后进入 `committing`，此时仍未对外宣告 committed/active。
4. `committing`：唯一 commit point 是一个 App 元数据事务。事务先按 `scope_key` 确定性排序并校验全部参与 RouteBindingHead 的 expected ETag；全部匹配时，在同一事务中以条件更新 CAS 全部 Head、把各新 revision 置 `active`、各旧 revision 置 `superseded`、把 AtomicityGroup 与全部成员事务置 `committed`，并写入 commit marker/journal version。只有成功更新行数严格等于 `AtomicityGroupBinding` 中应切换的不同 `scope_key` 数量，且不存在重复 scope 时，事务才能提交。纯凭据迁移组没有 Head 集合，其 commit point 仍是同一元数据事务中的 group/transaction committed marker。任一 Head 校验/条件更新为零、重复 scope 或元数据写入失败时整笔事务不提交，原子组必须进入 `rolling_back` 并记录稳定原因；Head ETag 不匹配使用 `HEAD_PRECONDITION_FAILED`，禁止刷新 ETag 后继续提交已经应用到文件的旧原子组。恢复器在继续持有本组资源锁时执行第 5 步：安全恢复成功后进入 `rolled_back` 并向调用方返回 412 `PRECONDITION_FAILED`；只有发现外部资源再次变化、无法安全覆盖时进入 `conflict`，无法证明恢复结果时进入 `recovery_required`。CAS 失败不得直接留下“文件为新值、Head 为旧值”的可运行状态，多 Head 集合也不得部分切换。若 journal 与 Head 无法处于同一事务数据库，实现必须先落地可恢复的 prepare/commit-decision 协议，不能降级为逐 Head 或“先 committed、随后激活”。
5. `rolling_back`：恢复事务前存在的文件，并删除事务前不存在、由本事务新建的文件。仅当当前 hash 等于 App 写入 hash 时允许自动覆盖；发现用户或其他工具的外部修改即进入 `conflict`，保留双方内容并要求人工选择。
6. 启动恢复：逐个恢复未收敛原子组。组内任一事务未到可提交条件时，整组默认回滚；已完成原子替换但未完成验证的组先重做全部验证，再决定整组提交或恢复；处于 `committing` 的组必须用 commit marker、全部参与 Head 与全部 revision 生命周期对账，只允许补完同一 commit decision 或完整回滚，禁止 `committed-but-head-old`、`head-new-but-group-uncommitted` 以及部分 Head 已推进。每个原子组最终只能得到一致旧态、完整新态或显式 `recovery_required`；全部原子组收敛后再计算操作级 `completed|partial_success|failed|cancelled|recovery_required`，不得显示虚假的全局成功。

【已实现】当前 `SwitchService` 已有备份、恢复和部分临时文件原子替换基础，也存在“客户端已应用、分流选择同步失败”等部分成功路径；正式 `ConfigApplyOperation`、按原子组划分的 journal、hash 冲突保护和绑定提交仍属【已确认设计】。

### 12.2 RouteBinding 生命周期与切换语义

```text
draft(new revision) → applying ──commit point──> active(new revision)
                         ↘ apply_failed
active(old revision) → restoring → restored
                           ↘ recovery_required
active(old revision) ──same commit point──> superseded(old revision) → retired
active(old revision) + endpoint_reference_state=head_advanced
                         └──user confirms/create──> draft(new revision)
```

- `draft` 是四层策略合并结果；`applying` 绑定一个 `atomicity_group_id`，并通过 `AtomicityGroupBinding/ConfigTransactionBinding` 关联该 revision 与全部参与事务。单绑定组与多绑定组都必须在 §12.1 的同一个元数据 commit point 让全部新 revision 进入 `active`、全部旧 revision 进入 `superseded`，并让原子组及全部成员事务进入 `committed`。
- `apply_failed` 表示该 revision 对应原子组已经 `rolled_back|rejected|cancelled|conflict|recovery_required`，因此该 revision 从未生效。事务已安全终结时继续保留旧 active revision；存在冲突或恢复要求时进入恢复工作台。`apply_failed` revision 不得原地重试或复活，修复后创建新 draft revision。
- 切换创建新 revision，旧 revision 进入 `superseded`。已启动会话和在途请求继续关联原 revision；用户可选择“立即应用”“会话结束后应用”或“仅新进程使用”。
- 目标健康劣化只更新 `runtime_health_state`；证书或 `deployment_id` 突变更新 `endpoint_reference_state=identity_changed`，信任吊销更新 `trust_revoked`，EndpointHead 推进更新 `head_advanced`；能力不兼容更新 `compatibility_state=incompatible`；外部配置变化更新 `config_integrity_state=externally_modified|conflict`。这些观测状态均不得被统称为 RouteBinding lifecycle 的 `stale`，也不得静默改变 active 目标或改写历史 RequestAttempt。
- 未叠加 C 的 A/B 路径按客户端设置 `reconnect_action=reconnect|restart|resend`；A+C/B+C 可以保持稳定本地代理入口，但代理内部目标切换仍受结果未知、语义等价、费用与隐私约束。
- 多客户端应用结果按 `ConfigApplyOperation` 和原子组逐项展示。一个原子组成功、另一个失败时，操作状态为 `partial_success`，成功绑定保持 active；`committed` 组可保持生效或通过新的恢复事务撤销，`rolled_back|rejected|cancelled` 组通过新 draft revision 重试，`conflict` 组只能查看 diff 并选择外部版本、App 版本或人工合并，`recovery_required` 组进入恢复工作台且不提供普通重试按钮。任何动作都不得原地复活 `apply_failed` revision，同一原子组内部不得部分提交。未安装或明确不受管的客户端不阻断已选择客户端完成。

## 13. 版本化能力契约与 API 分层

App 当前对 Sub2API 登录与后台接口耦合较深。应设计版本化的“中转能力契约”与适配器层（【已确认设计】），让云端、本地或第三方节点声明各自支持的能力，App 据此发现能力并降级。

### 13.1 三层 API 划分

- **管理 API**：面向平台治理（账号、房间、计费、运营），使用平台身份，多为云端后台能力。
- **客户端协同 API**：面向 App 与节点协商（capabilities、bootstrap、健康、模型价格、用量快照、路由决策），是能力契约的核心。
- **数据面 API**：面向实际请求转发及同一调用主体的 RequestRecord 请求级聚合状态查询（响应包含 `attempts[]`），使用 API Key 或本地数据面 Token，由执行节点提供。

三层接口身份不互通：Deployment 用户会话令牌不得当数据面 Key，数据面 API Key 不得访问管理接口，本地管理员授权不得复用云端管理员凭据。

### 13.2 客户端协同与数据面接口基线

以下均为【已确认设计】候选，当前 `/api/v1/client/*`、正式 attempts 查询与独立 readiness 尚未宣称存在。每个接口必须在 OpenAPI/JSON Schema 中固定 method、path、身份 scope、状态码、Content-Type、缓存、分页与限流，管理面、协同面和数据面 DTO 禁止复用。

| 接口 | 层与最小身份 | 用途与响应 | 缓存/关键状态 |
| --- | --- | --- | --- |
| `GET /health/live` | 公共 liveness；无身份 | 只证明进程存活，不返回依赖、版本、路径或拓扑 | `no-store`; 200/503 |
| `GET /api/v1/client/capabilities` | 匿名只返回最小公共发现；完整视图需 `client:discover` 或本地管理授权；只有已经 `anchor_verified` 的传输才可发送数据面 Key/Token 取得受限 self 视图 | 静态能力、协议、契约版本、Deployment 身份和弃用信息 | public 可按视图缓存；authenticated/self 为 `private, no-store` 并 `Vary: Authorization, Accept` |
| `POST /api/v1/client/endpoint-descriptor` | 传输必须先 `anchor_verified`；随后使用 `client:endpoint:read`、本地管理授权，或映射到当前数据面主体的 Key/Token `endpoint:read:self` | 请求携带 32-byte 高熵 `client_nonce`、目标协议和 App 已连接的规范化 origin/base path；返回由 Deployment 签名并绑定本次 nonce、`subject_scope_hash`、当前传输证据和该主体实际可调用单一入口的最小 EndpointDescriptor，供已有数据面 Key 首用和 TargetEndpoint revision 签发 | `no-store`；401/403/409/422；nonce 已消费/错配、主体摘要不匹配、过期、签名、入口映射或传输绑定失败时拒绝 |
| `GET /api/v1/client/bootstrap` | 平台/设备身份 `client:bootstrap`，或本地管理授权 | 客户端引导、身份方式、最低版本与端点引用 | 主体相关，默认 `no-store` |
| `GET /api/v1/client/readiness` | 完整视图需 `client:health` 或本地管理授权；数据面 Key/Token 可取得自身数据面的最小就绪视图 | 完整视图含实例、依赖、迁移、密钥、排空、降级与兼容状态；最小视图只含 Deployment、状态、受影响协议/feature、reason code 与重试提示 | 短 TTL；200 ready/degraded，503 draining/unavailable |
| `GET /api/v1/client/catalog/models` | `catalog:read`；公共目录可显式开放 | 模型、协议、能力与 `requested/effective` 映射约束 | ETag；主体特价须按身份分区缓存 |
| `GET /api/v1/client/catalog/pricing` | `pricing:read` | 币种、价格版本、适用主体与生效时间 | ETag；不得把不同主体价格共享缓存 |
| `GET /api/v1/client/usage/snapshot` | 平台或本地管理身份 `usage:read:self` | 字段最小化的聚合快照 | 短 TTL/ETag；不返回账号池、渠道或凭据字段 |
| `GET /api/v1/client/route-constraints` | `client:route` 或本地管理授权 | Endpoint 限制、健康摘要、可选能力与 `reason_code` | 短 TTL；只提供建议/约束，不替用户决定最终目标 |
| `POST /v1/requests/status` | 与原请求完全相同的数据面身份 | 按 `query_token` 或 `idempotency_key` 查询 RequestRecord 请求级聚合状态（含 `attempts[]`） | `no-store`; 200/202/400/401/403/429 |
| OpenAI/Anthropic/Google 兼容数据面 | 云端 API Key 或本地数据面 Token | 实际模型请求与协议原生流式响应 | 遵循对应协议；敏感响应 `no-store` |

【已确认设计】匿名 capabilities 只允许返回契约主版本、server role、公开协议和认证方式等最小字段；主体能力、内部依赖、账号/渠道、价格特例及 Deployment 内部拓扑必须鉴权。仅录入云端 API Key 或本地数据面 Token 的首用路径必须先按 §16.5.5 使用 `EndpointEnrollmentBundle` 把传输推进到 `anchor_verified`，此时才可发送 Key 取得受限 self DTO：`deployment_id`、`server_role`、`contract_version`、`server_version`、`endpoint_descriptor_version`、公开协议、与该主体相关的逐协议 idempotency/status-query 状态、最小 readiness，以及不含秘密的版本化 `subject_scope_hash`；它还可用同一主体经 nonce challenge 签发一个最小 EndpointDescriptor，但只能描述该主体实际可调用的单一数据面入口，不得返回管理、贡献、账号池、渠道、价格特例或依赖拓扑。没有受控 enrollment anchor 时只能保存候选，不能发送 Key、自省或 active。若同一 URI 同时提供公共与认证视图，public 与认证响应必须分离；authenticated/self 使用 `Cache-Control: private, no-store` 和 `Vary: Authorization, Accept`，防止缓存串主体。

EndpointDescriptor 由稳定 `descriptor_body` 和一次性挑战 envelope 构成。body 的规范字段固定为 `descriptor_version`、`endpoint_id`、`endpoint_revision`、`deployment_id`、`server_role`、`owner_kind=deployment`、`origin`、`base_path`、`protocol/protocol_version`、数据面 `auth_method` 类别与 `transport_binding`；`auth_method` 只能取该主体实际可调用的数据面认证方式（`data_plane_api_key`/`local_data_plane_token`），须与其 self capabilities 的 `auth_methods` 一致，禁止出现控制面方式。整段 URL 形态的 `base_uri`/`normalized_base_uri` 只允许作为 legacy 迁移输入，规范对象和输出中禁止出现，一律以 `origin + base_path` 表达。它只描述一个 TargetEndpoint revision，服务端只能从受信 Deployment 配置生成，不能回显请求 URL。`descriptor_body_digest` 使用 §16.5.1 的 `object_type=endpoint-descriptor-body`；envelope 固定该 digest、`subject_scope_hash`、`client_nonce`、`issued_at`、`expires_at`、`signing_key_id`、`keyset_revision`、`revocation_snapshot_version`、算法和 `signature`。签名的 unsigned payload 是 envelope 去掉 `signature`，使用 `object_type=endpoint-descriptor-envelope` 的统一 helper；因此签名通过 `descriptor_body_digest` 间接绑定 body，禁止另行实现“签完整对象”或“签 envelope 文本”变体。`subject_scope_hash` 必须与同一认证主体的受限 self capabilities 一致。`transport_binding` 必须绑定当前 TLS SPKI/本地 CA/mTLS 身份，或明确标记经验证的真实 loopback；App 还要把规范化 origin/base path、实际连接和批准入口映射逐跳校验，不能接受 descriptor 内任意 URL 触发跳转。签名密钥必须通过 enrollment 建立信任，descriptor 自签名本身不能替代首次传输信任。响应不包含秘密、账号池、Channel/Group/Account、内部实例、价格特例或管理能力。App 只接受本次请求登记且尚未消费的 nonce；nonce 错配/复用、主体摘要不匹配、响应过期或 TTL 超过 300 秒、跨主体/跨 origin 重放、错 Deployment/role/transport/entrypoint、keyset/吊销状态不新鲜或签名无效时，不得创建 TargetEndpoint revision 或 active RouteBinding。Deployment 端稳定 body 中的入口、签名主体、传输绑定或公开协议变化时必须签发新 revision；重复挑战同一逻辑入口保持同一 `endpoint_id` 和当时 current revision。

**EndpointDescriptor 可执行 JSON Schema（【已确认设计】，`descriptor_version="1"`）**。规范化、摘要与签名一律按 §16.5；`descriptor_body_digest`/`subject_scope_hash` 冻结口径见 §16.5.1/§16.5.3；`origin`/`base_path` 分离与 URI 规范化见 §16.5.2；签名公钥发现、轮换、吊销见 §16.5.4。

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://spec.local/endpoint-descriptor/v1.json",
  "title": "EndpointDescriptor",
  "type": "object",
  "additionalProperties": false,
  "required": ["descriptor_body", "envelope"],
  "properties": {
    "descriptor_body": {
      "type": "object",
      "additionalProperties": false,
      "required": ["descriptor_version", "endpoint_id", "endpoint_revision",
        "deployment_id", "server_role", "owner_kind", "origin", "base_path",
        "protocol", "protocol_version", "auth_method", "transport_binding"],
      "properties": {
        "descriptor_version": { "const": "1" },
        "endpoint_id":        { "type": "string", "format": "uuid" },
        "endpoint_revision":  { "type": "integer", "minimum": 1, "maximum": 9007199254740991 },
        "deployment_id":      { "type": "string", "minLength": 1 },
        "server_role":        { "enum": ["local-edge", "cloud-control-data-plane"] },
        "owner_kind":         { "const": "deployment" },
        "origin":             { "type": "string", "pattern": "^https?://[^/?#]+$", "description": "uri-normalization v1 的 scheme://host[:port]；输入含 query/fragment、userinfo、路径或非法 host 时拒绝，不能靠截断通过" },
        "base_path":          { "type": "string", "pattern": "^/(?:[^/?#%]|%[0-9A-Fa-f]{2})*$", "description": "规范化绝对路径前缀，无 query/fragment；dot segment、encoded slash/backslash 和歧义编码由 §16.5.2 验证器拒绝" },
        "protocol":           { "enum": ["openai", "anthropic", "google"] },
        "protocol_version":   { "type": "string", "minLength": 1 },
        "auth_method":        { "enum": ["data_plane_api_key", "local_data_plane_token"], "description": "descriptor 只描述单一数据面入口，故只能取数据面认证方式；须与该主体 self capabilities 的 auth_methods 交集一致，禁止出现 admin_x_api_key/deployment_user_jwt 等控制面方式" },
        "transport_binding": {
          "type": "object",
          "oneOf": [
            {
              "additionalProperties": false,
              "required": ["binding_kind", "server_spki_sha256"],
              "properties": {
                "binding_kind": { "const": "tls_spki" },
                "server_spki_sha256": { "type": "string", "pattern": "^sha256:[A-Za-z0-9_-]{43}$" }
              }
            },
            {
              "additionalProperties": false,
              "required": ["binding_kind", "ca_cert_sha256"],
              "properties": {
                "binding_kind": { "const": "local_ca" },
                "ca_cert_sha256": { "type": "string", "pattern": "^sha256:[A-Za-z0-9_-]{43}$" }
              }
            },
            {
              "additionalProperties": false,
              "required": ["binding_kind", "server_spki_sha256", "client_trust_domain_digest"],
              "properties": {
                "binding_kind": { "const": "mtls" },
                "server_spki_sha256": { "type": "string", "pattern": "^sha256:[A-Za-z0-9_-]{43}$" },
                "client_trust_domain_digest": { "type": "string", "pattern": "^sha256:[A-Za-z0-9_-]{43}$" }
              }
            },
            {
              "additionalProperties": false,
              "required": ["binding_kind"],
              "properties": { "binding_kind": { "const": "verified_loopback" } }
            }
          ]
        }
      },
      "allOf": [
          {
            "if": { "properties": { "server_role": { "const": "cloud-control-data-plane" } } },
            "then": { "properties": { "auth_method": { "const": "data_plane_api_key" } } }
          },
          {
            "if": { "properties": { "server_role": { "const": "local-edge" } } },
            "then": { "properties": { "auth_method": { "enum": ["data_plane_api_key", "local_data_plane_token"] } } }
          }
      ]
    },
    "envelope": {
      "type": "object",
      "additionalProperties": false,
      "required": ["descriptor_body_digest", "subject_scope_hash", "client_nonce",
        "issued_at", "expires_at", "signing_key_id", "keyset_revision",
        "revocation_snapshot_version", "algorithm", "signature"],
      "properties": {
        "descriptor_body_digest": { "type": "string", "pattern": "^sha256:[A-Za-z0-9_-]{43}$", "description": "§16.5.1 object_type=endpoint-descriptor-body" },
        "subject_scope_hash":     { "type": "string", "pattern": "^sha256:[A-Za-z0-9_-]{43}$", "description": "见 §16.5.3；客户端只比较不重算" },
        "client_nonce":           { "type": "string", "pattern": "^[A-Za-z0-9_-]{43}$", "description": "32-byte 高熵，一次性，base64url-no-pad" },
        "issued_at":              { "type": "string", "format": "date-time", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\\.[0-9]{3})?Z$" },
        "expires_at":             { "type": "string", "format": "date-time", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\\.[0-9]{3})?Z$" },
        "signing_key_id":         { "type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$" },
        "keyset_revision":        { "type": "integer", "minimum": 1, "maximum": 9007199254740991 },
        "revocation_snapshot_version": { "type": "integer", "minimum": 1, "maximum": 9007199254740991 },
        "algorithm":              { "const": "Ed25519" },
        "signature":              { "type": "string", "pattern": "^ed25519:[A-Za-z0-9_-]{86}$", "description": "§16.5.1 object_type=endpoint-descriptor-envelope" }
      }
    }
  }
}
```

黄金示例（占位值用 `<...>` 标注，实际由 Deployment 签发）：

```json
{
  "descriptor_body": {
    "descriptor_version": "1",
    "endpoint_id": "8b2f0c4e-1a2b-4c3d-9e5f-6a7b8c9d0e1f",
    "endpoint_revision": 3,
    "deployment_id": "dep_example",
    "server_role": "local-edge",
    "owner_kind": "deployment",
    "origin": "https://edge.local:8443",
    "base_path": "/api",
    "protocol": "anthropic",
    "protocol_version": "2023-06-01",
    "auth_method": "local_data_plane_token",
    "transport_binding": {
      "binding_kind": "tls_spki",
      "server_spki_sha256": "sha256:<base64url-no-pad-spki>"
    }
  },
  "envelope": {
    "descriptor_body_digest": "sha256:<base64url-no-pad-digest>",
    "subject_scope_hash": "sha256:<base64url-no-pad-subject-scope>",
    "client_nonce": "<base64url-no-pad-32-byte-nonce>",
    "issued_at": "2026-07-19T00:00:00Z",
    "expires_at": "2026-07-19T00:05:00Z",
    "signing_key_id": "edge-desc-key-2026-07",
    "keyset_revision": 4,
    "revocation_snapshot_version": 12,
    "algorithm": "Ed25519",
    "signature": "ed25519:<base64url-no-pad-ed25519-sig>"
  }
}
```

黄金校验用例至少覆盖五类：正确 descriptor 通过；错/复用 nonce 拒绝；`expires_at` 过期拒绝；`subject_scope_hash` 与 self 视图不一致拒绝；`origin/base_path`、`deployment_id`、`server_role`、`transport_binding` 任一与已确认传输不符（越权 entrypoint / 跨 origin 重放 / 错 role）拒绝。任一拒绝路径都不得产出可 active 的 TargetEndpoint revision。

### 13.3 路由决策、RouteBinding、RequestRecord 与 RequestAttempt

【已确认设计】App 根据四层优先级、用户偏好与节点约束生成路由决策，不由节点替用户决定最终目标。路由决策至少包含 `route_target`（local/cloud/direct）、`endpoint_id`、`endpoint_revision`、`endpoint_owner_kind`、可空 `deployment_id`、可空 `deployment_role`、可空 `trust_binding_version`、`requested_model`、预期 `effective_model`、`fallback_policy`、可空 `cost_limit`、`data_sensitivity`、`reason_code`、`user_confirmation_required` 和 `trace_id`。`endpoint_owner_kind/deployment_id/deployment_role` 都由已冻结的 TargetEndpoint revision 派生并作为审计快照；决策与绑定结构必须校验为同一值，禁止调用方维护一套可漂移的目标归属或把 local/cloud 任意互换。

决策落地后形成 RouteBinding，至少记录客户端、项目/任务、进程或配置目标、Endpoint/Deployment revision、应用时间、配置事务状态和重连要求。RouteBinding 属于 App 控制面事实，不包含请求级费用，也不证明请求已经执行。

【已确认设计】执行节点收到真实请求后，认证层先解析不可空 `ledger_domain_id`，它在单个 Deployment 内稳定表示“账本主体 + TargetEndpoint”。节点在任何上游 dispatch 前原子创建或复用 RequestRecord，记录节点侧稳定 `request_id`、`ledger_domain_id`、endpoint、幂等键 hash、指纹、查询句柄 hash 和聚合状态；随后每次真实上游调用创建独立 RequestAttempt，记录 `attempt_id`、实际 `instance_id`、endpoint revision、内部调度关联、`effective_model`、状态和可计费用量。未叠加 C 的 A/B 路径中，App 可能无法取得每个 RequestRecord/RequestAttempt；叠加 C 后可由代理传播关联信息。当前实现只有 UsageLog/request_id 基础，任何情况下都不能用 RouteBinding 推算或伪造数据面请求事实。

幂等登记与 dispatch 占有采用同一个 RequestRecord 权威：`ledger_domain_id` 只在单个 Deployment 内稳定，物理表、分区边界和唯一索引必须显式包含 Deployment 作用域；有幂等键时建立部分唯一约束 `UNIQUE (deployment_id, ledger_domain_id, idempotency_key_hash) WHERE idempotency_key_hash IS NOT NULL`（若实现按 Deployment 分表，也必须以等价迁移/约束证据证明）。`fingerprint_version` 与请求指纹不进入唯一键。并发冲突事务锁定既有记录后比较版本化指纹：同键同指纹复用，同键异指纹返回 409 `IDEMPOTENCY_KEY_REUSED` 并审计。每次 lease claim 原子更新 `dispatch_owner_token`、`dispatch_lease_until` 并单调递增 `dispatch_fence`；只有持有当前 owner/fence 的执行者可创建下一 RequestAttempt，其他并发调用只返回既有状态或缓存结果。

RequestRecord 与首个 `registered` RequestAttempt 必须先提交。执行者在任何 socket/connect/write 或可能触达上游的代理 I/O 前，必须以 owner、fence、lease 有效期和 attempt 当前状态做 CAS，在同一耐久事务中写入 `dispatch_intent_at`、`dispatch_intent_hash` 并把 attempt 从 `registered` 置为 `dispatching`；网络客户端只接受匹配已提交 intent/fence 的调用。`dispatching` 起即保守视为“可能已经进入上游”：崩溃、超时或租约过期只能进入查询/收敛流程，不得自动重发同一 attempt，也不得自动创建后继 attempt。只有 `registered` 且不存在已提交 intent 的 attempt 才具有 `result_state=not_dispatched` 的持久证明并可被新 lease owner 接管。所有 attempt 状态写入都必须携带 fence 做 CAS，旧 owner 的迟到写入不得覆盖新状态。现有通用 `idempotency_records` 可迁移或复用于内部锁实现，但不得与 RequestRecord 对同一数据面请求形成两套竞争唯一事实。

每个官方 CLI 的精确版本与协议组合必须在兼容矩阵中声明：是否能设置稳定 `idempotency_key`、是否会原样传播、是否能取得稳定查询令牌、是否支持状态查询。不支持稳定幂等键时，节点只能提供其实际观察到的 request/attempt 级防重与单终态保护，不能把调用方逻辑重试宣称为幂等；结果未知时禁止自动重试。跨 endpoint 不共享事务唯一约束，系统不承诺跨节点 global exactly-once。

### 13.4 能力协商字段

认证 capabilities 视图的规范字段集合为：`capability_view`、`contract_version`、`server_version`、`min_client_version`、`server_role`、`deployment_id`、`endpoint_descriptor_version`、`generated_at`、`expires_at`、`features`、`auth_methods`、`protocols`、`usage_contract_version`、`pricing_version`、`deprecation`；映射到当前数据面主体的受限 self view 额外必须返回不含秘密的 `subject_scope_hash`，管理视图不得把该字段当作跨主体身份。逐协议的幂等、重放与状态查询能力只以 `protocols[]` 为权威；旧顶层 `idempotency_modes`、`attempt_query` 及 `features.attempt_query` 视为弃用输入，客户端不得据此决策。`contract_version` 表示协同契约版本，`server_version` 表示软件构建版本；数据库使用 `db_schema_version`，App 配置、服务端配置与配置事务 journal 使用组件化 `config_schema_versions`，禁止用一个含糊字段混合表达不同演进对象。App 据最低客户端版本与 feature 状态决定启用/降级，据 `deployment_id`、`subject_scope_hash` 与受签 EndpointDescriptor 识别信任主体和端点 revision，据 `protocols[]` 判断是否能安全重试，据 `pricing_version` 与 `usage_contract_version` 对齐价格和用量口径。

`protocols` 是**协议描述符数组** `protocols[]`，不是裸字符串列表；每个协议独立声明自身能力，禁止用一个全局标量代表所有协议。每个元素规范字段为：

| 字段 | 含义与冻结口径 |
| --- | --- |
| `protocol` | `openai\|anthropic\|google` |
| `protocol_version` | 该协议 wire 版本（如 anthropic `2023-06-01`），随协议演进独立推进 |
| `data_plane_base_path` | 该协议数据面入口的规范化 `base_path`（uri-normalization v1，见 §16.5.2）；`origin` 由 EndpointDescriptor 承载，不在此重复 |
| `idempotency_key` | `{ "accepted": bool, "header": "Idempotency-Key", "propagated": bool }`，声明是否接受与是否原样传播稳定幂等键 |
| `processing_replay` | 处理中重放语义，枚举 `return_202_status\|conflict_409\|native_error`（三选一，见 §13.6 wire 状态） |
| `completion_replay` | 完成后重放语义，枚举 `replay_non_streaming\|status_only`；流式正文恒为 `status_only` |
| `stream_replay` | `status_only`（冻结，不可为其他值）；流式完成只返回 RequestRecord 状态与查询句柄，不缓存或重放正文 |
| `status_query` | `supported=true` 时固定 `{ "supported": true, "method": "POST", "path_kind": "origin_absolute", "path": "/v1/requests/status", "query_token_header": "X-Request-Query-Token" }`；path 是同一已签 EndpointDescriptor `origin` 下的绝对路径，不与 `data_plane_base_path` 拼接；false 时其余字段省略 |

旧 `idempotency_modes` 只允许迁移适配器读取并映射为 `protocols[]`；v1 规范输出不再返回该顶层字段。任何适配器不得从其他协议或旧汇总字段推断重放语义。

每个 feature 使用固定命名空间和结构化状态：`state=supported|unsupported|disabled|degraded`、可选 `reason_code`、`constraints`、`since` 与 `deprecated_at`。静态能力放 capabilities；实时依赖与实例健康放 readiness，禁止用“进程活着”推断“功能可用”。

新契约统一使用 `deployment_id`；历史适配器读到旧节点身份字段时，只可将其作为内部兼容输入映射到 Deployment，禁止把旧字段继续输出为规范契约，也禁止两个字段表达不同身份。

### 13.5 最小 Schema 与协商规则

【已确认设计】匿名发现响应只固定 `contract_version` 主版本、`server_role`、公开 `protocols`、`auth_methods`、`generated_at` 与 `expires_at`；不得返回主体能力、内部依赖、Deployment 内部拓扑、EndpointDescriptor 或价格特例。认证 capabilities 响应固定第 13.4 节的完整规范字段集合，并按当前主体裁剪 `features`；EndpointDescriptor 使用独立 `no-store` 的 nonce challenge 请求/响应 Schema 与签名验证，不混入可共享缓存的 capabilities DTO。字段类型、必填性、格式、枚举与 feature 命名空间由 JSON Schema/OpenAPI 固化，并纳入匿名、认证、数据面主体 descriptor、错/复用 nonce 和越权入口五类黄金响应与契约测试。

匿名发现与首次信任绑定的自洽性（【已确认设计】）：匿名发现**不返回 `deployment_id` 或 EndpointDescriptor**（含不返回 opaque 形态），二者只在认证后作为受信主体属性出现。首次信任依赖 §16.5.5 的 `EndpointEnrollmentBundle` 与传输层证据：loopback 必须同时验证真实回环、ACL/命名管道或受管启动进程身份，LAN/公网目标在 enrollment 时由用户确认证书指纹（TOFU）或使用本地 CA/mTLS。只有 `anchor_verified` 后 App 才能发送数据面 Key 取得 self capabilities，登记 `subject_scope_hash`、生成一次性 nonce、调用 EndpointDescriptor POST，并核对 nonce、主体摘要、签名、keyset/吊销版本、有效期、`deployment_id`、`server_role`、单一入口与已确认传输；全部通过后才把稳定 body digest 和签名 key hash 冻结到 `trust_binding`。后续每次认证再核对 descriptor revision 与 `trust_binding_version`；证书、签名 key、`deployment_id`、role 或稳定入口相对已绑定值发生突变，一律触发重新信任，不得静默接受。当前后端匿名面只有 `/health`（返回 `{"status":"ok"}`）与 `/setup/status`，既无 capabilities/bootstrap/readiness/endpoint-descriptor，也不泄露任何节点身份（本次审计确认），与上述设计不冲突。

capabilities 响应固定 `capability_view ∈ {public, authenticated, self}`，明确当前 DTO 的裁剪级别：`public` 为匿名最小发现（§13.5 首段字段）；`authenticated` 为完整规范字段集合（§13.4），按主体裁剪 `features`；`self` 为仅数据面 Key/Token 首用的受限自省视图（§13.2），额外含 `subject_scope_hash`。三种视图的 `protocols[]` 作用域不同：`public` 只列公开协议及 `protocol_version`，不含 `idempotency_key`/`status_query` 细节；`authenticated`/`self` 才展开逐协议重放与查询能力。

**三视图可执行 JSON Schema（【已确认设计】，`contract_version` 主版本 `1`）**。v1 只以 `protocols[]` 为权威；旧顶层 `idempotency_modes`、`attempt_query` 及 `features.attempt_query` 仅允许迁移适配器读取为兼容**输入**，`additionalProperties:false` 保证它们不再作为规范**输出**或决策源。以下是单一、自包含的 Draft 2020-12 Schema；不得把 `$defs` 与视图拆成无法独立解析的片段：

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://spec.local/client-capabilities/v1.json",
  "title": "ClientCapabilities",
  "oneOf": [
    { "$ref": "#/$defs/PublicCapabilities" },
    { "$ref": "#/$defs/AuthenticatedCapabilities" },
    { "$ref": "#/$defs/SelfCapabilities" }
  ],
  "$defs": {
    "Rfc3339Utc": {
      "type": "string",
      "format": "date-time",
      "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\\.[0-9]{3})?Z$"
    },
    "AuthMethod": {
      "enum": ["admin_x_api_key", "deployment_user_jwt", "data_plane_api_key", "local_data_plane_token"]
    },
    "FeatureStatus": {
      "type": "object",
      "additionalProperties": false,
      "required": ["state"],
      "properties": {
        "state": { "enum": ["supported", "unsupported", "disabled", "degraded"] },
        "reason_code": { "type": "string", "minLength": 1 },
        "constraints": { "type": "object" },
        "since": { "type": "string", "minLength": 1 },
        "deprecated_at": { "$ref": "#/$defs/Rfc3339Utc" }
      }
    },
    "StatusQuery": {
      "oneOf": [
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["supported"],
          "properties": { "supported": { "const": false } }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["supported", "method", "path_kind", "path", "query_token_header"],
          "properties": {
            "supported": { "const": true },
            "method": { "const": "POST" },
            "path_kind": { "const": "origin_absolute" },
            "path": { "const": "/v1/requests/status" },
            "query_token_header": { "const": "X-Request-Query-Token" }
          }
        }
      ]
    },
    "ProtocolDescriptor": {
      "type": "object",
      "additionalProperties": false,
      "required": ["protocol", "protocol_version", "data_plane_base_path", "idempotency_key",
        "processing_replay", "completion_replay", "stream_replay", "status_query"],
      "properties": {
        "protocol": { "enum": ["openai", "anthropic", "google"] },
        "protocol_version": { "type": "string", "minLength": 1 },
        "data_plane_base_path": { "type": "string", "pattern": "^/(?:[^/?#%]|%[0-9A-Fa-f]{2})*$", "description": "uri-normalization v1 的 base_path；origin 只来自受签 EndpointDescriptor" },
        "idempotency_key": {
          "type": "object",
          "additionalProperties": false,
          "required": ["accepted", "header", "propagated"],
          "properties": {
            "accepted": { "type": "boolean" },
            "header": { "const": "Idempotency-Key" },
            "propagated": { "type": "boolean" }
          },
          "allOf": [
            { "if": { "properties": { "accepted": { "const": false } } }, "then": { "properties": { "propagated": { "const": false } } } }
          ]
        },
        "processing_replay": { "enum": ["return_202_status", "conflict_409", "native_error"] },
        "completion_replay": { "enum": ["replay_non_streaming", "status_only"] },
        "stream_replay": { "const": "status_only" },
        "status_query": { "$ref": "#/$defs/StatusQuery" }
      }
    },
    "PublicProtocolDescriptor": {
      "type": "object",
      "additionalProperties": false,
      "required": ["protocol", "protocol_version"],
      "properties": {
        "protocol": { "enum": ["openai", "anthropic", "google"] },
        "protocol_version": { "type": "string", "minLength": 1 }
      }
    },
    "PublicCapabilities": {
      "type": "object",
      "additionalProperties": false,
      "required": ["capability_view", "contract_version", "server_role", "protocols", "auth_methods", "generated_at", "expires_at"],
      "properties": {
        "capability_view": { "const": "public" },
        "contract_version": { "type": "string", "pattern": "^[0-9]+(\\.[0-9]+)?$" },
        "server_role": { "enum": ["local-edge", "cloud-control-data-plane"] },
        "protocols": { "type": "array", "minItems": 1, "items": { "$ref": "#/$defs/PublicProtocolDescriptor" } },
        "auth_methods": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "$ref": "#/$defs/AuthMethod" } },
        "generated_at": { "$ref": "#/$defs/Rfc3339Utc" },
        "expires_at": { "$ref": "#/$defs/Rfc3339Utc" }
      }
    },
    "AuthenticatedCapabilities": {
      "type": "object",
      "additionalProperties": false,
      "required": ["capability_view", "contract_version", "server_version", "min_client_version",
        "server_role", "deployment_id", "endpoint_descriptor_version", "generated_at", "expires_at",
        "features", "auth_methods", "protocols", "usage_contract_version", "pricing_version", "deprecation"],
      "properties": {
        "capability_view": { "const": "authenticated" },
        "contract_version": { "type": "string", "pattern": "^[0-9]+\\.[0-9]+$" },
        "server_version": { "type": "string", "minLength": 1 },
        "min_client_version": { "type": "string", "minLength": 1 },
        "server_role": { "enum": ["local-edge", "cloud-control-data-plane"] },
        "deployment_id": { "type": "string", "minLength": 1 },
        "endpoint_descriptor_version": { "const": "1" },
        "generated_at": { "$ref": "#/$defs/Rfc3339Utc" },
        "expires_at": { "$ref": "#/$defs/Rfc3339Utc" },
        "features": { "type": "object", "additionalProperties": { "$ref": "#/$defs/FeatureStatus" } },
        "auth_methods": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "$ref": "#/$defs/AuthMethod" } },
        "protocols": { "type": "array", "minItems": 1, "items": { "$ref": "#/$defs/ProtocolDescriptor" } },
        "usage_contract_version": { "type": "string", "minLength": 1 },
        "pricing_version": { "type": "string", "minLength": 1 },
        "deprecation": { "type": ["object", "null"] }
      }
    },
    "SelfCapabilities": {
      "type": "object",
      "additionalProperties": false,
      "required": ["capability_view", "contract_version", "server_version", "server_role", "deployment_id",
        "endpoint_descriptor_version", "generated_at", "expires_at", "auth_methods", "protocols",
        "readiness_min", "subject_scope_hash"],
      "properties": {
        "capability_view": { "const": "self" },
        "contract_version": { "type": "string", "pattern": "^[0-9]+\\.[0-9]+$" },
        "server_version": { "type": "string", "minLength": 1 },
        "server_role": { "enum": ["local-edge", "cloud-control-data-plane"] },
        "deployment_id": { "type": "string", "minLength": 1 },
        "endpoint_descriptor_version": { "const": "1" },
        "generated_at": { "$ref": "#/$defs/Rfc3339Utc" },
        "expires_at": { "$ref": "#/$defs/Rfc3339Utc" },
        "auth_methods": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "enum": ["data_plane_api_key", "local_data_plane_token"] } },
        "protocols": { "type": "array", "minItems": 1, "items": { "$ref": "#/$defs/ProtocolDescriptor" } },
        "readiness_min": { "type": "object", "description": "最小就绪视图，见 §13.2" },
        "subject_scope_hash": { "type": "string", "pattern": "^sha256:[A-Za-z0-9_-]{43}$", "description": "见 §16.5.3；客户端只比较不重算" }
      },
      "allOf": [
        {
          "if": { "properties": { "server_role": { "const": "cloud-control-data-plane" } } },
          "then": { "properties": { "auth_methods": { "const": ["data_plane_api_key"] } } }
        }
      ]
    }
  }
}
```

三视图均以 `protocols[]` 为唯一权威；任何 `idempotency_modes`/`attempt_query` 顶层字段被 `additionalProperties:false` 排除在规范输出之外，只能由迁移适配器映射进 `protocols[]` 后消失。

缓存与协商作用域冻结：`Accept` 用于内容类型协商，契约版本走 `contract_version`（不复用 `Accept` 表达版本）；`public` 视图可共享缓存并带 `ETag`；`authenticated`/`self` 视图按主体隔离，若同一 URI 同时提供多视图，必须设 `Vary: Authorization` 且敏感字段响应 `Cache-Control: no-store`；EndpointDescriptor 恒 `no-store`，不进入任何可共享缓存。`ETag`/`If-None-Match` 仅用于协同面 GET 读缓存，与 §11.4/§16.6 的 Head 写并发 CAS（强 ETag + `If-Match`）是不同机制，不得混用。

协商规则：

- `contract_version` 主版本不兼容时，App 停止调用该协同 API，但保留用户手工配置和既有数据面调用能力；次版本只允许向后兼容增加。
- 新增可选字段按向后兼容处理；未知 `features` 必须忽略，不能默认启用。
- `capabilities`、模型和价格响应支持 `ETag`/`If-None-Match`；`generated_at/expires_at` 与 HTTP 缓存头一致。节点暂不可用时可以使用有版本缓存，并显示来源、缓存时间与非实时状态。
- 同键重放的 wire 行为由当前 `protocols[]` 元素的 `processing_replay/completion_replay/stream_replay` 决定：处理中按协议返回 202 状态、明确冲突或原生等价结果；非流式完成后只在声明 `replay_non_streaming` 时重放完整响应；流式完成后只返回状态查询信息，不缓存或重放正文。
- 协同/管理 API 的统一错误模型见 13.8；OpenAI、Anthropic、Google 数据面继续输出协议原生 wire shape，不强行套用管理面 envelope。
- 各接口明确鉴权方式：管理 API 使用平台登录身份；普通客户端协同 API 使用受限平台/设备身份或本地管理授权。数据面 Key/Token 对协同面的权限仅限受限 capabilities、最小 readiness 与 nonce-bound EndpointDescriptor 签发，不能调用 bootstrap、管理、贡献或治理接口；数据面 API 继续使用云端 API Key 或本地数据面 Token。
- 弃用窗口长度和最低客户端兼容周期属于【待决策】，在确定前不得静默移除字段或能力。

### 13.6 结果未知的查询协议

【已确认设计】当请求可能已进入上游、但调用方未取得确定结果时，入口节点应返回或允许恢复一个绑定 RequestRecord 的稳定 `query_token`；同一鉴权主体也可按 `idempotency_key` 定位同一 RequestRecord。状态查询属于数据面 API，调用方必须使用映射到原稳定 `principal_id + ledger_domain_id` 且具备查询 scope 的有效云端 API Key 或本地数据面 Token；密钥轮换后，新凭据若仍映射到同一 principal，可查询轮换前在途请求，原已撤销 Key 本身不能继续鉴权。API Key 脱离 App 的调用方可直接查询，App 只有持有同一主体的受限数据面授权时才能代查，Deployment 用户会话令牌、本地管理授权和设备身份都不能替代数据面身份。主体已删除或无法恢复数据面凭据时，只允许经 step-up、全量审计且默认脱敏的专用运维查询，不向普通调用方扩大权限。

`query_token` 只是绑定原主体、endpoint、账本域与 RequestRecord 的不可猜测关联句柄，不是 bearer 凭证，单独持有它不能取得状态；服务端只保存其不可逆 hash。查询使用 `POST /v1/requests/status`，令牌或幂等键置于请求体；请求体必须且只能提供两者之一，两者皆无或皆有返回 400 `INVALID_QUERY_SELECTOR`。URL、反向代理 access log、错误日志、遥测和支持包都不得记录原值。服务端先完成数据面鉴权，再做恒定时间的主体/域匹配，并对查询限流和审计。

`POST /v1/requests/status` 接口契约（【已确认设计】，冻结）：

| 维度 | 冻结口径 |
| --- | --- |
| path | `origin_absolute`：固定 `/v1/requests/status`，位于该主体已签 EndpointDescriptor 的 `origin` 下，**绝不与 `data_plane_base_path` 拼接**；协议特定的 `data_plane_base_path`（如 `/api`、`/v1`）不改变本查询入口 |
| method / Content-Type | `POST`；请求与响应均 `application/json`；恒 `Cache-Control: no-store` |
| 身份 header | `Authorization` 必须携带与原请求**完全相同**的数据面身份（云端 API Key 或本地数据面 Token，映射同一 `principal_id + ledger_domain_id`）；Deployment 用户会话令牌/管理授权/设备身份/`query_token` 单独持有均不可鉴权 |
| body | 严格二选一：`{ "query_token": <string> }` 或 `{ "idempotency_key": <string> }`；两者皆无或皆有返回 400 `INVALID_QUERY_SELECTOR`；`query_token` 非 bearer 凭证，`idempotency_key` 为 16–128 可打印 ASCII |
| response body | `query_status ∈ {pending, finalized, aborted, not_found}`、`safe_to_retry`、`request_id?`、`final_attempt_id?`、`attempts[]`（每项含 `attempt_id`/序号/执行/结算状态/effective model/目标引用/measured·billable usage）、`billable_usage_total?`、`convergence_deadline`、`retain_until` |
| status 映射 | `pending`→202；`finalized|aborted|not_found`→200；跨主体/跨 endpoint/过保留期/不存在统一 200 `not_found`（不用 403/404 暴露存在性）；401 仅数据面身份无效；403 仅身份有效但缺查询 scope；409 仅原始数据面请求的同键异指纹冲突；429 查询限流 |
| retry 语义 | `Retry-After`（秒或 HTTP-date）与 JSON `retry_after_ms`（毫秒）换算必须一致；客户端不得仅凭 `not_found` 自判可重试，只信服务端 `safe_to_retry=true` |

调用方幂等使用 `Idempotency-Key` 请求头；键采用 16–128 个可打印 ASCII 字符，服务端日志只记录不可逆 hash。节点在任何上游 dispatch 前原子登记“账本域 + 键 + `fingerprint_version` + 请求指纹”并建立 RequestRecord，再生成 `query_token`；每次 attempt 还必须按 §13.3 在首次可能触达上游的 I/O 前完成带 fence 的 durable dispatch intent。能返回响应头时使用 `X-Request-Query-Token`（统一命名；此前草案中的 `X-Attempt-Query-Token` 为其旧名，语义相同——句柄绑定的是 RequestRecord 而非单个 attempt，故正式名以 `Request` 为准，服务端在弃用窗口内可临时兼容旧名读取但只输出新名），协议允许的非流式错误体或流式终止事件可以同时返回该句柄。请求指纹至少覆盖 method、规范化路径、主体、endpoint、模型、影响语义的协议头与请求体 hash；规范化算法必须版本化（见 §16.5）。

幂等记录 TTL、查询保留期、账本保留期和删除墓碑期限属于【待决策】，但顺序约束固定：查询保留期覆盖最大执行/结算收敛窗口，墓碑在原记录清理后继续阻止危险的“过期即当作未执行”推断。非流式完成结果是否完整重放、流式结果是否只返回状态而不重放正文，必须由对应 `protocols[]` 元素声明。客户端永远不能只凭 `not_found` 自行判定可重试；只有服务端在保留期内返回 `safe_to_retry=true`，并以 RequestRecord/墓碑与不存在任何 durable dispatch intent 的事实证明请求未跨越 dispatch commit point，才可进入自动重试策略。

接口响应字段固定为 `query_status ∈ {pending, finalized, aborted, not_found}`。`pending` 当且仅当请求入口尚未关闭、存在执行非终态 attempt，或存在尚未进入 `settled|void` 的可能计费 attempt；`finalized` 要求逻辑结果成功、`final_attempt_id` 以复合外键引用本 RequestRecord 下的 `completed` attempt，且全部 attempt 的执行轴与结算轴终结；`aborted` 要求入口已关闭、无成功逻辑结果且全部 attempt 两轴终结。`not_found` 只属于查询响应域，不得持久化为 RequestRecord 或 RequestAttempt 状态。

命中 RequestRecord 时，响应返回非空 `request_id`、可空 `final_attempt_id`、`attempts[]` 摘要以及 `billable_usage_total`；每个摘要至少包含 `attempt_id`、序号、执行/结算状态、effective model、实际目标引用与该 attempt 的 measured/billable usage，禁止任意挑选一个 attempt 冒充整个请求。`pending` 返回 202，`finalized|aborted|not_found` 返回 200；401 只表示数据面身份无效，403 只表示身份有效但缺少查询 scope。跨主体、跨 endpoint、已过保留期或不存在统一返回 200 `not_found`，不得用 403/404 暴露存在性；409 只用于原始数据面请求的同键异指纹冲突。HTTP `Retry-After` 使用秒或 HTTP-date，JSON `retry_after_ms` 使用毫秒，二者换算必须一致。

`not_found` 时返回 `request_id=null`、`final_attempt_id=null`、`attempts=[]`、`billable_usage_total=null`，且 `safe_to_retry` 默认为 false。响应同时给出适用的 `convergence_deadline` 与 `retain_until`；具体收敛超时和保留期由 P0-6 的版本化契约冻结。`not_found` 本身不等于“从未执行”，只有节点能够证明查询仍处于受保护保留期、入口遵守“先登记、再写 durable intent、后发网”，且当前记录或墓碑证明没有任何 intent 跨越 commit point，才可返回 `safe_to_retry=true`；其余情况继续失败关闭或由用户确认潜在重复执行与成本。

该协议只帮助解析单一账本域内的未知状态，不建立跨 endpoint 事务，也不承诺 global exactly-once。跨 endpoint 或跨 Deployment 重新发起必须创建新的 RequestRecord，并以独立 `RequestLink` 关联：至少包含 `link_id`、`child_deployment_id`、`child_request_id`、`relation_type=retry|fallback|manual_resubmit`、`parent_deployment_id`、`parent_endpoint_id`、`parent_request_id`、可空 `trace_id`、`created_by_principal`、`link_evidence=server_observed|client_asserted`、`verification_state=local_verified|remote_unverified`、可空 `verification_reason` 与 `created_at`。同 Deployment 可建立本地复合外键；跨 Deployment 只保存不可变外部引用，不建跨库外键。客户端声明的 `remote_unverified` 只能用于非权威追溯展示，不得据此参与幂等、结算、授权或安全告警归因。API 统一输出 `parent_request_ref`，不再并存裸 `retry_of` 语义。

### 13.7 鉴权与授权矩阵

以下为【已确认设计】目标矩阵；当前代码已有 JWT 用户/管理员路由、API Key 数据面鉴权、本地管理员授权和部分 step-up 基础，细粒度 scope、设备身份与客户端协同 API 尚未全部实现。

| 身份 | 管理 API | 客户端协同 API | 数据面转发 | requests/status | 禁止关系 |
| --- | --- | --- | --- | --- | --- |
| 平台用户 access token | 本人资源与获准治理 | 按 `client:*`/`catalog:*`/`usage:read:self` scope | 禁止 | 禁止 | 不得自动兑换成数据面权限 |
| 平台管理员 access token | 角色与 step-up 允许的管理资源 | 运维/兼容视图 | 禁止 | 禁止 | 管理员身份也不能冒充用户 API Key |
| 设备凭证【远期方向】 | 禁止 | 仅设备绑定的最小 scope | 禁止 | 禁止 | 不替代用户登录或本地管理员授权 |
| 云端 API Key | 禁止 | 第 13.2 节固定的受限自省 DTO 与最小 readiness | 云端数据面 | 同主体、同 Deployment/账本域 | 不得访问平台管理资源或内部拓扑 |
| 本地管理员授权 | 本地 Deployment 管理 | 本地发现、bootstrap、readiness | 禁止 | 禁止 | 不复用云端管理员或数据面 Token |
| 本地数据面 Token | 禁止 | 第 13.2 节固定的受限自省 DTO 与最小 readiness | 本地数据面 | 本地同主体、同账本域 | 不得取得管理员权限或内部拓扑 |
| `query_token` | 禁止 | 禁止 | 禁止 | 仅作关联句柄，仍需原数据面身份 | 单独持有不构成授权 |

管理资源的 ETag/`If-Match` 解决并发覆盖，幂等键解决重复提交，两者不能互相替代。高风险操作的 step-up 必须由服务端验证，前端隐藏按钮不构成授权控制。

### 13.8 CanonicalError、协议投影与示例

【已确认设计】内部错误先归一为 `CanonicalError`，再投影到协同/管理 envelope 或 OpenAI、Anthropic、Google 的协议原生错误。最小语义如下：

| 字段 | 含义 |
| --- | --- |
| `canonical_code` | 稳定机器码，如 `VERSION_INCOMPATIBLE`、`CAPABILITY_DISABLED`、`RATE_LIMITED` |
| `result_state` | `not_dispatched\|unknown\|terminal`，说明请求是否可能已经进入上游 |
| `retry_directive` | `never\|same_endpoint_after\|query_before_retry\|user_confirmation_required` |
| `retry_after_ms` | 可空退避时间，并同步 HTTP `Retry-After` |
| 关联 ID | `trace_id`、可用时的 `request_id`、`attempt_id`、`upstream_request_id` |
| `details` | 字段白名单的安全细节，不含秘密、正文或内部拓扑 |

`canonical_code → HTTP status → result_state → retry_directive` 唯一映射（【已确认设计】，冻结；同一 `canonical_code` 只允许一行投影，不得多义）：

| canonical_code | HTTP | result_state | retry_directive | 说明 |
| --- | --- | --- | --- | --- |
| `INVALID_QUERY_SELECTOR` / `MALFORMED_REQUEST` | 400 | `not_dispatched` | `never` | 请求体/选择器非法（如 status 查询两者皆无或皆有），修正后才是新请求 |
| `UNAUTHENTICATED` | 401 | `not_dispatched` | `never` | 身份缺失/无效；换有效凭据是新请求，不是重试 |
| `CAPABILITY_DISABLED` / `FORBIDDEN_SCOPE` | 403 | `not_dispatched` | `never` | 身份有效但缺 scope 或能力被关闭 |
| `IDEMPOTENCY_FINGERPRINT_CONFLICT` | 409 | `terminal` | `never` | 同一账本域+幂等键异指纹冲突；须换键或核对原请求，禁止静默重试 |
| `PRECONDITION_FAILED` | 412 | `not_dispatched` | `user_confirmation_required` | Head 强 ETag CAS 过期/不匹配；须重取当前 Head 再决策，不得盲目重放 |
| `PRECONDITION_REQUIRED` | 428 | `not_dispatched` | `never` | 写入 Head 缺必填 `If-Match`；补齐后为新请求 |
| `RATE_LIMITED_PRE_DISPATCH` | 429 | `not_dispatched` | `same_endpoint_after` | **pre-dispatch 网关限流**，请求确未触达上游，按 `retry_after_ms` 同端点安全重试 |
| `RATE_LIMITED_UPSTREAM` | 429 | `unknown` | `query_before_retry` | **上游限流**，可能已触达上游；先 §13.6 查询原 RequestRecord，不得直接重试 |
| `UPSTREAM_UNAVAILABLE` | 503 | `unknown` | `query_before_retry` | 已 dispatch 但结果未知且当前协商支持 status query，先查询后决策 |
| `UPSTREAM_UNAVAILABLE_NO_QUERY` | 503 | `unknown` | `user_confirmation_required` | 已 dispatch 结果未知但协议不支持查询，失败关闭或由用户确认潜在重复 |
| `RESULT_UNKNOWN_QUERY_REQUIRED` | 503 | `unknown` | `query_before_retry` | 通用「结果未知、必须先查询」投影，等价于 §13.6 `query_status=pending/not_found` 未收敛 |

同一 `canonical_code` 的 HTTP 投影在协同/管理 envelope 与协议原生错误间保持一致；只有 `result_state=not_dispatched` 且 `retry_directive=same_endpoint_after` 才允许按退避在同端点自动重试，`unknown` 一律先查询或失败关闭。

协同/管理 API 错误示例：

```json
{
  "code": "VERSION_INCOMPATIBLE",
  "message": "client contract major is unsupported",
  "result_state": "not_dispatched",
  "retry_directive": "never",
  "retry_after_ms": null,
  "trace_id": "t_example",
  "details": { "min_client_version": "1.2.0" }
}
```

`capabilities` 认证视图示例：

```json
{
  "capability_view": "authenticated",
  "contract_version": "1.0",
  "server_version": "<build-version>",
  "server_role": "local-edge",
  "deployment_id": "dep_example",
  "min_client_version": "1.2.0",
  "endpoint_descriptor_version": "1",
  "generated_at": "<rfc3339>",
  "expires_at": "<rfc3339>",
  "auth_methods": ["admin_x_api_key", "deployment_user_jwt", "data_plane_api_key", "local_data_plane_token"],
  "protocols": [
    {
      "protocol": "anthropic",
      "protocol_version": "2023-06-01",
      "data_plane_base_path": "/api",
      "idempotency_key": { "accepted": true, "header": "Idempotency-Key", "propagated": true },
      "processing_replay": "return_202_status",
      "completion_replay": "status_only",
      "stream_replay": "status_only",
      "status_query": { "supported": true, "method": "POST", "path_kind": "origin_absolute", "path": "/v1/requests/status", "query_token_header": "X-Request-Query-Token" }
    },
    {
      "protocol": "openai",
      "protocol_version": "v1",
      "data_plane_base_path": "/v1",
      "idempotency_key": { "accepted": false, "header": "Idempotency-Key", "propagated": false },
      "processing_replay": "native_error",
      "completion_replay": "status_only",
      "stream_replay": "status_only",
      "status_query": { "supported": true, "method": "POST", "path_kind": "origin_absolute", "path": "/v1/requests/status", "query_token_header": "X-Request-Query-Token" }
    }
  ],
  "usage_contract_version": "1.0",
  "pricing_version": "2026-07-19",
  "features": {
    "contribution_credentials": { "state": "disabled", "reason_code": "SECURITY_GATE_CLOSED" }
  },
  "deprecation": null
}
```

结果未知查询示例；HTTP Authorization 仍使用原请求的数据面身份：

请求体：

```json
{ "query_token": "qt_example" }
```

响应体：

```json
{
  "query_status": "pending",
  "safe_to_retry": false,
  "request_id": "r_example",
  "final_attempt_id": null,
  "attempts": [
    {
      "attempt_id": "a_example",
      "attempt_sequence": 1,
      "execution_state": "unknown",
      "settlement_state": "unmeasured",
      "effective_model": "<model>",
      "billable_usage": null
    }
  ],
  "billable_usage_total": null,
  "convergence_deadline": "<rfc3339>",
  "retain_until": "<rfc3339>"
}
```

503 不能只返回 `retryable=true`：`result_state=unknown` 且当前协商结果支持 attempt query 时返回 `query_before_retry`；不支持查询时返回 `user_confirmation_required` 或 `never` 并保持失败关闭。只有 `not_dispatched` 才允许按退避策略在同一 endpoint 重试。流式错误采用对应协议事件格式，但需尽可能保留相同 canonical code、结果状态与关联 ID。

## 14. 数据权威、一致性、同步与用量账本

核心原则：**谁执行谁记录原始事实。** 请求级原始用量由实际处理请求的中转节点记录；直连上游时，最终账单权威在上游，App 只能展示观察值或估算值。RouteBinding 只代表控制面绑定意图，RequestRecord 是节点侧逻辑请求与幂等/查询事实，RequestAttempt 是其下每次真实上游尝试与账本事实。App 不重算、不伪造账本。

结算权威分级（修订，解决“边缘记录进云端结算”过早引入的问题）：**P0/P1 阶段云端结算只依赖云端权威账本**，不接受任何边缘节点自报记录进入云端计费。边缘账本仅在其本地 Deployment 内自洽，用于本地用量与诊断展示，不上升为云端结算依据。“边缘账本接入云端统一结算”整体推迟到 **P2（并入 P2-5 统一结算与生态合作）**，且必须先定义签名 `EdgeSettlementReceipt` 才能开工，其最小要素为：节点证书与 `deployment_id`、单调递增序号（防重放/防丢号）、账本域与主体、measured/billable 用量、`pricing_version`、时间戳、覆盖前述字段的节点签名；配套对账（云端重算与边缘回执比对）、吊销（节点证书吊销即拒收其后续回执）、争议处理（回执与云端记录不一致时的冻结与人工裁决）流程。在 P2 该机制通过安全签字前，云端结算对边缘记录保持拒绝。

一致性机制（【已确认设计】）：

- **当前基础**：【已实现】Sub2API 计费路径已有 `request_id`、请求指纹/hash，并在 `usage_billing_dedup` 与 `usage_logs` 上按唯一键 `(request_id, api_key_id)` 做重复结算防护（`ON CONFLICT (request_id, api_key_id) DO NOTHING`）。但本次审计确认两个关键事实：其一，喂给防重键的 `request_id` 实际取自 `resolveUsageBillingRequestID`，**优先使用每次 HTTP 请求新生成的 `client_request_id`（`client:<uuid>`）**，其次才回退到调用方保留的 `X-Request-ID`（`local:<value>`）、上游响应 ID 或新生成值；`X-Request-ID` 中间件确实“有则保留、无则生成 UUID”，但它不是防重主键的首选来源。其二，计费/防重登记发生在**上游 dispatch 成功之后**（`RecordUsage` 在转发返回后经有界 worker 池提交）。因此当前机制只能防止“同一次网关请求被重复计费”，**不能防止同一逻辑请求被重复 dispatch 到上游**；客户端发起的逻辑重试因 `client_request_id` 每次新生成，仍会形成新的计费记录。现有 `request_id` 语义更接近一次网关请求，尚不足以承担跨重试的业务幂等。
- **P0-6 专项迁移边界**：【已确认设计】`expand` 先新增 `request_records`、`request_attempts`、`request_adjustments`，并在 `usage_logs`/结算条目增加可空 `request_attempt_id`；新路径以 RequestRecord/RequestAttempt 为同步权威，先登记与 claim、后 dispatch，UsageLog 只作为 attempt 的用量投影。不得把同一稳定 RequestRecord `request_id` 复制到多个仍受旧 `(request_id, api_key_id)` 唯一索引约束的 UsageLog；旧字段语义冻结为 `legacy_gateway_request_id`，新结算唯一键迁到 `attempt_id`/`adjustment_id`。历史数据采用一条 UsageLog 映射一条确定性的 legacy RequestRecord + RequestAttempt，不猜测多个历史行属于同一逻辑请求，并标记 `migration_origin=legacy`、`attempt_query_supported=false`。双读与对账通过且旧 writer 为零后，才 contract 旧 dedup/index；回滚只能切回旧读路径，不能让已经生成的新 attempt 再次扣费。
- **`request_id` 与 RequestRecord**：【已确认设计】由数据面入口在原子登记时生成稳定 `request_id`；支持受信逻辑请求 ID 的调用方可提供候选值，经规范化和冲突校验后关联。RequestRecord 使用 `PRIMARY KEY (deployment_id, request_id)`，只属于一个 `ledger_domain_id` 和 endpoint，可包含零个或多个 RequestAttempt。`request_id` 只要求在所属 Deployment 内稳定唯一；独立本地节点与云端节点没有共享事务数据库，跨 endpoint 或 Deployment 重新发起必须创建新的 RequestRecord，不能假设共享一个全局请求 ID。
- **`attempt_id`**：【已确认设计】由执行节点为每次上游尝试生成，在该 Deployment 内全局唯一。RequestAttempt 使用 `PRIMARY KEY (deployment_id, request_id, attempt_id)`，并同时建立 `UNIQUE (deployment_id, attempt_id)` 与 `UNIQUE (deployment_id, request_id, attempt_sequence)`；前者防止不同 RequestRecord 复用 attempt 标识，后者固定请求内顺序。节点内账号池重试、模型重试等在同一 RequestRecord 下形成新的 attempt；跨端点重新发起进入新的 RequestRecord，并产生新的 attempt。
- **`idempotency_key`**：【已确认设计】由支持该能力的调用方提供；节点对“账本域（至少包含 tenant/user、API Key 主体与 endpoint）+ idempotency_key”建立唯一约束。同一键、同一指纹的重放定位同一 RequestRecord 并返回既有结果，不得再次结算；同键不同指纹返回 409 类冲突并记录审计。各 CLI/协议能否注入和传播该键必须进入兼容矩阵；不能注入时只提供节点实际观察范围内的 request/attempt 级防重，禁止把逻辑重试宣传为幂等。
- **RequestRecord 最小逻辑模型**：【已确认设计】至少包含 `deployment_id`、`ledger_domain_id`、账本主体快照、`request_id`、`endpoint_id`、`endpoint_revision`、`idempotency_key_hash?`、`fingerprint_version`、请求指纹、`query_token_hash`、`trace_id?`、入口开闭状态、聚合状态、`final_attempt_id?`、`dispatch_owner_token?`、`dispatch_lease_until?`、单调 `dispatch_fence`、创建/收敛/保留时间及 RequestAttempt 关联。`final_attempt_id` 使用 `(deployment_id, request_id, attempt_id)` 复合外键保证属于本记录；原始幂等键、查询令牌、秘密和正文不得明文入库。
- **RequestAttempt 双状态轴**：【已确认设计】执行轴只允许：`registered → dispatching|failed|cancelled`，`dispatching → dispatched|failed|cancelled|unknown`，`dispatched → streaming|completed|failed|cancelled|unknown`，`streaming → completed|failed|cancelled|unknown`，`unknown → completed|failed|cancelled`。`registered` 只有在不存在 durable dispatch intent 时才可直接失败或取消并携带 `result_state=not_dispatched`；进入 `dispatching` 后即视为可能触达上游，崩溃恢复不得倒退为 `registered/not_dispatched`。所有终态不可回退。结算轴只允许 `unmeasured → measured|void`、`measured → settled|void`，`settled|void` 不可回退。退款、冲正和补偿只写关联该 attempt 的追加式 adjustment，不覆盖原执行/结算终态。外部 `query_status` 按第 13.6 节真值表聚合，不能把 `finalized` 同时解释为“上游结束、客户端收到和结算完成”三个事件。
- **RequestAttempt 最小逻辑模型**：【已确认设计】至少包含 `deployment_id`、`instance_id`、账本主体、`request_id`、`attempt_id`、`attempt_sequence`、`endpoint_id`、`endpoint_revision`、`trace_id?`、`requested_model`、`effective_model`、`upstream_request_id?`、`dispatch_fence`、`dispatch_intent_at?`、`dispatch_intent_hash?`、持久 `result_state`、执行/结算双状态、各阶段时间戳、measured/billable usage、`pricing_version`、币种、canonical outcome 与安全错误码。Token 分项使用非负 64 位整数，至少区分 input/output/cache read/cache write/reasoning；媒体计量使用 `(kind, unit, quantity)` 明细，数量与价格/金额采用 `DECIMAL(38,12)`，JSON 一律输出十进制字符串，禁止 float。`currency` 使用大写 ISO-4217 代码，`billable_usage_total` 只汇总非 void 的已结算基础值与追加式 adjustment。内部账本还关联实际 Channel/Group/Account 与现有 UsageLog 的不可变 ID 或快照版本，用于归因、排空和对账；这些内部关联不得进入普通用户或数据面 Key DTO。原始幂等键、秘密、提示词/响应正文不进入账本对象；物理分区、索引与归档由 P0-6 详细设计冻结。
- **跨域请求关联**：【已确认设计】RequestLink 使用 `link_id` 作为主键，并对 `(child_deployment_id, child_request_id, parent_deployment_id, parent_request_id, relation_type)` 建唯一约束；同 Deployment 可校验复合外键，跨 Deployment 只保留带 `created_by_principal`、`link_evidence` 与原因的 `remote_unverified` 外部引用。RequestLink 只用于非权威追溯和 UX，不参与幂等唯一键、结算归属、授权判断或安全事件归因。
- **失败切换分别记录**：首选目标失败后重新请求候选目标，各执行域、RequestRecord 和 `attempt_id` 分别记录真实消耗。只有已产生可计费用量的 attempt 才能结算；连接失败、鉴权前失败等未产生用量的尝试进入 `void`。一个 RequestRecord 下可能存在多个节点内 attempt；跨 endpoint 的新 RequestRecord 通过 RequestLink 或控制面 `trace_id` 关联。防重目标是“同一账本域与幂等键不重复结算、同一 attempt 不重复终结”，不能承诺所有失败尝试都零成本。
- **结果未知时失败关闭**：请求已发送上游但结果未知时，任何路径都不得直接自动跨节点重试；先按第 13.6 节使用稳定查询令牌或 `idempotency_key` 查询原 RequestRecord 的 `query_status=pending|finalized|aborted|not_found` 及其 `attempts[]`。无法收敛或 `not_found` 不能证明从未执行时，只能保持失败关闭，或由用户确认潜在重复执行与成本。
- **中断计费口径**：流式中断后是否按已产生的可测量 Token 计费、何时补偿属于【待决策】；确定前必须在账单和回退确认中向用户展示可能产生的部分成本。
- **字段白名单与缓存分层**：原始或敏感的 `/v1/usage`、Key 级明细及管理用量接口使用默认拒绝 DTO，任意数据面 Key 均不得获得 `account_cost`、钱包余额、订阅总额度以及账号、渠道、代理或管理员内部字段；这些响应设置 `Cache-Control: no-store` 并由黄金 JSON 测试固定。客户端协同 API 另提供字段最小化的聚合快照，不含账号/渠道/代理明细，可按明确 `ttl`、`version`、`ETag` 与主体范围缓存；App 只保存这类快照，并显示 `cache_state` 与 `snapshot_at`。
- **本地权威保护**：本地数据库是本地权威，远端备份不得静默覆盖；本地连接、项目、偏好只在本地内部一致，云端不得回灌覆盖。

【已实现】UsageLog 保存请求模型、实际上游模型、映射链、Token、缓存 Token、费用、倍率、时延、UA、IP 以及账号/渠道/分组关联等；现有 request_id 去重基础应保留并按 P0-6 迁移。RequestRecord、RequestAttempt/RequestLink、调用方幂等键、dispatch lease/fence/intent、状态查询和追加式补偿事件仍是【已确认设计】。`deployment_id + ledger_domain_id + idempotency_key_hash` 只保证同一 Deployment 内域内唯一登记，且同一 attempt 的执行轴、结算轴各自单终态；跨 endpoint 不共享唯一约束，不提供 global exactly-once。App 通过客户端协同 API 主动查询最小化聚合快照，快照必须包含 `source`、可空 `deployment_id`、`provider/billing_scope`、主体范围、`snapshot_at`、`cache_state`、TTL 与版本；IP 与 User-Agent 按用途最小化、设保留期限并向用户说明（【已确认设计】）。

## 15. 贡献账号与贡献房间产品设计

本地 `sub2api` 仓库的 `main` 分支相对其 `upstream/main` 超前 10 个提交，形成“账号贡献市场/贡献房间”，应视为核心领域能力。

【已实现】的 Sub2API Web/API 能力：

- 用户可贡献账号，覆盖通用提交、OpenAI OAuth URL/code、refresh token、mobile refresh token、Codex PAT 等路径。
- 用户可维护自有代理并做连通性测试。
- 用户可创建和管理贡献房间；owner 由当前登录用户隐式确定，创建时固定为 active/public，之后才可修改可见性和其他治理属性。
- 房间账号可限制共享并发和共享预算，记录已用预算和验证时间；该预算按原始 Token 成本 USD 计量，不等于消费者最终计费。
- 验证结果只保存模型族、来源类型、测试模型、状态和脱敏错误摘要，Schema 明确禁止保存凭据或原始上游错误。
- API Key 可选择多个他人的公开贡献房间，不能选择自己拥有的房间，并可配置公池回退和回退分组；默认不允许公池回退。
- 分组字段 `allow_contribution_pool` 默认关闭。
- 普通余额不足时，符合条件的请求可进入 `ContributionCreditOnly` 或 `OwnContributedAccountsOnly` 受限通道。
- 管理员可查看贡献列表、汇总、治理属性、测试和删除；管理接口采用无凭据视图。
- Sub2API 前端已有用户“账号贡献”“共享贡献房间”和管理员“贡献治理”“贡献房间”页面。
- 已有迁移和测试覆盖贡献市场、结算、房间、预算、验证、API Key 偏好、用户代理、共享并发、分组准入与所有权来源修复。

桌面 App 当前已有本地账号查询、编辑、测试、删除等管理集成，但未确认一键创建贡献或完整房间管理（【已实现】现状）。桌面端一键贡献、条款确认、撤回进度和房间管理属于【已确认设计】，不能把 Sub2API Web/API 的能力直接写成 App 已落地能力。

### 15.1 贡献的凭据语义

显式贡献到目标 Sub2API 部署是“禁止自动同步”的**有意例外**。目标可以是本地或云端部署；只有云端目标会产生云边凭据跨域。语义如下：

- 贡献必须是单独、显式、可撤销且有条款告知的动作。绝不能把“添加账号”隐式解释为“贡献到目标部署”。
- 【已实现】贡献接口会接收必要凭据并形成目标节点可调度账号。当前技术行为会在目标部署创建托管材料，但产品语义尚未决定用户应提供专用凭据，还是把既有凭据转移给目标部署；不得预设来源副本始终继续有效。
- 【已确认设计】贡献始终只允许经 HTTPS/TLS 传输；真实 loopback 可使用 HTTP 的全局例外不适用于贡献凭据提交，任何 HTTP 贡献入口都必须由服务端拒绝。
- 【已确认设计】服务端设置 `contribution_credentials_enabled=false` 为默认值。当前代码审计未确认已有该强制开关，因此当前基线按关闭能力和 No-Go 处理；在开关落地前不得部署为真实生产凭据入口。UI 隐藏、前端禁用或文案提示都不能替代服务端拒绝。
- 【已确认设计】字段级信封加密、最小权限解密和密钥轮换是贡献托管上线门槛。本次审计确认当前账号凭据以明文子字段存于 `accounts.credentials` JSONB，尚无字段级加密，因此该门槛未达成。
- 验证与日志：验证结果只保存脱敏摘要，禁止保存凭据或原始上游错误。
- 【待决策】贡献采用“专用凭据副本”还是“把既有凭据转移给目标部署”。推荐要求用户使用可单独撤销的专用凭据，降低撤回和泄漏影响。
- 【已确认设计】若采用专用凭据，目标部署只托管该专用凭据，来源端原账号可保持独立；若采用显式转移，目标部署成为唯一运行副本，来源端必须停止并禁止继续调度。目标部署激活与来源端停止调度是跨 Deployment 协调流程，不宣称跨端原子事务；在来源端停止得到确认前不得显示“转移完成”，无法确认时进入 `manual_action_required` 并提示可能存在双副本调度风险。界面在提交前展示目标部署、所选模式及后果。
- 【已确认设计】撤回按顺序执行：停止新调度、等待或终止在途请求、撤销缓存与工作副本、删除主存储密文、写入不含秘密的删除墓碑。备份副本的不可恢复时限、审计保留期和删除 SLA 属于【待决策】。
- 【已确认设计】只有字段级信封加密、旧 JSONB 迁移、密钥轮换、备份策略与撤回删除闭环全部通过发布验收，并绑定构建版本与安全签字后，才可由服务端显式把 `contribution_credentials_enabled` 置为 `true`。开发与测试环境只使用伪凭据或可随时作废的测试凭据。

贡献模式必须把“上游账号/权益所有者”“凭据保管方”和“运行调度权威”分开记录，不能用一个 `owner` 字段混合三种含义：

| 模式 | 上游账号/权益所有者 | 来源 Deployment | 目标 Deployment | 生效条件 | 撤回/失败语义 |
| --- | --- | --- | --- | --- | --- |
| 专用可撤销凭据 | 用户保持上游账号与权益控制；为贡献单独签发凭据 | 原账号可独立保留和运行，不因目标激活而失去权威 | 仅获得专用凭据的委托保管与调度权，不取得用户其他凭据 | 目标加密存储、验证通过且服务端闸门开启后 `active` | 撤销目标专用凭据不应影响来源原账号；目标按删除闭环清理全部副本 |
| 显式转移既有凭据 | 用户仍是上游账号/权益所有者 | 在目标 `target_ready` 后必须停止调度并确认，随后失去运行副本权威 | 来源停止确认后成为唯一运行保管与调度权威 | `target_ready → source_stop_pending → source_stopped → active` 全部有审计证据 | 任一步无法确认即 `manual_action_required`；在来源停止前不得宣称目标是唯一副本，也不得显示转移完成 |

两种模式下，目标 Deployment 都只取得用户明确授权范围内的**委托保管与运行权**，不会自动取得上游账号所有权、云端产品身份或来源 Deployment 的其他数据。跨域提交记录 `credential_mode`、来源/目标 Deployment、凭据世代、授权条款版本、目标保管状态和来源停止状态；App 仅保存不含秘密的协调引用与状态，不成为第三个凭据副本。

### 15.2 现有模型语义限制（须在产品层明确）

- `ContributionRoom.owner_user_id` 唯一，当前语义是一个用户最多拥有一个房间。
- `ContributionRoomAccount.account_id` 唯一，同一贡献账号目前只能加入一个房间。
- 账号所有权传播曾出现并修复缺陷，是导入、管理员编辑和共享治理的高风险回归区。

是否放开“一用户一房间/一账号一房间”属于【待决策】。

### 15.3 公开目录与治理边界

- 【已确认设计】普通用户可见的公开房间目录使用独立最小 DTO，只返回展示名、消费倍率、支持的平台/模型族、可用状态与聚合容量。禁止返回内部 `user_id`、账号名、逐账号预算/用量、验证时间、代理信息或任何凭据相关字段。
- 【已确认设计】owner 详情与管理员治理详情使用独立授权 DTO，并以黄金 JSON 测试保证字段不会串用。
- 【远期方向】删除、批量治理、API Key 变更等高风险管理员操作增加再认证/MFA、操作理由和审计；大范围删除或导出可采用双人审批。治理进程始终无权读取明文凭据。

### 15.4 桌面端贡献与撤回旅程

桌面 App 当前没有完整的一键贡献/撤回流程；以下均为【已确认设计】目标状态机：

```text
draft → target_precheck → credential_mode → terms_confirmed → submitting → verifying
专用凭据：verifying → active
显式转移：verifying → target_ready → source_stop_pending → active
source_stop_pending 无法确认 → manual_action_required
active → paused
active/paused → withdrawal_requested → scheduling_stopped → draining → primary_deleted
primary_deleted → backup_cleanup_pending → completed
任一步不可自动收敛 → manual_action_required
```

提交前必须展示并确认：目标 Deployment 与身份、是否跨越云边域、TLS/证书状态、服务端贡献开关、房间和公开字段、专用凭据或转移模式、来源副本后果、共享并发、原始成本预算、收益口径、主存储与备份删除 SLA。页面不得把“贡献额度”或“添加账号”按钮等同于已经完成这些确认。

流程不变量：

- `contribution_credentials_enabled=false` 时服务端拒绝，App 不提供可绕过路径；能力发现显示 `disabled/SECURITY_GATE_CLOSED`。
- 上传失败、目标验证失败或用户取消都不改变来源账号状态；选择“转移”时只能在目标确认可用后请求来源端停止调度，得到来源确认后才进入 active。`source_stop_pending` 失败时不得假定任一侧已成为唯一副本。
- 【待决策】显式转移无法收敛时的补偿动作需冻结为：重试停止来源、撤销目标副本，或经 step-up 后由人工采用某一侧；所有选择都保留双端审计与凭据暴露窗口记录。
- 撤回首先停止新调度，再处理在途请求和工作副本；“已撤回”必须区分“停止新调度”“主存储已删”“备份清理中”“彻底完成”。
- 每次状态变化返回审计 ID、Deployment、主体、时间与无秘密原因；跨端超时进入 `manual_action_required`，不能用一个成功 Toast 掩盖部分完成。
- 贡献失败 attempt、验证调用和撤回排空可能产生的费用按第 14、17 节展示，不与收益混为一个数字。

## 16. 安全、隐私、合规与本地数据保护

安全控制以可验证不变量和发布闸门表达。凡当前实现未满足的控制不得标为【已实现】，也不能只靠 UI 提示把风险降级为可接受状态。以下结论来自本地代码审计：

### 16.1 威胁模型与信任边界

【已确认设计】威胁评审以“资产—数据流—信任边界—威胁主体—预防—检测—响应—残余风险—owner”为最小单元；STRIDE 用于检查遗漏，不替代业务风险判断。每条 P0 数据流必须有 owner，任何未接受的高风险都阻断 Go。

| 资产/数据流 | 主要边界与威胁 | 预防控制 | 检测/响应 | 残余风险 owner |
| --- | --- | --- | --- | --- |
| 平台身份、API Key | App↔cloud；仿冒、越权、Key 泄漏、重放 | 身份分面、scope、最小 DTO、no-store、Key 轮换/撤销、IP 规则 | 异常鉴权/用量告警、吊销 playbook | 平台安全 |
| 上游/贡献凭据 | 用户/App↔目标 Deployment↔上游；泄漏、跨域误传、删除不完整 | 服务端默认关闭、TLS、专用凭据、信封加密、最小解密、删除墓碑 | 凭据金丝雀、调度停止、轮换与备份清理 | 安全 + 数据 |
| 本地配置、项目与会话索引 | App↔官方 CLI 文件；篡改、秘密残留、远端覆盖 | ConfigTransaction、hash/ACL、凭据引用化、本地权威、恢复 diff | 残留扫描、冲突检测、恢复 journal | 桌面端 |
| 请求账本与结算 | 客户端↔数据面↔上游；重复收费、伪造、未知结果误重试 | 原子幂等登记、RequestAttempt 双状态轴、追加式 adjustment | 对账、未知状态查询、账本异常告警 | 数据 + 网关 |
| 管理与破坏性操作 | 用户/管理员↔管理 API；提权、错误目标、审计擦除 | 服务端 RBAC/scope、step-up、ETag、目标 Deployment 固定、双人审批候选 | 高可靠审计、外部不可变锚、事件升级 | 安全 + 运营 |
| 自定义 Endpoint/代理/探测 | App/节点↔任意地址；SSRF、DNS 重绑定、元数据访问 | scheme/主机/端口/逐跳重定向校验、私网策略、超时和大小上限 | 目的地址分类、拒绝/异常率告警 | 网关安全 |
| 更新、镜像与迁移 | 发布系统↔App/Deployment；篡改、依赖投毒、降级、schema 不兼容 | 固定 digest、签名、SBOM、provenance、防降级、expand-migrate-contract | 签名验证、发布证明、回滚/前滚演练 | 发布工程 + 运维 |
| 备份与恢复 | 权威存储↔对象存储↔恢复环境；泄漏、篡改、错误覆盖 | 分类备份、独立加密/MAC、最小恢复权限、隔离 staging、环境绑定 | 恢复演练、完整性校验、删除与新鲜度告警 | 数据 + 运维 |

P0 详细设计需为每行补充数据流图、攻击前提、严重度、控制证据和风险接受记录。云端、多租户及企业私有部署还需按 Deployment 分别评审，不能把本机 loopback 结论复制到 LAN/公网。

#### 16.1.1 目标信任分级（loopback / LAN 私有 / 公网）

【已确认设计】LAN 目标同样跨主机、跨管理员边界，不能因为地址落在 RFC1918 就当作可信。信任与贡献确认按 `trust_class` 三级处理，对应 §11.1.1 的 TargetEndpoint：

| 信任级别 | 典型目标 | 传输/信任要求 | 贡献确认门槛 |
| --- | --- | --- | --- |
| `loopback` | `127.0.0.1`/`::1` 上的本机 Deployment | 可用 HTTP（贡献凭据提交除外，仍强制 TLS） | 仍需显式贡献确认；跨主机风险最低 |
| `lan_private` | 局域网/企业私有 Deployment（RFC1918、`.local` 等） | 强制 TLS；enrollment 用证书指纹（TOFU）或本地 CA/mTLS 绑定；私网地址本身不构成信任 | 提交前额外展示“目标在另一主机、由他方管理员控制”，需二次确认跨主机托管 |
| `public` | 公网 Deployment | 强制 TLS；绑定证书指纹与 `deployment_id`；管理/协同/数据面分权 | 按跨域凭据处理，展示云边跨域告知与专用凭据建议 |

当前 App 的 `Sub2ApiEndpointSelection` 仍接受非 loopback 的 RFC1918 HTTP（本次审计确认 `IsPrivateNetworkHost` 放行 10/172.16-31/192.168/169.254，`allowPublicHttp` 关闭时仍允许私网 HTTP），与上表 `lan_private` 强制 TLS 相冲突，属 P0-5 迁移债。贡献确认不因目标处于同一局域网而降低门槛：LAN 私有目标的贡献等同于把凭据交给另一台主机的管理员。

### 16.2 安全控制与发布闸门

| 领域 | 当前代码事实 | 目标控制与发布闸门 |
| --- | --- | --- |
| 平台会话与 App 凭据 | 【已实现】access token 只驻留内存；refresh token 由 DPAPI 持久化、轮换并支持登出撤销；本地管理员授权使用 Windows Credential Manager 或显式环境变量。连接 Key 仍可能出现在 profiles、`.bak`、官方配置、VS Code 设置、环境变量和普通备份中 | 【已确认设计】完成可重入引用化迁移；安全存储为唯一源；兼容投影最小化、受限 ACL、可恢复、可清理，并通过残留扫描 |
| 非加密 HTTP | 【已实现】当前云端来源可配置公网 HTTP，登录与部分已保存会话恢复路径仍可能在 HTTP 上传输密码或 refresh token，且不同页面恢复策略不一致 | 【已确认设计】HTTP 唯一例外是真实 loopback（`127.0.0.1`/`::1`）；LAN 与公网强制 TLS；贡献凭据提交即使在 loopback 也强制 TLS；拒绝并清理既有不安全会话。局域网信任采用用户确认的证书指纹或 mTLS，私网地址本身不构成信任 |
| Key 级用量隔离 | 【已实现】`/v1/usage` 的 `model_stats` 会序列化 `account_cost`，部分分支还返回订阅总信息或钱包余额 | 【已确认设计】原始/敏感用量响应采用默认拒绝的 Key 级 DTO、字段白名单、`Cache-Control: no-store` 与授权矩阵；另建可缓存的最小聚合快照 DTO，固定 source、可空 `deployment_id`、provider/billing_scope、主体范围、snapshot_at、TTL 与版本；任何数据面 Key 都不能读取账号成本、钱包、订阅总额度或内部账号/渠道/代理/管理员字段 |
| 上游与贡献凭据 | 【已实现】上游账号秘密以明文子字段存于 `accounts.credentials` JSONB（本次审计确认无字段级加密）；日志脱敏工具与无凭据治理视图已存在；未发现服务端强制关闭开关 | 【已确认设计】服务端 `contribution_credentials_enabled=false` 默认拒绝真实贡献凭据；OAuth token、PAT、代理密码和贡献凭据完成信封加密、最小权限解密、密钥轮换、旧数据迁移、备份与删除闭环并通过签字后才可显式启用 |
| 贡献公开目录 | 【已实现】普通登录用户可取得包含 owner 与逐账号信息的较完整房间视图，存在过度暴露风险 | 【已确认设计】公开目录改用最小 DTO，只含展示名、倍率、平台/模型族、可用状态和聚合容量；owner/admin detail 分离，并用黄金 JSON 测试固定边界 |
| 本地监听与局域网共享 | 当前已具备本机/局域网来源和中转控制 | 【已确认设计】新安装默认只绑定 `127.0.0.1` 与 `::1`，生成随机高强度数据面 Token；局域网共享须显式选网卡、配防火墙、启用 TLS、使用独立可撤销 Token，禁止 `0.0.0.0` 无差别暴露；管理 UI 校验 Host/Origin、CORS 与 CSRF |
| SSRF 与自定义上游 | 当前支持代理、自定义上游和连通性探测 | 【已确认设计】逐跳校验 scheme、主机、端口、DNS 重绑定、重定向、云元数据地址、私网策略、响应大小与超时 |
| 日志、崩溃与支持包 | 已有部分脱敏工具，完整覆盖未获证实 | 【已确认设计】所有 sink 前统一脱敏，禁止 Authorization、Cookie、API Key、OAuth/PAT、代理密码、带凭据 URL、原始上游错误、提示词/响应正文、工具参数、代码内容和完整本地路径；本地日志默认不上传，支持包见第 17 节 |
| UsageLog 隐私 | 【已实现】UsageLog 包含 IP、User-Agent 等诊断字段 | 【已确认设计】按用途最小化，明确保留期限与用户说明，优先保留聚合统计，过期执行可验证删除 |
| 结算幂等 | 【已实现】现有路径已有 request_id、指纹和 `(request_id, api_key_id)` 去重基础，但每次 HTTP 请求会生成新 UUID | 【已确认设计】增加稳定调用方幂等键、RequestRecord、RequestAttempt、请求级聚合状态查询、409 指纹冲突和追加式补偿事件；不支持幂等键的 CLI 只具备节点实际观察范围内的 request/attempt 防重，不承诺跨节点 global exactly-once；具体不变量见第 13、14、20 节 |
| 故障切换 | 当前配置驱动路径及未叠加 C 的托管交互路径不处于逐请求数据面 | 【已确认设计】未叠加 C 的 A/B 路径失败关闭或在用户确认后重连/重发；叠加 C 后即使可代理回退，也不得静默改变模型、费用来源、协议/账号族、工具语义或隐私级别。结果未知时先查询 RequestRecord 聚合状态，无法收敛则禁止自动跨节点重试 |
| 本地权威与恢复 | 【已实现】当前有 App/CLI 配置快照与恢复、Sub2API 系统配置白名单备份；后者明确不含用户、账号凭据、贡献和用量，不构成节点完整灾备 | 【已确认设计】恢复前验证来源、Deployment/环境 ID、`app_version`、`server_version`、`contract_version`、`db_schema_version`、`config_schema_versions`、`key_id/version`、签名/MAC/hash，先恢复到隔离环境、展示 diff 并创建本地快照；远端备份和云端策略不得静默覆盖本地数据库、连接、项目或偏好 |
| 系统密钥用途隔离 | 【已实现】JWT secret 可持久化在数据库 `security_secrets.value`；通用 AES 加密器直接使用 TOTP encryption key。支付恢复签名在未配置 `PAYMENT_RESUME_SIGNING_KEY` 时回退使用该旧键；显式配置后新签名已隔离，但仍保留旧键验证兼容。未显式配置时 TOTP key 只在进程内随机生成 | 【已确认设计】建立用途独立的 KEK/DEK、key_id/version 与轮换状态；根密钥不与密文同库或同备份；生产缺少持久受管密钥时 fail closed；旧密文与遗留签名迁移采用双读单写/双验单签并可验证吊销 |
| 更新供应链 | 【已实现】App 更新下载校验 HTTPS、产品名与 manifest 内 SHA-256；当前只下载验证，不自动执行 | 【已确认设计】增加独立固定根公钥、manifest/代码/镜像签名、SBOM、provenance、`security_epoch`、版本单调、授权回滚与吊销；普通降级一律拒绝，受签 rollback manifest 也不得越过安全下限；SHA-256 只证明下载内容匹配 manifest，不证明 manifest 来源可信 |
| 跨协议完整性 | 【已实现】本地协议桥与跨客户端路由已存在 | 【已确认设计】以真实 CLI 版本矩阵验证流式、工具、审批、取消、超时、重试、断连、错误语义、上下文隔离和回退 |
| 测试目录隔离 | 【已实现】当前组合根仍可能在已注入临时服务时自行调用默认 AppData 路径，构造过程会读取真实 Codex/Claude/Gemini/Grok/VS Code 配置 | 【已确认设计】从 composition root 注入完整 `AppDataPaths`；被测组件不得自行 `CreateDefault()`；所有 worker 使用随机临时根并覆盖 USERPROFILE、APPDATA、LOCALAPPDATA、HOME、XDG，路径守卫命中真实用户目录立即失败 |
| 贡献治理高风险操作 | 管理员路由已有鉴权与审计基础，贡献删除/测试/大范围变更的 step-up 未获证实 | 【远期方向】再认证/MFA、操作理由、敏感操作双人审批、参数校验与回归测试；治理执行进程保持无明文凭据权限 |
| 安全审计可靠性 | 【已实现】当前管理审计使用容量 4096 的非阻塞异步队列，队列满会丢弃并计数，批量写失败写 stderr；仓储支持按保留期删除和全表清空，尚无外部不可变证据 | 【已确认设计】高风险控制面审计先持久化或进入耐久队列，写入失败 fail closed；普通数据面降级需显式丢失计数、告警与策略；审计链使用签名/WORM 锚点，清理只能追加可核验墓碑，不能擦除清理动作本身 |

【待决策】商业化前需专项核验上游项目许可证、商业授权提示、贡献账号条款和各模型服务条款；“一用户一房间、一账号一房间”约束是否放开也需由第 21 节指定决策人拍板。

### 16.3 密钥目录、轮换与吊销

【已确认设计】所有系统密钥进入统一目录，最少记录 `purpose`、`key_id`、`version`、算法、状态（active/decrypt-only/revoked）、owner、受管存储、创建/轮换/吊销时间、可解密数据范围与备份依赖。用途至少拆分为：JWT/会话签名、TOTP secret 加密、支付配置与恢复令牌、备份归档、渠道监控秘密、上游/贡献凭据信封加密、App 更新签名和节点证书。

- 不同 purpose 使用独立密钥；禁止继续用 TOTP encryption key 充当通用应用加密根或支付签名根。
- 密文携带 `key_id/version`；轮换采用新写只用新 key、读取兼容新旧 key 的双读单写窗口，完成后台重加密和抽样验证后再吊销旧 key。
- 根密钥保存在 OS 安全区、HSM/KMS 或等价受管系统，不与业务密文存放在同一数据库或同一备份。开发环境可使用明确标识的临时 key，生产环境缺少持久 key 时拒绝启动相关能力。
- 吊销需联动 access/API Key、贡献调度、备份解密、节点证书和更新信任根；任何无法解密、仍有旧密文或未完成副本清理的状态都必须可见并阻断“轮换完成”结论。

### 16.4 隐私数据治理与破坏性操作不变量

| 数据类别 | 默认用途与位置 | 最小化/保留要求 | 用户权利与共享边界 |
| --- | --- | --- | --- |
| 凭据与密钥 | 安全存储或实际执行 Deployment | 不进入日志/遥测/普通备份；保留到撤销/迁移完成 | 显式录入、轮换、撤回；贡献另行授权 |
| 提示词、响应、工具参数、代码 | 官方 CLI/上游或实际请求链 | 默认不由 App/中转诊断持久化；确需保留须独立告知 | 不进入支持包或遥测；跨域需单独授权 |
| UsageLog 与账本 | 实际执行 Deployment | 只保留结算、争议和安全所需字段；IP/UA 期限【待决策】 | 按主体查询/导出；跨来源不混淆权威 |
| 本地项目、偏好、会话索引 | 本地 App 权威存储 | 用户控制；普通远端恢复不得覆盖 | 支持本地导出/删除；默认不上云 |
| 审计日志 | 对应管理域 + 外部不可变锚 | 高风险事件保留期与清理规则【待决策】 | 普通用户、owner、管理员视图分权 |
| 配置备份与灾备 | 分类对象存储/本地快照 | 逐字段分类、加密、完整性、保留与删除；“系统配置”也需检查邮箱、Prompt、Endpoint 等内容 | 恢复前预览范围；跨环境默认禁止覆盖 |

删除账号、批量变更、停止中转、撤回贡献、恢复备份、清理审计、密钥轮换等操作必须固定目标 Deployment、身份、影响范围、在途任务、可恢复性、step-up 与审计 ID。批量操作逐项返回，不以单一成功结果掩盖部分失败。

审计 fail-closed 不得阻止安全遏制。【已确认设计】普通高风险业务变更只有在审计事件与业务事务通过 outbox/同一可恢复状态机原子关联后才能提交；Sev0/Sev1 的降权、吊销、隔离、停止数据面和停止发布属于 fail-safe break-glass，可在主审计域故障时执行，但必须同步写入本地耐久、追加式应急日志或第二审计域，强告警并记录操作者、理由和目标。主审计恢复后必须补锚、对账和复核；break-glass 不能用于新增权限、恢复服务、删除证据或绕过普通业务审批。

### 16.5 信任协议、规范化与签名基线（【已确认设计】）

本节把散落在 §11、§13、§19 的规范化、摘要与签名要求收敛为单一权威来源，其他章节只交叉引用本节，不再各自定义原语。所有跨主体、跨端签署或比较的对象（EndpointDescriptor、`subject_scope_hash`、RouteBinding `scope_key`、请求指纹、`release_evidence_manifest`、`release_status_statement`、`release_status_checkpoint`、`p0_11_detached_attestation`）必须使用同一套规范化与摘要规则；任一实现分叉即安全缺陷。

#### 16.5.1 规范化与摘要（canonical form v1）

- **JSON 规范化**：统一 RFC 8785 JSON Canonicalization Scheme（JCS）。对象键按 UTF-16 码元排序、UTF-8 无 BOM 序列化、无多余空白；**拒绝重复键**（解析即失败，不做后写胜出），拒绝 `NaN`、`Infinity` 和负零 `-0`。JCS 载荷中的整数只有在绝对值不超过 `2^53-1` 时才允许使用 JSON number；更大的整数、所有计数/序号需要跨语言精确保存时一律使用十进制字符串。金额、计量、倍率统一使用 `DECIMAL(38,12)` 语义的十进制字符串，禁止 IEEE-754 浮点进入任何被签名或被比较的 payload。
- **时间**：一律 UTC、RFC 3339、固定 `Z` 后缀，格式为 `YYYY-MM-DDTHH:mm:ssZ` 或 schema 明确规定的三位毫秒 `YYYY-MM-DDTHH:mm:ss.SSSZ`；同一字段不得混用精度，不接受本地时区偏移、缺 `Z` 或其他等价写法。
- **摘要与线缆表示**：所有摘要都使用唯一的 `object_type` 域标签 `ai-relay/<object_type>/v1`，不允许出现裸的 `subject-scope/v1`、`endpoint-descriptor-body/v1` 或其他平行 domain tag。字段中的线缆字符串固定为 `sha256:<base64url-no-pad(raw_32_bytes)>`，不得改用十六进制、带填充 Base64 或裸 Base64。不同用途必须登记不同 `object_type`；Ed25519 detached signature 的线缆字符串固定为 `ed25519:<base64url-no-pad(raw_64_bytes)>`，签名输入由下方统一 helper 冻结。
- **统一摘要/签名 helper**：每类对象先从 unsigned payload 删除自身 `*_digest` 与所有 `signature` 字段，得到 `JCS(unsigned_payload)`；再计算 `payload_digest_raw = SHA-256(UTF8("ai-relay/<object_type>/v1") || 0x00 || JCS(unsigned_payload))`，序列化字段为 `sha256:<base64url-no-pad(payload_digest_raw)>`。无签名对象也必须使用同一摘要 helper，只是不附加 detached signature。detached 签名的唯一输入为 `UTF8("ai-relay/signature/<object_type>/v1") || 0x00 || payload_digest_raw`，结果序列化为 `ed25519:<base64url-no-pad(raw_signature)>`。因此“签 canonical bytes”“签 envelope JCS”和“签带签名字段的对象”都不是独立变体；正文中的简写 `SHA-256(canonical(...))` 均指向本 helper，并必须在 schema 中写出 `object_type`。
- **`*_digest` 自排除**：凡 payload 内含自身 `*_digest` 或外层 `signature` 字段的对象，其 canonical 计算必须显式排除这两个字段；排除规则必须写入该对象的 schema/验证器（与 §19.4 各 manifest/statement/checkpoint 规则一致）。
- **严格线缆解码**：`^sha256:[A-Za-z0-9_-]{43}$`、`^ed25519:[A-Za-z0-9_-]{86}$` 等正则只用于早期格式筛选，不能代替解码。验证器必须使用拒绝填充、拒绝非 URL-safe 字符、拒绝非法尾部位（`len % 4 == 1` 或最后一个字符带非零未用位）的 strict base64url 解码，再以同一无填充编码重新编码并做字节级相等校验；任何不规范但可宽松解码的值均拒绝。二进制长度必须分别等于 SHA-256 的 32 bytes、Ed25519 公钥的 32 bytes 或签名的 64 bytes。
- **对象注册表（`object-registry/v1`）**：以下表是唯一的 `object_type`、摘要自排除规则和签名数量注册；`object_type` 只能取注册值，调用方不得自由命名，实现不得为同一语义增加别名或“签 canonical bytes”的旁路。多签对象的 unsigned payload 必须包含按 `role,key_id` 排序的 `required_signers`（或等价固定集合）及 keyset/revocation 版本；`signatures[]` 只作为 detached 字段被 helper 排除。验证器必须从受信 key registry 校验 `key_id → purpose/role/status`，不得信任未受签外层自报的 `signer_role`，也不得用一个 key 跨 purpose。

| 对象 | 固定 `object_type` | unsigned payload / 自排除字段 | 签名约束（P0） |
| --- | --- | --- | --- |
| EndpointDescriptor body | `endpoint-descriptor-body` | body 全部字段；body 不带自身 digest/signature | 不直接签；由 envelope 的 `descriptor_body_digest` 间接绑定 |
| subject scope hash | `subject-scope` | preimage 对象全部字段；不带自身 digest/signature | 不签名；仅由服务端返回并由客户端比较 |
| EndpointDescriptor envelope | `endpoint-descriptor-envelope` | envelope 去掉 `signature` | 恰 1 个当前 Deployment descriptor key |
| RouteBinding scope key | `route-scope-key` | §11.4 冻结的 scope preimage；不带 digest/signature | 不签名；作为规范比较键 |
| request fingerprint | `request-fingerprint` | §13.6 的版本化 method/path/主体/endpoint/模型/语义头/body-hash preimage；不带 digest/signature | 不签名；用于同键冲突比较 |
| EndpointDescriptorKeySet | `endpoint-keyset` | 去掉 `keyset_digest`、`root_signature` | 恰 1 个 descriptor root key |
| EndpointRootTransition | `endpoint-root-transition` | 去掉 `transition_digest`、`signatures[]` | 恰 2 个经 registry 绑定的旧/新 root key，序列号和生效时间在 payload 内 |
| EndpointEnrollmentBundle | `endpoint-enrollment-bundle` | 去掉 `bundle_digest`、`signature` | registry 来源由 release root 签；local/OOB 来源必须带同源管理员签名证明；调用方提交的 URL/公钥不能成为 signer |
| 吊销快照 | `revocation-snapshot` | 去掉 `snapshot_digest`、`signature` | 恰 1 个对应 purpose 的 revocation authority key |
| trust bootstrap / recovery / rollback manifest | `trust-bootstrap-manifest` / `trust-recovery-manifest` / `rollback-manifest` | 各自去掉 `*_digest`、`signature` | 恰 1 个指定 bootstrap/recovery/release root key；额外审批作为独立受签 approval 对象，不依赖外层角色字段 |
| release candidate fingerprint | `release-candidate-fingerprint` | §19.4 冻结 preimage；不带 digest/signature | 不签名；由后续 manifest/attestation/Go 对象绑定 |
| release evidence manifest | `release-evidence-manifest` | 去掉 `manifest_digest`、外层 `signature` | 恰 1 个 release-evidence-manifest key；evidence owner approvals 逐 evidence set 独立签署 |
| release status statement | `release-status-statement` | 去掉 `statement_digest`、`signature` | 恰 1 个 release-status authority key |
| release status checkpoint | `release-status-checkpoint` | 去掉 `checkpoint_digest`、authority/witness signatures | 1 个 status authority 加 `q` 个不同 witness key；公网 profile 固定 `n=4,q=3,f=1`，`witness_set_id/n/q/f` 在 payload 内 |
| checkpoint witness statement | `checkpoint-witness` | 去掉 `witness_digest`、`signature`；固定 checkpoint digest、tree root、witness set/id 与 observed_at | 每个 witness 1 个独立 witness key；不得由 checkpoint authority 代签 |
| P0-11 detached attestation | `p0-11-detached-attestation` | 去掉 `attestation_digest`、`signatures[]` | 恰 4 个独立 role key：`release_engineering`、`architecture`、`security`、`operations`；四 role/key_id 必须在 payload 与 registry 同时满足 |
| attestation status statement | `attestation-status-statement` | 去掉 `status_digest`、`signature` | 恰 1 个 attestation-status authority key；`issued → active → revoked\|superseded\|expired` 由追加节点表达 |
| Go decision | `release-go-decision` | 去掉 `decision_digest`、`signatures[]` | 恰 4 个独立 role key：`product`、`architecture`、`security`、`test`；绑定最终 manifest、active attestation、activation statement 和评审时 checkpoint；部署前另行取得 fresh checkpoint |

`subject_scope_hash`、`route-scope-key`、`request-fingerprint` 与 `release-candidate-fingerprint` 是注册表中的**无签名对象**：它们仍使用各自 `ai-relay/<object_type>/v1` 摘要 helper，只是不附加 detached signature。客户端只比较或在权威端重算相应值，不能把无签名摘要当作签名或信任根。
- **跨语言 golden vectors（【已确认设计】，P0-4 冻结）**：canonical form v1 必须提供服务端（Go）与桌面端（.NET）逐字节一致的黄金向量集，二者对同一输入必须产出**同一** `JCS(...)` 字节串、同一 `payload_digest_raw` 与同一 `sha256:`/`ed25519:` 线缆串；任一语言实现分叉即 No-Go。向量集至少覆盖：`{"b":1,"a":2}` 键须重排为 `a,b`；含重复键的输入（如 `{"a":1,"a":2}`）解析即失败；`-0`、`NaN`、`Infinity` 拒绝；`2^53` 及以上整数只接受十进制字符串形式（`"9007199254740992"`），JSON number 形式拒绝；非 ASCII 键（如 `"é"` vs `"é"`）按 UTF-16 码元排序且不做 Unicode 归一；空对象/空数组/嵌套对象的稳定序列化；`domain_tag` 错配（同 bytes 不同标签）必须得到不同摘要。

#### 16.5.2 URI 规范化（uri-normalization v1）

TargetEndpoint、EndpointDescriptor、RouteBinding 与出站探测的地址在进入任何签名、比较或连接前，必须先经版本化 `uri-normalization v1`，并**分离 `origin` 与 `base_path` 两个独立字段**（不得把整段 URL 当作单一可信入口）：

| 规则 | 冻结口径 |
| --- | --- |
| scheme | 小写；仅接受 `https`（`http` 仅真实 loopback，见 §16.6）；其他 scheme 拒绝 |
| host | ASCII 大小写统一转小写；域名按 IDNA2008（UTS-46 non-transitional）转 A-label；full-width、控制字符、混淆同形字和无法唯一映射的 Unicode 拒绝 |
| IPv6 | 方括号包裹、小写、RFC 5952 压缩；zone id 拒绝 |
| 端口 | 显式端口规范化；等于 scheme 默认端口（https 443）时移除；范围外拒绝 |
| 路径 | 先检查并**拒绝** raw 或 percent-encoded dot segment（`.`、`..`、`%2e` 变体）及路径遍历，再对非保留字符 percent-decode/重新编码；**拒绝** `%2F`/`%5C`（encoded slash/backslash）、反斜杠 `\`、连续 `//`（`origin` 后除外）与任何规范化歧义 |
| userinfo | 一律拒绝（URL 内嵌 `user:pass@` 不接受） |
| query / fragment | 规范输入原始字符串只要出现 `?` 或 `#` 即拒绝（即使组件为空），不得静默剥离；descriptor 与 trust binding 只固定 `origin + base_path`。legacy 迁移可先解析以诊断，但非空 query/fragment 必须拒绝，不能把它当路径 |

`origin = scheme://host[:port]`，`base_path` 为规范化后的绝对路径前缀。`endpoint_id` 身份、`transport_binding`、`trust_binding` 只绑定 `(origin, base_path)`；descriptor 内出现的任何其他 URL 只能作为待校验数据，不得触发自动跳转（见 §13.2）。整段 URL 形态的 `base_uri`/`normalized_base_uri` 只作为 legacy 迁移输入，规范化后即拆为 `(origin, base_path)`，输出与签名 payload 中不得再出现。

query/fragment 的拒绝发生在任何规范化、签名或连接之前：普通 TargetEndpoint、EndpointDescriptor、RouteBinding 和出站探测输入若原始文本含 `?`/`#`，验证器必须返回稳定错误，不能先截断再继续。legacy `base_uri` 迁移器允许解析组件以产生可审计诊断，但遇到非空 query 或 fragment 必须失败；即便迁移器选择兼容空组件，也只能丢弃并记录迁移事件，生成的规范对象仍不得带 query/fragment。跨语言验证必须覆盖“原始带 query/fragment”“legacy 非空 query/fragment”“空组件兼容（若启用）”三类向量，并确保 Go/.NET 给出相同拒绝/规范化结果。

uri-normalization v1 golden vectors（【已确认设计】，Go/.NET 逐条一致；每条给出输入与期望「接受→规范化结果 / 拒绝→原因」）：

| 输入 | 期望 |
| --- | --- |
| `HTTPS://Edge.LOCAL:443/API/` | 接受 → `origin=https://edge.local`、`base_path=/API`（scheme/host 小写、默认端口移除、尾斜杠去除；路径大小写保留） |
| `https://xn--bcher-kva.example/v1` | 接受 → host 已是 A-label（`bücher`→`xn--bcher-kva`）；直接输入 U-label `https://bücher.example/v1` 亦须经 IDNA2008/UTS-46 non-transitional 得到同一 A-label |
| `https://аpple.com/v1`（西里尔 а 混淆同形） | 拒绝 → 混淆同形/无法唯一映射 |
| `https://example.com/a/../b` | 拒绝 → raw dot segment（路径遍历） |
| `https://example.com/a/%2e%2e/b`、`https://example.com/%2E/x` | 拒绝 → percent-encoded dot segment 变体 |
| `https://example.com/a%2Fb`、`https://example.com/a%5Cb` | 拒绝 → encoded slash/backslash |
| `https://user:pass@example.com/v1` | 拒绝 → 内嵌 userinfo |
| `https://example.com//v1`（origin 后连续 `//`） | 拒绝 → 连续斜杠歧义 |
| `http://10.0.0.5/v1` | 拒绝 → 非 loopback 明文（仅真实 `127.0.0.1`/`::1` 允许 http，见 §16.6） |
| `https://[2001:DB8::1]:8443/v1` | 接受 → `origin=https://[2001:db8::1]:8443`（IPv6 小写、RFC 5952 压缩；带 zone id 则拒绝） |

percent-decode/重新编码只对非保留字符进行，且发生在 dot-segment 与 encoded slash/backslash 拒绝检查**之后**；先解码再判定会绕过遍历防护，属实现缺陷。

#### 16.5.3 `subject_scope_hash` 冻结 preimage

`subject_scope_hash` 是不含任何秘密的版本化、域分隔摘要，用于把「同一认证主体的受限 self 视图」与 EndpointDescriptor 绑定。**客户端只比较服务端返回值是否与既登记值一致，绝不自行重算 preimage**（避免客户端与服务端算法漂移导致误信任）。冻结 preimage 如下：

```text
subject_scope_hash =
  sha256:<BASE64URL_NOPAD( SHA-256(
    "ai-relay/subject-scope/v1" || 0x00 ||
    JCS({
      "scope_hash_version": "1",
      "deployment_id": <string>,
      "subject_class": "deployment_user|admin_api_key|data_plane_api_key|local_data_plane_token|upstream_provider",
      "subject_opaque_id": <string>,            // 至少 128 bit 随机、不可逆、稳定、非 PII 的主体标识
      "audience": [<string>, ...],              // 排序后的受众集合
      "effective_scopes": [<string>, ...],      // 去重并按码元排序的有效 scope
      "authz_policy_version": <string>          // 授权策略/角色映射版本
    })
  ) )>
```

preimage 必须使用统一 `ai-relay/subject-scope/v1` 域分隔、字段固定、集合排序去重；不得包含凭据、令牌、原始 IP 或提示词。`authz_policy_version` 变化即 `subject_scope_hash` 变化，触发既有 descriptor 重新协商。服务端在受限 capabilities 自省视图与 EndpointDescriptor envelope 中返回同一值；App 登记后逐次比较，不匹配即拒绝复用并重新走 nonce challenge。

**keyed 决策（【已确认设计】）：`subject_scope_hash` 为非 keyed 的域分隔 SHA-256，不采用 HMAC-SHA-256。** 理由有二：其一，preimage 中的 `subject_opaque_id` 已是 ≥128 bit 高熵、不可逆、非 PII 的主体标识，且 `deployment_id` 参与域分隔，使离线枚举/确认在计算上不可行，keying 不增加实质抗攻击性；其二，`subject_opaque_id` 等 preimage 输入**不下发给客户端**，客户端本就缺输入、无法重算，"只比较服务端返回值、绝不自行重算 preimage"已被**结构性**保证而非仅靠约定。线缆表示沿用 §16.5.1 的 `sha256:<base64url-no-pad>`，`object_type` 之外不引入 keyed 前缀，避免 §13.2 schema 与既有向量变更。若未来威胁模型升级到需要 keyed，必须整体切到显式 `hmac-sha256:` 线缆前缀并同步 §13.2 pattern，且仍保留"客户端只比较服务端返回值"这一条（keyed 后客户端更无从重算，规则不变）。

cross-language golden vector（【已确认设计】）：给定固定 preimage（`deployment_id`、`subject_class`、`subject_opaque_id`、排序后 `audience`、去重排序 `effective_scopes`、`authz_policy_version` 全部为具名常量）时，Go 与 .NET 必须产出同一 `subject_scope_hash`；改变 `authz_policy_version` 或 `effective_scopes` 顺序无关但集合有别时摘要必变，用于回归夹住"策略版本漂移即重协商"。

#### 16.5.4 签名与信任根（P0 基线）

- **算法**：P0 使用 Ed25519 detached 签名，严格采用 §16.5.1 的 object helper；验证器不得在“签 canonical bytes”“签 envelope JCS”“签带签名字段对象”之间自行选择。密钥 metadata 固定 `signing_key_id`、算法、用途、状态（active/verify-only/revoked）、有效期、keyset generation 与吊销快照版本，进入 §16.3 密钥目录。
- **用途隔离**：descriptor、release manifest、release status、checkpoint witness、attestation、TLS/node identity 使用独立 key purpose、独立 owner 和独立吊销域；同一公私钥不得跨 purpose 或跨 object type 使用。签名根、节点证书根和发布根也不得互相替代。
- **EndpointDescriptorKeySet**：每个 Deployment 维护单调的 `EndpointDescriptorKeySet`，最小字段为 `deployment_id`、`keyset_revision`、`previous_keyset_digest?`、`keys[{kid,alg=Ed25519,public_key,status=active|verify_only|revoked,not_before,not_after,revoked_at?}]`、`revocation_snapshot_version` 和由 descriptor root 签署的 `root_signature`。`public_key` 线缆表示为 `ed25519:<base64url-no-pad(raw_32_bytes)>`；keyset revision、时间和用途均进入签名 payload。
- **公钥发现**：EndpointDescriptor 签名公钥必须通过与首次传输信任同源的通道建立（TLS SPKI pin / TOFU 指纹 / 本地 CA / mTLS，见 §16.6 与 §13.5），descriptor 自签名本身不建立首次信任。App 首次登记时保存 keyset digest、generation、root id 和吊销快照版本。
- **轮换**：计划轮换采用“新签只用新 key、验证兼容新旧 key”的双验单签窗口，并发布由旧根和新根共同签署的 `EndpointRootTransition`（含旧/新 keyset digest、生效时间、generation 和 transition serial）。旧 key 在窗口结束后只能 `verify_only`，不得重新签发；静默替换、generation 回退或 transition 缺签均拒绝。
- **根恢复与吊销**：旧根失陷只能使用独立 recovery root 或重新 OOB enrollment，不能以当前 descriptor 自签名自证。吊销通过版本化吊销快照表达；descriptor、attestation、rollback manifest 的验证必须证明所用 key 在当次校验时未被吊销。无法取得当前吊销快照、快照过旧或 keyset 不可达时立即 No-Go，不得沿用旧结论。

#### 16.5.5 EndpointEnrollmentBundle 来源约束

首次把一个 Deployment/upstream 端点纳入信任所需的 enrollment 材料（`EndpointEnrollmentBundle`：共同绑定预期 `origin/base_path` 与 transport anchor；Deployment 分支另绑定 `deployment_id/server_role/descriptor_key_anchor`，upstream 分支改为 `provider_id/provider_identity_policy_digest`）**只允许来自以下三类受控来源**，其他任何来源（尤其调用方在请求中回显的 URL/公钥）一律不作为信任根：

1. `source=signed_registry`：随 App/发布制品预置且受发布签名保护的 registry（内置受信 Deployment 目录）；
2. `source=local_loopback`：真实 loopback 上的 local-edge 管理面，并由受管服务/进程或 ACL 保护的本地身份材料证明；
3. `source=oob_admin`：管理员带外（OOB）交付，在受信信道展示并由用户确认的 QR/指纹串（用于 LAN/公网首次 TOFU）。

`EndpointEnrollmentBundle` 使用闭合的 `bundle_version="1"`，摘要与签名采用 §16.5.1 的 `object_type=endpoint-enrollment-bundle`。规范输出只使用 `endpoint_kind=deployment|upstream`、`source=signed_registry|local_loopback|oob_admin`、`transport_anchor` 等下列字段；早期 `owner_kind/source_kind/anchor/upstream_provider_id` 只能由显式 legacy 迁移器读取，不能作为规范输出或签名输入。

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://spec.local/endpoint-enrollment-bundle/v1.json",
  "title": "EndpointEnrollmentBundle",
  "oneOf": [
    { "$ref": "#/$defs/DeploymentBundle" },
    { "$ref": "#/$defs/UpstreamBundle" }
  ],
  "$defs": {
    "Rfc3339UtcSeconds": {
      "type": "string",
      "format": "date-time",
      "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"
    },
    "Digest": { "type": "string", "pattern": "^sha256:[A-Za-z0-9_-]{43}$" },
    "Signature": { "type": "string", "pattern": "^ed25519:[A-Za-z0-9_-]{86}$" },
    "SigningKeyId": { "type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$" },
    "Origin": {
      "type": "string",
      "pattern": "^https?://[^/?#]+$",
      "description": "§16.5.2 uri-normalization v1；Schema 之外仍须验证 host、loopback HTTP、userinfo 与实际连接"
    },
    "BasePath": {
      "type": "string",
      "pattern": "^/(?:[^/?#%]|%[0-9A-Fa-f]{2})*$",
      "description": "§16.5.2 uri-normalization v1；dot segment、encoded slash/backslash 由验证器拒绝"
    },
    "DescriptorKeyAnchor": {
      "type": "object",
      "additionalProperties": false,
      "required": ["root_key_id", "keyset_digest"],
      "properties": {
        "root_key_id": { "$ref": "#/$defs/SigningKeyId" },
        "keyset_digest": { "$ref": "#/$defs/Digest" }
      }
    },
    "ProcessIdentity": {
      "oneOf": [
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["identity_kind", "service_sid", "binary_digest"],
          "properties": {
            "identity_kind": { "const": "managed_service" },
            "service_sid": { "type": "string", "minLength": 1 },
            "binary_digest": { "$ref": "#/$defs/Digest" }
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["identity_kind", "publisher_thumbprint_sha256", "binary_digest"],
          "properties": {
            "identity_kind": { "const": "signed_binary" },
            "publisher_thumbprint_sha256": { "$ref": "#/$defs/Digest" },
            "binary_digest": { "$ref": "#/$defs/Digest" }
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["identity_kind", "pipe_name", "server_sid", "acl_digest"],
          "properties": {
            "identity_kind": { "const": "named_pipe_acl" },
            "pipe_name": { "type": "string", "minLength": 1 },
            "server_sid": { "type": "string", "minLength": 1 },
            "acl_digest": { "$ref": "#/$defs/Digest" }
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["identity_kind", "launch_token_digest", "pid_start_time", "binary_digest"],
          "properties": {
            "identity_kind": { "const": "managed_process" },
            "launch_token_digest": { "$ref": "#/$defs/Digest" },
            "pid_start_time": { "$ref": "#/$defs/Rfc3339UtcSeconds" },
            "binary_digest": { "$ref": "#/$defs/Digest" }
          }
        }
      ]
    },
    "TransportAnchor": {
      "oneOf": [
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["anchor_kind", "server_spki_sha256"],
          "properties": {
            "anchor_kind": { "const": "tls_spki" },
            "server_spki_sha256": { "$ref": "#/$defs/Digest" }
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["anchor_kind", "ca_cert_sha256"],
          "properties": {
            "anchor_kind": { "const": "local_ca" },
            "ca_cert_sha256": { "$ref": "#/$defs/Digest" }
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["anchor_kind", "server_spki_sha256", "client_trust_domain_digest"],
          "properties": {
            "anchor_kind": { "const": "mtls" },
            "server_spki_sha256": { "$ref": "#/$defs/Digest" },
            "client_trust_domain_digest": { "$ref": "#/$defs/Digest" }
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["anchor_kind", "dns_name", "ca_policy_digest"],
          "properties": {
            "anchor_kind": { "const": "web_pki_dns" },
            "dns_name": { "type": "string", "minLength": 1 },
            "ca_policy_digest": { "$ref": "#/$defs/Digest" }
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["anchor_kind", "loopback_address", "process_identity"],
          "properties": {
            "anchor_kind": { "const": "verified_loopback" },
            "loopback_address": { "enum": ["127.0.0.1", "::1"] },
            "process_identity": { "$ref": "#/$defs/ProcessIdentity" }
          }
        }
      ]
    },
    "DeploymentBundle": {
      "type": "object",
      "additionalProperties": false,
      "required": ["bundle_version", "bundle_id", "endpoint_kind", "deployment_id", "server_role",
        "origin", "base_path", "transport_anchor", "descriptor_key_anchor", "source",
        "source_proof_digest", "issued_at", "expires_at", "signing_key_id",
        "revocation_snapshot_version", "bundle_digest", "signature"],
      "properties": {
        "bundle_version": { "const": "1" },
        "bundle_id": { "type": "string", "format": "uuid" },
        "endpoint_kind": { "const": "deployment" },
        "deployment_id": { "type": "string", "minLength": 1 },
        "server_role": { "enum": ["local-edge", "cloud-control-data-plane"] },
        "origin": { "$ref": "#/$defs/Origin" },
        "base_path": { "$ref": "#/$defs/BasePath" },
        "transport_anchor": { "$ref": "#/$defs/TransportAnchor" },
        "descriptor_key_anchor": { "$ref": "#/$defs/DescriptorKeyAnchor" },
        "source": { "enum": ["signed_registry", "local_loopback", "oob_admin"] },
        "source_proof_digest": { "$ref": "#/$defs/Digest" },
        "issued_at": { "$ref": "#/$defs/Rfc3339UtcSeconds" },
        "expires_at": { "$ref": "#/$defs/Rfc3339UtcSeconds" },
        "signing_key_id": { "$ref": "#/$defs/SigningKeyId" },
        "revocation_snapshot_version": { "type": "integer", "minimum": 1, "maximum": 9007199254740991 },
        "bundle_digest": { "$ref": "#/$defs/Digest" },
        "signature": { "$ref": "#/$defs/Signature" }
      },
      "allOf": [
        {
          "if": { "properties": { "source": { "const": "local_loopback" } }, "required": ["source"] },
          "then": { "properties": { "transport_anchor": { "type": "object", "properties": { "anchor_kind": { "const": "verified_loopback" } }, "required": ["anchor_kind"] } } }
        }
      ]
    },
    "UpstreamBundle": {
      "type": "object",
      "additionalProperties": false,
      "required": ["bundle_version", "bundle_id", "endpoint_kind", "provider_id",
        "origin", "base_path", "transport_anchor", "source", "source_proof_digest",
        "provider_identity_policy_digest", "issued_at", "expires_at", "signing_key_id",
        "revocation_snapshot_version", "bundle_digest", "signature"],
      "properties": {
        "bundle_version": { "const": "1" },
        "bundle_id": { "type": "string", "format": "uuid" },
        "endpoint_kind": { "const": "upstream" },
        "provider_id": { "type": "string", "minLength": 1 },
        "origin": { "$ref": "#/$defs/Origin" },
        "base_path": { "$ref": "#/$defs/BasePath" },
        "transport_anchor": { "$ref": "#/$defs/TransportAnchor" },
        "source": { "enum": ["signed_registry", "local_loopback", "oob_admin"] },
        "source_proof_digest": { "$ref": "#/$defs/Digest" },
        "provider_identity_policy_digest": { "$ref": "#/$defs/Digest" },
        "issued_at": { "$ref": "#/$defs/Rfc3339UtcSeconds" },
        "expires_at": { "$ref": "#/$defs/Rfc3339UtcSeconds" },
        "signing_key_id": { "$ref": "#/$defs/SigningKeyId" },
        "revocation_snapshot_version": { "type": "integer", "minimum": 1, "maximum": 9007199254740991 },
        "bundle_digest": { "$ref": "#/$defs/Digest" },
        "signature": { "$ref": "#/$defs/Signature" }
      },
      "allOf": [
        {
          "if": { "properties": { "source": { "const": "local_loopback" } }, "required": ["source"] },
          "then": { "properties": { "transport_anchor": { "type": "object", "properties": { "anchor_kind": { "const": "verified_loopback" } }, "required": ["anchor_kind"] } } }
        }
      ]
    }
  }
}
```

时间统一到秒级 `YYYY-MM-DDTHH:mm:ssZ`，验证器还必须检查 `expires_at > issued_at`。`endpoint_kind=deployment` 必须携带 `deployment_id/server_role/descriptor_key_anchor`；`endpoint_kind=upstream` 必须携带 `provider_id`，并因闭合 Schema 明确禁止 `deployment_id/server_role/descriptor_key_anchor`，direct 上游不套用 Deployment descriptor 语义。`verified_loopback` 的 `process_identity` 必须包含稳定 SID、签名二进制、命名管道 ACL 或 App 管理的启动令牌与二进制 digest，单独 PID、端口或 loopback 地址不足以建立信任。

Bundle 的外层签名必须由 `source` 对应的可信 registry/local-admin/OOB key 验证，且该 `signing_key_id` 的 purpose 与来源在受保护 registry 中匹配；签名正确只证明 bundle 来源，不能替代实际 TLS/OS identity 逐跳验证。Bundle 只能使用一次来建立或明确轮换 trust binding；`bundle_id` 重放、过期、来源/分支错配、Deployment 字段出现在 upstream 分支、loopback 缺稳定进程身份或 anchor 与实际连接不一致时均保持 `untrusted_candidate`。

**首次信任顺序固定为**：`untrusted_candidate → anchor_verified → principal_authenticated → descriptor_verified → active`；信任撤销或身份突变进入 `revoked|re_enrollment_required` 终态，计划轮换经过 `rotation_pending`，终态不得自动复活。`EndpointEnrollmentBundle` 未验证前只能保存候选和非敏感元数据，禁止发送 `Authorization`、Cookie、API Key、上游凭据或依赖这些秘密的 self capabilities 请求。App 先从预置 registry、ACL 保护的真实 loopback 通道或管理员 OOB 材料取得并核对 expected origin/base_path 与 transport anchor；Deployment 分支再核对 `deployment_id/server_role/descriptor_key_anchor`，upstream 分支只核对 `provider_id/provider_identity_policy_digest`，不得套用 Deployment 字段。逐跳传输校验通过后才进入 `anchor_verified`；Deployment 随后可发送最小权限数据面 Key 取得 self capabilities/`subject_scope_hash`，完成 nonce-bound descriptor、签名、主体、entrypoint 和 transport 校验后进入 `descriptor_verified`；upstream 则按 provider identity policy 验证主体与入口，不要求 EndpointDescriptor。最后把 trust binding 持久化为 `active`。没有 bundle/anchor 的候选保持未配置，不能通过“端点自我描述”建立信任。

#### 16.5.6 checkpoint 防分叉与 attestation 可验证吊销

- **防分叉**：`release_status_checkpoint`（§19.4）同时承诺同一 release 的两条独立状态链：release lifecycle 的 `release_status_max_serial + release_chain_head_digest`，以及当前 attestation lifecycle 的 `attestation_status_max_serial + attestation_chain_head_digest`（attestation 链作用域为 `(release_authority_id, release_id, attestation_id)`；无 attestation 时 serial=0、head=null）。checkpoint 还携带 authority epoch、透明日志 tree root、inclusion/consistency proof、`witness_set_id`、`witness_n/q/f`、`current_manifest_digest/current_manifest_revision`、`current_attestation_digest` 与 `attestation_state=none|issued|active|revoked|superseded|expired`。公网 profile 的候选参数为 `n=4,q=3,f=1`，必须满足 `2q-n>f`；缺少批准的 witness policy、见证不可达、quorum 不足、split-view、proof 缺失或 gossip head 不一致均 No-Go。验证器必须用一次性 nonce 取得 checkpoint，并分别补齐两条链从受保护 trust state 已知 head 到 checkpoint head 的完整连续节点，使两组 `serial/head` 与 checkpoint 精确一致；同 serial 不同 digest、prev 不匹配、任一 serial/head 回退或无法补齐即 No-Go。checkpoint 最大 TTL 为 300 秒，允许未来时钟偏差最多 60 秒；过期或剩余有效期不足时必须重新 challenge，last-known-good 不能放行新 Go。
- **attestation 未吊销可验证**：`p0_11_detached_attestation` 的有效性在每次 Go 与部署前重新判定，必须同时满足签名有效、未过期、`signing_key_id` 在当前吊销快照中未吊销、且精确绑定同一 `manifest_digest`/`candidate_fingerprint`/`p0_11a_evidence_set_digest`/初始 checkpoint。独立的 `attestation_status_statement` 状态只允许 `issued → active → revoked|superseded|expired`，终态不可复活；issued 或缺少 activation statement 不能 Go。最终 checkpoint 必须包含 `current_attestation_digest` 且 `attestation_state=active`，无法验证吊销、状态链或 activation 时按 No-Go 处理。

### 16.6 TLS 与传输安全基线（【已确认设计】，P0 冻结）

本节把「TLS 与证书工程基线」从纯【待决策】收敛为可自动测试的 P0 强制基线；仅证书签发/轮换/吊销的具体运营参数保留【待决策】（见 §21，owner：安全 + 运维）。所有 local/cloud 数据面、协同面、管理面、贡献提交与出站探测（含每一跳重定向、正向代理、WebSocket 升级）都适用同一基线，不因 loopback 名称、系统代理或“私网地址”放宽。

| 维度 | P0 冻结口径 |
| --- | --- |
| 协议版本 | 优先 TLS 1.3；兼容 TLS 1.2；**禁用 TLS 1.1 及以下、SSLv3、全部 CBC 套件、静态 RSA 密钥交换与静态 DH** |
| TLS 1.2 套件 | 仅允许 ECDHE 前向保密 + AEAD：`ECDHE-*-AES128-GCM`、`ECDHE-*-AES256-GCM`、`ECDHE-*-CHACHA20-POLY1305`；其余拒绝 |
| TLS 1.3 套件 | `TLS_AES_128_GCM_SHA256`、`TLS_AES_256_GCM_SHA384`、`TLS_CHACHA20_POLY1305_SHA256` |
| 密钥交换群 | 现代 ECDHE 群（如 X25519、secp256r1/secp384r1）；弱群拒绝 |
| SNI / 主机名 | 强制发送 SNI；强制主机名与用途（EKU）校验；证书 CN/SAN 与规范化 host 匹配 |
| ALPN | 按协议协商 ALPN；协商结果与实际使用协议一致 |
| 证书链 | 校验完整链、有效期（notBefore/notAfter）、吊销状态；LAN/公网按 §16.1.1 用 TOFU 指纹 / 本地 CA / mTLS 绑定；`deployment_id` 与证书身份绑定，突变触发重新信任 |
| 逐跳一致 | 每一跳重定向、正向代理（HTTP CONNECT/SOCKS5/5h）与 WebSocket(WSS) 升级都重复上述校验；跨 origin 跳转剥离 Authorization/Cookie/代理凭据 |
| 代理头信任 | 未经认证的 `X-Forwarded-For`/`X-Forwarded-Proto`/`X-Forwarded-Host` 等一律不可信，不据此判定“已 TLS 终止”或来源可信；只有受控入口链路的可信代理头可用 |
| loopback 例外 | 仅真实 `127.0.0.1`/`::1` 的非贡献表面允许明文 HTTP（见 §16.2）；贡献提交即使 loopback 也强制 TLS |

TLS 终止位置必须明确：公网入口在受控边界终止 TLS 后，内网链路按零信任策略处理，不得把“边界已终止 TLS”当作内部明文合法化依据。验收见 §20.3「loopback/LAN 安全」「SSRF、代理与出站探测」两行，并补充 TLS 版本/套件协商与降级拒绝的显式断言。

## 17. 可用性、降级、可观测性与诊断

- 可用性：`user_mode=none|local|cloud|hybrid` 按可用条件派生，用户切换的是 Endpoint、策略或 RouteBinding；界面同时显示派生模式、RouteBinding 与最近一次可确认的实际执行来源。
- 降级：云端管理面不可用时，本地中转与已保存数据面 Key 可按各自有效性继续工作；缺失的云端权威数据标记“暂不可用”，不臆造余额、额度或账单。
- 回退透明：未叠加 C 的 A/B 路径切换绑定后重连或重发；叠加 C 的路径所执行的自动回退可见、可审计、可关闭，携带 `reason_code`，且仅在语义等价与预授权下进行。

### 17.1 数据来源与权威标记

诊断、用量和成本展示必须区分 local、cloud、direct 三类来源；缓存是数据新鲜度状态，不是第四种来源：

| 来源 | 数据含义 | 权威边界 | UI 要求 |
| --- | --- | --- | --- |
| local | 本地中转的 RequestAttempt、节点健康与聚合快照 | 本地执行节点是请求级事实权威 | 显示 `source=local`、`deployment_id`、主体范围与 `snapshot_at` |
| cloud | 云端中转的 RequestAttempt、平台账单、服务摘要与聚合快照 | 云端节点/平台账本是其执行与结算权威 | 区分数据面执行事实和管理面余额/套餐 |
| direct | 直连上游时 App 可观察到的模型、Token 或估算成本 | 最终费用与官方用量以上游服务商为权威 | `deployment_id=null`，显示 provider/billing_scope，并明确标为“观察值/估算值”，不得混入中转账本 |
App 主动查询节点或上游适配器的聚合快照，不接收含糊的“自动回写”。跨来源聚合必须保留 `source=local|cloud|direct`、可空 `deployment_id`、`provider/billing_scope`、tenant/user 或 API Key 主体范围、`snapshot_at` 与 `cache_state=live|cached`，避免本地和云端相同整数 ID 发生碰撞。`cache_state=cached` 时同时显示 TTL、缓存版本和非实时提示，且必须保留原始 source；请求级原始事实仍留在执行节点，App 缓存副本不升级为权威。

### 17.2 统一诊断（【已确认设计】）

- 按来源分区显示 endpoint 健康、RouteBinding 状态、能力契约版本、聚合用量、缓存时效和最近错误分类。
- 未叠加 C 的 A/B 路径只展示 App 能确认的绑定、进程与事件事实；没有节点回执时不声称掌握 RequestAttempt。叠加 C 后可额外展示代理侧 request/attempt 关联。
- 诊断记录包含稳定错误码、`trace_id`、可用时的节点 `request_id`/`attempt_id`、`reason_code` 与数据来源，不包含秘密或会话正文。
- 【已实现】App 已具备 Sub2API 启停检测与服务摘要；【远期方向】扩展为按 local/cloud/direct 来源分区、并单独显示 live/cached 状态的跨节点统一诊断视图。
- 标识传播固定语义：支持时使用 W3C `traceparent` 传播 trace；数据面使用 `X-Request-ID`、`X-Attempt-ID`、`Idempotency-Key` 与 `X-Request-Query-Token`（旧名 `X-Attempt-Query-Token`）；上游 ID 只在安全响应/诊断字段中返回；`config_transaction_id`、B 链路 session/turn ID 保留在本地控制面关联。A/B 未经 C 时 App 只能掌握任务级 trace 与绑定事实；C 和实际执行节点只记录自己真实观察到的 ID。

### 17.3 日志与支持包边界（【已确认设计】）

- 支持包采用字段白名单，在本地生成并默认不上传；本地日志同样默认不上传。
- 默认排除配置正文、数据库、crash dump、原始日志、提示词/响应正文、工具参数与代码内容。任何被纳入的派生摘要都须先脱敏。
- 无论 debug、崩溃、遥测还是支持包，都不得包含 Authorization、Cookie、API Key、OAuth/PAT、代理密码、带凭据 URL、原始上游错误和完整本地路径。
- 生成后先向用户展示文件清单、大小、字段说明和脱敏结果；只有用户预览并显式授权，才能导出或上传。
- 临时支持包设过期删除；敏感金丝雀测试覆盖日志、异常、崩溃与打包路径，任一秘密命中都使验收失败。

### 17.4 生产部署、硬化与健康模型

【已确认设计】交付资产必须区分开发示例与产品生产基线。当前通用 Compose/systemd 中存在 `latest`、全网监听及仅返回固定 200 的 `/health`；它们是待收敛现状，不能直接成为 local-edge 或云端的发布默认。

| 维度 | desktop-orchestrator | local-edge | cloud-control-data-plane |
| --- | --- | --- | --- |
| 绑定/TLS | App 侧代理只绑真实 loopback | 默认 loopback；LAN 显式选网卡并强制 TLS | 公网入口强制 TLS，内网链路按零信任策略 |
| 身份 | 当前 OS 用户、受限子进程 | 独立服务账户；管理与数据面 Token 分离 | 工作负载身份、租户/用户/管理员分权 |
| 运行硬化 | 最小子进程环境、文件 ACL、禁止提权 | 容器/服务 non-root、read-only rootfs（可写卷例外）、cap-drop、no-new-privileges、资源限额 | 等价容器/主机硬化、网络策略、秘密挂载、资源配额 |
| 配置 | 本地权威 + 安全凭据引用 | 版本化配置、受管密钥、显式持久卷 | 配置/秘密分离、变更审计、租户隔离 |
| 健康 | CLI/代理进程与能力状态分开 | live、ready、degraded、draining 分开 | 实例与 Deployment 聚合状态分开 |
| 发布 | 签名 App/manifest、版本单调 | 固定版本与 digest；禁止生产使用 `latest` | 固定 digest、签名、分批发布与回滚/前滚 |

`/health/live` 只检查事件循环/进程；readiness 至少校验数据库、必要缓存、schema 迁移、持久密钥、端口、证书、关键队列和契约兼容。可选上游或部分渠道故障可进入 `degraded` 并列出受影响 feature；关键账本、鉴权、密钥或数据库不可用时 readiness 必须失败。每个 Deployment 发布 manifest 固定监听、端口、TLS、服务账户、资源、卷、备份、依赖、健康、排空和回滚责任。

进入 `draining` 后，live 在进程仍可响应时保持 200，readiness 立即返回 503，并在认证视图中给出 `state=draining`、`reason_code`、排空开始时间与 `Retry-After`。控制面先摘除新流量并等待负载均衡传播，再由实例以 `DRAINING/result_state=not_dispatched` 拒绝遗漏的新请求；已接收请求继续按普通、流式、`unknown/pending` 与待结算 attempt 的分层截止策略收敛。强制终止必须按可证明事实写成 `cancelled|failed|unknown`，保留账本与查询能力，不能把不确定请求伪装成未执行。停止、升级和实例替换复用该 Deployment 排空协议；贡献撤回复用相同的“停止新调度—处理在途—保留可查询事实”原则，但使用独立账号级状态机和时限配置。

排空验收使用统一记录：请求类别、停止接流时间、负载均衡传播截止、各类在途数量、分层截止时间、强制动作、最终状态、未知请求保留及查询期限。只有实测收敛时间不超过第 21 节冻结的目标，且每个在途请求均落入可查询终态，P0-10 才能通过。

### 17.5 NFR、容量、限流、超时与 SLO/SLI 口径

当前不承诺未经数据验证的 SLA 数值；【已确认设计】先固定测量口径、采样、分母、排除项和错误预算审批，再由产品与运维形成【待决策】目标值。

| SLI | 口径 |
| --- | --- |
| 数据面可用性 | 按 source/protocol/model family 分组的成功或明确终态请求 / 合法请求；鉴权失败单列 |
| 首包与流稳定性 | 连接、首包、空闲、总时长延迟分布；首包前失败与流中断分开 |
| 结算正确性 | 重复结算数、未知状态积压、adjustment 数、账本对账差异 |
| 配置可靠性 | committed 事务 / 发起事务；rolled_back、conflict、recovery_required 分布 |
| 恢复能力 | 云端故障到本地可工作、备份恢复、升级回滚/前滚的时长分布 |
| 安全运营 | 审计丢弃/写失败、证书/密钥到期、秘密金丝雀命中、未处理高风险数 |
| 供应链 | 候选构建测试/扫描/签名/SBOM/provenance 完整率与验证失败数 |

超时按连接、TLS、首包、流空闲、总时长、排空、状态查询收敛分别配置，禁止一个全局超时掩盖阶段原因。限流语义按表面分层：匿名发现按源地址；管理/协同按身份、Deployment 与源地址；数据面按 API Key/本地 Token、Endpoint、并发与成本维度；RequestRecord 状态查询（`attempt_query` 能力）按原主体、键/句柄与源地址。当前 Redis 限流器主要是 IP 固定窗口基础，目标算法、阈值和 fail-open/fail-close 策略为【待决策】；鉴权、账本、贡献、备份恢复等高风险表面默认 fail closed。所有 429 返回稳定错误码与 `Retry-After`。

容量模型至少记录：并发请求/流连接、平均与峰值上下文、账号池可调度深度、数据库/Redis 连接、账本写入与队列积压、日志/备份增长、磁盘水位和 App 同时托管的 CLI 数。压测先确定拐点与安全水位，再冻结目标；超过水位时按优先级拒绝新请求、保持在途请求和账本完整，不以 OOM 或数据丢失作为降级机制。

### 17.6 分类备份、灾难恢复与 Schema 升级

“备份”必须说明边界，以下六类资产互不替代：

| 类型 | 当前事实 | 目标契约 |
| --- | --- | --- |
| App 无秘密备份 | 【已实现】存在本地配置/工作状态备份基础，当前普通备份仍可能残留秘密 | 排除秘密，含组件化 `config_schema_versions`、环境 ID 与 hash，恢复前展示 diff，并生成本地权威快照 |
| CLI 配置事务快照 | 【已实现】有部分 `.bak`/恢复能力 | 仅供短期事务恢复；纳入 journal、ACL、残留清理，不充当用户长期备份 |
| Sub2API 可移植系统配置 | 【已实现】BackupService 只导出设置白名单、错误透传规则和 TLS 指纹模板，明确排除用户、账号凭据、贡献与用量 | 修正文案为系统配置快照；逐字段分类/预览，归档加密、内容 checksum/MAC/签名，S3 endpoint 强制 TLS |
| Deployment 完整灾备 | 当前未由内置 BackupService 提供 | 覆盖数据库、账本、必要队列状态、凭据密文和依赖版本；根密钥独立备份；定义 RPO/RTO、异地副本、保留和演练 |
| 凭据安全导出及审计/账本保留 | 当前没有统一产品契约 | 用户显式授权、独立口令/密钥、最小范围；审计/账本按法务与争议用途独立保留，不能混进普通配置导出 |
| 更新信任与防降级状态 | 当前缺少统一专用恢复契约 | 按 UpdateTrustScope 保护 `installation_id/deployment_id`、epoch 下限、可信根/吊销版本与已消费 rollback serial；另按 `(release_authority_id, release_id)` 保存 release 链的 `max_verified_release_status_serial/verified_release_chain_head_digest`，并按当前 `attestation_id` 保存 `max_verified_attestation_status_serial/verified_attestation_chain_head_digest` 与 `latest_trusted_checkpoint_digest`，拒绝任一链更低 serial 或同 serial 不同 head；不进入普通 App/业务库备份，不含发布私钥；使用独立完整性保护、专用备份或受签 recovery manifest 恢复，并单独演练 |

每类备份冻结资产清单、数据分类、RPO/RTO、加密和完整性、对象存储 TLS、保留/删除、恢复角色、step-up、`key_id/version` 与演练频率。恢复默认进入隔离 staging，验证 `deployment_id`、environment、`app_version`、`server_version`、`contract_version`、`db_schema_version`、组件化 `config_schema_versions`、`key_id/version`、签名/MAC/hash 和数据范围；通过 diff 与责任人确认后再切换。远端来源永远不能静默覆盖本地权威数据。

每次灾备验收统一记录 Deployment、数据类别、目标 RPO/RTO、故障注入点、最后连续已提交点、恢复后最新连续提交点、恢复启动时间、首次 ready 时间、账本/身份/密钥对账完成时间、实测值和二值结论。RPO 按故障点与恢复后最新连续提交点的差值计算；RTO 以“服务 ready”和“权威数据对账完成”两者中较晚者为完成点，避免用进程启动时间冒充业务恢复。

数据库升级采用 expand → migrate/backfill → 双版本验证 → contract。升级前创建并验证与候选构建、`db_schema_version`、组件化 `config_schema_versions` 和 `key_id/version` 绑定的恢复点；先排空/冻结不兼容写入，再执行向后兼容迁移。当前迁移具有 checksum 和常规事务基础，但整体语义偏 forward-only；不可逆迁移失败时必须明确选择前滚修复或从已验证恢复点恢复，禁止仅回退镜像后继续写入新数据库结构。

### 17.7 事件响应与安全运营

| 等级 | 示例 | 首要动作 |
| --- | --- | --- |
| Sev0 | 根密钥/发布签名根泄漏、大范围错误结算或跨租户泄漏 | 立即停止相关数据面/发布、吊销信任、保全证据、最高级别通知 |
| Sev1 | 生产凭据泄漏、备份完整性失败、账本持续异常、远端错误覆盖风险 | 隔离 Deployment/主体、停止贡献/结算、轮换、进入专项 playbook |
| Sev2 | 单一 Deployment 不可用、readiness 持续失败、审计写入异常 | 降级/排空、切换已授权路径、修复并验证 |
| Sev3 | 局部兼容、性能或非敏感诊断缺陷 | 登记、规避、按版本修复 |

响应流程固定为发现与定级 → 遏制/吊销/隔离 → 保全无秘密证据 → 恢复与验证 → 用户/监管通知判断 → 复盘与控制改进。凭据泄漏、结算/幂等异常、供应链、错误恢复覆盖、贡献撤回不完整、证书/密钥过期均需独立 playbook、owner、值班与升级链。P0-10 验收至少演练一次凭据泄漏、一次 Deployment 恢复和一次审计/账本异常，并把时间线、命令、产物和改进项绑定候选构建。

### 17.8 用户可见故障与恢复动作

| 场景 | 必须显示 | 允许动作 |
| --- | --- | --- |
| 云端管理面不可用、已保存 Key 仍可用 | “管理暂不可用”与“数据面可继续”分栏，余额/套餐标过期 | 继续已授权请求、切本地、稍后刷新；不臆造实时账单 |
| local-edge 未启动/半启动/端口占用 | live/readiness、失败依赖、端口和恢复步骤 | 重试启动、查看日志、选择安全端口；不自动改为全网监听 |
| TLS/证书/Deployment 身份变化 | 原/新指纹与风险，阻断原因 | 重新验证并显式信任或取消；禁止静默忽略 |
| 配置事务冲突/崩溃 | 每客户端文件、旧/新 hash、当前会话影响 | 查看 diff、回滚、采用外部版本、人工恢复 |
| RequestRecord `query_status=pending` | 可能已执行、收敛截止时间、已有 attempts 与可能费用 | 禁用重试；继续查询、后台等待或订阅收敛通知 |
| `result_state=unknown` 且不支持状态查询 | 无法确认是否执行、可能产生费用、客户端能力限制 | 保持失败关闭；等待上游/人工核验，或由用户明确确认潜在重复执行后发起新的逻辑请求 |
| `not_found` 且无 `safe_to_retry` | 不能证明未执行、重复执行与费用风险 | 保持失败关闭；用户确认后重新发起逻辑请求，由数据面创建新的 RequestRecord/RequestAttempt，并通过 RequestLink/`trace_id` 保留关联 |
| `aborted` | 请求未形成正常最终结果，但可能已经产生可计费用量 | 展示全部 attempts、`billable_usage_total` 与对账入口；不得显示为“未收费” |
| 能力/版本不兼容 | 缺失 feature、最低版本、可用降级路径 | 升级、手工配置、降级到明确支持的终端/模式 |

全局降级横幅同时列出“仍可使用”和“暂不可用”；消息状态卡、连接恢复面板、诊断页与配置恢复页使用同一 canonical error 和 reason code。

### 17.9 费用、用量与新鲜度视觉契约

每个金额或 Token 指标至少携带：`source=local|cloud|direct`、可空 `deployment_id`、权威级别（权威/观察/估算/可能费用）、`provider/billing_scope`、主体、币种、`pricing_version`、`snapshot_at`、`cache_state=live|cached` 与 TTL。跨来源默认并列；只有币种、汇率时间、价格版本和口径可比较时才提供明确标注的估算总计。

- direct 观察值不得标成平台实际费用；缓存快照不得显示为实时健康。
- 失败或流中断 attempt 单独展示已测量/可能费用；后续 adjustment 追加展示，不覆盖原费用事实。
- 回退确认框同时展示原 attempt 可能费用、新 attempt 预计费用、模型/协议/隐私变化和结算责任。
- 同一页面中不可比较的数据分区展示，避免用一个“当前数据”或“实际费用”标签掩盖来源、主体和新鲜度。
- 缺失、过期或主体不匹配的价格不得按 0 或实时价格参与决策。`cost_limit` 或自动回退依赖价格且无法证明满足限制时返回 `user_confirmation_required`；界面展示最后快照、时间、TTL、估算属性和不可比较项。direct 无可用官方适配器时显示“官方用量不可取得”，不得显示 0。

## 18. 本地 App 信息架构及后台能力复用

### 18.1 建议一级模块与现有能力映射

| 一级模块 | 职责 | 现有能力映射 | 状态 |
| --- | --- | --- | --- |
| 总览/工作台 | 状态总览、当前绑定与实际来源、快速切换 | 启动恢复、退出还原、托盘 | 【已实现】基础工作台；【远期方向】统一总览面板 |
| 连接与路由 | Source/ConnectionProfile、RouteBinding、目标端点与四层策略 | 本机/局域网/云端来源、按客户端选择 | 【已实现】来源与客户端选择；【已确认设计】RouteBinding 与分层路由 |
| 项目与会话 | 项目工作环境、会话索引/继续/删除、终端 | 项目中心、官方会话管理、ConPTY 终端 | 【已实现】 |
| 账号与贡献 | 账号管理、共享池、贡献入口与撤回进度 | App 已集成账号查询/编辑/测试/删除；贡献创建与房间管理当前由 Sub2API Web/API 提供 | 【已实现】账号管理；【已确认设计】App 一键贡献与房间管理 |
| 中转服务 | 本地中转安装/启停/健康/后台 | 本地 Sub2API 启停检测、打开后台 | 【已实现】 |
| 扩展中心 | MCP/Prompt/Skills 统一管理与同步 | 扩展统一管理并同步官方客户端 | 【已实现】 |
| 用量与诊断 | local/cloud/direct 来源分区、live/cached 状态、成本可见与支持包 | 云端账单/服务摘要 + 本地隐私统计 | 【已实现】现有双视图；【远期方向】统一诊断 |
| 设置与安全 | 凭据保护、安全默认、更新、审计 | 安全更新下载、单实例、开机启动 | 【已实现】现有基础；【已确认设计】凭据迁移与审计 |

### 18.2 后台能力复用的三种层级与选择原则

1. 云端后台页面直接打开：适合运营治理、复杂管理界面，避免重复造轮子。
2. API 复用：App 通过版本化契约调用后台 API，适合需嵌入 App 工作流的能力。
3. App 原生管理：适合高频、需与本地状态深度联动、需在不依赖本产品云端控制面时继续使用的能力。

选择原则：高频、强联动、本地自主的能力优先原生；低频、复杂治理优先直接打开后台；介于两者之间且需嵌入流程的走 API 复用。所有 API 复用必须经能力发现与适配层，保持接口解耦（【已确认设计】），不硬编码耦合具体后台实现。

### 18.3 与 CC Switch 关系定位

CC Switch 定义为“客户端配置管理参考”（部分扩展管理、项目 Profile、用量筛选、会话导入、托盘和备份行为参考或翻译自它）；本产品定义为“云端服务、边缘中转和本地 AI 工作台的一体化控制面”，差异已扩展到云端/局域网/本机节点控制、本地中转生命周期、Sub2API 用户与账号治理、图形聊天与终端、项目工作环境、跨协议路由、云端账单与本地隐私统计。

### 18.4 全局上下文与导航规则

【已确认设计】任何关键页面同时区分三类上下文，禁止用一个“当前来源”覆盖全部语义：

| 上下文 | 全局显示 | 典型页面 | 切换不变量 |
| --- | --- | --- | --- |
| 工作上下文 | Project、Session、受管客户端 | 工作台、项目、聊天、终端 | 切项目不自动改变治理目标；会话保留其原绑定 revision |
| 执行上下文 | 期望策略、已应用 RouteBinding、配置事务、最近可确认实际来源 | 连接与路由、聊天、诊断、用量 | 切绑定不改写在途/历史 attempt，也不切换账号管理目标 |
| 治理上下文 | Deployment、Endpoint、平台/本地身份、权限、数据新鲜度 | 中转服务、账号、贡献、设置、安全 | 删除、测试、贡献、停止、恢复均固定显示目标 Deployment 与身份 |

全局标题区用文字+图标分别显示三类上下文；颜色只作辅助。页面文案使用“期望路由”“已写入配置”“最近可确认实际来源”“缓存快照来源”“正在管理的 Deployment”等精确标签。任一跨上下文动作在执行前展示影响预览，取消后不留下半切换状态。

### 18.5 首次使用、升级迁移与激活闭环

当前 App 直接进入主窗口，尚未确认独立首次使用路由；以下为【已确认设计】向导。用户从四条并列入口任选其一，不强制云端登录或使用本产品中转：

| 入口 | 最小步骤 | 可跳过项 |
| --- | --- | --- |
| 登录云端并获取 Key | 云端地址/TLS → 登录 → 签发最小权限 Key → 选择客户端 → 配置预览 → 首次请求 | local-edge 安装 |
| 仅录入已有中转数据面 API Key | Endpoint/证书 → Key 安全落点 → 受限能力发现 → 一次性 nonce challenge 与受签 EndpointDescriptor → 传输/Deployment/role/entrypoint 信任绑定 → 选择客户端 → 配置预览 → 首次请求 | 平台登录与余额、Key 管理、贡献治理等管理面 |
| 本地自主 | 安装/发现 local-edge → readiness → 本地管理员授权与数据面 Token → 导入测试账号 → 选择客户端 → 首次请求 | 本产品云端全部能力 |
| 直连上游（direct） | 选择或创建 `owner_kind=upstream_provider` 的 TargetEndpoint → 确认使用官方 CLI 既有身份或录入上游提供商凭据 → 协议/模型预检 → 选择客户端 → 配置预览 → 首次请求 | 平台登录、云端中转和本地中转 |

“已有中转数据面 API Key”入口不接受上游 provider credential；未登录平台时，余额、Key 管理、贡献治理等管理能力显示“需登录”，但受限 capabilities、最小 readiness 与数据面请求仍可独立完成。direct 入口必须显示“未使用本产品中转、无本产品权威账本、最终账单以上游为准”。

每条路径都执行 CLI 检测、精确版本/支持等级提示、受管客户端选择、RouteBinding 预览、秘密投影说明、AtomicityGroup/ConfigTransaction 和首次请求；direct 以协议/模型与凭据预检替代 Deployment readiness。只安装一个客户端的用户可以完成激活，其他客户端标记“未安装/未选择”，不能成为阻断项。向导关闭、App 崩溃或网络失败后从最后一个已提交步骤恢复，并提供“继续上次步骤、从头开始、导出诊断”；重置向导不删除已经安全保存的 Key，除非用户另行确认。用户确认前不修改官方客户端配置。

首次端到端请求可能产生真实费用。执行前必须展示 TargetEndpoint、目标模型、费用权威/估算来源、可能成本与测试内容，并使用不含用户正文、代码或工具参数的最小合成请求。用户可以跳过；此时 RouteBinding 仍可因 AtomicityGroup 已提交而处于 `active`，但首次可用闭环状态只能显示“配置已应用、请求尚未验证”，不得显示“验证通过”。首次请求失败只影响验证状态，不自动回滚已经成功提交的配置原子组。

`ValidationEvidence` 是绑定 revision 的追加式验证证据，与 RouteBinding 生命周期、RequestRecord 和来源确认分离。最小字段包括 `evidence_id`、`binding_id`、`binding_revision`、`interaction_mode/proxy_mode`、`evidence_kind=supervised_cli|managed_cli_event|proxy_observation|node_receipt|upstream_billing_receipt|user_attested`、客户端及精确版本、`client_executable_digest`、`adapter_contract_version`、`credential_version_id`、`config_projection_hash`、`validation_scope_hash`、起止时间、可空 `valid_until`、`validation_outcome=not_run|passed|failed|unknown`、可空退出码/脱敏响应标记、可空 RequestRecord 引用，以及 `source_confirmation=unconfirmed|client_observed|proxy_observed|node_confirmed|upstream_billing_confirmed|not_applicable`；不得保存秘密、测试提示正文或模型响应正文。`credential_version_id` 只表达安全存储中的秘密世代，不包含秘密值。`user_attested` 只属于 `evidence_kind`，必须保持 `validation_outcome=not_run`、`source_confirmation=unconfirmed`；若随后取得机器证据，应追加新的证据对象，不能原地提升用户声明。Endpoint readiness、TLS 探测或配置文件可解析只属于预检，不等于 CLI 端到端验证成功。

`validation_scope_hash` 使用版本化 canonical JSON 计算，至少覆盖 `binding_id/revision`、interaction/proxy mode、endpoint id/revision、trust binding version、配置投影 hash、客户端精确版本与 executable digest、适配器/契约版本、credential version、协议及验证模型范围。`validation_outcome` 是不可变历史结果；当前是否仍可用于闭环由派生的 `current_validation_state=valid|stale|expired|revoked|unknown` 表达。passed evidence 只有在引用当前 active 的同一 binding revision、重新计算的 scope hash 完全一致、未超过 `valid_until` 且不存在更晚失效事件时才为 valid。绑定 revision、配置投影、CLI 可执行文件或精确版本、适配器/能力契约、credential version、Endpoint/trust 身份发生变化，或 `config_integrity_state` 进入 conflict 时，系统必须追加 `ValidationEvidenceInvalidation(evidence_id, reason_code, observed_at, superseding_binding_revision?)`；原 evidence 保留 passed 历史，不原地改写。瞬时 readiness/健康波动只影响 runtime 状态，不自动抹除历史验证。

B 使用版本化结构化 CLI 事件形成客户端证据；C 可使用代理真实观察，并在能精确关联 RequestRecord 时提升为节点确认。未经 C 的 A 路径只有在 App 显式启动一次性受监督的官方 CLI 验证进程，复用正式 A 路径的可执行文件、精确版本、已提交配置、参数与环境投影，并捕获兼容矩阵声明的结构化成功信号时，才可自动写 `validation_outcome=passed`；这次诊断不代理模型 HTTP，链路仍记为 A。单独退出码只有在该客户端版本契约证明其代表请求成功时才可作为通过依据，否则记为 `unknown`。用户在 App 外自行运行、App 未取得可验证信号时只能保持 `not_run` 或记录信息性 `user_attested`，不得冒充机器验证通过。

升级用户先进入迁移分支：识别 `legacy-cloud-ambiguous`、旧明文 profiles/`.bak`/环境/官方配置残留和旧绑定；把旧 URL 解析为规范化 `(origin, base_path, protocol, auth_method, trust_binding)` 后拆分 EndpointCandidate，逐项选择 cloud/direct、迁移到安全存储、预览 diff 并建立回滚点。首次可用闭环完成条件为：至少一个已选择客户端的 RouteBinding 已进入 active，且该同一 `binding_id + revision` 至少存在一条 `validation_outcome=passed`、`current_validation_state=valid` 的 ValidationEvidence；旧 revision、scope hash 不匹配、已失效、已过期或 `user_attested` 证据均不得满足闭环。只完成 active 时属于“配置已应用、请求尚未验证”。实际来源采用第三条独立轴：A/B 的客户端证据最多先到 `client_observed`，C 的代理观察可到 `proxy_observed`，只有通过稳定查询令牌、幂等键或节点签名回执与 RequestRecord 精确关联后才到 `node_confirmed`，不得按时间、模型名或绑定猜测来源。direct 未经 C 时预期停留在 `client_observed` 且本产品节点确认 `not_applicable`，不得显示为异常。

#### 18.5.1 用户语言、实现状态与统一请求时序

本小节是目标产品编排与验收基线，不是当前构建已经逐步验证的操作手册。用户界面先使用自然语言表达意图，再由 App 映射到 `release_profile_id`、`route_target`、Endpoint、凭据族和 A/B/C 链路；高级详情才显示内部术语。

| 用户说法/入口 | 系统编排映射 | 首次信任与凭据 | 请求/账本权威 | 当前状态 |
| --- | --- | --- | --- | --- |
| “登录云端，直接开始使用” | `route_target=cloud`；默认 A 或 B，C 仅显式启用 | cloud EnrollmentBundle/受签 descriptor；Deployment 用户会话用于管理，云端数据面 API Key 用于请求 | cloud Deployment | 登录、Key 与配置已有基础；统一 nonce 信任、事务和验证闭环为【已确认设计】 |
| “我已经有一个中转 Key” | 按 Key 所属 Endpoint 选择 local/cloud；不推断为官方上游 | 只发送最小数据面凭据完成主体自省与 nonce descriptor；不获得余额/贡献治理权限 | Key 所属 Deployment | Endpoint 选择已有基础；受限自省和完整信任闭环为【已确认设计】 |
| “数据尽量留在本机” | `route_target=local`，Windows local-edge 候选；是否仍需互联网取决于上游 | 本机 EnrollmentBundle、local admin 授权与独立数据面 Token | local-edge Deployment | 本地启停/后台/账号管理已有基础；生产安装、readiness、TLS/Token 与恢复证据为【已确认设计】 |
| “只管理官方 CLI，直连上游” | `user_mode=none`（无中转时）+ `route_target=direct`；默认 A，C 可选 | 官方 CLI 既有身份或上游提供商凭据；不得接受中转数据面 Key 冒充上游凭据 | 官方上游；App 只持观察值 | 配置驱动已有基础；direct 一等 Endpoint、明确账本展示和迁移为【已确认设计】 |

所有入口共用以下阶段与失败语义：

| 阶段 | App/系统动作 | 当前落地程度 | 失败或跳过后的唯一允许状态 |
| --- | --- | --- | --- |
| 1. 选择入口 | 记录用户意图、目标客户端和目标 profile，不改配置 | 独立四路向导尚未确认，为【已确认设计】 | `draft`；用户可退出且无副作用 |
| 2. 建立信任 | 规范化 Endpoint，验证 TLS/anchor、主体、nonce descriptor 与 role | 现有 Endpoint 选择可复用；统一信任协议为【已确认设计】 | `untrusted_candidate` / `re_enrollment_required`；禁止发送更多秘密 |
| 3. 准备凭据 | 区分 Deployment 会话、数据面 Key、本地管理员授权、上游凭据并写入安全源 | 部分安全存储已实现，完整迁移属于 P0-2/P0-3a | `credential_unready`；不得写入普通 profile/备份作为唯一源 |
| 4. 预检与应用 | 能力/版本/readiness 预检，展示 diff，提交 AtomicityGroup/ConfigTransaction | 现有配置写入已实现；统一事务与恢复为 P0-8 目标 | `rejected|conflict|recovery_required`；不得伪装为已生效 |
| 5. 首个请求 | 按 A/B/A+C/B+C 和 route target 发出最小合成请求 | A/B 与特定 Claude C 桥已有基础；通用 C 和统一 ID 传播未实现 | 可跳过并显示“配置已应用、请求尚未验证”；unknown 先查询，不自动重发 |
| 6. 验证与来源确认 | 追加 ValidationEvidence；分别派生验证状态与来源确认级别 | 为【已确认设计】 | `failed|unknown|user_attested` 均不能冒充机器 `passed` |
| 7. 展示与交接 | 展示 RouteBinding、最近可确认来源、费用权威、下一动作和诊断 ID | 分散展示已有基础；统一闭环与指标事件为 WP-07 | 缺事实时显示未知/缓存/不适用，不按绑定推断实际执行 |

```mermaid
sequenceDiagram
    actor U as 用户
    participant APP as desktop-orchestrator
    participant CLI as 官方 CLI
    participant PROXY as App 侧 C 代理
    participant NODE as local/cloud Deployment
    participant UP as 官方上游

    U->>APP: 选择云端 / 已有中转 Key / 本地自主 / direct
    APP->>APP: 信任、凭据、能力预检、配置事务
    alt A 配置驱动
        APP-->>CLI: 写入受控配置或启动参数
    else B 托管交互
        APP->>CLI: 启动结构化子进程并管理交互
    end
    alt 未叠加 C
        alt route_target=local/cloud
            CLI->>NODE: 模型请求
            NODE->>UP: 上游请求
            UP-->>NODE: 响应/用量事实
            NODE-->>CLI: 响应 + 可查询 RequestRecord
        else route_target=direct
            CLI->>UP: 直连请求
            UP-->>CLI: 响应；官方账单为权威
        end
    else A+C 或 B+C
        CLI->>PROXY: 模型请求
        alt route_target=local/cloud
            PROXY->>NODE: 转发并传播关联 ID
            NODE->>UP: 上游请求
            UP-->>NODE: 响应/用量事实
            NODE-->>PROXY: 响应 + 节点事实
        else route_target=direct
            PROXY->>UP: 直连转发
            UP-->>PROXY: 响应；官方账单为权威
        end
        PROXY-->>CLI: 响应 + proxy observation
    end
    APP-->>NODE: 仅在有节点时主动查询状态/聚合快照
    APP->>APP: 追加 ValidationEvidence，分轴展示绑定/验证/来源
    APP-->>U: 首次成功、未验证、失败或需恢复
```

这张图固定了 Reader Testing 中最易混淆的边界：A/B 未叠加 C 时 App 不在模型 HTTP 数据面；B 只表示 App 托管 CLI 交互；C 代理才观察逐请求流量；local/cloud 的节点账本与 direct 的上游账单各自保持权威。

### 18.6 关键交互状态机

#### 18.6.1 连接应用与切换

每个客户端独立呈现：`未安装`、`版本不支持`、`受限支持`、`保持原状`、`草稿待应用`、`预检中`、`写入中`、`已应用待重连`、`已应用待重启`、`已应用待重发`、`部分失败`、`外部修改冲突`、`可回滚`、`恢复处理中`、`已生效`。

切换前展示变更文件/进程、当前会话与新任务分别受何影响、A/B/A+C/B+C 链路、凭据投影、预计费用来源和必要确认。普通界面使用“配置驱动”“App 托管会话”“本地代理”名称，A/B/C 代码只在高级详情显示。用户可选择立即应用、会话结束后应用或仅新进程使用；路由页展示“单次任务覆盖 ← 项目策略 ← 客户端策略 ← 全局默认”的继承链，并提供“恢复继承”。【待决策】P0-9 需冻结每个客户端/版本的默认组合、用户可切换组合和不支持原因；默认暂行口径为外部官方 CLI 使用 A、App 内托管会话使用 B，C 只在协议转换、稳定代理入口或用户显式启用时叠加。

一个客户端成功而另一个失败时整体显示“部分成功”，并按原子组提供精确动作：committed 组保持生效或创建新的恢复事务，rolled_back/rejected/cancelled 组从新 draft revision 重试，conflict 组查看 diff 并选择外部版本、App 版本或人工合并，recovery_required 组进入恢复工作台；在途请求保持旧 revision。

#### 18.6.2 多客户端能力与会话

兼容矩阵驱动按钮和降级：客户端状态分为已安装且支持、受限支持、版本超范围、缺失、配置不可读。缺少 Grok/Gemini 不阻断 Codex/Claude；审批、取消、恢复或工具调用不支持时禁用相应按钮，显示原因与“高级终端/手工配置”等替代路径。

会话启动前显示客户端、精确版本、`requested_model`、预期 `effective_model`、RouteBinding revision、链路组合与预计费用来源。local/cloud 请求在取得节点回执时展示 RequestRecord 聚合状态和 `attempts[]`；未经 C 的 direct 请求不存在本产品节点 RequestRecord，展示“上游响应由客户端观察、本产品账本不适用、官方账单为最终权威”；direct 经 C 时只展示代理观察事实，并明确其不替代上游官方费用。不得把预期不存在节点回执的 direct 路径显示为异常。恢复旧会话时并列显示原连接、当前连接和“继续原绑定/按当前绑定分叉新会话”的选择，禁止静默换模型或来源。

#### 18.6.3 身份、权限与破坏性操作

界面分别标识平台登录、云端 API Key、本地管理员授权、本地数据面 Token，按服务端 scope 与 feature 状态启用操作。无权限时在操作前显示所需身份和获取路径；服务端仍执行最终授权。

删除账号/会话、批量治理、停止中转、撤回贡献、恢复备份、轮换密钥、清理审计等操作展示：目标 Deployment、当前身份、对象数量、在途任务、费用影响、可恢复性、step-up、审计 ID 和逐项结果。高影响操作可按第 16、21 节进入双人审批；路由切换不得改变治理操作目标。

#### 18.6.4 后台操作中心

安装、启动、配置事务、迁移、贡献验证/撤回、备份恢复、升级和支持包生成进入统一活动中心，记录 `operation_id`、目标、阶段、进度、可取消性、幂等/重试边界和结果。用户离开页面后任务按声明继续或暂停；重新进入可恢复观察。取消只停止允许取消的后续步骤，已经提交的事务、attempt 或删除阶段必须显示真实结果。

### 18.7 统一页面状态语法与无障碍基线

| 状态 | 必须呈现 | 主要动作 |
| --- | --- | --- |
| 加载中 | 对象、阶段、是否可取消；骨架或进度 | 取消/后台运行 |
| 空数据 | 空的原因和首个价值动作 | 添加连接、启动服务、登录或导入 |
| 实时正常 | 来源、更新时间、权威级别 | 刷新/继续工作 |
| 缓存陈旧 | `snapshot_at`、TTL、原 source、非实时说明 | 重试刷新/继续查看缓存 |
| 降级可用 | 仍可用与不可用能力清单、reason code | 切换已授权路径/诊断 |
| 权限不足 | 当前身份、所需 scope/step-up | 登录/授权/返回 |
| 版本不兼容 | 当前/最低/支持版本与降级路径 | 升级/手工配置 |
| 部分成功 | 每个对象的成功/失败/未执行 | 逐项重试/回滚 |
| 操作进行中 | operation ID、阶段、不可逆点 | 查看活动中心 |
| 需恢复 | journal/恢复点、风险与 diff | 自动恢复/人工处理 |
| 阻断错误 | canonical code、trace ID、安全详情 | 安全重试/查询/联系支持 |

状态不能只靠颜色表达；至少同时使用文字、图标与可读标签。键盘导航、焦点顺序、缩放、对比度、读屏名称和动态状态播报进入 UI 验收。P0-9 至少冻结 Windows 高对比度、200% 缩放、纯键盘操作、Narrator 与 NVDA 基线；动态状态使用可控 live region，焦点在对话框关闭、异步失败和恢复动作后回到可预测位置，支持减少动画。适用界面以 WCAG 2.2 AA 为目标，无法满足项登记例外与替代方案。单个 `StatusNotice`/Toast 只作摘要，复杂操作必须有结构化状态卡和可追溯详情。

## 19. 交付路线图（Epic 化）

### 19.1 P0 / 近期基线

| Epic | 主责 | 涉及模块 | 目标与主要产物 | 依赖 | Definition of Done |
| --- | --- | --- | --- | --- | --- |
| P0-1 Key 级用量字段白名单 | 网关 + 安全 | GatewayHandler、用量 DTO、契约测试 | 默认拒绝的 Key DTO、授权矩阵、敏感响应 no-store、最小快照缓存契约、黄金 JSON | 无 | 任意数据面 Key 均不可获得 `account_cost`、钱包/订阅总量或账号、渠道、代理、管理员内部字段；只有最小聚合快照可按 TTL/版本缓存 |
| P0-2 桌面端凭据迁移 | 桌面端 + 安全 | profiles、`.bak`、凭据存储、SwitchService、官方配置/环境/备份 | 可重入迁移器、安全源、事务化最小投影、ACL、备份排除与残留扫描 | 与 P0-8 联合冻结配置事务/恢复协议 | profiles 与 App 自建备份不含秘密；官方 CLI 必要投影具备最小字段、权限、清单和切换/退出清理；失败可回滚且不丢凭据 |
| P0-3 中转凭据与贡献托管加固（P0-3a/P0-3b） | 网关 + 安全 + 数据 | Account JSONB、贡献 Handler、公开 DTO、密钥管理、删除流程 | P0-3a：上游/中转凭据信封加密、旧数据迁移、轮换与最小解密；P0-3b：贡献服务端关闭闸门、专用/转移模式、公开目录白名单、撤回墓碑与删除作业 | P0-3a 与 P0-10 联合冻结根密钥；P0-3b 等待贡献凭据形态、删除与备份 SLA 决策 | P0-3a 在所有含中转的 profile 必须通过；P0-3b=R 时正向托管与撤回全链通过，P0-3b=C 时 `contribution_credentials_enabled=false` 且服务端/客户端负向证据通过；撤回后不可再调度，公开 DTO 无内部字段，主存储与备份删除范围可核验 |
| P0-4 能力契约 v1 | 架构 + 桌面端 + 网关 | capabilities/bootstrap、EndpointDescriptor、TargetEndpoint、RouteBinding、适配器、OpenAPI | 契约 v1、EndpointCandidate/nonce challenge/受签 EndpointDescriptor、全局 UUID 与不可变 revision、TargetEndpointHead/RouteBindingHead CAS、Deployment role、规范 scope、稳定错误码、版本协商、`user_mode=none`/direct 边界与缓存规则 | direct 支持边界、兼容与弃用窗口决策 | 支持/不支持/版本不兼容三类路径均有契约测试；仅持数据面 Key/Token 可安全取得最小自省、readiness 与 nonce-bound EndpointDescriptor；错/复用 nonce、过期响应、越权入口、跨主体/跨 origin 重放、错 Deployment/role/transport 均不能生成可 active 的 TargetEndpoint；Endpoint/Binding Head 并发 CAS 各只成功一个且推进 EndpointHead 不自动换绑；local/cloud 与认证 `server_role` 一致；同 scope 只一个 current；RouteBinding 不冒充 RequestRecord/RequestAttempt；direct-only 唯一表示为 `user_mode=none + user_mode_reason=no_enabled_relay + route_target=direct`；`legacy-cloud-ambiguous` 在显式迁移前不触发费用/隐私/回退判断 |
| P0-5 本地网络与 SSRF 安全 | 网关部署 + 桌面端 + 安全 | 监听、登录/恢复、TLS、Token、防火墙、代理/探测 | 仅真实 loopback 可用 HTTP、LAN/公网 TLS、独立 Token、SSRF 防护、不安全会话清理 | LAN TLS 信任模型、TLS 与证书工程基线冻结 | 新安装仅绑定 loopback；数据面、协同、管理、贡献、流式和重定向后的 HTTP/TLS 矩阵全部通过；贡献始终强制 TLS；LAN 未显式授权不可访问；SSRF 矩阵通过 |
| P0-6 结算幂等与补偿 | 网关 + 数据 | usage_billing、repository、中间件、RequestRecord、RequestAttempt、RequestLink | `deployment_id + ledger_domain_id + idempotency_key_hash` 唯一约束、dispatch lease/fence/durable intent、双轴真值表、requests/status、旧新账本迁移/对账、409 指纹冲突、追加式补偿事件 | P0-1、P0-4、流式中断计费口径、幂等与状态查询工程规格冻结 | 同一 Deployment + `ledger_domain_id` + `idempotency_key` 定位同一 RequestRecord；只有当前 lease/fence owner 能从 registered 写 intent 并发网；intent 前可安全接管，intent 后崩溃只能查询/收敛且旧 fence 写入被拒绝；同一 attempt 两轴只走合法边；查询返回 `attempts[]` 与总可计费用量；无用量进入 void；跨域 RequestLink 不参与授权或结算；旧 writer 清零并完成对账；不承诺 global exactly-once |
| P0-7 测试与诊断隔离 | 测试 + 桌面端 + 网关 | composition root、AppDataPaths、日志、崩溃/支持包 | 完整路径注入、随机临时根、真实路径守卫、字段白名单与统一脱敏 | 无 | 被测组件不自行创建默认用户路径；命中真实目录立即失败；支持包预览、默认不上传且敏感金丝雀测试无泄漏 |
| P0-8 CLI 配置事务与崩溃恢复 | 桌面端 | SwitchService、RouteBinding、协调器、快照/恢复 | ConfigApplyOperation、AtomicityGroup/ConfigTransaction、AtomicityGroupBinding/ConfigTransactionBinding、耐久资源锁、journal/多 Head 同事务 commit point、绑定持久化、启动恢复、runtime activation、差异记录 | P0-4；与 P0-2 联合冻结凭据投影/回滚边界；基础协调器设计可并行，Head/route_target Schema 与候选验收依赖 P0-4 | 原子组只对可恢复适配器承诺逻辑全有或全无，不虚构跨资源瞬时 ACID；commit point 前回滚、后幂等前滚；同组多个事务与多个 binding revision 不部分提交；全部 Head 条件更新数必须等于应切换的不同 scope 数，任一 Head CAS 冲突使零个 Head 推进并先恢复外部资源；同键同指纹重入、异指纹 409；取消、预检拒绝、CAS 冲突与进程激活失败均有闭合状态；多客户端可解释 partial_success；完成 legacy 显式 route_target 迁移且不残留半写配置 |
| P0-9 跨协议与四客户端兼容基线 | 桌面端 + 协议桥 + 测试 | Codex/Claude/Gemini/Grok 引擎、CLI、桥与核心 UX | 版本化兼容矩阵、真实 CLI 契约用例、默认链路声明和无障碍报告 | P0-4、P0-7、P0-8；默认链路/可编辑范围与无障碍基线冻结 | 客户端版本、协议、模型族、A/B/C 组合、流式、工具、审批、取消、超时、重试、断连、回退、幂等键、状态查询和无障碍各轴均有明确结果；P0 核心与首发声明能力必须通过，Grok 非首发缺口以支持声明明确并进入 P1-8 |
| P0-10 生产运维、灾备、密钥与事件响应基线 | 运维 + 安全 + 数据 | profile 交付端发布 manifest、N 端不发布证明、live/readiness/draining、密钥目录、审计链、分类备份/完整灾备、事件响应 | 三类交付形态的生产硬化矩阵、用途独立密钥及轮换、隔离恢复演练、高风险审计 fail-closed/break-glass 与外部不可变锚、Sev0–Sev3 playbook | P0-2、P0-3a、P0-3b、P0-4、P0-5、P0-6、P0-7、P0-8；基础设计可并行，候选验收依赖 P0-11a；RPO/RTO、排空时限、密钥托管与审计保留决策必须冻结 | profile 声明的交付端均有生产 manifest；标为 N 的端提交“不发布该端”的构建清单、路由矩阵和用户文案证明，不为 N 端虚构生产 manifest；关键依赖异常使 readiness 失败，draining 立即摘流并可核验在途收敛；用途隔离和轮换通过；完整灾备在隔离环境恢复通过；普通高风险变更审计失败时不提交，Sev0/Sev1 安全停止可走受限 break-glass 并补锚对账；恢复、审计/账本及事件演练均绑定候选构建 |
| P0-11 可信发布与升级供应链基线 | 发布工程 + 架构 + 安全 + 运维 | CI/CD、App 更新器、安装包/镜像、release manifest、迁移包与适配器包 | 固定版本与 digest、版本向量、签名、SBOM、依赖/许可证扫描、provenance、UpdateTrustScope/security_epoch、bootstrap/recovery、授权回滚与吊销、不可变证据 manifest、hash-chained 状态声明、nonce-bound checkpoint、独立 `p0_11_detached_attestation`、expand-migrate-contract | P0-4、P0-9；P0-11a 候选制品、验证器、签名与状态/checkpoint 流水线先行；P0-11b 在 P0-1、P0-2、P0-3a、P0-3b、P0-4～P0-10 与 P0-11a 的同一 `release_id` manifest 已签名且初始 current 状态已发布后签署独立 attestation；兼容/弃用窗口与签名根托管决策 | 每个制品证明绑定同一 digest；生产拒绝浮动 tag、未签名、篡改、普通降级和吊销产物；首次 bootstrap、trust-state 丢失与受签恢复可区分；桌面/Deployment rollback 绑定正确目标且不越过 epoch；manifest 不反向引用 attestation；截断状态后缀、旧 checkpoint 重放、错 nonce、链断裂和权威不可达均保持 No-Go；候选变化由 verifier 与受签状态立即恢复 No-Go；混合版本、迁移及回滚/前滚演练通过 |

执行顺序：在 G0 前，P0-1、P0-2、P0-3a、P0-5、P0-7、P0-8、P0-10 的基础设计，以及 P0-11a 的构建、签名、验证器与状态/checkpoint 流水线可以并行开发和预验证，但不得据此生成发布候选、冻结 candidate fingerprint 或宣称任何 profile 就绪。G0 先冻结 `release_profile_id`、`applicability_revision`、默认 CTA、拓扑、适用性和证据计划；G1～G3 再完成所选 profile 的实现级退出条件。进入 G4 后，P0-11a 才按已冻结输入产出绑定版本向量与 digest 的可验证候选构建并计算 candidate fingerprint；P0-10 随后以该候选完成部署、排空、轮换、恢复和事件演练，全部最终证据进入不可变 manifest、发布初始 current statement 并取得 `attestation_state=none` 的初始 checkpoint 后，G4 才可关闭。G5 再执行 detached attestation、`issued → active`、activation 后 checkpoint 与四方 Go。

依赖关系保持不变：P0-2/P0-8 必须联合冻结凭据投影、配置事务与恢复边界；P0-8 的 Head/route_target Schema 和候选验收等待 P0-4 契约冻结。P0-3a 不等待贡献产品决策，先完成所有含中转 profile 的上游凭据保护；P0-3b 的正向开放等待贡献凭据形态及删除/备份 SLA，Contained profile 则先完成关闭闸门与负向证据。P0-4 等待 direct 支持边界及兼容/弃用窗口；P0-5 等待 LAN 信任模型及 TLS/证书工程基线；P0-6 在 P0-1/P0-4 字段稳定后，等待流式中断计费口径及幂等/状态查询工程规格；P0-9 使用 P0-7 的隔离基础，并在 P0-4/P0-8 的契约和配置提交语义冻结后形成首发支持矩阵。十一项 P0 均为发布阻断项，灰度、受信回滚和必要的前滚方案写入各 Epic 的实现设计。

`P0-11a` 与 `P0-11b` 是 P0-11 内部的前后置闸门，不增加 Epic 数量；P0 总数仍为十一项。

### 19.2 P1 / 协同增强

| Epic | 主责 | 主要产物 | 依赖 | Definition of Done |
| --- | --- | --- | --- | --- |
| P1-1 项目级策略 | 桌面端 + 产品 | 四层策略引擎、项目/任务级 RouteBinding 增强与冲突预览 | P0-4、P0-8 | 在 P0 基础持久化之上实现单次任务、项目、客户端、全局四层合并；结果可解释、可回滚，绑定与 RequestRecord/RequestAttempt 明确区分 |
| P1-2 健康探测与受控回退 | 桌面端 + 网关 | 健康摘要、链路能力判定、用户确认与审计 | P0-4、P0-6、P0-8、P0-9 | 未叠加 C 的 A/B 路径仅协调重连/重发；未启用 C 类代理或 CLI 原生能力时不做逐请求跨端点自动回退 |
| P1-3 模型能力映射 | 协议桥 + 桌面端 | requested/effective 映射表与兼容等级 | P0-4、P0-9 | 映射、工具限制与上下文族差异在执行前和结果中可见 |
| P1-4 预算策略 | 产品 + 网关 + 桌面端 | cost_limit、提醒、阻断与回退费用提示 | P0-1、P0-4、P0-6 | 超预算按用户策略提醒或阻断，失败尝试的可能费用可见 |
| P1-5 统一诊断 | 桌面端 + 运维 | 跨节点健康、RouteBinding/RequestRecord/RequestAttempt 关联、来源标记和一键支持包 | P0-4、P0-6、P0-7 | 诊断不含秘密或正文，能区分 local/cloud/direct，并独立标记 live/cached；支持包本地预览后才可导出 |
| P1-6 贡献产品治理 | 产品 + 网关 + 安全 | App 一键贡献、条款确认、专用凭据引导、收益/预算解释、房间管理与撤回进度 | P0-3b、P0-6 | 端到端贡献流程通过；用户可理解凭据模式、原始成本预算、费用、收益、撤回状态和删除结果 |
| P1-7 配置差异预览与审计 | 桌面端 | 写入前 diff、变更清单和恢复记录 | P0-8 | 每次配置变更均可预览、追踪来源并恢复，不重复建设崩溃恢复机制 |
| P1-8 Grok 能力补齐 | 桌面端 + 测试 | 图形会话、会话索引/删除、扩展同步、项目 Capture/Apply | P0-9 | 第 8.5 节四项缺口完成或明确声明不支持，均有版本化验收结果 |

### 19.3 P2 / 平台化

| Epic | 主责 | 主要产物 | 依赖 | Definition of Done |
| --- | --- | --- | --- | --- |
| P2-1 组织与多租户 | 平台 + 安全 | 租户、角色、审计与数据隔离 | P1-5、P1-6 | 租户间身份、凭据、账本和日志隔离通过安全测试 |
| P2-2 策略中心 | 平台 + 桌面端 | 版本化策略发布、建议/强约束边界 | P1-1、P1-2 | 云端策略不静默覆盖本地偏好，冲突处理可审计 |
| P2-3 插件化适配器与开放 SDK | 架构 + 生态 | 适配器框架、SDK、认证与准入测试 | P0-4、P1-3 | 第三方节点可按契约接入并通过兼容/安全认证 |
| P2-4 企业私有部署与边缘编排 | 平台 + 运维 | 私有部署包、节点注册与升级/恢复 | P0-5、P0-10、P0-11、P1-5、P2-1 | 私有环境可独立部署；升级失败时仅在目标、epoch、吊销状态与迁移可逆性均通过验证后执行受签回滚，否则前滚修复或使用已验证恢复点；任何路径都不覆盖本地权威数据 |
| P2-5 统一结算与生态合作（含边缘账本接入） | 数据 + 财务 + 平台 + 安全 | 可追溯结算、对账、争议处理；签名 `EdgeSettlementReceipt`（节点证书/序号/防重/对账/吊销/争议） | P0-6、P1-4、P1-6 | 任一结算项可追溯到受信 attempt 记录并完成对账；边缘回执经签名验证、序号连续、可对账、证书吊销即拒收后，方可纳入云端结算 |

### 19.4 开发工作包、版本向量与发布交接

以下为【已确认设计】。Epic 定义交付目标，工作包定义可独立开发、验证和合入的执行单元。每个工作包必须指定 owner、代码与数据边界、输入/输出契约、数据库和配置影响、测试分片、灰度方案、回滚/前滚方案及完成证据。

| 工作包 | 覆盖 Epic | 主要范围 | 必要产物 | 合入门槛 |
| --- | --- | --- | --- | --- |
| WP-01 契约、用量与账本 | P0-1、P0-4、P0-6 | Key DTO、capabilities/readiness、EndpointDescriptor challenge、TargetEndpoint/RouteBinding、RequestRecord/Attempt/Link、CanonicalError、幂等与结算 | OpenAPI/JSON Schema、EndpointDescriptor 请求/响应与签名规范、DDL/迁移、黄金 JSON、Head CAS、claim/lease/fence/intent 并发与崩溃测试、旧新账本对账 | 字段、身份、主键、状态、缓存和错误语义冻结；EndpointDescriptor 错/复用 nonce、过期、越权 entrypoint、跨 origin 重放与错传输测试通过；Endpoint/Binding CAS、RequestRecord 聚合查询、intent 崩溃窗口、重复 dispatch/结算、越权查询、未知状态与 legacy 迁移测试通过 |
| WP-02 桌面凭据与配置事务 | P0-2、P0-8 | 安全凭据源、官方客户端投影、ConfigApplyOperation/AtomicityGroup/ConfigTransaction、AtomicityGroupBinding/ConfigTransactionBinding、耐久锁、journal/多 Head commit point、崩溃恢复 | 可重入迁移器、关联表 Schema、配置事务实现、残留扫描、commit point 前后与多 Head 冲突恢复矩阵、runtime activation | 每个原子组中断后只能得到一致旧态、完整新态或 recovery_required；同组多事务/多 binding revision 不部分提交；任一 Head ETag 冲突使全组零 Head 推进并恢复外部资源；取消/拒绝/冲突闭合，多客户端 partial_success 可解释；进程激活失败不倒写 commit；真实用户目录和普通备份中无秘密 |
| WP-03 网络、贡献与节点安全 | P0-3a、P0-3b、P0-5 | TLS、Token、SSRF、代理/探测、上游/贡献凭据、公开 DTO、撤回删除 | 网络/SSRF 矩阵、密钥迁移、服务端关闭闸门、撤回作业及审计 | 非 loopback HTTP、危险代理/重定向和越权 DTO 被拒绝；P0-3a 加密/迁移/轮换通过；P0-3b 按 profile 取得正向 R 或关闭态 C 证据 |
| WP-04 隔离测试与客户端兼容 | P0-7、P0-9 | 路径注入、日志/支持包、四客户端、跨协议矩阵与无障碍 | 并行分片、独立日志、完成 manifest、版本化支持/默认链路声明、无障碍报告 | 无真实目录访问和秘密泄漏；首发矩阵无空白单元；核心页面无障碍基线通过或有登记例外 |
| WP-05 生产运行与恢复 | P0-10 | profile 声明交付端的硬化、N 端不发布证明、readiness、排空、密钥、审计、备份和事件响应 | 生产 manifest、不发布证明、运行手册、恢复点、演练报告、告警规则 | profile 交付端的隔离恢复、轮换、排空、高风险审计及事件演练均通过并绑定候选构建；N 端三类不发布证明齐备 |
| WP-06 可信构建与升级 | P0-11 | 构建、签名、制品、版本向量、迁移和发布交接 | 固定 digest 制品、SBOM、provenance、签名、迁移包、trust bootstrap/recovery、不可变 `release_evidence_manifest`、hash-chained release/attestation status、nonce-bound `release_status_checkpoint`、独立 `p0_11_detached_attestation` | 所有证明绑定正确 digest；manifest → initial current/checkpoint → detached attestation → issued → active → activation 后新鲜 checkpoint → Go 的顺序可验证且无反向引用；篡改、降级、吊销、回滚/状态重放、截断后缀、错 nonce、权威不可达、trust-state 丢失和不兼容升级均被阻断 |
| WP-07 产品封套、首用体验与交付治理 | 全部首发 P0；跨 WP-01～WP-06 | release profile、四条首用路径、三上下文 UX、操作责任、支持声明、指标与决策回写 | `release_profile_id/applicability_revision` 封套、R/R*/C/N 矩阵、用户语言映射、逐阶段实现状态/失败态、RACI、指标字典、决策登记、交付检查表与明早摘要 | 所有 profile 均有单一 DRI、默认 CTA、Disabled/N 负向证据和支持文案；四条路径从入口到验证/恢复无未定义状态；指标有定义/数据源/owner/分群/节奏；D-REL-001、D-UX-001、适用时的 D-OPS-001 及受影响决策可追溯；不得以目标设计冒充当前已实现能力 |

`R-DIRECT-DESKTOP-v1` 的星号工作包子集固定如下，不允许在候选验收时临时缩减：

- `WP-01*`：只覆盖 direct TargetEndpoint、RouteBinding、capabilities 与 CanonicalError；排除 Deployment 节点账本、RequestRecord/RequestAttempt/RequestLink 与中转结算。
- `WP-03*`：只覆盖桌面端出站 TLS、SSRF/代理与凭据隔离；排除中转监听、节点凭据保管和贡献托管。
- `WP-05*`：只覆盖桌面密钥、诊断、更新相关运行保护与桌面事件响应；排除 Deployment readiness、draining、备份恢复与灾备。
- `WP-06*`：只覆盖 `artifact_family=desktop_app` 及桌面 `UpdateTrustScope` 的构建、签名、更新、回滚与状态验证；排除 server、adapter 和 migration_package 制品族。

上述四个 `R*` 子集必须在 `release_evidence_manifest` 中分别记录 `wp_id`、规范子集、明确排除面、acceptance case 与证据索引；子集内任一单元未通过即阻断 DIRECT 发布。§20.5.1 规定的 DIRECT P0 星号子集同时适用，两层记录不得互相替代。

发布以 G0～G5 六个顺序门禁组织，后门禁不能补偿前门禁缺失：

| Gate | 目标 | 必要输入 | 二值退出条件 | 主要责任 |
| --- | --- | --- | --- | --- |
| G0 范围冻结 | 冻结首发承诺 | `release_profile_id`、`applicability_revision`、persona、默认 CTA、拓扑/A/B/C、R/R*/C/N 矩阵、D-REL-001、D-UX-001；HYBRID/LOCAL-EDGE 另需 D-OPS-001，CLOUD/DIRECT 对该决策登记 N | profile、适用性、Disabled/N 表面、适用决策、DRI/审批人和证据清单全部具名；任一必填项空白即 No-Go，G0 关闭前不得生成候选 | 产品 DRI + 架构/安全/测试/运维 |
| G1 契约与安全止血 | 先关闭越权和明文风险 | P0-1、P0-3a、P0-4/P0-5 冻结契约；P0-3b=R 的保管/权限/服务端强制边界，P0-3b=C 的 containment | 字段白名单、凭据加密/迁移、TLS/SSRF、身份/端点信任均有二值证据；P0-3b=R 的正向保管与权限边界通过，P0-3b=C 的服务端拒绝、客户端不可达和默认关闭通过 | 架构 + 网关 + 安全 |
| G2 核心数据与恢复 | 建立可恢复执行地基 | P0-2、P0-6、P0-8、P0-10 设计与实现；P0-3b=R 的转移/撤回/删除/备份对账 | 配置事务、幂等/intent、账本迁移、密钥/审计和恢复能力按 profile 通过；P0-3b=R 的转移、撤回、主存储删除与备份对账数据闭环通过 | 桌面 + 数据 + 运维 |
| G3 UX 与兼容 | 形成可交付产品路径 | P0-7、P0-9、WP-07、客户端支持矩阵；P0-3b=R 的贡献用户旅程、条款与失败恢复 | 四条首用路径、A/B/C、失败/恢复、无障碍、支持声明与指标事件无空白；P0-3b=R 的授权、条款、转移/撤回进度和 `manual_action_required` 恢复可用；目标设计与已实现状态分离 | 产品 + 桌面 + 测试 |
| G4 候选与证据 | 生成并冻结同一候选 | 已关闭的 G0～G3、P0-11a、P0-10 候选绑定演练计划 | G4 开始后才生成制品并冻结版本/profile/schema/fingerprint；P0-10 的部署、排空、轮换、恢复和事件演练绑定该候选；P0-3b=R 收集该候选的最终正向证据；全部适用 P0/WP 与 C/N 证据进入不可变 manifest；initial current statement 与 `attestation_state=none` 的初始 checkpoint 有效 | 发布工程 + 各 evidence owner |
| G5 激活与发布决策 | 证明 attestation 可用且仍新鲜 | manifest、detached attestation、状态权威与 witness | attestation `issued → active` 连续；activation 后 checkpoint 明确 active；产品/架构/安全/测试四方 Go 均绑定同一对象；部署前再取新鲜 checkpoint | 发布工程 + 产品/架构/安全/测试/运维 |

每个候选发布集使用唯一 `release_id` 关联以下完整版本向量：

| 字段 | 含义与核验规则 |
| --- | --- |
| `release_profile_id` | 第 5.2 节冻结的首发封套；profile 变化必须生成新 `release_id` 和 fingerprint |
| `applicability_revision` | R/R*/C/N 矩阵修订号；任一 P0/WP 适用性或 Disabled/N 边界变化必须提升 |
| `app_version` | 桌面 App 构建版本及更新通道 |
| `server_version` | local-edge/cloud Sub2API 服务端构建版本 |
| `contract_version` | 客户端协同 API 主/次版本；遵循第 13.5 节兼容规则 |
| `db_schema_version` | 已应用迁移终点、迁移集合 checksum 与兼容区间 |
| `config_schema_versions` | 组件化映射，至少分别记录 `app_config`、`server_config`、`transaction_journal` 的版本与兼容区间；禁止用一个标量掩盖三者独立演进 |
| `adapter_set_version` | 适配器集合版本；manifest 逐项列出每个客户端/协议适配器的版本与 digest |

每个制品另有独立 `artifact_digest`。测试、扫描、SBOM、签名和 provenance 绑定各自制品的同一 digest，再由 `release_id` 与完整版本向量关联。缺失组件必须显式记录为 `not_applicable`，不能静默省略；`deployment_id` 是运行时信任主体，不属于版本号，但必须与实际部署的 release manifest 和版本向量关联。

防降级与故障回滚采用同一可信发布协议。`security_epoch` 是由安全发布权威签发的无符号单调整数，签发作用域固定为 `(release_authority_id, artifact_family)`；运行时完整 `UpdateTrustScope=(target_kind, target_id, release_authority_id, artifact_family)`。`artifact_family` 至少区分 `desktop_app|server|adapter|migration_package`，发布通道不能降低同一作用域的安全下限。每个 release manifest 声明各 artifact family 的版本、epoch、已知吊销与数据库/配置可逆性；桌面安装实例保存 `max_accepted_epoch[artifact_family]`，服务端 Deployment 保存其对应数组。普通安装器和部署器拒绝任何版本倒退或 epoch 低于已保存下限的制品。

确需故障回滚时，必须使用由独立发布根签名的 rollback manifest。其目标使用 `target_kind=desktop_installation|deployment`：桌面制品绑定稳定 `installation_id`，服务端制品绑定 `deployment_id`；适配器和迁移包同时绑定宿主目标与 artifact family。manifest 还绑定来源/目标 digest、版本向量、原因、审批人、有效期和恢复点。回滚不得越过当前 `security_epoch`，不得恢复已吊销或已知高危版本，也不得跨越不可逆迁移；不满足条件时只允许前滚修复或从已验证恢复点恢复。验收分别证明恶意降级被拒绝、合法授权回滚成功、过期/篡改/错目标/越过安全下限的回滚被拒绝。

`security_epoch` 安全下限、已消费 serial、签名根/吊销快照版本和目标身份存放在不会随应用、业务数据库或普通恢复点回滚的受保护 trust state 中；授权回滚只能改变版本向量，不能降低已经记录的安全下限。桌面 App 的 `installation_id` 在首次可信安装时生成并保存在该状态中，升级期间保持稳定；Deployment 使用其既有 `deployment_id`。rollback manifest 还必须包含唯一 `manifest_id`/单调 `serial`、签名 `key_id`、签发时间、有效期、来源/目标 epoch、吊销快照版本和审批策略。验证器持久记录已消费 serial，拒绝重放、过期 manifest 与过旧吊销状态。

首次安装与 trust state 丢失必须分流：只有目标明确处于 `uninitialized`、不存在既有 installation/Deployment 标记及受管业务数据时，才接受内嵌信任根验证通过的受签 `trust_bootstrap_manifest`，创建目标身份并初始化安全下限；已 enrollment 的目标一旦发现 trust state 缺失、损坏或与现有数据身份不一致，必须 fail closed，不能伪装成首次安装。硬件更换或灾备恢复使用独立受签、带唯一 serial、旧/新目标身份、最近安全下限、吊销快照、审批与有效期的 `trust_recovery_manifest` 重建保护状态，恢复后的下限不得低于此前已确认值；无法证明此前下限时只允许前滚到当前受信安全 epoch，或按人工隔离流程恢复，不得静默降级。

数据库与持久配置升级统一执行 expand-migrate-contract：

| 阶段 | 必须动作 | 退出条件 |
| --- | --- | --- |
| `expand` | 先发布向后兼容的新增表、列、字段和读写路径；旧 App/server/adapter 仍可运行，不执行破坏性删除 | 新旧版本契约测试和 readiness 通过 |
| `migrate/backfill` | 以可恢复、幂等、带 checkpoint/checksum 的任务迁移数据；记录进度、失败对象与重跑结果 | 数据完整性、账本、秘密保护及重跑测试通过 |
| 双版本验证 | 在混合 App/server/adapter 版本下验证读写、配置、审计、备份恢复与回滚/前滚 | 支持窗口内的版本矩阵无空白和未解释差异 |
| `contract` | 排空旧实例、停止旧格式写入，再删除旧字段、索引、配置或适配器路径 | 兼容窗口结束、恢复点已验证、旧写入为零，并有不可逆步骤审批 |

发布交接至少包含：签名安装包/镜像及 digest；release manifest、版本向量、源码提交和构建环境；OpenAPI/JSON Schema、迁移包及 checksum；测试分片 manifest、兼容矩阵和安全报告；SBOM、依赖/许可证扫描、签名与 provenance 验证结果；灰度、排空、回滚/前滚、备份恢复和事件响应手册；监控告警与阈值；已知问题、风险接受、支持/弃用声明、owner、值班和升级链。

每个发布证据包必须包含受签且不可变的 `release_evidence_manifest`。其未签 `manifest_payload` 至少记录 `schema_version`、`manifest_revision`、`release_id`、`candidate_fingerprint`、完整版本向量、各制品 `artifact_digest`、源码提交、构建环境摘要、测试环境指纹、P0-1、P0-2、P0-3a、P0-3b、P0-4～P0-10 与 P0-11a 的逐项验收证据、测试分片 manifest、演练报告、迁移/恢复证据、风险接受、各 evidence set 的前置 owner approval 元数据及其 detached signature digest；它不包含 manifest 外层签名、最终 `manifest_digest`、P0-11b、对自身完成的证明或最终 Go 决策。每项证据记录 `p0_id`、`acceptance_phase`、`acceptance_case`、`shard_id/cell_id`、执行命令、生产者、起止时间、`evidence_status`、可空 `result_state`、日志/result digest、复用来源、缺陷 ID 与内容 hash。`manifest_digest` 必须严格使用 §16.5.1 helper 的 `object_type=release-evidence-manifest`：unsigned payload 删除 `manifest_digest` 与外层 `signature` 后做 JCS，再计算域分隔 payload digest；外层签名唯一覆盖该 unsigned payload 及其 digest。前置 owner approval 只能绑定相应 evidence set 及其内容摘要，不能反向引用尚未形成的最终 `manifest_digest`；P0-11b 与最终 Go 只在 manifest 外向前绑定该 digest。制品 digest 或完整版本向量变化必须创建新的 `release_id`；同一候选仅重跑、补签或替换证据时保留 `release_id` 并递增 `manifest_revision`。

DIRECT profile 的 manifest 还必须包含 WP-01*/WP-03*/WP-05*/WP-06* 的 `wp_id`、规范子集、明确排除面、acceptance case 与证据索引。manifest 中所谓 P0-11a“状态/checkpoint 证据”只指流水线、验证器、测试向量和预演结果；签署 manifest 后为本候选生成的初始 current statement 与初始 checkpoint 明确排除在 manifest 之外，只能由后续 detached attestation 和状态对象向前引用，禁止把本候选初始 checkpoint digest 反向写入 manifest。

P0-11 采用固定的单向签署顺序，任何一步缺失都保持 No-Go：

1. G0 已冻结且 G1～G3 已关闭后，在 G4 开始时按冻结输入生成候选制品、版本向量和 candidate fingerprint；再生成 P0-1、P0-2、P0-3a、P0-3b、P0-4～P0-10 的全部最终证据，以及 P0-11a 的构建、签名、验证器、bootstrap/recovery/rollback、防降级、状态/checkpoint 流水线预演和迁移演练证据。P0-10 的部署、排空、轮换、恢复与事件演练必须绑定该候选；本步骤不生成或引用本候选的初始 current statement/checkpoint。
2. 生成并签署上述不可变 `release_evidence_manifest`。
3. 为该 manifest 发布初始 `release_status_statement`（`lifecycle_state=current`），并取得新鲜的初始 `release_status_checkpoint`；该 checkpoint 的 `current_attestation_digest=null`、`attestation_state=none`，只证明 manifest 的状态链 head，不表示候选已经 Go。
4. 发布工程、架构、安全和运维 owner 在受保护 payload 中固定 `required_signers=[release_engineering,architecture,security,operations]`，各用 purpose/role 匹配的独立 key 签署 `p0_11_detached_attestation`。其最小字段为 `attestation_id`、`release_authority_id`、`release_id`、`candidate_fingerprint`、`manifest_digest`、`manifest_revision`、`p0_11a_evidence_set_digest`、初始状态 statement 的 digest/serial、初始 checkpoint digest、`required_signers`、`signed_at`、`expires_at` 和 `signatures[]`；`attestation_digest` 严格使用 §16.5.1 helper 的 `object_type=p0-11-detached-attestation`，unsigned payload 排除 `attestation_digest` 与 `signatures[]`，manifest 不反向包含该 attestation digest。
5. 发布 attestation 的受签 `attestation_status_statement`（`attestation_state=issued`），验证器核对四个 role/key、吊销快照、有效期、candidate/manifest/初始 checkpoint 绑定后，再追加不可变的 activation statement（`attestation_state=active`）。activation payload 必须引用同一 `attestation_digest`、manifest digest/revision、candidate fingerprint、初始 checkpoint digest，并由 attestation-status authority 签署；`issued` 不能直接派生 Go。
6. activation statement 写入独立 attestation 状态链后，重新取得一次性 nonce 绑定的**新鲜** `release_status_checkpoint`。该 checkpoint 必须同时承诺当前 release 链的 `release_status_max_serial/release_chain_head_digest` 与 attestation 链的 `attestation_status_max_serial/attestation_chain_head_digest`，并固定 `current_attestation_digest=attestation_digest`、`attestation_state=active`、同一 manifest digest/revision 和 `derived_release_state=current`；验证器必须分别补齐两条链从受保护旧 head 到 checkpoint head 的完整节点。
7. 产品、架构、安全、测试四方以 `object_type=release-go-decision` 各自独立签署 Go，受保护 payload 绑定最终 manifest digest/revision、active attestation digest、activation statement digest、评审时 checkpoint digest及两组 serial/head、完整版本向量和目标身份。生产部署前必须再次执行第 6 步取得 fresh checkpoint；任一对象变化都要重新执行受影响的后续层级，不能沿用旧签名。

P0-11 完成的二值定义为：manifest 内全部 P0-11a 单元通过，存在签名有效、未过期、未吊销且四 role 完整的 attestation，存在 `issued → active` 的不可截断独立 attestation 状态链，并有新鲜 checkpoint 同时承诺唯一 current release head、active attestation head 与同一 manifest。P0-11b 只完成该 detached attestation，不向 manifest 内追加对自身的 evidence cell；缺 activation 或只拿到 `attestation_state=none` 的初始 checkpoint 均为 No-Go。

`candidate_fingerprint` 冻结为 §16.5.1 helper 的 `object_type=release-candidate-fingerprint`。unsigned preimage 固定为 `fingerprint_version="1"`、`release_authority_id`、`release_id`、`release_profile_id`、`applicability_revision`、`evidence_valid_until`、按 `(artifact_family,artifact_name)` 排序的 `artifact_set[{artifact_family,artifact_name,artifact_digest}]`、完整 `version_vector`、按 `schema_name` 排序的 `schema_set[{schema_name,schema_version,schema_digest}]`、按 artifact family 排序的 `security_epochs[{artifact_family,epoch}]`、按 `(purpose,root_id)` 排序的 `trust_roots[{purpose,root_id,keyset_digest}]`、按 purpose 排序的 `revocation_snapshots[{purpose,snapshot_version,snapshot_digest}]`、`candidate_deployment_config_digest`、`migration_plan_digest` 与 `compatibility_policy_digest`；集合先去重再按 UTF-16 码元排序，`epoch` 使用十进制字符串。`manifest_revision`、evidence cell/日志、owner approval、执行时间、attestation/status/checkpoint/Go digest 均明确排除：它们描述同一候选的证据和决策演进。profile/适用性、证据有效截止时间、制品、版本、schema digest、安全 epoch、根/吊销材料、候选配置、迁移或兼容策略任一变化必须生成新 fingerprint 与新 `release_id`；仅在上述 preimage 完全不变时，重跑或补签证据才可保持 fingerprint、递增 `manifest_revision`。

受签 manifest 和证据记录一经签名不得原地改写；release lifecycle 与 attestation lifecycle 必须维护两条独立、不可截断的追加链，禁止以一个共享 serial/head 让任一链被选择性截断。`release_status_statement` 的受签 payload 固定包含 `release_authority_id`、`release_id`、`release_status_serial`、可空 `prev_release_status_digest`、目标 manifest digest/revision、`lifecycle_state=current|invalidated|superseded|revoked|expired`、可空 replacement digest/reason、`issued_at`、可空 `effective_at`、`signing_key_id`、keyset 与吊销版本；`statement_digest` 严格使用 `object_type=release-status-statement`，排除自身 digest 与 `signature`。`attestation_status_statement` 在独立作用域 `(release_authority_id, release_id, attestation_id)` 中固定包含 `attestation_status_serial`、可空 `prev_attestation_status_digest`、`attestation_digest`、manifest digest/revision、candidate fingerprint、`attestation_state=issued|active|revoked|superseded|expired`、可空 `activation_of_status_digest`/reason、签署与吊销版本；`status_digest` 使用 `object_type=attestation-status-statement`。`active` 只允许紧接同一 attestation 的 `issued` 历史，activation 必须引用 issued 节点；终态不可复活。两条链的 serial=1 都必须以空 prev 开始，后续节点只能引用本链上一节点 digest，同 serial 不得出现不同 digest。

两条链的状态节点作为 hash 链历史记录都不因时间推移消失；attestation 自身超过 `expires_at` 时验证器立即 No-Go，权威还必须在 attestation 链追加 `expired` 形成可审计终态。checkpoint 的新鲜度只由其自身有效期约束。

Go 评审和每次生产部署前，验证器必须生成一次性、至少 128 bit 的 `client_nonce`，从配置的发布状态权威取得受签 `release_status_checkpoint`，或取得具备同等新鲜度保证的受见证透明日志 checkpoint。checkpoint 至少包含 `release_authority_id`、`release_id`、该 nonce、`release_status_max_serial`、`release_chain_head_digest`、`attestation_status_max_serial`、可空 `attestation_chain_head_digest`、可空 `current_manifest_digest/current_manifest_revision`、可空 `current_attestation_digest`、`attestation_state=none|issued|active|revoked|superseded|expired`、`derived_release_state=current|no_current|invalidated|revoked|superseded|expired`、签发时间、有效期、key id、authority signature 和 witness signatures；`checkpoint_digest` 严格使用 §16.5.1 helper 的 `object_type=release-status-checkpoint`，unsigned payload 排除 `checkpoint_digest`、authority signature 和 witness signatures，签名输入只能是 helper 生成的 payload digest。无 attestation 时必须精确为 `attestation_status_max_serial=0`、`attestation_chain_head_digest=null`、`current_attestation_digest=null`、`attestation_state=none`。当 `attestation_state=active` 时，attestation head 必须是同一 `current_attestation_digest` 的可验证 activation 节点，release head 必须派生同一 current manifest；不能只回显未签字段。无 current 或 `attestation_state=none|issued` 时仍能签发明确的 No-Go checkpoint。

验证器必须分别取得并验证两条链直到 checkpoint 承诺的 head，使两组 serial/head 精确一致，并在受保护 trust state 中原子更新 `max_verified_release_status_serial`、`verified_release_chain_head_digest`、当前 attestation 的 `max_verified_attestation_status_serial`、`verified_attestation_chain_head_digest`、`latest_trusted_checkpoint_digest` 及 attestation digest/state；仅验证调用方提供的任一连续前缀不能通过。新安装在没有既有 floor 时也必须从两条链各自 genesis（无 attestation 时使用 0/null 哨兵）验证至 checkpoint。状态权威不可达、checkpoint 过期或 nonce 错配、任一链断裂、任一 serial/head 回退、同 serial 异 digest、两链与 manifest/attestation 绑定不一致、`current_attestation_digest` 与 activation 不一致、无法补齐到任一最新 head，或 `derived_release_state` 非 current 时立即 No-Go。

候选指纹或信任材料变化时，验证器必须本地立即拒绝旧 manifest、持久化 No-Go 观察与原因；只有 release authority 能追加受签 `invalidated` statement，replacement 尚未产生也不延迟本地拒绝。新 manifest 产生后由权威再追加 `superseded` 关系并记录 replacement digest。Go 评审和实际部署都必须重新计算 candidate fingerprint、验证最新 checkpoint、完整状态链与吊销索引，不能只依赖异步任务修改数据库标志，也不能假定验证器持有发布权威签名权限。

各阶段不虚构月份承诺；退出以门槛达成为准。

### 19.5 开发者启动清单

【已确认设计】新开发者按以下顺序进入，不依赖口头背景：

1. 先读第 1 节的四种状态标签语义，再读第 2、6、8、9、22 节，确认三端职责、A/B/C/direct、数据权威和当前 ADR 索引；再进入所属工作包对应章节。
2. 每个工作包的首个合入先补齐该包的入口资产：owner/边界说明、独立 ADR 或本文决策引用、OpenAPI/JSON Schema、数据库/配置 schema、测试命令、分片 manifest、样例与本地恢复步骤。当前独立 `docs/adr/`、正式契约文件和统一测试入口尚未全部存在，不能把索引当作已经交付的实现资产。
3. WP-01 先冻结 TargetEndpoint/RouteBinding 持久化键、RequestRecord/RequestAttempt/RequestLink、能力自省、错误和查询契约；WP-02 同步冻结 ConfigApplyOperation/AtomicityGroup/ConfigTransaction 与秘密投影。其余工作包只依赖已版本化产物，不复制未定型字段或状态机。
4. 每个 PR 标明影响的 `release_id` 版本向量字段、迁移/兼容范围、测试分片、回滚或前滚条件及对应 P0 验收行；无法映射到 Epic、工作包、ADR/章节和二值验收的改动不得作为“顺手魔改”合入。

首轮实施应在 `docs/adr/`、契约目录和各工作包 README 中建立双向链接与可复制命令；本文继续作为产品与跨域决策权威，代码级细节由这些版本化资产承接。

## 20. 验收标准、测试策略与核心指标

### 20.1 测试现状

本次 Codex 验证：

- Sub2API 定向 Go 测试（handler、service、repository）通过。
- 本地 App 测试 374 项通过，1 项失败。
- 失败表象是测试读取真实 `%APPDATA%\Code\User\settings.json` 时遇到文件占用；代码审计确认根因是组合根路径注入不完整：部分被测组件仍会自行创建默认 AppData 路径，并在构造/恢复流程中读取真实 Codex、Claude、Gemini、Grok 与 VS Code 配置。
- 该失败作为“测试隔离与本地数据安全”技术债：完整 `AppDataPaths` 必须由 composition root 注入；被测组件不得自行 `CreateDefault()`，测试只使用临时根、伪凭据和本地 stub。

上述为定向单元/集成验证，本文不宣称已经完成全量端到端、真实 CLI 全版本或生产环境验证。

### 20.2 测试策略（【已确认设计】）

测试体系包括契约测试、凭据迁移、异常退出与配置恢复、真实 CLI 兼容矩阵、跨协议语义、流式计费幂等、云边故障演练、公开 DTO 与支持包黄金测试。

快速单元与烟雾测试可单进程运行；全量系统矩阵、版本扫描、协议/模型族组合和故障演练默认并行。按客户端、版本、协议、模型族或场景分片，每个 worker 使用独立临时根、输出目录和日志，支持恢复执行并生成完成 manifest。安全并发未知时从 4 个 worker 起步，观察 CPU 与内存后扩展；汇总前确认每个计划单元均处于“新完成”或“已复用”状态，不以单个串行长进程执行大网格。

每个计划单元使用稳定唯一 `cell_id`，记录客户端、精确版本、协议、模型族、场景、seed、候选制品 digest、执行命令、环境指纹、日志 digest、复用来源和互斥终态。每批必须满足 `planned_cells == new_cells_completed + cells_reused`，且每个计划单元恰有一个终态；复用结果必须引用相同制品 digest 与兼容环境。P0 分片要求 `not_run=0`，P0 核心不得以 `known_unsupported` 或风险接受放行。

### 20.3 可二值判定验收表（Given/When/Then）

| 场景 | Given | When | Then（二值判定） |
| --- | --- | --- | --- |
| usage 字段隔离与缓存 | 任意数据面 API Key，以及用户/管理员对照身份 | 请求原始/敏感 `/v1/usage` 与最小化聚合快照接口，并验证缓存 | 数据面 Key 响应不含 `account_cost`、钱包、订阅总额、账号、渠道、代理或管理员字段；原始/敏感响应含 `Cache-Control: no-store`；只有最小快照可按明确 TTL/ETag/版本缓存，且含 source、可空 `deployment_id`、provider/billing_scope、主体范围、snapshot_at 与 cache_state；授权矩阵与黄金 JSON 全部通过 |
| 凭据迁移 | profiles、`.bak`、App 备份和官方 CLI 兼容载体中存在伪造明文秘密 | 执行可重入迁移并模拟任一步骤失败 | 安全存储成为唯一源；profiles 与 App 自建备份无秘密；必要官方投影满足最小字段/ACL/清单/清理；失败可回滚且秘密不丢失 |
| legacy Cloud Profile 迁移 | Cloud Profile 无法证明其目标是云中转还是 direct 上游 | 升级、展示和执行迁移 | 迁移前显示 `legacy-cloud-ambiguous`，不自动判断费用/隐私/回退；迁移后显式 route_target 与 endpoint 可恢复、可回滚且来源显示可靠 |
| 能力协商 | 节点分别返回支持、缺少 feature、主版本不兼容和未知可选字段；准备匿名、数据面 Key/Token、管理身份三类调用方，以及 EndpointDescriptor 正确/错误/复用 nonce、过期、跨主体/跨 origin/错传输响应 | App 调用客户端协同 API，并由仅持数据面凭据的 App 执行 nonce challenge | 支持项正常启用；缺失项安全降级；主版本不兼容停止协同 API 但保留手工配置/既有数据面调用；未知 feature 不会被默认启用；仅持数据面凭据可取得 Deployment/版本/协议/自身 feature、最小 readiness 与自身可调用入口的受签 descriptor，但不能读取内部依赖、账号、渠道、价格特例或管理能力；错/复用 nonce、过期、跨主体/跨 origin/错 Deployment/role/transport/entrypoint 响应均不能生成可 active 的 TargetEndpoint |
| loopback/LAN 安全 | 新安装、旧的非加密会话、未授权 LAN、已配置 TLS 的 LAN，以及数据面/协同/管理/贡献/流式入口和重定向目标 | 对每个表面执行启动、登录、恢复、请求、流式连接、贡献提交与逐跳重定向测试 | 只有真实 loopback 的非贡献表面允许 HTTP；贡献始终要求 TLS；任何 LAN/公网 HTTP 及跳转后的非 loopback HTTP 都被拒绝；未授权 LAN 不可达；获准 LAN 只有在 TLS 与独立 Token 有效时可访问 |
| SSRF、代理与出站探测 | HTTP/HTTPS/WS/WSS、自定义上游、每跳重定向、HTTP CONNECT、SOCKS5/5h、代理探测和安全开关开/关样本 | 覆盖 DNS 重绑定、IPv4/IPv6、CGNAT、链路本地、云元数据、IDNA/混淆地址、代理地址自身、远端 DNS、跨域跳转、凭据头、响应大小及连接/TLS/首包/总时限 | 每一跳和最终解析地址都按策略校验；危险目的地与明文公网探测被拒绝；跨 origin 自动剥离 Authorization/Cookie/代理凭据；安全校验关闭或依赖故障不得意外 fail-open；HTTP、WebSocket 与代理链结果一致且有 reason_code |
| 计费幂等与未知状态 | 同一 Deployment、`ledger_domain_id` 和 `idempotency_key` 的并发相同/异指纹重放、dispatch lease/fence 接管、intent 提交前后崩溃、首字节后状态落盘前崩溃、旧 owner 恢复、单个 RequestRecord 下多个 attempt、跨 endpoint RequestLink、无用量失败及四类 query_status | 对支持和不支持幂等键的 CLI 执行并发重试、各崩溃点、租约过期、断连、Key 轮换后同 principal 查询、跨主体查询、状态码和跨端点场景，并扫描 URL/日志/支持包 | 相同重放定位同一 RequestRecord；只有当前 owner/fence 能把无 intent 的 registered attempt CAS 为 dispatching 并发网；intent 前可接管，intent 后只能查询/收敛，旧 fence 写入被拒绝；不同指纹 409；查询严格执行 selector 与 200/202/401/403 语义并返回聚合/attempts/用量；轮换后同 principal 可查询、跨主体统一 not_found；无用量 void；跨域链接不参与幂等/结算/授权；补偿只追加；不宣称 global exactly-once |
| 测试目录隔离 | worker 使用随机临时根并设置 USERPROFILE、APPDATA、LOCALAPPDATA、HOME、XDG | 构造 App 主视图、协调器和 SwitchService 并运行测试 | 所有路径均来自注入根；被测组件不自行创建默认路径；命中真实用户目录立即失败；只使用伪凭据和本地 stub |
| CLI 配置崩溃恢复 | ConfigApplyOperation 的多个 AtomicityGroup/ConfigTransaction 在每个可中断步骤及 commit point 前后崩溃；包含同组多个 binding revision/Head 且仅一个 ETag 冲突的样本，并注入同键同/异 fingerprint、资源锁冲突、journal、Credential Manager、外部文件修改、取消和进程启动失败 | 重启 App、恢复原子组并重放同一操作 | 每个原子组只收敛为一致旧版本、完整新版本或 recovery_required；全部参与 Head、新旧 revision、group/transaction committed 在同一 commit point 一致，条件更新数必须等于应切换的不同 scope 数；任一 Head CAS 失败使零个 Head 推进并先恢复外部资源；同键同指纹返回原 operation、异指纹 409；取消/拒绝/冲突闭合；进程失败只改变 runtime activation；操作聚合严格含 completed/partial_success/failed/cancelled/recovery_required；规范秘密不丢失且不进入普通备份 |
| RouteBinding/RequestRecord/RequestAttempt 边界 | A、B、A+C、B+C 各执行一次包含节点内重试的请求 | 查看诊断、切换绑定并查询用量 | 未叠加 C 的 A/B 不伪造逐请求事实；叠加 C 后只展示代理实际观察；切换 RouteBinding 不改写历史/在途 RequestRecord 或 RequestAttempt；一个逻辑请求与其多个 attempt、实际 instance/endpoint revision 及账本来源均可追溯 |
| 跨协议与四客户端兼容 | 锁定的 Codex、Claude Code、Gemini、Grok 版本与协议组合 | 运行兼容矩阵 | 每个矩阵单元有通过/不支持/缺陷编号之一；流式、工具、审批、取消、超时、重试、断连、回退、幂等键、状态查询和错误语义都有明确结果，不允许空白 |
| 贡献目录与托管闸门 | 普通用户、owner、管理员三类身份及伪贡献凭据 | 查询公开/详情 DTO，并在服务端开关关闭/开启两种状态执行贡献、撤回 | 公开 DTO 无内部 ID 和逐账号敏感字段；身份视图隔离；默认 `contribution_credentials_enabled=false` 且服务端拒绝；未满足加密/迁移/轮换/备份/删除闸门时不能启用；撤回后不可再调度 |
| 支持包 | 日志、配置和异常中植入金丝雀秘密与敏感正文 | 本地生成、预览、导出/上传支持包 | 默认包不含配置正文、数据库、crash dump、原始日志或金丝雀；未显式授权不上传；用户可见文件清单/大小；临时包按期删除 |
| 生产部署、readiness 与排空 | profile 声明交付形态的候选 manifest、N 端三类不发布证明、关键依赖和可选上游故障，以及存在普通、流式、unknown/pending、待结算 attempt 的升级/停止场景 | 验证监听、TLS、服务身份、卷、资源和制品版本，注入依赖故障并触发排空 | 生产不使用 `latest`，local-edge 不以未授权全网监听为默认；live 不替代 readiness；关键依赖异常 unavailable，非关键能力 degraded；进入 draining 后 live 仍可 200、readiness 立即 503，先摘流再以 `DRAINING/not_dispatched` 拒绝新请求并返回 Retry-After；分层时限内收敛或按事实写入 cancelled/failed/unknown，查询与账本保留；证据绑定候选构建，N 端不生成生产 manifest |
| 密钥用途隔离与轮换 | 各用途具有独立 `key_id/version`，存在 active、decrypt-only、revoked 密钥和旧版本密文 | 执行轮换、重启、备份恢复、旧密文迁移和旧密钥吊销，并模拟生产持久密钥缺失 | JWT、TOTP、支付/恢复、备份、上游/贡献、更新签名和节点证书不复用根密钥；根密钥不与密文同库同备份；双读单写迁移可核验；吊销后旧密钥不能继续签名或加密；生产缺少必需持久密钥时相关能力 fail closed |
| 分类备份与完整灾备 | 第 17.6 节六类资产、带标记的本地权威数据、账本、凭据密文、独立根密钥与更新 trust state 专用备份 | 执行备份、篡改、跨 Deployment/环境恢复、隔离 staging、整套 Deployment 丢失及 trust-state 恢复演练 | 六类资产有独立清单、分类、加密、完整性和保留规则；篡改及错环境恢复被拒绝；隔离恢复验证目标身份、版本向量、签名/MAC/hash、epoch 下限和数据范围；trust state 不混入普通业务备份且不含发布私钥；远端来源不覆盖本地权威；实测 RPO/RTO 绑定候选构建 |
| 审计可靠性、不可变锚与 break-glass | 删除、恢复、贡献撤回、密钥轮换和批量治理等普通高风险操作，以及 Sev0/Sev1 停止、吊销、隔离操作；可注入主审计域故障与篡改 | 在正常、延迟、写失败、队列中断和篡改状态执行普通变更与安全遏制 | 普通高风险业务变更在审计/outbox 无法持久关联时不提交；Sev0/Sev1 降权、吊销、隔离和停止仍可走受限 break-glass，并同步写第二耐久审计域、强告警、恢复后补锚对账；break-glass 不能新增权限、恢复服务或删除证据；已接受记录不丢失且篡改可检测 |
| 可信发布、授权回滚与升级 | 全新与既有 App/Deployment、各候选制品，以及未签名、篡改、普通降级、错目标/重放 rollback、trust state 丢失、合法/非法 recovery、吊销、候选 fingerprint 改变、manifest 自指/反向引用、缺失或错 manifest digest 的 detached attestation、删除最新 invalidated/revoked 状态后缀、旧/错 nonce checkpoint、状态权威不可达和 replacement 尚未生成样本 | 运行构建/更新/部署校验、首次 bootstrap、trust recovery、单向签署流、两条状态链失效处理器中断、Go/部署时新鲜 checkpoint 与 fingerprint 重算及 expand-migrate-contract 演练 | 证明绑定正确 digest；全新环境只从有效根签名 bootstrap；既有目标 trust state 丢失 fail closed；合法恢复不降低 epoch 或任一 status floor；桌面 rollback 绑定 installation_id、服务端绑定 deployment_id；错目标、重放、过期、篡改、越下限、吊销和不可逆迁移均拒绝；manifest 只含 P0-1、P0-2、P0-3a、P0-3b、P0-4～P0-10 与 P0-11a 且不反向引用 attestation；P0-11b detached attestation 精确绑定 manifest；release 与 attestation 两条状态链都必须补齐至本次 nonce checkpoint 的对应 head，截断/旧 checkpoint/权威不可达均 No-Go；候选变化即使无 replacement 也立即 No-Go |
| 首次使用与激活 | 全新安装、无云账号，以及云端登录、仅录入已有中转数据面 API Key、本地自主、direct-only 四条入口；覆盖 A、B、A+C、B+C 和外部 A 请求；仅 Key 路径另含正确/错误/复用 nonce 与跨主体 descriptor | 分别完成预检、nonce-bound EndpointDescriptor/信任绑定、配置绑定、一次性受监督 CLI/结构化事件/代理观察验证，并注入权限、TLS、版本、服务未就绪、费用确认、跳过和用户外部确认场景 | 四条路径可到首个成功请求或给出阻断说明；仅 Key 路径只有在 nonce、签名、入口、传输和 Deployment/role 全部匹配时才能创建 active RouteBinding；direct-only 固定显示 `user_mode=none + user_mode_reason=no_enabled_relay + route_target=direct`；RouteBinding active 与 ValidationEvidence/来源确认分轴；跳过时显示“配置已应用、请求尚未验证”；未经 C 的 A 只有受监督且契约化成功信号可机器通过，普通外部请求最多 user_attested；节点回执只提升来源确认；失败不回滚已提交配置 |
| 多客户端部分成功 | 一个 ConfigApplyOperation 包含至少两个独立原子组，其中一个成功、一个失败；另含一个跨共享文件、多个事务、多个 binding revision/Head 的单一原子组 | 应用 RouteBinding 后查看结果并执行保持、创建恢复事务、新 draft 重试、冲突合并或进入恢复工作台 | 独立组得到 partial_success，成功组保持 active；共享文件所属单一原子组的全部事务、binding revision 与 Head 只能整体 committed 或整体回滚，任一 Head 冲突时零 Head 推进，不出现组内部分提交；各状态只显示合法动作，apply_failed revision 不复活；每项原因/diff 可见，在途请求保持旧 revision |
| 贡献撤回闭环 | active/paused 贡献、在途请求、工作副本、主存储和备份副本均存在 | 在撤回各阶段注入超时、删除失败和备份清理延迟 | 状态严格经过 withdrawal_requested、scheduling_stopped、draining、primary_deleted、backup_cleanup_pending、completed，无法收敛时进入 manual_action_required；停止新调度立即生效；各阶段返回审计 ID 和 Deployment；主存储删除前不得显示彻底完成，费用与备份清理状态可见 |
| 统一 UX 状态与无障碍 | 第 18.7 节全部状态，以及 Windows 高对比度、200% 缩放、纯键盘、Narrator、NVDA 和减少动画环境 | 在加载、空数据、实时、缓存陈旧、降级、权限不足、版本不兼容、部分成功、进行中、需恢复和阻断错误下渲染核心页面 | 每种状态均显示规定的对象、来源/时间、`reason_code`、权威级别和可执行动作；状态不只靠颜色；焦点恢复、读屏名称、可控 live region、逐项结果和适用界面的 WCAG 2.2 AA 基线通过；例外有替代方案；Toast 不替代复杂操作详情 |

### 20.4 兼容矩阵轴定义

| 轴 | 取值范围与记录要求 |
| --- | --- |
| 客户端及版本 | Codex、Claude Code、Gemini、Grok；记录精确版本、支持区间与超出区间行为 |
| 协议 | 官方原生协议、app-server/stream-json/ACP 交互通道、协议桥/本地代理 |
| 链路组合 | A、B、A+C、B+C；A/B 是二选一主模式，C 是可选叠加层；B 内部使用配置或进程参数不另记为 A+B |
| 模型族 | `requested_model` 与 `effective_model` 的模型族、上下文限制与映射等级 |
| 流式 | 流式、非流式、首包前失败、流中失败 |
| 工具调用 | 无工具、单工具、并行工具、参数错误 |
| 审批 | 无审批、批准、拒绝、审批超时 |
| 取消 | 用户取消、进程终止、上游取消确认 |
| 超时 | 连接、首包、空闲、总时长超时 |
| 重试与幂等 | 连接前重试、账号池重试、逻辑幂等重放、结果未知；记录是否可注入/传播 `idempotency_key`、是否返回 query_token、是否支持状态查询 |
| 断连 | 客户端断连、节点断连、上游断连与恢复 |
| 回退 | 无回退、节点内账号回退、未叠加 C 的 A/B 绑定切换、A+C/B+C 或 CLI 原生逐请求回退 |

### 20.5 发布门槛（不虚构 SLA）

- **当前结论：No-Go。** 本文只定义门槛，不构成发布证明；当前尚未批准唯一 `release_profile_id/applicability_revision`，也没有绑定同一候选的实现证据。候选 manifest 必须逐项记录该 profile 的 R/R*/C/N：R/R* 取得正向二值证据，C 取得服务端与客户端关闭态负向证据，N 取得“不发布该表面”的构建/路由/文案证明。P0-11b 形成 detached attestation 后，还必须存在连续的 `issued → active` 状态声明和 activation 后的新鲜 checkpoint；产品、架构、安全、测试四方只能绑定该 active checkpoint 签署 Go，生产部署前还要重新 challenge。
- 第 20.3 节所有 P0 核心与首发支持声明对应的用例必须通过；任何空白矩阵单元、真实用户目录访问、秘密泄漏、非 loopback HTTP、SSRF 安全开关意外 fail-open、非语义等价静默回退或重复结算都阻断发布。
- 生产资产仍使用浮动 tag、local-edge 默认未授权全网监听或固定 200 健康检查，缺少完整灾备隔离恢复证据，生产密钥存在用途复用或无法轮换，普通高风险变更允许审计 fail-open、break-glass 无第二耐久记录或缺少不可变锚，候选制品缺少签名、SBOM、provenance、security_epoch、可信防降级/授权回滚、吊销或版本向量证据时，直接维持 No-Go。
- 长矩阵的每个分片必须有独立日志与完成 manifest；汇总只接受结果明确的“通过、已知不支持、已登记缺陷”，不接受未执行。“已知不支持”只能用于支持声明中明确排除的非首发能力；P0 核心能力不得标记为不支持。Sev0/Sev1 安全或数据完整性缺陷，以及任一 P0 Epic 未完成项不得豁免；其他缺陷需要风险接受记录、责任人和修复版本。
- Grok 首发范围必须发布版本化支持声明：连接、配置、路由和终端等首发能力按 P0 通过；图形会话、会话索引/删除、扩展同步和项目 Capture/Apply 可以明确列为非首发“不支持”，并由 P1-8 跟踪，不能用模糊的“部分兼容”放行。
- 具体商业或性能目标值（响应时延、成功率数值、成本节省比例等）保持【待决策】，在数据积累前不虚构 SLA。

#### 20.5.1 P0 发布证据追踪矩阵

下表的适用性是 §5.2 `applicability-1` 的规范副本；两处必须由同一 schema 生成或做一致性校验。`R*` 的子集边界进入 evidence manifest，不能由测试人员临时解释；`C` 只接受 containment 负向证据，`N` 只接受表面未发布证明。

| P0 | profile 适用性 | 权威章节 | 第 20.3 节二值验收行 | 候选构建必要证据 | 主签责任 |
| --- | --- | --- | --- | --- | --- |
| P0-1 | CLOUD/HYBRID/EDGE=R；DIRECT=N | §13.2、§17.1 | usage 字段隔离与缓存 | 授权矩阵、黄金 JSON、缓存/越权报告、制品 digest；DIRECT 提交无中转用量表面的 N 证明 | 网关 + 安全 |
| P0-2 | 全部 profile=R | §10.5、§12、§16.2–§16.3 | 凭据迁移、CLI 配置崩溃恢复 | 可重入迁移报告、残留扫描、ACL/投影/回滚、commit point 与真实目录隔离证据 | 桌面端 + 安全 |
| P0-3a | CLOUD/HYBRID/EDGE=R；DIRECT=N | §10.5、§15、§16.2–§16.4 | 凭据迁移、密钥用途隔离与轮换 | 上游/中转凭据信封加密、旧 JSONB 迁移、轮换、最小解密、备份范围与明文残留扫描 | 网关 + 安全 + 数据 |
| P0-3b | HYBRID=R；CLOUD/EDGE=C；DIRECT=N | §15、§16.2、§16.4、§17.6 | 贡献目录与托管闸门、贡献撤回闭环 | R：专用/转移、撤回、墓碑与备份对账；C：`contribution_credentials_enabled=false`、客户端不可达和服务端拒绝；N：贡献表面未打包/未路由 | 网关 + 安全 + 数据 + 产品 |
| P0-4 | CLOUD/HYBRID/EDGE=R；DIRECT=R*（direct Endpoint/配置子集） | §11.1.1、§13、§18.5 | legacy Cloud Profile 迁移、能力协商、事实边界、首次使用 | OpenAPI/JSON Schema、descriptor/Endpoint/Binding、黄金响应、nonce/重放拒绝及首用契约报告 | 架构 + 桌面端 + 网关 |
| P0-5 | CLOUD/HYBRID/EDGE=R；DIRECT=R*（桌面出站/TLS/SSRF 子集） | §16.1–§16.2、§17.4 | loopback/LAN 安全、SSRF/代理/出站探测 | 网络/TLS/重定向/代理矩阵、reason code 与生产监听/桌面出站 manifest | 网关部署 + 桌面端 + 安全 |
| P0-6 | CLOUD/HYBRID/EDGE=R；DIRECT=N | §13.3、§13.6、§14 | 计费幂等与未知状态、事实边界 | DDL/迁移、claim/lease/fence/intent、崩溃窗口、真值表、旧新账本对账；DIRECT 为无本产品账本表面证明 | 网关 + 数据 |
| P0-7 | 全部 profile=R | §17.3、§20.1–§20.2 | 测试目录隔离、支持包 | 分片 manifest、真实路径守卫、敏感金丝雀与支持包黄金报告 | 测试 + 桌面端 + 网关 |
| P0-8 | 全部 profile=R | §11.4、§12、§18.5–§18.6 | CLI 配置崩溃恢复、多客户端部分成功、首次使用 | journal/锁、关联 schema、多 Head commit point、冲突恢复、runtime activation 对账 | 桌面端 + 架构 |
| P0-9 | CLOUD/HYBRID/EDGE=R；DIRECT=R*（声明客户端/链路子集） | §8.5、§18.5、§18.7、§20.4 | 跨协议与四客户端兼容、首次验证、统一 UX/无障碍 | 精确 CLI 版本矩阵、ValidationEvidence、协议/模型/交互结果、无障碍报告 | 桌面端 + 协议桥 + 测试 |
| P0-10 | CLOUD/HYBRID/EDGE=R；DIRECT=R*（桌面密钥/更新/事件子集） | §17.4–§17.7 | 生产部署/readiness/排空、密钥、灾备、审计 | profile 声明交付端的生产 manifest 与候选绑定演练；N 端提交构建/路由/文案三类不发布证明；DIRECT 只验桌面密钥、更新、诊断和事件响应子集 | 运维 + 安全 + 数据 |
| P0-11 | CLOUD/HYBRID/EDGE=R；DIRECT=R*（desktop artifact family） | §17.6、§19.4、§20.5 | 可信发布、授权回滚与升级 | P0-11a 制品/SBOM/provenance/trust 证据；manifest；detached attestation；issued/active 状态链；activation 后 checkpoint；截断/重放拒绝 | 发布工程 + 架构 + 安全 + 运维 |

对同一 `(release_authority_id, release_id)`，验证器必须先生成一次性 nonce 并取得签名有效、未过期的最新 `release_status_checkpoint`，再分别验证 release 状态链与当前 attestation 状态链从受保护 trust state 已知 head 到 checkpoint head 的完整连续节点；新安装没有既有 floor 时仍须从两条链各自 genesis（无 attestation 为 0/null）验证至该 head。只有 `release_status_max_serial/release_chain_head_digest` 与 `attestation_status_max_serial/attestation_chain_head_digest` 均精确等于 checkpoint，`derived_release_state=current`、`current_manifest_digest/current_manifest_revision` 非空且 `attestation_state=active` 时，才能派生唯一 current `release_evidence_manifest` digest/revision。仅从调用方提供的 statements 中选择最高 serial 不构成有效验证；权威不可达、checkpoint 过期/nonce 错配、任一链重复/断裂、prev digest 不匹配、任一 serial/head 回退、同 serial 异 digest、两链绑定不一致、非法状态转换、没有 current、没有 active attestation 或出现多个 current 时立即 No-Go。验证成功后原子更新受保护的 `max_verified_release_status_serial`、`verified_release_chain_head_digest`、当前 attestation 的 `max_verified_attestation_status_serial`、`verified_attestation_chain_head_digest` 和 `latest_trusted_checkpoint_digest`。

当前 manifest 对 P0-1、P0-2、P0-3a、P0-3b、P0-4～P0-10 与 P0-11a 分别保存 `applicability=R|R*|C|N`、`applicability_revision` 与证据索引。R/R* 的每个 `p0_id × acceptance_case × cell_id` 必须恰有一条正向证据；C 的每个 containment case 必须有客户端不可达、服务端拒绝和配置默认关闭证据；N 必须有构建清单、路由矩阵和用户文案三类证明，不生成虚假的正向 cell。当前 `evidence_status` 的非终态为 `not_started|in_progress|evidence_pending`，执行/审核终态为 `passed|failed`；`superseded` 不属于当前 evidence_status，只能作为旧 manifest 或旧证据在受签状态索引中的历史 `lifecycle_state`。`result_state` 在执行未终结时必须为 null，执行终结后只允许 `passed|failed`；`evidence_status=passed` 必须同时满足 `result_state=passed`、证据完整性校验和 owner 签署通过。

候选 Go 评审要求 manifest 内全部 R/R* 正向单元、C containment 单元与 N 证明的当前 `evidence_status=passed` 且 `result_state=passed`，不存在空白或任何非终态，并且 checkpoint 证明该 manifest 为唯一 current。P0-11b 不占用 manifest 内的 evidence cell；detached attestation 必须签名有效、未过期、未吊销并精确绑定同一 manifest/fingerprint/P0-11a evidence set/初始 checkpoint，随后还要在独立 attestation 链上完成 `issued → active` activation，并由新鲜 checkpoint 同时承诺两条链 head。任何被替代证据、旧 attestation、缺 activation 或 `attestation_state=none|issued` 的 checkpoint 都不能沿用。

Go 决策必须绑定 `release_authority_id`、`release_id`、`release_profile_id`、`applicability_revision`、`release_evidence_manifest` digest/revision、`p0_11_detached_attestation` digest、activation statement digest、评审时 checkpoint digest、`release_status_max_serial/release_chain_head_digest`、`attestation_status_max_serial/attestation_chain_head_digest`、完整版本向量、Deployment/安装实例与发布通道范围、各 artifact family 的 `security_epoch`、风险接受编号、有效窗口和签署人。P0-10/P0-11 的运维、数据与发布 owner 先完成证据/attestation/activation，再进行产品、架构、安全、测试四方 Go 签字；部署器不能把评审 checkpoint 当作部署时新鲜度证明。任一 profile、适用性、制品、manifest、attestation、schema、`security_epoch`、签名根、吊销状态、候选配置、证据有效期或任一状态链发生变化，验证器都必须拒绝旧决策并恢复为 No-Go；旧受签对象本身保持不可变。

### 20.6 核心指标

指标先冻结口径和数据质量，再讨论目标值。默认在本地生成可见统计；跨设备/上传聚合必须遵循 §17.3 的预览、脱敏和显式授权。所有指标事件至少携带 `metric_schema_version`、`release_profile_id`、`applicability_revision`、App/server 版本、客户端及精确版本、A/B/A+C/B+C、`route_target`、可空 Deployment 类别、结果/reason code 与时间窗口；禁止携带凭据、提示正文、模型响应正文、原始项目路径或可逆用户标识。

| metric_id | 指标与精确定义 | 权威数据源 | owner | 必须分群 | 复盘节奏与用途 |
| --- | --- | --- | --- | --- | --- |
| `M-ACT-001` | 首用验证完成率 = 在入口开始后 24h 内取得同一 active binding 的 `passed + valid` ValidationEvidence 的安装数 / 有效入口开始安装数；跳过不计成功 | App 本地向导事件、RouteBinding、ValidationEvidence | 产品 + 桌面 | 四入口、客户端/版本、profile、失败阶段 | 每周；决定向导、默认 CTA 与兼容优先级 |
| `M-ACT-002` | 配置完成未验证率 = 已 active 但 24h 内无 valid evidence 的 binding / 新 active binding；另列 `user_attested` | App 配置事务与证据库 | 产品 + 测试 | A/B/C、route target、reason code | 每周；定位“看似完成但未闭环” |
| `M-DP-001` | 中转请求成功率 = 执行终态成功 RequestRecord / 所有执行终态 RequestRecord；direct 不混入，另以客户端观察率展示 | 实际执行节点 RequestRecord/Attempt | 网关 + 数据 + 运维 | local/cloud、协议/模型族、版本、错误族 | 日/周；运维告警与容量复盘，不在样本稳定前承诺 SLA |
| `M-CFG-001` | 配置操作收敛率 = `completed|partial_success 后已收敛` operation / 已终结 operation；同时报告 recovery_required 最老年龄和组内部分提交数 | ConfigApplyOperation/AtomicityGroup journal | 桌面 + 架构 | 客户端、适配器、原子组类型、profile | 每构建 + 每周；阻断 P0-8 回归 |
| `M-LEDGER-001` | 账本正确性 = 重复 dispatch、重复结算、`billable_usage_total != attempts[]`、非法双轴转换的计数；目标为零，不以百分比稀释 | 节点账本、对账作业、约束告警 | 数据 + 网关 | Deployment、版本、协议、崩溃点 | 实时告警 + 每候选；任一未解释非零即 No-Go |
| `M-UNKNOWN-001` | unknown 积压 = `query_status=unknown|pending` 数量、最老年龄与收敛时长分位数 | RequestRecord 状态查询与 reconciliation | 网关 + 运维 | 原因、上游、客户端、是否可查询 | 日；决定查询 TTL、人工处置与回退边界 |
| `M-OPS-001` | 就绪与恢复 = 启动到 ready、draining 收敛、隔离恢复实测 RPO/RTO；分别记录成功率与分位数 | readiness、部署事件、恢复演练 manifest | 运维 + 数据 | cloud/local、资产类别、版本 | 每次发布演练 + 月度；冻结 D-DR/D-DRAIN 数值 |
| `M-SEC-001` | 凭据安全 = P0-2/P0-3a 迁移覆盖率、明文残留/秘密金丝雀命中数、旧密文积压、吊销后使用尝试数 | 迁移器、残留扫描、密钥目录、安全测试 | 安全 + 桌面 + 数据 | 凭据族、存储载体、profile | 每候选 + 日常告警；任何真实秘密泄漏阻断发布 |
| `M-AUD-001` | 审计耐久性 = 写失败/丢弃、队列最老年龄、第二审计域写入失败、待补锚量与跨域差异 | 审计 outbox、第二耐久域、锚定/对账任务 | 安全 + 运维 | 风险等级、操作类型、Deployment | 实时告警 + 周复盘；高风险未留痕即事件 |
| `M-REL-001` | 发布证据完整率 = 当前 profile 应有的 R/R*/C/N 证据通过数 / 计划数；另记录 checkpoint 年龄、attestation state、链补齐失败数 | evidence manifest、status chain、checkpoint verifier | 发布工程 + 测试 | Gate、P0/WP、artifact family、profile | 每次候选/部署；G4/G5 二值门禁 |
| `M-UX-001` | 任务恢复率 = 首用/切换/贡献/升级失败后，经界面建议动作最终收敛的 operation / 可恢复失败 operation；并报告无障碍用例通过率 | App 活动中心、恢复 journal、无障碍自动/人工报告 | 产品 + 桌面 + 测试 | 任务、状态、输入方式、缩放/读屏环境 | 每构建 + 每周；驱动 WP-07 和 D-UX-001 |
| `M-COST-001` | 成本可见完整率 = 有权威来源、billing scope、价格版本、snapshot_at/cache_state 标签的成本/用量视图 / 展示视图；跨来源不可比项不得计为完整合计 | cloud/local 聚合快照与 direct 上游适配器观察 | 产品 + 数据 | route target、provider、币种、cache state | 周/月；决定预算与总成本视图是否可开放 |
| `M-LOCAL-001` | 本地自主可用率 = 不依赖本产品云端控制面完成 local RouteBinding 与请求的演练成功次数 / 计划次数；“真正断网”仅在上游也位于本机/LAN 时单列 | local-edge 演练与 App 本地状态 | 运维 + 桌面 | 上游位置、Windows/容器、版本 | 每候选 + 月度；验证本地附加价值 |

指标数据质量必须报告事件缺失率、重复率、时钟偏差、schema 版本覆盖和分群样本量；缺失或样本不足时显示“不可判定”，不得以 0 填充。产品周会看激活/任务闭环/成本可见，工程候选评审看配置/账本/安全/发布，运维日常看数据面/unknown/readiness/审计；任何指标目标值和告警阈值都需对应稳定 decision ID 后才能写成承诺。

## 21. 风险、技术债与待决策清单

当前技术债：

- App 连接秘密仍可能出现在 profiles、`.bak`、官方配置、VS Code 设置、用户/子进程环境和普通备份中。
- 部分组件在测试注入临时服务后仍会创建默认 AppData 路径，组合根隔离不完整。
- 上游账号凭据以明文子字段存于 `accounts.credentials` JSONB，本次审计确认无字段级信封加密（`account_repo.go` 直接 `SET credentials = $1::jsonb`）。
- 现有部署模板仍使用浮动 `:latest`，端口映射和服务监听默认采用 `0.0.0.0`；尚未形成生产镜像 digest 固定、默认最小暴露面和环境分层 manifest。
- `/health` 固定返回 `200 {"status":"ok"}`，容器健康检查直接依赖该端点且不检查数据库、Redis 或排空状态；当前缺少 live、readiness、draining 分离及流量摘除门禁。
- JWT 签名密钥当前以原值持久化到业务库 `security_secrets.value` 普通 `text` 字段，数据库既有值优先，可能随业务库备份扩散。通用 AES `SecretEncryptor` 仍直接使用 TOTP encryption key，并复用于 TOTP secret、备份 S3 SecretAccessKey 和渠道监控 API Key。支付恢复令牌已支持独立 `PAYMENT_RESUME_SIGNING_KEY`：显式配置后新签名使用独立键，但验证仍兼容 TOTP 派生旧键；未配置时仍使用旧键签名。各用途仍缺 `key_id/version`、独立轮换、遗留材料迁移、可验证吊销及外部托管。
- 支付 provider 新配置当前直接写入明文 JSON，TOTP 派生 AES 只保留为历史密文读取兼容；其中包含 private key、secret key、API v3 key 和 webhook secret 等敏感字段。生产化前需改为用途独立的字段级信封加密，并迁移、扫描和清理既有明文记录。
- 内置 `BackupService` 只备份白名单系统配置，明确排除用户、账号凭据、贡献数据和用量；原完整数据管理 agent 已废弃，当前不构成 Deployment 完整灾备，归档也缺少 digest、MAC、签名、key version 与隔离恢复证明。
- 管理审计队列满时会丢弃记录，批量写失败后会清空失败批次且不重试，`droppedCount`/`writeFailed` 未进入 health 或 readiness；系统仍允许 TOTP 后执行 `TRUNCATE TABLE audit_logs`，清空与留痕不在同一原子事务且没有外部不可变锚，不满足高风险操作 fail-closed 与不可变审计要求。
- App 的 `Sub2ApiEndpointSelection` 仍接受 RFC1918 等非 loopback HTTP，并允许 Cloud Profile 的公网 HTTP 数据路径；该行为与“只有真实 loopback 可使用 HTTP”的当前基线冲突，属于 P0-5 迁移债。
- `sub2api/backend/internal/repository/proxy_probe_service.go` 仍按顺序访问 `http://ip-api.com/json/?lang=zh-CN` 与 `http://httpbin.org/ip`；共享 HTTP 客户端只提供解析 IP、私网地址、超时和响应大小等防护，没有禁止公网明文 HTTP。该实现与“HTTP 只允许真实 loopback”及“明文公网探测被拒绝”的安全基线冲突。代理出口、上游连通性、健康与计费等所有出站探测必须共用逐跳 URL 安全策略；生产改用 HTTPS 或受认证的自托管 HTTPS 探测服务，任何非真实 loopback HTTP（含重定向后）一律拒绝。
- `/v1/usage` 存在 Key 级字段过度暴露，贡献公开房间视图也存在字段过宽。
- 当前 Cloud Profile 不能可靠区分云中转与 direct 上游，迁移前必须按 `legacy-cloud-ambiguous` 处理。
- 贡献凭据接收与账号创建路径当前已经存在：认证用户接口可提交 API Key、OAuth session、OpenAI token/code、mobile refresh token 或 Codex PAT，并创建包含凭据的 Account；上游凭据仍以明文 JSONB 子字段保存。通用 backend mode 不能替代贡献专用服务端闸门；在 `contribution_credentials_enabled=false` 服务端拒绝、信封加密、迁移、轮换、删除及备份闭环通过前，生产基线为 No-Go。
- 现有 `request_id` 接近单次网关请求，计费登记发生在 dispatch 后，`usage_logs`/`usage_billing_dedup` 的 `(request_id, api_key_id)` 唯一约束也无法直接承载一个 RequestRecord 下多个 attempt；RequestRecord、RequestAttempt、dispatch lease、稳定幂等键、结果未知查询、RequestLink 和追加式补偿事件尚未落地。
- Sub2API 主发布配置只提供 SHA-256 checksum，简化 GoReleaser 配置关闭 checksum，发布工作流未见签名、SBOM 或 provenance；WPF 脚本只完成 ZIP 打包与烟测，App 更新器依赖 HTTPS 和清单内 SHA-256，仍缺独立签名信任根、吊销和可信防降级链。

以下项目保持【待决策】。已经固定的安全基线不再列为选项：HTTP 只允许真实 loopback，LAN 与公网一律要求 TLS。

下表列的“决策阶段”即**最晚决策里程碑**（迟于该里程碑未定案，对应 Epic 不得进入完成）；“谨慎推荐”即**默认暂行口径**（决策人未拍板前按此执行）；“决策人”即建议 owner。每项的**受影响章节**见表后映射。

| decision_id | 记录状态 | 待决策项 | 主要选项 | 阻断 Epic/Gate | 最晚里程碑 | 默认暂行口径 | 单一 DRI | Approver |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `D-REL-001` | open | 首发 release profile | CLOUD；HYBRID；LOCAL-EDGE；DIRECT；分批发布 | G0、全部首发 P0 | 候选实现分支冻结前 | 先评审 CLOUD 与 LOCAL-EDGE 两个独立封套；任何 profile 未签字都不对外宣称首发 | 产品负责人 | 架构、安全、测试、运维 |
| `D-ROUTE-001` | open | direct 路径支持边界 | 仅旁路；RouteBinding+观察；代理回退 | P0-4、P1-2 | 契约 v1 字段冻结前 | 显式 RouteBinding/来源标记；不进本产品权威账本，不承诺逐请求回退 | 架构负责人 | 产品负责人 |
| `D-TRUST-001` | open | LAN TLS 首次信任 | 指纹/TOFU；本地 CA；mTLS | P0-5 | P0-5 设计冻结前 | 单人指纹确认；多人/企业 mTLS；私网地址不构成信任 | 安全负责人 | 架构、运维 |
| `D-TLS-001` | open | 证书签发/轮换/吊销参数 | 内建 CA；企业 CA；托管 PKI | P0-5 | P0-5 实现设计冻结前 | §16.6 协议基线不变，运营参数写入可测试 runbook | 运维负责人 | 安全负责人 |
| `D-CONTRACT-001` | open | 能力契约兼容与弃用窗口 | 时间窗；版本数；存活率动态 | P0-4 | 契约 v1 发布前 | 至少保留上一受支持主版本并用 `deprecation` 预告 | 架构负责人 | 产品、桌面、网关 |
| `D-COMPAT-001` | open | 支持的官方 CLI 版本 | 最新；明确区间；LTS | P0-9 | 首轮兼容矩阵冻结前 | 发布精确区间与测试日期，超范围显式提示 | 测试负责人 | 桌面负责人 |
| `D-UX-001` | open | 默认链路、四入口与用户可编辑范围 | 外部 A/托管 B；自动选择；高级全开 | G0、P0-9、WP-07 | 向导与兼容矩阵冻结前 | 外部 CLI 默认 A、App 托管默认 B；C 只按协议需要或显式启用 | 产品负责人 | 桌面、测试、架构 |
| `D-A11Y-001` | open | 桌面无障碍验收基线 | 控件默认；Windows 专项；WCAG 2.2 AA | P0-9 | 核心页面设计系统冻结前 | Windows 高对比度、200%、键盘、Narrator/NVDA、焦点/live region 为 P0 | 桌面负责人 | 产品、测试 |
| `D-CRED-001` | open | 贡献凭据形态 | 专用可撤销；转移既有 | P0-3b、P1-6 | 真实贡献托管开放前 | 默认专用凭据；转移只由用户显式选择 | 产品负责人 | 安全、法务 |
| `D-CRED-002` | open | 转移无法收敛的补偿 | 重试停来源；撤目标；人工采用一侧 | P0-3b、P1-6 | 转移能力开放前 | 保持 `manual_action_required`，优先重试停来源，人工操作需 step-up | 安全负责人 | 产品、运维 |
| `D-CRED-003` | open | 撤回删除与备份 SLA | 主存储即时；备份分层延迟；固定墓碑 | P0-3b | 贡献托管上线前 | 先停调度并删除主副本；冻结可核验备份删除上限与审计保留 | 数据负责人 | 安全、法务、运维 |
| `D-BILL-001` | open | 流式中断计费 | 已测量 Token；不计费；按原因补偿 | P0-6 | 结算状态机冻结前 | 按可验证已测量用量记账，补偿只追加冲正 | 数据负责人 | 产品、财务 |
| `D-LEDGER-001` | open | 幂等与状态查询规格 | TTL、fingerprint、lease/fence/intent、保留期 | P0-6 | 契约与 DDL 冻结前 | Deployment+ledger domain+key 原子登记；intent 后只查询收敛 | 架构负责人 | 数据、测试 |
| `D-CONFIG-001` | open | 配置事务工程规格 | 适配器资格、journal/锁、Head commit、runtime activation | P0-8 | Schema 与恢复实现冻结前 | 仅可恢复资源进原子组；多 Head 同事务；冲突整组恢复 | 桌面负责人 | 架构、测试 |
| `D-OBS-001` | open | 日志保留与遥测 | 全本地；匿名聚合；用户诊断上传 | P0-7、P1-5 | 统一诊断冻结前 | 默认本地不上传；上传需预览、脱敏和显式授权 | 产品负责人 | 安全、法务 |
| `D-OPS-001` | open | local-edge 分发与升级 | Windows 原生；Compose；并行；App 托管/独立 | G0、P2-4 | LOCAL-EDGE/HYBRID profile 冻结前 | Windows 原生优先，Compose 回退；签名、单调版本、数据卷不覆盖 | 运维负责人 | 架构、桌面、安全 |
| `D-DR-001` | open | Deployment 灾备目标 | 全局；按端分级；按数据类分级 | P0-10 | 灾备方案冻结前 | 按 Deployment 与资产类别冻结 RPO/RTO 和演练频率 | 运维负责人 | 数据、产品、安全 |
| `D-DRAIN-001` | open | 排空与强制终止时限 | 固定；请求类型分层；动态收敛 | P0-10、P0-11 | 升级流程冻结前 | 普通/流式/unknown/待结算分层；未知执行不得静默重试 | 运维负责人 | 架构、数据 |
| `D-KEY-001` | open | 根密钥托管与轮换 | OS 安全区；KMS/HSM；企业托管 | P0-3a、P0-10、P0-11 | 密钥目录与签名根冻结前 | 本地 OS 安全区、云端 KMS/HSM；用途/owner 分离、双版本解密 | 安全负责人 | 运维、发布工程 |
| `D-REL-002` | open | 可信更新与发布证据规格 | trust backend、状态链/witness、保留期 | P0-11、G4、G5 | P0-11a Schema 冻结前 | 保持 §19.4 release/attestation 两条独立状态链、issued/active、nonce checkpoint 与 fail closed 基线 | 发布工程负责人 | 架构、安全、运维 |
| `D-AUDIT-001` | open | 审计保留与不可变锚 | 固定期；风险分层；监管驱动 | P0-10 | 审计契约冻结前 | 高风险独立保留并锚定 WORM/签名存储；清理写追加墓碑 | 安全负责人 | 法务、运维 |
| `D-COST-001` | open | 回退产生费用的责任 | 用户；平台；按原因分摊 | P0-6、P1-4 | 预算/商业结算冻结前 | 先固定 attempt 技术事实，界面如实展示失败成本 | 产品负责人 | 财务、数据 |
| `D-COST-002` | open | 跨来源总成本视图 | 并列；汇率估算；账单周期对账 | P1-4、P2-5 | 成本视图冻结前 | 默认按来源并列；合计必须标注币种、汇率、版本、估算和不可比项 | 产品负责人 | 数据、财务 |
| `D-CONTRIB-001` | open | 房间/账号唯一约束 | 保持；分阶段放开；直接放开 | P1-6 | 市场扩展评估后 | 先保持现有唯一约束，以治理数据决定放开 | 产品负责人 | 数据、运营 |
| `D-CONTRIB-002` | open | 贡献治理 step-up | 管理员审计；MFA；双人审批 | P1-6 | 贡献治理开放前 | 删除/批量/导出至少再认证与理由，高影响评估双人审批 | 安全负责人 | 运营、产品 |
| `D-LEGAL-001` | open | 商业许可证与服务条款 | 上线前专项；后补 | 商业化前置 | 商业化发布前 | 上线前完成上游许可证、授权提示、贡献条款和模型条款核验 | 法务负责人 | 产品负责人 |
| `D-SLO-001` | open | 性能与成功率目标 | 立即 SLA；灰度后冻结 | 非功能基线后置 | 有稳定样本后 | 先按 §20.6 定义采样和数据质量，暂不承诺数值 | 产品负责人 | 运维、数据 |

受影响章节映射（每项定案后需回改的章节）：

| decision_id | 待决策项 | 受影响章节 |
| --- | --- | --- |
| `D-REL-001` | 首发 release profile | §5.2、§19.4、§20.5、§21.1、§22 |
| `D-ROUTE-001` | direct 路径支持边界 | §9、§11.1.1、§11.3、§13.3、§17.1 |
| `D-TRUST-001` | LAN TLS 信任模型 | §11.1.1、§16.1.1、§16.2 |
| `D-TLS-001` | TLS 与证书工程基线运营参数 | §16.1.1、§16.2、§17.4 |
| `D-CONTRACT-001` | 能力契约兼容与弃用窗口 | §13.4、§13.5、§19.4、§20.4 |
| `D-COMPAT-001` | 支持的官方 CLI 版本 | §8.5、§20.4、P1-8 |
| `D-UX-001` | 各客户端默认链路与用户可编辑范围 | §9、§18.5.1、§18.6.1、§20.4 |
| `D-A11Y-001` | 桌面端无障碍验收基线 | §18.7、§20.3、§20.4 |
| `D-CRED-001` | 贡献凭据形态 | §10.3–§10.5、§15.1、§15.4 |
| `D-CRED-002` | 显式转移无法收敛时的补偿 | §15.1、§15.4、§17.7 |
| `D-CRED-003` | 凭据撤回删除与备份 SLA | §15.1、§15.4、§17.6 |
| `D-BILL-001` | 流式中断计费 | §14、§17.9 |
| `D-LEDGER-001` | 幂等与状态查询工程规格 | §13.3、§13.6、§14 |
| `D-CONFIG-001` | 配置事务工程规格 | §11.4、§12、§18.6、§20.3 |
| `D-OBS-001` | 日志保留与遥测 | §16.2、§17.2、§17.3 |
| `D-OPS-001` | 本地中转分发与升级 | §7.1、§17.4、§17.6 |
| `D-DR-001` | Deployment 灾备目标 | §17.6 |
| `D-DRAIN-001` | 排空与强制终止时限 | §7.2、§17.4 |
| `D-KEY-001` | 根密钥托管与轮换责任 | §16.3、§19.4、§20.3、§20.5 |
| `D-REL-002` | 可信更新状态与发布证据工程规格 | §17.6、§19.4、§20.3、§20.5、§22 |
| `D-AUDIT-001` | 审计保留与不可变锚 | §16.2、§16.4 |
| `D-COST-001` | 回退产生费用的责任 | §14、§17.9 |
| `D-COST-002` | 跨来源总成本视图 | §17.1、§17.9 |
| `D-CONTRIB-001` | 一用户一房间/一账号一房间 | §11.5、§15.2 |
| `D-CONTRIB-002` | 贡献治理 step-up | §15.3、§16.2 |
| `D-LEGAL-001` | 商业化许可证与服务条款 | §16.2、§21 |
| `D-SLO-001` | 性能与成功率目标 | §17.5、§20.5、§20.6 |

“边缘账本接入云端结算下沉 P2”已在 §14 与 P2-5 定案，不属于待决策项；其关联章节保留在 ADR-016 中追溯。

### 21.1 决策冻结与回写协议

每个正式决策使用稳定 `decision_id`，并且只能有一个单一 DRI；记录字段至少包括：`record_status`、DRI、approver 集合、决策日期、最晚里程碑、最终选择、被否决方案及理由、不可变约束、受影响章节/Epic/契约/schema/兼容矩阵、迁移与回滚方案、二值验收证据、重访条件、取代关系、关联 `release_profile_id/applicability_revision` 和当前 profile 适用性。表中的“默认暂行口径”只用于阻止实现漂移，不能冒充已批准决策，也不能据此把【待决策】改为【已确认设计】。

决策形成或被取代时，必须在同一次变更中回写第 21 节决策项、第 22 节决策摘要、ADR 索引及所有受影响章节；涉及 API、状态机、数据结构或兼容性的决策还要同步更新 OpenAPI/JSON Schema、数据库迁移设计、第 20.3 节验收行与第 20.4 节兼容矩阵。任一必要回写缺失时，该决策记录保持未冻结，对应 Epic 不得进入完成。

最终审批人以表中 Approver 为准，DRI 负责在截止里程碑前收集证据并发起回写。带“阻断 Epic/Gate”的项目必须在对应 Epic 或 Gate 冻结前形成记录，否则不得进入完成状态；跨报告的事实综合与红旗汇总由产品负责人牵头。决策表、受影响章节、ADR、契约/schema、20.3 验收行、20.5 适用性记录和 22 节摘要必须在同一变更中更新。

### 21.2 待决事项与 release profile 交叉表

下表用于明早逐项判断“该 profile 是否被该决策阻断”。`R-B`=该 profile 正向 Required，决策未定即阻断；`R*-B`=桌面/子集 Required，子集决策未定即阻断；`D-B`=表面首发 Disabled，必须完成关闭闸门和负向证据后才可继续；`N`=profile 不发布该表面；`P`=后置增强，不阻断当前 profile，但不得写成首发承诺。该表只表达发布适用性，最终决策仍以 21.1 的完整登记为准。

| decision_id | 决策主题 | CLOUD | HYBRID | LOCAL-EDGE | DIRECT | 未决时的门禁动作 |
| --- | --- | --- | --- | --- | --- | --- |
| `D-REL-001` | 首发 profile 选择 | R-B | R-B | R-B | R-B | G0 保持 No-Go，未批准 profile 不生成候选 |
| `D-UX-001` | 默认链路/入口/可编辑范围 | R-B | R-B | R-B | R*-B | WP-07 不得关闭，四条路径文案与失败态保持未冻结 |
| `D-OPS-001` | local-edge 分发与升级 | N | R-B | R-B | N | HYBRID/EDGE 不得进入 G2/G4；CLOUD/DIRECT 不宣称 local-edge 支持 |
| `D-CRED-001` | 贡献凭据模式 | D-B | R-B | D-B | N | CLOUD/EDGE 服务端关闭并出负向证据；HYBRID 未定不得开放正向贡献 |
| `D-CRED-002` | 转移失败补偿 | D-B | R-B | D-B | N | 只能显示 `manual_action_required`，不得显示转移完成 |
| `D-CRED-003` | 撤回/备份删除 SLA | D-B | R-B | D-B | N | 贡献托管不能进入 active；主存储/备份对账证据缺失即 No-Go |
| `D-ROUTE-001` | direct 支持边界 | R-B | R-B | R-B | R*-B | P0-4/首用映射未定，不得自动选择 direct 或声明回退 |
| `D-TRUST-001` / `D-TLS-001` | LAN/TLS 运营参数 | R-B | R-B | R-B | R*-B | 只允许 §16.6 已冻结协议基线；具体信任/证书参数未定保持 No-Go |
| `D-CONTRACT-001` / `D-COMPAT-001` | 契约与 CLI 兼容窗口 | R-B | R-B | R-B | R*-B | G1/G3 不通过；超范围客户端只能显示不支持 |
| `D-LEDGER-001` / `D-BILL-001` | 幂等/未知/流式计费 | R-B | R-B | R-B | N | 节点 profile 缺证据不得放行；DIRECT 不发布本产品账本表面 |
| `D-CONFIG-001` | 配置事务与恢复 | R-B | R-B | R-B | R-B | G2 阻断；只允许旧配置保持或 recovery_required |
| `D-DR-001` / `D-DRAIN-001` | 灾备与排空时限 | R-B | R-B | R-B | R*-B | 没有按数据类别的实测证据不得进入 G4 |
| `D-KEY-001` / `D-AUDIT-001` | 根密钥与审计锚 | R-B | R-B | R-B | R*-B | 任何用途复用、审计丢失或不可变锚缺口保持 No-Go |
| `D-REL-002` | 可信更新/证据规格 | R-B | R-B | R-B | R*-B | G4/G5 不能跳过 issued/active、checkpoint 或 trust-state 校验 |
| `D-SLO-001` / `D-COST-001` / `D-COST-002` | 性能与商业口径 | P | P | P | P | 先按指标字典观测；不得把未定数值写入 SLA 或营销承诺 |

profile 交叉表每次随 `applicability_revision` 升级；若某单元由 `C` 改为 `R` 或由 `N` 改为发布表面，必须生成新候选 fingerprint、重新走 G0→G5，并同步更新 20.5.1 证据计划。

## 22. 决策记录摘要

以下为已固定的关键决策（【已确认设计】口径）：

1. App 是主要交互与编排控制面，API Key 用户仍可脱离 App 直接调用中转数据面；云端登录只影响管理面能力。
2. 本地中转是 App 的可选附加价值，不是 App 前置条件。
3. A 配置驱动与 B 托管交互是二选一主模式，C 协议桥/本地代理是可叠加层，支持组合固定为 A、B、A+C、B+C。B 管理交互事件但实际模型请求仍由 CLI 发出；只有 C 的 App 侧代理组件进入逐请求数据面，经 local/cloud 时中转节点本身也属于数据面。
4. RouteBinding 是 App 控制面的绑定事实；RequestRecord 是执行节点入口的逻辑请求、幂等与状态查询事实；RequestAttempt 是该记录下每次真实上游尝试及账本事实。三者不能互相冒充。
5. 未叠加 C 的 A/B 路径不承诺逐请求跨端点自动回退，当前通过切换 RouteBinding 后重连、重启或重发；叠加 C 的路径或 CLI 原生能力才具备逐请求回退前提。
6. App 只选择 `route_target` 与 TargetEndpoint；节点在内部按 Group/Account（`account_groups` 多对多）完成账号选路，上游协议/端点按 `account.Platform + account.Type` 分支判定，Channel 只承担分组聚合与定价，App 不直接选择账号或读取上游凭据。
7. 云端与本地可共享代码和版本化能力契约，不共享数据库、Redis、日志、备份、加密根密钥、产品级平台身份、Deployment 范围身份、OAuth 凭据、账号池或对话正文。
8. 实际执行节点掌握请求级原始用量事实；direct 的最终费用与官方用量以上游服务商为权威，App 只保存观察值或估算值。
9. 账本以 RequestRecord 承载逻辑请求、域内幂等、dispatch 占有与状态查询，以 RequestAttempt 承载每次真实上游尝试和计费事实：同一 Deployment 内的同一 `ledger_domain_id` 以幂等键 hash 唯一定位 RequestRecord，指纹只用于同键冲突校验；lease claim 单调递增 fence，只有当前 owner/fence 能把无 intent 的 `registered` attempt CAS 为 `dispatching` 后发网，intent 后崩溃不得自动重发；attempt 执行/结算双轴单向终结。无可计费用量进入 void，补偿采用追加事件；跨 endpoint/Deployment 重发创建新的 RequestRecord 并用 RequestLink 追溯，系统不承诺 global exactly-once。
10. 自动回退必须同时满足用户授权、模型/协议/账号族与工具语义等价、费用边界和数据敏感级别；任一属性变化都需提示确认。结果未知时先查询 RequestRecord 的请求级聚合状态；无法证明原请求未执行则不自动跨节点重试。
11. 显式贡献是禁止自动同步的有意例外，由目标 Sub2API Web/API 接收凭据；目标为云端时才跨越云边安全域。P0-3 拆为所有含中转 profile 必须通过的 P0-3a 上游凭据保护，以及按 profile 为 R/C/N 的 P0-3b 贡献托管。桌面 App 一键贡献属于目标设计。贡献始终经 TLS，服务端默认 `contribution_credentials_enabled=false`；只有适用闸门通过并签字后才可启用。专用凭据可直接激活；显式转移采用 `target_ready → source_stop_pending → source_stopped → active` 协调流程，来源停止未确认时不得显示完成。
12. HTTP 只允许真实 loopback；LAN 和公网必须使用 TLS，局域网信任通过证书指纹、本地 CA 或 mTLS 建立，不能把私网地址当作可信依据。
13. 本地数据库、连接、项目、偏好与本地编排状态属于本地权威，远端恢复和云端策略不得静默覆盖；云端策略只能作为建议或版本化约束进入 App。
14. 路由目标统一为 TargetEndpoint，用 `owner_kind=deployment|upstream_provider` 表达归属：`endpoint_id` 为全局唯一 UUID，逻辑主表、不可变 revision 与 TargetEndpointHead 分离；local/cloud 还必须与认证 `deployment_role` 一致，direct 由 `installation_id` 签发且 `deployment_id` 为空。Deployment 端 EndpointCandidate 只有经认证的 nonce-bound EndpointDescriptor 与当前传输、主体、入口映射核验通过后才能签发，历史/跨主体响应不能重放；认证前候选不得 active。RouteBinding revision 主键为 `(binding_id, revision)`，使用规范 `scope_key` 与 RouteBindingHead/ETag CAS；EndpointHead 的 current 与 Binding 的 active 含义分离，Head 推进不自动换绑。
15. 身份分四族系（Deployment 用户会话、本机管理员 x-api-key、数据面 API Key、上游提供商凭据），issuer/audience 互不混同。local-edge 与云端同一份服务，同时具备管理员与用户会话及数据面 Key，不存在独立“Deployment JWT”类型。登出本地必清、远端 best-effort 撤销，需区分“已撤销”与“撤销未确认”双状态。
16. P0/P1 云端结算只依赖云端权威账本；边缘账本接入云端结算下沉 P2，须先落地签名 `EdgeSettlementReceipt` 及对账/吊销/争议流程。
17. direct 是绕过本产品中转的旁路 `route_target`，与由 local/cloud 可用性决定的 `user_mode` 正交；无中转可用时派生 `user_mode=none` 并给出 `user_mode_reason`，direct-only 唯一表达为 `user_mode=none + user_mode_reason=no_enabled_relay + route_target=direct`。`none` 不构成第四种中转模式，最终用量与账单以上游为权威，健康变化不得静默改写 active RouteBinding。
18. 配置应用采用 `ConfigApplyOperation → AtomicityGroup → ConfigTransaction` 三层结构。原子组只对具有可验证恢复语义的资源提供逻辑全有或全无，不宣称跨文件/安全存储/进程瞬时 ACID；同组全部 `RouteBindingHead` CAS 集合、新旧 revision 生命周期、group/transaction committed 在同一元数据 commit point 成立，条件更新行数必须等于应切换的不同 scope 数，任一 Head 冲突使零个 Head 推进并先恢复外部资源；commit 后进程激活使用独立 runtime 状态。独立组可分别提交并形成 `partial_success`，取消、拒绝、冲突和恢复要求均有闭合状态。
19. 可信发布按 `UpdateTrustScope=(target_kind,target_id,release_authority_id,artifact_family)` 在受保护 trust state 保存 `security_epoch` 下限，并按 `(release_authority_id, release_id)` 分别保存 release 链和当前 attestation 链的最高已验证 serial/head、当前 attestation 与 checkpoint digest；首次受签 bootstrap、既有目标 trust-state 丢失和受签 recovery 明确分流。普通降级拒绝；rollback manifest 带唯一 serial，桌面绑定 `installation_id`、服务端绑定 `deployment_id`，并受 digest、版本向量、有效期、吊销快照、恢复点与迁移可逆性约束。Go 前必须验证 attestation `issued → active` 并取得同时承诺两条链的 activation 后 nonce-bound checkpoint，部署前再次 challenge；任何恢复都不能降低 epoch 或任一 status floor。
20. 首次使用提供云端登录、已有中转数据面 Key、本地自主和 direct-only 四条并列入口；首个合成请求可能真实计费，执行前显示目标、模型、费用与隐私边界。RouteBinding active、ValidationEvidence 和来源确认是三个独立状态：跳过请求为“配置已应用、请求尚未验证”；未经 C 的 A 只有受监督且契约化成功信号可机器验证通过，节点回执只提升来源确认级别。
21. 贡献凭据显式转移是跨 Deployment 协调流程，不是原子事务；`source_stop_pending` 无法确认时进入 `manual_action_required`，界面提示双副本调度风险，补偿动作按第 21 节另行冻结。
22. 每个候选发布绑定不可变 `release_profile_id`、`applicability_revision` 与 R/R*/C/N 证据计划；版本向量和 candidate fingerprint 同时覆盖两者。受签 `release_evidence_manifest` 只包含同一候选的 P0-1、P0-2、P0-3a、P0-3b、P0-4～P0-10 与 P0-11a 及适用性证据；P0-11b 由只引用 manifest 的独立 `p0_11_detached_attestation` 完成，manifest 不反向引用。release 与 attestation 状态分别维护两条带 prev digest 的追加链，attestation 链固定经过 `issued → active`，Go 绑定 manifest、active attestation、activation statement、两链 head 与 activation 后 checkpoint，部署前再次取得 nonce-bound checkpoint。任一状态后缀截断、重放、权威不可达、profile/候选或信任材料变化均立即 No-Go。
23. 发布按 G0 范围冻结、G1 契约/安全止血、G2 核心数据与恢复、G3 UX/兼容、G4 候选证据、G5 attestation 激活与四方 Go 顺序推进。WP-07 负责 profile 封套、四条首用路径、操作责任、指标字典、决策治理和交付交接；所有正式决策使用稳定 ID、单一 DRI 与具名 Approver，并同次回写第 21、22 节、ADR、契约/schema 和验收矩阵。

### 22.1 ADR 索引

当前 ADR 索引以本文对应章节为权威。独立 `docs/adr/` 文件尚不存在；创建、复核并建立双向链接前，不得把独立 ADR 文件引用为现有资产，也不得用草案覆盖本文已确认决策。

| ADR | 主题 | 本文权威章节 | 状态 |
| --- | --- | --- | --- |
| ADR-001 | App 控制面与三端职责 | §7、§8 | 索引已建立；独立文件待创建 |
| ADR-002 | A/B 主模式与 C 叠加组合 | §9 | 索引已建立；独立文件待创建 |
| ADR-003 | RouteBinding、RequestRecord 与 RequestAttempt 事实边界 | §11.4、§13.3、§13.6、§14 | 索引已建立；独立文件待创建 |
| ADR-004 | 无 C 时不提供逐请求跨端点回退 | §9.2、§12.2 | 索引已建立；独立文件待创建 |
| ADR-005 | App 选择 endpoint、节点调度内部账号 | §8.4、§11.3、§13.3 | 索引已建立；独立文件待创建 |
| ADR-006 | Deployment 数据、密钥与本地权威隔离 | §10.2–§10.5、§16.3 | 索引已建立；独立文件待创建 |
| ADR-007 | RequestRecord 域内幂等、dispatch lease/fence/intent、RequestAttempt 双轴终态、RequestLink 与非 global exactly-once | §13.3、§13.6、§14 | 索引已建立；独立文件待创建 |
| ADR-008 | 贡献作为显式托管例外及默认 No-Go | §10.3–§10.5、§15、§16.3 | 索引已建立；独立文件待创建 |
| ADR-009 | 仅 loopback HTTP，LAN/公网 TLS | §9.1、§16.1–§16.2、§17.4 | 索引已建立；独立文件待创建 |
| ADR-010 | 能力契约、版本与弃用窗口 | §13.4–§13.5、§19.4、§20.4 | 索引已建立；独立文件待创建 |
| ADR-011 | live/readiness/draining 生命周期 | §17.4、§20.3 | 索引已建立；独立文件待创建 |
| ADR-012 | 分类备份、完整灾备与隔离恢复 | §17.6、§20.3 | 索引已建立；独立文件待创建 |
| ADR-013 | 可信发布、UpdateTrustScope、bootstrap/recovery、受保护 epoch/status floor、nonce checkpoint 与授权回滚 | §16.2、§17.6、§19.4、§20.3、§20.5 | 索引已建立；独立文件待创建 |
| ADR-014 | TargetEndpoint 全局 UUID、nonce-bound EndpointDescriptor 签发、不可变 revision、Endpoint/Binding Head CAS | §11.1.1、§11.4、§13.2–§13.5 | 索引已建立；独立文件待创建 |
| ADR-015 | 身份四族系与登出双状态、离线撤销队列 | §10.1、§13.7 | 索引已建立；独立文件待创建 |
| ADR-016 | 边缘账本接入云端结算下沉 P2 与 EdgeSettlementReceipt | §14、§19.3（P2-5） | 索引已建立；独立文件待创建 |
| ADR-017 | direct 与用户模式正交的旁路目标 | §9、§11.1.1、§13.3 | 索引已建立；独立文件待创建 |
| ADR-018 | ConfigApplyOperation 三层逻辑原子性、多 Head 同事务 commit point、冲突恢复与 runtime activation | §12.1–§12.2、§18.6.4、§20.3 | 索引已建立；独立文件待创建 |
| ADR-019 | `user_mode=none\|local\|cloud\|hybrid`、direct-only、首次使用入口与 ValidationEvidence/来源确认分层 | §9、§13.2–§13.5、§18.5–§18.6、§20.3 | 索引已建立；独立文件待创建 |
| ADR-020 | 贡献凭据转移采用跨 Deployment 协调状态机 | §15.1、§15.4、§21 | 索引已建立；独立文件待创建 |
| ADR-021 | 不可变 evidence manifest、detached P0-11 attestation、hash-chained 状态、nonce checkpoint、Go 失效与单向签署协议 | §19.4、§20.5、§21.1、§22 | 索引已建立；独立文件待创建 |

## 23. 附录：现有资产、关键代码与设计决策摘要

### 23.1 本地 App 关键代码依据

- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/ViewModels/MainWindowViewModel.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/ViewModels/AccountCenterViewModel.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Core/ConnectionProfile.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Services/WorkspaceDataService.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Terminal/CliTerminalCommandFactory.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Infrastructure/AppDataPaths.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Infrastructure/LegacyProfileEditor.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Services/ChatSessionController.cs`
- `tools/manufactor_app/ai-switch-gui/LocalGatewayService.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Services/Sub2ApiSessionManager.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Services/Sub2ApiEndpointSelection.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Services/LocalSub2ApiAccountSessionStore.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Services/Sub2ApiAccountCenterClient.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Services/LegacySwitchCoordinator.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Infrastructure/ProjectWorkspaceProfileService.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Infrastructure/OfficialClientExtensionSynchronizer.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Chat/CodexAppServerEngine.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Chat/ClaudeStreamJsonEngine.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Chat/GeminiAcpEngine.cs`
- `tools/manufactor_app/ai-switch-gui/SwitchService.cs`
- `tools/manufactor_app/ai-switch-gui/ClaudeGptBridgeServer.cs`

App 安全、UX 与发布链补充依据：

- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Services/DesktopShellServices.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Services/LocalGatewayEndpointResolver.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/ViewModels/ConnectionsManagementViewModel.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/ViewModels/GatewayViewModel.cs`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Views/AccountCenterView.xaml`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Views/ConnectionsView.xaml`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Views/GatewayView.xaml`
- `tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/Views/SettingsView.xaml`
- `tools/manufactor_app/ai-switch-gui/scripts/publish-wpf.ps1`
- `tools/manufactor_app/ai-switch-gui/scripts/publish-public-wpf.ps1`

### 23.2 Sub2API 关键代码依据

- `sub2api/backend/internal/server/routes/user.go`
- `sub2api/backend/internal/server/routes/admin.go`
- `sub2api/backend/internal/server/middleware/api_key_auth.go`
- `sub2api/backend/internal/handler/account_contribution_handler.go`
- `sub2api/backend/internal/handler/contribution_room_handler.go`
- `sub2api/backend/internal/handler/contribution_proxy_handler.go`
- `sub2api/backend/internal/handler/admin/account_contribution_governance.go`
- `sub2api/backend/ent/schema/contribution_room.go`
- `sub2api/backend/ent/schema/contribution_room_account.go`
- `sub2api/backend/ent/schema/contribution_account_verification.go`
- `sub2api/backend/ent/schema/user_contribution_room_preference.go`
- `sub2api/backend/ent/schema/account.go`
- `sub2api/backend/ent/schema/api_key.go`
- `sub2api/backend/ent/schema/usage_log.go`
- `sub2api/backend/internal/handler/gateway_handler.go`
- `sub2api/backend/internal/pkg/usagestats/usage_log_types.go`
- `sub2api/backend/internal/server/middleware/client_request_id.go`
- `sub2api/backend/internal/repository/usage_billing_repo.go`

安全、运维、备份、审计与供应链补充依据：

- `sub2api/backend/internal/server/routes/common.go`
- `sub2api/backend/internal/repository/security_secret_bootstrap.go`
- `sub2api/backend/ent/schema/security_secret.go`
- `sub2api/backend/internal/repository/aes_encryptor.go`
- `sub2api/backend/internal/payment/wire.go`
- `sub2api/backend/internal/service/wire.go`
- `sub2api/backend/internal/service/backup_service.go`
- `sub2api/backend/internal/service/data_management_service.go`
- `sub2api/backend/internal/service/audit_log_service.go`
- `sub2api/backend/internal/handler/admin/audit_log_handler.go`
- `sub2api/backend/internal/repository/audit_log_repo.go`
- `sub2api/deploy/docker-compose.yml`
- `sub2api/deploy/docker-compose.standalone.yml`
- `sub2api/deploy/config.example.yaml`
- `sub2api/deploy/Dockerfile`
- `sub2api/.github/workflows/release.yml`
- `sub2api/.goreleaser.yaml`
- `sub2api/.goreleaser.simple.yaml`

### 23.3 设计决策摘要

- 三类协同能力：A 配置驱动与 B 托管交互是二选一主模式，C 协议桥/本地代理是可叠加层；支持组合为 A、B、A+C、B+C。
- `user_mode=none|local|cloud|hybrid` 由 local/cloud 可用条件派生；direct 是与其正交的旁路 `route_target`。direct-only 唯一表达为 `user_mode=none + user_mode_reason=no_enabled_relay + route_target=direct`，不构成第四种中转模式，最终用量与账单以上游为权威。
- 三端角色命名：cloud-control-data-plane、local-edge、desktop-orchestrator（【已确认设计】）。
- 能力契约版本化与适配层是解耦演进的基础（【已确认设计】）。
- TargetEndpoint 以全局唯一 UUID 建立逻辑主表与不可变 revision，记录 Deployment/桌面安装签发者、`owner_kind` 和认证冻结的 `deployment_role`；TargetEndpointHead 通过 ETag CAS 选择 current revision，创建 revision 不自动切换 Head。Deployment 端只有经认证的 nonce-bound EndpointDescriptor 与当前传输、主体及入口映射核验后才能从 EndpointCandidate 签发；历史/跨主体 descriptor 不能重放。local/cloud 必须与认证 `server_role` 一致，direct 的 `deployment_id` 必为空。
- RouteBinding 以 `(binding_id, revision)` 标识不可变 revision，以版本化 canonical tuple 的 SHA-256 生成 `scope_key`，并通过 RouteBindingHead/ETag CAS 选择 current；路由优先级为单次任务 > 项目 > 客户端 > 全局。RequestRecord 记录节点入口的逻辑请求、幂等与查询事实，RequestAttempt 记录真实上游尝试与账本事实，RequestLink 只在具备主体与证据的显式关联下追踪跨记录重发。
- 配置应用采用 ConfigApplyOperation → AtomicityGroup → ConfigTransaction 三层结构；原子组只承诺可崩溃恢复的逻辑全有或全无，不宣称跨文件、安全存储与进程的瞬时 ACID。同组全部 RouteBindingHead CAS 集合、新旧 revision 生命周期以及 group/transaction committed 在同一元数据 commit point 成立；任一 Head 冲突使零个 Head 推进并先恢复外部资源。进程读取新配置由独立 `runtime_activation_state` 表达；独立组可形成 `partial_success`。
- App 选择 `route_target` 与 TargetEndpoint；节点按 Group/Account（`account_groups` 多对多）完成账号选路，上游协议和端点由 `account.Platform + account.Type` 判定，Channel 仅承担分组聚合与定价；App 不直接选择内部账号。
- 谁执行谁记录原始事实；同一 Deployment 内的同一 `ledger_domain_id` 由幂等键唯一定位 RequestRecord，同键异指纹返回冲突。dispatch 由 lease owner 携带单调 `dispatch_fence` 执行，任何发网前必须耐久写入 `dispatch_intent`；intent 后崩溃只能查询和收敛，禁止自动重发，旧 fence 的迟到写入被拒绝。attempt 的执行/结算双轴单向终结，跨 endpoint/Deployment 重发创建新 RequestRecord 并以经验证的 RequestLink 关联。
- 未叠加 C 的 A/B 路径通过切换绑定后重连/重发；A+C、B+C 或 CLI 原生能力才具备逐请求回退前提。自动回退以语义等价、费用/隐私边界和用户授权为前提，结果未知先查询 RequestRecord 的请求级聚合状态。
- 能力可共享，秘密与权威数据强隔离；本地权威不得被远端恢复或云端策略静默覆盖。
- 身份严格分为 Deployment 用户会话、本机管理员 x-api-key、数据面 API Key 和上游提供商凭据四族系；登出本地必清，远端撤销为 best-effort，并区分“已撤销”与“撤销未确认”。
- 显式贡献为有意的凭据托管例外，经 TLS 进入目标 Sub2API Deployment 并受关闭、加密和删除闸门约束；目标为云端时才跨越云边安全域，凭据转移采用跨 Deployment 协调状态机，桌面端一键贡献属于目标设计。
- P0/P1 云端结算只依赖云端权威账本；边缘账本接入云端结算下沉 P2，需先实现签名 EdgeSettlementReceipt 及对账、吊销和争议流程。
- 网络安全基线：HTTP 只允许真实 loopback，LAN/公网强制 TLS，默认 loopback 监听、随机数据面 Token、默认不回退公池、字段最小化（【已确认设计】）。
- RouteBinding active、首次请求 ValidationEvidence 与实际来源确认是三条独立状态轴；未经 C 的 A 只有 App 受监督运行官方 CLI 并取得兼容契约声明的成功信号，才可机器判定验证通过，普通外部 CLI 请求最多记录 `user_attested`。
- 可信更新按 `UpdateTrustScope=(target_kind,target_id,release_authority_id,artifact_family)` 保存 `security_epoch` 安全下限，并按 release 分别保存 release 与 attestation 两条链的最高状态 serial/head 和 checkpoint digest；桌面目标绑定 `installation_id`，服务端绑定 `deployment_id`，首次受签 bootstrap、既有目标 trust state 丢失和受签 recovery 分流。受签 `release_evidence_manifest` 只包含 P0-1、P0-2、P0-3a、P0-3b、P0-4～P0-10 与 P0-11a；P0-11b 使用 manifest 外的 `p0_11_detached_attestation`，Go 同时绑定两者、activation statement 与评审时同时承诺两条链的 nonce-bound checkpoint，部署前再次 challenge。两条状态链各自带 prev digest，任一链截断/重放/权威不可达或候选变化立即恢复 No-Go。

### 23.4 既有产品与原型资产

本节资产均为历史证据或交互参考，属于非规范资产，不自动继承为当前设计要求。本文、版本化能力契约和后续正式 ADR 的结论优先；历史原型不得作为协议、安全、权限或验收依据。

| 资产 | 用途与继承关系 |
| --- | --- |
| `docs/本地中转管理软件-产品技术与UX设计-v1.md` | 本地 App 与本地中转的前身方案，用于追溯 API Key、项目工作台、会话、WPF 与视觉方向。其第 15、742 行允许可信 LAN/RFC1918 HTTP 的结论已经失效；当前只有真实 loopback 可使用 HTTP，LAN/公网必须使用 TLS。 |
| `docs/ui-prototype.html` | 本地 App 的历史交互原型，可参考侧栏、连接状态和工作台布局；其中 `http://192.168.31.247:8080` 等 LAN HTTP 地址只是历史视觉样例，不能作为实现、安全评审或验收依据。 |
| `docs/claude-code-gpt-routing-ui-mockup.html` | Claude Code 使用 GPT 及跨客户端/跨协议路由的历史交互示意，可参考 requested/effective model 的呈现；不定义协议、状态机、权限模型或安全边界。 |

### 23.5 实现状态审计表（本轮高风险状态修订与证据）

本表记录本轮基于本地代码取证对“实现状态”做出的高风险修订，供 Codex 复审逐条核对。所有证据均为本地相对路径与符号名；凡代码无法证明的能力一律不标【已实现】。

| # | 主题 | 本轮修订后口径 | 关键代码证据 | 状态 |
| --- | --- | --- | --- | --- |
| A1 | Channel 定义 | Channel 是聚合多个 Group 的定价配置层（`channels`/`channel_groups`/`channel_model_pricing`），非上游协议适配器 | `sub2api/backend/migrations/081_create_channels.sql`；`sub2api/backend/internal/service/channel.go:45` | 【已实现】（实体存在，定义已更正） |
| A2 | 上游选择方式 | 无“渠道适配注册表”，上游按 `account.Platform + account.Type` 在网关分支判定 | `sub2api/backend/internal/service/gateway_forward.go:101,121`；`account.go:1309,1893` | 【已实现】 |
| A3 | 账号凭据加密 | 凭据以明文子字段存于 `accounts.credentials` JSONB，无字段级信封加密（此前写“未获证实”，现为确证的明文） | `sub2api/backend/ent/schema/account.go:74-81`；`account_repo.go:106-111,619-645` | 【已实现】现状为明文；字段级加密为【已确认设计】 |
| A4 | 计费防重键来源 | 防重键的 `request_id` 首选每请求新生成的 `client_request_id`，`X-Request-ID` 仅次级回退 | `gateway_usage_billing.go:195-207`；`middleware/client_request_id.go:14,26,32`；`middleware/request_logger.go:14,24-27` | 【已实现】 |
| A5 | 防重时机 | 计费/防重登记发生在上游 dispatch 成功之后，不能防止同一逻辑请求重复 dispatch | `gateway_handler.go:524-529`；`usage_billing_repo.go:71-76` | 【已实现】现状；先登记后 dispatch 为【已确认设计】 |
| A6 | 防重唯一键 | `(request_id, api_key_id)` 唯一，`request_fingerprint` 比对但不入键 | `migrations/071_add_usage_billing_dedup.sql:12`；`migrations/027_usage_billing_consistency.sql:32` | 【已实现】 |
| A7 | Deployment/Instance 身份 | 后端无 `deployment_id`/`node_id`/`instance_id`（仅支付渠道有 `provider_instance_id`） | RECON grep：`account.go` 附近仅 `provider_instance_id`；`cmd/server/main.go:79` 仅注释 | 无相关字段（Deployment/Instance 为【已确认设计】） |
| A8 | local-edge 身份面 | 与云端同一份服务，同时暴露 user JWT、admin JWT/admin x-api-key、数据面 API Key；无独立“Deployment JWT”类型 | `sub2api/backend/internal/server/router.go:117-120`；`middleware/admin_auth.go:24,127,200`；`api_key_auth.go:43-62` | 【已实现】 |
| A9 | 匿名面 | 匿名仅 `/health`（`{"status":"ok"}`）与 `/setup/status`，无 capabilities/bootstrap/readiness，不返回节点身份 | `sub2api/backend/internal/server/routes/common.go:12,21` | 【已实现】现状；契约端点为【已确认设计】 |
| A10 | 贡献凭据入口与专用开关 | 认证用户贡献路由已经注册，可接收 API Key、OAuth session、OpenAI token/code、mobile refresh token 与 Codex PAT 并创建 Account；未发现 `contribution_credentials_enabled` 或贡献专用服务端关闭开关，通用 backend-mode 不能替代该闸门 | `server/routes/user.go:87-112`；`handler/account_contribution_handler.go:92-110,154-261,621-675`；`handler/account_contribution_openai_handler.go:16-34,67-153,226-258`；`middleware/backend_mode_guard.go:12-27` | 接收路径【已实现】；强制关闭开关为【已确认设计】，当前基线 No-Go |
| A11 | ClaudeGptBridgeServer | loopback 绑定、单一可配置上游、Anthropic Messages→OpenAI Responses；无请求 ID 传播/跨目标回退/RouteBinding revision | `tools/manufactor_app/ai-switch-gui/ClaudeGptBridgeServer.cs` | 桥本体【已实现】；通用 C/传播/回退为【已确认设计】 |
| A12 | App 端点 HTTP 策略 | `Sub2ApiEndpointSelection` 仍放行非 loopback 的 RFC1918 HTTP | `.../Services/Sub2ApiEndpointSelection.cs:233-240,273-279` | 【已实现】现状（P0-5 迁移债） |
| A13 | 登出语义 | 本地 access/refresh 必定清理；远端 `POST /api/v1/auth/logout` 尽力撤销，失败被吞不阻断本地清理 | App `AccountCenterViewModel.LogoutAsync`、`Sub2ApiAccountCenterClient.LogoutAsync`（RECON3 确认 best-effort） | 【已实现】现状；离线撤销队列/双状态提示为【已确认设计】 |
