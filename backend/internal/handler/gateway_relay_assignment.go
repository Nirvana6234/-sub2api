package handler

import (
	"net/http"
	"strings"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// RelayAssignment 返回这把 API Key 分配的接入地址（设计 10.9：API Key 版本的分配查询）。
// GET /v1/relay/assignment —— 主节点域名和所有从节点都可以访问（从节点交给主节点执行，8.4），不返回票据。
// Key 被重新分配后，工具可以用它发现新地址。
func (h *GatewayHandler) RelayAssignment(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	c.Header("Cache-Control", "no-store")
	// 还没分配（主从分流没开，或开关打开前建的 Key）：沿用现在的地址。
	if apiKey.RelayNodeID == nil {
		c.JSON(http.StatusOK, service.RelayAddress{Role: service.RelayRoleUnassigned, BaseURL: requestBaseURL(c)})
		return
	}
	addr, ok := service.ResolveRelayAddressForKey(c.Request.Context(), apiKey.RelayNodeID)
	if !ok {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The address assigned to this API key is not available")
		return
	}
	if addr.Role == service.RelayRoleMaster && addr.BaseURL == "" {
		addr.BaseURL = requestBaseURL(c)
	}
	c.JSON(http.StatusOK, addr)
}

// requestBaseURL 是当前请求用的站点地址（没有配置 api_base_url 时主节点的地址）。
func requestBaseURL(c *gin.Context) string {
	scheme := "https"
	if c.Request.TLS == nil && !strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host
}
