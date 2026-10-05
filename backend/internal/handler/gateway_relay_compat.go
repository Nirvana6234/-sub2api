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
	"go.uber.org/zap"
)

// Anthropic / Gemini / Antigravity 平台分组的 /v1/responses、/v1/chat/completions 在主从分流里的几处：选号经主节点（一轮选号与准入，
// 换号状态在从节点），错误按这两个处理函数自己的 OpenAI 兼容格式由从节点写。

// GatewayCompatFirstSelectFailureRejection 是第一次就选不出账号的错误（本地 Responses / ChatCompletions：按模型不存在分类，
// 再按限流诊断改写，否则带调度器的错误）。platform 是这次请求的有效平台。
func GatewayCompatFirstSelectFailureRejection(ctx context.Context, diag service.ModelAvailabilityDiagnoser, apiKey *service.APIKey, model, platform string, selectErr error) OpenAIGatewayRejection {
	cls := classifyNoAccountError(ctx, diag, apiKey, model, model, platform)
	cls = classifySelectionFailureError(selectErr, cls)
	r := OpenAIGatewayRejection{Status: cls.Status, ErrType: cls.ErrType, Message: cls.Message, Compat: true}
	if cls.ModelNotFound {
		r.OpsBusinessLimitedReason = service.OpsClientBusinessLimitedReasonLocalModelConfiguration
	} else {
		r.RoutingCapacityLimited = isOpsNoAvailableAccountError(selectErr)
		r.Message = "No available accounts: " + selectErr.Error()
	}
	return r
}

// GatewayCompatSelectOutcomeRejection 是准入失败（没有等待计划、抢槽出错）的错误：没有等待计划按 OpenAI 兼容格式写，抢槽出错与
// Messages 一样走并发错误的写法（handleConcurrencyError）。这两个处理函数不计账号排队数，没有"队列满"。
func GatewayCompatSelectOutcomeRejection(outcome AnthropicSelectOutcome) OpenAIGatewayRejection {
	if outcome.Kind == AnthropicSelectSlotError {
		status, errType, code, message := concurrencyErrorResponse(outcome.Err, "account")
		return OpenAIGatewayRejection{Status: status, ErrType: errType, Code: code, Message: message}
	}
	return OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: "No available accounts", RoutingCapacityLimited: true, Compat: true}
}

// compatKind 区分 GatewayHandler 上的两个 OpenAI 兼容入口。
type compatKind int

const (
	compatResponses compatKind = iota
	compatChat
)

// writeCompatError 按这两个处理函数各自的错误格式写（Responses 写 code，Chat 写 type）。
func (h *GatewayHandler) writeCompatError(c *gin.Context, kind compatKind, status int, errType, message string) {
	if kind == compatChat {
		h.chatCompletionsErrorResponse(c, status, errType, message)
		return
	}
	h.responsesErrorResponse(c, status, errType, message)
}

// writeCompatFailoverExhausted 按各自的格式写换号用完的错误。
func (h *GatewayHandler) writeCompatFailoverExhausted(c *gin.Context, kind compatKind, lastErr *service.UpstreamFailoverError, streamStarted bool) {
	if kind == compatChat {
		h.handleCCFailoverExhausted(c, lastErr, streamStarted)
		return
	}
	h.handleResponsesFailoverExhausted(c, lastErr, streamStarted)
}

// writeCompatRejection 写出主节点的 Gateway 拒绝：Compat 的按处理函数的格式（运维标记、Retry-After），其余（并发错误）与
// Messages 一样按流式感知的写法。
func (h *GatewayHandler) writeCompatRejection(c *gin.Context, kind compatKind, r OpenAIGatewayRejection, streamStarted bool) {
	if !r.Compat {
		h.writeGatewayRejection(c, r, streamStarted)
		return
	}
	if r.OpsBusinessLimitedReason != "" {
		service.MarkOpsClientBusinessLimited(c, r.OpsBusinessLimitedReason)
	}
	if r.RoutingCapacityLimited {
		markOpsRoutingCapacityLimited(c)
	}
	if r.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(r.RetryAfter))
	}
	h.writeCompatError(c, kind, r.Status, r.ErrType, r.Message)
}

// relayCompatSelect 是从节点上 Responses / ChatCompletions 主循环的一轮选号：经主节点选号，结果转成本地的 AnthropicSelectOutcome。
// 主节点标了"自动分组换组"的拒绝（Chat 入口、第一次就选不出账号）转成选号失败，错误里带着这个拒绝（relayAutoGroupFailoverError）。
// written 为 true 时已按主节点的拒绝写好响应。
func (h *GatewayHandler) relayCompatSelect(c *gin.Context, kind compatKind, fs *FailoverState, apiKey *service.APIKey, model string, stream bool, sessionHash string,
	streamStarted bool, reqLog *zap.Logger,
) (outcome AnthropicSelectOutcome, attempt *OpenAIRelayAttempt, written bool) {
	res := h.relay.Select(c, OpenAIRelaySelectRequest{
		GatewayResponses: kind == compatResponses, GatewayChat: kind == compatChat,
		APIKey: apiKey, Model: model, Stream: stream, SessionHash: sessionHash, Excluded: fs.FailedAccountIDs,
	})
	ctx := c.Request.Context()
	if r := res.Rejection; r != nil {
		switch {
		case r.Kind == OpenAIRelayRejectFailoverExhausted:
			return AnthropicSelectOutcome{Kind: AnthropicSelectFailed, Ctx: ctx, Err: service.ErrNoAvailableAccounts}, nil, false
		case r.Kind == OpenAIRelayRejectProfitVetoed:
			return AnthropicSelectOutcome{Kind: AnthropicSelectProfitVetoed, Account: &service.Account{ID: r.VetoedAccountID}, Ctx: ctx}, nil, false
		case r.AutoGroupFailover:
			return AnthropicSelectOutcome{Kind: AnthropicSelectFailed, Ctx: ctx, Err: &relayAutoGroupFailoverError{rejection: r}}, nil, false
		}
		h.writeCompatRelayRejection(c, kind, r, fs, streamStarted, reqLog)
		return AnthropicSelectOutcome{}, nil, true
	}
	a := res.Attempt
	setOpsSelectedAccount(c, a.Account.ID, a.Account.Platform)
	if a.SingleAccountRetry && !fs.relaySingleAccountSet {
		fs.relaySingleAccountSet = true
		c.Request = c.Request.WithContext(service.WithSingleAccountRetry(c.Request.Context(), true, h.metadataBridgeEnabled()))
	}
	if a.MaxAccountSwitches > 0 {
		fs.MaxSwitches = a.MaxAccountSwitches
	}
	return AnthropicSelectOutcome{Kind: AnthropicSelected, Account: a.Account, Release: func() { h.relay.AttemptDone(c, a) }, Ctx: ctx}, a, false
}

// writeCompatRelayRejection 写出主节点的拒绝（自动分组换组之外的）。
func (h *GatewayHandler) writeCompatRelayRejection(c *gin.Context, kind compatKind, r *OpenAIRelayRejection, fs *FailoverState, streamStarted bool, reqLog *zap.Logger) {
	switch r.Kind {
	case OpenAIRelayRejectRaw:
		middleware2.WriteCapturedRejection(c, r.Raw)
	case OpenAIRelayRejectUnsupported:
		if streamStarted || c.Writer.Written() {
			reqLog.Error("gateway.relay_handoff_after_response_started")
			h.writeCompatError(c, kind, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return
		}
		h.relay.HandOff(c)
	case OpenAIRelayRejectUnavailable:
		// 主节点不可达：不重试、不换号（设计 3.1）。第一次尝试按 503 写，之后按最近一次上游错误写。
		reqLog.Warn("gateway.relay_master_unavailable")
		if fs.LastFailoverErr != nil {
			h.writeCompatFailoverExhausted(c, kind, fs.LastFailoverErr, streamStarted)
			return
		}
		h.writeCompatError(c, kind, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable")
	default:
		h.writeCompatRejection(c, kind, r.Gateway, streamStarted)
	}
}

// writeCompatAutoGroupRejection：自动分组换组没换成时，选号错误是主节点的拒绝就按它写（与没有自动分组时一样）。
func (h *GatewayHandler) writeCompatAutoGroupRejection(c *gin.Context, kind compatKind, err error, fs *FailoverState, streamStarted bool, reqLog *zap.Logger) bool {
	var relayErr *relayAutoGroupFailoverError
	if !errors.As(err, &relayErr) {
		return false
	}
	h.writeCompatRelayRejection(c, kind, relayErr.rejection, fs, streamStarted, reqLog)
	return true
}

// tryAutoGroupFailoverCompat 是 ChatCompletions 里的自动分组换组：单机按本机的选组器选，从节点经主节点选（与 OpenAI 处理函数同一套）。
func (h *GatewayHandler) tryAutoGroupFailoverCompat(c *gin.Context, apiKey **service.APIKey, model string, failedGroupIDs map[int64]struct{}, subscription **service.UserSubscription) bool {
	if h.relay == nil {
		return tryOpenAIAutoGroupFailover(c, h.apiKeyService, apiKey, model, failedGroupIDs, subscription)
	}
	return tryAutoGroupFailoverWith(c, func(key *service.APIKey, _ *service.UserSubscription) (*service.APIKey, *service.UserSubscription, bool) {
		sw, ok := h.relay.SwitchAutoGroup(c, key, strings.TrimSpace(model), failedGroupIDs)
		if !ok || sw.APIKey == nil || sw.APIKey.GroupID == nil {
			return nil, nil, false
		}
		if _, alreadyTried := failedGroupIDs[*sw.APIKey.GroupID]; alreadyTried {
			return nil, nil, false
		}
		c.Set(relayAutoGroupBillingKey, sw.BillingRejection)
		return sw.APIKey, sw.Subscription, true
	}, apiKey, model, failedGroupIDs, subscription)
}

// compatAutoGroupBillingError 是换组之后的计费资格复查：单机本地查；从节点用换组时主节点查好的结果。
func (h *GatewayHandler) compatAutoGroupBillingError(c *gin.Context, apiKey *service.APIKey, subscription *service.UserSubscription) error {
	if h.relay == nil {
		return h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey))
	}
	v, _ := c.Get(relayAutoGroupBillingKey)
	if r, ok := v.(*OpenAIGatewayRejection); ok && r != nil {
		return &relayBillingRejectionError{rejection: *r}
	}
	return nil
}

// OpenAIEmbeddingsFirstSelectFailureRejection：Embeddings 第一次就选不出账号时的错误（本地 Embeddings：只按模型不存在分类，
// 文案取分类结果，不按限流诊断改写）。
func OpenAIEmbeddingsFirstSelectFailureRejection(ctx context.Context, diag service.ModelAvailabilityDiagnoser, apiKey *service.APIKey, model string, selectErr error) OpenAIGatewayRejection {
	cls := classifyNoAccountError(ctx, diag, apiKey, model, model, service.PlatformOpenAI)
	r := OpenAIGatewayRejection{Status: cls.Status, ErrType: cls.ErrType, Message: cls.Message}
	if cls.ModelNotFound {
		r.OpsBusinessLimitedReason = service.OpsClientBusinessLimitedReasonLocalModelConfiguration
	} else {
		r.RoutingCapacityLimited = isOpsNoAvailableAccountError(selectErr)
	}
	return r
}
