package service

// 本文件承载"严格字节保真透传"（accounts.extra.openai_passthrough_strict）的
// 生效判定。开关本身叠加在 openai_passthrough 之上，取消的是透传路径上那些为
// 非官方客户端兜底的规范化（强制 store、删不支持字段、input 归一、合成
// instructions、请求头闭合白名单）。
//
// 这些兜底能取消的**唯一依据**是：本次请求确实来自官方 Codex 客户端。因此 strict
// 的判定不看"账号配置开没开"这一件事，而是三个条件同时成立：
//
//  1. 账号配置开了 strict；
//  2. 本次请求**确实通过了** codex_cli_only 门禁（不是"账号上开了门"，是"这一条
//     过了门"）；
//  3. 该门禁不是被 gateway.force_codex_cli 旁路放行的。
//
// 任一不成立就降级为普通透传并留下原因，不拒绝请求——这个开关由运维主动控制，
// 配错时应该退回一个已知安全的行为，而不是让整个账号的流量 403。

import (
	"context"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"go.uber.org/zap"
)

// codexClientRestrictionResultContextKey 暂存本次请求的 codex_cli_only 判定结果。
//
// 判定发生在 Forward 的最前面（早于任何 body 处理），而 strict 的分叉在透传分支
// 内部。中间隔着若干改写步骤，把结果放进 context 而不是层层透传参数，是为了让
// "有没有过门"这件事对后续每一处都可查，且**没查到就等于没过门**。
const codexClientRestrictionResultContextKey = "openai_codex_client_restriction_result"

// stageCodexClientRestrictionResult 暂存本 attempt 的门禁判定。
//
// 必须在 Forward 判定后立即调用。failover 每个 attempt 会重新判定并覆写：门禁
// 策略与账号相关（codex_cli_only 是账号级开关），上一账号的结论不得残留。
func stageCodexClientRestrictionResult(c *gin.Context, result CodexClientRestrictionDetectionResult) {
	if c != nil {
		c.Set(codexClientRestrictionResultContextKey, result)
	}
}

// stagedCodexClientRestrictionResult 读取暂存的门禁判定。
// 第二个返回值为 false 表示本请求路径根本没跑过门禁判定。
func stagedCodexClientRestrictionResult(c *gin.Context) (CodexClientRestrictionDetectionResult, bool) {
	if c == nil {
		return CodexClientRestrictionDetectionResult{}, false
	}
	value, exists := c.Get(codexClientRestrictionResultContextKey)
	if !exists {
		return CodexClientRestrictionDetectionResult{}, false
	}
	result, ok := value.(CodexClientRestrictionDetectionResult)
	return result, ok
}

// strict 降级原因。空串表示 strict 生效。
// 这些值会进 ops 记录（passthrough_mode / strict_degraded_reason），是排查
// "开了 strict 但看起来没生效"的唯一线索——strict 是个"少做事"的开关，
// 生效与否在请求本身上看不出区别，只有上游报错时才会暴露。
const (
	// OpenAIStrictPassthroughReasonAccountDisabled 账号没开 strict（或没开透传）。
	OpenAIStrictPassthroughReasonAccountDisabled = "account_strict_disabled"
	// OpenAIStrictPassthroughReasonGateNotEvaluated 本请求路径没跑过 codex_cli_only
	// 判定。fail-closed：判定缺席一律当作没过门。
	OpenAIStrictPassthroughReasonGateNotEvaluated = "client_gate_not_evaluated"
	// OpenAIStrictPassthroughReasonCodexCLIOnlyDisabled 账号没开 codex_cli_only。
	// Detect 第一步就会因此短路返回 Disabled，指纹门与版本门**根本没跑**——
	// strict 想依赖的那层保护此时并不存在。
	OpenAIStrictPassthroughReasonCodexCLIOnlyDisabled = "codex_cli_only_disabled"
	// OpenAIStrictPassthroughReasonForceCodexCLIEnabled 门禁被 gateway.force_codex_cli
	// 无条件旁路。该配置的本意是"网关未透传 UA 时的兼容兜底"，此时门是假的。
	OpenAIStrictPassthroughReasonForceCodexCLIEnabled = "force_codex_cli_enabled"
	// OpenAIStrictPassthroughReasonGateNotMatched 本请求没通过门禁。正常情况下这类
	// 请求已在 Forward 入口被 403，走不到这里；留作 fail-closed 兜底。
	OpenAIStrictPassthroughReasonGateNotMatched = "client_gate_not_matched"
)

// OpenAIPassthroughMode 是 ops 记录里的透传档位。
const (
	OpenAIPassthroughModeOff      = "off"
	OpenAIPassthroughModeAuthOnly = "auth_only"
	OpenAIPassthroughModeStrict   = "strict"
)

// resolveOpenAIStrictPassthrough 判定本次请求是否按 strict 处理。
//
// 返回 (false, reason) 表示降级为普通透传；(true, "") 表示 strict 生效。
// 降级会打一条 WARN——静默降级会让运维以为 strict 生效了。
func (s *OpenAIGatewayService) resolveOpenAIStrictPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
) (bool, string) {
	if !account.IsOpenAIPassthroughStrictEnabled() {
		// 账号没开，不是"降级"，无需告警。
		return false, OpenAIStrictPassthroughReasonAccountDisabled
	}

	reason := openAIStrictPassthroughDegradeReason(c)
	if reason == "" {
		return true, ""
	}

	logger.FromContext(ctx).Warn("openai.passthrough_strict_degraded",
		zap.Int64("account_id", account.ID),
		zap.String("account_name", account.Name),
		zap.String("reason", reason),
	)
	return false, reason
}

// openAIStrictPassthroughDegradeReason 返回账号已开 strict 时的降级原因，
// 空串表示无降级。拆成纯函数是为了让四种组合可以脱开日志与 service 单测。
func openAIStrictPassthroughDegradeReason(c *gin.Context) string {
	restriction, ok := stagedCodexClientRestrictionResult(c)
	if !ok {
		// 判定缺席：可能是新增了一条没过门禁的入口，也可能是有人重排了 Forward
		// 里那个 403 早返回。两种都不该让 strict 生效。
		return OpenAIStrictPassthroughReasonGateNotEvaluated
	}
	if !restriction.Enabled {
		return OpenAIStrictPassthroughReasonCodexCLIOnlyDisabled
	}
	if restriction.Reason == CodexClientRestrictionReasonForceCodexCLI {
		return OpenAIStrictPassthroughReasonForceCodexCLIEnabled
	}
	if !restriction.Matched {
		return OpenAIStrictPassthroughReasonGateNotMatched
	}
	return ""
}

// OpenAIPassthroughModeForOps 把账号配置与本次判定折成 ops 记录里的档位字符串。
func OpenAIPassthroughModeForOps(strict bool, passthroughEnabled bool) string {
	switch {
	case strict:
		return OpenAIPassthroughModeStrict
	case passthroughEnabled:
		return OpenAIPassthroughModeAuthOnly
	default:
		return OpenAIPassthroughModeOff
	}
}

// openAIStrictPassthroughDecisionContextKey 暂存本 attempt 的 strict 判定。
//
// 判定在 forwardOpenAIPassthrough 顶部做一次（含 WARN 日志），出站请求构造器读取
// 暂存值——否则同一 attempt 里每构造一次请求就会重复告警一次。
const openAIStrictPassthroughDecisionContextKey = "openai_passthrough_strict_decision"

// stageOpenAIStrictPassthrough 暂存本 attempt 的 strict 判定。
//
// 必须无条件覆写：failover 从 strict 账号切到普通账号时，上一账号的判定不得残留。
// 只靠 forwardOpenAIPassthrough 顶部那次覆写还不够——换到**非透传**账号时那一段
// 压根不执行，所以 `Forward` 顶部另有一次无条件复位（openai_gateway_forward.go）。
func stageOpenAIStrictPassthrough(c *gin.Context, strict bool) {
	if c != nil {
		c.Set(openAIStrictPassthroughDecisionContextKey, strict)
	}
}

// stagedOpenAIStrictPassthrough 读取暂存判定。
//
// 未暂存一律返回 false。这条 fail-closed 语义配合上面那两处复位，保住了 Non-goals
// 里那句「不碰 WS」：`buildUpstreamRequestOpenAIPassthrough` 还有一个来自
// openai_ws_http_bridge.go 的调用方，那条路径自己不做 strict 判定，读到的要么是
// 「没暂存」要么是复位后的 false。
func stagedOpenAIStrictPassthrough(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, exists := c.Get(openAIStrictPassthroughDecisionContextKey)
	if !exists {
		return false
	}
	strict, ok := value.(bool)
	return ok && strict
}

// openAIStrictPassthroughBlockedHeaders 是 strict 下**不**转发的客户端请求头。
//
// 与非 strict 的闭合白名单相反：strict 默认放行客户端头，只扣掉网关自管的那些。
// 依据是门禁——客户端的头就是官方 Codex 那一套，白名单存在的理由（"避免非标准/
// 环境噪声头触发风控"）此时没有对象，而它反而在吃掉官方确实会发的头：
// x-codex-routing-hint、x-openai-subagent、x-responsesapi-include-timing-metrics、
// x-codex-parent-thread-id，以及连字符形式的 session-id / thread-id。
//
// 分三类，删任意一条之前先想清楚它属于哪一类：
//
//	凭据与账号身份 —— 客户端自报的一律不可信，上游认证由服务端账号产生；
//	出站身份与传输 —— 由画像/压缩逻辑统一生成，客户端值会与之矛盾；
//	逐跳与来源标识 —— HTTP 语义上就不该跨跳转发。
var openAIStrictPassthroughBlockedHeaders = map[string]bool{
	// —— 凭据与账号身份 ——
	"authorization":                     true,
	"cookie":                            true,
	"cookie2":                           true,
	"x-api-key":                         true,
	"x-goog-api-key":                    true,
	"x-openai-actor-authorization":      true,
	"chatgpt-account-id":                true,
	"chatgpt-organization-id":           true,
	"chatgpt-org-id":                    true,
	"chatgpt-project-id":                true,
	"openai-organization":               true,
	"openai-project":                    true,
	"x-openai-organization":             true,
	"x-openai-project":                  true,
	"x-oai-attestation":                 true,
	"x-oai-is":                          true,
	"x-oai-is-update":                   true,
	"x-openai-internal-codex-residency": true,

	// —— 出站身份与传输 ——
	// user-agent / originator / version 随后会被 enforceCodexIdentityHeaders 收口，
	// 这里先挡一道，避免"客户端值曾短暂存在于出站头里"这种中间态。
	"user-agent":       true,
	"originator":       true,
	"version":          true,
	"host":             true,
	"content-length":   true,
	"content-encoding": true,

	// —— 逐跳与来源标识 ——
	"connection":          true,
	"keep-alive":          true,
	"proxy-connection":    true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"forwarded":           true,
	"x-forwarded-for":     true,
	"x-forwarded-host":    true,
	"x-forwarded-proto":   true,
	"x-forwarded-port":    true,
	"x-real-ip":           true,
	"true-client-ip":      true,
	"cf-connecting-ip":    true,
	"cf-ray":              true,
	"cf-ipcountry":        true,
	"x-request-id":        true,
}

// isOpenAIStrictPassthroughForwardableHeader 判断 strict 下该客户端头是否转发上游。
//
// 超时类头沿用与非 strict 相同的配置门（gateway.openai_passthrough_allow_timeout_headers）：
// 真实 Codex 不发它们，放行与否与"像不像官方"无关，属于运维策略，strict 不该顺手改。
//
// **`x-codex-installation-id` 刻意不在黑名单里**（这里与 codex-proxy-rs 不同）：
// 它在那边被挡掉是因为那边**总是**按租约生成一个；而本仓库只在指纹收敛开启时生成
// （codexFingerprintMode 默认 off）。收敛关着时挡掉它，上游会看到一个**没有**安装
// 标识的请求——比放行更不像官方客户端，与 strict 的目标相反。放行后若收敛开着，
// applyStagedCodexFingerprintHeaders 会照常覆写它，与今天行为一致。
func isOpenAIStrictPassthroughForwardableHeader(lowerKey string, allowTimeoutHeaders bool) bool {
	if lowerKey == "" {
		return false
	}
	if isOpenAIPassthroughTimeoutHeader(lowerKey) {
		return allowTimeoutHeaders
	}
	return !openAIStrictPassthroughBlockedHeaders[lowerKey]
}

// 官方 Codex app-server 发 /v1/responses 时对请求体做 zstd 压缩（对照实现
// codex-proxy-rs 的 client_sse.rs 注释写明"与官方 Codex app-server 一致"，压缩级别 3）。
// 本仓库出站一直是明文 —— 因为非 strict 路径要改写请求体，改完自然没有再压回去。
// strict 不改请求体，于是这条一致性是纯新增就能拿到的。
const openAIStrictPassthroughZstdLevel = zstd.SpeedDefault // 对应 zstd level 3

// 编码器无状态且可并发复用（EncodeAll 是线程安全的），按进程建一次即可。
// 建失败时返回 nil，调用方降级为明文出站——压缩是保真度优化，不值得为它失败一个请求。
var openAIStrictPassthroughZstdEncoder = sync.OnceValue(func() *zstd.Encoder {
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(openAIStrictPassthroughZstdLevel),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		return nil
	}
	return encoder
})

// compressOpenAIStrictPassthroughBody 按官方形态压缩出站请求体。
//
// 返回 (压缩后字节, true) 表示调用方需要设置 Content-Encoding: zstd；
// 返回 (原字节, false) 表示压缩不可用或不划算，按明文出站。
//
// **必须在所有请求体改写之后调用** —— 压缩之后再改字节等于把 body 写坏，而且
// Content-Length 会与实际长度脱节。
func compressOpenAIStrictPassthroughBody(body []byte) ([]byte, bool) {
	if len(body) == 0 {
		return body, false
	}
	encoder := openAIStrictPassthroughZstdEncoder()
	if encoder == nil {
		return body, false
	}
	compressed := encoder.EncodeAll(body, nil)
	// 压不动就别压：小请求体加上 zstd 帧头反而更大，而"更像官方"并不要求
	// 每一个请求都带 Content-Encoding —— 官方客户端自己也是按需压。
	if len(compressed) >= len(body) {
		return body, false
	}
	return compressed, true
}
