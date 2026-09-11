package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// 2026-09-11：ops_openai_passthrough_mode / ops_openai_strict_degraded_reason 原来只
// c.Set 进 gin.Context，从没有任何生产代码读出来落地——DEV_GUIDE 坑 14 教人查这两个
// 字段，但排查一次真实账号时发现日志里一条都没有。这条测试锁死它们确实出现在访问日志里，
// 不是"编译过就当测过"。
func newLoggerTestContext(t *testing.T) (*gin.Context, *observer.ObservedLogs) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zapcore.InfoLevel)
	zl := zap.New(core)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req = req.WithContext(logger.IntoContext(req.Context(), zl))
	c.Request = req
	return c, logs
}

func findAccessLogEntry(logs *observer.ObservedLogs) (observer.LoggedEntry, bool) {
	for _, e := range logs.All() {
		if e.Message == "http request completed" {
			return e, true
		}
	}
	return observer.LoggedEntry{}, false
}

func TestLogger_SurfacesOpenAIPassthroughMode(t *testing.T) {
	c, logs := newLoggerTestContext(t)
	c.Set(service.OpsOpenAIPassthroughModeKey, service.OpenAIPassthroughModeStrict)
	c.Set(service.OpsOpenAIStrictDegradedReasonKey, "")

	Logger()(c)

	entry, ok := findAccessLogEntry(logs)
	require.True(t, ok, "访问日志必须落地一条 http request completed")
	ctx := entry.ContextMap()
	assert.Equal(t, "strict", ctx["passthrough_mode"])
	_, hasReason := ctx["strict_degraded_reason"]
	assert.False(t, hasReason, "空降级原因不该占一个字段位置")
}

func TestLogger_SurfacesStrictDegradedReason(t *testing.T) {
	c, logs := newLoggerTestContext(t)
	c.Set(service.OpsOpenAIPassthroughModeKey, service.OpenAIPassthroughModeAuthOnly)
	c.Set(service.OpsOpenAIStrictDegradedReasonKey, service.OpenAIStrictPassthroughReasonGateNotMatched)

	Logger()(c)

	entry, ok := findAccessLogEntry(logs)
	require.True(t, ok)
	ctx := entry.ContextMap()
	assert.Equal(t, "auth_only", ctx["passthrough_mode"])
	assert.Equal(t, "client_gate_not_matched", ctx["strict_degraded_reason"])
}

// 非 OpenAI / 没跑过 Forward 的请求：两个键从未被 Set 过，日志不该凭空出现这两个字段。
func TestLogger_OmitsPassthroughFieldsWhenNeverStaged(t *testing.T) {
	c, logs := newLoggerTestContext(t)

	Logger()(c)

	entry, ok := findAccessLogEntry(logs)
	require.True(t, ok)
	ctx := entry.ContextMap()
	_, hasMode := ctx["passthrough_mode"]
	_, hasReason := ctx["strict_degraded_reason"]
	assert.False(t, hasMode)
	assert.False(t, hasReason)
}
