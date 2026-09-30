package middleware

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AutoGroupObservedResult 是自动分组 Key 一次请求结束时拿去调整选组的状态码和首字耗时（单机的自动分组中间件与
// 主从分流的从节点共用）。在处理函数返回之后调用。
func AutoGroupObservedResult(c *gin.Context) (status int, firstTokenMs *int64) {
	status = c.Writer.Status()
	if streamErr, ok := service.GetOpsStreamError(c); ok && streamErr.IntendedStatus >= http.StatusBadRequest {
		status = streamErr.IntendedStatus
	}
	// Handlers map upstream 529 to a client-facing 503. Preserve the raw
	// upstream status for auto-group observation so overload is not mistaken
	// for a confirmed group failure.
	//
	// Only a failed request may take the raw upstream status. The key is
	// written by every upstream attempt and is not cleared when a later
	// attempt (often on another group after auto-group failover) succeeds;
	// letting it override a final 2xx would record the earlier group's 503
	// against the group that actually served the request.
	if rawStatus, ok := c.Get(service.OpsUpstreamStatusCodeKey); ok && status >= http.StatusBadRequest {
		switch typed := rawStatus.(type) {
		case int:
			if typed > 0 {
				status = typed
			}
		case int32:
			if typed > 0 {
				status = int(typed)
			}
		case int64:
			if typed > 0 {
				status = int(typed)
			}
		}
	}
	return status, autoGroupFirstTokenMs(c)
}

func autoGroupFirstTokenMs(c *gin.Context) *int64 {
	if c == nil {
		return nil
	}
	value, ok := c.Get(service.OpsTimeToFirstTokenMsKey)
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case int64:
		return &typed
	case int:
		converted := int64(typed)
		return &converted
	case int32:
		converted := int64(typed)
		return &converted
	default:
		return nil
	}
}
