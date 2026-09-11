# 设计：严格字节保真透传

本文按「改动点 → 改法 → 判据」组织。**判据**回答的是同一个问题：这条改写在 strict 下为什么可以去掉（或为什么必须留）。没有判据的改动点不要做。

贯穿全篇的一条总判据：

> **strict 的合法性完全来自客户端门禁。** 每一条被取消的规范化，都是「非官方客户端才需要的兜底」。凡是官方 Codex 自己也会踩的坑，一条都不能取消。

---

## 1. 开关与门禁

### 1.1 新增账号级开关

**位置**：`backend/internal/service/account.go`，紧邻 `IsOpenAIPassthroughEnabled()`（约 2261-2278 行）。

**改法**：

```go
// IsOpenAIPassthroughStrictEnabled 返回账号是否启用「严格字节保真透传」。
// 叠加语义：仅在 IsOpenAIPassthroughEnabled() 为 true 时有意义。
// 字段缺失或类型不正确时，按 false（关闭）处理——零值必须落在「现状」一侧。
func (a *Account) IsOpenAIPassthroughStrictEnabled() bool {
	if a == nil || !a.IsOpenAIPassthroughEnabled() {
		return false
	}
	enabled, ok := a.Extra["openai_passthrough_strict"].(bool)
	return ok && enabled
}
```

**判据**：零值安全。仓库自己在 `config.Config.DisableCodexIdentityEnforcement` 的注释里写过这条理由——未经 viper 加载而手工构造的配置（测试、工具）其零值必须落在保护开启那一侧。这里对应的是：任何没写这个键的账号，行为与今天逐字节一致。

**反例（不要这么做）**：把 `openai_passthrough` 改成 `"off" | "auth_only" | "strict"` 三态字符串。读取点包括 `account.go`、`scheduler_cache.go`、`openai_gateway_forward.go`、`openai_ws_http_bridge.go` 和前端三个 modal，全部要跟着改，且旧数据迁移期间存在两种真值来源。

### 1.2 生效判定：逐请求，且与 codex_cli_only 独立

> **2026-09-11 重写。** 初稿把 strict 绑在账号级 `codex_cli_only` 上，要求「账号开了那个门
> 且这一条过了门」。需求方指出这条依赖是错的，理由在实际拓扑上成立——见下方「为什么改」。

**位置**：`service/openai_passthrough_strict.go` 的 `resolveOpenAIStrictPassthrough`。

**判定**（两条，缺一不可）：

1. 账号开了 `openai_passthrough_strict`（它本身叠加在 `openai_passthrough` 之上）；
2. **这一条请求**被判定为官方 Codex 客户端发出。

第 2 条不成立 → **回落到普通自动透传，照常服务**。不是 403，不是报错。

**判定实现**：`EvaluateCodexClientIdentity`，与 `codex_cli_only` 共用同一份代码
（官方 UA → originator → 全局白名单 → app-server，再过版本门与引擎指纹门，黑名单优先拒）。
共用而不是各写一套：「像不像官方 Codex」在两处必须是同一个答案，否则管理员按其中一个
调白名单，另一个会悄悄不跟。

**两处刻意的差异**：

- **不看账号的 `codex_cli_only` 开关**。`Detect` 在那个开关关闭时第一步就短路返回
  `Disabled`，压根没看客户端——拿它兼任身份判定，等于要求必须先开 `codex_cli_only`。
- **不认 `gateway.force_codex_cli`**。那条是无条件放行，证明不了来路。

**取策略的条件也要跟着放宽**：`resolveCodexRestrictionPolicy` 原来只在
`IsCodexCLIOnlyEnabled()` 时才去读全局设置。strict 必须并列进这个条件，否则管理员配的
黑名单/版本门在「只开 strict、不开 codex_cli_only」的账号上形同虚设——而那恰恰是最常见的用法。

**为什么改**：两个开关的语义根本不同。

| | codex_cli_only | strict |
| --- | --- | --- |
| 非 Codex 请求 | **403 拒掉** | **降级为普通透传，照常服务** |
| 适用账号类型 | 仅 oauth / setup-token | 凡是能开自动透传的（含 apikey） |

真实部署里 strict 最常见的用法是 apikey 账号 + `base_url` 指向另一台 sub2api 中继。
那台中继自己也在判「流量像不像官方 Codex」，所以字节保真在这条链路上比直连 chatgpt.com
更有价值。而 `codex_cli_only` 对 apikey 账号根本不开放——绑定它等于把 strict 挡在了
它最该生效的场景之外。

**告警策略**：只有 `client_gate_not_evaluated` 打 WARN。「不是 Codex 客户端」是设计内的
正常路径（网页、curl、各类 SDK 打到 strict 账号上都会落在这里），每条请求告警一次只会
把日志淹掉。

**已接受的残留风险**：判定依据里的 UA / originator 是客户端自报的，可以伪造。伪造的后果
是该请求的头按黑名单放行（而非白名单裁剪）、body 原样上送；凭据与身份类头仍然一律剥除。
2026-09-11 与需求方确认后接受，是取舍不是疏漏。

### 1.3 建议同时收紧引擎指纹门（配置项，非代码）

**位置**：全局设置 `SettingKeyCodexCLIOnlyEngineFingerprintSignals`。

**改法**：strict 账号所在部署，建议把默认列表里那两条 `body_path` 信号也勾上（`Required: true`）：
`client_metadata.x-codex-window-id`、`client_metadata.x-codex-installation-id`。

**判据**：默认种子只勾了 `header_prefix: x-codex-`——**只看头**。而 strict 的效果正是把 body 原样送上去，**body 形状才是真正的风险面**：头装得像、body 是个 chat/completions 翻译产物，那才会给账号招上游的眼。这两条信号已经在 `DefaultEngineFingerprintSignals` 里躺着，只是 `Required: false`，勾上即可，不需要写代码。

---

## 2. 请求体

分叉点在 `forwardOpenAIPassthrough`（`openai_gateway_passthrough.go:126`）内部，`account.UsesOpenAICodexProtocol()` 那个块（约 153-215 行）。

### 2.1 stream=true 强制上送 —— **必须保留**

**判据**：三条独立理由，任一条都足够。

1. Codex 上游只交付 SSE，非流式请求直接 400。codex-proxy-rs 自己也强制（`client_sse.rs:90`，注释同义）。
2. **计费依赖它。** 用量从 SSE 事件里解析（`parseSSEUsageBytesWithType`，`openai_gateway_passthrough.go:2078`）。拆掉这条，`stream:false` 的请求要么被上游拒，要么拿不到用量——本变更承诺「计费保持」，这条是承诺的支点。
3. 下游要 `stream:false` 时不要透传，用现成的 `reconstructResponseOutputFromSSE`（`openai_gateway_response_handling.go` 约 2311 行）在下游重组完整 JSON。这条路径已经存在且有测试。

**注意**：`stream` 只在**出站构造那一刻**覆盖，不要回写进用于 failover 重放的 canonical body。

### 2.2 store 强制 —— strict 下取消

**改法**：`normalizeOpenAIPassthroughOAuthBody`（`openai_gateway_request_body.go:1210`）增加 `strict bool` 参数；strict 时跳过 `store` 的 set。

**判据**：真实 Codex 自己就发 `store=false`。强制存在的意义只是接住那些发 `store=true` 的第三方客户端（上游会回 "Store must be set to false"）。门禁成立时这类客户端进不来；万一进来了，让上游自己回 400 是 strict 模式下的正确行为——错在调用方，损失是它自己那一次请求。

### 2.3 删 8 个 openAIChatGPTInternalUnsupportedFields —— strict 下取消

涉及字段：`chat_template_kwargs`、`user`、`metadata`、`prompt_cache_retention`、`safety_identifier`、`stream_options`、`truncation`、`stop_sequences`。

**判据**：同 2.2。官方 Codex 一个都不发。保留删除逻辑等于在 strict 模式下仍然假设下游可能不是 Codex，与开关的前提矛盾。

**留一条尾巴**：strict 下若上游回 400 且提到某字段不支持，日志要能一眼看出是哪个字段（见 §7），而不是回来重读代码。

### 2.4 input 归一（字符串/对象 → 数组）—— strict 下取消

**判据**：官方 Codex 恒发数组。归一是为宽容 `input: "hello"` 这种手写请求。

### 2.5 instructions 合成默认值 + 空 instructions 的 403 —— strict 下取消

涉及 `detectOpenAIPassthroughInstructionsRejectReason`（`openai_gateway_request_body.go` 约 1310 行）与 `defaultCodexSynthInstructions`。

**判据**：官方 Codex 每轮都自带完整 instructions（实测约 21KB）。合成默认值是给「只发了 model+input」的裸客户端用的；403 是防止那种客户端把一个没有 instructions 的请求打到共享账号上、被上游按异常客户端记账。门禁成立后两者都无对象。

**风险提示**：这是本清单里唯一一条「取消后会放宽本地防护」的改动。如果部署里 `codex_cli_only` 通过 `AllowAppServerClients` 或全局白名单放行了自定义 app-server 客户端，那些客户端确实可能发空 instructions。**因此 §1.3 的 body_path 指纹门在这条上尤其值得勾。**

### 2.6 保留工具名别名（python → python__sub2api）—— **不动**

**判据**：`aliasOpenAIOAuthReservedToolName` 只认 `python` 一个名字（`openai_codex_tool_names.go:62`），没命中时 `len(reverse)==0` 直接返回 `changed=false`，是**零成本 no-op**。不需要为 strict 做任何事，留着即可。为它加分支只会增加两条路径漂移的机会。

### 2.7 账号身份影射 + 指纹收敛 —— **必须保留**

涉及 `applyCodexAccountIdentityClientMetadataRaw` 与 `applyCodexFingerprintClientMetadataRaw`（`openai_gateway_passthrough.go` 约 183-212 行）。

**判据**：这从来就不是两边的差异。codex-proxy-rs 的 `scope_request_to_account` 是它自己声明的透传例外，做的是同一件事（跨账号剥身份键、统一 installation_id）。多账号共享 OAuth 账号的场景下，这层去掉就等于把每个用户的设备指纹原样交给上游。

### 2.8 turn-state 跨账号守卫 —— **必须保留**

`guardOpenAICodexTurnStateEcho`（`openai_gateway_passthrough.go:640`）。

**判据**：failover 换号后客户端仍会回带旧账号铸造的 blob。这是代理链独有、真实 Codex 永远不会产生的矛盾信号。strict 的目标是「看起来像官方」，留着这个矛盾恰好相反。

---

## 3. 请求头：闭合白名单 → 黑名单

**位置**：`buildUpstreamRequestOpenAIPassthrough`（`openai_gateway_passthrough.go:585`）里那段 `isOpenAIPassthroughAllowedRequestHeader` 过滤（约 624-635 行）。

**改法**：strict 时改用 denylist。建议直接照 codex-proxy-rs `transport/headers.rs` 的 `response_headers()` 跳过表逐条对齐：

```
originator, user-agent, version, authorization, chatgpt-account-id, cookie,
x-openai-internal-codex-residency, openai-beta, accept, content-type,
content-encoding, x-codex-routing-hint, x-codex-installation-id,
x-codex-turn-id, x-oai-attestation, x-oai-is, x-oai-is-update,
<responses-lite header>
```

再叠加 Go 侧必须自管的逐跳头：`connection`、`keep-alive`、`transfer-encoding`、`upgrade`、`te`、`trailer`、`host`、`content-length`、`proxy-*`，以及 `x-forwarded-*` / `x-real-ip` / `cf-connecting-ip` 这类来源标识。

**判据**：现有白名单的注释写得很清楚——「避免将非标准/环境噪声头传给上游触发风控」。门禁成立时客户端的头**就是**官方那一套，噪声来源消失，而白名单反而在吃掉官方确实会发的头。

**实测增量（2026-09-11，`TestBuildUpstreamRequestOpenAIPassthrough_StrictHeaderDelta`）**。初稿这里是照着 codex-proxy-rs 推的，**猜错了两条**，以下为实际量出的差集：

| header | auth_only | strict |
|---|---|---|
| `session-id`（连字符） | 丢弃 | 转发 |
| `thread-id` | 丢弃 | 转发 |
| `x-client-request-id` | 丢弃 | 转发 |
| `x-codex-parent-thread-id` | 丢弃 | 转发 |
| `x-openai-subagent` | 丢弃 | 转发 |
| `x-responsesapi-include-timing-metrics` | 丢弃 | 转发 |
| 任意未知头 | 丢弃 | 转发 |

除此之外两种模式**完全一致**。两处更正：

- **`x-codex-routing-hint` 不在增量里**，它是**网关自有**头：`setOpenAICodexRoutingHint`（`openai_routing_hint.go:19`）先删光客户端的每一种拼写，再按最终上游模型合成。初稿说「白名单在吃掉它」是错的——它压根不是转发来的。两种模式下客户端值都无法出站。
- **`x-codex-installation-id` 不进黑名单**（与 codex-proxy-rs 分歧）。那边挡掉它是因为**总是**按租约生成一个；本仓库只在指纹收敛开启时生成，而收敛默认 off。收敛关着时挡掉它，上游会看到一个**没有安装标识**的请求——比放行更不像官方客户端，与 strict 的目标相反。实测两种模式今天都在转发它；收敛开启时 `applyStagedCodexFingerprintHeaders` 会照常覆写。

**「任意未知头也透过去」是黑名单的直接后果**，也正是白名单注释担心的那件事。它在 strict 下可接受的唯一理由就是门禁：客户端已被证明是官方 Codex，「非标准噪声头」这个风险来源不存在。这条依赖必须和 §1.2 的硬约束一起看——门禁一旦形同虚设（如 `force_codex_cli`），这条就变成真的风险，所以 strict 对它是显式互斥的。

**方法论**：断言写成**差集**而不是「strict 应转发 X」的逐条清单。后者容易写成对设计文档的复述——而设计文档在这里恰好错了两条。差集会因为任何一侧多出或少掉一条而变红，包括没预料到的那条。

---

## 4. 传输：出站 zstd

**位置**：`buildUpstreamRequestOpenAIPassthrough` 里 `http.NewRequestWithContext` 那一步。

**改法**：strict 时把最终 body 用 `klauspost/compress/zstd`（已在依赖里，见 `internal/pkg/httputil/body.go`）压缩，设 `Content-Encoding: zstd`。压缩级别对齐参照实现的 **3**。

**判据**：官方 Codex app-server 就是这么发的，codex-proxy-rs 的注释直接写明「与官方 Codex app-server 一致」。strict 的目标是字节形状一致，这是唯一一条纯新增就能拿到的一致性。

**必须检查的两处**：

1. 压缩要在**所有改写之后**做，`Content-Length` 按压缩后长度算。
2. 诊断链路不能在压缩后再去读 body：`internal/handler/request_body_read_log.go` 认得 `zstd`，但它读的是**入站**体；确认没有别的 ops/日志路径在出站构造之后重读 `req.Body`。

---

## 5. 跨模式 failover

**位置**：`backend/internal/handler/openai_gateway_reasoning_failover.go:26` 的 `deriveOpenAIForwardAttemptBody` 与 `openAIPassthroughFailoverState`。

**改法**：给 state 加 `strictSeen bool`，逻辑与既有 `passthroughSeen` 同构。

**判据**：这个「换号换语义」的问题**仓库已经承认并处理过一次**——普通透传账号与非透传账号之间 failover 时，`SanitizeOpenAICrossModeFailoverReasoning` 会清理 reasoning。strict 只是同一状态机上多一维，不需要发明新机制。

关键不变量（沿用既有写法）：`Forward(ctx, c, account, body)` 是**逐 attempt** 调用的（`openai_gateway_handler.go:737`），body 每次从请求级 canonical body 派生。所以 **strict 与否必须每 attempt 从当前账号重新判定**，绝不能把上一 attempt 派生出的 body 直接喂给下一个账号。

**不采用的方案**：在选号阶段冻结 strict 并把非 strict 账号排除出候选。它会在只有一个 strict 账号可用时把可恢复的请求变成硬失败，代价大于收益。

---

## 6. 调度与缓存

**位置**：`backend/internal/repository/scheduler_cache.go:1016` 附近的 extra 键投影表。

**改法**：在 `"openai_passthrough"`、`"openai_oauth_passthrough"` 旁边加 `"openai_passthrough_strict"`。

**判据**：那张表旁边已有一段血泪注释——透传开关不进投影时，候选过滤 `ListSchedulableAccounts` 读到的是裁剪过的 extra，`Account.IsModelSupported` 的短路失效，表现为「单独测账号能通、走网关报 no available accounts」（issue #4936）。strict 虽然不参与 `IsModelSupported`，但 `IsOpenAIPassthroughStrictEnabled()` 依赖 `IsOpenAIPassthroughEnabled()`，一旦有任何读取点落在投影后的账号对象上就会静默读到 false。**漏掉这条不会报错，只会让开关在某些路径上莫名不生效。**

---

## 7. 可观测性

**改法**：在 ops 日志/请求记录里加两个字段：

- `passthrough_mode`：`off` / `auth_only` / `strict`
- `strict_degraded_reason`：`""` / `codex_cli_only_disabled` / `force_codex_cli_enabled`

**判据**：strict 是「少做事」的开关，出问题时的现象是**上游 400 而本地一切正常**。没有这两个字段，排查只能靠翻账号配置猜。降级原因尤其重要——静默降级是 §1.2 选定的行为，必须留下痕迹，否则运维会以为 strict 生效了。

---

## 8. 管理端

**位置**：`frontend/src/components/account/CreateAccountModal.vue`、`EditAccountModal.vue`、`BulkEditAccountModal.vue`（现有 `openai_passthrough` 的三个写入点）。

**改法**：

1. strict 开关**嵌套**在透传开关下，透传关闭时不可见/不可勾。
2. 可见范围**与透传完全一致**（含 apikey）——不跟 `codex_cli_only` 的账号类型走。
3. 文案写清楚两件事：这是「只对判定为官方 Codex 的请求生效的字节保真模式」，
   以及「不是 Codex 的请求会自动回落、不会被拒绝」。不要写成「更快的透传」。

**没有保存期硬校验**（初稿有，2026-09-11 随 §1.2 一起去掉）：strict 不依赖任何别的开关，
没有"能存下来但永远不生效"的组合，也就没有需要堵的东西。

---

## 9. 明确不动的清单

| 事项 | 为什么不动 |
|---|---|
| 非透传路径（`applyCodexOAuthTransform`）的全部行为 | strict 是透传的叠加档，不碰另一条链路 |
| WS 路径 `openai_ws_forwarder_payload.go` 的头集合 | 第一版只覆盖 HTTP SSE；WS 的头是显式拷贝表，另案 |
| 响应侧 SSE 逐行回改（工具名/namespace/function_call arguments/image 状态） | 它们是**回程**修复，对应请求侧仍在做的改写；请求侧取消哪条，回程那条自然不触发 |
| `previous_response_not_found` / `invalid_encrypted_content` 的服务端恢复 | 属于续链策略，与字节保真正交；改它要另开一个 change |
| `model` 字段 | 透传路径本来就不改（compact 回退除外） |
| 数据库 schema | 复用 `accounts.extra` |
