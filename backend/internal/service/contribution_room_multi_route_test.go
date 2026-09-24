package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type contributionRoomRouteAccountRepoStub struct {
	AccountRepository
	accounts       map[int64]*Account
	groupAccounts  map[int64][]Account
	requestedGroup int64
}

func (r *contributionRoomRouteAccountRepoStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	result := make([]*Account, 0, len(ids))
	for _, id := range ids {
		if account := r.accounts[id]; account != nil {
			result = append(result, account)
		}
	}
	return result, nil
}

func (r *contributionRoomRouteAccountRepoStub) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, _ string) ([]Account, error) {
	r.requestedGroup = groupID
	return append([]Account(nil), r.groupAccounts[groupID]...), nil
}

func (r *contributionRoomRouteAccountRepoStub) ListSchedulableByGroupIDAndPlatforms(_ context.Context, groupID int64, _ []string) ([]Account, error) {
	r.requestedGroup = groupID
	return append([]Account(nil), r.groupAccounts[groupID]...), nil
}

type contributionRoomRouteRepoStub struct {
	route             *ContributionRoomRoute
	requestedUserID   int64
	requestedAPIKeyID int64
}

func (r *contributionRoomRouteRepoStub) ResolveRouteForAPIKey(_ context.Context, userID, apiKeyID int64) (*ContributionRoomRoute, error) {
	r.requestedUserID = userID
	r.requestedAPIKeyID = apiKeyID
	return r.route, nil
}

func TestApplyContributionRoomRoutingKeepsEachRoomMultiplier(t *testing.T) {
	firstRate, secondRate := 1.25, 2.5
	first := &Account{ID: 101, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Concurrency: 5}
	second := &Account{ID: 202, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Concurrency: 6}
	adminFallback := Account{
		ID:          303,
		Platform:    PlatformOpenAI,
		Status:      StatusActive,
		Schedulable: true,
	}
	contributedDefault := Account{
		ID: 404, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{
			AccountContributionSourceKey: AccountContributionSourceValue,
			AccountContributorUserIDKey:  float64(88),
		},
	}
	fallbackGroupID := int64(909)
	accountRepo := &contributionRoomRouteAccountRepoStub{
		accounts: map[int64]*Account{
			first.ID: first, second.ID: second,
		},
		groupAccounts: map[int64][]Account{
			fallbackGroupID: {adminFallback, contributedDefault},
		},
	}
	routeRepo := &contributionRoomRouteRepoStub{route: &ContributionRoomRoute{
		Rooms: []ContributionRoomRouteRoom{
			{RoomID: 11, ConsumerRateMultiplier: firstRate, AccountIDs: []int64{first.ID}, AccountConcurrencies: map[int64]int{first.ID: 2}},
			{RoomID: 22, ConsumerRateMultiplier: secondRate, AccountIDs: []int64{second.ID}, AccountConcurrencies: map[int64]int{second.ID: 3}},
		},
		AllowPoolFallback: true,
		FallbackGroupID:   &fallbackGroupID,
	}}
	service := &GatewayService{
		accountRepo:          accountRepo,
		contributionRoomRepo: routeRepo,
	}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(7))
	ctx = context.WithValue(ctx, ctxkey.APIKeyID, int64(70))

	accounts, err := service.applyContributionRoomRouting(ctx, []Account{adminFallback, contributedDefault}, nil, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Len(t, accounts, 3)
	byID := map[int64]Account{}
	for _, account := range accounts {
		byID[account.ID] = account
	}
	require.Equal(t, int64(11), byID[first.ID].ContributionRoomID)
	require.Equal(t, firstRate, *byID[first.ID].ContributionRateMultiplierOverride)
	require.Equal(t, int64(22), byID[second.ID].ContributionRoomID)
	require.Equal(t, secondRate, *byID[second.ID].ContributionRateMultiplierOverride)
	require.Equal(t, 2, byID[first.ID].Concurrency)
	require.Equal(t, 2, *byID[first.ID].ContributionConcurrencyOverride)
	require.Equal(t, 3, byID[second.ID].Concurrency)
	require.Equal(t, 3, *byID[second.ID].ContributionConcurrencyOverride)
	require.Empty(t, byID[adminFallback.ID].ContributionRouteSource)
	require.Equal(t, -1<<30, byID[first.ID].Priority)
	require.Equal(t, -1<<30, byID[second.ID].Priority)
	require.Greater(t, byID[adminFallback.ID].Priority, byID[first.ID].Priority)
	require.NotContains(t, byID, contributedDefault.ID)
	require.Equal(t, fallbackGroupID, accountRepo.requestedGroup)
	require.Equal(t, int64(7), routeRepo.requestedUserID)
	require.Equal(t, int64(70), routeRepo.requestedAPIKeyID)
}

func TestApplyOpenAIContributionRoomRoutingFallsBackToExplicitAdminGroup(t *testing.T) {
	adminAccount := Account{
		ID: 501, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
	}
	wrongDefaultAccount := Account{
		ID: 503, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
	}
	contributedAccount := Account{
		ID: 502, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{
			AccountContributionSourceKey: AccountContributionSourceValue,
			AccountContributorUserIDKey:  float64(88),
		},
	}
	fallbackGroupID := int64(606)
	accountRepo := &contributionRoomRouteAccountRepoStub{
		groupAccounts: map[int64][]Account{
			fallbackGroupID: {adminAccount, contributedAccount},
		},
	}
	routeRepo := &contributionRoomRouteRepoStub{route: &ContributionRoomRoute{
		ExplicitlySelected: true,
		AllowPoolFallback:  true,
		FallbackGroupID:    &fallbackGroupID,
	}}
	service := &OpenAIGatewayService{
		accountRepo:          accountRepo,
		contributionRoomRepo: routeRepo,
	}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(7))
	ctx = context.WithValue(ctx, ctxkey.APIKeyID, int64(70))

	accounts, err := service.applyContributionRoomRouting(ctx, []Account{wrongDefaultAccount}, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, []Account{adminAccount}, accounts)
	require.Empty(t, accounts[0].ContributionRouteSource, "admin group fallback must keep normal group billing")
	require.Equal(t, fallbackGroupID, accountRepo.requestedGroup)
}

func TestApplyContributionRoomRoutingExplicitUnavailableRoomStaysIsolated(t *testing.T) {
	defaultAccount := Account{
		ID:          404,
		Platform:    PlatformOpenAI,
		Status:      StatusActive,
		Schedulable: true,
	}
	routeRepo := &contributionRoomRouteRepoStub{route: &ContributionRoomRoute{
		ExplicitlySelected: true,
		AllowPoolFallback:  false,
	}}
	accountRepo := &contributionRoomRouteAccountRepoStub{}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(7))
	ctx = context.WithValue(ctx, ctxkey.APIKeyID, int64(70))

	generic := &GatewayService{accountRepo: accountRepo, contributionRoomRepo: routeRepo}
	accounts, err := generic.applyContributionRoomRouting(ctx, []Account{defaultAccount}, nil, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Empty(t, accounts)

	openAI := &OpenAIGatewayService{accountRepo: accountRepo, contributionRoomRepo: routeRepo}
	accounts, err = openAI.applyContributionRoomRouting(ctx, []Account{defaultAccount}, PlatformOpenAI)
	require.NoError(t, err)
	require.Empty(t, accounts)
}
