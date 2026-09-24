# 明早评审摘要——产品设计说明书 v2.0

> 评审对象：《基于云边协同的AI中转系统——产品设计说明书》v2.0。本文档已经形成可指导后续产品、架构、接口、数据、安全、测试与交付拆解的候选设计基线；当前没有批准唯一首发 profile，也没有与同一候选构建绑定的完整实现证据，**产品、实现与发布结论继续保持 No-Go**。

## 1. 明早先形成的三个结论

| decision_id | 必须回答的问题 | 未形成结论时的影响 |
| --- | --- | --- |
| `D-REL-001` | 首发采用 CLOUD、HYBRID、LOCAL-EDGE、DIRECT 中哪一个 release profile，还是分批发布 | G0 不通过，不生成候选，不对外承诺“首发支持” |
| `D-UX-001` | 四条首次使用入口、各客户端默认 A/B/A+C/B+C 链路及用户可编辑范围 | WP-07 不能关闭，向导、支持声明和兼容矩阵不能冻结 |
| `D-OPS-001` | local-edge 首发采用 Windows 原生、Compose 或并行交付，以及由 App 托管还是独立升级 | HYBRID/LOCAL-EDGE 不能进入 G2/G4；CLOUD/DIRECT 不得宣称包含 local-edge 交付 |

建议先完成 `D-REL-001` 与 `D-UX-001`；若选择 HYBRID/LOCAL-EDGE，再于 G0 同步完成 `D-OPS-001`，CLOUD/DIRECT 对该决策登记 N。随后再讨论 DDL、API、密钥、灾备和可信发布参数。正式结论必须写入主文档 §21，并在同一次变更中回写 §5.2、§19.4、§20.5、§22、ADR、契约/schema 和验收矩阵。

## 2. 四个候选发布封套

| profile | 默认用户动作 | 首发拓扑/链路 | P0-3a 上游凭据保护 | P0-3b 贡献托管 | 当前状态 |
| --- | --- | --- | --- | --- | --- |
| `R-CLOUD-DESKTOP-v1` | 登录云端并完成首个请求 | App + cloud；A/B，C 显式启用 | R | C：关闭闸门与负向证据 | No-Go，待 `D-REL-001` |
| `R-HYBRID-DESKTOP-v1` | 连接云端，再添加本地节点 | App + cloud + Windows local-edge | R | R：正向托管、转移、撤回、删除全链 | No-Go，待 `D-REL-001/D-OPS-001` |
| `R-LOCAL-EDGE-WIN-v1` | 发现本地节点并完成本地请求 | App + Windows local-edge | R | C：关闭闸门与负向证据 | No-Go，待 `D-REL-001/D-OPS-001` |
| `R-DIRECT-DESKTOP-v1` | 直连上游 | App + direct；A，C 可选 | N | N | No-Go，待 `D-REL-001` |

profile 未冻结前，“云端优先”只作为产品探索默认。G0 冻结后，表内 CTA 覆盖通用默认：LOCAL-EDGE 默认发现本地节点，DIRECT 默认直连上游；同一发行渠道若包含多个 profile，必须先显式选择封套，不得静默回落到云端。

适用性固定使用四种符号：`R` 为完整 Required；`R*` 为明确子集 Required，子集内仍阻断发布；`C` 为表面存在但首发 Contained，必须同时证明服务端拒绝、客户端不可达和默认关闭；`N` 为该 profile 不发布该表面，必须由构建清单、路由矩阵和用户文案证明。DIRECT 的 `WP-01*` 只含 direct Endpoint/RouteBinding/capabilities/CanonicalError，`WP-03*` 只含桌面出站 TLS/SSRF/凭据隔离，`WP-05*` 只含桌面密钥/更新/诊断/事件响应，`WP-06*` 只含 desktop artifact family 与桌面 UpdateTrustScope；四个子集及排除面都必须进入 evidence manifest。任何适用性变化都提升 `applicability_revision`，生成新候选 fingerprint，并重新走 G0→G5。

## 3. v2.0 已收敛的设计基线

| 主题 | 已收敛内容 | 主文档 |
| --- | --- | --- |
| 三端边界 | App 负责编排和体验；cloud 负责产品级身份、云服务、云端财务权威和运营；local-edge 负责边缘执行、私有账号池和本地自主 | §7、§8 |
| 链路模型 | A 配置驱动、B 托管交互二选一；C 为可叠加数据面代理；固定支持 A、B、A+C、B+C | §2、§9、§18.5.1 |
| 路由事实 | RouteBinding 表达控制面绑定，RequestRecord 表达逻辑请求和幂等事实，RequestAttempt 表达真实尝试与计费事实 | §11–§14 |
| 身份与令牌 | 区分产品云会话、Deployment access/refresh JWT、数据面 API Key、本地管理员授权和上游凭据，禁止以“平台 Token”混称 | §6.2、§10.1、§10.5、§13.7 |
| 操作权威 | 增加三方 RACI，逐项说明配置、执行、账本、凭据、贡献、恢复、发布和事件响应的 R/A/C/I | §8.6 |
| 凭据安全 | P0-3 拆为 P0-3a 上游/中转凭据加密迁移与 P0-3b 贡献托管、转移、撤回和删除 | §5.2、§15.1、§19、§20.5 |
| 贡献转移 | 现有凭据转移必须经过 `target_ready → source_stop_pending → source_stopped → active`；来源未停不能显示完成 | §15.1、§15.4 |
| 产品交付 | 新增 WP-07，覆盖 release profile、四条首用路径、三上下文 UX、责任矩阵、指标字典、决策回写和交付检查表 | §18.5.1、§19.4、§20.6 |
| 发布治理 | 新增 G0～G5 顺序门禁，候选绑定不可变 `release_profile_id` 与 `applicability_revision` | §5.2、§19.4、§20.5 |
| 决策治理 | 每项待决策使用稳定 ID、`open` 状态、单一 DRI、具名 Approver、最晚里程碑和 profile 阻断矩阵 | §21 |

## 4. G0→G5 评审门禁

| Gate | 明早关注点 | 退出条件摘要 |
| --- | --- | --- |
| G0 范围冻结 | `D-REL-001`、`D-UX-001`；HYBRID/LOCAL-EDGE 另需 `D-OPS-001` | profile、persona、默认 CTA、拓扑、A/B/C、R/R*/C/N、适用决策、DRI、审批人与证据计划全部具名；关闭前不生成候选 |
| G1 契约与安全止血 | P0-1、P0-3a、P0-4、P0-5；P0-3b 按 R/C 分流 | P0-3b=R 完成保管、权限和服务端强制边界；C 完成服务端拒绝、客户端不可达和默认关闭；其余安全边界形成二值证据 |
| G2 核心数据与恢复 | P0-2、P0-6、P0-8、P0-10 设计；P0-3b=R 数据闭环 | 配置事务、幂等/intent、账本迁移、密钥/审计和恢复能力通过；贡献转移、撤回、删除与备份对账闭环 |
| G3 UX 与兼容 | P0-7、P0-9、WP-07；P0-3b=R 用户旅程 | 四条首用路径、贡献授权/条款/失败恢复、无障碍、支持声明、兼容矩阵和指标事件无空白 |
| G4 候选与证据 | 已关闭的 G0～G3、P0-11a、P0-10 候选演练 | G4 开始才生成并冻结制品/版本/profile/schema/fingerprint；P0-10 演练和最终正向/containment/N/WP* 证据绑定候选进入 manifest；初始 checkpoint 固定 `attestation_state=none` |
| G5 激活与 Go | P0-11b、两条状态权威、witness | attestation 独立链连续 `issued → active`；activation 后 checkpoint 同时承诺 release/attestation 两组 serial/head 并明确 active；四方 Go 绑定同一对象；部署前再次 challenge |

后一个 Gate 不能补偿前一个 Gate 的缺失。当前 G0 尚未通过，因此整体保持 No-Go。

## 5. 可信发布固定顺序

1. G0 先冻结 profile、persona、默认 CTA、适用性、证据计划和适用决策；此时不生成候选。
2. G1～G3 完成所选 profile 的实现级退出条件，包括 P0-3b=R 的安全、数据和用户旅程闭环。
3. 进入 G4 后，P0-11a 才按冻结输入生成候选制品、版本向量和 candidate fingerprint。
4. 以该候选执行 P0-10 演练并收集 P0-1～P0-10、P0-11a、适用 WP*、C/N 的最终证据，再签署不可变 `release_evidence_manifest`。
5. 发布初始 current statement 并取得 `current_attestation_digest=null`、`attestation_state=none` 的初始 checkpoint；它不反向进入 manifest。
6. 发布工程、架构、安全、运维四个独立 role 签署引用该初始 checkpoint 的 `p0_11_detached_attestation`。
7. 在独立 attestation 状态链上发布 `issued`，核验后再发布引用 issued 节点的 `active` activation statement；release lifecycle 另维护自己的状态链。
8. activation 后重新取得 nonce-bound fresh checkpoint，同时证明 release/attestation 两组 serial/head、唯一 current manifest 与 `attestation_state=active`；产品、架构、安全、测试四方 Go 绑定同一组对象。
9. 每次生产部署前重新 challenge；评审 checkpoint 不能充当部署时新鲜度证明。

manifest 不反向引用本候选初始 checkpoint、attestation 或 Go；其中的状态/checkpoint 证据只指流水线与验证器预演。缺 activation、只持有 `attestation_state=none|issued` 的 checkpoint、任一状态链截断或 split-view、checkpoint 过期/重放、权威不可达、profile/候选/信任材料变化时，结论立即为 No-Go。

## 6. 明早第二批决策

| decision_id | 需要冻结的内容 | 主要牵引 |
| --- | --- | --- |
| `D-ROUTE-001` | direct 首版边界、来源展示、费用权威及是否提供代理回退 | P0-4；§9、§13.3、§17.1 |
| `D-TRUST-001` / `D-TLS-001` | LAN 首次信任、证书签发/轮换/吊销与运营参数 | P0-5；§16.1、§16.2、§16.6 |
| `D-LEDGER-001` / `D-BILL-001` | 幂等 TTL、lease/fence/intent、状态查询保留期、流式中断计费 | P0-6；§13.3、§13.6、§14 |
| `D-CONFIG-001` | 可进入原子组的适配器、journal/锁、Head commit point 和 runtime activation | P0-8；§12、§18.6 |
| `D-CRED-001/002/003` | 贡献凭据模式、转移失败补偿、主存储/备份删除 SLA | P0-3b；§15、§17.6 |
| `D-DR-001` / `D-DRAIN-001` | 按 Deployment/数据类别的 RPO/RTO、排空与强制终止时限 | P0-10；§17.4～§17.6 |
| `D-KEY-001` / `D-AUDIT-001` | 根密钥托管、用途隔离、轮换和不可变审计锚 | P0-3a/P0-10/P0-11；§16、§17.7 |
| `D-REL-002` | trust backend、状态链/witness、checkpoint、证据保留与验证器工程规格 | P0-11、G4、G5；§19.4、§20.5 |

主文档 §21 是完整待决策清单的唯一来源。本摘要省略的事项不会自动定案。

## 7. 当前最高风险

1. 贡献凭据入口和账号创建路径已经存在，但贡献专用服务端关闭闸门、信封加密、迁移、轮换、撤回删除及备份闭环尚未形成同一候选证据。
2. 当前计费防重位于上游 dispatch 之后，尚无目标设计要求的 RequestRecord、dispatch lease/fence 和 durable intent，不能证明重复 dispatch 已被阻止。
3. 入站与出站仍有非 loopback 明文 HTTP 债务；与“仅真实 loopback 可用 HTTP，LAN/公网必须 TLS”的基线冲突。
4. JWT secret、TOTP/其他用途密钥和部分支付配置仍存在用途隔离或普通存储风险。
5. 高风险审计存在队列丢弃、批量写失败和可清空路径，尚未达到普通高风险变更 fail-closed 与第二耐久域要求。
6. 可信更新协议目前主要是目标设计，尚无绑定同一候选的签名制品、SBOM、provenance、manifest、active attestation、fresh checkpoint 和四方 Go 证据。

## 8. 建议评审顺序

1. 选择 release profile，并确认是否分批发布。
2. 确认四条首用入口、默认 CTA、A/B/C 默认链路和支持声明。
3. 确认 local-edge 分发与升级形态。
4. 冻结 direct、Endpoint/RouteBinding、RequestRecord/Attempt/Link 与配置事务工程规格。
5. 冻结贡献凭据、TLS、密钥、审计、RPO/RTO 与排空参数。
6. 复核 G0→G5、R/R*/C/N 证据规则和可信发布单向签署顺序。
7. 为每个已定事项填写 DRI、Approver、截止里程碑、证据形式和回写范围。

## 9. 最终只读校验

- 主文档快照：待最终校验回填。
- 摘要快照：待最终校验回填；原始文件 SHA-256 在最终交付回执中给出，避免摘要自引用导致 hash 变化。
- Markdown 表格、围栏、JSON、标题、章节引用、本地路径和 Mermaid：待最终校验回填。
- Claude Code 终审与新鲜 Codex 读者复核：待最终校验回填。
- 修改范围仅限 `docs` Markdown 文件；未修改业务代码、配置、数据库或本地服务状态，也未访问远端环境。

## 10. 明早结论记录

| decision_id | 结论 | DRI / Approver | 截止里程碑 | 必须回写的位置 |
| --- | --- | --- | --- | --- |
| `D-REL-001` | ________ | ________ | ________ | §5.2、§19.4、§20.5、§21、§22 |
| `D-UX-001` | ________ | ________ | ________ | §9、§18.5.1、§18.6、§20.4、§21 |
| `D-OPS-001` | ________ | ________ | ________ | §7.1、§17.4、§17.6、§21 |
| 其他 decision ID | ________ | ________ | ________ | 以主文档 §21 映射表为准 |

明早评审的目标是形成决策和后续工作输入，不是把候选设计基线误认成发布证明。任何 P0 适用项、containment 证据、可信发布对象或四方签署缺失时，发布结论继续保持 No-Go。
