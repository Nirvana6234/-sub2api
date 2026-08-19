//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type autoGroupRateQueryGate struct {
	userGroupRateRepoStubForGroupRate

	mu           sync.Mutex
	calls        int
	firstRates   map[int64]float64
	retryRates   map[int64]float64
	firstStarted chan struct{}
	firstRelease chan struct{}
	retryStarted chan struct{}
	retryRelease chan struct{}
}

func newAutoGroupRateQueryGate(firstRates, retryRates map[int64]float64) *autoGroupRateQueryGate {
	return &autoGroupRateQueryGate{
		firstRates:   firstRates,
		retryRates:   retryRates,
		firstStarted: make(chan struct{}),
		firstRelease: make(chan struct{}),
		retryStarted: make(chan struct{}),
		retryRelease: make(chan struct{}),
	}
}

func (r *autoGroupRateQueryGate) GetByUserID(ctx context.Context, _ int64) (map[int64]float64, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()

	switch call {
	case 1:
		close(r.firstStarted)
		select {
		case <-r.firstRelease:
			return cloneAutoGroupRatesForRaceTest(r.firstRates), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case 2:
		close(r.retryStarted)
		select {
		case <-r.retryRelease:
			return cloneAutoGroupRatesForRaceTest(r.retryRates), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	default:
		return cloneAutoGroupRatesForRaceTest(r.retryRates), nil
	}
}

func (r *autoGroupRateQueryGate) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func cloneAutoGroupRatesForRaceTest(rates map[int64]float64) map[int64]float64 {
	cloned := make(map[int64]float64, len(rates))
	for groupID, rate := range rates {
		cloned[groupID] = rate
	}
	return cloned
}

func newAutoGroupRaceService(rateRepo UserGroupRateRepository, metrics AutoGroupMetricRepository) *APIKeyService {
	svc := NewAPIKeyService(
		nil,
		&userRepoStub{user: &User{ID: 7, Status: StatusActive}},
		&playgroundGroupRepoStub{groups: []Group{
			{ID: 10, Name: "first", Status: StatusActive, Platform: PlatformOpenAI, RateMultiplier: 0.5, ActiveAccountCount: 1},
			{ID: 20, Name: "second", Status: StatusActive, Platform: PlatformOpenAI, RateMultiplier: 0.5, ActiveAccountCount: 1},
		}},
		playgroundSubscriptionRepoStub{},
		rateRepo,
		nil,
		nil,
	)
	svc.SetAutoGroupMetricRepository(metrics)
	return svc
}

func waitForAutoGroupRaceSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func TestResolveAutoGroupExpiredPendingProbeReloadsPersonalMetricsAndSettlesOnMeasuredGroup(t *testing.T) {
	metrics := &autoGroupMetricRepoForReview{
		personal: map[int64][]int64{
			10: {31_000, 31_100, 31_200},
			20: {2_000, 2_100, 2_200},
		},
	}
	svc := newAutoGroupRaceService(&userGroupRateRepoStubForGroupRate{}, metrics)
	apiKey := &APIKey{
		ID:                101,
		UserID:            7,
		AutoGroup:         true,
		AutoGroupStrategy: autoGroupStrategyBalanced,
		AutoGroupIDs:      []int64{10, 20},
	}
	selectionKey := autoGroupSelectionKey(apiKey, "gpt-test")
	firstGroup := &Group{ID: 10, Name: "first", Status: StatusActive, Platform: PlatformOpenAI, RateMultiplier: 0.5, ActiveAccountCount: 1}
	svc.storeAutoGroupSelection(selectionKey, autoGroupSelection{
		userID:            apiKey.UserID,
		candidateGroupIDs: append([]int64(nil), apiKey.AutoGroupIDs...),
		groupID:           firstGroup.ID,
		pendingGroupID:    firstGroup.ID,
		pendingSince:      time.Now().Add(-autoGroupProbeWaitTimeout - time.Second),
		selectedGroup:     firstGroup,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
	})

	resolved, err := svc.ResolveAutoGroupForModel(context.Background(), apiKey, "gpt-test")

	require.NoError(t, err)
	require.NotNil(t, resolved.GroupID)
	require.Equal(t, int64(20), *resolved.GroupID)
	require.Equal(t, 1, metrics.personalCalls)
	selection := svc.autoGroupSelection(selectionKey)
	require.Zero(t, selection.pendingGroupID)
	require.True(t, selection.settled)
}

func TestSelectStableAutoGroupTreatsMissingPendingTimestampAsExpired(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.1, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
	}
	metrics := map[int64][]int64{10: {31_000, 31_100, 31_200}}

	selected, state, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategyBalanced, autoGroupSelection{
		groupID:        20,
		probedGroupIDs: []int64{20},
		pendingGroupID: 20,
	})

	require.NoError(t, err)
	require.Equal(t, int64(10), selected.ID)
	require.Zero(t, state.pendingGroupID)
	require.True(t, state.settled)
}

func TestResolveAutoGroupRetriesWhenUserRateInvalidationOccursDuringQuery(t *testing.T) {
	testResolveAutoGroupRetriesAfterRateInvalidation(t, func(svc *APIKeyService) {
		svc.InvalidateAutoGroupSelectionsByUserID(context.Background(), 7)
	})
}

func TestResolveAutoGroupRetriesWhenGroupRateInvalidationOccursDuringQuery(t *testing.T) {
	testResolveAutoGroupRetriesAfterRateInvalidation(t, func(svc *APIKeyService) {
		svc.InvalidateAutoGroupSelectionsByGroupID(context.Background(), 10)
	})
}

func testResolveAutoGroupRetriesAfterRateInvalidation(t *testing.T, invalidate func(*APIKeyService)) {
	t.Helper()
	rates := newAutoGroupRateQueryGate(
		map[int64]float64{10: 0.1, 20: 0.9},
		map[int64]float64{10: 0.9, 20: 0.1},
	)
	svc := newAutoGroupRaceService(rates, nil)
	apiKey := &APIKey{
		ID:                102,
		UserID:            7,
		AutoGroup:         true,
		AutoGroupStrategy: autoGroupStrategyPrice,
		AutoGroupIDs:      []int64{10, 20},
	}

	type resolveResult struct {
		apiKey *APIKey
		err    error
	}
	resultCh := make(chan resolveResult, 1)
	go func() {
		resolved, err := svc.ResolveAutoGroupForModel(context.Background(), apiKey, "gpt-test")
		resultCh <- resolveResult{apiKey: resolved, err: err}
	}()

	waitForAutoGroupRaceSignal(t, rates.firstStarted, "first rate query")
	invalidate(svc)
	close(rates.firstRelease)
	waitForAutoGroupRaceSignal(t, rates.retryStarted, "retry rate query")
	close(rates.retryRelease)

	select {
	case result := <-resultCh:
		require.NoError(t, result.err)
		require.NotNil(t, result.apiKey.GroupID)
		require.Equal(t, int64(20), *result.apiKey.GroupID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for auto group resolution")
	}
	require.GreaterOrEqual(t, rates.callCount(), 2)
}

func TestResolveAutoGroupDoesNotOverwriteFailureEventObservedDuringQuery(t *testing.T) {
	rates := newAutoGroupRateQueryGate(
		map[int64]float64{10: 0.1, 20: 0.9},
		map[int64]float64{10: 0.1, 20: 0.9},
	)
	svc := newAutoGroupRaceService(rates, nil)
	apiKey := &APIKey{
		ID:                103,
		UserID:            7,
		AutoGroup:         true,
		AutoGroupStrategy: autoGroupStrategyPrice,
		AutoGroupIDs:      []int64{10, 20},
	}
	selectionKey := autoGroupSelectionKey(apiKey, "gpt-test")
	firstGroup := &Group{ID: 10, Name: "first", Status: StatusActive, Platform: PlatformOpenAI, RateMultiplier: 0.5, ActiveAccountCount: 1}
	svc.storeAutoGroupSelection(selectionKey, autoGroupSelection{
		userID:            apiKey.UserID,
		candidateGroupIDs: append([]int64(nil), apiKey.AutoGroupIDs...),
		groupID:           firstGroup.ID,
		pendingGroupID:    firstGroup.ID,
		pendingSince:      time.Now().Add(-autoGroupProbeWaitTimeout - time.Second),
		selectedGroup:     firstGroup,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
	})

	type resolveResult struct {
		apiKey *APIKey
		err    error
	}
	resultCh := make(chan resolveResult, 1)
	go func() {
		resolved, err := svc.ResolveAutoGroupForModel(context.Background(), apiKey, "gpt-test")
		resultCh <- resolveResult{apiKey: resolved, err: err}
	}()

	waitForAutoGroupRaceSignal(t, rates.firstStarted, "first rate query")
	svc.ObserveAutoGroupRequestResult(apiKey, "gpt-test", 503, nil)
	close(rates.firstRelease)
	waitForAutoGroupRaceSignal(t, rates.retryStarted, "retry after observed failure")

	selection := svc.autoGroupSelection(selectionKey)
	require.True(t, selection.needsEvaluation)
	require.Equal(t, int64(10), selection.eventGroupID)
	close(rates.retryRelease)

	select {
	case result := <-resultCh:
		require.NoError(t, result.err)
		require.NotNil(t, result.apiKey.GroupID)
		require.Equal(t, int64(20), *result.apiKey.GroupID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for auto group resolution")
	}
	selection = svc.autoGroupSelection(selectionKey)
	require.False(t, selection.needsEvaluation)
	require.Zero(t, selection.eventGroupID)
	require.Equal(t, int64(20), selection.groupID)
}
