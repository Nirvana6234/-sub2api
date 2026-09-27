//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 入账跑在 detached worker 上，worker 的 ctx 只有 PropagateFallbackPoolUsageContext 搬过来的通用 key。
// RecordUsage 必须从这个 key 读兜底事实，否则用量行的 fallback_* 恒为空、兜底告警失效。
// 主从分流的主节点入账时也是把凭证里的兜底事实装进这个 key（开发计划 2.2、WP8）。
func TestRecordUsageSeesTheFallbackTraceInTheWorkerContext(t *testing.T) {
	schedulerCtx := withOpenAIFallbackGroupState(context.Background(), fallbackGroupState{
		originGroupID: 20, originGroupName: "plus-free", targetGroupID: 29, targetGroupName: "plus-fallback", hops: 1,
	})
	sel := attachSelectionProfitGate(schedulerCtx, &AccountSelectionResult{Account: &Account{ID: 221}})
	requestCtx := ContextWithSelectionFallbackTrace(context.Background(), sel)
	workerCtx := PropagateFallbackPoolUsageContext(requestCtx, context.Background())

	requireFallback := func(t *testing.T, log *UsageLog) {
		t.Helper()
		require.NotNil(t, log)
		require.True(t, log.FallbackPoolUsed)
		require.Equal(t, int64(20), *log.FallbackSourceGroupID)
		require.Equal(t, int64(29), *log.FallbackTargetGroupID)
	}

	t.Run("openai", func(t *testing.T) {
		usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
		svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
		require.NoError(t, svc.RecordUsage(workerCtx, &OpenAIRecordUsageInput{
			Result:  &OpenAIForwardResult{RequestID: "resp_fallback", Usage: OpenAIUsage{InputTokens: 10, OutputTokens: 5}, Model: "gpt-5.1", Duration: time.Second},
			APIKey:  &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
			User:    &User{ID: 2000},
			Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
		}))
		requireFallback(t, usageRepo.lastLog)
	})

	t.Run("gateway", func(t *testing.T) {
		usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
		svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
		require.NoError(t, svc.RecordUsage(workerCtx, &RecordUsageInput{
			Result:  &ForwardResult{RequestID: "gw_fallback", Usage: ClaudeUsage{InputTokens: 10, OutputTokens: 6}, Model: "claude-sonnet-4", Duration: time.Second},
			APIKey:  &APIKey{ID: 501},
			User:    &User{ID: 601},
			Account: &Account{ID: 701},
		}))
		requireFallback(t, usageRepo.lastLog)
	})
}
