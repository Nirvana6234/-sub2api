package routes

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// pawRelayAssignmentRequest：POST 连不上分配的节点时带上它的 ID（设计 10.9）。
type pawRelayAssignmentRequest struct {
	UnreachableNodeID int64 `json:"unreachable_node_id"`
}

// pawRelayAssignmentHandler 是小白端的分配查询接口（设计 10.9）：登录后、每隔 refresh_after 秒、切组后调 GET；
// 连不上分配的节点时调 POST 报告并拿新的分配。返回 role（relay / master）、node_id、base_url，从节点还带中转票据。
// 主从分流没开时 404（客户端沿用现在的主节点地址）；没有可用中转时 503 RELAY_UNAVAILABLE，客户端按 refresh_after 继续查。
func pawRelayAssignmentHandler(report bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			pawChatError(c, http.StatusUnauthorized, PawErrorCodeAuthRequired, "authenticated user is required")
			return
		}
		var unreachable int64
		if report {
			var req pawRelayAssignmentRequest
			if err := c.ShouldBindJSON(&req); err != nil || req.UnreachableNodeID <= 0 {
				pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "unreachable_node_id is required")
				return
			}
			unreachable = req.UnreachableNodeID
		}
		res, err := service.AssignRelayUser(c.Request.Context(), subject.UserID, unreachable)
		switch {
		case errors.Is(err, service.ErrRelayNotEnabled):
			pawChatError(c, http.StatusNotFound, "RELAY_NOT_ENABLED", "relay is not enabled")
			return
		case errors.Is(err, service.ErrRelayUnavailable):
			c.Header("Retry-After", "60")
			pawChatError(c, http.StatusServiceUnavailable, "RELAY_UNAVAILABLE", "no relay is available right now")
			return
		case errors.Is(err, service.ErrUserNotActive):
			pawChatError(c, http.StatusUnauthorized, "USER_INACTIVE", "User account is not active")
			return
		case err != nil:
			pawChatError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "relay assignment failed")
			return
		}
		if res.Role == service.RelayRoleMaster && res.BaseURL == "" {
			scheme := "https"
			if c.Request.TLS == nil && !strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
				scheme = "http"
			}
			res.BaseURL = scheme + "://" + c.Request.Host
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, res)
	}
}
