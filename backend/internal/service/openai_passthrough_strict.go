package service

// 本文件承载"严格字节保真透传"（accounts.extra.openai_passthrough_strict）的
// 生效判定。开关本身叠加在 openai_passthrough 之上，取消的是透传路径上那些为
// 非官方客户端兜底的规范化（强制 store、删不支持字段、input 归一、合成
// instructions、请求头闭合白名单）。
//
// 这些兜底能取消的**唯一依据**是：本次请求确实来自官方 Codex 客户端。所以判定是
// **逐请求**的，不是逐账号的：
//
//  1. 账号配置开了 strict（且开了透传）；
//  2. **这一条请求**被判定为官方 Codex 客户端发出（UA / originator / 白名单 /
//     app-server，再过版本门与引擎指纹门）。
//
// 第 2 条不成立就**回落到普通自动透传**，照常服务——这是本开关与 codex_cli_only
// 的根本区别：那个开关的语义是"不是 Codex 就 403 拒掉"，这个开关的语义是
// "是 Codex 就多给一点保真度，不是就按老样子发"。两者互相独立，可以单开。
//
// 判定复用 EvaluateCodexClientIdentity（与 codex_cli_only 同一份实现），但**不认**
// gateway.force_codex_cli 旁路——那条是无条件放行，证明不了来路。
//
// 已知的残留风险（2026-09-11 与用户确认后接受）：判定依据里的 UA / originator 是
// 客户端自报的，可以伪造。伪造的后果是该请求的请求头按黑名单放行（而非白名单裁剪）、
// body 原样上送；凭据与身份类请求头仍然一律剥除。这是一个有意的取舍，不是疏漏。

import (
	"context"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"go.uber.org/zap"
)

// codexClientIdentityContextKey 暂存本次请求的 Codex 身份判定结果。
//
// 判定发生在 Forward 的最前面（早于任何 body 处理），而 strict 的分叉在透传分支
// 内部。中间隔着若干改写步骤，把结果放进 context 而不是层层透传参数，是为了让
// "这条请求是不是 Codex"对后续每一处都可查，且**没查到就等于不是**。
const codexClientIdentityContextKey = "openai_codex_client_identity_result"

// stageCodexClientIdentity 暂存本 attempt 的身份判定。
//
// 必须在 Forward 判定后立即调用。failover 每个 attempt 会重新判定并覆写：判定要
// 吃账号维度的策略（app-server 放行、全局策略的取数条件），上一账号的结论不得残留。
func stageCodexClientIdentity(c *gin.Context, result CodexClientRestrictionDetectionResult) {
	if c != nil {
		c.Set(codexClientIdentityContextKey, result)
	}
}

// stagedCodexClientIdentity 读取暂存的身份判定。
// 第二个返回值为 false 表示本请求路径根本没跑过身份判定。
func stagedCodexClientIdentity(c *gin.Context) (CodexClientRestrictionDetectionResult, bool) {
	if c == nil {
		return CodexClientRestrictionDetectionResult{}, false
	}
	value, exists := c.Get(codexClientIdentityContextKey)
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
	// OpenAIStrictPassthroughReasonGateNotEvaluated 本请求路径没跑过身份判定。
	// fail-closed：判定缺席一律当作"不是 Codex"。这是**代码路径问题**（新增了一条
	// 绕过 Forward 的入口，或有人重排了暂存顺序），不是配置问题——唯一该告警的一种。
	OpenAIStrictPassthroughReasonGateNotEvaluated = "client_gate_not_evaluated"
	// OpenAIStrictPassthroughReasonGateNotMatched 这条请求不是官方 Codex 客户端发的。
	// **这是设计内的正常路径**，不是故障：Chat 前端、curl、各类 SDK 打到 strict 账号
	// 上都会落在这里，然后按普通自动透传照常服务。因此不打 WARN。
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
// 返回 (false, reason) 表示回落为普通透传；(true, "") 表示 strict 生效。
//
// 只有 client_gate_not_evaluated 会打 WARN：那意味着判定压根没跑，属于代码路径
// 缺陷。"不是 Codex 客户端"是设计内的正常路径，每条请求告警一次只会把日志淹掉。
func (s *OpenAIGatewayService) resolveOpenAIStrictPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
) (bool, string) {
	if !account.IsOpenAIPassthroughStrictEnabled() {
		return false, OpenAIStrictPassthroughReasonAccountDisabled
	}

	reason := openAIStrictPassthroughDegradeReason(c)
	if reason == "" {
		return true, ""
	}

	if reason == OpenAIStrictPassthroughReasonGateNotEvaluated {
		logger.FromContext(ctx).Warn("openai.passthrough_strict_degraded",
			zap.Int64("account_id", account.ID),
			zap.String("account_name", account.Name),
			zap.String("reason", reason),
		)
	}
	return false, reason
}

// openAIStrictPassthroughDegradeReason 返回账号已开 strict 时的回落原因，
// 空串表示 strict 生效。拆成纯函数是为了让各种组合可以脱开日志与 service 单测。
func openAIStrictPassthroughDegradeReason(c *gin.Context) string {
	identity, ok := stagedCodexClientIdentity(c)
	if !ok || !identity.Enabled {
		// 判定缺席：可能是新增了一条不经 Forward 的入口，也可能是有人重排了暂存。
		// 两种都不该让 strict 生效。
		return OpenAIStrictPassthroughReasonGateNotEvaluated
	}
	if !identity.Matched {
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
