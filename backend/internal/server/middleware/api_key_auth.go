package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

const maxAPIKeyAuthorizationHeaderBytes = service.MaxAPIKeyCredentialBytes + 128

// NewAPIKeyAuthMiddleware 创建 API Key 认证中间件
func NewAPIKeyAuthMiddleware(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, cfg *config.Config) APIKeyAuthMiddleware {
	return APIKeyAuthMiddleware(apiKeyAuthWithSubscription(apiKeyService, subscriptionService, cfg))
}

// apiKeyAuthWithSubscription API Key认证中间件（支持订阅验证）
//
// 中间件职责分为两层：
//   - 鉴权（Authentication）：验证 Key 有效性、用户状态、IP 限制 —— 始终执行
//   - 计费执行（Billing Enforcement）：过期/配额/订阅/余额检查 —— skipBilling 时整块跳过
//
// /v1/usage、/v1/sub2api/billing 端点与异步生图任务查询只需鉴权，不需要计费执行。
// usage 允许过期/配额耗尽的 Key 查询自身用量，billing 用于读取当前 Key 的倍率配置，
// 异步生图查询允许已耗尽额度的 Key 拉取自身任务结果。
func apiKeyAuthWithSubscription(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ── 1. 提取 API Key ──────────────────────────────────────────
		if rejectInvalidAuthAbuse(c, apiKeyService) {
			AbortWithError(c, http.StatusTooManyRequests, "INVALID_AUTH_RATE_LIMITED", "Too many invalid authentication attempts; retry later")
			return
		}

		apiKeyString, ok := ExtractAPIKeyCredential(c, func() { recordInvalidAuthFailure(c, apiKeyService) })
		if !ok {
			return
		}

		// ── 2. 验证 Key 存在 ─────────────────────────────────────────

		apiKey, err := apiKeyService.GetByKey(c.Request.Context(), apiKeyString)
		if err != nil {
			if errors.Is(err, service.ErrAPIKeyNotFound) {
				recordInvalidAuthFailure(c, apiKeyService)
				MarkIngressRejected(c, IngressRejectInvalidAPIKey)
				AbortWithError(c, 401, "INVALID_API_KEY", "Invalid API key")
				return
			}
			if errors.Is(err, service.ErrAPIKeyAuthOverloaded) {
				MarkIngressRejected(c, IngressRejectAPIKeyAuthOverloaded)
				AbortWithError(c, http.StatusServiceUnavailable, "API_KEY_AUTH_OVERLOADED", "API key authentication is temporarily unavailable")
				return
			}
			if errors.Is(err, service.ErrAutoGroupUnavailable) {
				AbortWithError(c, http.StatusForbidden, "AUTO_GROUP_UNAVAILABLE", "No available group for this API key")
				return
			}
			AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to validate API key")
			return
		}

		// apiKey 已加载（含 User/Group）。即便后续因分组停用/Key 停用/用户停用/
		// IP 限制等早退中断，也让 Ops 错误日志能回退取到 user/group/platform。
		SetOpsFallbackAPIKey(c, apiKey)

		authenticateResolvedAPIKey(c, apiKey, apiKeyService, subscriptionService, cfg)
	}
}

func authenticateResolvedAPIKey(c *gin.Context, apiKey *service.APIKey, apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, cfg *config.Config) {
	if cfg == nil {
		cfg = &config.Config{}
	}
	in := APIKeyAuthInput{APIKey: apiKey, APIKeys: apiKeyService, Subscriptions: subscriptionService, Config: cfg,
		Method: c.Request.Method, Path: c.Request.URL.Path}
	if apiKey != nil && (len(apiKey.IPWhitelist) > 0 || len(apiKey.IPBlacklist) > 0) {
		in.ClientIP = ip.GetSecurityClientIP(c, cfg.TrustForwardedIPForAPIKeyACL())
	}

	if rejection := EvaluateAPIKeyAuthentication(in); rejection != nil {
		abortWithAPIKeyAuthRejection(c, rejection)
		return
	}
	setAuthenticatedAPIKeyRequestContext(c, apiKey)

	decision, rejection := EvaluateAPIKeyBilling(c.Request.Context(), in)
	if rejection != nil {
		abortWithAPIKeyAuthRejection(c, rejection)
		return
	}
	if decision.ContributionCreditOnly {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ContributionCreditOnly, true))
	}
	if decision.OwnContributedAccountsOnly {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.OwnContributedAccountsOnly, true))
	}
	setAuthenticatedAPIKeyGinContext(c, apiKey, decision.Subscription)
	if decision.TouchLastUsed {
		_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
	}

	c.Next()
}

// ExtractAPIKeyCredential 按网关鉴权的规则从请求头取 API Key（Authorization Bearer、x-api-key、x-goog-api-key；
// 查询参数里的 Key 已废弃）。取不到或不合法时写出拒绝并返回 false，写之前调用 onInvalid（无效鉴权计数，可为 nil）。
// 本地鉴权中间件和主从分流从节点的准入中间件共用。
func ExtractAPIKeyCredential(c *gin.Context, onInvalid func()) (string, bool) {
	invalid := func() {
		if onInvalid != nil {
			onInvalid()
		}
	}
	if apiKeyHeadersTooLarge(c) {
		invalid()
		MarkIngressRejected(c, IngressRejectInvalidAPIKey)
		AbortWithError(c, http.StatusUnauthorized, "INVALID_API_KEY", "Invalid API key")
		return "", false
	}

	queryKey := strings.TrimSpace(c.Query("key"))
	queryApiKey := strings.TrimSpace(c.Query("api_key"))
	if queryKey != "" || queryApiKey != "" {
		invalid()
		MarkIngressRejected(c, IngressRejectQueryAPIKeyDeprecated)
		AbortWithError(c, 400, "api_key_in_query_deprecated", "API key in query parameter is deprecated. Please use Authorization header instead.")
		return "", false
	}

	// 尝试从Authorization header中提取API key (Bearer scheme)
	authHeader := c.GetHeader("Authorization")
	var apiKeyString string

	if authHeader != "" {
		// 验证Bearer scheme
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			apiKeyString = strings.TrimSpace(parts[1])
		}
	}

	// 如果Authorization header中没有，尝试从x-api-key header中提取
	if apiKeyString == "" {
		apiKeyString = c.GetHeader("x-api-key")
	}
	if len(apiKeyString) > service.MaxAPIKeyCredentialBytes {
		invalid()
		MarkIngressRejected(c, IngressRejectInvalidAPIKey)
		AbortWithError(c, http.StatusUnauthorized, "INVALID_API_KEY", "Invalid API key")
		return "", false
	}

	// 如果x-api-key header中没有，尝试从x-goog-api-key header中提取（Gemini CLI兼容）
	if apiKeyString == "" {
		apiKeyString = c.GetHeader("x-goog-api-key")
	}

	// 如果所有header都没有API key
	if apiKeyString == "" {
		invalid()
		if hasAPIKeyCredentialInput(c) {
			MarkIngressRejected(c, IngressRejectInvalidAPIKey)
		} else {
			MarkIngressRejected(c, IngressRejectAPIKeyRequired)
		}
		AbortWithError(c, 401, "API_KEY_REQUIRED", "API key is required in Authorization header (Bearer scheme), x-api-key header, or x-goog-api-key header")
		return "", false
	}
	return apiKeyString, true
}

// abortWithAPIKeyAuthRejection 按鉴权判断的结果打运维标记、写错误响应。
func abortWithAPIKeyAuthRejection(c *gin.Context, r *APIKeyAuthRejection) {
	if r.OpsReason != "" {
		service.MarkOpsClientBusinessLimited(c, r.OpsReason)
	}
	if r.IngressReason != "" {
		MarkIngressRejected(c, r.IngressReason)
	}
	if r.OpenAIQuotaFormat {
		abortWithOpenAIQuotaError(c, r.Status, r.Message)
		return
	}
	AbortWithError(c, r.Status, r.Code, r.Message)
}

func setAuthenticatedAPIKeyGinContext(c *gin.Context, apiKey *service.APIKey, subscription *service.UserSubscription) {
	if subscription != nil {
		c.Set(string(ContextKeySubscription), subscription)
	}
	c.Set(string(ContextKeyAPIKey), apiKey)
	c.Set(string(ContextKeyUser), AuthSubject{
		UserID:      apiKey.User.ID,
		Concurrency: apiKey.User.Concurrency,
	})
	c.Set(string(ContextKeyUserRole), apiKey.User.Role)
	setGroupContext(c, apiKey.Group)
}

// ReplaceAuthenticatedAPIKey updates the authenticated request context after a
// request-aware router resolves a different group for an automatic API key.
// Authentication itself has already succeeded; this only replaces the routing
// snapshot before handlers inspect the group and platform.
func ReplaceAuthenticatedAPIKey(c *gin.Context, apiKey *service.APIKey, subscription *service.UserSubscription) {
	if c == nil || apiKey == nil {
		return
	}
	setAuthenticatedAPIKeyRequestContext(c, apiKey)
	setAuthenticatedAPIKeyGinContext(c, apiKey, subscription)
	SetOpsFallbackAPIKey(c, apiKey)
}

func apiKeyHeadersTooLarge(c *gin.Context) bool {
	if c == nil {
		return false
	}
	return len(c.GetHeader("Authorization")) > maxAPIKeyAuthorizationHeaderBytes ||
		len(c.GetHeader("x-api-key")) > service.MaxAPIKeyCredentialBytes ||
		len(c.GetHeader("x-goog-api-key")) > service.MaxAPIKeyCredentialBytes
}

func hasAPIKeyCredentialInput(c *gin.Context) bool {
	if c == nil {
		return false
	}
	return c.GetHeader("Authorization") != "" ||
		c.GetHeader("x-api-key") != "" ||
		c.GetHeader("x-goog-api-key") != ""
}

func isAsyncImageTaskRead(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	return strings.HasPrefix(path, "/v1/images/tasks/") || strings.HasPrefix(path, "/images/tasks/")
}

// GetAPIKeyFromContext 从上下文中获取API key
func GetAPIKeyFromContext(c *gin.Context) (*service.APIKey, bool) {
	value, exists := c.Get(string(ContextKeyAPIKey))
	if !exists {
		return nil, false
	}
	apiKey, ok := value.(*service.APIKey)
	return apiKey, ok
}

// SetOpsFallbackAPIKey 记录已加载的 API Key，供 Ops 错误日志在鉴权早退时回退使用。
// 与 ContextKeyAPIKey 区分：写入它不代表请求已通过鉴权，因此不影响 handler、
// 审计日志等对“已鉴权”的判断。
func SetOpsFallbackAPIKey(c *gin.Context, apiKey *service.APIKey) {
	if c == nil || apiKey == nil {
		return
	}
	c.Set(string(ContextKeyOpsFallbackAPIKey), apiKey)
}

// GetOpsFallbackAPIKey 读取 Ops 错误日志专用的回退 API Key。
func GetOpsFallbackAPIKey(c *gin.Context) (*service.APIKey, bool) {
	value, exists := c.Get(string(ContextKeyOpsFallbackAPIKey))
	if !exists {
		return nil, false
	}
	apiKey, ok := value.(*service.APIKey)
	return apiKey, ok
}

// GetSubscriptionFromContext 从上下文中获取订阅信息
func GetSubscriptionFromContext(c *gin.Context) (*service.UserSubscription, bool) {
	value, exists := c.Get(string(ContextKeySubscription))
	if !exists {
		return nil, false
	}
	subscription, ok := value.(*service.UserSubscription)
	return subscription, ok
}

func setGroupContext(c *gin.Context, group *service.Group) {
	c.Request = c.Request.WithContext(withGroupContext(c.Request.Context(), group))
}

func withGroupContext(ctx context.Context, group *service.Group) context.Context {
	if !service.IsGroupContextValid(group) {
		return ctx
	}
	if existing, ok := ctx.Value(ctxkey.Group).(*service.Group); ok && existing != nil && existing.ID == group.ID && service.IsGroupContextValid(existing) {
		return ctx
	}
	return context.WithValue(ctx, ctxkey.Group, group)
}

func setAuthenticatedAPIKeyRequestContext(c *gin.Context, apiKey *service.APIKey) {
	if c == nil || apiKey == nil {
		return
	}
	c.Request = c.Request.WithContext(withAuthenticatedAPIKey(c.Request.Context(), apiKey))
}

func withAuthenticatedAPIKey(ctx context.Context, apiKey *service.APIKey) context.Context {
	userID := apiKey.UserID
	if apiKey.User != nil && apiKey.User.ID > 0 {
		userID = apiKey.User.ID
	}
	if userID > 0 {
		ctx = context.WithValue(ctx, ctxkey.UserID, userID)
	}
	ctx = context.WithValue(ctx, ctxkey.APIKeyID, apiKey.ID)
	if strings.HasPrefix(apiKey.Name, "共飞工作台-") && strings.HasSuffix(apiKey.Name, "-客户端") {
		ctx = context.WithValue(ctx, ctxkey.WorkspaceLocalFallbackRoute, true)
	}
	return ctx
}

// RelayRequestContext 给主节点选号用的 ctx 装上本地鉴权中间件会放进请求 ctx 的值
// （用户、Key、工作台回退标记、贡献房间限制、分组），调度与计价读到的与单机一致。
func RelayRequestContext(ctx context.Context, adm RelayAPIKeyAdmission) context.Context {
	ctx = withAuthenticatedAPIKey(ctx, adm.APIKey)
	if adm.Billing.ContributionCreditOnly {
		ctx = context.WithValue(ctx, ctxkey.ContributionCreditOnly, true)
	}
	if adm.Billing.OwnContributedAccountsOnly {
		ctx = context.WithValue(ctx, ctxkey.OwnContributedAccountsOnly, true)
	}
	return withGroupContext(ctx, adm.APIKey.Group)
}

// apiKeyBalanceBelowAuthThreshold 保持鉴权层的历史语义：仅在余额耗尽（<=0）时拒绝。
// MinimumBalanceReserve 只作为 billing-cache 预检的保守下限，不得复用为鉴权硬门槛，
// 否则已配置该值的存量部署升级后，0 < balance < reserve 的用户会在所有端点被静默 403。
func apiKeyBalanceBelowAuthThreshold(balance float64, _ *config.Config) bool {
	return balance <= 0
}

func validateAPIKeyGroupAllowed(apiKey *service.APIKey) bool {
	if apiKey == nil || apiKey.GroupID == nil || apiKey.User == nil || apiKey.Group == nil {
		return true
	}
	group := apiKey.Group
	if group.IsSubscriptionType() {
		return true
	}
	return apiKey.User.CanBindGroup(group.ID, group.IsExclusive)
}

func validateAPIKeyGroupAvailable(apiKey *service.APIKey) (string, string, bool) {
	if apiKey == nil || apiKey.GroupID == nil {
		return "", "", true
	}
	group := apiKey.Group
	if group == nil || strings.EqualFold(group.Status, "deleted") {
		return "GROUP_DELETED", "API Key 所属分组已删除", false
	}
	if !group.IsActive() {
		return "GROUP_DISABLED", "API Key 所属分组已停用", false
	}
	return "", "", true
}
