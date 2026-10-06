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
		if h == nil || !IsRelayForwardingRoute(c.Request.Method, c.FullPath()) {
			c.Next()
			return
		}
		ctx := c.Request.Context()
		if !h.g.BlockMasterForwarding(ctx) {
			c.Next()
			return
		}
		if marker := c.GetHeader(sign.HandoffHeader); marker != "" && h.g.VerifyHandoff(marker, c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		message := "This server does not forward API key requests; please use the address assigned to your API key"
		if addr := h.g.AssignedAddress(ctx, peekAPIKeyCredential(c)); addr != "" {
			message += ": " + addr
		}
		MarkIngressRejected(c, IngressRejectMasterRelayDisabled)
		WriteCapturedRejection(c, relayPermissionRejection(c.Request.Method, c.Request.URL.Path, "master_relay_disabled", message))
		c.Abort()
	}
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
