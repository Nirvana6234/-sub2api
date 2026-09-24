package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type latencyFallbackSelectionScheduler struct {
	account *Account
	group   int64
}

func (s *latencyFallbackSelectionScheduler) Select(_ context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	s.group = derefGroupID(req.GroupID)
	return &AccountSelectionResult{Account: s.account, Acquired: true}, OpenAIAccountScheduleDecision{SelectedAccountID: s.account.ID}, nil
}

func (*latencyFallbackSelectionScheduler) ReportResult(int64, bool, *int) {}
func (*latencyFallbackSelectionScheduler) ReportSwitch()                  {}
func (*latencyFallbackSelectionScheduler) SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	return OpenAIAccountSchedulerMetricsSnapshot{}
}

// Regression: a latency fallback state must be consulted before the normal
// scheduler. Removing this call silently disables the configured latency-aware
// safety net and lets repeated upstream jitter trip the API-key breaker.
func TestSelectAccountWithSchedulerUsesLatencyFallbackStickyState(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		latencyAwareFallbackEnabled: true,
		latencyThresholdMs:          30000,
		fallbackSpeedupRatio:        0.6,
		expiresAt:                   time.Now().Add(time.Hour).UnixNano(),
	})
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	account := &Account{ID: 29, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	scheduler := &latencyFallbackSelectionScheduler{account: account}
	svc := &OpenAIGatewayService{openaiScheduler: scheduler}
	svc.markOpenAIFallbackSticky(2, 29, openAILatencyBucketNormal)

	groupID := int64(2)
	selection, _, err := svc.SelectAccountWithScheduler(
		context.Background(), &groupID, "", "", "gpt-5.5", nil, OpenAIUpstreamTransportAny, false,
	)

	require.NoError(t, err)
	require.Equal(t, int64(29), scheduler.group)
	require.Same(t, account, selection.Account)
}
