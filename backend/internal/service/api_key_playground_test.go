//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type playgroundAPIKeyRepoStub struct {
	apiKeyRepoStub
	keys []APIKey
}

func (s *playgroundAPIKeyRepoStub) Create(_ context.Context, key *APIKey) error {
	copy := *key
	copy.ID = int64(len(s.keys) + 1)
	key.ID = copy.ID
	s.keys = append(s.keys, copy)
	return nil
}

func (s *playgroundAPIKeyRepoStub) SearchAPIKeys(_ context.Context, userID int64, keyword string, _ int) ([]APIKey, error) {
	keys := make([]APIKey, 0)
	for _, key := range s.keys {
		if key.UserID == userID && strings.Contains(strings.ToLower(key.Name), strings.ToLower(keyword)) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

type playgroundGroupRepoStub struct {
	groupRepoNoop
	groups []Group
}

func (s *playgroundGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	for i := range s.groups {
		if s.groups[i].ID == id {
			return &s.groups[i], nil
		}
	}
	return nil, ErrGroupNotFound
}

func (s *playgroundGroupRepoStub) ListActive(context.Context) ([]Group, error) {
	return append([]Group(nil), s.groups...), nil
}

type playgroundSubscriptionRepoStub struct{ userSubRepoNoop }

func (playgroundSubscriptionRepoStub) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	return nil, nil
}

type playgroundDefaultsProviderStub struct {
	config PlaygroundDefaultConfig
}

func (s playgroundDefaultsProviderStub) GetPlaygroundDefaultConfig(context.Context) (PlaygroundDefaultConfig, error) {
	return s.config, nil
}

func TestSelectPlaygroundGroups(t *testing.T) {
	chat, image := selectPlaygroundGroups([]Group{
		{ID: 1, Platform: PlatformAnthropic, AllowImageGeneration: true},
		{ID: 2, Platform: PlatformOpenAI},
		{ID: 3, Platform: PlatformOpenAI, AllowImageGeneration: true},
	})

	require.NotNil(t, chat)
	require.Equal(t, int64(2), chat.ID)
	require.NotNil(t, image)
	require.Equal(t, int64(3), image.ID)
}

func TestSelectPlaygroundGroupsWithoutImageGroup(t *testing.T) {
	chat, image := selectPlaygroundGroups([]Group{{ID: 2, Platform: PlatformOpenAI}})

	require.NotNil(t, chat)
	require.Nil(t, image)
}

func TestAPIKeyServiceGetByIDForAuthResolvesAutomaticGroup(t *testing.T) {
	apiKeyRepo := &apiKeyRepoStub{apiKey: &APIKey{
		ID:                41,
		UserID:            17,
		AutoGroup:         true,
		AutoGroupStrategy: autoGroupStrategyBalanced,
		AutoGroupIDs:      []int64{1, 2},
	}}
	service := NewAPIKeyService(
		apiKeyRepo,
		&userRepoStub{user: &User{ID: 17, Status: StatusActive}},
		&playgroundGroupRepoStub{groups: []Group{
			{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.8, ActiveAccountCount: 1},
			{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.2, ActiveAccountCount: 1},
		}},
		playgroundSubscriptionRepoStub{},
		nil,
		nil,
		&config.Config{},
	)

	resolved, err := service.GetByIDForAuth(context.Background(), 41)

	require.NoError(t, err)
	require.NotNil(t, resolved.GroupID)
	require.Equal(t, int64(2), *resolved.GroupID)
	require.NotNil(t, resolved.Group)
	require.Equal(t, PlatformOpenAI, resolved.Group.Platform)
	require.True(t, resolved.AutoGroup)
	require.Nil(t, apiKeyRepo.apiKey.GroupID)
}

func TestResolveAutoGroupForModelExcludingAdvancesPastExhaustedGroup(t *testing.T) {
	groupRepo := &playgroundGroupRepoStub{groups: []Group{
		{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 11, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.4, ActiveAccountCount: 1},
	}}
	service := NewAPIKeyService(
		&apiKeyRepoStub{},
		&userRepoStub{user: &User{ID: 21, Status: StatusActive}},
		groupRepo,
		playgroundSubscriptionRepoStub{},
		nil,
		nil,
		&config.Config{},
	)
	apiKey := &APIKey{
		ID:                121,
		UserID:            21,
		AutoGroup:         true,
		AutoGroupStrategy: autoGroupStrategyPrice,
		AutoGroupIDs:      []int64{2, 11},
	}
	selectionKey := autoGroupSelectionKey(apiKey, "gpt-test")
	service.autoGroupSelections.Store(selectionKey, autoGroupSelection{
		groupID:           2,
		selectedGroup:     &groupRepo.groups[0],
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
	})

	resolved, err := service.ResolveAutoGroupForModelExcluding(context.Background(), apiKey, "gpt-test", map[int64]struct{}{2: {}})

	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.NotNil(t, resolved.GroupID)
	require.Equal(t, int64(11), *resolved.GroupID)
	require.Equal(t, int64(11), service.autoGroupSelection(selectionKey).groupID)
}

func TestResolveAutoGroupForModelExcludingStopsWhenEveryCandidateWasTried(t *testing.T) {
	groupRepo := &playgroundGroupRepoStub{groups: []Group{
		{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 11, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.4, ActiveAccountCount: 1},
	}}
	service := NewAPIKeyService(
		&apiKeyRepoStub{},
		&userRepoStub{user: &User{ID: 22, Status: StatusActive}},
		groupRepo,
		playgroundSubscriptionRepoStub{},
		nil,
		nil,
		&config.Config{},
	)
	apiKey := &APIKey{
		ID:                122,
		UserID:            22,
		AutoGroup:         true,
		AutoGroupStrategy: autoGroupStrategyPrice,
		AutoGroupIDs:      []int64{2, 11},
	}
	selectionKey := autoGroupSelectionKey(apiKey, "gpt-test")
	service.autoGroupSelections.Store(selectionKey, autoGroupSelection{
		groupID:           2,
		selectedGroup:     &groupRepo.groups[0],
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		settled:           true,
	})

	resolved, err := service.ResolveAutoGroupForModelExcluding(context.Background(), apiKey, "gpt-test", map[int64]struct{}{2: {}, 11: {}})

	require.ErrorIs(t, err, ErrAutoGroupUnavailable)
	require.Nil(t, resolved)
}

func TestResolveAutoGroupForModelExcludingSkipsAllFailedCheapGroupsBeforeSelecting(t *testing.T) {
	groupRepo := &playgroundGroupRepoStub{groups: []Group{
		{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.05, ActiveAccountCount: 1},
		{ID: 11, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.075, ActiveAccountCount: 1},
		{ID: 13, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.5, ActiveAccountCount: 1, AllowImageGeneration: true},
	}}
	service := NewAPIKeyService(
		&apiKeyRepoStub{},
		&userRepoStub{user: &User{ID: 23, Status: StatusActive}},
		groupRepo,
		playgroundSubscriptionRepoStub{},
		nil,
		nil,
		&config.Config{},
	)
	apiKey := &APIKey{
		ID:                123,
		UserID:            23,
		AutoGroup:         true,
		AutoGroupStrategy: autoGroupStrategyPrice,
		AutoGroupIDs:      []int64{2, 11, 13},
	}

	resolved, err := service.ResolveAutoGroupForModelExcluding(
		context.Background(),
		apiKey,
		"gpt-image-2",
		map[int64]struct{}{2: {}, 11: {}},
	)

	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.NotNil(t, resolved.GroupID)
	require.Equal(t, int64(13), *resolved.GroupID)
}

func TestAPIKeyServiceEnsurePlaygroundAPIKeys(t *testing.T) {
	apiKeyRepo := &playgroundAPIKeyRepoStub{}
	groupRepo := &playgroundGroupRepoStub{groups: []Group{
		{ID: 1, Platform: PlatformOpenAI, Status: StatusActive},
		{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true},
	}}
	service := NewAPIKeyService(
		apiKeyRepo,
		&userRepoStub{user: &User{ID: 17, Status: StatusActive}},
		groupRepo,
		playgroundSubscriptionRepoStub{},
		nil,
		nil,
		&config.Config{},
	)

	require.NoError(t, service.EnsurePlaygroundAPIKeys(context.Background(), 17))
	require.Len(t, apiKeyRepo.keys, 2)
	require.Equal(t, PlaygroundChatAPIKeyName, apiKeyRepo.keys[0].Name)
	require.True(t, apiKeyRepo.keys[0].AutoGroup)
	require.Nil(t, apiKeyRepo.keys[0].GroupID)
	require.Equal(t, []int64{1, 2}, apiKeyRepo.keys[0].AutoGroupIDs)
	require.Equal(t, PlaygroundImageAPIKeyName, apiKeyRepo.keys[1].Name)
	require.True(t, apiKeyRepo.keys[1].AutoGroup)
	require.Nil(t, apiKeyRepo.keys[1].GroupID)
	require.Equal(t, []int64{2}, apiKeyRepo.keys[1].AutoGroupIDs)

	require.NoError(t, service.EnsurePlaygroundAPIKeys(context.Background(), 17))
	require.Len(t, apiKeyRepo.keys, 2)
}

func TestAPIKeyServiceEnsurePlaygroundAPIKeysWithoutImageGroup(t *testing.T) {
	apiKeyRepo := &playgroundAPIKeyRepoStub{}
	service := NewAPIKeyService(
		apiKeyRepo,
		&userRepoStub{user: &User{ID: 18, Status: StatusActive}},
		&playgroundGroupRepoStub{groups: []Group{{ID: 1, Platform: PlatformOpenAI, Status: StatusActive}}},
		playgroundSubscriptionRepoStub{},
		nil,
		nil,
		&config.Config{},
	)

	require.NoError(t, service.EnsurePlaygroundAPIKeys(context.Background(), 18))
	require.Len(t, apiKeyRepo.keys, 1)
	require.Equal(t, PlaygroundChatAPIKeyName, apiKeyRepo.keys[0].Name)
}

func TestAPIKeyServiceEnsurePlaygroundAPIKeysUsesConfiguredCandidates(t *testing.T) {
	apiKeyRepo := &playgroundAPIKeyRepoStub{}
	service := NewAPIKeyService(
		apiKeyRepo,
		&userRepoStub{user: &User{ID: 19, Status: StatusActive}},
		&playgroundGroupRepoStub{groups: []Group{
			{ID: 1, Platform: PlatformOpenAI, Status: StatusActive},
			{ID: 2, Platform: PlatformOpenAI, Status: StatusActive},
			{ID: 3, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true},
		}},
		playgroundSubscriptionRepoStub{}, nil, nil, &config.Config{},
	)
	service.SetPlaygroundDefaultsProvider(playgroundDefaultsProviderStub{config: PlaygroundDefaultConfig{
		ChatGroupIDs:  []int64{2},
		ImageGroupIDs: []int64{3},
		ChatStrategy:  autoGroupStrategySpeed,
		ImageStrategy: autoGroupStrategyBalanced,
	}})

	require.NoError(t, service.EnsurePlaygroundAPIKeys(context.Background(), 19))
	require.Len(t, apiKeyRepo.keys, 2)
	require.Equal(t, []int64{2}, apiKeyRepo.keys[0].AutoGroupIDs)
	require.Equal(t, autoGroupStrategySpeed, apiKeyRepo.keys[0].AutoGroupStrategy)
	require.Equal(t, []int64{3}, apiKeyRepo.keys[1].AutoGroupIDs)
	require.Equal(t, autoGroupStrategyBalanced, apiKeyRepo.keys[1].AutoGroupStrategy)
}

func TestSelectPlaygroundGroupIDsDoesNotEscapeConfiguredScope(t *testing.T) {
	groups := []Group{{ID: 1, Platform: PlatformOpenAI, Status: StatusActive}}

	require.Empty(t, selectPlaygroundGroupIDs(groups, []int64{99}, false))
}
