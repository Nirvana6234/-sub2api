package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/accountgroup"
	"github.com/Wei-Shaw/sub2api/ent/contributionaccountverification"
	"github.com/Wei-Shaw/sub2api/ent/contributionroomaccount"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/usercontributionroompreference"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestContributionRoomMembershipRequiresVerifiedPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "owner@example.com")
	account := createContributionRoomTestAccount(t, client, owner.ID, service.PlatformOpenAI)
	room := client.ContributionRoom.Create().
		SetOwnerUserID(owner.ID).
		SetName("Owner room").
		SetConsumerRateMultiplier(1.25).
		SetStatus(contributionRoomStatusActive).
		SetVisibility(contributionRoomVisibilityOpen).
		SaveX(ctx)
	h := newContributionRoomTestHandler(owner.ID, account, client)

	c, recorder := contributionRoomTestContext(http.MethodPost, "/account-contributions/room/accounts", owner.ID, fmt.Sprintf(`{"account_id":%d,"share_budget_usd":5,"share_concurrency":2}`, account.ID))
	h.AddOwnRoomAccount(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, client.ContributionRoomAccount.Query().CountX(ctx))

	verification := client.ContributionAccountVerification.Create().
		SetAccountID(account.ID).
		SetPlatform(service.PlatformAnthropic).
		SetStatus(contributionVerificationStatus).
		SetModelFamily("gpt").
		SetTestedAt(time.Now().UTC()).
		SaveX(ctx)
	c, recorder = contributionRoomTestContext(http.MethodPost, "/account-contributions/room/accounts", owner.ID, fmt.Sprintf(`{"account_id":%d,"share_budget_usd":5,"share_concurrency":2}`, account.ID))
	h.AddOwnRoomAccount(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, client.ContributionRoomAccount.Query().CountX(ctx))

	verification.Update().SetPlatform(account.Platform).SaveX(ctx)
	c, recorder = contributionRoomTestContext(http.MethodPost, "/account-contributions/room/accounts", owner.ID, fmt.Sprintf(`{"account_id":%d,"share_budget_usd":5,"share_concurrency":2}`, account.ID))
	h.AddOwnRoomAccount(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, client.ContributionRoomAccount.Query().CountX(ctx))
	require.Equal(t, room.ID, client.ContributionRoomAccount.Query().OnlyX(ctx).RoomID)
}

func TestCreateContributionRoomRequiresVerifiedAccountsAndCreatesMembersAtomically(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "create-room-owner@example.com")
	account := createContributionRoomTestAccount(t, client, owner.ID, service.PlatformOpenAI)
	h := newContributionRoomTestHandler(owner.ID, account, client)
	body := fmt.Sprintf(`{"name":"Ready room","consumer_rate_multiplier":1.2,"accounts":[{"account_id":%d,"share_budget_usd":7.5,"share_concurrency":2}]}`, account.ID)

	c, recorder := contributionRoomTestContext(http.MethodPost, "/account-contributions/room", owner.ID, body)
	h.CreateOwnRoom(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, client.ContributionRoom.Query().CountX(ctx))
	require.Zero(t, client.ContributionRoomAccount.Query().CountX(ctx))

	verifiedAt := time.Now().UTC()
	client.ContributionAccountVerification.Create().
		SetAccountID(account.ID).
		SetPlatform(account.Platform).
		SetStatus(contributionVerificationStatus).
		SetModelFamily("gpt").
		SetTestedAt(verifiedAt).
		SaveX(ctx)
	invalidConcurrencyBody := fmt.Sprintf(`{"name":"Ready room","consumer_rate_multiplier":1.2,"accounts":[{"account_id":%d,"share_budget_usd":7.5,"share_concurrency":4}]}`, account.ID)
	c, recorder = contributionRoomTestContext(http.MethodPost, "/account-contributions/room", owner.ID, invalidConcurrencyBody)
	h.CreateOwnRoom(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, client.ContributionRoom.Query().CountX(ctx))

	c, recorder = contributionRoomTestContext(http.MethodPost, "/account-contributions/room", owner.ID, body)
	h.CreateOwnRoom(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, client.ContributionRoom.Query().CountX(ctx))
	membership := client.ContributionRoomAccount.Query().OnlyX(ctx)
	require.Equal(t, account.ID, membership.AccountID)
	require.InDelta(t, 7.5, membership.ShareBudgetUsd, 0.000001)
	require.Equal(t, 2, membership.ShareConcurrency)
	require.NotNil(t, membership.VerifiedAt)

	var envelope struct {
		Data ContributionRoomView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Accounts, 1)
	require.InDelta(t, 7.5, envelope.Data.Accounts[0].ShareBudgetUSD, 0.000001)
	require.Equal(t, 3, envelope.Data.Accounts[0].Concurrency)
	require.Equal(t, 2, envelope.Data.Accounts[0].ShareConcurrency)
	require.Zero(t, client.AccountGroup.Query().Where(accountgroup.AccountIDEQ(account.ID)).CountX(ctx))
}

func TestAddingAccountToContributionRoomRemovesNormalGroupBindings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "room-group-owner@example.com")
	account := createContributionRoomTestAccount(t, client, owner.ID, service.PlatformOpenAI)
	group := client.Group.Create().SetName("normal-group").SaveX(ctx)
	client.AccountGroup.Create().SetAccountID(account.ID).SetGroupID(group.ID).SetPriority(1).SaveX(ctx)
	client.ContributionAccountVerification.Create().
		SetAccountID(account.ID).
		SetPlatform(account.Platform).
		SetStatus(contributionVerificationStatus).
		SetModelFamily("gpt").
		SetTestedAt(time.Now().UTC()).
		SaveX(ctx)
	client.ContributionRoom.Create().
		SetOwnerUserID(owner.ID).
		SetName("Owner room").
		SetConsumerRateMultiplier(1).
		SetStatus(contributionRoomStatusActive).
		SetVisibility(contributionRoomVisibilityOpen).
		SaveX(ctx)
	h := newContributionRoomTestHandler(owner.ID, account, client)

	c, recorder := contributionRoomTestContext(http.MethodPost, "/account-contributions/room/accounts", owner.ID, fmt.Sprintf(`{"account_id":%d,"share_budget_usd":5,"share_concurrency":2}`, account.ID))
	h.AddOwnRoomAccount(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Zero(t, client.AccountGroup.Query().Where(accountgroup.AccountIDEQ(account.ID)).CountX(ctx))
}

func TestCreateContributionRoomRejectsEmptyAccountSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "empty-room-owner@example.com")
	h := newContributionRoomTestHandler(owner.ID, nil, client)

	c, recorder := contributionRoomTestContext(http.MethodPost, "/account-contributions/room", owner.ID, `{"name":"Empty room","consumer_rate_multiplier":1}`)
	h.CreateOwnRoom(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, client.ContributionRoom.Query().CountX(ctx))
}

func TestContributionRoomCatalogOnlyIncludesActivePublicRoomsAndOmitsCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := newContributionRoomTestClient(t)
	viewer := createContributionRoomTestUser(t, client, "viewer@example.com")
	apiKey := createContributionRoomTestAPIKey(t, client, viewer.ID, "viewer-key")
	publicOwner := createContributionRoomTestUser(t, client, "public@example.com")
	privateOwner := createContributionRoomTestUser(t, client, "private@example.com")
	pausedOwner := createContributionRoomTestUser(t, client, "paused@example.com")

	publicRoom := createVerifiedContributionRoom(t, client, publicOwner.ID, "Public", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	createVerifiedContributionRoom(t, client, privateOwner.ID, "Private", contributionRoomStatusActive, contributionRoomVisibilityHide)
	createVerifiedContributionRoom(t, client, pausedOwner.ID, "Paused", contributionRoomStatusPaused, contributionRoomVisibilityOpen)

	h := newContributionRoomTestHandler(viewer.ID, nil, client)
	c, recorder := contributionRoomTestContext(http.MethodGet, fmt.Sprintf("/contribution-rooms?api_key_id=%d", apiKey.ID), viewer.ID, "")
	h.ListSelectableContributionRooms(c)
	require.Equal(t, http.StatusOK, recorder.Code)

	var envelope struct {
		Data ContributionRoomCatalogResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Items, 1)
	require.Equal(t, publicRoom.ID, envelope.Data.Items[0].ID)
	require.True(t, envelope.Data.Items[0].Selectable)
	require.Len(t, envelope.Data.Items[0].Accounts, 1)

	body := recorder.Body.String()
	require.NotContains(t, body, "secret-api-key")
	require.NotContains(t, body, "private-extra")
}

func TestContributionRoomCatalogSupportsSearchAndPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := newContributionRoomTestClient(t)
	viewer := createContributionRoomTestUser(t, client, "catalog-viewer@example.com")
	apiKey := createContributionRoomTestAPIKey(t, client, viewer.ID, "catalog-key")
	for _, name := range []string{"GPT Alpha", "GPT Beta", "GPT Gamma"} {
		owner := createContributionRoomTestUser(t, client, name+"@example.com")
		createVerifiedContributionRoom(t, client, owner.ID, name, contributionRoomStatusActive, contributionRoomVisibilityOpen)
	}
	createVerifiedContributionRoom(t, client, viewer.ID, "Claude only", contributionRoomStatusActive, contributionRoomVisibilityOpen)

	h := newContributionRoomTestHandler(viewer.ID, nil, client)
	c, recorder := contributionRoomTestContext(http.MethodGet, fmt.Sprintf("/contribution-rooms?api_key_id=%d&keyword=GPT&page=2&limit=1", apiKey.ID), viewer.ID, "")
	h.ListSelectableContributionRooms(c)
	require.Equal(t, http.StatusOK, recorder.Code)

	var envelope struct {
		Data ContributionRoomCatalogResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, 3, envelope.Data.Total)
	require.Equal(t, 2, envelope.Data.Page)
	require.Equal(t, 1, envelope.Data.Limit)
	require.Len(t, envelope.Data.Items, 1)
	require.Contains(t, envelope.Data.Items[0].Name, "GPT")
}

func TestAdminContributionRoomListSupportsSearchFiltersAndPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "admin-room-owner@example.com")
	createVerifiedContributionRoom(t, client, owner.ID, "Alpha One", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	createVerifiedContributionRoom(t, client, owner.ID, "Alpha Two", contributionRoomStatusActive, contributionRoomVisibilityHide)
	createVerifiedContributionRoom(t, client, owner.ID, "Beta Paused", contributionRoomStatusPaused, contributionRoomVisibilityOpen)

	h := newContributionRoomTestHandler(owner.ID, nil, client)
	c, recorder := contributionRoomTestContext(http.MethodGet, "/admin/contribution-rooms?keyword=Alpha&status=active&page=2&page_size=1", owner.ID, "")
	h.ListContributionRoomsForAdmin(c)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var envelope struct {
		Data struct {
			Items    []ContributionRoomView `json:"items"`
			Total    int                    `json:"total"`
			Page     int                    `json:"page"`
			PageSize int                    `json:"page_size"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, 2, envelope.Data.Total)
	require.Equal(t, 2, envelope.Data.Page)
	require.Equal(t, 1, envelope.Data.PageSize)
	require.Len(t, envelope.Data.Items, 1)
	require.Contains(t, envelope.Data.Items[0].Name, "Alpha")
	require.NotContains(t, recorder.Body.String(), "secret-api-key")
}

func TestContributionRoomCatalogHidesOwnAndExhaustedRooms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	viewer := createContributionRoomTestUser(t, client, "room-owner@example.com")
	apiKey := createContributionRoomTestAPIKey(t, client, viewer.ID, "room-owner-key")
	createVerifiedContributionRoom(t, client, viewer.ID, "My own room", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	exhaustedOwner := createContributionRoomTestUser(t, client, "exhausted-owner@example.com")
	exhaustedRoom := createVerifiedContributionRoom(t, client, exhaustedOwner.ID, "Exhausted room", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	client.ContributionRoomAccount.Query().Where(contributionroomaccount.RoomIDEQ(exhaustedRoom.ID)).OnlyX(ctx).
		Update().SetShareUsedUsd(5).SaveX(ctx)

	h := newContributionRoomTestHandler(viewer.ID, nil, client)
	c, recorder := contributionRoomTestContext(http.MethodGet, fmt.Sprintf("/contribution-rooms?api_key_id=%d", apiKey.ID), viewer.ID, "")
	h.ListSelectableContributionRooms(c)
	require.Equal(t, http.StatusOK, recorder.Code)

	var envelope struct {
		Data ContributionRoomCatalogResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Empty(t, envelope.Data.Items)
}

func TestContributionRoomPreferenceRejectsOwnRoom(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "preference-owner@example.com")
	apiKey := createContributionRoomTestAPIKey(t, client, owner.ID, "owner-key")
	room := createVerifiedContributionRoom(t, client, owner.ID, "Owner room", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	h := newContributionRoomTestHandler(owner.ID, nil, client)

	c, recorder := contributionRoomTestContext(
		http.MethodPut,
		"/contribution-rooms/preference",
		owner.ID,
		fmt.Sprintf(`{"api_key_id":%d,"room_ids":[%d],"allow_pool_fallback":false}`, apiKey.ID, room.ID),
	)
	h.UpdateContributionRoomPreference(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestContributionRoomPreferenceCanSelectSeveralRooms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	viewer := createContributionRoomTestUser(t, client, "multi-viewer@example.com")
	apiKey := createContributionRoomTestAPIKey(t, client, viewer.ID, "multi-key")
	ownerA := createContributionRoomTestUser(t, client, "multi-owner-a@example.com")
	ownerB := createContributionRoomTestUser(t, client, "multi-owner-b@example.com")
	roomA := createVerifiedContributionRoom(t, client, ownerA.ID, "Room A", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	roomB := createVerifiedContributionRoom(t, client, ownerB.ID, "Room B", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	h := newContributionRoomTestHandler(viewer.ID, nil, client)

	c, recorder := contributionRoomTestContext(
		http.MethodPut,
		"/contribution-rooms/preference",
		viewer.ID,
		fmt.Sprintf(`{"api_key_id":%d,"room_ids":[%d,%d],"allow_pool_fallback":true,"fallback_group_id":99}`, apiKey.ID, roomB.ID, roomA.ID),
	)
	h.UpdateContributionRoomPreference(c)
	require.Equal(t, http.StatusOK, recorder.Code)

	var envelope struct {
		Data ContributionRoomPreferenceView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.ElementsMatch(t, []int64{roomA.ID, roomB.ID}, envelope.Data.RoomIDs)
	require.True(t, envelope.Data.AllowPoolFallback)
	require.NotNil(t, envelope.Data.FallbackGroupID)
	require.Equal(t, int64(99), *envelope.Data.FallbackGroupID)
	require.Equal(t, apiKey.ID, envelope.Data.APIKeyID)
	require.Equal(t, 2, client.UserContributionRoomPreference.Query().
		Where(usercontributionroompreference.APIKeyIDEQ(apiKey.ID)).
		CountX(ctx))
}

func TestContributionRoomPreferenceIsIsolatedPerAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	viewer := createContributionRoomTestUser(t, client, "key-isolation-viewer@example.com")
	firstKey := createContributionRoomTestAPIKey(t, client, viewer.ID, "first-room-key")
	secondKey := createContributionRoomTestAPIKey(t, client, viewer.ID, "second-room-key")
	firstOwner := createContributionRoomTestUser(t, client, "key-isolation-owner-a@example.com")
	secondOwner := createContributionRoomTestUser(t, client, "key-isolation-owner-b@example.com")
	firstRoom := createVerifiedContributionRoom(t, client, firstOwner.ID, "First key room", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	secondRoom := createVerifiedContributionRoom(t, client, secondOwner.ID, "Second key room", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	h := newContributionRoomTestHandler(viewer.ID, nil, client)

	for _, selection := range []struct {
		apiKeyID int64
		roomID   int64
	}{
		{apiKeyID: firstKey.ID, roomID: firstRoom.ID},
		{apiKeyID: secondKey.ID, roomID: secondRoom.ID},
	} {
		c, recorder := contributionRoomTestContext(
			http.MethodPut,
			"/contribution-rooms/preference",
			viewer.ID,
			fmt.Sprintf(`{"api_key_id":%d,"room_ids":[%d],"allow_pool_fallback":false}`, selection.apiKeyID, selection.roomID),
		)
		h.UpdateContributionRoomPreference(c)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}

	firstPreference, err := h.contributionRoomPreference(ctx, viewer.ID, firstKey.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{firstRoom.ID}, firstPreference.RoomIDs)
	secondPreference, err := h.contributionRoomPreference(ctx, viewer.ID, secondKey.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{secondRoom.ID}, secondPreference.RoomIDs)
}

func TestContributionRoomPreferenceKeepsFallbackGroupWhileDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	viewer := createContributionRoomTestUser(t, client, "fallback-disabled-viewer@example.com")
	apiKey := createContributionRoomTestAPIKey(t, client, viewer.ID, "fallback-disabled-key")
	owner := createContributionRoomTestUser(t, client, "fallback-disabled-owner@example.com")
	room := createVerifiedContributionRoom(t, client, owner.ID, "Fallback disabled room", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	h := newContributionRoomTestHandler(viewer.ID, nil, client)

	c, recorder := contributionRoomTestContext(
		http.MethodPut,
		"/contribution-rooms/preference",
		viewer.ID,
		fmt.Sprintf(`{"api_key_id":%d,"room_ids":[%d],"allow_pool_fallback":false,"fallback_group_id":99}`, apiKey.ID, room.ID),
	)
	h.UpdateContributionRoomPreference(c)
	require.Equal(t, http.StatusOK, recorder.Code)

	preference := client.UserContributionRoomPreference.Query().
		Where(usercontributionroompreference.APIKeyIDEQ(apiKey.ID)).
		OnlyX(ctx)
	require.False(t, preference.AllowPoolFallback)
	require.NotNil(t, preference.FallbackGroupID)
	require.Equal(t, int64(99), *preference.FallbackGroupID)

	view, err := h.contributionRoomPreference(ctx, viewer.ID, apiKey.ID)
	require.NoError(t, err)
	require.False(t, view.AllowPoolFallback)
	require.NotNil(t, view.FallbackGroupID)
	require.Equal(t, int64(99), *view.FallbackGroupID)
}

func TestContributionRoomPreferenceRequiresFallbackGroupWhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := newContributionRoomTestClient(t)
	viewer := createContributionRoomTestUser(t, client, "fallback-required-viewer@example.com")
	apiKey := createContributionRoomTestAPIKey(t, client, viewer.ID, "fallback-required-key")
	owner := createContributionRoomTestUser(t, client, "fallback-required-owner@example.com")
	room := createVerifiedContributionRoom(t, client, owner.ID, "Fallback required room", contributionRoomStatusActive, contributionRoomVisibilityOpen)
	h := newContributionRoomTestHandler(viewer.ID, nil, client)

	c, recorder := contributionRoomTestContext(
		http.MethodPut,
		"/contribution-rooms/preference",
		viewer.ID,
		fmt.Sprintf(`{"api_key_id":%d,"room_ids":[%d],"allow_pool_fallback":true}`, apiKey.ID, room.ID),
	)
	h.UpdateContributionRoomPreference(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestRecordContributionVerificationUsesVerifiedStatus(t *testing.T) {
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "verify@example.com")
	account := createContributionRoomTestAccount(t, client, owner.ID, service.PlatformOpenAI)
	h := &AccountContributionHandler{entClient: client}

	require.NoError(t, h.recordContributionVerification(ctx, &service.Account{ID: account.ID, Platform: account.Platform, Type: account.Type}, "gpt-test", &service.ScheduledTestResult{Status: "success"}))
	verification := client.ContributionAccountVerification.Query().
		Where(contributionaccountverification.AccountIDEQ(account.ID)).
		OnlyX(ctx)
	require.Equal(t, contributionVerificationStatus, verification.Status)
	require.Equal(t, account.Platform, verification.Platform)
}

func TestContributionConnectionUpdateInvalidatesRoomVerification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "connection-owner@example.com")
	account := createContributionRoomTestAccount(t, client, owner.ID, service.PlatformOpenAI)
	verification := client.ContributionAccountVerification.Create().
		SetAccountID(account.ID).
		SetPlatform(account.Platform).
		SetStatus(contributionVerificationStatus).
		SetModelFamily("gpt").
		SetSourceKind("openai_compatible").
		SetTestedModel("gpt-5.4").
		SetTestedAt(time.Now().UTC()).
		SaveX(ctx)
	room := client.ContributionRoom.Create().
		SetOwnerUserID(owner.ID).
		SetName("Connection room").
		SetConsumerRateMultiplier(1.2).
		SetStatus(contributionRoomStatusActive).
		SetVisibility(contributionRoomVisibilityOpen).
		SaveX(ctx)
	client.ContributionRoomAccount.Create().
		SetRoomID(room.ID).
		SetAccountID(account.ID).
		SetEnabled(true).
		SetShareBudgetUsd(5).
		SetVerifiedAt(*verification.TestedAt).
		SaveX(ctx)

	h := newContributionRoomTestHandler(owner.ID, account, client)
	c, recorder := contributionRoomTestContext(http.MethodPut, fmt.Sprintf("/account-contributions/%d", account.ID), owner.ID, `{"api_key":"replacement-secret","base_url":"https://replacement.example.com/v1"}`)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(account.ID, 10)}}
	h.Update(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	verification = client.ContributionAccountVerification.Query().
		Where(contributionaccountverification.AccountIDEQ(account.ID)).
		OnlyX(ctx)
	require.Equal(t, contributionVerificationPending, verification.Status)
	require.Equal(t, "unknown", verification.ModelFamily)
	require.Equal(t, "unknown", verification.SourceKind)
	require.Nil(t, verification.TestedAt)
	require.Nil(t, verification.TestedModel)
	member := client.ContributionRoomAccount.Query().
		Where(contributionroomaccount.AccountIDEQ(account.ID)).
		OnlyX(ctx)
	require.Nil(t, member.VerifiedAt)
}

func TestSuccessfulRetestRestoresRoomVerification(t *testing.T) {
	ctx := context.Background()
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "retest-owner@example.com")
	account := createContributionRoomTestAccount(t, client, owner.ID, service.PlatformOpenAI)
	client.ContributionAccountVerification.Create().
		SetAccountID(account.ID).
		SetPlatform(account.Platform).
		SetStatus(contributionVerificationPending).
		SetModelFamily("unknown").
		SetSourceKind("unknown").
		SaveX(ctx)
	room := client.ContributionRoom.Create().
		SetOwnerUserID(owner.ID).
		SetName("Retest room").
		SetConsumerRateMultiplier(1.2).
		SetStatus(contributionRoomStatusActive).
		SetVisibility(contributionRoomVisibilityOpen).
		SaveX(ctx)
	client.ContributionRoomAccount.Create().
		SetRoomID(room.ID).
		SetAccountID(account.ID).
		SetEnabled(true).
		SetShareBudgetUsd(5).
		SaveX(ctx)

	h := &AccountContributionHandler{entClient: client}
	require.NoError(t, h.recordContributionVerification(ctx, &service.Account{
		ID: account.ID, Platform: account.Platform, Type: account.Type,
		Credentials: map[string]any{"api_key": "replacement-secret", "base_url": "https://replacement.example.com/v1"},
	}, "gpt-5.4", &service.ScheduledTestResult{Status: "success"}))

	verification := client.ContributionAccountVerification.Query().
		Where(contributionaccountverification.AccountIDEQ(account.ID)).
		OnlyX(ctx)
	require.Equal(t, contributionVerificationStatus, verification.Status)
	require.NotNil(t, verification.TestedAt)
	member := client.ContributionRoomAccount.Query().
		Where(contributionroomaccount.AccountIDEQ(account.ID)).
		OnlyX(ctx)
	require.NotNil(t, member.VerifiedAt)
}

func newContributionRoomTestClient(t *testing.T) *dbent.Client {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:contribution_room_%d?mode=memory&cache=shared", time.Now().UnixNano()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func createContributionRoomTestUser(t *testing.T, client *dbent.Client, email string) *dbent.User {
	t.Helper()
	return client.User.Create().
		SetEmail(email).
		SetPasswordHash("test-hash").
		SetUsername(email).
		SaveX(context.Background())
}

func createContributionRoomTestAPIKey(t *testing.T, client *dbent.Client, userID int64, name string) *dbent.APIKey {
	t.Helper()
	return client.APIKey.Create().
		SetUserID(userID).
		SetKey("sk-" + name).
		SetName(name).
		SetStatus(service.StatusAPIKeyActive).
		SaveX(context.Background())
}

func createContributionRoomTestAccount(t *testing.T, client *dbent.Client, contributorID int64, platform string) *dbent.Account {
	t.Helper()
	return client.Account.Create().
		SetName("Verified account").
		SetPlatform(platform).
		SetType(service.AccountTypeAPIKey).
		SetConcurrency(3).
		SetCredentials(map[string]any{"api_key": "secret-api-key"}).
		SetExtra(map[string]any{
			service.AccountContributionSourceKey: service.AccountContributionSourceValue,
			service.AccountContributorUserIDKey:  contributorID,
			"private_extra":                      "private-extra",
		}).
		SaveX(context.Background())
}

func createVerifiedContributionRoom(t *testing.T, client *dbent.Client, ownerID int64, name, status, visibility string) *dbent.ContributionRoom {
	t.Helper()
	ctx := context.Background()
	account := createContributionRoomTestAccount(t, client, ownerID, service.PlatformOpenAI)
	verification := client.ContributionAccountVerification.Create().
		SetAccountID(account.ID).
		SetPlatform(account.Platform).
		SetStatus(contributionVerificationStatus).
		SetModelFamily("gpt").
		SetTestedAt(time.Now().UTC()).
		SaveX(ctx)
	room := client.ContributionRoom.Create().
		SetOwnerUserID(ownerID).
		SetName(name).
		SetConsumerRateMultiplier(1.5).
		SetStatus(status).
		SetVisibility(visibility).
		SaveX(ctx)
	client.ContributionRoomAccount.Create().
		SetRoomID(room.ID).
		SetAccountID(account.ID).
		SetEnabled(true).
		SetShareBudgetUsd(5).
		SetVerifiedAt(*verification.TestedAt).
		SaveX(ctx)
	return room
}

func newContributionRoomTestHandler(userID int64, account *dbent.Account, client *dbent.Client) *AccountContributionHandler {
	userSvc := service.NewUserService(&contributionUserRepoStub{user: &service.User{ID: userID, Email: "test@example.com"}}, nil, nil, nil)
	var serviceAccount *service.Account
	if account != nil {
		serviceAccount = &service.Account{
			ID:          account.ID,
			Name:        account.Name,
			Platform:    account.Platform,
			Type:        account.Type,
			Concurrency: account.Concurrency,
			Extra: map[string]any{
				service.AccountContributionSourceKey: service.AccountContributionSourceValue,
				service.AccountContributorUserIDKey:  userID,
			},
		}
	}
	return NewAccountContributionHandler(userSvc, &contributionAdminServiceStub{account: serviceAccount}, nil, &service.AccountTestService{}, nil, nil, nil, client)
}

func contributionRoomTestContext(method, path string, userID int64, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
	return c, recorder
}
