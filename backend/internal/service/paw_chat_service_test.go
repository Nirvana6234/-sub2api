package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type pawChatKeySourceStub struct {
	apiKey       *APIKey
	subscription *UserSubscription
	userID       int64
	groupID      int64
}

func (s *pawChatKeySourceStub) ResolvePawAPIKey(_ context.Context, userID, groupID int64) (*APIKey, *UserSubscription, error) {
	s.userID = userID
	s.groupID = groupID
	if s.apiKey == nil {
		return nil, nil, errors.New("missing internal key")
	}
	return s.apiKey, s.subscription, nil
}

type pawChatAPIKeyLookupStub struct {
	groups       []Group
	keys         []APIKey
	created      bool
	ensureCalled bool
}

func (s *pawChatAPIKeyLookupStub) SearchAPIKeys(context.Context, int64, string, int) ([]APIKey, error) {
	return append([]APIKey(nil), s.keys...), nil
}
func (s *pawChatAPIKeyLookupStub) EnsurePlaygroundAPIKeys(context.Context, int64) error {
	s.ensureCalled = true
	return nil
}
func (s *pawChatAPIKeyLookupStub) Create(_ context.Context, userID int64, req CreateAPIKeyRequest) (*APIKey, error) {
	s.created = true
	key := &APIKey{ID: 100, UserID: userID, Name: req.Name, Status: StatusActive, User: &User{ID: userID}}
	s.keys = append(s.keys, *key)
	return key, nil
}
func (s *pawChatAPIKeyLookupStub) GetByID(context.Context, int64) (*APIKey, error) {
	if len(s.keys) == 0 {
		return nil, errors.New("key not found")
	}
	key := s.keys[len(s.keys)-1]
	return &key, nil
}
func (s *pawChatAPIKeyLookupStub) GetAvailableGroups(context.Context, int64) ([]Group, error) {
	return append([]Group(nil), s.groups...), nil
}
func (s *pawChatAPIKeyLookupStub) GetActiveSubscriptionForGroup(context.Context, int64, int64) (*UserSubscription, error) {
	return nil, nil
}

type pawChatGroupSourceStub struct {
	groups []Group
}

func (s pawChatGroupSourceStub) AvailableGroups(context.Context, int64) ([]Group, error) {
	return append([]Group(nil), s.groups...), nil
}

func newPawChatTestService(keySource PawChatKeySource) *PawChatService {
	config := NewPawConfigService(
		pawChatGroupSourceStub{groups: []Group{
			{ID: 7, Name: "OpenAI", Platform: PlatformOpenAI, Status: StatusActive},
		}},
		&pawConfigUserSourceStub{user: &User{ID: 42, Username: "user", Email: "user@example.com"}},
		&pawConfigChannelSourceStub{channels: map[int64]*Channel{
			7: {ID: 70, Status: StatusActive, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-5"}}}},
		}},
		&pawConfigDefaultsStoreStub{},
		&PricingService{pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5": {SupportsReasoning: true, SupportsMinimalReasoningEffort: true, SupportsXHighReasoningEffort: true, SupportsMaxReasoningEffort: true},
		}},
	)
	return NewPawChatService(config, keySource)
}

func TestPawChatServicePreparesAuthorizedRequestWithServerSideKey(t *testing.T) {
	key := &APIKey{ID: 99, UserID: 42, Status: StatusActive, User: &User{ID: 42}, Group: &Group{ID: 7, Platform: PlatformOpenAI}}
	keys := &pawChatKeySourceStub{apiKey: key}
	svc := newPawChatTestService(keys)

	resolution, err := svc.Prepare(context.Background(), 42, PawChatRequest{
		GroupID:   7,
		ModelID:   "gpt-5",
		Reasoning: "high",
		Messages:  []PawChatMessage{{Role: "user", Content: "hello"}},
		Stream:    true,
	})

	require.NoError(t, err)
	require.Equal(t, int64(42), keys.userID)
	require.Equal(t, int64(7), keys.groupID)
	require.NotSame(t, key, resolution.APIKey)
	require.Equal(t, int64(7), *resolution.APIKey.GroupID)
	require.Equal(t, "gpt-5", resolution.Model)
	var body map[string]any
	require.NoError(t, json.Unmarshal(resolution.Body, &body))
	require.Equal(t, "gpt-5", body["model"])
	require.Equal(t, true, body["stream"])
	require.Equal(t, "high", body["reasoning_effort"])
	require.NotContains(t, body, "api_key")
	require.NotContains(t, body, "key")
	require.NotContains(t, body, "group_id")
}

func TestPawChatServiceRejectsForbiddenGroup(t *testing.T) {
	svc := newPawChatTestService(&pawChatKeySourceStub{apiKey: &APIKey{ID: 99, UserID: 42, Status: StatusActive}})

	_, err := svc.Prepare(context.Background(), 42, PawChatRequest{
		GroupID: 8,
		ModelID: "gpt-5",
		Messages: []PawChatMessage{
			{Role: "user", Content: "hello"},
		},
	})

	require.Error(t, err)
	require.Equal(t, "GROUP_FORBIDDEN", infraerrors.Reason(err))
}

func TestPawChatServiceRejectsModelOutsideSelectedGroup(t *testing.T) {
	svc := newPawChatTestService(&pawChatKeySourceStub{apiKey: &APIKey{ID: 99, UserID: 42, Status: StatusActive}})

	_, err := svc.Prepare(context.Background(), 42, PawChatRequest{
		GroupID: 7,
		ModelID: "missing-model",
		Messages: []PawChatMessage{
			{Role: "user", Content: "hello"},
		},
	})

	require.Error(t, err)
	require.Equal(t, "MODEL_UNAVAILABLE", infraerrors.Reason(err))
}

func TestPawChatServiceRejectsUnsupportedReasoning(t *testing.T) {
	svc := newPawChatTestService(&pawChatKeySourceStub{apiKey: &APIKey{ID: 99, UserID: 42, Status: StatusActive}})

	_, err := svc.Prepare(context.Background(), 42, PawChatRequest{
		GroupID:   7,
		ModelID:   "gpt-5",
		Reasoning: "unsupported",
		Messages:  []PawChatMessage{{Role: "user", Content: "hello"}},
	})

	require.Error(t, err)
	require.Equal(t, "REASONING_UNSUPPORTED", infraerrors.Reason(err))
}

func TestAPIKeyPawChatKeySourceReusesPurposeBoundKey(t *testing.T) {
	lookup := &pawChatAPIKeyLookupStub{
		groups: []Group{{ID: 7, Platform: PlatformOpenAI, Status: StatusActive}},
		keys:   []APIKey{{ID: 99, UserID: 42, Name: PlaygroundChatAPIKeyName, Status: StatusActive, User: &User{ID: 42}}},
	}

	key, _, err := (APIKeyPawChatKeySource{Service: lookup}).ResolvePawAPIKey(context.Background(), 42, 7)

	require.NoError(t, err)
	require.Equal(t, int64(99), key.ID)
	require.False(t, lookup.ensureCalled)
	require.False(t, lookup.created)
}

func TestAPIKeyPawChatKeySourceCreatesServerSideKeyWhenMissing(t *testing.T) {
	lookup := &pawChatAPIKeyLookupStub{
		groups: []Group{{ID: 7, Platform: PlatformOpenAI, Status: StatusActive}},
	}

	key, _, err := (APIKeyPawChatKeySource{Service: lookup}).ResolvePawAPIKey(context.Background(), 42, 7)

	require.NoError(t, err)
	require.NotNil(t, key)
	require.Equal(t, PlaygroundChatAPIKeyName, key.Name)
	require.True(t, lookup.ensureCalled)
	require.True(t, lookup.created)
}

func TestPawChatServiceRejectsExhaustedInternalKey(t *testing.T) {
	svc := newPawChatTestService(&pawChatKeySourceStub{apiKey: &APIKey{
		ID:        99,
		UserID:    42,
		Status:    StatusAPIKeyQuotaExhausted,
		Quota:     1,
		QuotaUsed: 1,
		User:      &User{ID: 42, Status: StatusActive},
	}})

	_, err := svc.Prepare(context.Background(), 42, PawChatRequest{
		GroupID:  7,
		ModelID:  "gpt-5",
		Messages: []PawChatMessage{{Role: "user", Content: "hello"}},
	})

	require.Error(t, err)
	require.Equal(t, "QUOTA_EXCEEDED", infraerrors.Reason(err))
}
