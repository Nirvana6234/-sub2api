package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

const (
	privateScopeOwnerID = int64(3)
	privateScopeOtherID = int64(4)
)

func privateScopeContribution(id int64, shareMode string) Account {
	extra := map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(privateScopeOwnerID),
	}
	if shareMode != "" {
		extra[AccountShareModeKey] = shareMode
	}
	return Account{
		ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 30, Extra: extra,
	}
}

func privateScopeCtx(userID int64) context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, userID)
	return context.WithValue(ctx, ctxkey.APIKeyID, userID*10)
}

type privateScopeAccountRepoStub struct {
	contributionRoomRouteAccountRepoStub
}

func (r *privateScopeAccountRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	if account := r.accounts[id]; account != nil {
		clone := *account
		return &clone, nil
	}
	return nil, nil
}

func TestAccountIsContributionAvailableTo(t *testing.T) {
	private := privateScopeContribution(795, AccountShareModePrivate)
	pool := privateScopeContribution(796, AccountShareModePool)
	pausedPool := privateScopeContribution(797, AccountShareModePool)
	pausedPool.Extra[AccountContributionGovernanceStateKey] = AccountContributionGovernancePaused
	roomRouted := cloneContributionRouteAccount(private, ContributionRouteSourceRoom, 11, 1)
	admin := Account{ID: 1, Platform: PlatformOpenAI}

	require.True(t, private.IsContributionAvailableTo(privateScopeOwnerID))
	require.False(t, private.IsContributionAvailableTo(privateScopeOtherID))
	require.False(t, private.IsContributionAvailableTo(0), "a caller without identity never reaches a private contribution")
	require.True(t, pool.IsContributionAvailableTo(privateScopeOtherID))
	require.False(t, pausedPool.IsContributionAvailableTo(privateScopeOtherID))
	require.True(t, pausedPool.IsContributionAvailableTo(privateScopeOwnerID))
	require.True(t, roomRouted.IsContributionAvailableTo(privateScopeOtherID))
	require.True(t, admin.IsContributionAvailableTo(privateScopeOtherID))
}

func TestFilterContributionAccountsForUserKeepsOrderAndSkipsCopyWhenNothingRemoved(t *testing.T) {
	admin := Account{ID: 1, Platform: PlatformOpenAI}
	private := privateScopeContribution(795, AccountShareModePrivate)
	pool := privateScopeContribution(796, AccountShareModePool)

	input := []Account{admin, pool}
	unchanged := filterContributionAccountsForUser(privateScopeOtherID, input)
	require.Same(t, &input[0], &unchanged[0])

	filtered := filterContributionAccountsForUser(privateScopeOtherID, []Account{admin, private, pool})
	require.Equal(t, []int64{1, 796}, privateScopeAccountIDs(filtered))
}

func TestOpenAIImplicitRouteHidesOtherUsersPrivateContribution(t *testing.T) {
	admin := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	private := privateScopeContribution(795, AccountShareModePrivate)
	routeRepo := &contributionRoomRouteRepoStub{route: &ContributionRoomRoute{}}
	service := &OpenAIGatewayService{
		accountRepo:          &contributionRoomRouteAccountRepoStub{},
		contributionRoomRepo: routeRepo,
	}

	accounts, err := service.applyContributionRoomRouting(privateScopeCtx(privateScopeOtherID), []Account{admin, private}, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, privateScopeAccountIDs(accounts))

	accounts, err = service.applyContributionRoomRouting(privateScopeCtx(privateScopeOwnerID), []Account{admin, private}, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, []int64{795, 1}, privateScopeAccountIDs(accounts), "the contributor still reaches and prefers their own account")

	withoutRoomRepo := &OpenAIGatewayService{}
	accounts, err = withoutRoomRepo.applyContributionRoomRouting(privateScopeCtx(privateScopeOtherID), []Account{admin, private}, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, privateScopeAccountIDs(accounts))
}

func TestGatewayImplicitRouteHidesOtherUsersPrivateContribution(t *testing.T) {
	admin := Account{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	private := privateScopeContribution(795, AccountShareModePrivate)
	service := &GatewayService{
		accountRepo:          &contributionRoomRouteAccountRepoStub{},
		contributionRoomRepo: &contributionRoomRouteRepoStub{route: &ContributionRoomRoute{}},
	}

	accounts, err := service.applyContributionRoomRouting(privateScopeCtx(privateScopeOtherID), []Account{admin, private}, nil, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, privateScopeAccountIDs(accounts))

	accounts, err = service.applyContributionRoomRouting(privateScopeCtx(privateScopeOwnerID), []Account{admin, private}, nil, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Equal(t, []int64{795, 1}, privateScopeAccountIDs(accounts))

	withoutRoomRepo := &GatewayService{}
	accounts, err = withoutRoomRepo.applyContributionRoomRouting(privateScopeCtx(privateScopeOtherID), []Account{admin, private}, nil, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, privateScopeAccountIDs(accounts))
}

func TestOpenAIStickyLookupRejectsOtherUsersPrivateContribution(t *testing.T) {
	private := privateScopeContribution(795, AccountShareModePrivate)
	accountRepo := &privateScopeAccountRepoStub{contributionRoomRouteAccountRepoStub{
		accounts: map[int64]*Account{private.ID: &private},
	}}
	implicit := &contributionRoomRouteRepoStub{route: &ContributionRoomRoute{}}
	service := &OpenAIGatewayService{accountRepo: accountRepo, contributionRoomRepo: implicit}

	account, err := service.getSchedulableAccount(privateScopeCtx(privateScopeOtherID), private.ID)
	require.NoError(t, err)
	require.Nil(t, account, "a sticky or previous_response binding must not reuse another user's private account")

	account, err = service.getSchedulableAccount(privateScopeCtx(privateScopeOwnerID), private.ID)
	require.NoError(t, err)
	require.NotNil(t, account)

	roomMember := &OpenAIGatewayService{
		accountRepo:          accountRepo,
		contributionRoomRepo: &contributionRoomRouteRepoStub{route: &ContributionRoomRoute{ExplicitlySelected: true}},
	}
	account, err = roomMember.getSchedulableAccount(privateScopeCtx(privateScopeOtherID), private.ID)
	require.NoError(t, err)
	require.NotNil(t, account, "a room member reloading a room account must not be rejected")
}

func TestGatewayStickyLookupRejectsOtherUsersPrivateContribution(t *testing.T) {
	private := privateScopeContribution(795, AccountShareModePrivate)
	accountRepo := &privateScopeAccountRepoStub{contributionRoomRouteAccountRepoStub{
		accounts: map[int64]*Account{private.ID: &private},
	}}
	service := &GatewayService{
		accountRepo:          accountRepo,
		contributionRoomRepo: &contributionRoomRouteRepoStub{route: &ContributionRoomRoute{}},
	}

	account, err := service.getSchedulableAccount(privateScopeCtx(privateScopeOtherID), private.ID)
	require.NoError(t, err)
	require.Nil(t, account)

	account, err = service.getSchedulableAccount(privateScopeCtx(privateScopeOwnerID), private.ID)
	require.NoError(t, err)
	require.NotNil(t, account)
}

func TestGeminiCompatHidesOtherUsersPrivateContribution(t *testing.T) {
	admin := Account{ID: 1, Platform: PlatformGemini, Status: StatusActive, Schedulable: true}
	private := privateScopeContribution(795, AccountShareModePrivate)
	private.Platform = PlatformGemini
	groupID := int64(3)
	accountRepo := &privateScopeAccountRepoStub{contributionRoomRouteAccountRepoStub{
		accounts:      map[int64]*Account{private.ID: &private},
		groupAccounts: map[int64][]Account{groupID: {admin, private}},
	}}
	service := &GeminiMessagesCompatService{accountRepo: accountRepo}

	accounts, err := service.listSchedulableAccountsOnce(privateScopeCtx(privateScopeOtherID), &groupID, PlatformGemini, true)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, privateScopeAccountIDs(accounts))

	accounts, err = service.listSchedulableAccountsOnce(privateScopeCtx(privateScopeOwnerID), &groupID, PlatformGemini, true)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 795}, privateScopeAccountIDs(accounts))

	account, err := service.getSchedulableAccount(privateScopeCtx(privateScopeOtherID), private.ID)
	require.NoError(t, err)
	require.Nil(t, account)
}

func privateScopeAccountIDs(accounts []Account) []int64 {
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].ID)
	}
	return ids
}
