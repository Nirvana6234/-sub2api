package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 生产账号 184（mcgrox 中转）实际返回的响应体。
var poolModeInsufficientBalanceBody = []byte(`{"error":{"message":"账户余额不足，请充值后继续使用。","type":"INSUFFICIENT_BALANCE"}}`)

type poolBalanceRepoStub struct {
	AccountRepository
	tempCalls   int
	tempReason  string
	setErrCalls int
}

func (r *poolBalanceRepoStub) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, reason string) error {
	r.tempCalls++
	r.tempReason = reason
	return nil
}

func (r *poolBalanceRepoStub) SetError(context.Context, int64, string) error {
	r.setErrCalls++
	return nil
}

func newPoolModeBalanceTestAccount(id int64) *Account {
	return &Account{
		ID:       id,
		Type:     AccountTypeAPIKey,
		Platform: PlatformOpenAI,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_count":        float64(3),
			"pool_mode_retry_status_codes": []any{float64(401), float64(403), float64(429), float64(503)},
		},
	}
}

func TestHandleUpstreamError_PoolModeInsufficientBalance(t *testing.T) {
	for _, statusCode := range []int{http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests} {
		repo := &poolBalanceRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)

		shouldDisable := svc.HandleUpstreamError(context.Background(), newPoolModeBalanceTestAccount(40), statusCode, http.Header{}, poolModeInsufficientBalanceBody)

		require.True(t, shouldDisable, "status %d", statusCode)
		require.Equal(t, 1, repo.tempCalls, "status %d", statusCode)
		require.Equal(t, 0, repo.setErrCalls, "余额不足可恢复，不能永久置 error")
		require.Contains(t, repo.tempReason, "账户余额不足")
	}
}

func TestHandleUpstreamError_PoolModeOrdinary403StillSkips(t *testing.T) {
	repo := &poolBalanceRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)

	shouldDisable := svc.HandleUpstreamError(context.Background(), newPoolModeBalanceTestAccount(41), http.StatusForbidden, http.Header{},
		[]byte(`{"error":{"message":"pool member temporarily forbidden","type":"forbidden"}}`))

	require.False(t, shouldDisable)
	require.Equal(t, 0, repo.tempCalls)
}

func TestHandleUpstreamError_PoolModeBalanceTextOnNonBillingStatusSkips(t *testing.T) {
	repo := &poolBalanceRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)

	shouldDisable := svc.HandleUpstreamError(context.Background(), newPoolModeBalanceTestAccount(42), http.StatusServiceUnavailable, http.Header{}, poolModeInsufficientBalanceBody)

	require.False(t, shouldDisable)
	require.Equal(t, 0, repo.tempCalls)
}

func TestCheckErrorPolicy_PoolModeInsufficientBalance(t *testing.T) {
	repo := &poolBalanceRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)

	result := svc.CheckErrorPolicy(context.Background(), newPoolModeBalanceTestAccount(43), http.StatusForbidden, poolModeInsufficientBalanceBody)

	require.Equal(t, ErrorPolicyTempUnscheduled, result)
	require.Equal(t, 1, repo.tempCalls)
}

func TestIsPoolModeInsufficientBalanceError(t *testing.T) {
	require.True(t, isPoolModeInsufficientBalanceError(http.StatusForbidden, poolModeInsufficientBalanceBody))
	require.True(t, isPoolModeInsufficientBalanceError(http.StatusPaymentRequired, []byte(`{"error":{"message":"Insufficient balance"}}`)))
	require.False(t, isPoolModeInsufficientBalanceError(http.StatusForbidden, nil))
	require.False(t, isPoolModeInsufficientBalanceError(http.StatusBadRequest, poolModeInsufficientBalanceBody))
}

// 端到端：OpenAI 网关收到池模式账号的余额不足 403，应直接换号并临时停调，
// 而不是在同一个欠费账号上再重试 pool_mode_retry_count 次。
func TestOpenAIFailover_PoolModeInsufficientBalanceSkipsSameAccountRetry(t *testing.T) {
	repo := &poolBalanceRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{cfg: &config.Config{}, rateLimitService: rateLimitService}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := newPoolModeBalanceTestAccount(44)
	account.Status = StatusActive
	account.Schedulable = true

	failoverErr := gateway.failoverOpenAIUpstreamHTTPError(
		context.Background(),
		nil,
		account,
		&http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}},
		poolModeInsufficientBalanceBody,
		"账户余额不足，请充值后继续使用。",
		"gpt-5.6-sol",
	)

	require.NotNil(t, failoverErr)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, 1, repo.tempCalls)
	require.Equal(t, 0, repo.setErrCalls)
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}
