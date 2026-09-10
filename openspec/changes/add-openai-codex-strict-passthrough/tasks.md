# 实施清单

分六个阶段，每阶段可独立评审、独立回滚。阶段 1-2 不改变任何现网行为（开关默认关）。

## 0. 修复测试编译基线（阻塞项，单独提交，不混进本变更）

> 范围提醒：坏的只有**测试包**。`go build ./cmd/server` 与后端启动一直正常——缺的方法都在 `_test.go` 的桩上，go build 不看 `_test.go`。

- [x] 0.1 补四个测试桩缺失的接口方法（两个接口、五个方法），让 `internal/service` 与 `internal/server_test` 能编译。`go vet -tags=unit ./...` 与 `-tags=integration ./...` 均干净（见 verification.md V-0）
- [x] 0.2 用 `go test -list` 复核三个 pattern 的基线执行数并写回 verification.md（587 / 121 / 279，三条门当前均绿）
- [ ] 0.2b **清掉编译修好后暴露的既有失败**（V-0 表格）。优先 `internal/service` 那个 nil-pointer panic（`gateway_scheduling.go:1315`）——它中断整包，失败面在修掉它之前不可见
- [ ] 0.3 录制 Codex `/v1/responses` 请求体 fixture 到 `internal/service/testdata/`（V-2 步骤 0）
- [ ] 0.4 生成 `codex_responses_upstream_auth_only.golden.json`（V-12 的 golden 基准，必须在动手改代码之前生成）

## 1. 开关与门禁（无行为变化）

- [ ] 1.1 `service/account.go` 新增 `IsOpenAIPassthroughStrictEnabled()`，零值 = false，依赖 `IsOpenAIPassthroughEnabled()`
- [ ] 1.2 `repository/scheduler_cache.go:1016` 的 extra 投影键表加 `"openai_passthrough_strict"`（见 design §6）
- [ ] 1.3 新增 `resolveOpenAIStrictPassthrough(c, account) (bool, reason string)`：strict ∧ codex_cli_only ∧ ¬force_codex_cli
- [ ] 1.4 降级时打 WARN 日志，带 account_id 与缺失条件
- [ ] 1.5 单测：四种组合（strict off / strict+no cli_only / strict+force_codex_cli / 全满足）各自的返回值与 reason
- [ ] 1.6 单测：`Extra` 为 nil、键类型为 string/number 时一律 false

## 2. 请求体分叉

- [ ] 2.1 `normalizeOpenAIPassthroughOAuthBody` 增加 `strict bool` 参数；strict 时跳过 `store` 的强制写入
- [ ] 2.2 strict 时跳过 `openAIChatGPTInternalUnsupportedFields` 的 8 个字段删除
- [ ] 2.3 strict 时跳过 `input` 归一
- [ ] 2.4 strict 时跳过 `defaultCodexSynthInstructions` 注入与 `detectOpenAIPassthroughInstructionsRejectReason` 的 403
- [ ] 2.5 **确认 `stream=true` 仍然强制上送**，且只在出站构造时覆盖、不回写 canonical body（design §2.1）
- [ ] 2.6 **确认身份影射 / 指纹收敛 / turn-state 守卫在 strict 下原样执行**（design §2.7、§2.8）
- [ ] 2.7 保留工具名别名不加任何分支（design §2.6）
- [ ] 2.8 单测：同一份官方 Codex 请求体，strict 与非 strict 分别出站，逐字段 diff 只应出现预期差异
- [ ] 2.9 单测：strict 下客户端发 `store:true` 时 body 原样保留（由上游拒绝，不在本地改）

## 3. 请求头与传输

- [ ] 3.1 `buildUpstreamRequestOpenAIPassthrough` 的头过滤按 strict 分叉为 denylist（design §3）
- [ ] 3.2 **确认 `x-codex-installation-id` 在 denylist 中**——它由指纹收敛生成，不能透传客户端原值
- [ ] 3.3 补齐 Go 侧逐跳头与来源标识头的排除
- [ ] 3.4 strict 时出站 body zstd 压缩（level 3）+ `Content-Encoding: zstd`
- [ ] 3.5 审计出站构造之后是否有任何路径重读 `req.Body`；确认 `request_body_read_log.go` 只读入站体
- [ ] 3.6 单测：strict 下 `x-codex-routing-hint` / `x-openai-subagent` / `session-id` / `thread-id` 能到达上游请求头
- [ ] 3.7 单测：strict 下客户端伪造 `authorization` / `cookie` / `chatgpt-account-id` 不会到达上游

## 4. failover 与可观测性

- [ ] 4.1 `openAIPassthroughFailoverState` 增加 `strictSeen`，`deriveOpenAIForwardAttemptBody` 处理 strict→非 strict 的降级派生
- [ ] 4.2 确认每 attempt 从当前账号重新判定 strict，不复用上一 attempt 的 body
- [ ] 4.3 ops 记录新增 `passthrough_mode` 与 `strict_degraded_reason`
- [ ] 4.4 单测：strict 账号首选失败 → 换到非 strict 账号，第二次 attempt 的 body 走完整规范化
- [ ] 4.5 单测：非 strict → strict 方向同样正确（canonical body 未被上一 attempt 污染）
- [ ] 4.6 断言 strict 在 WS 路径上惰性：`openai_ws_http_bridge.go:539` 的 `c.Set("openai_passthrough", true)` 不得让 `resolveOpenAIStrictPassthrough` 在 WS 入站返回 true（对应 V-14）
- [ ] 4.7 新写 V-14 那条对比用例（同账号 strict on/off，经 WS 入站的出站头集合与 body 必须完全相同）

## 5. 管理端与文档

- [ ] 5.1 三个 modal 增加嵌套开关（`CreateAccountModal.vue` / `EditAccountModal.vue` / `BulkEditAccountModal.vue`）
- [ ] 5.2 保存期硬校验：strict 需要 codex_cli_only，否则阻止保存
- [ ] 5.3 文案定稿，明确「只给官方 Codex 用的字节保真模式」
- [ ] 5.4 Vitest 覆盖三个 modal 的开关联动与校验分支
- [ ] 5.5 在 `DEV_GUIDE.md` 补一条：strict 排查从 `passthrough_mode` 字段看起
- [ ] 5.6 建议部署文档记录 §1.3 的指纹门配置（勾上两条 body_path 信号）

## 6. 灰度

- [ ] 6.1 单账号开启 strict，跑 verification.md 的 V-1 ~ V-6
- [ ] 6.2 观察 24h：上游 4xx 率、`server_is_overloaded` 命中率、用量记录完整率三项与开启前对比
- [ ] 6.3 确认无回归后再扩到同组其他账号
