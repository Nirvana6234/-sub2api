//go:build unit

package service

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type countingDecider struct {
	OpenAIUpstreamErrorDecider
	handled, retries, timeouts, policies atomic.Int64
}

func (d *countingDecider) HandleUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte, canonicalModel ...string) bool {
	d.handled.Add(1)
	return d.OpenAIUpstreamErrorDecider.HandleUpstreamError(ctx, account, statusCode, headers, body, canonicalModel...)
}

func (d *countingDecider) OAuth429SameAccountRetry(ctx context.Context, account *Account) (bool, time.Time) {
	d.retries.Add(1)
	return d.OpenAIUpstreamErrorDecider.OAuth429SameAccountRetry(ctx, account)
}

func (d *countingDecider) HandleStreamTimeout(ctx context.Context, account *Account, model string) bool {
	d.timeouts.Add(1)
	return d.OpenAIUpstreamErrorDecider.HandleStreamTimeout(ctx, account, model)
}

func (d *countingDecider) CheckErrorPolicy(ctx context.Context, account *Account, statusCode int, body []byte, model string) ErrorPolicyResult {
	d.policies.Add(1)
	return d.OpenAIUpstreamErrorDecider.CheckErrorPolicy(ctx, account, statusCode, body, model)
}

func newDeciderTestGateway() (*OpenAIGatewayService, *oauth429RateLimitRepo) {
	repo := &oauth429RateLimitRepo{}
	rateLimits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc := &OpenAIGatewayService{rateLimitService: rateLimits}
	rateLimits.SetAccountRuntimeBlocker(svc)
	return svc, repo
}

// 主从分流：从节点的转发代码不变，只把判定换成主节点执行的同一段代码。同样的错误序列，
// "从节点 + 主节点"与单机得到同样的结果、同样的账号状态；状态记在主节点上，不在从节点上。
func TestRemoteUpstreamErrorDecisionsMatchLocal(t *testing.T) {
	ctx := context.Background()
	local, localRepo := newDeciderTestGateway()
	master, masterRepo := newDeciderTestGateway()
	node := &OpenAIGatewayService{}
	counter := &countingDecider{OpenAIUpstreamErrorDecider: master.LocalUpstreamErrorDecider()}
	node.SetUpstreamErrorDecider(counter)
	require.True(t, node.hasErrorDecider(), "the node has a decider although it has no rate limit service")

	oauth := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKey := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	for _, gw := range []*OpenAIGatewayService{local, node} {
		require.False(t, gw.handleOpenAIAccountUpstreamError(ctx, oauth, http.StatusTooManyRequests, http.Header{}, nil))
		require.False(t, gw.handleOpenAIAccountUpstreamError(ctx, apiKey, http.StatusTooManyRequests, http.Header{}, nil))
	}
	require.Equal(t, localRepo.setRateLimitedCalls, masterRepo.setRateLimitedCalls)
	require.Equal(t, 1, masterRepo.setRateLimitedCalls, "the API-key 429 is persisted by the master")
	require.True(t, master.isOpenAIAccountRuntimeBlocked(apiKey), "the runtime block lives where the scheduler runs")
	require.False(t, node.isOpenAIAccountRuntimeBlocked(apiKey))

	localErr := local.newOpenAIAccountFailoverError(oauth, http.StatusTooManyRequests, http.Header{}, nil, "", false, false)
	nodeErr := node.newOpenAIAccountFailoverError(oauth, http.StatusTooManyRequests, http.Header{}, nil, "", false, false)
	require.True(t, localErr.RetryableOnSameAccount)
	require.Equal(t, localErr.RetryableOnSameAccount, nodeErr.RetryableOnSameAccount)
	require.WithinDuration(t, localErr.SameAccountRetryDeadline, nodeErr.SameAccountRetryDeadline, time.Second)
	require.Equal(t, int64(1), counter.retries.Load())

	// 与状态无关的情况在从节点本地判断，不问主节点。
	nodeErr = node.newOpenAIAccountFailoverError(apiKey, http.StatusTooManyRequests, http.Header{}, nil, "", false, false)
	require.False(t, nodeErr.RetryableOnSameAccount)
	nodeErr = node.newOpenAIAccountFailoverError(oauth, http.StatusBadGateway, http.Header{}, nil, "", false, false)
	require.False(t, nodeErr.RetryableOnSameAccount)
	require.Equal(t, int64(1), counter.retries.Load())

	node.errorDecider().HandleStreamTimeout(ctx, oauth, "gpt-5")
	require.Equal(t, int64(1), counter.timeouts.Load())
	require.Equal(t, int64(2), counter.handled.Load())
}
