# Sub2API 可调度来源与自动恢复审计任务

## 1. 审计目标

本任务用于审计 TransitHub 是否能严格区分 Sub2API 账号由管理员手动停用与由系统错误自动停用，并只恢复系统自动停用的账号。

必须满足以下核心规则：

- 管理员在 Sub2API 将“可调度”关闭后，该决定永久优先。TransitHub 不得探测后自动打开，也不得清除或覆盖管理员状态。
- 只有来源明确为 automatic 的系统自动停用账号，才能进入 TransitHub 定期恢复队列。
- 来源为 manual 的账号永不进入自动恢复队列。
- 来源未知、字段缺失或值非法时必须失败关闭：不探测、不恢复、不猜测。
- status=error 的恢复必须发送一次真实模型生成请求。GET /v1/models、TCP/HTTP 连通、HTTP 200、请求已接收或 response.created 都不能证明账号已恢复。
- 只有真实模型生成已经完成且结果有效，TransitHub 才能请求 Sub2API 清除系统错误状态。
- 真实生成失败，包括 401、403、429、余额不足、超时、5xx、无效响应和只收到 response.created 时，均不得清错。
- 清错操作必须由 Sub2API 在数据库中做条件更新，保证探测期间管理员改为 manual 时，TransitHub 的恢复请求无法覆盖管理员决定。

## 2. 用户确认的恢复语义

对 schedulable=true 且 status=error 的账号，错误状态代表账号当前不具备正常服务能力。恢复检查允许产生极小的真实模型请求消耗；其目的不是只检查端点是否在线，而是验证凭据、余额、路由和模型生成链路已经全部恢复。

恢复成功的唯一业务证据是：模型生成请求完成并返回有效生成结果。收到该证据后才允许清除 Sub2API 的系统错误状态，使账号重新参与调度。

## 3. 当前本地 TransitHub 已完成内容

当前本地实现已完成真实生成恢复检查：

- scheduler.go 中的 runSub2APIRecoveryProbe 强制使用 real_model，普通策略即使配置 models_endpoint，恢复任务也会覆盖为真实模型请求。
- probe_runner.go 增加 RequireCompletedGeneration，恢复检查要求有效 choices、response.completed、response.done，或有效 choices 后收到 [DONE]。
- 单独收到 response.created 只表示请求被接受，不算生成完成。
- 只有探测结果为 ResultOK 时才调用 Sub2API clear-error；其他结果保留错误状态。
- 恢复检查目前只清除系统运行错误，不直接修改 schedulable。

相关测试覆盖：

- 恢复请求使用 POST /v1/chat/completions，而不是 GET /v1/models。
- 普通策略配置为 models_endpoint 时，错误恢复仍强制使用真实生成。
- 401 等生成失败不调用 clear-error。
- response.created 不算恢复成功。
- response.completed 和有效完成的生成结果才算恢复成功。

## 4. 当前阻断问题

审计结论：当前仅部分完成，尚未达到“manual 永久关闭、automatic 自动恢复”的完整要求，不应按已完成版本发布。

原因是 Sub2API 管理员账号接口当前没有返回可靠的停用来源字段。现有 DTO 只包含 schedulable、status、temp_unschedulable_until、temp_unschedulable_reason、限流、过载和过期等运行字段，没有 manual/automatic 或同等语义字段。

TransitHub 当前仍依据状态组合推断来源：

- schedulable=false 被视为管理员手动关闭。
- schedulable=true 且 status=error 被视为系统错误，可进入恢复。

该推断不可靠。Sub2API 内部存在系统自动写入 schedulable=false 的生产路径，例如凭据错误、OAuth 错误、Grok 错误隔离和账号过期自动暂停。由此会把部分 automatic 自动停用误判为 manual，使其永远无法自动恢复。反过来，未来任何新的状态组合也可能造成误恢复风险。

在可靠来源字段落地前，TransitHub 必须对来源未知的账号保持失败关闭，不能用状态组合继续扩大自动恢复范围。

## 5. Sub2API 必须提供的数据契约

建议在账号持久化模型及管理员 API 中增加：

- schedulability_source：manual、automatic、none。
- schedulability_reason：管理员关闭、credential_error、balance_error、expired、upstream_error 等稳定原因码。
- schedulability_changed_at：本次来源与状态变更时间。

字段语义：

- manual：管理员明确关闭可调度。任何系统错误处理、自动恢复、后台任务都不能覆盖。
- automatic：Sub2API 系统基于错误、余额、凭据、过期等原因自动停止调度，允许 TransitHub 定期验证并恢复。
- none：当前没有停用来源。用于正常可调度账号；不能被解释为 automatic。

所有状态和来源必须在同一数据库事务或同一原子 UPDATE 中写入，禁止先改 schedulable 再异步补来源。

## 6. 写入规则

管理员单账号和批量操作：

- 管理员关闭可调度时，原子写 schedulable=false、schedulability_source=manual、reason=admin_disabled。
- 管理员重新开启时，原子写 schedulable=true、source=none，并按 Sub2API 现有规则处理 status。

系统自动停用：

- 凭据、余额、上游故障、过期等自动隔离必须原子写入 source=automatic 和稳定 reason。
- 自动任务不得把已有 source=manual 改成 automatic。
- 如果管理员已经手动关闭，后续系统错误只能记录运行错误，不能改变 manual 所有权。

## 7. 安全恢复接口

Sub2API 清错/恢复接口必须使用数据库 compare-and-set，而不是无条件更新。建议条件至少包含：

- account_id 匹配。
- schedulability_source=automatic。
- 当前 status=error 或当前 reason 与恢复目标匹配。
- 可选：schedulability_changed_at 或版本号与 TransitHub 入队时观察值一致。

条件不满足时返回 conflict/no-op，并保持账号状态不变。特别是当 TransitHub 探测期间管理员把账号改为 manual，迟到的恢复请求必须失败，不能覆盖管理员决定。

成功恢复时由 Sub2API 原子清除系统错误并按其调度规则恢复账号，例如写入 status=active、schedulable=true、source=none。TransitHub 不应分别调用多个接口拼接这一状态变化。

## 8. TransitHub 队列规则

拿到新字段后，TransitHub 的恢复候选必须改为：

- source=automatic：允许进入定期真实生成恢复检查。
- source=manual：永不进入恢复检查。
- source=none 且账号正常：按健康账号的常规周期测试。
- source 缺失、未知或非法：不进入恢复检查，并记录可审计原因。

TransitHub 不得再通过 schedulable 与 status 的组合猜测停用来源。

## 9. 验收测试

必须至少通过以下场景：

1. 管理员单账号关闭后，无论等待多久、上游是否恢复，TransitHub 都不发恢复探测、不打开账号。
2. 管理员批量关闭具有同样保护。
3. automatic 账号即使 schedulable=false，也能被 TransitHub 识别并进入恢复检查。
4. GET /v1/models 成功不能触发清错。
5. 真实生成返回 401、403、429、余额不足、超时或 5xx 时不清错。
6. HTTP 200 但响应无有效生成结果时不清错。
7. 只收到 response.created 时不清错。
8. 收到有效 choices、response.completed、response.done，或有效 choices 加 [DONE] 后才允许清错。
9. 探测成功后、清错前管理员改为 manual，Sub2API 条件更新必须拒绝恢复。
10. 来源字段缺失、未知或非法时 TransitHub 不探测、不恢复。
11. automatic 恢复成功后，Sub2API 原子恢复可调度状态并清除来源，TransitHub 后续按健康周期检查。

## 10. 发布状态与边界

- 当前改动仅存在于本地 TransitHub 工作区。
- 本任务未授权部署，本轮未上传云端。
- 所有构建必须在本地完成；云端禁止构建。
- 在 Sub2API 来源字段、条件恢复接口、TransitHub 消费逻辑和上述竞态测试全部完成前，本审计状态保持“不通过”。

## 11. 审计结论

真实模型生成恢复规则已经按最新要求修正：只有生成完成成功后才允许清除系统错误。

但 manual/automatic 来源契约目前仍不存在，TransitHub 仍无法可靠区分“管理员永久关闭”和“系统自动停用”。因此当前实现不能宣称达到最终要求。下一阶段必须先修改 Sub2API 的持久化字段、管理员 API 和原子恢复接口，再修改 TransitHub 只消费明确来源并补齐并发竞态测试。
