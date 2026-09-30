package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ChatCompletions handles OpenAI Chat Completions API requests.
// POST /v1/chat/completions
func (h *OpenAIGatewayHandler) ChatCompletions(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.chat_completions",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}
	if h.relay != nil {
		defer h.relay.RequestDone(c)
	}

	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		logRequestBodyReadFailure(reqLog, c.Request, err)
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
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	ensureCompositeTargetPlatform(c, apiKey, reqModel)
	if !openAICompatibleTextTargetAllowed(c, apiKey, reqModel) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Model is not supported by this OpenAI-compatible endpoint for composite groups")
		return
	}
	if cappedBody, changed, err := applyOpenAIReasoningEffortPolicyForRequest(c, apiKey, body); err != nil {
		respondOpenAIReasoningEffortPolicyError(c, err, h.errorResponse)
		return
	} else if changed {
		body = cappedBody
	}
	reqStream, ok := parseOpenAICompatibleStream(body)
	if !ok {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", invalidStreamFieldTypeMessage)
		return
	}
	if _, err := service.ValidateOpenAIServiceTierField(body); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if service.IsGPTImageGenerationModel(reqModel) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "This model is not supported on the Chat Completions endpoint")
		return
	}

	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	setOpsRequestContext(c, reqModel, reqStream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(reqStream, false)))

	// 从节点：安全审计经主节点判定（h.relay.SecurityAudit）；cyber 会话屏蔽、渠道映射在主节点选号时做。
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIChat, reqModel, body); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}
	if h.relay == nil {
		if h.rejectIfCyberSessionBlocked(c, apiKey, body, reqModel, cyberBlockFormatChat) {
			return
		}
	}

	// 解析渠道级模型映射
	var channelMapping service.ChannelMappingResult
	if h.relay == nil {
		channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
	}
	forwardModel := openAIChannelForwardModel(channelMapping, reqModel)

	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	requestPlatform := openAICompatibleRequestPlatform(c.Request.Context(), apiKey)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	// 从节点：用户并发槽和计费资格在主节点第一次选号时做。
	if h.relay == nil {
		userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted, reqLog)
		if !acquired {
			return
		}
		if userReleaseFunc != nil {
			defer userReleaseFunc()
		}

		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
			reqLog.Info("openai_chat_completions.billing_eligibility_check_failed", zap.Error(err))
			status, code, message, retryAfter := billingErrorDetails(err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			h.handleStreamingAwareError(c, status, code, message, streamStarted)
			return
		}
	}

	sessionHash := h.gatewayService.GenerateSessionHash(c, body)
	promptCacheKey := h.gatewayService.ExtractSessionID(c, body)

	maxAccountSwitches := h.maxAccountSwitches
	switchCount := 0
	profitVetoCount := 0
	failedAccountIDs := make(map[int64]struct{})
	failedGroupIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	var oauth429FailoverState service.OpenAIOAuth429FailoverState

	// 分组利润控制：chat completions 文本入口请求级装门并固定 pricingAt。
	// 从节点：计价在主节点。
	var ccPricingCtx context.Context
	var pricingAt time.Time
	if h.relay == nil {
		ccPricingCtx, pricingAt = h.gatewayService.WithOpenAIRequestPricingContext(c.Request.Context(), apiKey.GroupID)
		c.Request = c.Request.WithContext(ccPricingCtx)
	}
	var relayAttempt *OpenAIRelayAttempt

	for {
		if failoverClientGone(c) {
			return
		}
		var outcome OpenAISelectOutcome
		if h.relay != nil {
			res := h.relay.Select(c, OpenAIRelaySelectRequest{
				Chat: true, APIKey: apiKey, Model: reqModel, Stream: reqStream, SessionHash: sessionHash,
				Excluded: failedAccountIDs, Body: body,
			})
			if res.Rejection != nil && !res.Rejection.AutoGroupFailover {
				h.writeOpenAIRelayRejection(c, res.Rejection, apiKey, reqModel, cyberBlockFormatChat, lastFailoverErr, streamStarted, reqLog)
				return
			}
			if res.Rejection != nil {
				// 本地这时先换到下一个候选分组再试：照本地选号失败的分支走（没换成时按这个拒绝写）。
				outcome = relayAutoGroupFailoverOutcome(c, res.Rejection, sessionHash)
			} else {
				relayAttempt = res.Attempt
				channelMapping = relayAttempt.ChannelMapping
				forwardModel = relayAttempt.ForwardModel
				maxAccountSwitches = relayAttempt.MaxAccountSwitches
				setOpsSelectedAccount(c, relayAttempt.Account.ID, relayAttempt.Account.Platform)
				outcome = h.relayAttemptOutcome(c, relayAttempt)
			}
		} else {
			// Select account and admit it (shared with relay selection, see OpenAIAccountAdmitter.SelectAndAdmit).
			onTick, cannotWait := h.openAIAdmissionWaitHooks(c, reqStream, &streamStarted)
			selectState := OpenAISelectState{ProfitVetoCount: profitVetoCount, LastFailoverErr: lastFailoverErr}
			outcome = OpenAIAccountAdmitter{Gateway: h.gatewayService, Concurrency: h.concurrencyHelper}.SelectAndAdmit(c.Request.Context(), OpenAISelectRequest{
				GroupID:            apiKey.GroupID,
				SessionHash:        sessionHash,
				ForwardModel:       forwardModel,
				RequestPlatform:    requestPlatform,
				RequiredCapability: service.OpenAIEndpointCapabilityChatCompletions,
				Excluded:           failedAccountIDs,
				OnTick:             onTick,
				CannotWait:         cannotWait,
				OnAccountChosen: func(_ context.Context, selection *service.AccountSelectionResult) context.Context {
					setOpsSelectedAccount(c, selection.Account.ID, selection.Account.Platform)
					c.Request = c.Request.WithContext(service.ContextWithSelectionFallbackTrace(c.Request.Context(), selection))
					return c.Request.Context()
				},
			}, &selectState, reqLog)
			profitVetoCount, lastFailoverErr = selectState.ProfitVetoCount, selectState.LastFailoverErr
		}
		sessionHash = outcome.SessionHash
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
			h.writeOpenAIGatewayRejection(c, OpenAINoAccountRejection(c.Request.Context(), h.gatewayService, apiKey, reqModel, openAICompatibleRequestPlatform(c.Request.Context(), apiKey), nil), streamStarted)
			return
		case OpenAISelectFailed:
			err := outcome.Err
			if failoverClientGone(c) {
				reqLog.Info("openai_chat_completions.account_select_aborted_client_disconnected", zap.Error(err))
				return
			}
			reqLog.Warn("openai_chat_completions.account_select_failed",
				zap.Error(openAICompatibleSelectionErrorForLog(err, requestPlatform)),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if len(failedAccountIDs) == 0 {
				if isAutoGroupSelectionFailoverError(err) && h.tryAutoGroupFailover(c, &apiKey, reqModel, failedGroupIDs, &subscription) {
					channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
					forwardModel = openAIChannelForwardModel(channelMapping, reqModel)
					requestPlatform = openAICompatibleRequestPlatform(c.Request.Context(), apiKey)
					ccPricingCtx, pricingAt = h.gatewayService.WithOpenAIRequestPricingContext(c.Request.Context(), apiKey.GroupID)
					c.Request = c.Request.WithContext(ccPricingCtx)
					continue
				}
				if h.writeRelayAutoGroupFailoverRejection(c, err, apiKey, reqModel, cyberBlockFormatChat, lastFailoverErr, streamStarted, reqLog) {
					return
				}
				h.writeOpenAIGatewayRejection(c, OpenAIFirstSelectFailureRejection(c.Request.Context(), h.gatewayService, apiKey, reqModel, openAICompatibleRequestPlatform(c.Request.Context(), apiKey), false, err), streamStarted)
				return
			} else {
				if h.tryAutoGroupFailover(c, &apiKey, reqModel, failedGroupIDs, &subscription) {
					failedAccountIDs = make(map[int64]struct{})
					sameAccountRetryCount = make(map[int64]int)
					switchCount = 0
					profitVetoCount = 0
					lastFailoverErr = nil
					oauth429FailoverState = service.OpenAIOAuth429FailoverState{}
					channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
					forwardModel = openAIChannelForwardModel(channelMapping, reqModel)
					requestPlatform = openAICompatibleRequestPlatform(c.Request.Context(), apiKey)
					ccPricingCtx, pricingAt = h.gatewayService.WithOpenAIRequestPricingContext(c.Request.Context(), apiKey.GroupID)
					c.Request = c.Request.WithContext(ccPricingCtx)
					if err := h.autoGroupFailoverBillingError(c, apiKey, subscription); err != nil {
						reqLog.Warn("openai_chat_completions.auto_group_failover_billing_check_failed", zap.Error(err))
						status, code, message, retryAfter := billingErrorDetails(err)
						if retryAfter > 0 {
							c.Header("Retry-After", strconv.Itoa(retryAfter))
						}
						h.handleStreamingAwareError(c, status, code, message, streamStarted)
						return
					}
					continue
				}
				if h.writeRelayAutoGroupFailoverRejection(c, err, apiKey, reqModel, cyberBlockFormatChat, lastFailoverErr, streamStarted, reqLog) {
					return
				}
				if lastFailoverErr != nil {
					h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
				} else {
					h.handleStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
				}
				return
			}
		}
		account := outcome.Account
		accountReleaseFunc := wrapReleaseOnDone(outcome.Ctx, outcome.Release)

		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()

		forwardBody := body
		if channelMapping.Mapped {
			forwardBody = h.gatewayService.ReplaceModelInBody(body, channelMapping.MappedModel)
		}
		writerSizeBeforeForward := c.Writer.Size()
		result, err := func() (*service.OpenAIForwardResult, error) {
			defer func() {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
			}()
			return h.gatewayService.ForwardAsChatCompletions(c.Request.Context(), c, account, forwardBody, promptCacheKey, "")
		}()
		if service.GetOpsCyberPolicy(c) != nil {
			h.recordCyberPolicyIfMarked(c, apiKey, account, subscription, reqModel, err != nil, body, clientRequestedUsageFields(c, channelMapping, reqModel, ""), service.HashUsageRequestPayload(body), relayAttempt)
		}

		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
		responseLatencyMs := forwardDurationMs
		if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
			responseLatencyMs = forwardDurationMs - upstreamLatencyMs
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)
		if err == nil && result != nil && result.FirstTokenMs != nil {
			service.SetOpsLatencyMs(c, service.OpsTimeToFirstTokenMsKey, int64(*result.FirstTokenMs))
		}
		// #5148 对齐：错误返回携带的部分 result（流中断前上游已计量的 usage）照常
		// 入账；failover 错误恒定 result=nil，不会重复计费。
		submitChatUsage := func(res *service.OpenAIForwardResult) {
			if res == nil {
				return
			}
			stampOpenAIRequestedReasoningEffort(res, c)
			// Chat 的用量不记请求体哈希（与原来一致）。
			facts := collectOpenAIUsageFacts(c, account, res, func() string { return "" }, false)
			if h.relay != nil {
				h.relay.SubmitUsage(c, relayAttempt, facts, res)
				return
			}
			quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
			h.submitOpenAIUsageRecordTask(c.Request.Context(), res, func(ctx context.Context) {
				if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
					Result:             res,
					APIKey:             apiKey,
					User:               apiKey.User,
					Account:            account,
					Subscription:       subscription,
					InboundEndpoint:    facts.InboundEndpoint,
					UpstreamEndpoint:   facts.UpstreamEndpoint,
					UserAgent:          facts.UserAgent,
					IPAddress:          facts.IPAddress,
					APIKeyService:      h.apiKeyService,
					QuotaPlatform:      quotaPlatform,
					SessionID:          facts.SessionID,
					ChannelUsageFields: clientRequestedUsageFields(c, channelMapping, reqModel, res.UpstreamModel),
					PricingAt:          pricingAt,
					CyberBlocked:       facts.CyberBlocked,
				}); err != nil {
					logger.L().With(
						zap.String("component", "handler.openai_gateway.chat_completions"),
						zap.Int64("user_id", subject.UserID),
						zap.Int64("api_key_id", apiKey.ID),
						zap.Any("group_id", apiKey.GroupID),
						zap.String("model", reqModel),
						zap.Int64("account_id", account.ID),
					).Error("openai_chat_completions.record_usage_failed", zap.Error(err))
				}
			})
		}
		if err != nil {
			if result != nil && result.ImageCount > 0 {
				reqLog.Warn("openai_chat_completions.forward_partial_error_with_image_result",
					zap.Int64("account_id", account.ID),
					zap.Int("image_count", result.ImageCount),
					zap.Error(err),
				)
			} else {
				var failoverErr *service.UpstreamFailoverError
				if errors.As(err, &failoverErr) {
					if failoverClientGone(c) {
						reqLog.Info("openai_chat_completions.failover_aborted_client_disconnected",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
						)
						return
					}
					if c.Writer.Size() != writerSizeBeforeForward {
						h.gatewayService.ObserveOpenAIAccountHealthFailure(c.Request.Context(), account, err)
						h.handleFailoverExhausted(c, failoverErr, true)
						return
					}
					if failoverErr.ShouldReportAccountScheduleFailure() {
						h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, nil), false, nil, err)
					}
					if !failoverErr.ShouldRetryNextAccount() {
						if shouldTryOpenAIAutoGroupAfterTerminalFailover(failoverErr) && h.tryAutoGroupFailover(c, &apiKey, reqModel, failedGroupIDs, &subscription) {
							failedAccountIDs = make(map[int64]struct{})
							sameAccountRetryCount = make(map[int64]int)
							switchCount = 0
							profitVetoCount = 0
							lastFailoverErr = nil
							oauth429FailoverState = service.OpenAIOAuth429FailoverState{}
							channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
							forwardModel = openAIChannelForwardModel(channelMapping, reqModel)
							requestPlatform = openAICompatibleRequestPlatform(c.Request.Context(), apiKey)
							ccPricingCtx, pricingAt = h.gatewayService.WithOpenAIRequestPricingContext(c.Request.Context(), apiKey.GroupID)
							c.Request = c.Request.WithContext(ccPricingCtx)
							continue
						}
						h.handleFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					// Pool mode: retry on the same account
					if failoverErr.RetryableOnSameAccount {
						retryLimit := effectiveSameAccountRetryLimit(failoverErr, account)
						if sameAccountRetryAllowed(failoverErr, sameAccountRetryCount[account.ID], retryLimit) {
							sameAccountRetryCount[account.ID]++
							retryDelay := sameAccountRetryDelayFor(failoverErr, sameAccountRetryCount[account.ID])
							reqLog.Warn("openai_chat_completions.pool_mode_same_account_retry",
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
						if h.tryAutoGroupFailover(c, &apiKey, reqModel, failedGroupIDs, &subscription) {
							failedAccountIDs = make(map[int64]struct{})
							sameAccountRetryCount = make(map[int64]int)
							switchCount = 0
							profitVetoCount = 0
							lastFailoverErr = nil
							oauth429FailoverState = service.OpenAIOAuth429FailoverState{}
							channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
							forwardModel = openAIChannelForwardModel(channelMapping, reqModel)
							requestPlatform = openAICompatibleRequestPlatform(c.Request.Context(), apiKey)
							ccPricingCtx, pricingAt = h.gatewayService.WithOpenAIRequestPricingContext(c.Request.Context(), apiKey.GroupID)
							c.Request = c.Request.WithContext(ccPricingCtx)
							continue
						}
						h.handleFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					switchCount++
					if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
						h.handleFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					reqLog.Warn("openai_chat_completions.upstream_failover_switching",
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
						zap.Int("switch_count", switchCount),
						zap.Int("max_switches", maxAccountSwitches),
					)
					continue
				}
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, nil), false, nil, err)
				upstreamErrorAlreadyCommunicated := openAIForwardErrorAlreadyCommunicated(c, writerSizeBeforeForward, err)
				wroteFallback := false
				if !upstreamErrorAlreadyCommunicated {
					wroteFallback = h.ensureOpenAIStreamReadErrorResponse(c, err, streamStarted)
					if !wroteFallback {
						wroteFallback = h.ensureForwardErrorResponse(c, streamStarted)
					}
				}
				reqLog.Warn("openai_chat_completions.forward_failed",
					zap.Int64("account_id", account.ID),
					zap.Bool("fallback_error_response_written", wroteFallback),
					zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
					zap.Error(err),
				)
				submitChatUsage(result)
				return
			}
		}
		if result != nil {
			h.gatewayService.ReportOpenAIAccountScheduleResultWithLatency(account, openAIAccountScheduleModel(c, account, reqModel, false, result), true, result.FirstTokenMs, openAIServingGroupIDForLatency(c), openAIReasoningEffortForLatency(result))
		} else {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, result), true, nil)
		}

		submitChatUsage(result)
		reqLog.Debug("openai_chat_completions.request_completed",
			zap.Int64("account_id", account.ID),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}

// resolveOpenAIUpstreamEndpoint returns the actual upstream endpoint for an
// OpenAI-compatible account. A forwarding result is authoritative because a
// single inbound route may choose raw Chat or a Responses bridge at runtime.
// The account-based derivation remains as a fallback for existing callers and
// forwarding paths that do not report their endpoint yet.
func resolveOpenAIUpstreamEndpoint(c *gin.Context, account *service.Account, result *service.OpenAIForwardResult) string {
	if result != nil {
		if endpoint := strings.TrimSpace(result.UpstreamEndpoint); endpoint != "" {
			return endpoint
		}
	}
	if endpoint := service.GetActualOpenAIUpstreamEndpoint(c); endpoint != "" {
		return endpoint
	}
	if account != nil && account.Type == service.AccountTypeAPIKey &&
		!openai_compat.ShouldUseResponsesAPI(account.Extra) {
		return EndpointChatCompletions
	}
	return GetUpstreamEndpoint(c, account.Platform)
}
