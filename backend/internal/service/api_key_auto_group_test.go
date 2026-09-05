//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type autoGroupModelAvailabilityStub struct {
	accounts map[int64][]Account
}

func (s *autoGroupModelAvailabilityStub) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, _ []string, _ bool) ([]Account, error) {
	if s == nil || groupID == nil {
		return nil, nil
	}
	return append([]Account(nil), s.accounts[*groupID]...), nil
}

func TestLowestAvailableRateGroupUsesUserOverrideBeforeGroupRate(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.8, SortOrder: 1, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 1.2, SortOrder: 2, ActiveAccountCount: 1},
	}

	selected := lowestAvailableRateGroup(groups, map[int64]float64{20: 0.4})

	require.NotNil(t, selected)
	require.Equal(t, int64(20), selected.ID)
}

func TestLowestAvailableRateGroupUsesSortOrderAndIDAsTieBreakers(t *testing.T) {
	groups := []Group{
		{ID: 30, RateMultiplier: 1, SortOrder: 2, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 1, SortOrder: 1, ActiveAccountCount: 1},
		{ID: 10, RateMultiplier: 1, SortOrder: 1, ActiveAccountCount: 1},
	}

	selected := lowestAvailableRateGroup(groups, nil)

	require.NotNil(t, selected)
	require.Equal(t, int64(10), selected.ID)
}

func TestLowestAvailableRateGroupSkipsUnavailableCheapestGroup(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.1, SortOrder: 1},
		{ID: 20, RateMultiplier: 0.2, SortOrder: 2, ActiveAccountCount: 2},
	}

	selected := lowestAvailableRateGroup(groups, nil)

	require.NotNil(t, selected)
	require.Equal(t, int64(20), selected.ID)
}

func TestLowestAvailableRateGroupReturnsNilWhenNoGroupIsAvailable(t *testing.T) {
	require.Nil(t, lowestAvailableRateGroup(nil, nil))
	require.Nil(t, lowestAvailableRateGroup([]Group{{ID: 10, RateMultiplier: 0.1}}, nil))
}

func TestFilterAutoGroupCandidatesLimitsRoutingToUserSelection(t *testing.T) {
	groups := []Group{
		{ID: 10, Name: "lowest"},
		{ID: 20, Name: "selected"},
		{ID: 30, Name: "also-selected"},
	}

	selected := filterAutoGroupCandidates(groups, []int64{30, 20, 30})
	require.Equal(t, []int64{20, 30}, []int64{selected[0].ID, selected[1].ID})

	// Existing automatic keys have no persisted candidate list. Keep their
	// prior all-available behavior until the owner explicitly edits the key.
	require.Equal(t, groups, filterAutoGroupCandidates(groups, nil))
	// A persisted empty list is distinct from a legacy nil list: it means the
	// configured candidates were removed and must not broaden to all groups.
	require.Empty(t, filterAutoGroupCandidates(groups, []int64{}))
}

func TestFilterAutoGroupCandidatesForModelKeepsOnlyImageCapableGroups(t *testing.T) {
	groups := []Group{
		{ID: 10, Platform: PlatformOpenAI, AllowImageGeneration: false, ActiveAccountCount: 1},
		{ID: 20, Platform: PlatformOpenAI, AllowImageGeneration: true, ActiveAccountCount: 1},
		{ID: 30, Platform: PlatformOpenAI, AllowImageGeneration: true, ActiveAccountCount: 1},
	}
	repo := &autoGroupModelAvailabilityStub{accounts: map[int64][]Account{
		10: {{ID: 101, Platform: PlatformOpenAI}},
		20: {{ID: 201, Platform: PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"},
		}}},
		30: {{ID: 301, Platform: PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-image-1": "gpt-image-1"},
		}}},
	}}
	svc := &APIKeyService{autoGroupModelRepo: repo}

	filtered, err := svc.filterAutoGroupCandidatesForModel(context.Background(), groups, "gpt-image-2")

	require.NoError(t, err)
	require.Equal(t, []int64{20}, []int64{filtered[0].ID})
}

func TestFilterAutoGroupCandidatesForModelHonorsConfiguredModelList(t *testing.T) {
	groups := []Group{
		{ID: 10, Platform: PlatformOpenAI, AllowImageGeneration: true, ActiveAccountCount: 1,
			ModelsListConfig: GroupModelsListConfig{Enabled: true, Models: []string{"gpt-5.6"}}},
		{ID: 20, Platform: PlatformOpenAI, AllowImageGeneration: true, ActiveAccountCount: 1,
			ModelsListConfig: GroupModelsListConfig{Enabled: true, Models: []string{"gpt-image-*"}}},
	}
	svc := &APIKeyService{}

	filtered, err := svc.filterAutoGroupCandidatesForModel(context.Background(), groups, "gpt-image-2")

	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, int64(20), filtered[0].ID)
}

func TestResolveAutoGroupForModelNeverSelectsGroupWithoutRequestedModel(t *testing.T) {
	groups := []Group{
		{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.1, ActiveAccountCount: 1, AllowImageGeneration: true},
		{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.2, ActiveAccountCount: 1, AllowImageGeneration: true},
	}
	repo := &autoGroupModelAvailabilityStub{accounts: map[int64][]Account{
		10: {{ID: 101, Platform: PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-image-1": "gpt-image-1", "gpt-5.5": "gpt-5.5"},
		}}},
		20: {{ID: 201, Platform: PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2", "gpt-5.6": "gpt-5.6"},
		}}},
	}}
	svc := NewAPIKeyService(
		&apiKeyRepoStub{},
		&userRepoStub{user: &User{ID: 7, Status: StatusActive}},
		&playgroundGroupRepoStub{groups: groups},
		playgroundSubscriptionRepoStub{},
		nil,
		nil,
		nil,
	)
	svc.SetAutoGroupModelAvailabilityRepository(repo)
	apiKey := &APIKey{
		ID:                91,
		UserID:            7,
		AutoGroup:         true,
		AutoGroupStrategy: autoGroupStrategyPrice,
		AutoGroupIDs:      []int64{10, 20},
	}

	for _, model := range []string{"gpt-image-2", "gpt-5.6"} {
		resolved, err := svc.ResolveAutoGroupForModel(context.Background(), apiKey, model)
		require.NoError(t, err)
		require.NotNil(t, resolved.GroupID)
		require.Equal(t, int64(20), *resolved.GroupID)
	}

	unsupported, err := svc.ResolveAutoGroupForModel(context.Background(), apiKey, "gpt-5.7")
	require.ErrorIs(t, err, ErrAutoGroupUnavailable)
	require.Nil(t, unsupported)
}

func TestResolveAutoGroupForModelDropsStaleUnsupportedSelection(t *testing.T) {
	groupRepo := &playgroundGroupRepoStub{groups: []Group{
		{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.1, ActiveAccountCount: 1},
		{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.2, ActiveAccountCount: 1, AllowImageGeneration: true},
	}}
	svc := NewAPIKeyService(
		&apiKeyRepoStub{},
		&userRepoStub{user: &User{ID: 8, Status: StatusActive}},
		groupRepo,
		playgroundSubscriptionRepoStub{},
		nil,
		nil,
		nil,
	)
	apiKey := &APIKey{ID: 92, UserID: 8, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyPrice, AutoGroupIDs: []int64{10, 20}}
	selectionKey := autoGroupSelectionKey(apiKey, "gpt-image-2")
	svc.autoGroupSelections.Store(selectionKey, autoGroupSelection{
		userID:            apiKey.UserID,
		groupID:           10,
		selectedGroup:     &groupRepo.groups[0],
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
	})

	resolved, err := svc.ResolveAutoGroupForModel(context.Background(), apiKey, "gpt-image-2")

	require.NoError(t, err)
	require.NotNil(t, resolved.GroupID)
	require.Equal(t, int64(20), *resolved.GroupID)
}

func TestResolveAutoGroupForModelDropsSettledSelectionWhenAccountLosesModel(t *testing.T) {
	groups := []Group{
		{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.1, ActiveAccountCount: 1, AllowImageGeneration: true},
		{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.2, ActiveAccountCount: 1, AllowImageGeneration: true},
	}
	repo := &autoGroupModelAvailabilityStub{accounts: map[int64][]Account{
		10: {{ID: 101, Platform: PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-image-1": "gpt-image-1"},
		}}},
		20: {{ID: 201, Platform: PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"},
		}}},
	}}
	groupRepo := &playgroundGroupRepoStub{groups: groups}
	svc := NewAPIKeyService(
		&apiKeyRepoStub{},
		&userRepoStub{user: &User{ID: 9, Status: StatusActive}},
		groupRepo,
		playgroundSubscriptionRepoStub{},
		nil,
		nil,
		nil,
	)
	svc.SetAutoGroupModelAvailabilityRepository(repo)
	apiKey := &APIKey{ID: 93, UserID: 9, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyPrice, AutoGroupIDs: []int64{10, 20}}
	selectionKey := autoGroupSelectionKey(apiKey, "gpt-image-2")
	svc.autoGroupSelections.Store(selectionKey, autoGroupSelection{
		userID:            apiKey.UserID,
		groupID:           10,
		selectedGroup:     &groupRepo.groups[0],
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
		priceReviewAt:     time.Now().Add(time.Hour),
	})

	resolved, err := svc.ResolveAutoGroupForModel(context.Background(), apiKey, "gpt-image-2")

	require.NoError(t, err)
	require.NotNil(t, resolved.GroupID)
	require.Equal(t, int64(20), *resolved.GroupID)
}

func TestAutoGroupCandidatesSharePlatform(t *testing.T) {
	require.True(t, autoGroupCandidatesSharePlatform([]Group{
		{ID: 10, Platform: PlatformOpenAI},
		{ID: 20, Platform: PlatformOpenAI},
	}))
	require.False(t, autoGroupCandidatesSharePlatform([]Group{
		{ID: 10, Platform: PlatformOpenAI},
		{ID: 20, Platform: PlatformAnthropic},
	}))
}

func TestSelectStableAutoGroupKeepsCheapestGreenGroup(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.1, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
	}
	metrics := map[int64][]int64{
		10: {25_000, 25_100, 25_200},
		20: {1_000, 1_100, 1_200},
	}

	selected, state, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategySpeed, autoGroupSelection{})
	require.NoError(t, err)
	require.Equal(t, int64(10), selected.ID)
	require.True(t, state.settled)
}

func TestSelectStableAutoGroupSamplesCheapestBeforeAlternatives(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.05, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 30, RateMultiplier: 0.1, ActiveAccountCount: 1},
	}
	selected, state, err := selectStableAutoGroup(groups, nil, map[int64][]int64{10: {31_000, 32_000}}, autoGroupStrategyBalanced, autoGroupSelection{})
	require.NoError(t, err)
	require.Equal(t, int64(10), selected.ID)
	require.False(t, state.settled)
}

func TestSelectStableAutoGroupProbesAtMostThreeAlternativesThenSettles(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.1, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 30, RateMultiplier: 0.3, ActiveAccountCount: 1},
		{ID: 40, RateMultiplier: 0.4, ActiveAccountCount: 1},
		{ID: 50, RateMultiplier: 0.5, ActiveAccountCount: 1},
	}
	metrics := map[int64][]int64{10: {31_000, 32_000, 33_000}}

	first, state, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategySpeed, autoGroupSelection{})
	require.NoError(t, err)
	require.Equal(t, int64(20), first.ID)
	require.Equal(t, []int64{20}, state.probedGroupIDs)

	metrics[20] = []int64{31_000, 32_000, 33_000}
	second, state, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategySpeed, state)
	require.NoError(t, err)
	require.Equal(t, int64(30), second.ID)
	require.Equal(t, []int64{20, 30}, state.probedGroupIDs)

	metrics[30] = []int64{34_000, 35_000, 36_000}
	third, state, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategySpeed, state)
	require.NoError(t, err)
	require.Equal(t, int64(40), third.ID)
	require.Equal(t, []int64{20, 30, 40}, state.probedGroupIDs)

	metrics[40] = []int64{4_000, 4_100, 4_200}
	selected, state, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategySpeed, state)
	require.NoError(t, err)
	require.Equal(t, int64(40), selected.ID)
	require.True(t, state.settled)
}

func TestSelectStableAutoGroupKeepsPendingProbeUntilMetricsArrive(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.1, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 30, RateMultiplier: 0.3, ActiveAccountCount: 1},
	}
	metrics := map[int64][]int64{10: {31_000, 32_000, 33_000}}

	selected, state, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategySpeed, autoGroupSelection{})
	require.NoError(t, err)
	require.Equal(t, int64(20), selected.ID)
	require.Equal(t, int64(20), state.pendingGroupID)

	selected, nextState, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategySpeed, state)
	require.NoError(t, err)
	require.Equal(t, int64(20), selected.ID)
	require.Equal(t, state.pendingGroupID, nextState.pendingGroupID)

	metrics[20] = []int64{8_000, 8_100, 8_200}
	selected, nextState, err = selectStableAutoGroup(groups, nil, metrics, autoGroupStrategySpeed, nextState)
	require.NoError(t, err)
	require.Equal(t, int64(20), selected.ID)
	require.Zero(t, nextState.pendingGroupID)
	require.True(t, nextState.settled)
}

func TestSelectStableAutoGroupReturnsToBetterExperiencedGroup(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.1, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 30, RateMultiplier: 0.3, ActiveAccountCount: 1},
	}
	metrics := map[int64][]int64{
		10: {31_000, 32_000, 33_000},
		20: {5_000, 5_100, 5_200},
		30: {25_000, 25_100, 25_200},
	}

	selected, state, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategySpeed, autoGroupSelection{
		groupID: 30,
		settled: true,
	})
	require.NoError(t, err)
	require.Equal(t, int64(20), selected.ID)
	require.Equal(t, int64(20), state.groupID)
	require.True(t, state.settled)
}

func TestSelectStableAutoGroupAvoidsMarginalSwitch(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.1, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 30, RateMultiplier: 0.3, ActiveAccountCount: 1},
	}
	metrics := map[int64][]int64{
		10: {31_000, 32_000, 33_000},
		20: {10_000, 10_100, 10_200},
		30: {9_000, 9_100, 9_200},
	}

	selected, state, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategySpeed, autoGroupSelection{
		groupID: 20,
		settled: true,
	})
	require.NoError(t, err)
	require.Equal(t, int64(20), selected.ID)
	require.Equal(t, int64(20), state.groupID)
}

func TestSelectStableAutoGroupFallsBackToCheapestWhenExperiencePoolHasNoGreenGroup(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.1, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 30, RateMultiplier: 0.3, ActiveAccountCount: 1},
		{ID: 40, RateMultiplier: 0.4, ActiveAccountCount: 1},
	}
	metrics := map[int64][]int64{
		10: {31_000, 32_000, 33_000},
		20: {34_000, 35_000, 36_000},
		30: {37_000, 38_000, 39_000},
		40: {40_000, 41_000, 42_000},
	}

	selected, state, err := selectStableAutoGroup(groups, nil, metrics, autoGroupStrategyBalanced, autoGroupSelection{
		probedGroupIDs: []int64{20, 30, 40},
	})
	require.NoError(t, err)
	require.Equal(t, int64(10), selected.ID)
	require.True(t, state.settled)
	require.True(t, state.fallbackUntil.After(time.Now()))
}

func TestResolveAutoGroupForModelReusesSettledSelectionWithoutRepositories(t *testing.T) {
	service := &APIKeyService{}
	apiKey := &APIKey{
		ID:                91,
		UserID:            7,
		AutoGroup:         true,
		AutoGroupStrategy: autoGroupStrategyBalanced,
		AutoGroupIDs:      []int64{10, 20},
	}
	group := &Group{ID: 20, Name: "settled", RateMultiplier: 0.2, ActiveAccountCount: 1}
	key := autoGroupSelectionKey(apiKey, "gpt-test")
	service.autoGroupSelections.Store(key, autoGroupSelection{
		groupID:           group.ID,
		selectedGroup:     group,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		priceReviewAt:     time.Now().Add(autoGroupPriceReviewInterval),
		settled:           true,
	})

	resolved, err := service.ResolveAutoGroupForModel(context.Background(), apiKey, "gpt-test")
	require.NoError(t, err)
	require.NotNil(t, resolved.GroupID)
	require.Equal(t, int64(20), *resolved.GroupID)
	require.Equal(t, "settled", resolved.Group.Name)
}

func TestResolveAutoGroupForModelExcludingLeavesFixedGroupKeyUntouched(t *testing.T) {
	groupID := int64(2)
	apiKey := &APIKey{ID: 95, UserID: 10, GroupID: &groupID}

	resolved, err := (&APIKeyService{}).ResolveAutoGroupForModelExcluding(context.Background(), apiKey, "gpt-test", map[int64]struct{}{2: {}})

	require.NoError(t, err)
	require.Same(t, apiKey, resolved)
	require.Equal(t, int64(2), *resolved.GroupID)
}

func TestPeerVerifiedCheaperAutoGroupRequiresAvailabilityAndReliableSpeed(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.6, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 30, RateMultiplier: 0.1, ActiveAccountCount: 0},
	}

	selected := peerVerifiedCheaperAutoGroup(groups, nil, 10, map[int64][]int64{
		20: {2_000, 2_100, 2_200},
		30: {1_000, 1_100, 1_200},
	})

	require.NotNil(t, selected)
	require.Equal(t, int64(20), selected.ID)
}

func TestPeerVerifiedCheaperAutoGroupRejectsSparseOrSlowPeerData(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.6, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 30, RateMultiplier: 0.3, ActiveAccountCount: 1},
	}

	require.Nil(t, peerVerifiedCheaperAutoGroup(groups, nil, 10, map[int64][]int64{
		20: {1_000, 1_100},
		30: {31_000, 31_100, 31_200},
	}))
}

func TestPeerVerifiedCheaperAutoGroupRequiresEightPercentSavings(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.60, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.56, ActiveAccountCount: 1},
	}

	selected := peerVerifiedCheaperAutoGroup(groups, nil, 10, map[int64][]int64{
		20: {2_000, 2_100, 2_200},
	})

	require.Nil(t, selected, "a small price difference must not justify changing the upstream account")
}

func TestObserveAutoGroupRequestResultTriggersOnlyAfterFiveSlowEvents(t *testing.T) {
	service := &APIKeyService{}
	apiKey := &APIKey{ID: 92, UserID: 8, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyBalanced}
	group := &Group{ID: 20, ActiveAccountCount: 1}
	key := autoGroupSelectionKey(apiKey, "gpt-test")
	service.autoGroupSelections.Store(key, autoGroupSelection{
		groupID:           group.ID,
		selectedGroup:     group,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
	})
	slow := int64(31_000)

	for range 4 {
		service.ObserveAutoGroupRequestResult(apiKey, "gpt-test", 200, &slow)
	}
	selection := service.autoGroupSelection(key)
	require.False(t, selection.needsEvaluation)
	require.Equal(t, 4, selection.consecutiveSlowRequests)

	service.ObserveAutoGroupRequestResult(apiKey, "gpt-test", 200, &slow)
	selection = service.autoGroupSelection(key)
	require.True(t, selection.needsEvaluation)
}

func TestObserveAutoGroupRequestResultRequiresThreeTransientFailures(t *testing.T) {
	service := &APIKeyService{}
	apiKey := &APIKey{ID: 93, UserID: 9, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyBalanced}
	group := &Group{ID: 20, ActiveAccountCount: 1}
	key := autoGroupSelectionKey(apiKey, "gpt-test")
	service.autoGroupSelections.Store(key, autoGroupSelection{
		groupID:           group.ID,
		selectedGroup:     group,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
	})

	service.ObserveAutoGroupRequestResult(apiKey, "gpt-test", 503, nil)
	selection := service.autoGroupSelection(key)
	require.False(t, selection.needsEvaluation)
	require.Equal(t, 1, selection.transientFailureStreak)
	service.ObserveAutoGroupRequestResult(apiKey, "gpt-test", 503, nil)
	selection = service.autoGroupSelection(key)
	require.False(t, selection.needsEvaluation)
	require.Equal(t, 2, selection.transientFailureStreak)
	service.ObserveAutoGroupRequestResult(apiKey, "gpt-test", 503, nil)
	selection = service.autoGroupSelection(key)
	require.True(t, selection.needsEvaluation)
	revision := selection.revision

	service.ObserveAutoGroupRequestResult(apiKey, "gpt-test", 503, nil)
	require.Equal(t, revision, service.autoGroupSelection(key).revision, "duplicate failure observations must not starve an in-flight resolver")
}

func TestMarkAutoGroupSelectionForImmediateFailureBypassesTransientThreshold(t *testing.T) {
	service := &APIKeyService{}
	apiKey := &APIKey{ID: 96, UserID: 12, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyBalanced}
	group := &Group{ID: 20, ActiveAccountCount: 1}
	key := autoGroupSelectionKey(apiKey, "gpt-test")
	service.autoGroupSelections.Store(key, autoGroupSelection{
		groupID:           group.ID,
		selectedGroup:     group,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
	})

	require.True(t, service.markAutoGroupSelectionForImmediateFailure(apiKey, "gpt-test"))
	selection := service.autoGroupSelection(key)
	require.True(t, selection.needsEvaluation)
	require.True(t, selection.failureTriggered)
	require.Equal(t, group.ID, selection.eventGroupID)
	require.Equal(t, autoGroupTransientFailureThreshold, selection.transientFailureStreak)
}

func TestObserveAutoGroupRequestResultIgnores529ForSwitching(t *testing.T) {
	service := &APIKeyService{}
	apiKey := &APIKey{ID: 95, UserID: 11, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyBalanced}
	group := &Group{ID: 20, ActiveAccountCount: 1}
	key := autoGroupSelectionKey(apiKey, "gpt-test")
	service.autoGroupSelections.Store(key, autoGroupSelection{
		groupID:           group.ID,
		selectedGroup:     group,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
	})

	for range 5 {
		service.ObserveAutoGroupRequestResult(apiKey, "gpt-test", 529, nil)
	}
	selection := service.autoGroupSelection(key)
	require.False(t, selection.needsEvaluation)
	require.Zero(t, selection.transientFailureStreak)
	require.False(t, selection.failureTriggered)
}

func TestObserveAutoGroupRequestResultDoesNotReevaluateStableGreenRequests(t *testing.T) {
	service := &APIKeyService{}
	apiKey := &APIKey{ID: 94, UserID: 10, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyBalanced}
	group := &Group{ID: 20, ActiveAccountCount: 1}
	key := autoGroupSelectionKey(apiKey, "gpt-test")
	service.autoGroupSelections.Store(key, autoGroupSelection{
		groupID:           group.ID,
		selectedGroup:     group,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
	})
	green := int64(5_000)

	for range 20 {
		service.ObserveAutoGroupRequestResult(apiKey, "gpt-test", 200, &green)
	}
	selection := service.autoGroupSelection(key)
	require.False(t, selection.needsEvaluation)
	require.Zero(t, selection.consecutiveSlowRequests)
}

func TestAutoGroupBalancedWeightsFavorPrice(t *testing.T) {
	priceWeight, speedWeight := autoGroupWeights(autoGroupStrategyBalanced)
	require.Equal(t, 0.70, priceWeight)
	require.Equal(t, 0.30, speedWeight)
}

func TestNormalizeAutoGroupStrategyDefaultsToPrice(t *testing.T) {
	require.Equal(t, autoGroupStrategyPrice, normalizeAutoGroupStrategy(""))
	require.Equal(t, autoGroupStrategyPrice, normalizeAutoGroupStrategy("unknown"))
}

func TestSelectStableAutoGroupPriceAlwaysUsesCheapestRate(t *testing.T) {
	groups := []Group{
		{ID: 10, RateMultiplier: 0.9, ActiveAccountCount: 1},
		{ID: 20, RateMultiplier: 0.2, ActiveAccountCount: 1},
	}
	selected, state, err := selectStableAutoGroup(groups, map[int64]float64{10: 0.05}, map[int64][]int64{
		10: {0, 100},
		20: {29_999, 30_000},
	}, autoGroupStrategyPrice, autoGroupSelection{})
	require.NoError(t, err)
	require.Equal(t, int64(10), selected.ID)
	require.True(t, state.settled)
	require.Equal(t, 0.0, clampAutoGroupScore(-0.1))
	require.Equal(t, 1.0, clampAutoGroupScore(1.1))
}
