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
	"go.uber.org/zap"
)

// Images handles OpenAI Images API requests.
// POST /v1/images/generations
// POST /v1/images/edits
func (h *OpenAIGatewayHandler) Images(c *gin.Context) {
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
		"handler.openai_gateway.images",
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

	if isMultipartImagesContentType(c.GetHeader("Content-Type")) {
		setOpsRequestContext(c, "", false)
	} else {
		setOpsRequestContext(c, "", false)
	}

	parsed, err := h.gatewayService.ParseOpenAIImagesRequest(c, body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	requestModel := parsed.Model
	ensureCompositeTargetPlatform(c, apiKey, requestModel)
	clientRequestModel := clientRequestedModel(c, requestModel)
	routingModel := requestModel
	if resolvedModel, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context()); ok {
		routingModel = resolvedModel
	}
	if !compositeTargetPlatformAllowed(c, apiKey, requestModel, service.PlatformOpenAI) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Model is not supported by this OpenAI-compatible endpoint for composite groups")
		return
	}

	reqLog = reqLog.With(
		zap.String("model", clientRequestModel),
		zap.String("routing_model", routingModel),
		zap.Bool("stream", parsed.Stream),
		zap.Bool("multipart", parsed.Multipart),
		zap.String("capability", string(parsed.RequiredCapability)),
		zap.String("img_quality", parsed.Quality),
		zap.String("img_size", parsed.Size),
	)

	if !service.GroupAllowsImageGeneration(apiKey.Group) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, requestModel, parsed.ModerationBody()); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}
	imageReleaseFunc, acquired := h.acquireImageGenerationSlot(c, streamStarted)
	if !acquired {
		return
	}
	if imageReleaseFunc != nil {
		defer imageReleaseFunc()
	}

	setOpsRequestContext(c, clientRequestModel, parsed.Stream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(parsed.Stream, false)))

	// 从节点：渠道映射在主节点选号时做。
	var channelMapping service.ChannelMappingResult
	if h.relay == nil {
		channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, routingModel)
	}

	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	// 从节点：用户并发槽和计费资格在主节点第一次选号时做。
	if h.relay != nil {
		defer h.relay.RequestDone(c)
	} else {
		userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, parsed.Stream, &streamStarted, reqLog)
		if !acquired {
			return
		}
		if userReleaseFunc != nil {
			defer userReleaseFunc()
		}

		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
			reqLog.Info("openai.images.billing_eligibility_check_failed", zap.Error(err))
			status, code, message, retryAfter := billingErrorDetails(err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			h.handleStreamingAwareError(c, status, code, message, streamStarted)
			return
		}
	}

	// 余额模式在途预留（与计费同口径估算；计费任务扣减余额缓存后才释放）。
	inflightDone, inflightErr := reserveInflightBalance(c, h.billingCacheService, h.gatewayService, apiKey, subscription, service.InflightEstimateRequest{Model: routingModel, BodyBytes: len(body), Kind: service.InflightEstimateImage, Units: parsed.N})
	if inflightErr != nil {
		status, code, message, retryAfter := billingErrorDetails(inflightErr)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}
	defer inflightDone()

	sessionHash := h.gatewayService.GenerateExplicitSessionHash(c, body)
	requestCtx := service.WithOpenAIImagesEndpoint(service.WithOpenAIImageGenerationIntent(c.Request.Context()))

	maxAccountSwitches := h.maxAccountSwitches
	switchCount := 0
	profitVetoCount := 0
	failedAccountIDs := make(map[int64]struct{})
	failedGroupIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	stopJSONKeepalive := func() {}
	jsonKeepaliveStarted := false
	defer func() { stopJSONKeepalive() }()
	var oauth429FailoverState service.OpenAIOAuth429FailoverState
	var relayAttempt *OpenAIRelayAttempt

	for {
		reqLog.Debug("openai.images.account_selecting", zap.Int("excluded_account_count", len(failedAccountIDs)))
		var outcome OpenAISelectOutcome
		if h.relay != nil {
			res := h.relay.Select(c, OpenAIRelaySelectRequest{
				Images: true, ImagesCapability: string(parsed.RequiredCapabilityForModel(channelMapping.MappedModel)), APIKey: apiKey, Model: routingModel, Stream: parsed.Stream,
				SessionHash: sessionHash, Excluded: failedAccountIDs, Body: body,
			})
			if res.Rejection != nil && !res.Rejection.AutoGroupFailover {
				h.writeOpenAIRelayRejection(c, res.Rejection, apiKey, clientRequestModel, cyberBlockFormatResponses, lastFailoverErr, streamStarted, reqLog)
				return
			}
			if res.Rejection != nil {
				// 本地这时先换到下一个候选分组再试：照本地选号失败的分支走（没换成时按这个拒绝写）。
				outcome = relayAutoGroupFailoverOutcome(c, res.Rejection, sessionHash)
			} else {
				relayAttempt = res.Attempt
				channelMapping = relayAttempt.ChannelMapping
				maxAccountSwitches = relayAttempt.MaxAccountSwitches
				setOpsSelectedAccount(c, relayAttempt.Account.ID, relayAttempt.Account.Platform)
				outcome = h.relayAttemptOutcome(c, relayAttempt)
			}
		} else {
			// 选号与准入（与主节点的选号共用 OpenAIAccountAdmitter）：图片入口按能力选，不装利润门。
			onTick, cannotWait := h.openAIAdmissionWaitHooks(c, parsed.Stream, &streamStarted)
			selectState := OpenAISelectState{ProfitVetoCount: profitVetoCount}
			outcome = OpenAIAccountAdmitter{Gateway: h.gatewayService, Concurrency: h.concurrencyHelper}.SelectAndAdmit(requestCtx, OpenAISelectRequest{
				GroupID:          apiKey.GroupID,
				SessionHash:      sessionHash,
				ForwardModel:     routingModel,
				RequestPlatform:  service.PlatformOpenAI,
				ImagesCapability: parsed.RequiredCapabilityForModel(channelMapping.MappedModel),
				Excluded:         failedAccountIDs,
				OnTick:           onTick,
				CannotWait:       cannotWait,
				OnAccountChosen: func(ctx context.Context, selection *service.AccountSelectionResult) context.Context {
					setOpsSelectedAccount(c, selection.Account.ID, selection.Account.Platform)
					c.Request = c.Request.WithContext(service.ContextWithSelectionFallbackTrace(c.Request.Context(), selection))
					return service.ContextWithSelectionFallbackTrace(ctx, selection)
				},
			}, &selectState, reqLog)
			profitVetoCount = selectState.ProfitVetoCount
		}
		sessionHash = outcome.SessionHash
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
			cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, clientRequestModel, routingModel, service.PlatformOpenAI)
			if !cls.ModelNotFound {
				markOpsRoutingCapacityLimited(c)
			}
			message := cls.Message
			if !cls.ModelNotFound {
				message = "No available compatible accounts"
			}
			h.handleStreamingAwareError(c, cls.Status, cls.ErrType, message, streamStarted)
			return
		default:
			err = outcome.Err
		}
		if err != nil {
			if failoverClientGone(c) {
				reqLog.Info("openai.images.account_select_aborted_client_disconnected", zap.Error(err))
				return
			}
			reqLog.Warn("openai.images.account_select_failed",
				zap.Error(err),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if len(failedAccountIDs) == 0 {
				if isAutoGroupSelectionFailoverError(err) && h.tryAutoGroupFailover(c, &apiKey, clientRequestModel, failedGroupIDs, &subscription) {
					channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, clientRequestModel)
					routingModel = openAIChannelForwardModel(channelMapping, clientRequestModel)
					requestCtx = service.WithOpenAIImagesEndpoint(service.WithOpenAIImageGenerationIntent(c.Request.Context()))
					continue
				}
				if h.writeRelayAutoGroupFailoverRejection(c, err, apiKey, clientRequestModel, cyberBlockFormatResponses, lastFailoverErr, streamStarted, reqLog) {
					return
				}
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, clientRequestModel, routingModel, service.PlatformOpenAI)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				}
				message := cls.Message
				if !cls.ModelNotFound {
					message = "No available compatible accounts"
				}
				h.handleStreamingAwareError(c, cls.Status, cls.ErrType, message, streamStarted)
				return
			}
			if lastFailoverErr != nil {
				if h.tryAutoGroupFailover(c, &apiKey, clientRequestModel, failedGroupIDs, &subscription) {
					failedAccountIDs = make(map[int64]struct{})
					sameAccountRetryCount = make(map[int64]int)
					switchCount = 0
					profitVetoCount = 0
					lastFailoverErr = nil
					oauth429FailoverState = service.OpenAIOAuth429FailoverState{}
					channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, clientRequestModel)
					routingModel = openAIChannelForwardModel(channelMapping, clientRequestModel)
					requestCtx = service.WithOpenAIImagesEndpoint(service.WithOpenAIImageGenerationIntent(c.Request.Context()))
					if err := h.autoGroupFailoverBillingError(c, apiKey, subscription); err != nil {
						reqLog.Warn("openai.images.auto_group_failover_billing_check_failed", zap.Error(err))
						status, code, message, retryAfter := billingErrorDetails(err)
						if retryAfter > 0 {
							c.Header("Retry-After", strconv.Itoa(retryAfter))
						}
						h.handleStreamingAwareError(c, status, code, message, streamStarted)
						return
					}
					continue
				}
				h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
			} else {
				h.handleFailoverExhaustedSimple(c, 502, streamStarted)
			}
			return
		}
		account := outcome.Account
		accountReleaseFunc := wrapReleaseOnDone(outcome.Ctx, outcome.Release)

		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		if !parsed.Stream && !jsonKeepaliveStarted {
			stopJSONKeepalive = service.StartOpenAIImagesJSONKeepalive(c, h.openAIImagesJSONKeepaliveInterval())
			jsonKeepaliveStarted = true
		}
		forwardStart := time.Now()
		writerSizeBeforeForward := service.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c)
		result, err := func() (*service.OpenAIForwardResult, error) {
			defer func() {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
			}()
			return h.gatewayService.ForwardImages(requestCtx, c, account, body, parsed, channelMapping.MappedModel)
		}()
		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
		responseLatencyMs := forwardDurationMs
		if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
			responseLatencyMs = forwardDurationMs - upstreamLatencyMs
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)
		if result != nil && result.FirstTokenMs != nil {
			service.SetOpsLatencyMs(c, service.OpsTimeToFirstTokenMsKey, int64(*result.FirstTokenMs))
		}
		if err != nil {
			if result != nil && result.ImageCount > 0 {
				reqLog.Warn("openai.images.forward_partial_error_with_image_result",
					zap.Int64("account_id", account.ID),
					zap.Int("image_count", result.ImageCount),
					zap.Error(err),
				)
			} else {
				var imageUpstreamErr *service.OpenAIImagesUpstreamError
				if errors.As(err, &imageUpstreamErr) {
					retryableServerError := service.IsOpenAIImagesRetryableUpstreamError(imageUpstreamErr)
					if retryableServerError {
						h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, requestModel, false, result), false, nil, err)
					} else {
						h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, requestModel, false, result), true, nil)
					}
					logEvent := "openai.images.upstream_user_error"
					if retryableServerError {
						logEvent = "openai.images.upstream_server_error_after_flush"
					}
					reqLog.Warn(logEvent,
						zap.Int64("account_id", account.ID),
						zap.Int("status_code", imageUpstreamErr.StatusCode),
						zap.String("error_type", imageUpstreamErr.ErrorType),
						zap.String("error_code", imageUpstreamErr.Code),
						zap.Error(err),
					)
					return
				}
				var failoverErr *service.UpstreamFailoverError
				if errors.As(err, &failoverErr) {
					h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, requestModel, false, result), false, nil, err)
					if service.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) != writerSizeBeforeForward {
						reqLog.Warn("openai.images.upstream_failover_skipped_after_flush",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
						)
						h.handleFailoverExhausted(c, failoverErr, true)
						return
					}
					if failoverClientGone(c) {
						reqLog.Info("openai.images.failover_aborted_client_disconnected",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
						)
						return
					}
					if failoverErr.RetryableOnSameAccount {
						retryLimit := effectiveSameAccountRetryLimit(failoverErr, account)
						if sameAccountRetryAllowed(failoverErr, sameAccountRetryCount[account.ID], retryLimit) {
							sameAccountRetryCount[account.ID]++
							retryDelay := sameAccountRetryDelayFor(failoverErr, sameAccountRetryCount[account.ID])
							reqLog.Warn("openai.images.pool_mode_same_account_retry",
								zap.Int64("account_id", account.ID),
								zap.Int("upstream_status", failoverErr.StatusCode),
								zap.Int("retry_limit", retryLimit),
								zap.Int("retry_count", sameAccountRetryCount[account.ID]),
								zap.Duration("retry_delay", retryDelay),
							)
							select {
							case <-requestCtx.Done():
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
						if h.tryAutoGroupFailover(c, &apiKey, clientRequestModel, failedGroupIDs, &subscription) {
							failedAccountIDs = make(map[int64]struct{})
							sameAccountRetryCount = make(map[int64]int)
							switchCount = 0
							profitVetoCount = 0
							lastFailoverErr = nil
							oauth429FailoverState = service.OpenAIOAuth429FailoverState{}
							channelMapping, _ = h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, clientRequestModel)
							routingModel = openAIChannelForwardModel(channelMapping, clientRequestModel)
							requestCtx = service.WithOpenAIImagesEndpoint(service.WithOpenAIImageGenerationIntent(c.Request.Context()))
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
					reqLog.Warn("openai.images.upstream_failover_switching",
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
						zap.Int("switch_count", switchCount),
						zap.Int("max_switches", maxAccountSwitches),
					)
					continue
				}
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, requestModel, false, result), false, nil, err)
				upstreamErrorAlreadyCommunicated := openAIForwardErrorAlreadyCommunicated(c, writerSizeBeforeForward, err)
				wroteFallback := false
				if !upstreamErrorAlreadyCommunicated {
					wroteFallback = h.ensureForwardErrorResponse(c, streamStarted)
				}
				fields := []zap.Field{
					zap.Int64("account_id", account.ID),
					zap.Bool("fallback_error_response_written", wroteFallback),
					zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
					zap.Error(err),
				}
				if shouldLogOpenAIForwardFailureAsWarn(c, wroteFallback) {
					reqLog.Warn("openai.images.forward_failed", fields...)
					return
				}
				reqLog.Error("openai.images.forward_failed", fields...)
				return
			}
		}
		if result != nil {
			// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
			if account.Type == service.AccountTypeOAuth && !account.IsShadow() {
				h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), account.ID, result.ResponseHeaders)
			}
			h.gatewayService.ReportOpenAIAccountScheduleResultWithLatency(account, openAIAccountScheduleModel(c, account, requestModel, false, result), true, result.FirstTokenMs, openAIServingGroupIDForLatency(c), openAIReasoningEffortForLatency(result))
		} else {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, requestModel, false, result), true, nil)
		}

		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		requestPayloadHash := service.HashUsageRequestPayload(body)
		if parsed.Multipart {
			requestPayloadHash = service.HashUsageRequestPayload([]byte(parsed.StickySessionSeed()))
		}
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
		quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)

		upstreamModel := ""
		if result != nil {
			upstreamModel = result.UpstreamModel
		}
		sessionID := service.ExtractClientSessionID(c)
		if h.relay != nil {
			// 从节点：写进本地扣费队列，主节点按凭证用同一个 RecordUsage 入账。
			h.relay.SubmitUsage(c, relayAttempt, OpenAIUsageFacts{
				InboundEndpoint: inboundEndpoint, UpstreamEndpoint: upstreamEndpoint, UserAgent: userAgent, IPAddress: clientIP,
				RequestPayloadHash: requestPayloadHash, SessionID: sessionID,
			}, result)
			return
		}
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
				ChannelUsageFields: clientRequestedUsageFields(c, channelMapping, requestModel, upstreamModel),
			}); err != nil {
				logger.L().With(
					zap.String("component", "handler.openai_gateway.images"),
					zap.Int64("user_id", subject.UserID),
					zap.Int64("api_key_id", apiKey.ID),
					zap.Any("group_id", apiKey.GroupID),
					zap.String("model", clientRequestModel),
					zap.Int64("account_id", account.ID),
				).Error("openai.images.record_usage_failed", zap.Error(err))
			}
		})

		reqLog.Debug("openai.images.request_completed",
			zap.Int64("account_id", account.ID),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}

func (h *OpenAIGatewayHandler) openAIImagesJSONKeepaliveInterval() time.Duration {
	if h.cfg == nil || h.cfg.Gateway.ImageNonstreamKeepaliveInterval <= 0 {
		return 0
	}
	return time.Duration(h.cfg.Gateway.ImageNonstreamKeepaliveInterval) * time.Second
}

func isMultipartImagesContentType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/form-data")
}
