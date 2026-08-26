package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type anthropicFallbackAccountRepo struct {
	AccountRepository
	byGroup map[int64][]Account
}

func (r *anthropicFallbackAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, _ string) ([]Account, error) {
	return append([]Account(nil), r.byGroup[groupID]...), nil
}

func (r *anthropicFallbackAccountRepo) ListSchedulableByGroupIDAndPlatforms(_ context.Context, groupID int64, _ []string) ([]Account, error) {
	return append([]Account(nil), r.byGroup[groupID]...), nil
}

type anthropicFallbackGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *anthropicFallbackGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	group := r.groups[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	return group, nil
}

func (r *anthropicFallbackGroupRepo) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	group := r.groups[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	return group, nil
}

func TestGatewayAnthropicFallbackPoolSelectsTargetAccount(t *testing.T) {
	t.Parallel()

	sourceID, fallbackID := int64(100), int64(200)
	source := &Group{
		ID:              sourceID,
		Platform:        PlatformAnthropic,
		Status:          StatusActive,
		FallbackGroupID: &fallbackID,
	}
	fallback := &Group{
		ID:             fallbackID,
		Platform:       PlatformAnthropic,
		Status:         StatusActive,
		IsFallbackPool: true,
	}
	accountRepo := &anthropicFallbackAccountRepo{
		byGroup: map[int64][]Account{
			sourceID:   nil,
			fallbackID: {{ID: 901, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}},
		},
	}
	groupRepo := &anthropicFallbackGroupRepo{
		groups: map[int64]*Group{sourceID: source, fallbackID: fallback},
	}
	svc := &GatewayService{
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	account, err := svc.SelectAccountForModelWithExclusions(
		context.Background(),
		&sourceID,
		"",
		"",
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(901), account.ID)
}

func TestGatewayAnthropicFallbackPoolStopsCycle(t *testing.T) {
	t.Parallel()

	sourceID, fallbackID := int64(110), int64(210)
	source := &Group{
		ID:              sourceID,
		Platform:        PlatformAnthropic,
		Status:          StatusActive,
		FallbackGroupID: &fallbackID,
	}
	fallback := &Group{
		ID:              fallbackID,
		Platform:        PlatformAnthropic,
		Status:          StatusActive,
		IsFallbackPool:  true,
		FallbackGroupID: &sourceID,
	}
	accountRepo := &anthropicFallbackAccountRepo{
		byGroup: map[int64][]Account{sourceID: nil, fallbackID: nil},
	}
	groupRepo := &anthropicFallbackGroupRepo{
		groups: map[int64]*Group{sourceID: source, fallbackID: fallback},
	}
	svc := &GatewayService{
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	_, err := svc.SelectAccountForModelWithExclusions(
		context.Background(),
		&sourceID,
		"",
		"",
		nil,
	)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestGatewayAnthropicFallbackPoolLoadAwareEntrySelectsTargetAccount(t *testing.T) {
	t.Parallel()

	sourceID, fallbackID := int64(120), int64(220)
	source := &Group{
		ID:              sourceID,
		Platform:        PlatformAnthropic,
		Status:          StatusActive,
		FallbackGroupID: &fallbackID,
	}
	fallback := &Group{
		ID:             fallbackID,
		Platform:       PlatformAnthropic,
		Status:         StatusActive,
		IsFallbackPool: true,
	}
	accountRepo := &anthropicFallbackAccountRepo{
		byGroup: map[int64][]Account{
			sourceID:   nil,
			fallbackID: {{ID: 902, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}},
		},
	}
	groupRepo := &anthropicFallbackGroupRepo{
		groups: map[int64]*Group{sourceID: source, fallbackID: fallback},
	}
	svc := &GatewayService{
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	result, err := svc.SelectAccountWithLoadAwareness(
		context.Background(),
		&sourceID,
		"",
		"",
		nil,
		"",
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Account)
	require.Equal(t, int64(902), result.Account.ID)
}
