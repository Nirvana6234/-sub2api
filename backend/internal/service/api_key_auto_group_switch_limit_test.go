//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAutoGroupSwitchLimitBlocksSecondSwitchWithinOneHour(t *testing.T) {
	now := time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	committed, switchState := commitAutoGroupSwitchLimitTest(t, now, []time.Time{now.Add(-5 * time.Minute)}, true)

	require.Equal(t, int64(10), committed.groupID)
	require.Equal(t, now.Add(55*time.Minute), committed.priceReviewAt)
	require.Equal(t, []time.Time{now.Add(-5 * time.Minute)}, switchState.switchedAt)
}

func TestAutoGroupSwitchLimitBlocksFourthSwitchInRollingHour(t *testing.T) {
	now := time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	history := []time.Time{now.Add(-55 * time.Minute), now.Add(-35 * time.Minute), now.Add(-20 * time.Minute)}
	committed, switchState := commitAutoGroupSwitchLimitTest(t, now, history, true)

	require.Equal(t, int64(10), committed.groupID)
	require.Equal(t, now.Add(40*time.Minute), committed.priceReviewAt)
	require.Equal(t, history, switchState.switchedAt)
}

func TestAutoGroupSwitchLimitUsesOneBudgetPerAPIKeyAcrossModels(t *testing.T) {
	now := time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	svc := &APIKeyService{
		autoGroupUserGenerations:  make(map[int64]uint64),
		autoGroupGroupGenerations: make(map[int64]uint64),
	}
	apiKey := &APIKey{ID: 303, UserID: 7, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyBalanced, AutoGroupIDs: []int64{10, 20}}
	firstModelSwitchKey := autoGroupSwitchStateKey(apiKey)
	secondModelSwitchKey := autoGroupSwitchStateKey(apiKey)
	require.Equal(t, firstModelSwitchKey, secondModelSwitchKey)

	history := []time.Time{now.Add(-55 * time.Minute), now.Add(-35 * time.Minute), now.Add(-20 * time.Minute)}
	svc.autoGroupSwitchStates.Store(firstModelSwitchKey, autoGroupSwitchState{
		groupIDs:   map[string]int64{"gpt-model-a": 10},
		switchedAt: append([]time.Time(nil), history...),
		revision:   1,
		expiresAt:  now.Add(time.Hour),
	})
	selectionKey := autoGroupSelectionKey(apiKey, "gpt-model-b")
	currentGroup := &Group{ID: 10, RateMultiplier: 0.5, ActiveAccountCount: 1}
	current := autoGroupSelection{
		userID:            apiKey.UserID,
		candidateGroupIDs: append([]int64(nil), apiKey.AutoGroupIDs...),
		groupID:           currentGroup.ID,
		selectedGroup:     currentGroup,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
	}
	svc.storeAutoGroupSelection(selectionKey, current)
	current, generation := svc.autoGroupSelectionSnapshot(selectionKey, secondModelSwitchKey, apiKey.UserID, apiKey.AutoGroupIDs)
	nextGroup := &Group{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1}
	next := current
	next.groupID = nextGroup.ID
	next.selectedGroup = nextGroup
	next.priceReviewAt = now.Add(autoGroupPriceReviewInterval)

	committed, stored := svc.commitAutoGroupSelectionIfCurrent(
		selectionKey,
		secondModelSwitchKey,
		"gpt-model-b",
		apiKey.UserID,
		generation,
		current,
		next,
		[]Group{*currentGroup, *nextGroup},
		now,
	)

	require.True(t, stored)
	require.Equal(t, currentGroup.ID, committed.groupID)
	require.Equal(t, now.Add(40*time.Minute), committed.priceReviewAt)
}

func TestAutoGroupSwitchLimitAllowsSwitchAfterCooldown(t *testing.T) {
	now := time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	committed, switchState := commitAutoGroupSwitchLimitTest(t, now, []time.Time{now.Add(-61 * time.Minute)}, true)

	require.Equal(t, int64(20), committed.groupID)
	require.Equal(t, int64(20), switchState.groupIDs["gpt-test"])
	require.Equal(t, now, switchState.switchedAt[len(switchState.switchedAt)-1])
}

func TestAutoGroupSwitchLimitAllowsFailoverWhenCurrentGroupUnavailable(t *testing.T) {
	now := time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	// A group with no active accounts cannot serve any request, so the switch
	// must happen immediately regardless of the rate-limit budget. Nothing is
	// charged to switchedAt — the rate-limit is irrelevant when the group is
	// simply unusable.
	committed, switchState := commitAutoGroupSwitchLimitTest(t, now, []time.Time{now.Add(-5 * time.Minute)}, false)

	require.Equal(t, int64(20), committed.groupID)
	require.Equal(t, []time.Time{now.Add(-5 * time.Minute)}, switchState.switchedAt)
}

func TestAutoGroupSwitchLimitDoesNotCountHardFailureFailover(t *testing.T) {
	now := time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	history := []time.Time{now.Add(-5 * time.Minute)}
	committed, switchState := commitAutoGroupSwitchLimitScenario(t, now, history, true, true)

	require.Equal(t, int64(20), committed.groupID)
	require.Equal(t, history, switchState.switchedAt)
}

func TestAutoGroupSwitchLimitMarksConfirmedFailureAsFailover(t *testing.T) {
	now := time.Now()
	svc := &APIKeyService{
		autoGroupUserGenerations:  make(map[int64]uint64),
		autoGroupGroupGenerations: make(map[int64]uint64),
	}
	apiKey := &APIKey{ID: 304, UserID: 7, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyBalanced, AutoGroupIDs: []int64{10, 20}}
	selectionKey := autoGroupSelectionKey(apiKey, "gpt-test")
	switchKey := autoGroupSwitchStateKey(apiKey)
	svc.storeAutoGroupSelection(selectionKey, autoGroupSelection{
		userID:            apiKey.UserID,
		candidateGroupIDs: append([]int64(nil), apiKey.AutoGroupIDs...),
		groupID:           10,
		selectedGroup:     &Group{ID: 10, ActiveAccountCount: 1},
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
	})
	previousHealthySwitch := now.Add(-5 * time.Minute)
	svc.autoGroupSwitchStates.Store(switchKey, autoGroupSwitchState{
		groupIDs:   map[string]int64{"gpt-test": 10},
		switchedAt: []time.Time{previousHealthySwitch},
		revision:   1,
		expiresAt:  now.Add(time.Hour),
	})

	for range autoGroupTransientFailureThreshold {
		svc.ObserveAutoGroupRequestResult(apiKey, "gpt-test", 502, nil)
	}
	current, generation := svc.autoGroupSelectionSnapshot(selectionKey, switchKey, apiKey.UserID, apiKey.AutoGroupIDs)
	require.True(t, current.failureTriggered)
	next := current
	next.groupID = 20
	next.selectedGroup = &Group{ID: 20, ActiveAccountCount: 1}
	next.settled = true

	committed, stored := svc.commitAutoGroupSelectionIfCurrent(selectionKey, switchKey, "gpt-test", apiKey.UserID, generation, current, next, []Group{
		{ID: 10, ActiveAccountCount: 1},
		{ID: 20, ActiveAccountCount: 1},
	}, now)

	require.True(t, stored)
	require.Equal(t, int64(20), committed.groupID)
	value, ok := svc.autoGroupSwitchStates.Load(switchKey)
	require.True(t, ok)
	require.Equal(t, []time.Time{previousHealthySwitch}, value.(autoGroupSwitchState).switchedAt)
}

func TestAutoGroupSwitchLimitDoesNotCountInitialSelection(t *testing.T) {
	now := time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	svc := &APIKeyService{
		autoGroupUserGenerations:  make(map[int64]uint64),
		autoGroupGroupGenerations: make(map[int64]uint64),
	}
	apiKey := &APIKey{ID: 302, UserID: 7, AutoGroup: true, AutoGroupIDs: []int64{10, 20}}
	selectionKey := autoGroupSelectionKey(apiKey, "gpt-test")
	switchKey := autoGroupSwitchStateKey(apiKey)
	current, generation := svc.autoGroupSelectionSnapshot(selectionKey, switchKey, apiKey.UserID, apiKey.AutoGroupIDs)
	group := &Group{ID: 10, ActiveAccountCount: 1}
	next := autoGroupSelection{groupID: group.ID, selectedGroup: group, settled: true}

	committed, stored := svc.commitAutoGroupSelectionIfCurrent(selectionKey, switchKey, "gpt-test", apiKey.UserID, generation, current, next, []Group{*group}, now)

	require.True(t, stored)
	require.Equal(t, int64(10), committed.groupID)
	value, ok := svc.autoGroupSwitchStates.Load(switchKey)
	require.True(t, ok)
	require.Empty(t, value.(autoGroupSwitchState).switchedAt)
}

func commitAutoGroupSwitchLimitTest(t *testing.T, now time.Time, history []time.Time, currentAvailable bool) (autoGroupSelection, autoGroupSwitchState) {
	t.Helper()
	return commitAutoGroupSwitchLimitScenario(t, now, history, currentAvailable, false)
}

func commitAutoGroupSwitchLimitScenario(t *testing.T, now time.Time, history []time.Time, currentAvailable, failureTriggered bool) (autoGroupSelection, autoGroupSwitchState) {
	t.Helper()
	svc := &APIKeyService{
		autoGroupUserGenerations:  make(map[int64]uint64),
		autoGroupGroupGenerations: make(map[int64]uint64),
	}
	apiKey := &APIKey{ID: 301, UserID: 7, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyBalanced, AutoGroupIDs: []int64{10, 20}}
	selectionKey := autoGroupSelectionKey(apiKey, "gpt-test")
	switchKey := autoGroupSwitchStateKey(apiKey)
	currentGroup := &Group{ID: 10, RateMultiplier: 0.5, ActiveAccountCount: 1}
	current := autoGroupSelection{
		userID:            apiKey.UserID,
		candidateGroupIDs: append([]int64(nil), apiKey.AutoGroupIDs...),
		groupID:           currentGroup.ID,
		selectedGroup:     currentGroup,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
		failureTriggered:  failureTriggered,
	}
	svc.storeAutoGroupSelection(selectionKey, current)
	svc.autoGroupSwitchStates.Store(switchKey, autoGroupSwitchState{groupIDs: map[string]int64{"gpt-test": 10}, switchedAt: append([]time.Time(nil), history...), revision: 1, expiresAt: now.Add(time.Hour)})
	current, generation := svc.autoGroupSelectionSnapshot(selectionKey, switchKey, apiKey.UserID, apiKey.AutoGroupIDs)
	nextGroup := &Group{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1}
	next := current
	next.groupID = nextGroup.ID
	next.selectedGroup = nextGroup
	next.priceReviewAt = now.Add(autoGroupPriceReviewInterval)
	next.settled = true
	groups := []Group{*nextGroup}
	if currentAvailable {
		groups = append(groups, *currentGroup)
	}

	committed, stored := svc.commitAutoGroupSelectionIfCurrent(selectionKey, switchKey, "gpt-test", apiKey.UserID, generation, current, next, groups, now)
	require.True(t, stored)
	value, ok := svc.autoGroupSwitchStates.Load(switchKey)
	require.True(t, ok)
	switchState, ok := value.(autoGroupSwitchState)
	require.True(t, ok)
	return committed, switchState
}
