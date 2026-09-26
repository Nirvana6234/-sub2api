package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// APIKeyAuthInput 是 API Key 鉴权判断的输入（不依赖 gin）。
//
// 本地的 apiKeyAuth 中间件和主从分流主节点的选号（开发计划 WP7）用同一套判断：
// 从节点本地鉴权后，主节点选号时凭 Key 原文再判一次，结果和单机一致。
type APIKeyAuthInput struct {
	APIKey        *service.APIKey
	APIKeys       *service.APIKeyService
	Subscriptions *service.SubscriptionService
	Config        *config.Config
	// ClientIP 是已按信任代理规则解析好的客户端 IP（IP 黑白名单用）。
	ClientIP string
	// Method、Path 是请求方法和路径（决定是否跳过计费检查、额度错误用哪种格式）。
	Method string
	Path   string
}

// APIKeyAuthRejection 是鉴权拒绝：状态码、错误码、文案，以及运维标记。
type APIKeyAuthRejection struct {
	Status  int
	Code    string
	Message string
	// IngressReason、OpsReason 为空时不标记。
	IngressReason IngressRejectReason
	OpsReason     string
	// OpenAIQuotaFormat：Key 额度用完的错误按 OpenAI 格式写（Responses 路径）。
	OpenAIQuotaFormat bool
}

// APIKeyBillingDecision 是计费检查通过后的结果。
type APIKeyBillingDecision struct {
	Subscription *service.UserSubscription
	// 简易模式下余额耗尽时的贡献房间限制（写进请求上下文）。
	ContributionCreditOnly     bool
	OwnContributedAccountsOnly bool
	// TouchLastUsed：是否刷新 Key 的最后使用时间（查账单信息的请求不刷）。
	TouchLastUsed bool
}

// EvaluateAPIKeyAuthentication 做鉴权的前一半：Key 状态、IP 限制、用户状态、分组可用与允许。
// 通过返回 nil。
func EvaluateAPIKeyAuthentication(in APIKeyAuthInput) *APIKeyAuthRejection {
	apiKey := in.APIKey
	if apiKey == nil {
		return &APIKeyAuthRejection{Status: 401, Code: "INVALID_API_KEY", Message: "Invalid API key"}
	}

	if !apiKey.IsActive() &&
		apiKey.Status != service.StatusAPIKeyExpired &&
		apiKey.Status != service.StatusAPIKeyQuotaExhausted {
		return &APIKeyAuthRejection{Status: 401, Code: "API_KEY_DISABLED", Message: "API key is disabled", IngressReason: IngressRejectAPIKeyDisabled}
	}

	if len(apiKey.IPWhitelist) > 0 || len(apiKey.IPBlacklist) > 0 {
		clientIP := in.ClientIP
		allowed, _ := ip.CheckIPRestrictionWithCompiledRules(clientIP, apiKey.CompiledIPWhitelist, apiKey.CompiledIPBlacklist)
		if !allowed {
			if clientIP == "" {
				clientIP = "unknown"
			}
			return &APIKeyAuthRejection{Status: 403, Code: "ACCESS_DENIED", Message: fmt.Sprintf("Access denied. Your IP is %s", clientIP),
				IngressReason: IngressRejectIPRestricted, OpsReason: service.OpsClientBusinessLimitedReasonIPRestriction}
		}
	}

	if apiKey.User == nil {
		return &APIKeyAuthRejection{Status: 401, Code: "USER_NOT_FOUND", Message: "User associated with API key not found"}
	}

	if !apiKey.User.IsActive() {
		return &APIKeyAuthRejection{Status: 401, Code: "USER_INACTIVE", Message: "User account is not active", IngressReason: IngressRejectUserInactive}
	}
	if code, message, ok := validateAPIKeyGroupAvailable(apiKey); !ok {
		reason := IngressRejectGroupDisabled
		if code == "GROUP_DELETED" {
			reason = IngressRejectGroupDeleted
		}
		return &APIKeyAuthRejection{Status: 403, Code: code, Message: message, IngressReason: reason,
			OpsReason: service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable}
	}
	if !validateAPIKeyGroupAllowed(apiKey) {
		return &APIKeyAuthRejection{Status: 403, Code: "GROUP_NOT_ALLOWED", Message: "API Key 所属专属分组不再允许当前用户使用",
			IngressReason: IngressRejectGroupNotAllowed, OpsReason: service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable}
	}
	return nil
}

// EvaluateAPIKeyBilling 做鉴权的后一半（计费执行）：订阅、Key 过期与额度、订阅用量窗口、余额。
// 查账单信息、用量、异步图片任务查询这几类请求跳过计费检查。
func EvaluateAPIKeyBilling(ctx context.Context, in APIKeyAuthInput) (APIKeyBillingDecision, *APIKeyAuthRejection) {
	cfg := in.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	apiKey := in.APIKey
	billingInfoRequest := in.Path == "/v1/sub2api/billing"
	skipBilling := in.Path == "/v1/usage" || billingInfoRequest || isAsyncImageTaskRead(in.Method, in.Path)
	decision := APIKeyBillingDecision{TouchLastUsed: !billingInfoRequest}

	if cfg.RunMode == config.RunModeSimple {
		if apiKeyBalanceBelowAuthThreshold(apiKey.User.Balance, cfg) {
			contributionBalance, contributionErr := in.APIKeys.GetContributionBalance(ctx, apiKey.User.ID)
			if contributionErr == nil && contributionBalance > 0 {
				decision.ContributionCreditOnly = true
			} else {
				decision.OwnContributedAccountsOnly = true
			}
		}
		return decision, nil
	}

	isSubscriptionType := apiKey.Group != nil && apiKey.Group.IsSubscriptionType()
	if isSubscriptionType && in.Subscriptions != nil && !billingInfoRequest {
		sub, subErr := in.Subscriptions.GetActiveSubscription(ctx, apiKey.User.ID, apiKey.Group.ID)
		if subErr != nil {
			if !skipBilling {
				return decision, &APIKeyAuthRejection{Status: 403, Code: "SUBSCRIPTION_NOT_FOUND", Message: "No active subscription found for this group"}
			}
		} else {
			decision.Subscription = sub
		}
	}
	if skipBilling {
		return decision, nil
	}

	quotaExhausted := &APIKeyAuthRejection{Status: http.StatusTooManyRequests, Code: "API_KEY_QUOTA_EXHAUSTED", Message: "API key 额度已用完",
		OpenAIQuotaFormat: isOpenAICompatibleAPIKeyPath(in.Path)}
	switch apiKey.Status {
	case service.StatusAPIKeyQuotaExhausted:
		return decision, quotaExhausted
	case service.StatusAPIKeyExpired:
		return decision, &APIKeyAuthRejection{Status: 403, Code: "API_KEY_EXPIRED", Message: "API key 已过期"}
	}
	if apiKey.IsExpired() {
		return decision, &APIKeyAuthRejection{Status: 403, Code: "API_KEY_EXPIRED", Message: "API key 已过期"}
	}
	if apiKey.IsQuotaExhausted() {
		return decision, quotaExhausted
	}

	if subscription := decision.Subscription; subscription != nil {
		needsMaintenance, validateErr := in.Subscriptions.ValidateAndCheckLimits(subscription, apiKey.Group)
		if needsMaintenance {
			refreshed, maintenanceErr := in.Subscriptions.EnsureWindowMaintenance(ctx, subscription)
			if maintenanceErr != nil {
				return decision, &APIKeyAuthRejection{Status: 500, Code: "SUBSCRIPTION_MAINTENANCE_FAILED", Message: "Failed to maintain subscription usage windows"}
			}
			decision.Subscription = refreshed
			_, validateErr = in.Subscriptions.ValidateAndCheckLimits(refreshed, apiKey.Group)
		}
		if validateErr != nil {
			code, status := "SUBSCRIPTION_INVALID", 403
			if errors.Is(validateErr, service.ErrDailyLimitExceeded) ||
				errors.Is(validateErr, service.ErrWeeklyLimitExceeded) ||
				errors.Is(validateErr, service.ErrMonthlyLimitExceeded) {
				code, status = "USAGE_LIMIT_EXCEEDED", 429
			}
			return decision, &APIKeyAuthRejection{Status: status, Code: code, Message: validateErr.Error()}
		}
	} else if apiKeyBalanceBelowAuthThreshold(apiKey.User.Balance, cfg) {
		// 贡献房间自用（OwnContributedAccountsOnly/ContributionCreditOnly）本应放行
		// 余额耗尽用户但把调度限定在其自己贡献/信用范围内；但下游调度层
		// （gateway_scheduling.go 等）目前并未读取这两个 context key 做任何限制，
		// 放行等于让零余额用户白嫖整个共享账号池。在配套的调度层限制补齐之前，
		// 维持鉴权层的历史语义：余额耗尽直接拒绝。
		return decision, &APIKeyAuthRejection{Status: 403, Code: "INSUFFICIENT_BALANCE", Message: "Insufficient account balance"}
	}
	return decision, nil
}

// isOpenAICompatibleAPIKeyPath：Responses 系列路径的 Key 额度错误按 OpenAI 格式写。
func isOpenAICompatibleAPIKeyPath(path string) bool {
	path = strings.TrimRight(path, "/")
	for _, root := range []string{
		"/v1/responses",
		"/openai/v1/responses",
		"/responses",
		"/backend-api/codex/responses",
	} {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}
