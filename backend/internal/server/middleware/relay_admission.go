package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
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

// CaptureResponse 在一个临时 gin 上下文里运行 write，收集它写出的响应（主节点按本地写法生成拒绝，交给从节点原样写出）。
func CaptureResponse(method, path string, write func(c *gin.Context)) *CapturedRejection {
	return captureRejection(method, path, write)
}

// RelayNodeNotAssignedRejection 是"这个 Key 不是分配给这台从节点"的拒绝（节点规则为"仅分配的从节点"，设计 10.2）：403，
// 按入口写成 OpenAI / Anthropic / Google 的错误格式，提示改用分配的地址。
func RelayNodeNotAssignedRejection(method, path, message string) *CapturedRejection {
	return relayPermissionRejection(method, path, "api_key_node_mismatch", message)
}

// relayPermissionRejection 是主从分流的 403 拒绝：按入口写成 OpenAI / Anthropic / Google 的错误格式。
func relayPermissionRejection(method, path, code, message string) *CapturedRejection {
	return captureRejection(method, path, func(c *gin.Context) {
		switch {
		case IsGoogleRelayPath(path):
			abortWithGoogleError(c, http.StatusForbidden, message)
		case strings.Contains(path, "/messages"):
			AnthropicErrorWriter(c, http.StatusForbidden, message)
			c.Abort()
		default:
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": message, "type": "permission_error", "code": code}})
			c.Abort()
		}
	})
}

// RelayAPIKeyAdmissionInput 是主节点复查 API Key 请求的输入。
type RelayAPIKeyAdmissionInput struct {
	APIKeyAuthInput
	// RawKey 是从节点转来的 Key 原文。
	RawKey   string
	Settings *service.SettingService
	// Models 是请求里可能被下游解析到的全部模型名（从节点按分组白名单中间件的规则提取）。
	Models []string
	// Google：Gemini 原生入口（/v1beta、/antigravity/v1beta），错误按 Google 格式写，鉴权按
	// APIKeyAuthWithSubscriptionGoogle（简易模式不查计费；未分组拦截按 requireGroupGoogle）。
	Google bool
	// AutoGroup 给自动分组 Key 定这次请求用的分组（本地 autoGroupModelRoutingMiddleware 那一步）：返回换好分组的
	// Key 快照；返回的 Key 与传入的分组相同表示保留鉴权时的冷启动分组。nil 时自动分组 Key 回"暂不支持"。
	AutoGroup func(ctx context.Context, apiKey *service.APIKey) (*service.APIKey, error)
}

// RelayAPIKeyAdmission 是复查通过的结果。
type RelayAPIKeyAdmission struct {
	APIKey  *service.APIKey
	Billing APIKeyBillingDecision
}

// ErrRelayAdmissionUnsupported：这个 Key 的请求还不能经主从分流处理。调用方回"暂不支持"，从节点改由主节点转发。
// AutoGroup 也可以返回它（选定的分组从节点还接不了）。
var ErrRelayAdmissionUnsupported = errors.New("this API key cannot be served through relay nodes yet")

// EvaluateRelayAPIKeyAdmission 按本地网关中间件链的顺序复查一个 API Key 请求：
// 全局 IP 黑名单 → Key 鉴权与计费检查 → 全局用户黑名单 → 自动分组 → 分组模型白名单 → 未分组拦截。
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

	authRejection := abortWithAPIKeyAuthRejection
	unassignedWriter := AnthropicErrorWriter
	if in.Google {
		authRejection, unassignedWriter = abortWithGoogleAPIKeyAuthRejection, GoogleErrorWriter
	}

	apiKey, err := in.APIKeys.GetByKey(ctx, in.RawKey)
	if err != nil && in.Google {
		return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) {
			switch {
			case errors.Is(err, service.ErrAPIKeyNotFound):
				MarkIngressRejected(c, IngressRejectInvalidAPIKey)
				abortWithGoogleError(c, 401, "Invalid API key")
			case errors.Is(err, service.ErrAPIKeyAuthOverloaded):
				MarkIngressRejected(c, IngressRejectAPIKeyAuthOverloaded)
				abortWithGoogleError(c, http.StatusServiceUnavailable, "API key authentication is temporarily unavailable")
			default:
				abortWithGoogleError(c, 500, "Failed to validate API key")
			}
		}), nil
	}
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
		return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) { authRejection(c, r) }), nil
	}
	var billing APIKeyBillingDecision
	if in.Google && in.Config != nil && in.Config.RunMode == config.RunModeSimple {
		// Gemini 入口的鉴权在简易模式下直接放行（不查余额、订阅，也没有贡献房间的限定）。
	} else {
		var r *APIKeyAuthRejection
		billing, r = EvaluateAPIKeyBilling(ctx, in.APIKeyAuthInput)
		if r != nil {
			return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) { authRejection(c, r) }), nil
		}
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
		if in.AutoGroup == nil {
			return RelayAPIKeyAdmission{}, nil, ErrRelayAdmissionUnsupported
		}
		resolved, err := in.AutoGroup(ctx, apiKey)
		if errors.Is(err, ErrRelayAdmissionUnsupported) {
			return RelayAPIKeyAdmission{}, nil, err
		}
		if err != nil || resolved == nil {
			// 与本地自动分组中间件选组出错时的写法一致。
			return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) {
				if errors.Is(err, service.ErrAutoGroupUnavailable) {
					AbortWithError(c, http.StatusForbidden, "AUTO_GROUP_UNAVAILABLE", "No available group satisfies the automatic routing requirements")
				} else {
					AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve automatic API key group")
				}
			}), nil
		}
		if resolved.GroupID != nil && (apiKey.GroupID == nil || *resolved.GroupID != *apiKey.GroupID) {
			// 与本地一样：换了分组时订阅按新分组取（取不到就没有订阅）。
			billing.Subscription = nil
			if resolved.Group != nil && resolved.Group.IsSubscriptionType() && in.Subscriptions != nil {
				billing.Subscription, _ = in.Subscriptions.GetActiveSubscription(ctx, resolved.UserID, resolved.Group.ID)
			}
		}
		apiKey = resolved
	}
	if apiKey.Group != nil && apiKey.Group.ModelAllowlistEnabled() {
		if blocked := firstModelNotAllowed(apiKey.Group.ModelAllowlist, in.Models); blocked != "" {
			return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) { abortGroupModelNotAllowed(c, blocked) }), nil
		}
	}
	if apiKey.GroupID == nil && (in.Settings == nil || !in.Settings.IsUngroupedKeySchedulingAllowed(ctx)) {
		return RelayAPIKeyAdmission{}, capture(func(c *gin.Context) { abortGroupUnassigned(c, unassignedWriter) }), nil
	}
	return RelayAPIKeyAdmission{APIKey: apiKey, Billing: billing}, nil, nil
}

// abortWithGoogleAPIKeyAuthRejection 是 Gemini 入口的鉴权拒绝（Google 格式，运维标记同 abortWithAPIKeyAuthRejection）。
func abortWithGoogleAPIKeyAuthRejection(c *gin.Context, r *APIKeyAuthRejection) {
	if r.OpsReason != "" {
		service.MarkOpsClientBusinessLimited(c, r.OpsReason)
	}
	if r.IngressReason != "" {
		MarkIngressRejected(c, r.IngressReason)
	}
	abortWithGoogleError(c, r.Status, r.Message)
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
