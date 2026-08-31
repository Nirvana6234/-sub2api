package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type pawConfigGroupSourceStub struct {
	groups []Group
}

func (s *pawConfigGroupSourceStub) AvailableGroups(context.Context, int64) ([]Group, error) {
	return s.groups, nil
}

type pawConfigUserSourceStub struct{ user *User }

func (s *pawConfigUserSourceStub) GetByID(context.Context, int64) (*User, error) { return s.user, nil }

type pawConfigChannelSourceStub struct{ channels map[int64]*Channel }

func (s *pawConfigChannelSourceStub) GetChannelForGroup(_ context.Context, groupID int64) (*Channel, error) {
	return s.channels[groupID], nil
}

type pawConfigDefaultsStoreStub struct {
	defaults PawDefaults
	called   int
}

func (s *pawConfigDefaultsStoreStub) GetPawDefaults(context.Context, int64) (PawDefaults, error) {
	return s.defaults, nil
}

func (s *pawConfigDefaultsStoreStub) SavePawDefaults(_ context.Context, _ int64, defaults PawDefaults) error {
	s.called++
	s.defaults = defaults
	return nil
}

func newPawConfigTestService(store PawDefaultsStore) *PawConfigService {
	return NewPawConfigService(
		&pawConfigGroupSourceStub{groups: []Group{
			{ID: 7, Name: "Allowed", Description: "ok", Platform: PlatformOpenAI, Status: StatusActive},
			{ID: 8, Name: "Denied", Platform: PlatformOpenAI, Status: StatusActive},
		}},
		&pawConfigUserSourceStub{user: &User{ID: 42, Username: "user", Email: "user@example.com"}},
		&pawConfigChannelSourceStub{channels: map[int64]*Channel{
			7: {ID: 70, Status: StatusActive, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-5", "gpt-5-mini"}}}},
		}},
		store,
	)
}

func TestPawConfigServiceGetConfigReturnsOnlyAuthorizedGroupsAndScopedModels(t *testing.T) {
	store := &pawConfigDefaultsStoreStub{}
	config, err := newPawConfigTestService(store).GetConfig(context.Background(), 42)

	require.NoError(t, err)
	require.Len(t, config.Groups, 1)
	require.Equal(t, int64(7), config.Groups[0].ID)
	require.Equal(t, []string{"gpt-5", "gpt-5-mini"}, []string{config.Groups[0].Models[0].ID, config.Groups[0].Models[1].ID})
}

func TestPawConfigServiceDoesNotAdvertiseUnsupportedReasoningValues(t *testing.T) {
	config, err := newPawConfigTestService(&pawConfigDefaultsStoreStub{}).GetConfig(context.Background(), 42)

	require.NoError(t, err)
	for _, model := range config.Groups[0].Models {
		require.NotContains(t, model.Reasoning.Values, "unsupported")
	}
}

func TestPawConfigServiceRejectsInvalidDefaultWithoutChangingPreviousDefault(t *testing.T) {
	store := &pawConfigDefaultsStoreStub{defaults: PawDefaults{GroupID: 7, ModelID: "gpt-5", Reasoning: "low"}}
	err := newPawConfigTestService(store).SaveDefaults(context.Background(), 42, PawDefaults{GroupID: 8, ModelID: "not-available", Reasoning: "unsupported"})

	require.Error(t, err)
	require.Equal(t, 0, store.called)
	require.Equal(t, PawDefaults{GroupID: 7, ModelID: "gpt-5", Reasoning: "low"}, store.defaults)
}

func TestPawConfigServicePersistsValidDefault(t *testing.T) {
	store := &pawConfigDefaultsStoreStub{}
	defaults := PawDefaults{GroupID: 7, ModelID: "gpt-5", Reasoning: "low"}
	err := newPawConfigTestService(store).SaveDefaults(context.Background(), 42, defaults)

	require.NoError(t, err)
	require.Equal(t, 1, store.called)
	require.Equal(t, defaults, store.defaults)
}
