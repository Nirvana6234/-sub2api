package handler

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SetRelayDispatcher 让 Messages 在主从分流的从节点上运行：用户槽、计费资格、选号与准入、粘性会话、记用量
// 这些主节点的步骤经 d 远程完成（开发计划 WP10）；其余步骤与单机同一份代码。
func (h *GatewayHandler) SetRelayDispatcher(d OpenAIRelayDispatcher) {
	h.relay = d
}

// relayAnthropicSelect 是从节点上 Messages 主循环的一轮选号（本地 AnthropicAccountAdmitter.SelectAndAdmit）：
// 经主节点选号，结果转成本地的 AnthropicSelectOutcome，交给同一段循环处理（换号状态 fs 在这里，与单机一样）。
// written 为 true 时已按主节点的拒绝写好响应，调用方直接返回。
func (h *GatewayHandler) relayAnthropicSelect(
	c *gin.Context,
	fs *FailoverState,
	apiKey *service.APIKey,
	model string,
	stream bool,
	sessionKey string,
	parsed *service.ParsedRequest,
	body []byte,
	isClaudeCodeClient bool,
	platform string,
	streamStarted *bool,
	reqLog *zap.Logger,
) (outcome AnthropicSelectOutcome, attempt *OpenAIRelayAttempt, written bool) {
	onTick, _ := h.anthropicAdmissionWaitHooks(c, stream, streamStarted)
	res := h.relay.Select(c, OpenAIRelaySelectRequest{
		Anthropic: true, APIKey: apiKey, Model: model, Stream: stream, SessionHash: sessionKey,
		MetadataUserID: parsed.MetadataUserID, InterceptType: detectInterceptType(body, model, parsed.MaxTokens, isClaudeCodeClient),
		Excluded: fs.FailedAccountIDs, Body: body,
		OnTick: onTick, TickInterval: h.concurrencyHelper.pingInterval,
	})
	ctx := c.Request.Context()
	if r := res.Rejection; r != nil {
		switch r.Kind {
		case OpenAIRelayRejectFailoverExhausted:
			// 主节点只在带了已排除账号时这样回：照本地的选号耗尽处理（HandleSelectionExhausted）。
			return AnthropicSelectOutcome{Kind: AnthropicSelectFailed, Ctx: ctx, Err: service.ErrNoAvailableAccounts}, nil, false
		case OpenAIRelayRejectIntercepted:
			return AnthropicSelectOutcome{Kind: AnthropicSelectIntercepted, Ctx: ctx, Intercept: r.InterceptType}, nil, false
		case OpenAIRelayRejectProfitVetoed:
			return AnthropicSelectOutcome{Kind: AnthropicSelectProfitVetoed, Account: &service.Account{ID: r.VetoedAccountID}, Ctx: ctx}, nil, false
		}
		h.writeAnthropicRelayRejection(c, r, fs, platform, *streamStarted, reqLog)
		return AnthropicSelectOutcome{}, nil, true
	}
	a := res.Attempt
	setOpsSelectedAccount(c, a.Account.ID, a.Account.Platform)
	reqLog.Info("sticky.account_selected",
		zap.Int64("selected_account_id", a.Account.ID),
		zap.String("account_name", a.Account.Name),
		zap.Int64("sticky_bound_account_id", a.StickyBoundAccountID),
		zap.Bool("sticky_honored", a.StickyBoundAccountID > 0 && a.StickyBoundAccountID == a.Account.ID),
	)
	if a.SingleAccountRetry && !fs.relaySingleAccountSet {
		// 单账号分组提前设 SingleAccountRetry（本地在请求开始时设一次，之后一直在请求 ctx 里）。
		fs.relaySingleAccountSet = true
		c.Request = c.Request.WithContext(service.WithSingleAccountRetry(c.Request.Context(), true, h.metadataBridgeEnabled()))
	}
	if a.MaxAccountSwitches > 0 {
		fs.MaxSwitches = a.MaxAccountSwitches
	}
	// 有绑定的会话换号时强制按缓存计费（needForceCacheBilling）：绑定在主节点请求开始时查一次，这里也只取第一次的。
	if !fs.relayStickyKnown {
		fs.relayStickyKnown = true
		fs.hasBoundSession = sessionKey != "" && a.StickyBoundAccountID > 0
	}
	return AnthropicSelectOutcome{Kind: AnthropicSelected, Account: a.Account, Release: func() { h.relay.AttemptDone(c, a) }, Ctx: ctx}, a, false
}

// writeAnthropicRelayRejection 按 Messages 的写法写出主节点的拒绝。
func (h *GatewayHandler) writeAnthropicRelayRejection(c *gin.Context, r *OpenAIRelayRejection, fs *FailoverState, platform string, streamStarted bool, reqLog *zap.Logger) {
	switch r.Kind {
	case OpenAIRelayRejectRaw:
		middleware2.WriteCapturedRejection(c, r.Raw)
	case OpenAIRelayRejectUnsupported:
		if streamStarted || c.Writer.Written() {
			reqLog.Error("gateway.relay_handoff_after_response_started")
			h.handleStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
			return
		}
		h.relay.HandOff(c)
	case OpenAIRelayRejectUnavailable:
		// 主节点不可达：不重试、不换号（设计 3.1）。第一次尝试按 503 写，之后按最近一次上游错误写。
		reqLog.Warn("gateway.relay_master_unavailable")
		if fs.LastFailoverErr != nil {
			h.handleFailoverExhausted(c, fs.LastFailoverErr, platform, streamStarted)
			return
		}
		h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", streamStarted)
	default:
		h.writeGatewayRejection(c, r.Gateway, streamStarted)
	}
}

// SetSecurityAuditCoordinator 装上安全审计协调器（从节点装配用，与 OpenAI 处理函数的同名方法一致）。
func (h *GatewayHandler) SetSecurityAuditCoordinator(c *securityaudit.Coordinator) {
	h.securityAuditCoordinator = c
}

// antigravityFallbackAPIKey 是 Antigravity 回 prompt 过长时换到兜底分组的前半段（本地 Messages 里 PromptTooLongError 分支）：
// 解析兜底分组、核对它合规（Anthropic 平台、非订阅、自己没有兜底）、做计费资格复查，换上兜底分组的 Key。
// 从节点经主节点做（分组、计费资格都在主节点）。ok 为 false 时响应已经写好（原来的 prompt 过长错误或计费错误）。
func (h *GatewayHandler) antigravityFallbackAPIKey(c *gin.Context, apiKey *service.APIKey, fallbackGroupID int64, account *service.Account,
	tooLong *service.PromptTooLongError, streamStarted bool, reqLog *zap.Logger,
) (*service.APIKey, bool) {
	writeOriginal := func() {
		_ = h.antigravityGatewayService.WriteMappedClaudeError(c, account, tooLong.StatusCode, tooLong.RequestID, tooLong.Body)
	}
	if h.relay != nil {
		sw, ok := h.relay.SwitchFallbackGroup(c, apiKey)
		if sw.BillingRejection != nil {
			h.writeGatewayRejection(c, *sw.BillingRejection, streamStarted)
			return nil, false
		}
		if !ok || sw.APIKey == nil {
			reqLog.Warn("gateway.fallback_group_unavailable", zap.Int64("fallback_group_id", fallbackGroupID))
			writeOriginal()
			return nil, false
		}
		return sw.APIKey, true
	}
	fallbackGroup, err := h.gatewayService.ResolveGroupByID(c.Request.Context(), fallbackGroupID)
	if err != nil {
		reqLog.Warn("gateway.resolve_fallback_group_failed", zap.Int64("fallback_group_id", fallbackGroupID), zap.Error(err))
		writeOriginal()
		return nil, false
	}
	if fallbackGroup.Platform != service.PlatformAnthropic ||
		fallbackGroup.SubscriptionType == service.SubscriptionTypeSubscription ||
		fallbackGroup.FallbackGroupIDOnInvalidRequest != nil {
		reqLog.Warn("gateway.fallback_group_invalid",
			zap.Int64("fallback_group_id", fallbackGroup.ID),
			zap.String("fallback_platform", fallbackGroup.Platform),
			zap.String("fallback_subscription_type", fallbackGroup.SubscriptionType),
		)
		writeOriginal()
		return nil, false
	}
	fallbackAPIKey := cloneAPIKeyWithGroup(apiKey, fallbackGroup)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), fallbackAPIKey.User, fallbackAPIKey, fallbackGroup, nil, service.PlatformFromAPIKey(fallbackAPIKey)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return nil, false
	}
	return fallbackAPIKey, true
}
