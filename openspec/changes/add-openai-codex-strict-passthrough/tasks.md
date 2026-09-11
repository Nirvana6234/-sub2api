# 实施清单

分六个阶段，每阶段可独立评审、独立回滚。阶段 1-2 不改变任何现网行为（开关默认关）。

## 0. 修复测试编译基线（阻塞项，单独提交，不混进本变更）

> 范围提醒：坏的只有**测试包**。`go build ./cmd/server` 与后端启动一直正常——缺的方法都在 `_test.go` 的桩上，go build 不看 `_test.go`。

- [x] 0.1 补四个测试桩缺失的接口方法（两个接口、五个方法），让 `internal/service` 与 `internal/server_test` 能编译。`go vet -tags=unit ./...` 与 `-tags=integration ./...` 均干净（见 verification.md V-0）
- [x] 0.2 用 `go test -list` 复核三个 pattern 的基线执行数并写回 verification.md（587 / 121 / 279，三条门当前均绿）
- [x] 0.2b 清掉编译修好后暴露的既有失败（V-0 表格）。2026-09-11 完成，`go test -tags=unit ./internal/...` 49 包全绿；其中 3 条是生产代码真问题（调度 nil 守卫缺失、Grok 软门漏在粘滞路径、分成比例设置从未被解析）
- [ ] 0.3 录制 Codex `/v1/responses` 请求体 fixture 到 `internal/service/testdata/`（V-2 步骤 0）
- [ ] 0.4 生成 `codex_responses_upstream_auth_only.golden.json`（V-12 的 golden 基准，必须在动手改代码之前生成）

## 1. 开关与门禁（无行为变化）✅ 已完成

- [x] 1.1 `service/account.go` 新增 `IsOpenAIPassthroughStrictEnabled()`，零值 = false，依赖 `IsOpenAIPassthroughEnabled()`
- [x] 1.2 `repository/scheduler_cache.go` 的 extra 投影键表加 `"openai_passthrough_strict"`（见 design §6）
- [x] 1.3 新增 `service/openai_passthrough_strict.go`：`stageCodexClientRestrictionResult` / `stagedCodexClientRestrictionResult` / `resolveOpenAIStrictPassthrough(ctx, c, account) (bool, reason)`。判定读**本次请求实际的门禁结果**而非账号开关，判定缺席即 fail-closed（见 design §1.2）
- [x] 1.3b `openai_gateway_forward.go:43` 判定算出后立即 stage，failover 每 attempt 无条件覆写
- [x] 1.4 降级时打 WARN `openai.passthrough_strict_degraded`，带 account_id / account_name / reason
- [x] 1.5 单测：四种组合（strict off / strict+no cli_only / strict+force_codex_cli / 全满足）各自的返回值与 reason
- [x] 1.6 单测：`Extra` 为 nil、键类型为 string/number/nil、只开 strict 不开透传、非 OpenAI 平台，一律 false
- [x] 1.7 单测：fail-closed 三条（判定未 stage / context 为 nil / 判定为「没过门」）+ 跨 attempt 覆写 + 类型不符视同缺席

验证：`go build ./...` 与 `go vet -tags=unit ./internal/service ./internal/repository` 干净；新增 12 个用例全绿；三条回归门全绿（见 verification.md 基线表）；`internal/repository` 仍只有那两个既有失败，无新增。

## 2. 请求体分叉（**押后**，见下）

> **2026-09-11 实测把这一阶段降级了。** 录完 fixture 后用真实 Codex 报文跑
> `normalizeOpenAIPassthroughOAuthBody`，返回 `changed=false`——**一个字节都没改**。
> 逐条对：`store` 本来就是 false；8 个待删字段一个都不存在；`input` 本来就是数组；
> `instructions` 17174 字符。另外 `flatten_namespaces` 与指纹收敛都是账号级 opt-in
> 且默认关，工具名别名对无 `python` 工具是 no-op。
>
> 也就是说：**默认配置的透传账号 + 真实 Codex 请求，今天的 body 链路已经字节保真。**
> 本阶段全做完，对合规流量的可观测变化是 0；价值只剩「挡住畸形形状带来的意外改写」
> 这层保险。用户 2026-09-11 拍板先做 §3+§4，本阶段押后。

- [ ] 2.1 `normalizeOpenAIPassthroughOAuthBody` 增加 `strict bool` 参数；strict 时跳过 `store` 的强制写入
- [ ] 2.2 strict 时跳过 `openAIChatGPTInternalUnsupportedFields` 的 8 个字段删除
- [ ] 2.3 strict 时跳过 `input` 归一
- [ ] 2.4 strict 时跳过 `defaultCodexSynthInstructions` 注入与 `detectOpenAIPassthroughInstructionsRejectReason` 的 403
- [ ] 2.5 **确认 `stream=true` 仍然强制上送**，且只在出站构造时覆盖、不回写 canonical body（design §2.1）
- [ ] 2.6 **确认身份影射 / 指纹收敛 / turn-state 守卫在 strict 下原样执行**（design §2.7、§2.8）
- [ ] 2.7 保留工具名别名不加任何分支（design §2.6）
- [ ] 2.8 单测：同一份官方 Codex 请求体，strict 与非 strict 分别出站，逐字段 diff 只应出现预期差异
- [ ] 2.9 单测：strict 下客户端发 `store:true` 时 body 原样保留（由上游拒绝，不在本地改）

## 3. 请求头与传输 ✅ 已完成

- [x] 3.1 `buildUpstreamRequestOpenAIPassthrough` 的头过滤按 strict 分叉为 denylist（design §3）
- [x] 3.2 ~~确认 `x-codex-installation-id` 在 denylist 中~~ **结论相反：刻意不放进 denylist**。本仓库只在指纹收敛开启时生成安装标识（默认 off），挡掉它会让上游看到一个没有安装标识的请求，比放行更不像官方。理由与实测见 design §3
- [x] 3.3 补齐 Go 侧逐跳头与来源标识头的排除
- [x] 3.4 strict 时出站 body zstd 压缩（level 3）+ `Content-Encoding: zstd`；压不小时退回明文
- [x] 3.5 审计出站构造之后重读 `req.Body` 的路径：只有 `http_upstream.go` 的 Grok 官方 API 回退（`newGrokOfficialAPIFallbackRequest`）用 `GetBody`，它按 host/`X-XAI-Token-Auth` 判定，与 Codex 路径无交集；`request_body_read_log.go` 只读入站体
- [x] 3.6 单测：**差集断言**锁死 strict 的全部请求头增量（7 条），而不是逐条复述设计文档——初稿在这里猜错过两条
- [x] 3.7 单测：两种模式下客户端伪造的 `authorization`/`cookie`/`chatgpt-account-id`/`x-oai-attestation`/`x-forwarded-for` 都不会到达上游
- [x] 3.8 单测：`x-codex-routing-hint` 是网关自有头，两种模式都不得让客户端值出站
- [x] 3.9 单测：zstd 往返逐字节相等、小体积不压、空体不压、非 strict 仍明文出站
- [x] 3.10 ops 记录新增 `ops_openai_passthrough_mode` 与 `ops_openai_strict_degraded_reason`（原 §4.3 提前到此）

验证：`go build ./...`、`go vet -tags=unit ./...`、`go vet -tags=integration ./...` 干净；改动文件 gofmt 干净；三条回归门 614 / 121 / 279 全绿；golden 未变（非 strict 出站字节未动）。

## 4. failover 与可观测性 ✅ 已完成

> **4.1 不做，理由留在这里免得下次被反射式加回来。** strict 从头到尾没有回写过
> `body`——`forwardOpenAIPassthrough` 只在顶部判定+暂存，出站构造器
> `buildUpstreamRequestOpenAIPassthrough` 用的是函数内的局部 `outboundBody`。
> §2（请求体分叉）押后，就没有任何 body 差异需要跨 attempt 派生。等 §2 真正开工时
> 再评估 `strictSeen`。

- [x] 4.1 ~~`openAIPassthroughFailoverState` 增加 `strictSeen`~~ **不做**，见上
- [x] 4.2 每 attempt 重新判定：`Forward` 顶部**无条件复位** strict 暂存与 ops 档位，
      `forwardOpenAIPassthrough` 顶部再用真实判定覆写。复位不能只放在后者——
      failover 换到**非透传**账号时那一段根本不执行，上一 attempt 的 `true` 会原样残留。
      位置选在 `Forward` 顶部（`stageCodexClientRestrictionResult` 之后、403 分支之前），
      套路同下方已有的 `stageCodexFingerprintIDs(c, nil)`
- [x] 4.3 ops 记录新增 `passthrough_mode` 与 `strict_degraded_reason`（已在 §3.10 落地）
- [x] 4.4 单测：strict → 非透传账号，第二个 attempt 的暂存值与 ops 档位都必须复位。
      **去掉复位后这条会红**（实测）。body 派生方向的断言随 §2 一起押后——今天两种模式
      出站 body 本就相同，写了也是空断言
- [x] 4.5 单测：非 strict → strict 的升档方向；以及门禁拒绝（403）分支同样先复位
- [x] 4.6 断言 strict 在 WS 路径上惰性。查清了根因：WS 用的是 upgrade 请求自己的
      `*gin.Context`（`openai_gateway_handler.go:3020` → `ProxyResponsesWebSocketFromClient`），
      它永远不流经 `Forward`，所以 `stagedOpenAIStrictPassthrough` 读到的是「没暂存」。
      惰性是结构性的，不是巧合
- [x] 4.7 V-14 对比用例：未暂存的 context + 开着 strict 的账号，出站头必须落在 auth_only

验证：`go build ./...` 干净；新增 4 个用例全绿，其中 2 个在移除 `Forward` 顶部复位后变红。

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
