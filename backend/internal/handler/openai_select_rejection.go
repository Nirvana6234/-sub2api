package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// OpenAIGatewayRejection 是 OpenAI 网关处理函数在选号阶段写出的错误：本地直接写出，
// 主从分流的主节点把它交给从节点按同样的方式写出（handleStreamingAwareErrorWithCode），两边一致。
type OpenAIGatewayRejection struct {
	Status  int
	ErrType string
	Code    string
	Message string
	// RetryAfter 大于 0 时写 Retry-After（秒）。
	RetryAfter int
	// RoutingCapacityLimited、OpsBusinessLimitedReason 是运维标记。
	RoutingCapacityLimited   bool
	OpsBusinessLimitedReason string
	// Anthropic：按 Anthropic Messages 的错误格式写（OpenAI 分组的 /v1/messages 入口，Code 不写出）。
	Anthropic bool
	// Compat：按 Anthropic 平台分组的 /v1/responses、/v1/chat/completions 处理函数自己的错误格式写（Responses 写 code、Chat 写 type）。
	Compat bool
}

// writeOpenAIGatewayRejection 打运维标记并写出错误（流式已开始时写流内错误）。
func (h *OpenAIGatewayHandler) writeOpenAIGatewayRejection(c *gin.Context, r OpenAIGatewayRejection, streamStarted bool) {
	if r.OpsBusinessLimitedReason != "" {
		service.MarkOpsClientBusinessLimited(c, r.OpsBusinessLimitedReason)
	}
	if r.RoutingCapacityLimited {
		markOpsRoutingCapacityLimited(c)
	}
	if r.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(r.RetryAfter))
	}
	if r.Anthropic {
		h.anthropicStreamingAwareError(c, r.Status, r.ErrType, r.Message, streamStarted)
		return
	}
	h.handleStreamingAwareErrorWithCode(c, r.Status, r.ErrType, r.Code, r.Message, streamStarted, false)
}

// OpenAIMessagesNoAccountRejection：OpenAI 分组 /v1/messages 入口没选出账号时的错误（Anthropic 格式）。
// routingModel 是选号用的模型（分组的派发映射或规范化后的请求模型）；selectErr 为调度器错误，没选出账号时为 nil。
// 与 Responses 不同，这里不按调度错误细分（与原来的 Messages 处理函数一致）。
func OpenAIMessagesNoAccountRejection(ctx context.Context, diag service.ModelAvailabilityDiagnoser, apiKey *service.APIKey, routingModel, reqModel, platform string, selectErr error) OpenAIGatewayRejection {
	cls := classifyNoAccountError(ctx, diag, apiKey, routingModel, reqModel, platform)
	r := OpenAIGatewayRejection{Status: cls.Status, ErrType: cls.ErrType, Message: cls.Message, Anthropic: true}
	switch {
	case cls.ModelNotFound:
		r.OpsBusinessLimitedReason = service.OpsClientBusinessLimitedReasonLocalModelConfiguration
	case selectErr == nil:
		r.RoutingCapacityLimited = true
	default:
		r.RoutingCapacityLimited = isOpsNoAvailableAccountError(selectErr)
	}
	return r
}

// OpenAIMessagesDispatchDeniedRejection：分组不允许 /v1/messages 派发。
func OpenAIMessagesDispatchDeniedRejection() OpenAIGatewayRejection {
	return OpenAIGatewayRejection{Status: http.StatusForbidden, ErrType: "permission_error", Message: "This group does not allow /v1/messages dispatch", Anthropic: true}
}

// OpenAIMessagesRoutingModel 是 /v1/messages 入口选号用的模型：分组配置的派发映射优先，否则规范化后的请求模型。
func OpenAIMessagesRoutingModel(apiKey *service.APIKey, reqModel string) string {
	return OpenAIMessagesRoutingModelFor(apiKey, "", reqModel)
}

// OpenAIMessagesRoutingModelFor 同 OpenAIMessagesRoutingModel；resolvedPlatform 是组合平台选定的目标平台。
func OpenAIMessagesRoutingModelFor(apiKey *service.APIKey, resolvedPlatform, reqModel string) string {
	if mapped := openAIMessagesDispatchMappedModelFor(apiKey, resolvedPlatform, reqModel); mapped != "" {
		return mapped
	}
	return service.NormalizeOpenAICompatRequestedModel(reqModel)
}

// OpenAINoAccountRejection：调度器没选出账号时的错误（404 模型没有账号支持，或 503）。
// selectErr 是调度器返回的错误（没有错误、只是没选出账号时为 nil）。
func OpenAINoAccountRejection(ctx context.Context, diag service.ModelAvailabilityDiagnoser, apiKey *service.APIKey, reqModel, platform string, selectErr error) OpenAIGatewayRejection {
	cls := classifyNoAccountError(ctx, diag, apiKey, reqModel, reqModel, platform)
	r := OpenAIGatewayRejection{}
	if cls.ModelNotFound {
		r.OpsBusinessLimitedReason = service.OpsClientBusinessLimitedReasonLocalModelConfiguration
	}
	if selectErr != nil {
		cls = classifySelectionFailureError(selectErr, cls)
		r.RoutingCapacityLimited = !cls.ModelNotFound && isOpsNoAvailableAccountError(selectErr)
	} else {
		r.RoutingCapacityLimited = !cls.ModelNotFound
	}
	r.Status, r.ErrType, r.Message = cls.Status, cls.ErrType, cls.Message
	return r
}

// OpenAIFirstSelectFailureRejection：本请求第一次选号就失败（还没排除过账号、也没有自动分组可换）时的错误。
func OpenAIFirstSelectFailureRejection(ctx context.Context, diag service.ModelAvailabilityDiagnoser, apiKey *service.APIKey, reqModel, platform string, legacyCompact bool, err error) OpenAIGatewayRejection {
	if legacyCompact && errors.Is(err, service.ErrNoAvailableCompactAccounts) {
		return OpenAIGatewayRejection{
			Status: http.StatusServiceUnavailable, ErrType: "compact_not_supported", Message: "No available accounts support /responses/compact",
			RoutingCapacityLimited: isOpsNoAvailableAccountError(err),
		}
	}
	return OpenAINoAccountRejection(ctx, diag, apiKey, reqModel, platform, err)
}

// OpenAISelectOutcomeRejection：准入失败、利润否决次数用完时的错误。
func OpenAISelectOutcomeRejection(outcome OpenAISelectOutcome) OpenAIGatewayRejection {
	if outcome.Kind == OpenAISelectVetoExhausted {
		return OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: profitVetoExhaustedMessage, RoutingCapacityLimited: true}
	}
	status, errType, code, message := OpenAIAdmissionFailureResponse(outcome)
	return OpenAIGatewayRejection{Status: status, ErrType: errType, Code: code, Message: message, RoutingCapacityLimited: outcome.Kind == OpenAISelectNoWaitPlan}
}

// OpenAIConcurrencyRejection：抢并发槽失败（队列满、排队超时等）的错误。
func OpenAIConcurrencyRejection(err error, slotType string) OpenAIGatewayRejection {
	status, errType, code, message := concurrencyErrorResponse(err, slotType)
	return OpenAIGatewayRejection{Status: status, ErrType: errType, Code: code, Message: message}
}

// OpenAIBillingRejection：计费资格检查不通过的错误（与 billingErrorDetails 一致，错误码放在错误类型里）。
func OpenAIBillingRejection(err error) OpenAIGatewayRejection {
	status, code, message, retryAfter := billingErrorDetails(err)
	return OpenAIGatewayRejection{Status: status, ErrType: code, Message: message, RetryAfter: retryAfter}
}

// OpenAICyberSessionBlockedRejection：会话被 cyber 策略屏蔽的错误（Responses / Chat 的格式）。
func OpenAICyberSessionBlockedRejection() OpenAIGatewayRejection {
	return OpenAIGatewayRejection{Status: http.StatusForbidden, ErrType: "permission_error", Code: "session_blocked_by_cyber_policy", Message: cyberSessionBlockedClientMsg}
}

// OpenAIResponsesRequiredCapability 见 openAIResponsesRequiredCapabilityForRequest。
func OpenAIResponsesRequiredCapability(imageIntent, needsResponses bool, platform string) service.OpenAIEndpointCapability {
	return openAIResponsesRequiredCapabilityForRequest(imageIntent, needsResponses, platform)
}

// OpenAIChannelForwardModel 见 openAIChannelForwardModel。
func OpenAIChannelForwardModel(mapping service.ChannelMappingResult, requestedModel string) string {
	return openAIChannelForwardModel(mapping, requestedModel)
}
