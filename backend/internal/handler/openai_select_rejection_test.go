//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 选号阶段的错误：状态码、文案和运维标记与原来处理函数里内联的写法一致（本地与主从分流共用）。
func TestOpenAISelectionRejections(t *testing.T) {
	ctx := context.Background()
	apiKey := &service.APIKey{GroupID: ptrInt64(7)}
	noModel := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	busy := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}}
	rateLimited := fmt.Errorf("%w: rate_limited=3", service.ErrNoAvailableAccounts)

	r := OpenAINoAccountRejection(ctx, busy, apiKey, "gpt-5", service.PlatformOpenAI, nil)
	require.Equal(t, http.StatusServiceUnavailable, r.Status)
	require.True(t, r.RoutingCapacityLimited)
	require.Empty(t, r.OpsBusinessLimitedReason)

	r = OpenAINoAccountRejection(ctx, noModel, apiKey, "gpt-x", service.PlatformOpenAI, nil)
	require.Equal(t, http.StatusNotFound, r.Status)
	require.Equal(t, "model_not_found", r.ErrType)
	require.False(t, r.RoutingCapacityLimited, "a missing model is a configuration problem, not capacity")
	require.Equal(t, service.OpsClientBusinessLimitedReasonLocalModelConfiguration, r.OpsBusinessLimitedReason)

	r = OpenAIFirstSelectFailureRejection(ctx, busy, apiKey, "gpt-5", service.PlatformOpenAI, false, rateLimited)
	require.Equal(t, http.StatusTooManyRequests, r.Status)
	require.Equal(t, "rate_limit_error", r.ErrType)
	require.True(t, r.RoutingCapacityLimited)

	r = OpenAIFirstSelectFailureRejection(ctx, noModel, apiKey, "gpt-x", service.PlatformOpenAI, false, rateLimited)
	require.Equal(t, http.StatusNotFound, r.Status, "model_not_found is not downgraded to a rate limit")
	require.False(t, r.RoutingCapacityLimited)

	r = OpenAIFirstSelectFailureRejection(ctx, busy, apiKey, "gpt-5", service.PlatformOpenAI, false, errors.New("scheduler exploded"))
	require.Equal(t, http.StatusServiceUnavailable, r.Status)
	require.False(t, r.RoutingCapacityLimited, "only no-available-account errors count as routing capacity")

	r = OpenAIFirstSelectFailureRejection(ctx, busy, apiKey, "gpt-5", service.PlatformOpenAI, true, service.ErrNoAvailableCompactAccounts)
	require.Equal(t, "compact_not_supported", r.ErrType)
	require.True(t, r.RoutingCapacityLimited)

	r = OpenAISelectOutcomeRejection(OpenAISelectOutcome{Kind: OpenAISelectVetoExhausted})
	require.Equal(t, profitVetoExhaustedMessage, r.Message)
	require.True(t, r.RoutingCapacityLimited)

	r = OpenAIBillingRejection(service.ErrUserRPMExceeded)
	require.Equal(t, http.StatusTooManyRequests, r.Status)
	require.Equal(t, "rate_limit_exceeded", r.ErrType)
	require.Positive(t, r.RetryAfter)

	// 写出：Retry-After、错误码、运维标记。
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	(&OpenAIGatewayHandler{}).writeOpenAIGatewayRejection(c, OpenAIGatewayRejection{Status: 429, ErrType: "rate_limit_exceeded", Message: "slow down", RetryAfter: 7, RoutingCapacityLimited: true, OpsBusinessLimitedReason: "x"}, false)
	require.Equal(t, 429, w.Code)
	require.Equal(t, "7", w.Header().Get("Retry-After"))
	require.JSONEq(t, `{"error":{"type":"rate_limit_exceeded","message":"slow down"}}`, w.Body.String())
	require.True(t, isOpsRoutingCapacityLimited(c))
	require.Equal(t, "x", service.OpsClientBusinessLimitedReason(c))

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	(&OpenAIGatewayHandler{}).writeOpenAIGatewayRejection(c, OpenAICyberSessionBlockedRejection(), false)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.JSONEq(t, `{"error":{"type":"permission_error","code":"session_blocked_by_cyber_policy","message":"`+cyberSessionBlockedClientMsg+`"}}`, w.Body.String(),
		"same body as rejectIfCyberSessionBlocked writes")
}
