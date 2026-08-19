package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountContributionConsumerRateMultiplier_UsesOnlyRouteSnapshot(t *testing.T) {
	roomRate := 1.75
	room := &Account{
		ContributionRouteSource:            ContributionRouteSourceRoom,
		ContributionRoomID:                 9,
		ContributionRateMultiplierOverride: &roomRate,
	}
	rate, routed := room.ContributionConsumerRateMultiplier()
	require.True(t, routed)
	require.Equal(t, 1.75, rate)

	legacyPool := &Account{ContributionRouteSource: ContributionRouteSourcePool}
	rate, routed = legacyPool.ContributionConsumerRateMultiplier()
	require.True(t, routed)
	require.Equal(t, 1.0, rate, "an existing public pool without a configured multiplier must remain billable")

	pool := &Account{
		ContributionRouteSource: ContributionRouteSourcePool,
		Extra: map[string]any{
			AccountShareConsumerRateMultiplierKey: 2.25,
		},
	}
	rate, routed = pool.ContributionConsumerRateMultiplier()
	require.True(t, routed)
	require.Equal(t, 2.25, rate)

	normal := &Account{}
	_, routed = normal.ContributionConsumerRateMultiplier()
	require.False(t, routed)

	groupPool := &Account{Extra: map[string]any{AccountShareModeKey: AccountShareModePool}}
	_, routed = groupPool.ContributionConsumerRateMultiplier()
	require.False(t, routed, "administrator-pool accounts must use the selected API key group multiplier")
}

func TestPreserveContributionRouteMetadata_PreservesRoomRateAcrossHydration(t *testing.T) {
	rate := 1.4
	concurrency := 2
	selected := &Account{
		ID:                                 8,
		ContributionRouteSource:            ContributionRouteSourceRoom,
		ContributionRoomID:                 3,
		ContributionRateMultiplierOverride: &rate,
		ContributionConcurrencyOverride:    &concurrency,
	}
	hydrated := preserveContributionRouteMetadata(selected, &Account{ID: selected.ID, Name: "fresh", Concurrency: 5})
	require.Equal(t, ContributionRouteSourceRoom, hydrated.ContributionRouteSource)
	require.Equal(t, int64(3), hydrated.ContributionRoomID)
	got, routed := hydrated.ContributionConsumerRateMultiplier()
	require.True(t, routed)
	require.Equal(t, 1.4, got)
	require.Equal(t, 2, hydrated.Concurrency)
	require.Equal(t, 2, *hydrated.ContributionConcurrencyOverride)
}

func TestRecheckSelectedOpenAIAccountFromDB_PreservesContributionRoomRoute(t *testing.T) {
	rate := 1.4
	fresh := Account{
		ID:          8,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			AccountContributionSourceKey: AccountContributionSourceValue,
			AccountContributorUserIDKey:  float64(77),
		},
	}
	selected := cloneContributionRouteAccount(fresh, ContributionRouteSourceRoom, 3, rate)
	svc := &OpenAIGatewayService{
		accountRepo:       stubOpenAIAccountRepo{accounts: []Account{fresh}},
		schedulerSnapshot: &SchedulerSnapshotService{},
	}

	got := svc.recheckSelectedOpenAIAccountFromDB(
		context.Background(),
		&selected,
		nil,
		PlatformOpenAI,
		"gpt-5.4",
		false,
		OpenAIEndpointCapability(""),
	)

	require.NotNil(t, got)
	require.Equal(t, ContributionRouteSourceRoom, got.ContributionRouteSource)
	require.Equal(t, int64(3), got.ContributionRoomID)
	gotRate, routed := got.ContributionConsumerRateMultiplier()
	require.True(t, routed)
	require.Equal(t, rate, gotRate)
	require.Equal(t, int64(77), got.ContributorUserID())
}

func TestResolveFreshSchedulableOpenAIAccount_PreservesContributionRoomRoute(t *testing.T) {
	rate := 1.4
	fresh := &Account{
		ID:          8,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
	}
	selected := cloneContributionRouteAccount(*fresh, ContributionRouteSourceRoom, 3, rate)
	svc := &OpenAIGatewayService{
		schedulerSnapshot: NewSchedulerSnapshotService(&openAISnapshotCacheStub{
			accountsByID: map[int64]*Account{fresh.ID: fresh},
		}, nil, nil, nil, nil),
	}

	got := svc.resolveFreshSchedulableOpenAIAccount(
		context.Background(),
		&selected,
		PlatformOpenAI,
		"gpt-5.4",
		false,
		OpenAIEndpointCapability(""),
	)

	require.NotNil(t, got)
	require.Equal(t, ContributionRouteSourceRoom, got.ContributionRouteSource)
	require.Equal(t, int64(3), got.ContributionRoomID)
	gotRate, routed := got.ContributionConsumerRateMultiplier()
	require.True(t, routed)
	require.Equal(t, rate, gotRate)
}
