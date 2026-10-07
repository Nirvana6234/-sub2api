package handler

import (
	"context"
	"net/http"
	"strconv"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Gemini 原生入口（GeminiV1BetaModels）在主从分流里的几处：选号经主节点（与 Messages 一样一轮只做选号与准入，
// 换号状态 FailoverState 在从节点的处理函数里），错误按 Google 格式写。主节点的拒绝只带状态码、文案、Retry-After
// 和运维标记，格式由这里决定。

// GeminiUserSlotRejection 是用户并发槽拿不到的错误（本地 GeminiV1BetaModels：googleConcurrencyError，状态码和文案按并发错误统一映射）。
func GeminiUserSlotRejection(err error) OpenAIGatewayRejection {
	status, _, _, message := concurrencyErrorResponse(err, "user")
	return OpenAIGatewayRejection{Status: status, Message: message}
}

// GeminiPricingUnavailableRejection 是模型没有可用价格时的错误（本地 GeminiV1BetaModels：503，Google 错误格式）。
func GeminiPricingUnavailableRejection() OpenAIGatewayRejection {
	return OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, Message: pricingUnavailableMessage}
}

// GeminiFirstSelectFailureRejection 是第一次就选不出账号的错误（本地 GeminiV1BetaModels：按模型不存在分类，否则带调度器的错误）。
func GeminiFirstSelectFailureRejection(ctx context.Context, diag service.ModelAvailabilityDiagnoser, apiKey *service.APIKey, model string, selectErr error) OpenAIGatewayRejection {
	r, modelNotFound := AnthropicFirstSelectFailureRejection(ctx, diag, apiKey, model, service.PlatformGemini, selectErr)
	if !modelNotFound {
		r.Message = "No available Gemini accounts: " + selectErr.Error()
	}
	return r
}

// GeminiSelectOutcomeRejection 是准入失败（没有等待计划、队列满、抢槽出错）的错误（本地 GeminiV1BetaModels 的文案）。
func GeminiSelectOutcomeRejection(outcome AnthropicSelectOutcome) OpenAIGatewayRejection {
	switch outcome.Kind {
	case AnthropicSelectQueueFull:
		return OpenAIGatewayRejection{Status: http.StatusTooManyRequests, Message: "Too many pending requests, please retry later"}
	case AnthropicSelectSlotError:
		return OpenAIGatewayRejection{Status: http.StatusTooManyRequests, Message: outcome.Err.Error()}
	default:
		return OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, Message: "No available Gemini accounts", RoutingCapacityLimited: true}
	}
}

// writeGeminiGatewayRejection 按 GeminiV1BetaModels 的写法写出选号阶段的错误（运维标记、Retry-After、Google 格式）。
func writeGeminiGatewayRejection(c *gin.Context, r OpenAIGatewayRejection) {
	if r.OpsBusinessLimitedReason != "" {
		service.MarkOpsClientBusinessLimited(c, r.OpsBusinessLimitedReason)
	}
	if r.RoutingCapacityLimited {
		markOpsRoutingCapacityLimited(c)
	}
	if r.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(r.RetryAfter))
	}
	googleError(c, r.Status, r.Message)
}

// relayGeminiSelect 是从节点上 GeminiV1BetaModels 主循环的一轮选号（本地 AnthropicAccountAdmitter.SelectAndAdmit）：
// 经主节点选号，结果转成本地的 AnthropicSelectOutcome，交给同一段循环处理。会话键、粘性绑定的账号由主节点定
// （内容摘要会话匹配也在主节点），第一次选号的结果带回 sessionKey、sessionBound。written 为 true 时已按主节点的拒绝写好响应。
func (h *GatewayHandler) relayGeminiSelect(
	c *gin.Context,
	fs *FailoverState,
	apiKey *service.APIKey,
	model, sessionHash, digestChain string,
	stream bool,
	reqLog *zap.Logger,
) (outcome AnthropicSelectOutcome, attempt *OpenAIRelayAttempt, written bool) {
	res := h.relay.Select(c, OpenAIRelaySelectRequest{
		Gemini: true, APIKey: apiKey, Model: model, Stream: stream, SessionHash: sessionHash, GeminiDigestChain: digestChain,
		Excluded: fs.FailedAccountIDs,
	})
	ctx := c.Request.Context()
	if r := res.Rejection; r != nil {
		switch r.Kind {
		case OpenAIRelayRejectFailoverExhausted:
			// 主节点只在带了已排除账号时这样回：照本地的选号耗尽处理（HandleSelectionExhausted）。
			return AnthropicSelectOutcome{Kind: AnthropicSelectFailed, Ctx: ctx, Err: service.ErrNoAvailableAccounts}, nil, false
		case OpenAIRelayRejectProfitVetoed:
			return AnthropicSelectOutcome{Kind: AnthropicSelectProfitVetoed, Account: &service.Account{ID: r.VetoedAccountID}, Ctx: ctx}, nil, false
		}
		h.writeGeminiRelayRejection(c, r, fs, reqLog)
		return AnthropicSelectOutcome{}, nil, true
	}
	a := res.Attempt
	setOpsSelectedAccount(c, a.Account.ID, a.Account.Platform)
	if a.SingleAccountRetry && !fs.relaySingleAccountSet {
		// 单账号分组提前设 SingleAccountRetry（本地在请求开始时设一次，之后一直在请求 ctx 里）。
		fs.relaySingleAccountSet = true
		c.Request = c.Request.WithContext(service.WithSingleAccountRetry(c.Request.Context(), true, h.metadataBridgeEnabled()))
	}
	if a.MaxAccountSwitches > 0 {
		fs.MaxSwitches = a.MaxAccountSwitches
	}
	return AnthropicSelectOutcome{Kind: AnthropicSelected, Account: a.Account, Release: func() { h.relay.AttemptDone(c, a) }, Ctx: ctx}, a, false
}

// writeGeminiRelayRejection 按 GeminiV1BetaModels 的写法写出主节点的拒绝。
func (h *GatewayHandler) writeGeminiRelayRejection(c *gin.Context, r *OpenAIRelayRejection, fs *FailoverState, reqLog *zap.Logger) {
	switch r.Kind {
	case OpenAIRelayRejectRaw:
		middleware2.WriteCapturedRejection(c, r.Raw)
	case OpenAIRelayRejectUnsupported:
		if c.Writer.Written() {
			reqLog.Error("gemini.relay_handoff_after_response_started")
			googleError(c, http.StatusBadGateway, "Upstream request failed")
			return
		}
		h.relay.HandOff(c)
	case OpenAIRelayRejectUnavailable:
		// 主节点不可达：不重试、不换号（设计 3.1）。第一次尝试按 503 写，之后按最近一次上游错误写。
		reqLog.Warn("gemini.relay_master_unavailable")
		if fs.LastFailoverErr != nil {
			h.handleGeminiFailoverExhausted(c, fs.LastFailoverErr)
			return
		}
		googleError(c, http.StatusServiceUnavailable, "Service temporarily unavailable")
	default:
		writeGeminiGatewayRejection(c, r.Gateway)
	}
}
