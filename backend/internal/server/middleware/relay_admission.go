package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// CapturedRejection 是按本地中间件同样的写法生成的拒绝响应：主从分流的主节点在选号时做这些检查
// （设计 3.2），生成的状态码、响应体、响应头和运维标记原样交给从节点写出，与单机一致。
type CapturedRejection struct {
	Status        int
	Header        http.Header
	Body          []byte
	IngressReason string
	OpsReason     string
}

// captureRejection 在一个临时 gin 上下文里运行 write，收集它写出的响应和打的运维标记。
func captureRejection(method, path string, write func(c *gin.Context)) *CapturedRejection {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, "http://relay.local/", nil)
	c.Request.URL.Path = path
	write(c)
	out := &CapturedRejection{Status: rec.Code, Header: rec.Header().Clone(), Body: rec.Body.Bytes(), OpsReason: service.OpsClientBusinessLimitedReason(c)}
	if reason, ok := GetIngressRejectReason(c); ok {
		out.IngressReason = string(reason)
	}
	return out
}

// RelayAPIKeyAdmissionInput 是主节点复查 API Key 请求的输入。
type RelayAPIKeyAdmissionInput struct {
	APIKeyAuthInput
	// RawKey 是从节点转来的 Key 原文。
	RawKey   string
	Settings *service.SettingService
	// Models 是请求里可能被下游解析到的全部模型名（从节点按分组白名单中间件的规则提取）。
	Models []string
}

// RelayAPIKeyAdmission 是复查通过的结果。
type RelayAPIKeyAdmission struct {
	APIKey  *service.APIKey
	Billing APIKeyBillingDecision
}

// ErrRelayAdmissionUnsupported：这个 Key 的请求还不能经主从分流处理（自动分组、组合平台分组，
// 开发计划 WP7 逐步接入）。调用方回"暂不支持"，从节点改由主节点转发。
var ErrRelayAdmissionUnsupported = errors.New("this API key cannot be served through relay nodes yet")

// EvaluateRelayAPIKeyAdmission 按本地网关中间件链的顺序复查一个 API Key 请求：
// 全局 IP 黑名单 → Key 鉴权与计费检查 → 全局用户黑名单 → 分组模型白名单 → 未分组拦截。
// 被拒时返回按本地写法生成的响应；Key 的分组要走还没接入的路径时返回 ErrRelayAdmissionUnsupported。
// 未分组拦截按 /v1 网关链的 Anthropic 错误格式（与 routes/gateway.go 的 requireGroupAnthropic 一致）。
func EvaluateRelayAPIKeyAdmission(ctx context.Context, in RelayAPIKeyAdmissionInput) (RelayAPIKeyAdmission, *CapturedRejection, error) {
	capture := func(write func(c *gin.Context)) *CapturedRejection {
		return captureRejection(in.Method, in.Path, write)
	}
	if in.Settings != nil {
		matched, entry, err := in.Settings.IsGloballyBlacklisted(ctx, 0, in.ClientIP)
		if err != nil || matched {
			return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) { abortIfGloballyBlacklisted(c, matched, entry, err) }), nil
		}
	}

	apiKey, err := in.APIKeys.GetByKey(ctx, in.RawKey)
	if err != nil {
		return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) {
			switch {
			case errors.Is(err, service.ErrAPIKeyNotFound):
				MarkIngressRejected(c, IngressRejectInvalidAPIKey)
				AbortWithError(c, 401, "INVALID_API_KEY", "Invalid API key")
			case errors.Is(err, service.ErrAPIKeyAuthOverloaded):
				MarkIngressRejected(c, IngressRejectAPIKeyAuthOverloaded)
				AbortWithError(c, http.StatusServiceUnavailable, "API_KEY_AUTH_OVERLOADED", "API key authentication is temporarily unavailable")
			case errors.Is(err, service.ErrAutoGroupUnavailable):
				AbortWithError(c, http.StatusForbidden, "AUTO_GROUP_UNAVAILABLE", "No available group for this API key")
			default:
				AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to validate API key")
			}
		}), nil
	}
	in.APIKey = apiKey
	if r := EvaluateAPIKeyAuthentication(in.APIKeyAuthInput); r != nil {
		return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) { abortWithAPIKeyAuthRejection(c, r) }), nil
	}
	billing, r := EvaluateAPIKeyBilling(ctx, in.APIKeyAuthInput)
	if r != nil {
		return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) { abortWithAPIKeyAuthRejection(c, r) }), nil
	}

	if in.Settings != nil {
		userID := apiKey.UserID
		if apiKey.User != nil && apiKey.User.ID > 0 {
			userID = apiKey.User.ID
		}
		matched, entry, err := in.Settings.IsGloballyBlacklisted(ctx, userID, in.ClientIP)
		if err != nil || matched {
			return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) { abortIfGloballyBlacklisted(c, matched, entry, err) }), nil
		}
	}
	if apiKey.AutoGroup {
		return RelayAPIKeyAdmission{}, nil, ErrRelayAdmissionUnsupported
	}
	if apiKey.Group != nil && apiKey.Group.ModelAllowlistEnabled() {
		if blocked := firstModelNotAllowed(apiKey.Group.ModelAllowlist, in.Models); blocked != "" {
			return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) { abortGroupModelNotAllowed(c, blocked) }), nil
		}
	}
	if apiKey.GroupID == nil && (in.Settings == nil || !in.Settings.IsUngroupedKeySchedulingAllowed(ctx)) {
		return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) { abortGroupUnassigned(c, AnthropicErrorWriter) }), nil
	}
	return RelayAPIKeyAdmission{APIKey: apiKey, Billing: billing}, nil, nil
}

// WriteCapturedRejection 在从节点上写出主节点生成的拒绝：原样的状态码、响应头、响应体和运维标记。
func WriteCapturedRejection(c *gin.Context, r *CapturedRejection) {
	for k, vs := range r.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	if r.IngressReason != "" {
		MarkIngressRejected(c, IngressRejectReason(r.IngressReason))
	}
	if r.OpsReason != "" {
		service.MarkOpsClientBusinessLimited(c, r.OpsReason)
	}
	c.Status(r.Status)
	_, _ = c.Writer.Write(r.Body)
	c.Abort()
}
