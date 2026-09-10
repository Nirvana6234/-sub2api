## Why

现有 OpenAI 透传（账号级 `accounts.extra.openai_passthrough`，注释里叫"自动透传（仅替换认证）"）并不是字节保真的。即便开着它，Codex 请求仍会被改写：

- 强制 `store=false` / `stream=true`（`normalizeOpenAIPassthroughOAuthBody`）
- 删掉 8 个 `openAIChatGPTInternalUnsupportedFields`
- `input` 归一（字符串/对象 → 数组）
- codex 模型缺 `instructions` 时合成默认值；`instructions` 存在但为空/非字符串时本地回 403
- 出站请求头走 14 条闭合白名单（`openaiPassthroughAllowedHeaders`）
- 出站请求体明文 JSON

这些改写全部是给**非官方客户端**兜底的安全网。真实 Codex 从不发出需要它们的形状。

参照实现 `zyycn/codex-proxy-rs` 的做法相反：请求体是"唯一真相源，逐字段（含顺序、含未知字段）透传"，热路径只写 `model` 并删 `max_output_tokens`/`temperature`；身份收敛（`scope_request_to_account`）是唯一的明确例外；请求头是黑名单；出站体默认 zstd 压缩（注释写明"与官方 Codex app-server 一致"）。

**它敢这么做不是因为技术更好，是因为它买了单**：README 明写不支持 `/v1/chat/completions`，且客户端版本不够时在进 Provider 前回 426。

sub2api 已经有等价的、甚至更细的门禁：`codex_cli_only`（官方 UA/originator 精确集 + 全局黑/白名单 + app-server 开闸）、可配置引擎指纹门（`EngineFingerprintSignals`）、Codex 引擎版本 min/max 门。且这套门禁的执行点 `detectCodexClientRestriction` 在 `openai_gateway_forward.go:43`，**早于所有 body 处理**。轻量信号 `isCodexCLI` 在同文件 212 行算好，正是透传分叉（245 行）的上一行。

所以这条路在本仓库是可达的，缺的只是一个"门禁成立时不做兜底"的开关。

## What Changes

- 新增账号级布尔 `accounts.extra.openai_passthrough_strict`。**零值 = 现状**，只在 `openai_passthrough` 为 true 时有意义。
- **strict 生效的硬前置条件**：同账号 `codex_cli_only` 必须开启。不满足时 strict 静默降级为普通透传并打一条告警日志（不是拒绝请求）。
- strict 下**取消**：`store` 强制、8 字段删除、`input` 归一、`instructions` 合成、空 `instructions` 的本地 403。
- strict 下**请求头改为黑名单**：默认放行客户端头，只扣掉网关自管的身份/传输头。
- strict 下**出站请求体 zstd 压缩**（`Content-Encoding: zstd`）。
- strict 下**保持不变**：`stream=true` 强制上送、账号身份影射、指纹收敛、turn-state 跨账号守卫、保留工具名别名、SSE 逐行回改、计费与用量记录。
- 跨模式 failover：在既有 `openAIPassthroughFailoverState` 上扩一个 strict 维度，沿用 `deriveOpenAIForwardAttemptBody` 这个既有钩子。
- `repository/scheduler_cache.go` 的 extra 投影键表补 `openai_passthrough_strict`。
- 管理端三个 modal 增加开关，并对"勾了 strict 但没勾 codex_cli_only"做保存期校验。

## Impact

- **计费/用量**：不受影响。用量在响应侧从 SSE 解析（`parseSSEUsageBytesWithType`），计费模型走 `requestedModel`/`upstreamPassthroughModel` 的独立追踪，而透传模式本来就不改 body 里的 `model`。前提是"`stream=true` 强制上送"这条保留（见 design §2.1）。
- **调度**：`IsModelSupported` 已因 `openai_passthrough` 短路放行全部模型，strict 不改变候选集语义。
- **数据库**：无迁移。复用 `accounts.extra` JSON。
- **其他平台**：不涉及。strict 只对 `IsOpenAI() && UsesOpenAICodexProtocol()` 的账号有意义。

## Non-goals

- 不引入 codex-proxy-rs 的续链范围建模（`PreviousResponseScope::Persisted / ConnectionLocal`）。`Persisted` 由客户端 `store=true` 触发，而 ChatGPT codex 端点对两边都拒 `store=true`；`ConnectionLocal` 属于 WS 连接池议题，另案。
- 不改 WS 路径（`openai_ws_forwarder_payload.go`）的头集合。第一版只覆盖 HTTP SSE 透传。
- 不删除、不改写现有 `openai_passthrough` 的任何行为。
- 不把现有 bool 改成三态字符串——那会牵动每一个读取点，包括前端三个 modal。
