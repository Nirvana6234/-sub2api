package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// typeSafeMaxAccountSwitches 是一次 Jev 请求最多换几次号。
const typeSafeMaxAccountSwitches = 3

// SystemOne 转发 TypeSafe 的 Jev 判断请求（POST /v1/systemone、POST /api/v1/paw/systemone）。
//
// 两个入口都把已认证的 API Key 放进上下文再进来：/v1 是用户自己的 key，/paw 是
// 带上 X-Paw-Group-Id 分组的内部 key 副本。请求体是用户的聊天文字，所以这里
// 不做内容审计和内容审核，日志也只记状态、账号、用量和耗时。
func (h *GatewayHandler) SystemOne(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		typeSafeError(c, http.StatusUnauthorized, "authentication_error", "AUTH_REQUIRED", "Invalid API key")
		return
	}
	if apiKey.Group == nil || apiKey.Group.Platform != service.PlatformTypeSafe {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		typeSafeError(c, http.StatusNotFound, "not_found_error", "PLATFORM_UNSUPPORTED", "System One API is not supported for this platform")
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			typeSafeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "REQUEST_TOO_LARGE", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		typeSafeError(c, http.StatusBadRequest, "invalid_request_error", "INVALID_REQUEST", "Failed to read request body")
		return
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		typeSafeError(c, http.StatusBadRequest, "invalid_request_error", "INVALID_REQUEST", "Failed to parse request body")
		return
	}
	modelResult := gjson.GetBytes(body, "model")
	model := strings.TrimSpace(modelResult.String())
	if modelResult.Type != gjson.String || model == "" {
		typeSafeError(c, http.StatusBadRequest, "invalid_request_error", "INVALID_REQUEST", "model is required")
		return
	}
	setOpsRequestContext(c, model, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))

	reqLog := requestLogger(c, "handler.gateway.systemone",
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("model", model),
	)

	// 两条网关找不到价格时都按 0 元入账，Jev 不能这样免费用：没配价格就不转发。
	if !h.gatewayService.HasTypeSafePricing(c.Request.Context(), model, apiKey) {
		reqLog.Warn("gateway.systemone.pricing_missing")
		typeSafeError(c, http.StatusServiceUnavailable, "api_error", "PRICING_UNAVAILABLE", "Pricing is not configured for this model")
		return
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		typeSafeError(c, status, code, strings.ToUpper(code), message)
		return
	}

	failedAccounts := make(map[int64]struct{})
	var lastFailover *service.UpstreamFailoverError
	for attempt := 0; attempt <= typeSafeMaxAccountSwitches; attempt++ {
		selected, selectErr := h.gatewayService.SelectAccountWithLoadAwareness(
			c.Request.Context(), apiKey.GroupID, "", model, failedAccounts, "", 0,
		)
		if selectErr != nil || selected == nil || selected.Account == nil {
			if lastFailover != nil {
				break
			}
			if selectErr != nil {
				reqLog.Warn("gateway.systemone.account_select_failed", zap.Error(selectErr))
			}
			markOpsRoutingCapacityLimited(c)
			typeSafeError(c, http.StatusServiceUnavailable, "api_error", "NO_AVAILABLE_ACCOUNTS", "No available accounts")
			return
		}
		release, acquired, acquireErr := h.acquireWebSearchAccountSlot(c, selected)
		if !acquired {
			if attempt == 0 && acquireErr != nil {
				h.handleConcurrencyError(c, acquireErr, "account", false)
				return
			}
			failedAccounts[selected.Account.ID] = struct{}{}
			continue
		}
		account := selected.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)

		result, forwardErr := h.gatewayService.ForwardTypeSafeSystemOne(c.Request.Context(), c, account, body)
		if release != nil {
			release()
		}
		if forwardErr == nil {
			h.recordSystemOneUsage(c, apiKey, subscription, account, result)
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

func (h *GatewayHandler) recordSystemOneUsage(c *gin.Context, apiKey *service.APIKey, subscription *service.UserSubscription, account *service.Account, result *service.ForwardResult) {
	if result == nil {
		return
	}
	if result.RequestID == "" {
		// request_id 是扣费的幂等键，上游没给就每次现生成，不能让两次请求撞在一起。
		result.RequestID = "systemone:" + uuid.NewString()
	}
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
			Result:           result,
			APIKey:           apiKey,
			User:             apiKey.User,
			Account:          account,
			Subscription:     subscription,
			InboundEndpoint:  EndpointSystemOne,
			UpstreamEndpoint: EndpointSystemOne,
			UserAgent:        userAgent,
			IPAddress:        clientIP,
			APIKeyService:    h.apiKeyService,
			QuotaPlatform:    quotaPlatform,
		}); err != nil {
			logger.L().With(
				zap.String("component", "handler.gateway.systemone"),
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
