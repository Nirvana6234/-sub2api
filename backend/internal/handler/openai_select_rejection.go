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
	h.handleStreamingAwareErrorWithCode(c, r.Status, r.ErrType, r.Code, r.Message, streamStarted, false)
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
