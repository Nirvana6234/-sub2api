package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// AlphaSearch proxies the standalone search endpoint used by Codex Responses Lite.
func (h *OpenAIGatewayHandler) AlphaSearch(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)
	setOpenAIClientTransportHTTP(c)
	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex alpha search is only available for OpenAI and Composite groups")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.alpha_search",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}
	if !gjson.ValidBytes(body) {
		logRequestBodyParseFailure(reqLog, body, nil)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || strings.TrimSpace(modelResult.String()) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	requestedModel := strings.TrimSpace(modelResult.String())
	if !compositeTargetPlatformAllowed(c, apiKey, requestedModel, service.PlatformOpenAI) {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex alpha search only supports OpenAI models for Composite groups")
		return
	}
	reqLog = reqLog.With(zap.String("model", requestedModel))
	setOpsRequestContext(c, requestedModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, "openai_alpha_search", requestedModel, body); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}

	// 从节点：渠道映射在主节点选号时做。
	var channelMapping service.ChannelMappingResult
	if h.relay == nil {
		channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, requestedModel)
	}
	forwardBody := openAIModelMappedBody(body, channelMapping.Mapped, channelMapping.MappedModel, h.gatewayService.ReplaceModelInBody)
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	// 从节点：用户并发槽和计费资格在主节点第一次选号时做。
	if h.relay != nil {
		defer h.relay.RequestDone(c)
	} else {
		userRelease, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
		if !acquired {
			return
		}
		if userRelease != nil {
			defer userRelease()
		}

		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
			status, code, message, retryAfter := billingErrorDetails(err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			h.errorResponse(c, status, code, message)
			return
		}
	}

	searchID := strings.TrimSpace(gjson.GetBytes(body, "id").String())
	sessionHash := h.gatewayService.GenerateSessionHashWithFallback(c, nil, searchID)
	profitVetoCount := 0
	failedAccountIDs := make(map[int64]struct{})
	failedGroupIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	switchCount := 0
	var oauth429FailoverState service.OpenAIOAuth429FailoverState
	routingStart := time.Now()

	// 分组利润控制：alpha search 文本入口请求级装门并固定 pricingAt
	//（记录路径经 service.OpenAIPricingAtFromContext 从请求 ctx 回读）。
	// 从节点：计价在主节点。
	var asPricingCtx context.Context
	if h.relay == nil {
		asPricingCtx, _ = h.gatewayService.WithOpenAIRequestPricingContext(c.Request.Context(), apiKey.GroupID)
		c.Request = c.Request.WithContext(asPricingCtx)
	}
	var relayAttempt *OpenAIRelayAttempt
	maxAccountSwitches := h.maxAccountSwitches

	for {
		var outcome OpenAISelectOutcome
		if h.relay != nil {
			res := h.relay.Select(c, OpenAIRelaySelectRequest{AlphaSearch: true, APIKey: apiKey, Model: requestedModel, SessionHash: sessionHash, Excluded: failedAccountIDs})
			if res.Rejection != nil && !res.Rejection.AutoGroupFailover {
				h.writeOpenAIRelayRejection(c, res.Rejection, apiKey, requestedModel, cyberBlockFormatChat, lastFailoverErr, false, reqLog)
				return
			}
			if res.Rejection != nil {
				// 本地这时先换到下一个候选分组再试：照本地选号失败的分支走（没换成时按这个拒绝写）。
				outcome = relayAutoGroupFailoverOutcome(c, res.Rejection, sessionHash)
			} else {
				relayAttempt = res.Attempt
				channelMapping = relayAttempt.ChannelMapping
				forwardBody = openAIModelMappedBody(body, channelMapping.Mapped, channelMapping.MappedModel, h.gatewayService.ReplaceModelInBody)
				maxAccountSwitches = relayAttempt.MaxAccountSwitches
				setOpsSelectedAccount(c, relayAttempt.Account.ID, relayAttempt.Account.Platform)
				outcome = h.relayAttemptOutcome(c, relayAttempt)
			}
		} else {
			// 选号与准入（与主节点的选号共用 OpenAIAccountAdmitter）：alpha search 只走 HTTP/SSE，按次计费不按上游 token 成本选号。
			selectState := OpenAISelectState{ProfitVetoCount: profitVetoCount}
			outcome = OpenAIAccountAdmitter{Gateway: h.gatewayService, Concurrency: h.concurrencyHelper}.SelectAndAdmit(c.Request.Context(), OpenAISelectRequest{
				GroupID:             apiKey.GroupID,
				SessionHash:         sessionHash,
				ForwardModel:        requestedModel,
				RequestPlatform:     service.PlatformOpenAI,
				RequiredCapability:  service.OpenAIEndpointCapabilityAlphaSearch,
				Transport:           service.OpenAIUpstreamTransportHTTPSSE,
				NoUpstreamTokenCost: true,
				Excluded:            failedAccountIDs,
				OnAccountChosen: func(_ context.Context, selection *service.AccountSelectionResult) context.Context {
					setOpsSelectedAccount(c, selection.Account.ID, selection.Account.Platform)
					c.Request = c.Request.WithContext(service.ContextWithSelectionFallbackTrace(c.Request.Context(), selection))
					return c.Request.Context()
				},
			}, &selectState, reqLog)
			profitVetoCount = selectState.ProfitVetoCount
		}
		var err error
		switch outcome.Kind {
		case OpenAISelected:
		case OpenAISelectAborted:
			failoverClientGone(c)
			return
		case OpenAISelectVetoExhausted:
			h.handleOpenAIProfitVetoExhausted(c, streamStarted, reqLog, profitVetoCount)
			return
		case OpenAISelectQueueFull, OpenAISelectSlotError, OpenAISelectNoWaitPlan:
			h.writeOpenAIAdmissionFailure(c, outcome, streamStarted)
			return
		case OpenAISelectNone:
			// 没有选出账号也没有错误：与选号失败同一个分支。
		default:
			err = outcome.Err
		}
		if outcome.Kind == OpenAISelectNone || err != nil {
			if failoverClientGone(c) {
				reqLog.Info("openai_alpha_search.account_select_aborted_client_disconnected", zap.Error(err))
				return
			}
			if len(failedAccountIDs) == 0 {
				if isAutoGroupSelectionFailoverError(err) && h.tryAutoGroupFailover(c, &apiKey, requestedModel, failedGroupIDs, &subscription) {
					channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, requestedModel)
					forwardBody = openAIModelMappedBody(body, channelMapping.Mapped, channelMapping.MappedModel, h.gatewayService.ReplaceModelInBody)
					asPricingCtx, _ = h.gatewayService.WithOpenAIRequestPricingContext(c.Request.Context(), apiKey.GroupID)
					c.Request = c.Request.WithContext(asPricingCtx)
					continue
				}
				if h.writeRelayAutoGroupFailoverRejection(c, err, apiKey, requestedModel, cyberBlockFormatChat, lastFailoverErr, streamStarted, reqLog) {
					return
				}
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, requestedModel, requestedModel, service.PlatformOpenAI)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				}
				h.errorResponse(c, cls.Status, cls.ErrType, cls.Message)
				return
			}
			if lastFailoverErr != nil {
				if h.tryAutoGroupFailover(c, &apiKey, requestedModel, failedGroupIDs, &subscription) {
					failedAccountIDs = make(map[int64]struct{})
					sameAccountRetryCount = make(map[int64]int)
					switchCount = 0
					profitVetoCount = 0
					lastFailoverErr = nil
					oauth429FailoverState = service.OpenAIOAuth429FailoverState{}
					channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, requestedModel)
					forwardBody = openAIModelMappedBody(body, channelMapping.Mapped, channelMapping.MappedModel, h.gatewayService.ReplaceModelInBody)
					asPricingCtx, _ = h.gatewayService.WithOpenAIRequestPricingContext(c.Request.Context(), apiKey.GroupID)
					c.Request = c.Request.WithContext(asPricingCtx)
					if err := h.autoGroupFailoverBillingError(c, apiKey, subscription); err != nil {
						reqLog.Warn("openai_alpha_search.auto_group_failover_billing_check_failed", zap.Error(err))
						status, code, message, retryAfter := billingErrorDetails(err)
						if retryAfter > 0 {
							c.Header("Retry-After", strconv.Itoa(retryAfter))
						}
						h.errorResponse(c, status, code, message)
						return
					}
					continue
				}
				h.handleFailoverExhausted(c, lastFailoverErr, false)
			} else {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			}
			return
		}

		account := outcome.Account
		accountRelease := wrapReleaseOnDone(outcome.Ctx, outcome.Release)
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		writerSizeBeforeForward := c.Writer.Size()
		forwardStart := time.Now()
		var result *service.OpenAIForwardResult
		result, err = func() (*service.OpenAIForwardResult, error) {
			if accountRelease != nil {
				defer accountRelease()
			}
			return h.gatewayService.ForwardAlphaSearch(c.Request.Context(), c, account, forwardBody)
		}()
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, time.Since(forwardStart).Milliseconds())

		if err == nil {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, requestedModel, false, result), true, nil)
			if result != nil {
				if h.relay != nil {
					// 从节点：写进本地扣费队列，主节点按凭证用同一个 RecordUsage 入账。
					h.relay.SubmitUsage(c, relayAttempt, OpenAIUsageFacts{
						InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: GetUpstreamEndpoint(c, account.Platform), UserAgent: c.GetHeader("User-Agent"),
						IPAddress: ip.GetClientIP(c), RequestPayloadHash: service.HashUsageRequestPayload(body), SessionID: service.ExtractClientSessionID(c),
					}, result)
					return
				}
				h.recordAlphaSearchUsage(c, apiKey, account, subscription, channelMapping, requestedModel, body, result, subject.UserID)
			}
			return
		}

		var failoverErr *service.UpstreamFailoverError
		if !errors.As(err, &failoverErr) {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, requestedModel, false, result), false, nil, err)
			if c.Writer.Size() == writerSizeBeforeForward {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			}
			reqLog.Warn("openai_alpha_search.forward_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			return
		}

		h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, requestedModel, false, result), false, nil, err)
		if c.Writer.Size() != writerSizeBeforeForward {
			h.handleFailoverExhausted(c, failoverErr, true)
			return
		}
		if failoverClientGone(c) {
			reqLog.Info("openai_alpha_search.failover_aborted_client_disconnected",
				zap.Int64("account_id", account.ID),
				zap.Int("upstream_status", failoverErr.StatusCode),
			)
			return
		}
		if failoverErr.RetryableOnSameAccount {
			retryLimit := account.GetPoolModeRetryCount()
			if sameAccountRetryAllowed(failoverErr, sameAccountRetryCount[account.ID], retryLimit) {
				sameAccountRetryCount[account.ID]++
				retryDelay := sameAccountRetryDelayFor(failoverErr, sameAccountRetryCount[account.ID])
				reqLog.Warn("openai_alpha_search.same_account_retry",
					zap.Int64("account_id", account.ID),
					zap.Int("upstream_status", failoverErr.StatusCode),
					zap.Int("retry_limit", retryLimit),
					zap.Int("retry_count", sameAccountRetryCount[account.ID]),
					zap.Duration("retry_delay", retryDelay),
				)
				select {
				case <-c.Request.Context().Done():
					return
				case <-time.After(retryDelay):
				}
				continue
			}
		}
		h.gatewayService.RecordOpenAIAccountSwitch()
		failedAccountIDs[account.ID] = struct{}{}
		lastFailoverErr = failoverErr
		if switchCount >= maxAccountSwitches {
			if h.tryAutoGroupFailover(c, &apiKey, requestedModel, failedGroupIDs, &subscription) {
				failedAccountIDs = make(map[int64]struct{})
				sameAccountRetryCount = make(map[int64]int)
				switchCount = 0
				profitVetoCount = 0
				lastFailoverErr = nil
				oauth429FailoverState = service.OpenAIOAuth429FailoverState{}
				channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, requestedModel)
				forwardBody = openAIModelMappedBody(body, channelMapping.Mapped, channelMapping.MappedModel, h.gatewayService.ReplaceModelInBody)
				asPricingCtx, _ = h.gatewayService.WithOpenAIRequestPricingContext(c.Request.Context(), apiKey.GroupID)
				c.Request = c.Request.WithContext(asPricingCtx)
				continue
			}
			h.handleFailoverExhausted(c, failoverErr, false)
			return
		}
		switchCount++
		if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
			h.handleFailoverExhausted(c, failoverErr, false)
			return
		}
		reqLog.Warn("openai_alpha_search.upstream_failover_switching",
			zap.Int64("account_id", account.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
			zap.Int("switch_count", switchCount),
		)
	}
}

// recordAlphaSearchUsage 为一次成功的 alpha/search 网页搜索落按次计费用量行
// （上游不返回 usage 字段，按 WebSearchCalls 走分组单价 × 倍率的按次口径）。
// 与 images 一致使用 mandatory 池提交，池满时同步兜底执行，保证扣费不丢。
func (h *OpenAIGatewayHandler) recordAlphaSearchUsage(
	c *gin.Context,
	apiKey *service.APIKey,
	account *service.Account,
	subscription *service.UserSubscription,
	channelMapping service.ChannelMappingResult,
	requestedModel string,
	body []byte,
	result *service.OpenAIForwardResult,
	userID int64,
) {
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	sessionID := service.ExtractClientSessionID(c)
	requestPayloadHash := service.HashUsageRequestPayload(body)
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)

	h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
			Result:             result,
			APIKey:             apiKey,
			User:               apiKey.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          userAgent,
			IPAddress:          clientIP,
			RequestPayloadHash: requestPayloadHash,
			APIKeyService:      h.apiKeyService,
			QuotaPlatform:      quotaPlatform,
			SessionID:          sessionID,
			ChannelUsageFields: channelMapping.ToUsageFields(requestedModel, result.UpstreamModel),
			PricingAt:          service.OpenAIPricingAtFromContext(c.Request.Context()),
		}); err != nil {
			logger.L().With(
				zap.String("component", "handler.openai_gateway.alpha_search"),
				zap.Int64("user_id", userID),
				zap.Int64("api_key_id", apiKey.ID),
				zap.Any("group_id", apiKey.GroupID),
				zap.String("model", requestedModel),
				zap.Int64("account_id", account.ID),
			).Error("openai_alpha_search.record_usage_failed", zap.Error(err))
		}
	})
}
