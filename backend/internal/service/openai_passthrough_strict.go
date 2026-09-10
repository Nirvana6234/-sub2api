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

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
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
