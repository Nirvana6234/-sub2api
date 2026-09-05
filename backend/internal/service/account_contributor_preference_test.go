package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestPreferContributorAccounts(t *testing.T) {
	shared := &Account{ID: 1}
	owned := &Account{ID: 2, Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(42),
	}}
	other := &Account{ID: 3, Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(7),
	}}

	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
	require.Equal(t, []*Account{owned, shared}, prioritizeContributorAccounts(ctx, []*Account{shared, owned, other}))
	require.Equal(t, []*Account{shared}, prioritizeContributorAccounts(context.Background(), []*Account{shared, other}))
	require.True(t, hasContributorAccounts(ctx, []*Account{shared, owned, other}))

	ownerOnlyCtx := context.WithValue(ctx, ctxkey.OwnContributedAccountsOnly, true)
	require.Equal(t, []*Account{owned}, prioritizeContributorAccounts(ownerOnlyCtx, []*Account{shared, owned, other}))

	creditOnlyCtx := context.WithValue(ctx, ctxkey.ContributionCreditOnly, true)
	require.Equal(t, []*Account{owned}, prioritizeContributorAccounts(creditOnlyCtx, []*Account{shared, owned, other}))
}

func TestOpenAIImplicitOwnAccountsReorderOnlyTheCurrentGroupCandidates(t *testing.T) {
	shared := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	owned := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(42),
	}}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))

	accounts, err := (&OpenAIGatewayService{}).prependImplicitOwnContributionAccounts(ctx, []Account{shared, owned}, PlatformOpenAI)

	require.NoError(t, err)
	require.Equal(t, int64(2), accounts[0].ID)
	require.Equal(t, int64(1), accounts[1].ID)
}

func TestGenericImplicitOwnAccountsReorderOnlyTheCurrentGroupCandidates(t *testing.T) {
	shared := Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	owned := Account{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(42),
	}}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))

	accounts, err := (&GatewayService{}).prependImplicitOwnContributionAccounts(ctx, []Account{shared, owned}, PlatformAnthropic, false)

	require.NoError(t, err)
	require.Equal(t, int64(2), accounts[0].ID)
	require.Equal(t, int64(1), accounts[1].ID)
}

func TestWorkspaceOwnedPreferencePreservesStoredAccountOrder(t *testing.T) {
	first := Account{ID: 1, Priority: 2}
	second := Account{ID: 2, Priority: 9}

	preferWorkspaceOwnedAccount(&first)
	preferWorkspaceOwnedAccount(&second)

	require.Less(t, first.Priority, second.Priority)
	require.Less(t, second.Priority, 0)
}

func TestContributorAccountsFallBackToPoolOnlyAfterOwnCandidatesAreUnavailable(t *testing.T) {
	owned := &Account{ID: 1, Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(42),
	}}
	pool := &Account{ID: 2}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))

	withOwnedCandidate := filterByContributorPreference(ctx, []accountWithLoad{
		{account: owned},
		{account: pool},
	})
	require.Len(t, withOwnedCandidate, 1)
	require.Equal(t, int64(1), withOwnedCandidate[0].account.ID)

	// Quota exhaustion and full concurrency remove an account before this
	// preference stage. Once every owned candidate has been removed, the pool
	// is the remaining eligible fallback.
	withOwnedQuotaExhausted := filterByContributorPreference(ctx, []accountWithLoad{{account: pool}})
	require.Len(t, withOwnedQuotaExhausted, 1)
	require.Equal(t, int64(2), withOwnedQuotaExhausted[0].account.ID)
}

func TestContributedPoolAccountIsAvailableAndGovernanceCanPauseIt(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	account := &Account{Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(7),
		AccountShareModeKey:          AccountShareModePool,
		AccountShareTotalBudgetKey:   5.0,
		AccountShareDailyBudgetKey:   1.0,
		AccountShareExpiresAtKey:     now.Add(time.Hour).Format(time.RFC3339),
	}}
	require.True(t, account.IsSharedPoolAvailableTo(7, now))
	require.True(t, account.IsSharedPoolAvailableTo(8, now))
	account.Extra[AccountContributionGovernanceStateKey] = AccountContributionGovernancePaused
	require.False(t, account.IsSharedPoolAvailableTo(8, now))

	adminPoolAccount := &Account{Extra: map[string]any{AccountShareModeKey: AccountShareModePool}}
	require.True(t, adminPoolAccount.IsSharedPoolAvailableTo(8, now))
}

func TestPoolAccountIsReachableByItsGroupScheduler(t *testing.T) {
	private := &Account{ID: 1, Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(7),
	}}
	pool := &Account{ID: 2, Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(7),
		AccountShareModeKey:          AccountShareModePool,
	}}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(8))

	require.Equal(t, []*Account{pool}, filterContributorAccountAccess(ctx, 8, false, []*Account{private, pool}))
}

func TestAnthropicContributionPoolAccountsAppendOnlyWhenGroupAllows(t *testing.T) {
	defaultAccount := Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	adminPoolAccount := Account{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		AccountShareModeKey: AccountShareModePool,
	}}
	userPoolAccount := Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(7),
		AccountShareModeKey:          AccountShareModePool,
	}}
	grokPoolAccount := Account{ID: 4, Platform: PlatformGrok, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		AccountShareModeKey: AccountShareModePool,
	}}
	groupID := int64(10)
	repo := contributionPoolAccountRepoStub{accounts: []Account{adminPoolAccount, userPoolAccount, grokPoolAccount}}
	svc := &GatewayService{accountRepo: repo}

	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
		ID:                    groupID,
		Platform:              PlatformAnthropic,
		Status:                StatusActive,
		Hydrated:              true,
		AllowContributionPool: true,
	})

	accounts, err := svc.applyContributionRoomRouting(ctx, []Account{defaultAccount}, &groupID, PlatformAnthropic, true)

	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, accountIDs(accounts))
	require.Empty(t, accounts[1].ContributionRouteSource, "administrator pool accounts keep normal group billing")

	ctx = context.WithValue(context.Background(), ctxkey.Group, &Group{
		ID:                    groupID,
		Platform:              PlatformAnthropic,
		Status:                StatusActive,
		Hydrated:              true,
		AllowContributionPool: false,
	})
	accounts, err = svc.applyContributionRoomRouting(ctx, []Account{defaultAccount}, &groupID, PlatformAnthropic, true)

	require.NoError(t, err)
	require.Equal(t, []int64{1}, accountIDs(accounts))
}

func TestContributorUserIDRequiresContributionSource(t *testing.T) {
	account := &Account{Extra: map[string]any{AccountContributorUserIDKey: float64(42)}}
	require.Zero(t, account.ContributorUserID())
	account.Extra[AccountContributionSourceKey] = AccountContributionSourceValue
	require.Equal(t, int64(42), account.ContributorUserID())
}

type contributionPoolAccountRepoStub struct {
	AccountRepository
	accounts []Account
}

func (r contributionPoolAccountRepoStub) ListSchedulableByPlatforms(_ context.Context, platforms []string) ([]Account, error) {
	allowed := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		allowed[platform] = struct{}{}
	}
	var result []Account
	for _, account := range r.accounts {
		if _, ok := allowed[account.Platform]; ok {
			result = append(result, account)
		}
	}
	return result, nil
}

func (r contributionPoolAccountRepoStub) ListSchedulableByPlatform(_ context.Context, platform string) ([]Account, error) {
	var result []Account
	for _, account := range r.accounts {
		if account.Platform == platform {
			result = append(result, account)
		}
	}
	return result, nil
}

func accountIDs(accounts []Account) []int64 {
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	return ids
}
