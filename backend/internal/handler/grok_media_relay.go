package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GrokMediaSelectFailure 是媒体入口（handleGrokMedia）选号失败时分类用的事实：主节点按它生成与本地同样的错误。
type GrokMediaSelectFailure struct {
	Endpoint     service.GrokMediaEndpoint
	RequestModel string
	RoutingModel string
	Platform     string
	// Bound：这次是任务查询（按任务绑定的账号选号）。
	Bound bool
	// NoneExcluded：这次请求还没排除过账号（本地 len(failedAccountIDs)==0）。
	NoneExcluded bool
	// EligibilityOnly：排除过账号但都是"没有生成资格"，没有失败换号的错误（本地 mediaEligibilityRejected && lastFailoverErr == nil）。
	EligibilityOnly bool
	// SelectErr 是调度器的错误；没选出账号也没有错误时为 nil。
	SelectErr error
}

// GrokMediaNoAccountCode 是生成类入口没有可用账号时的错误类型和消息（Seedance 与 Grok 不同）。
func GrokMediaNoAccountCode(endpoint service.GrokMediaEndpoint) (string, string) {
	if endpoint.IsSeedance() {
		return "seedance_no_eligible_account", "No eligible Seedance accounts"
	}
	return "grok_media_no_eligible_account", "No eligible Grok media accounts"
}

// GrokMediaVideoNotFoundRejection 是任务没有绑定账号（或绑定的账号不可用）时的错误。
func GrokMediaVideoNotFoundRejection() OpenAIGatewayRejection {
	return OpenAIGatewayRejection{Status: http.StatusNotFound, ErrType: "not_found_error", Message: "Video request not found"}
}

// GrokMediaSelectFailureRejection 与 handleGrokMedia 选号失败时写的错误一致。ok 为 false 表示这时本地会按换号用完处理
// （最近一次上游错误），由从节点写。
func GrokMediaSelectFailureRejection(ctx context.Context, diag service.ModelAvailabilityDiagnoser, apiKey *service.APIKey, f GrokMediaSelectFailure) (OpenAIGatewayRejection, bool) {
	if f.SelectErr != nil {
		if f.Bound && errors.Is(f.SelectErr, service.ErrNoAvailableAccounts) {
			return GrokMediaVideoNotFoundRejection(), true
		}
		if f.Endpoint.IsGenerationRequest() && errors.Is(f.SelectErr, service.ErrNoAvailableAccounts) && (f.NoneExcluded || f.EligibilityOnly) {
			code, message := GrokMediaNoAccountCode(f.Endpoint)
			return OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, ErrType: code, Message: message, RoutingCapacityLimited: true}, true
		}
		if !f.NoneExcluded {
			return OpenAIGatewayRejection{}, false
		}
	}
	// 没选出账号（调度器没返回错误），或第一次选号就出错：生成类入口回"没有可用的媒体账号"，其余按模型可用性分类。
	if f.SelectErr == nil && f.Endpoint.IsGenerationRequest() {
		code, message := GrokMediaNoAccountCode(f.Endpoint)
		return OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, ErrType: code, Message: message, RoutingCapacityLimited: true}, true
	}
	cls := classifyNoAccountError(ctx, diag, apiKey, f.RequestModel, f.RoutingModel, f.Platform)
	r := OpenAIGatewayRejection{Status: cls.Status, ErrType: cls.ErrType, Message: cls.Message}
	switch {
	case cls.ModelNotFound:
		r.OpsBusinessLimitedReason = service.OpsClientBusinessLimitedReasonLocalModelConfiguration
	case f.SelectErr == nil:
		r.RoutingCapacityLimited = true
	default:
		r.RoutingCapacityLimited = isOpsNoAvailableAccountError(f.SelectErr)
	}
	return r, true
}

// GrokMediaEligibilityExhaustedRejection：没有生成资格的账号换了 maxAccountSwitches 次还是没有。
func GrokMediaEligibilityExhaustedRejection() OpenAIGatewayRejection {
	return OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, ErrType: "grok_media_no_eligible_account", Message: "No eligible Grok media accounts", RoutingCapacityLimited: true}
}

// writeGrokMediaRelayRejection 按媒体入口自己的写法写出主节点的拒绝（错误体没有 code，除非是并发错误）。
func (h *OpenAIGatewayHandler) writeGrokMediaRelayRejection(c *gin.Context, r *OpenAIRelayRejection, lastFailoverErr *service.UpstreamFailoverError) {
	switch r.Kind {
	case OpenAIRelayRejectRaw:
		middleware2.WriteCapturedRejection(c, r.Raw)
	case OpenAIRelayRejectUnsupported:
		if c.Writer.Written() {
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return
		}
		h.relay.HandOff(c)
	case OpenAIRelayRejectFailoverExhausted:
		if lastFailoverErr != nil {
			h.handleFailoverExhausted(c, lastFailoverErr, false)
			return
		}
		h.errorResponse(c, http.StatusBadGateway, "api_error", "Upstream request failed")
	case OpenAIRelayRejectUnavailable:
		if lastFailoverErr != nil {
			h.handleFailoverExhausted(c, lastFailoverErr, false)
			return
		}
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable")
	default:
		g := r.Gateway
		if g.OpsBusinessLimitedReason != "" {
			service.MarkOpsClientBusinessLimited(c, g.OpsBusinessLimitedReason)
		}
		if g.RoutingCapacityLimited {
			markOpsRoutingCapacityLimited(c)
		}
		if g.RetryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(g.RetryAfter))
		}
		if g.Code != "" {
			h.handleStreamingAwareErrorWithCode(c, g.Status, g.ErrType, g.Code, g.Message, false, false)
			return
		}
		h.errorResponse(c, g.Status, g.ErrType, g.Message)
	}
}

// isGrokVideoTaskCompletion 报告这次状态 / 内容轮询的转发结果是不是看到了完成的视频（本地才有计费要做）。
func isGrokVideoTaskCompletion(endpoint service.GrokMediaEndpoint, result *service.OpenAIForwardResult) bool {
	if result == nil {
		return false
	}
	switch endpoint {
	case service.SeedanceEndpointStatus:
		return result.Usage.OutputTokens > 0
	case service.GrokMediaEndpointVideoStatus, service.GrokMediaEndpointVideoContent:
		return result.VideoCount > 0
	}
	return false
}

// grokMediaPayloadHash 是媒体用量行的请求指纹：有请求体取请求体，没有（查询）取任务 ID。
func grokMediaPayloadHash(body []byte, requestID string) string {
	payload := body
	if len(payload) == 0 && strings.TrimSpace(requestID) != "" {
		payload = []byte(requestID)
	}
	return service.HashUsageRequestPayload(payload)
}
