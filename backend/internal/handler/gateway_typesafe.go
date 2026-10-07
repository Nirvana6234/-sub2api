package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/typesafe"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// SystemOne 转发 TypeSafe 的 Jev 判断请求（POST /v1/systemone、POST /api/v1/paw/systemone）。
//
// 两个入口都把已认证的 API Key 放进上下文再进来：/v1 是用户自己的 key，/paw 是
// 带上 X-Paw-Group-Id 分组的内部 key 副本。请求体是用户的聊天文字，所以这里
// 不做内容审计和内容审核，日志也只记状态、账号、用量和耗时。
//
// 流程与其他网关入口对齐：用户并发槽 → 计费预检 → 在途余额预留 → 选号（含账号槽、
// 利润控制终检）→ 转发 → 换号状态机 → 入账。和官方实现不同的地方只有三处：
// 不做提示词审计（隐私）、没有价格就不转发（Jev 按官方标价入账）、上游对请求本身的
// 4xx 回答原样还给客户端（客户端据此从 jev-1.13.0 退回 jev-latest）。
func (h *GatewayHandler) SystemOne(c *gin.Context) {
	requestStart := time.Now()
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		typeSafeError(c, http.StatusUnauthorized, "authentication_error", "AUTH_REQUIRED", "Invalid API key")
		return
	}
	if apiKey.Group == nil {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		typeSafeError(c, http.StatusNotFound, "not_found_error", "PLATFORM_UNSUPPORTED", "System One API is not supported for this platform")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		subject = middleware2.AuthSubject{UserID: apiKey.UserID}
		if apiKey.User != nil {
			subject.Concurrency = apiKey.User.Concurrency
		}
	}

	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			typeSafeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "REQUEST_TOO_LARGE", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		typeSafeError(c, http.StatusBadRequest, "invalid_request_error", "INVALID_REQUEST", "Failed to read request body")
		return
	}
	// 结构校验与官方一致（键名、重复键、问题类型、stream）；model 不强制 jev-latest。
	model, err := typesafe.ValidateSystemOneShape(body)
	if err != nil {
		typeSafeError(c, http.StatusBadRequest, "invalid_request_error", "INVALID_REQUEST", err.Error())
		return
	}
	ensureCompositeTargetPlatform(c, apiKey, model)
	if apiKey.Group.Platform != service.PlatformTypeSafe &&
		(apiKey.Group.Platform != service.PlatformComposite || !compositeTargetPlatformAllowed(c, apiKey, model, service.PlatformTypeSafe)) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		typeSafeError(c, http.StatusNotFound, "not_found_error", "PLATFORM_UNSUPPORTED", "System One API is not supported for this platform")
		return
	}
	setOpsRequestContext(c, model, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	reqLog := requestLogger(c, "handler.gateway.systemone",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("model", model),
	)

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	// 从节点：用户并发槽、计费资格、价格、选号与准入都在主节点（设计 3.2），换号状态在 systemOneViaRelay 里。
	if h.relay != nil {
		if apiKey.Group.Platform == service.PlatformComposite {
			// 组合平台分组按模型选目标：本地不做，交给主节点按完整流程处理。
			h.relay.HandOff(c)
			return
		}
		h.systemOneViaRelay(c, apiKey, model, body, subscription, reqLog)
		return
	}

	// 两条网关找不到价格时都按 0 元入账，Jev 不能这样免费用：没配价格就不转发。
	if !h.gatewayService.HasTypeSafePricing(c.Request.Context(), model, apiKey) {
		reqLog.Warn("gateway.systemone.pricing_missing")
		typeSafeError(c, http.StatusServiceUnavailable, "api_error", "PRICING_UNAVAILABLE", "Pricing is not configured for this model")
		return
	}

	pricingCtx, pricingAt := service.WithGatewayTokenRequestPricing(c.Request.Context())
	c.Request = c.Request.WithContext(pricingCtx)
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, model)

	streamStarted := false
	userRelease, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, false, &streamStarted)
	if err != nil {
		reqLog.Warn("gateway.systemone.user_slot_acquire_failed", zap.Error(err))
		h.handleConcurrencyError(c, err, "user", false)
		return
	}
	// 请求结束或 Context 取消时确保释放槽位，避免客户端断开造成泄漏。
	userRelease = wrapReleaseOnDone(c.Request.Context(), userRelease)
	if userRelease != nil {
		defer userRelease()
	}

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		typeSafeError(c, status, code, strings.ToUpper(code), message)
		return
	}
	// 余额模式在途预留：并发请求在预检时看到同一份余额会集体透支。
	inflightRelease, err := reserveInflightBalance(c, h.billingCacheService, h.gatewayService, apiKey, subscription, tokenInflightEstimate(model, body))
	if err != nil {
		reqLog.Info("gateway.systemone.inflight_reservation_rejected", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		typeSafeError(c, status, code, strings.ToUpper(code), message)
		return
	}
	defer inflightRelease()

	fs := NewFailoverState(h.maxAccountSwitches, false)
	for {
		if failoverClientGone(c) {
			return
		}
		selection, selectErr := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, "", model, fs.FailedAccountIDs, "", subject.UserID)
		if selectErr == nil && (selection == nil || selection.Account == nil) {
			selectErr = service.ErrNoAvailableAccounts
		}
		if selectErr != nil {
			if failoverClientGone(c) {
				reqLog.Info("gateway.systemone.account_select_aborted_client_disconnected", zap.Error(selectErr))
				return
			}
			if len(fs.FailedAccountIDs) == 0 {
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, model, model, service.PlatformTypeSafe)
				cls = classifySelectionFailureError(selectErr, cls)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, selectErr)
				}
				reqLog.Warn("gateway.systemone.account_select_failed", zap.Bool("model_not_found", cls.ModelNotFound), zap.Error(selectErr))
				code := "NO_AVAILABLE_ACCOUNTS"
				if cls.ModelNotFound {
					code = "MODEL_NOT_FOUND"
				}
				typeSafeError(c, cls.Status, cls.ErrType, code, cls.Message)
				return
			}
			switch fs.HandleSelectionExhausted(c.Request.Context()) {
			case FailoverContinue:
				continue
			case FailoverCanceled:
				failoverClientGone(c)
				return
			default:
				writeTypeSafeFailoverExhausted(c, fs.LastFailoverErr)
				return
			}
		}
		account := selection.Account

		accountRelease := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				markOpsRoutingCapacityLimited(c)
				typeSafeError(c, http.StatusServiceUnavailable, "api_error", "NO_AVAILABLE_ACCOUNTS", "No available accounts")
				return
			}
			accountRelease, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(c, account.ID, selection.WaitPlan.MaxConcurrency, selection.WaitPlan.Timeout, false, &streamStarted)
			if err != nil {
				reqLog.Warn("gateway.systemone.account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				h.handleConcurrencyError(c, err, "account", false)
				return
			}
		}
		// 准入终检：与其他网关入口一致，利润控制否决的账号不得承接本次请求。
		admissionCtx := service.ContextWithSelectionProfitGate(c.Request.Context(), selection)
		latest, vetoed, reason := h.gatewayService.GatewayProfitControlVetoLatest(admissionCtx, account)
		if vetoed {
			if accountRelease != nil {
				accountRelease()
			}
			reqLog.Debug("gateway.systemone.account_slot_profit_vetoed", zap.Int64("account_id", account.ID), zap.String("reason", reason))
			if fs.RecordProfitVeto(account.ID) == FailoverExhausted {
				reqLog.Warn("gateway.systemone.profit_veto_attempts_exhausted", zap.Int("profit_veto_count", fs.ProfitVetoCount()))
				typeSafeError(c, http.StatusServiceUnavailable, "api_error", "NO_AVAILABLE_ACCOUNTS", profitVetoExhaustedMessage)
				return
			}
			continue
		}
		account = latest
		accountRelease = wrapReleaseOnDone(c.Request.Context(), accountRelease)
		setOpsSelectedAccount(c, account.ID, account.Platform)

		forwardStart := time.Now()
		result, forwardErr := h.gatewayService.ForwardTypeSafeSystemOne(c.Request.Context(), c, account, body)
		if accountRelease != nil {
			accountRelease()
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, time.Since(forwardStart).Milliseconds())
		if forwardErr == nil {
			h.recordSystemOneUsage(c, apiKey, subscription, account, channelMapping, model, body, result, subject.UserID, pricingAt)
			return
		}

		var clientErr *service.TypeSafeClientError
		if errors.As(forwardErr, &clientErr) {
			// 上游的回答已原样写回（例如 400 Unknown model，客户端据此退回 jev-latest）。
			// 这类错误体可能带着出错的输入原文，不进运维错误日志。
			c.Set(opsDedicatedErrorRecordedKey, true)
			reqLog.Info("gateway.systemone.upstream_client_error",
				zap.Int64("account_id", account.ID),
				zap.Int("upstream_status", clientErr.StatusCode),
			)
			return
		}
		var failoverErr *service.UpstreamFailoverError
		if errors.As(forwardErr, &failoverErr) {
			switch fs.HandleFailoverError(c.Request.Context(), h.gatewayService, account.ID, account.Platform, account.GetPoolModeRetryCount(), failoverErr) {
			case FailoverContinue:
				reqLog.Warn("gateway.systemone.upstream_failover_switching",
					zap.Int64("account_id", account.ID),
					zap.Int("upstream_status", failoverErr.StatusCode),
					zap.Int("switch_count", fs.SwitchCount),
				)
				continue
			case FailoverExhausted:
				writeTypeSafeFailoverExhausted(c, fs.LastFailoverErr)
				return
			case FailoverCanceled:
				failoverClientGone(c)
				return
			}
		}
		if failoverClientGone(c) {
			return
		}
		reqLog.Warn("gateway.systemone.forward_failed", zap.Int64("account_id", account.ID), zap.Error(forwardErr))
		if errors.Is(forwardErr, typesafe.ErrSystemOneResponseTooLarge) {
			typeSafeError(c, http.StatusBadGateway, "upstream_error", "UPSTREAM_RESPONSE_TOO_LARGE", "Upstream response exceeds the gateway size limit")
			return
		}
		typeSafeError(c, http.StatusBadGateway, "upstream_error", "UPSTREAM_ERROR", "Upstream request failed")
		return
	}
}

// TypeSafeFirstSelectFailureRejection 是第一次就选不出账号时的错误（本地 SystemOne 与主从分流共用）：
// 按模型不存在 / 调度器的错误分类，code 是 MODEL_NOT_FOUND 或 NO_AVAILABLE_ACCOUNTS。
func TypeSafeFirstSelectFailureRejection(ctx context.Context, diag service.ModelAvailabilityDiagnoser, apiKey *service.APIKey, model string, selectErr error) OpenAIGatewayRejection {
	cls := classifyNoAccountError(ctx, diag, apiKey, model, model, service.PlatformTypeSafe)
	cls = classifySelectionFailureError(selectErr, cls)
	r := OpenAIGatewayRejection{Status: cls.Status, ErrType: cls.ErrType, Message: cls.Message, Code: "NO_AVAILABLE_ACCOUNTS"}
	if cls.ModelNotFound {
		r.Code = "MODEL_NOT_FOUND"
	} else {
		r.RoutingCapacityLimited = isOpsNoAvailableAccountError(selectErr)
	}
	return r
}

// typeSafeMaxAccountSwitches 是从节点路径一次 Jev 请求最多换几次号的默认值（主节点随选号带下来 gateway.max_account_switches）。
const typeSafeMaxAccountSwitches = 3

// systemOneViaRelay 是从节点上的 SystemOne：选号与准入（含用户并发槽、计费资格、价格）经主节点，
// 转发在本机，用量写进本地扣费队列由主节点按凭证入账。
func (h *GatewayHandler) systemOneViaRelay(c *gin.Context, apiKey *service.APIKey, model string, body []byte, subscription *service.UserSubscription, reqLog *zap.Logger) {
	// 请求结束时放掉这次的选号。
	defer h.relay.RequestDone(c)

	failedAccounts := make(map[int64]struct{})
	var lastFailover *service.UpstreamFailoverError
	var relayAttempt *OpenAIRelayAttempt
	maxSwitches := typeSafeMaxAccountSwitches
	for attempt := 0; attempt <= maxSwitches; attempt++ {
		res := h.relay.Select(c, OpenAIRelaySelectRequest{SystemOne: true, APIKey: apiKey, Model: model, Excluded: failedAccounts})
		if r := res.Rejection; r != nil {
			switch r.Kind {
			case OpenAIRelayRejectFailoverExhausted:
				// 之前换过号、再选不出账号：按最近一次上游错误写。
				writeTypeSafeFailoverExhausted(c, lastFailover)
				return
			case OpenAIRelayRejectProfitVetoed:
				// 准入失败：把这个账号排除接着选。
				failedAccounts[r.VetoedAccountID] = struct{}{}
				continue
			}
			h.writeTypeSafeRelayRejection(c, r, lastFailover)
			return
		}
		relayAttempt = res.Attempt
		if relayAttempt.MaxAccountSwitches > 0 {
			maxSwitches = relayAttempt.MaxAccountSwitches
		}
		account := relayAttempt.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)

		result, forwardErr := h.gatewayService.ForwardTypeSafeSystemOne(c.Request.Context(), c, account, body)
		h.relay.AttemptDone(c, relayAttempt)
		if forwardErr == nil {
			h.relay.ForwardSucceeded(c, relayAttempt)
			h.recordSystemOneRelayUsage(c, apiKey, account, body, result, relayAttempt)
			return
		}

		var clientErr *service.TypeSafeClientError
		if errors.As(forwardErr, &clientErr) {
			// 上游的回答已原样写回（例如 400 Unknown model，客户端据此退回 jev-latest）。
			// 这类错误体可能带着出错的输入原文，不进运维错误日志。
			c.Set(opsDedicatedErrorRecordedKey, true)
			reqLog.Info("gateway.systemone.upstream_client_error",
				zap.Int64("account_id", account.ID),
				zap.Int("upstream_status", clientErr.StatusCode),
			)
			return
		}
		var failoverErr *service.UpstreamFailoverError
		if !errors.As(forwardErr, &failoverErr) {
			reqLog.Warn("gateway.systemone.forward_failed", zap.Int64("account_id", account.ID), zap.Error(forwardErr))
			if errors.Is(forwardErr, typesafe.ErrSystemOneResponseTooLarge) {
				typeSafeError(c, http.StatusBadGateway, "upstream_error", "UPSTREAM_RESPONSE_TOO_LARGE", "Upstream response exceeds the gateway size limit")
				return
			}
			typeSafeError(c, http.StatusBadGateway, "upstream_error", "UPSTREAM_ERROR", "Upstream request failed")
			return
		}
		reqLog.Warn("gateway.systemone.upstream_failover",
			zap.Int64("account_id", account.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
			zap.Int("attempt", attempt),
		)
		failedAccounts[account.ID] = struct{}{}
		lastFailover = failoverErr
		if failoverClientGone(c) {
			return
		}
	}
	writeTypeSafeFailoverExhausted(c, lastFailover)
}

// recordSystemOneRelayUsage 把 SystemOne 的用量写进本地扣费队列，主节点按凭证用同一个 RecordUsage 入账。
func (h *GatewayHandler) recordSystemOneRelayUsage(c *gin.Context, apiKey *service.APIKey, account *service.Account, body []byte, result *service.ForwardResult, relayAttempt *OpenAIRelayAttempt) {
	if result == nil {
		return
	}
	if result.RequestID == "" {
		// request_id 是扣费的幂等键，上游没给就每次现生成，不能让两次请求撞在一起。
		result.RequestID = "systemone:" + uuid.NewString()
	}
	h.relay.SubmitAnthropicUsage(c, relayAttempt, OpenAIUsageFacts{
		InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: GetUpstreamEndpoint(c, account.Platform),
		UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
		SessionID: service.ExtractClientSessionID(c),
		// 只存请求体的哈希（用于幂等与对账），不存原文。
		RequestPayloadHash: service.HashUsageRequestPayload(body),
	}, result, false)
}

func (h *GatewayHandler) recordSystemOneUsage(c *gin.Context, apiKey *service.APIKey, subscription *service.UserSubscription, account *service.Account, mapping service.ChannelMappingResult, model string, body []byte, result *service.ForwardResult, userID int64, pricingAt time.Time) {
	if result == nil {
		return
	}
	if result.RequestID == "" {
		// request_id 是扣费的幂等键，上游没给就每次现生成，不能让两次请求撞在一起。
		result.RequestID = "systemone:" + uuid.NewString()
	}
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	sessionID := service.ExtractClientSessionID(c)
	// 只存请求体的哈希（用于幂等与对账），不存原文。
	requestPayloadHash := service.HashUsageRequestPayload(body)
	h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
			Result:             result,
			APIKey:             apiKey,
			User:               apiKey.User,
			Account:            account,
			Subscription:       subscription,
			PricingAt:          pricingAt,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          userAgent,
			IPAddress:          clientIP,
			SessionID:          sessionID,
			RequestPayloadHash: requestPayloadHash,
			APIKeyService:      h.apiKeyService,
			QuotaPlatform:      quotaPlatform,
			ChannelUsageFields: clientRequestedUsageFields(c, mapping, model, result.UpstreamResponseModel),
		}); err != nil {
			logger.L().With(
				zap.String("component", "handler.gateway.systemone"),
				zap.Int64("user_id", userID),
				zap.Int64("api_key_id", apiKey.ID),
				zap.Int64("account_id", account.ID),
			).Error("gateway.systemone.record_usage_failed", zap.Error(err))
		}
	})
}

// writeTypeSafeFailoverExhausted 在换号用尽时给出我们自己的错误，不转述上游错误体：
// 上游的 401/403 说的是我们的账号 key，原样转给客户端会被当成客户端自己的 key 无效。
// 429/529 保留状态码和 Retry-After，客户端按繁忙处理。
func writeTypeSafeFailoverExhausted(c *gin.Context, lastFailover *service.UpstreamFailoverError) {
	if lastFailover != nil && lastFailover.ResponseHeaders != nil {
		if retryAfter := lastFailover.ResponseHeaders.Get("Retry-After"); retryAfter != "" {
			c.Header("Retry-After", retryAfter)
		}
	}
	status := http.StatusBadGateway
	if lastFailover != nil {
		switch {
		case lastFailover.StatusCode == http.StatusTooManyRequests:
			status = http.StatusTooManyRequests
		case lastFailover.StatusCode == 529:
			status = 529
		case lastFailover.StatusCode == http.StatusUnauthorized, lastFailover.StatusCode == http.StatusPaymentRequired, lastFailover.StatusCode == http.StatusForbidden:
			status = http.StatusServiceUnavailable
		}
	}
	switch status {
	case http.StatusTooManyRequests, 529:
		typeSafeError(c, status, "rate_limit_error", "UPSTREAM_BUSY", "Upstream is busy, please retry later")
	case http.StatusServiceUnavailable:
		typeSafeError(c, status, "api_error", "NO_AVAILABLE_ACCOUNTS", "No available accounts")
	default:
		typeSafeError(c, status, "upstream_error", "UPSTREAM_ERROR", "Upstream request failed")
	}
}

// typeSafeError 写我们自己的错误。type 给 API Key 用户（OpenAI 风格），code 给小白端
// 客户端（与 /paw 其他接口的 {"error":{"code","message"}} 一致）。上游透传的错误不经这里，
// 保留 TypeSafe 自己的 {"detail":{...}}。
func typeSafeError(c *gin.Context, status int, errType, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "code": code, "message": message}})
}

// writeTypeSafeRelayRejection 按 TypeSafe 自己的错误格式写出主节点的拒绝（Gateway 拒绝的 code 是错误码，没有时取错误类型的大写）。
func (h *GatewayHandler) writeTypeSafeRelayRejection(c *gin.Context, r *OpenAIRelayRejection, lastFailover *service.UpstreamFailoverError) {
	switch r.Kind {
	case OpenAIRelayRejectRaw:
		middleware2.WriteCapturedRejection(c, r.Raw)
	case OpenAIRelayRejectUnsupported:
		if c.Writer.Written() {
			typeSafeError(c, http.StatusBadGateway, "upstream_error", "UPSTREAM_ERROR", "Upstream request failed")
			return
		}
		h.relay.HandOff(c)
	case OpenAIRelayRejectUnavailable:
		if lastFailover != nil {
			writeTypeSafeFailoverExhausted(c, lastFailover)
			return
		}
		typeSafeError(c, http.StatusServiceUnavailable, "api_error", "SERVICE_UNAVAILABLE", "Service temporarily unavailable")
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
		code := g.Code
		if code == "" {
			code = strings.ToUpper(g.ErrType)
		}
		typeSafeError(c, g.Status, g.ErrType, code, g.Message)
	}
}
