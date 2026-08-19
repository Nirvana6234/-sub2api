package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceLocalFallbackPrefersOwnedAccountsBeforeManagedUpstream(t *testing.T) {
	const userID int64 = 88
	personal := Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Priority: 5000,
		Extra: map[string]any{
			AccountContributionSourceKey: AccountContributionSourceValue,
			AccountContributorUserIDKey:  float64(userID),
		},
	}
	otherUser := personal
	otherUser.ID = 2
	otherUser.Extra = map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(userID + 1),
	}
	fallback := Account{
		ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Priority: 1000,
	}
	repo := stubOpenAIAccountRepo{accounts: []Account{personal, otherUser}}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, userID)
	ctx = context.WithValue(ctx, ctxkey.WorkspaceLocalFallbackRoute, true)

	openAI := &OpenAIGatewayService{accountRepo: repo}
	accounts, err := openAI.applyContributionRoomRouting(ctx, []Account{fallback}, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, []int64{personal.ID, fallback.ID}, []int64{accounts[0].ID, accounts[1].ID})
	require.Equal(t, (-1<<30)+personal.Priority, accounts[0].Priority)
	require.Equal(t, 1000, accounts[1].Priority)

	generic := &GatewayService{accountRepo: repo}
	accounts, err = generic.applyContributionRoomRouting(ctx, []Account{fallback}, nil, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Equal(t, []int64{personal.ID, fallback.ID}, []int64{accounts[0].ID, accounts[1].ID})
}

func TestWorkspaceLocalFallbackDBRecheckAllowsOnlyCurrentUsersUngroupedAccount(t *testing.T) {
	const userID int64 = 88
	personal := Account{
		ID: 10, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 30,
		Extra: map[string]any{
			AccountContributionSourceKey: AccountContributionSourceValue,
			AccountContributorUserIDKey:  float64(userID),
		},
	}
	otherUser := personal
	otherUser.ID = 11
	otherUser.Extra = map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(userID + 1),
	}
	groupID := int64(9)
	ctx := context.WithValue(context.Background(), ctxkey.UserID, userID)
	ctx = context.WithValue(ctx, ctxkey.WorkspaceLocalFallbackRoute, true)
	svc := &OpenAIGatewayService{
		accountRepo:       stubOpenAIAccountRepo{accounts: []Account{personal, otherUser}},
		cfg:               &config.Config{RunMode: config.RunModeStandard},
		schedulerSnapshot: &SchedulerSnapshotService{},
	}

	rechecked := svc.recheckSelectedOpenAIAccountFromDB(ctx, &personal, &groupID, PlatformOpenAI, "gpt-5.1", false, "")
	require.NotNil(t, rechecked)
	require.Equal(t, personal.ID, rechecked.ID)
	require.Nil(t, svc.recheckSelectedOpenAIAccountFromDB(ctx, &otherUser, &groupID, PlatformOpenAI, "gpt-5.1", false, ""))
}

func TestWorkspaceLocalFallbackLegacySchedulerKeepsPersonalPoolAheadOfCheaperUpstream(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	const userID int64 = 88
	groupID := int64(11)
	personal := upstreamCostTestOAuthAccount(171)
	personal.Status = StatusActive
	personal.Schedulable = true
	personal.Concurrency = 30
	personal.Priority = 5000
	personal.Extra = map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(userID),
	}
	fallback := upstreamCostTestAccount(147, UpstreamBillingProbeStatusOK, 0.001, time.Now().Add(-time.Minute), 30*time.Minute)
	fallback.Status = StatusActive
	fallback.Schedulable = true
	fallback.Concurrency = 30
	fallback.Priority = 0
	fallback.GroupIDs = []int64{groupID}

	repo := schedulerTestOpenAIAccountRepo{accounts: []Account{*personal, *fallback}}
	snapshotCache := &openAISnapshotCacheStub{
		snapshotAccounts: []*Account{fallback},
		accountsByID: map[int64]*Account{
			personal.ID: personal,
			fallback.ID: fallback,
		},
	}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, userID)
	ctx = context.WithValue(ctx, ctxkey.WorkspaceLocalFallbackRoute, true)

	for _, loadBatchEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "load batch disabled", true: "load batch enabled"}[loadBatchEnabled], func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatchEnabled
			settings := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{
				openAIAdvancedSchedulerSettingKey:              "false",
				SettingKeyOpenAILowUpstreamRatePriorityEnabled: "true",
				SettingKeyOpenAIOAuthSchedulingRateMultiplier:  "0.05",
			}}
			svc := &OpenAIGatewayService{
				accountRepo:        repo,
				cache:              &schedulerTestGatewayCache{},
				cfg:                cfg,
				rateLimitService:   &RateLimitService{settingService: NewSettingService(settings, cfg)},
				schedulerSnapshot:  &SchedulerSnapshotService{cache: snapshotCache, accountRepo: repo},
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}

			selection, decision, err := svc.SelectAccountWithSchedulerForCapability(
				ctx,
				&groupID,
				"",
				"",
				"gpt-5.1",
				nil,
				OpenAIUpstreamTransportHTTPSSE,
				OpenAIEndpointCapabilityResponses,
				false,
				false,
				true,
			)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.Equal(t, personal.ID, selection.Account.ID)
			require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}
