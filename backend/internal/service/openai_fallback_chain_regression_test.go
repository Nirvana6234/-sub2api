// 这些回归用例来自 2026-09-24 对自动分组/兜底池改动的外部评审复现。

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type regressionOpenAIScheduler struct {
	accounts map[int64]*Account
	errs     map[int64]error
	calls    []int64
	releases map[int64]int
}

func (s *regressionOpenAIScheduler) Select(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	id := derefGroupID(req.GroupID)
	s.calls = append(s.calls, id)
	if err := s.errs[id]; err != nil {
		return nil, OpenAIAccountScheduleDecision{}, err
	}
	account := s.accounts[id]
	if account == nil {
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	}
	return attachSelectionProfitGate(ctx, &AccountSelectionResult{
		Account: account, Acquired: true,
		ReleaseFunc: func() { s.releases[id]++ },
	}), OpenAIAccountScheduleDecision{}, nil
}
func (*regressionOpenAIScheduler) ReportResult(int64, bool, *int) {}
func (*regressionOpenAIScheduler) ReportSwitch()                  {}
func (*regressionOpenAIScheduler) SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	return OpenAIAccountSchedulerMetricsSnapshot{}
}

func regressionLatencyService(t *testing.T) (*OpenAIGatewayService, *regressionOpenAIScheduler) {
	t.Helper()
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		enabled: true, latencyAwareFallbackEnabled: true, latencyThresholdMs: 30000,
		fallbackSpeedupRatio: 0.6, expiresAt: time.Now().Add(time.Hour).UnixNano(),
	})
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	s := &regressionOpenAIScheduler{accounts: map[int64]*Account{}, errs: map[int64]error{}, releases: map[int64]int{}}
	for _, id := range []int64{10, 30, 40} {
		s.accounts[id] = &Account{ID: id*10 + 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	}
	svc := fallbackTestService(
		&Group{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, FallbackGroupIDs: []int64{20, 30}},
		&Group{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true, FallbackGroupIDs: []int64{40}},
		&Group{ID: 30, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
		&Group{ID: 40, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
	)
	svc.openaiScheduler = s
	for i := 0; i < openAILatencyMinSamples; i++ {
		svc.getOpenAILatencyTracker().ObserveGroup(10, openAILatencyBucketNormal, 93000)
	}
	return svc, s
}

func TestLatencyFallbackPinsFinalPool(t *testing.T) {
	svc, sched := regressionLatencyService(t)
	id := int64(10)
	selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &id, "", "", "", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, []int64{10, 20, 40}, sched.calls)
	require.Equal(t, int64(40), selection.fallbackTrace.TargetGroupID)
	value, ok := svc.openaiFallbackStickyStates.Load(id)
	require.True(t, ok)
	state := value.(*openAIFallbackStickyState)
	t.Logf("successful pool=%d, sticky pool=%d", selection.fallbackTrace.TargetGroupID, state.targetGroupID)
	selection.ReleaseFunc()
	require.Equal(t, int64(40), state.targetGroupID, "sticky target must be the pool that actually supplied the account")
}

func TestLatencyFallbackNextRequestKeepsFinalPool(t *testing.T) {
	svc, sched := regressionLatencyService(t)
	id := int64(10)
	first, _, err := svc.SelectAccountWithScheduler(context.Background(), &id, "", "", "", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	first.ReleaseFunc()
	sched.calls = nil
	second, _, err := svc.SelectAccountWithScheduler(context.Background(), &id, "", "", "", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	second.ReleaseFunc()
	t.Logf("next request calls=%v selected=%d", sched.calls, second.Account.ID)
	require.Equal(t, first.Account.ID, second.Account.ID, "next request must retain the successful downstream pool")
}

func TestLatencyFallbackStopsOnNonAvailabilityError(t *testing.T) {
	svc, sched := regressionLatencyService(t)
	sched.errs[20] = errors.New("database temporarily unavailable")
	id := int64(10)
	selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &id, "", "", "", nil, OpenAIUpstreamTransportAny, false)
	if selection != nil && selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	t.Logf("calls=%v err=%v", sched.calls, err)
	require.Equal(t, []int64{10, 20}, sched.calls, "only no-account errors may advance to the next sibling")
}

func TestLatencyFallbackRejectsSlowerDownstream(t *testing.T) {
	svc, sched := regressionLatencyService(t)
	for i := 0; i < openAILatencyMinSamples; i++ {
		svc.getOpenAILatencyTracker().ObserveGroup(40, openAILatencyBucketNormal, 200000)
	}
	id := int64(10)
	selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &id, "", "", "", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	selection.ReleaseFunc()
	t.Logf("calls=%v final pool=%d", sched.calls, selection.fallbackTrace.TargetGroupID)
	require.Equal(t, int64(30), selection.fallbackTrace.TargetGroupID, "a 200s downstream pool must not replace the 93s source during latency fallback")
}

type regressionCountingEmptyRepo struct {
	AccountRepository
	calls []int64
}

func (r *regressionCountingEmptyRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, id int64, _ string) ([]Account, error) {
	r.calls = append(r.calls, id)
	return nil, nil
}

func TestLegacyFallbackVisitsEachPoolOnce(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{expiresAt: time.Now().Add(time.Hour).UnixNano()})
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	repo := &regressionCountingEmptyRepo{}
	groups := &fallbackGroupRepoStub{groups: map[int64]*Group{
		10: {ID: 10, Platform: PlatformOpenAI, Status: StatusActive, FallbackGroupIDs: []int64{20, 30}},
		20: {ID: 20, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
		30: {ID: 30, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
	}}
	svc := &OpenAIGatewayService{
		accountRepo:       repo,
		cfg:               &config.Config{RunMode: config.RunModeStandard},
		schedulerSnapshot: NewSchedulerSnapshotService(nil, nil, repo, groups, nil),
	}
	id := int64(10)
	_, _, err := svc.SelectAccountWithScheduler(context.Background(), &id, "", "", "", nil, OpenAIUpstreamTransportAny, false)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	t.Logf("actual pool queries=%v", repo.calls)
	require.Equal(t, []int64{10, 20, 30}, repo.calls, "the three wrappers must share a traversal instead of each restarting fallback")
}

type regressionHydrationFailureRepo struct{ AccountRepository }

func (*regressionHydrationFailureRepo) GetByID(context.Context, int64) (*Account, error) {
	return nil, errors.New("account hydration unavailable")
}

func TestGatewayReleasesSlotWhenHydrationFails(t *testing.T) {
	repo := &regressionHydrationFailureRepo{}
	svc := &GatewayService{schedulerSnapshot: NewSchedulerSnapshotService(nil, nil, repo, nil, nil)}
	releases := 0
	selection, err := svc.newSelectionResult(context.Background(), &Account{ID: 123}, true, func() { releases++ }, nil)
	require.Error(t, err)
	require.Nil(t, selection)
	require.Equal(t, 1, releases, "a slot acquired before hydration must be released when no selection can be returned")
}
