package service

import (
	"context"
	"errors"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
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

type pawConfigAttrDefRepoStub struct {
	def *UserAttributeDefinition
	err error
}

func (s *pawConfigAttrDefRepoStub) Create(context.Context, *UserAttributeDefinition) error {
	return nil
}
func (s *pawConfigAttrDefRepoStub) GetByID(context.Context, int64) (*UserAttributeDefinition, error) {
	return nil, nil
}
func (s *pawConfigAttrDefRepoStub) GetByKey(context.Context, string) (*UserAttributeDefinition, error) {
	return s.def, s.err
}
func (s *pawConfigAttrDefRepoStub) Update(context.Context, *UserAttributeDefinition) error {
	return nil
}
func (s *pawConfigAttrDefRepoStub) Delete(context.Context, int64) error { return nil }
func (s *pawConfigAttrDefRepoStub) List(context.Context, bool) ([]UserAttributeDefinition, error) {
	return nil, nil
}
func (s *pawConfigAttrDefRepoStub) UpdateDisplayOrders(context.Context, map[int64]int) error {
	return nil
}
func (s *pawConfigAttrDefRepoStub) ExistsByKey(context.Context, string) (bool, error) {
	return false, nil
}

type pawConfigAttrValueRepoStub struct{}

func (s *pawConfigAttrValueRepoStub) GetByUserID(context.Context, int64) ([]UserAttributeValue, error) {
	return nil, nil
}
func (s *pawConfigAttrValueRepoStub) GetByUserIDs(context.Context, []int64) ([]UserAttributeValue, error) {
	return nil, nil
}
func (s *pawConfigAttrValueRepoStub) UpsertBatch(context.Context, int64, []UpdateUserAttributeInput) error {
	return nil
}
func (s *pawConfigAttrValueRepoStub) DeleteByAttributeID(context.Context, int64) error { return nil }
func (s *pawConfigAttrValueRepoStub) DeleteByUserID(context.Context, int64) error      { return nil }

func newPawDefaultsStoreService(defRepo UserAttributeDefinitionRepository, valueRepo UserAttributeValueRepository) *UserAttributeService {
	return NewUserAttributeService(defRepo, valueRepo)
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

func TestPawConfigServiceDoesNotAdvertiseReasoningForUnknownOpenAIModel(t *testing.T) {
	svc := NewPawConfigService(
		&pawConfigGroupSourceStub{groups: []Group{{ID: 7, Name: "Allowed", Platform: PlatformOpenAI, Status: StatusActive}}},
		&pawConfigUserSourceStub{user: &User{ID: 42, Username: "user", Email: "user@example.com"}},
		&pawConfigChannelSourceStub{channels: map[int64]*Channel{
			7: {ID: 70, Status: StatusActive, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"custom-openai-model"}}}},
		}},
		&pawConfigDefaultsStoreStub{},
	)

	config, err := svc.GetConfig(context.Background(), 42)

	require.NoError(t, err)
	require.Len(t, config.Groups, 1)
	require.Len(t, config.Groups[0].Models, 1)
	require.False(t, config.Groups[0].Models[0].Reasoning.Supported)
	require.Empty(t, config.Groups[0].Models[0].Reasoning.Values)
	require.Empty(t, config.Groups[0].Models[0].Reasoning.Default)
}

func TestPawConfigServiceRejectsStalePersistedDefaults(t *testing.T) {
	store := &pawConfigDefaultsStoreStub{defaults: PawDefaults{GroupID: 7, ModelID: "missing-model", Reasoning: "low"}}
	_, err := newPawConfigTestService(store).GetConfig(context.Background(), 42)

	require.Error(t, err)
	require.Equal(t, 503, infraerrors.Code(err))
	require.Equal(t, "CONFIG_UNAVAILABLE", infraerrors.Reason(err))
}

func TestPawConfigServiceCanReplaceStalePersistedDefaults(t *testing.T) {
	store := &pawConfigDefaultsStoreStub{defaults: PawDefaults{GroupID: 7, ModelID: "missing-model", Reasoning: "low"}}
	want := PawDefaults{GroupID: 7, ModelID: "gpt-5", Reasoning: "low"}

	err := newPawConfigTestService(store).SaveDefaults(context.Background(), 42, want)

	require.NoError(t, err)
	require.Equal(t, want, store.defaults)
	require.Equal(t, 1, store.called)
}

func TestPawConfigServiceSaveDefaultsWithNilStoreReturnsUnavailable(t *testing.T) {
	err := newPawConfigTestService(nil).SaveDefaults(context.Background(), 42, PawDefaults{GroupID: 7, ModelID: "gpt-5", Reasoning: "low"})

	require.Error(t, err)
	require.Equal(t, "CONFIG_UNAVAILABLE", infraerrors.Reason(err))
}

func TestUserAttributePawDefaultsStorePropagatesDefinitionLookupErrors(t *testing.T) {
	wantErr := errors.New("definition lookup failed")
	store := UserAttributePawDefaultsStore{Service: newPawDefaultsStoreService(&pawConfigAttrDefRepoStub{err: wantErr}, &pawConfigAttrValueRepoStub{})}

	_, err := store.GetPawDefaults(context.Background(), 42)

	require.ErrorIs(t, err, wantErr)
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

func TestPawConfigServiceMapsGroupAndModelCapabilities(t *testing.T) {
	svc := NewPawConfigService(
		&pawConfigGroupSourceStub{groups: []Group{
			{ID: 7, Name: "Images", Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true},
			{ID: 8, Name: "Blocked Images", Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: false},
		}},
		&pawConfigUserSourceStub{user: &User{ID: 42, Username: "user", Email: "user@example.com"}},
		&pawConfigChannelSourceStub{channels: map[int64]*Channel{
			7: {ID: 70, Status: StatusActive, ModelPricing: []ChannelModelPricing{
				{Platform: PlatformOpenAI, Models: []string{"gpt-5.4"}, ImageInputPrice: pawFloatPtr(0.01)},
				{Platform: PlatformOpenAI, Models: []string{"gpt-image-2"}, BillingMode: BillingModeImage},
			}},
			8: {ID: 80, Status: StatusActive, ModelPricing: []ChannelModelPricing{
				{Platform: PlatformOpenAI, Models: []string{"gpt-image-2"}, BillingMode: BillingModeImage},
			}},
		}},
		&pawConfigDefaultsStoreStub{},
	)

	config, err := svc.GetConfig(context.Background(), 42)

	require.NoError(t, err)
	require.Len(t, config.Groups, 2)
	require.True(t, config.Groups[0].Models[0].Vision)
	require.True(t, config.Groups[0].Models[0].FileInput)
	require.False(t, config.Groups[0].Models[0].ImageGeneration)
	require.True(t, config.Groups[0].Models[1].ImageGeneration)
	require.False(t, config.Groups[1].Models[0].ImageGeneration)
}

func pawFloatPtr(value float64) *float64 {
	return &value
}
