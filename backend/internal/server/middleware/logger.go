package middleware

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Logger 请求日志中间件
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 开始时间
		startTime := time.Now()

		// 请求路径
		path := c.Request.URL.Path

		// 处理请求
		c.Next()

		// 跳过健康检查和管理用量分页等高频路径的访问日志。
		// 这只影响日志记录，不影响请求处理或响应。
		if shouldSkipAccessLog(path) {
			return
		}

		endTime := time.Now()
		latency := endTime.Sub(startTime)

		method := c.Request.Method
		statusCode := c.Writer.Status()
		clientIP := ip.GetClientIP(c)
		protocol := c.Request.Proto
		accountID, hasAccountID := c.Request.Context().Value(ctxkey.AccountID).(int64)
		platform, _ := c.Request.Context().Value(ctxkey.Platform).(string)
		model, _ := c.Request.Context().Value(ctxkey.Model).(string)
		reason, rejected := GetIngressRejectReason(c)
		if rejected {
			recordIngressReject(c, reason)
			allowed, droppedSummary := globalIngressRejectAccessSampler.allow(endTime)
			if droppedSummary > 0 {
				logger.FromContext(c.Request.Context()).Info("ingress rejection access logs dropped",
					zap.String("component", "http.access"),
					zap.Uint64("dropped_count", droppedSummary),
					zap.Bool(logger.OpsSystemLogSkipField, true),
				)
			}
			if !allowed {
				return
			}
		}

		fields := []zap.Field{
			zap.String("component", "http.access"),
			zap.Int("status_code", statusCode),
			zap.Int64("latency_ms", latency.Milliseconds()),
			zap.String("client_ip", clientIP),
			zap.String("protocol", protocol),
			zap.String("method", method),
			zap.String("path", path),
		}
		if rejected {
			fields = append(fields,
				zap.String("ingress_reject_reason", string(reason)),
				zap.Bool(logger.OpsSystemLogSkipField, true),
			)
		}
		if hasAccountID && accountID > 0 {
			fields = append(fields, zap.Int64("account_id", accountID))
		}
		if platform != "" {
			fields = append(fields, zap.String("platform", platform))
		}
		if model != "" {
			fields = append(fields, zap.String("model", model))
		}
		// OpenAI 透传档位：只在 Forward 跑过的请求上存在（c.Set，不是 request.Context()），
		// 所以直接读 gin.Context 的键值存储，不存在时静默跳过。这是排查"strict 开了但
		// 看起来没生效"的唯一日志入口——之前完全没有落地，只暂存在 context 里没人读
		// （见 DEV_GUIDE 坑 14 与 2026-09-11 的排查）。
		if mode, ok := c.Get(service.OpsOpenAIPassthroughModeKey); ok {
			if modeStr, ok := mode.(string); ok && modeStr != "" {
				fields = append(fields, zap.String("passthrough_mode", modeStr))
			}
		}
		if reason, ok := c.Get(service.OpsOpenAIStrictDegradedReasonKey); ok {
			if reasonStr, ok := reason.(string); ok && reasonStr != "" {
				fields = append(fields, zap.String("strict_degraded_reason", reasonStr))
			}
		}

		l := logger.FromContext(c.Request.Context()).With(fields...)
		l.Info("http request completed", zap.Time("completed_at", endTime))

		if len(c.Errors) > 0 {
			l.Warn("http request contains gin errors", zap.String("errors", c.Errors.String()))
		}
	}
}

func shouldSkipAccessLog(path string) bool {
	if path == "/health" || path == "/setup/status" {
		return true
	}
	return path == "/api/v1/admin/usage"
}
