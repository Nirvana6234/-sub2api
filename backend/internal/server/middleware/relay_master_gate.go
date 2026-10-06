package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/gin-gonic/gin"
)

// 主节点分配比例为 0 时，主节点不做任何中转（设计 10.5、10.8）：API Key 的转发请求打到主节点回 403，提示改用这把 Key 分配的地址。
// 例外：从节点交给主节点转发的请求（带主节点验过的标记，sign.HandoffHeader）、不是转发的接口（模型列表、用量、余额、批量图片、
// 分配查询）、网页和后台。

// RelayMasterGate 由主从分流运行时实现；没有（开关关闭）时这个中间件什么都不做。
type RelayMasterGate interface {
	// BlockMasterForwarding 报告主节点现在不转发（主从分流在运行且分配比例为 0）。
	BlockMasterForwarding(ctx context.Context) bool
	// VerifyHandoff 验从节点交给主节点的请求标记。
	VerifyHandoff(header, method, path string) bool
	// AssignedAddress 返回这把 Key 分配的地址（提示用）；取不到时为空。
	AssignedAddress(ctx context.Context, rawKey string) string
	// AcquireMasterSlot 占主节点转发的一个并发名额（设计 10.5：主节点转发有上限）；ok 为 false 时回"服务繁忙"。
	AcquireMasterSlot(ctx context.Context) (release func(), ok bool)
}

type relayMasterGateHolder struct{ g RelayMasterGate }

var activeRelayMasterGate atomic.Pointer[relayMasterGateHolder]

// SetRelayMasterGate 设置（nil 取消）主节点转发闸门。
func SetRelayMasterGate(g RelayMasterGate) {
	if g == nil {
		activeRelayMasterGate.Store(nil)
		return
	}
	activeRelayMasterGate.Store(&relayMasterGateHolder{g: g})
}

// RelayMasterGateMiddleware 挂在整个 HTTP 引擎上：只对"API Key 转发路由"生效（按路由模板判断，不认识的路由一律放行）。
func RelayMasterGateMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := activeRelayMasterGate.Load()
		if h == nil {
			c.Next()
			return
		}
		fullPath := c.FullPath()
		forwarding := IsRelayForwardingRoute(c.Request.Method, fullPath)
		if !forwarding && !IsMasterCappedRoute(fullPath) {
			c.Next()
			return
		}
		ctx := c.Request.Context()
		handedOff := false
		if marker := c.GetHeader(sign.HandoffHeader); marker != "" && h.g.VerifyHandoff(marker, c.Request.Method, c.Request.URL.Path) {
			handedOff = true
		}
		// 主节点分配比例为 0：API Key 的转发请求不接（从节点交来的除外）。
		if forwarding && !handedOff && h.g.BlockMasterForwarding(ctx) {
			message := "This server does not forward API key requests; please use the address assigned to your API key"
			if addr := h.g.AssignedAddress(ctx, peekAPIKeyCredential(c)); addr != "" {
				message += ": " + addr
			}
			MarkIngressRejected(c, IngressRejectMasterRelayDisabled)
			WriteCapturedRejection(c, relayRejection(c.Request.Method, c.Request.URL.Path, http.StatusForbidden, "master_relay_disabled", "permission_error", message))
			c.Abort()
			return
		}
		// 主节点转发有上限（设计 10.5）：超过并发或带宽上限回"服务繁忙"；网页聊天、游客试用和从节点交来的请求同样计入。
		release, ok := h.g.AcquireMasterSlot(ctx)
		if !ok {
			c.Header("Retry-After", "5")
			MarkIngressRejected(c, IngressRejectMasterBusy)
			WriteCapturedRejection(c, relayRejection(c.Request.Method, c.Request.URL.Path, http.StatusServiceUnavailable, "server_busy", "api_error", "The server is busy, please retry shortly"))
			c.Abort()
			return
		}
		defer release()
		c.Next()
	}
}

// IsMasterCappedRoute 报告一条路由模板是不是计入主节点转发上限的非 API Key 转发入口：
// 小白端转发接口（/api/v1/paw/ 下会选账号的）、网页聊天（playground）、游客试用——它们同样在转发上游流量（设计 10.5）。
// 手机同步会话、登录态的其他接口不算（连接长、消息小，算进去超限时会把手机连接断掉）。
func IsMasterCappedRoute(fullPath string) bool {
	switch {
	case strings.HasPrefix(fullPath, "/api/v1/paw/"):
		rest := strings.TrimPrefix(fullPath, "/api/v1/paw/")
		for _, seg := range []string{"responses", "messages", "chat/completions", "images/", "systemone"} {
			if rest == strings.TrimSuffix(seg, "/") || strings.HasPrefix(rest, seg) {
				return true
			}
		}
		return false
	case strings.HasPrefix(fullPath, "/api/v1/playground/"), strings.HasPrefix(fullPath, "/api/v1/trial/"):
		return true
	}
	return false
}

// peekAPIKeyCredential 只读出请求里的 API Key（不写任何错误、不记无效鉴权）；没有时为空。
func peekAPIKeyCredential(c *gin.Context) string {
	if auth := c.GetHeader("Authorization"); auth != "" {
		if parts := strings.SplitN(auth, " ", 2); len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			if v := strings.TrimSpace(parts[1]); v != "" {
				return v
			}
		}
	}
	for _, name := range []string{"x-api-key", "x-goog-api-key"} {
		if v := strings.TrimSpace(c.GetHeader(name)); v != "" {
			return v
		}
	}
	return ""
}

// forwardingPrefixes 是网关入口的路径前缀；长的在前（/v1beta 要先于 /v1 去掉），"" 是根路径别名。
var forwardingPrefixes = []string{"/antigravity/v1beta", "/antigravity/v1", "/backend-api/codex", "/api/v3", "/v1beta", "/v1", "/v3", ""}

// forwardingSegments 是会选账号、转发到上游的入口（去掉前缀后的第一段，及其子路径）。
var forwardingSegments = []string{
	"/messages", "/responses", "/chat/completions", "/images/generations", "/images/edits", "/videos", "/tts", "/stt",
	"/custom-voices", "/realtime", "/embeddings", "/alpha/search", "/systemone", "/web_search", "/x_search",
	"/contents/generations/tasks", "/live",
}

// IsRelayForwardingRoute 报告一条路由模板（c.FullPath()）是不是 API Key 的转发入口（设计 8.3 的"转发"那一类）。
// 用正面清单：模型列表、用量、余额、批量图片、图片任务查询、分配查询等不是转发的接口（8.4）不在里面，不认识的路由也放行。
func IsRelayForwardingRoute(method, fullPath string) bool {
	if fullPath == "" {
		return false
	}
	rest, matched := fullPath, false
	for _, p := range forwardingPrefixes {
		if p != "" && (fullPath == p || strings.HasPrefix(fullPath, p+"/")) {
			rest, matched = strings.TrimPrefix(fullPath, p), true
			break
		}
	}
	if !matched {
		rest = fullPath
	}
	if rest == "" {
		return false
	}
	for _, seg := range forwardingSegments {
		if rest == seg || strings.HasPrefix(rest, seg+"/") {
			return true
		}
	}
	// Gemini 原生入口：POST /models/*modelAction 是生成；GET /models（列表）、/models/:model（详情）不是转发。
	if method == http.MethodPost && strings.HasPrefix(rest, "/models/") {
		return matched
	}
	return false
}
