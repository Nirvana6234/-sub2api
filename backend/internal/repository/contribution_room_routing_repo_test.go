package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestContributionRoomRouteExcludesUnschedulableAccounts(t *testing.T) {
	ctx := context.Background()
	client := newContributionRoomRoutingTestClient(t)
	owner := createContributionRouteUser(t, client, "route-owner@example.com")
	consumer := createContributionRouteUser(t, client, "route-consumer@example.com")
	apiKey := client.APIKey.Create().SetUserID(consumer.ID).SetKey("sk-route-consumer").SetName("route-consumer").SetStatus(service.StatusAPIKeyActive).SaveX(ctx)
	room := client.ContributionRoom.Create().
		SetOwnerUserID(owner.ID).SetName("route-room").SetConsumerRateMultiplier(1.4).
		SetStatus("active").SetVisibility("public").SaveX(ctx)
	client.UserContributionRoomPreference.Create().
		SetUserID(consumer.ID).SetAPIKeyID(apiKey.ID).SetRoomID(room.ID).
		SetAllowPoolFallback(false).SaveX(ctx)

	ready := createContributionRouteAccount(t, client, owner.ID, "ready", true)
	blocked := createContributionRouteAccount(t, client, owner.ID, "blocked", false)
	for _, item := range []struct {
		account          *dbent.Account
		shareConcurrency int
	}{
		{account: ready, shareConcurrency: 2},
		{account: blocked, shareConcurrency: 7},
	} {
		verifiedAt := time.Now().UTC()
		client.ContributionAccountVerification.Create().
			SetAccountID(item.account.ID).SetPlatform(service.PlatformOpenAI).
			SetStatus(service.ContributionVerificationStatusVerified).SetModelFamily("gpt").
			SetTestedAt(verifiedAt).SaveX(ctx)
		client.ContributionRoomAccount.Create().
			SetRoomID(room.ID).SetAccountID(item.account.ID).SetEnabled(true).
			SetShareConcurrency(item.shareConcurrency).SetShareBudgetUsd(5).SetVerifiedAt(verifiedAt).SaveX(ctx)
	}

	route, err := NewContributionRoomRoutingRepository(client).ResolveRouteForAPIKey(ctx, consumer.ID, apiKey.ID)
	require.NoError(t, err)
	require.NotNil(t, route)
	require.True(t, route.IsExplicitSelection())
	require.Len(t, route.Rooms, 1)
	require.Equal(t, []int64{ready.ID}, route.Rooms[0].AccountIDs)
	require.Equal(t, 2, route.Rooms[0].AccountConcurrencies[ready.ID])
	_, foundBlocked := route.Rooms[0].AccountConcurrencies[blocked.ID]
	require.False(t, foundBlocked)
}

func TestNormalGroupSchedulingExcludesContributionRoomMembers(t *testing.T) {
	ctx := context.Background()
	client := newContributionRoomRoutingTestClient(t)
	owner := createContributionRouteUser(t, client, "group-owner@example.com")
	group := client.Group.Create().SetName("normal-group").SaveX(ctx)
	room := client.ContributionRoom.Create().
		SetOwnerUserID(owner.ID).SetName("room").SetConsumerRateMultiplier(1).
		SetStatus("active").SetVisibility("public").SaveX(ctx)

	roomAccount := createContributionRouteAccount(t, client, owner.ID, "room-account", true)
	normalAccount := createContributionRouteAccount(t, client, owner.ID, "normal-account", true)
	for _, account := range []*dbent.Account{roomAccount, normalAccount} {
		client.AccountGroup.Create().SetAccountID(account.ID).SetGroupID(group.ID).SetPriority(1).SaveX(ctx)
	}
	client.ContributionRoomAccount.Create().
		SetRoomID(room.ID).SetAccountID(roomAccount.ID).SetEnabled(true).
		SetShareConcurrency(1).SetShareBudgetUsd(5).SetVerifiedAt(time.Now().UTC()).SaveX(ctx)

	repo := NewAccountRepository(client, nil, nil)
	accounts, err := repo.(*accountRepository).queryAccountsByGroup(ctx, group.ID, accountGroupQueryOptions{
		status:               service.StatusActive,
		schedulable:          true,
		ignoreTransientState: true,
	})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, normalAccount.ID, accounts[0].ID)
}

func newContributionRoomRoutingTestClient(t *testing.T) *dbent.Client {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:contribution_room_route_%d?mode=memory&cache=shared", time.Now().UnixNano()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func createContributionRouteUser(t *testing.T, client *dbent.Client, email string) *dbent.User {
	t.Helper()
	return client.User.Create().SetEmail(email).SetUsername(email).SetPasswordHash("test-hash").SaveX(context.Background())
}

func createContributionRouteAccount(t *testing.T, client *dbent.Client, ownerID int64, name string, schedulable bool) *dbent.Account {
	t.Helper()
	return client.Account.Create().
		SetName(name).SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeAPIKey).
		SetCredentials(map[string]any{"api_key": "test-key"}).
		SetExtra(map[string]any{service.AccountContributionSourceKey: service.AccountContributionSourceValue, service.AccountContributorUserIDKey: ownerID}).
		SetSchedulable(schedulable).SaveX(context.Background())
}
