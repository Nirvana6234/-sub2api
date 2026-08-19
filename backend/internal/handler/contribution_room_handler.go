package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/accountgroup"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/contributionaccountverification"
	"github.com/Wei-Shaw/sub2api/ent/contributionroom"
	"github.com/Wei-Shaw/sub2api/ent/contributionroomaccount"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/ent/usercontributionroompreference"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	contributionRoomStatusActive    = "active"
	contributionRoomStatusPaused    = "paused"
	contributionRoomVisibilityOpen  = "public"
	contributionRoomVisibilityHide  = "private"
	contributionVerificationStatus  = "verified"
	contributionVerificationFailed  = "failed"
	contributionVerificationPending = "pending"
	maxSelectedContributionRooms    = 20
	maxContributionRoomAccounts     = 100
)

// ContributionRoomAccountView is deliberately credential-free. It is used by
// both contributor and administrator room endpoints.
type ContributionRoomAccountView struct {
	AccountID               int64      `json:"account_id"`
	Name                    string     `json:"name"`
	Platform                string     `json:"platform"`
	Type                    string     `json:"type"`
	Status                  string     `json:"status"`
	Schedulable             bool       `json:"schedulable"`
	Concurrency             int        `json:"concurrency"`
	ShareConcurrency        int        `json:"share_concurrency"`
	Enabled                 bool       `json:"enabled"`
	ShareBudgetUSD          float64    `json:"share_budget_usd"`
	ShareUsedUSD            float64    `json:"share_used_usd"`
	ShareRemainingUSD       float64    `json:"share_remaining_usd"`
	MemberVerifiedAt        *time.Time `json:"member_verified_at,omitempty"`
	VerificationStatus      string     `json:"verification_status"`
	VerificationPlatform    string     `json:"verification_platform,omitempty"`
	VerificationModelFamily string     `json:"verification_model_family,omitempty"`
	VerificationSourceKind  string     `json:"verification_source_kind,omitempty"`
	VerificationTestedAt    *time.Time `json:"verification_tested_at,omitempty"`
	VerificationTestModel   string     `json:"verification_test_model,omitempty"`
	NeedsAttention          bool       `json:"needs_attention"`
}

type ContributionRoomOwnerView struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username,omitempty"`
}

// ContributionRoomView contains only scheduling and operational metadata. No
// account credentials, extras, notes, e-mail addresses, or upstream errors are
// serialised by room APIs.
type ContributionRoomView struct {
	ID                     int64                         `json:"id"`
	Name                   string                        `json:"name"`
	Owner                  ContributionRoomOwnerView     `json:"owner"`
	ConsumerRateMultiplier float64                       `json:"consumer_rate_multiplier"`
	Status                 string                        `json:"status"`
	Visibility             string                        `json:"visibility"`
	Selectable             bool                          `json:"selectable"`
	Accounts               []ContributionRoomAccountView `json:"accounts"`
	CreatedAt              time.Time                     `json:"created_at"`
	UpdatedAt              time.Time                     `json:"updated_at"`
}

type ContributionRoomPreferenceView struct {
	APIKeyID          int64   `json:"api_key_id"`
	RoomIDs           []int64 `json:"room_ids"`
	AllowPoolFallback bool    `json:"allow_pool_fallback"`
	FallbackGroupID   *int64  `json:"fallback_group_id,omitempty"`
}

type ContributionRoomCatalogResponse struct {
	Items      []ContributionRoomView         `json:"items"`
	Total      int                            `json:"total"`
	Page       int                            `json:"page"`
	Limit      int                            `json:"limit"`
	Preference ContributionRoomPreferenceView `json:"preference"`
}

type CreateContributionRoomRequest struct {
	Name                   string                               `json:"name" binding:"required"`
	ConsumerRateMultiplier float64                              `json:"consumer_rate_multiplier"`
	Accounts               []CreateContributionRoomAccountInput `json:"accounts"`
}

type CreateContributionRoomAccountInput struct {
	AccountID        int64   `json:"account_id"`
	ShareBudgetUSD   float64 `json:"share_budget_usd"`
	ShareConcurrency int     `json:"share_concurrency"`
}

type UpdateContributionRoomRequest struct {
	Name                   *string  `json:"name"`
	ConsumerRateMultiplier *float64 `json:"consumer_rate_multiplier"`
	Status                 *string  `json:"status"`
	Visibility             *string  `json:"visibility"`
}

type AddContributionRoomAccountRequest struct {
	AccountID        int64   `json:"account_id" binding:"required"`
	ShareBudgetUSD   float64 `json:"share_budget_usd" binding:"required"`
	ShareConcurrency int     `json:"share_concurrency" binding:"required"`
}

type UpdateContributionRoomAccountRequest struct {
	Enabled          *bool    `json:"enabled"`
	ShareBudgetUSD   *float64 `json:"share_budget_usd"`
	ShareConcurrency *int     `json:"share_concurrency"`
}

type UpdateContributionRoomPreferenceRequest struct {
	APIKeyID          int64   `json:"api_key_id" binding:"required"`
	RoomIDs           []int64 `json:"room_ids" binding:"required"`
	AllowPoolFallback bool    `json:"allow_pool_fallback"`
	FallbackGroupID   *int64  `json:"fallback_group_id"`
}

func (h *AccountContributionHandler) GetOwnRoom(c *gin.Context) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	room, err := h.findRoomByOwner(c.Request.Context(), subject.UserID)
	if err != nil {
		if dbent.IsNotFound(err) {
			response.NotFound(c, "Contribution room not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	view, err := h.contributionRoomView(c.Request.Context(), room)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

func (h *AccountContributionHandler) CreateOwnRoom(c *gin.Context) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	var req CreateContributionRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 100 {
		response.BadRequest(c, "room name must contain 1 to 100 characters")
		return
	}
	multiplier := req.ConsumerRateMultiplier
	if multiplier == 0 {
		multiplier = 1
	}
	if !validContributionRoomMultiplier(multiplier) {
		response.BadRequest(c, "consumer_rate_multiplier must be greater than 0 and no more than 100")
		return
	}
	if len(req.Accounts) == 0 || len(req.Accounts) > maxContributionRoomAccounts {
		response.BadRequest(c, fmt.Sprintf("accounts must contain 1 to %d shared accounts", maxContributionRoomAccounts))
		return
	}
	if existing, err := h.findRoomByOwner(c.Request.Context(), subject.UserID); err == nil && existing != nil {
		response.Error(c, http.StatusConflict, "Each contributor can only create one room")
		return
	} else if err != nil && !dbent.IsNotFound(err) {
		response.ErrorFrom(c, err)
		return
	}
	if h.entClient == nil {
		response.Error(c, http.StatusServiceUnavailable, "Contribution room service unavailable")
		return
	}
	verifiedAt := make(map[int64]time.Time, len(req.Accounts))
	seenAccountIDs := make(map[int64]struct{}, len(req.Accounts))
	accountIDs := make([]int64, 0, len(req.Accounts))
	for _, item := range req.Accounts {
		if item.AccountID <= 0 || !validContributionRoomBudget(item.ShareBudgetUSD) || item.ShareBudgetUSD <= 0 || item.ShareConcurrency <= 0 {
			response.BadRequest(c, "each shared account requires a positive account_id, share_budget_usd, and share_concurrency")
			return
		}
		if _, exists := seenAccountIDs[item.AccountID]; exists {
			response.BadRequest(c, "the same account cannot be added to a room more than once")
			return
		}
		seenAccountIDs[item.AccountID] = struct{}{}
		accountIDs = append(accountIDs, item.AccountID)
		account, ok := h.ownedAccountByID(c, item.AccountID)
		if !ok {
			return
		}
		if account.IsSharedPoolAccount() {
			response.BadRequest(c, "an account in an administrator pool cannot also join a contribution room")
			return
		}
		if !validContributionRoomShareConcurrency(item.ShareConcurrency, account.Concurrency) {
			response.BadRequest(c, fmt.Sprintf("share_concurrency for account %d must be between 1 and its maximum concurrency %d", account.ID, account.Concurrency))
			return
		}
		verification, err := h.verifiedContributionAccount(c.Request.Context(), account.ID, account.Platform)
		if err != nil {
			if dbent.IsNotFound(err) {
				response.BadRequest(c, "all selected accounts must pass platform verification before the room is created")
				return
			}
			response.ErrorFrom(c, err)
			return
		}
		if verification.TestedAt == nil {
			response.BadRequest(c, "account verification is missing its test time; run the account test again")
			return
		}
		if exists, err := h.entClient.ContributionRoomAccount.Query().Where(contributionroomaccount.AccountIDEQ(account.ID)).Exist(c.Request.Context()); err != nil {
			response.ErrorFrom(c, err)
			return
		} else if exists {
			response.Error(c, http.StatusConflict, "a selected account already belongs to a contribution room")
			return
		}
		verifiedAt[item.AccountID] = *verification.TestedAt
	}
	previousGroupIDs, err := h.accountGroupIDs(c.Request.Context(), accountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	tx, err := h.entClient.Tx(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	room, err := tx.ContributionRoom.Create().
		SetOwnerUserID(subject.UserID).
		SetName(name).
		SetConsumerRateMultiplier(multiplier).
		SetStatus(contributionRoomStatusActive).
		SetVisibility(contributionRoomVisibilityOpen).
		Save(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for _, item := range req.Accounts {
		if _, err := tx.ContributionRoomAccount.Create().
			SetRoomID(room.ID).
			SetAccountID(item.AccountID).
			SetEnabled(true).
			SetShareConcurrency(item.ShareConcurrency).
			SetShareBudgetUsd(item.ShareBudgetUSD).
			SetVerifiedAt(verifiedAt[item.AccountID]).
			Save(c.Request.Context()); err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if _, err := tx.AccountGroup.Delete().
			Where(accountgroup.AccountIDEQ(item.AccountID)).
			Exec(c.Request.Context()); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for _, item := range req.Accounts {
		if err := h.notifyAccountGroupsChanged(c.Request.Context(), item.AccountID, previousGroupIDs[item.AccountID]); err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if err := h.restoreContributionScheduling(c.Request.Context(), item.AccountID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	room, err = h.loadContributionRoom(c.Request.Context(), room.ID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	view, err := h.contributionRoomView(c.Request.Context(), room)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

func (h *AccountContributionHandler) UpdateOwnRoom(c *gin.Context) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	room, err := h.findRoomByOwner(c.Request.Context(), subject.UserID)
	if err != nil {
		if dbent.IsNotFound(err) {
			response.NotFound(c, "Contribution room not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	var req UpdateContributionRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	update := room.Update()
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len([]rune(name)) > 100 {
			response.BadRequest(c, "room name must contain 1 to 100 characters")
			return
		}
		update.SetName(name)
	}
	if req.ConsumerRateMultiplier != nil {
		if !validContributionRoomMultiplier(*req.ConsumerRateMultiplier) {
			response.BadRequest(c, "consumer_rate_multiplier must be greater than 0 and no more than 100")
			return
		}
		update.SetConsumerRateMultiplier(*req.ConsumerRateMultiplier)
	}
	if req.Status != nil {
		status, valid := normalizeContributionRoomStatus(*req.Status)
		if !valid {
			response.BadRequest(c, "status must be active or paused")
			return
		}
		update.SetStatus(status)
	}
	if req.Visibility != nil {
		visibility, valid := normalizeContributionRoomVisibility(*req.Visibility)
		if !valid {
			response.BadRequest(c, "visibility must be public or private")
			return
		}
		update.SetVisibility(visibility)
	}
	updated, err := update.Save(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	updated, err = h.loadContributionRoom(c.Request.Context(), updated.ID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	view, err := h.contributionRoomView(c.Request.Context(), updated)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

func (h *AccountContributionHandler) AddOwnRoomAccount(c *gin.Context) {
	_, room, ok := h.ownedContributionRoom(c)
	if !ok {
		return
	}
	var req AddContributionRoomAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.AccountID <= 0 || !validContributionRoomBudget(req.ShareBudgetUSD) || req.ShareBudgetUSD <= 0 || req.ShareConcurrency <= 0 {
		response.BadRequest(c, "account_id, share_budget_usd, and share_concurrency must be positive")
		return
	}
	account, ok := h.ownedAccountByID(c, req.AccountID)
	if !ok {
		return
	}
	if account.IsSharedPoolAccount() {
		response.BadRequest(c, "an account in an administrator pool cannot also join a contribution room")
		return
	}
	if !validContributionRoomShareConcurrency(req.ShareConcurrency, account.Concurrency) {
		response.BadRequest(c, fmt.Sprintf("share_concurrency must be between 1 and the account maximum concurrency %d", account.Concurrency))
		return
	}
	verification, err := h.verifiedContributionAccount(c.Request.Context(), account.ID, account.Platform)
	if err != nil {
		if dbent.IsNotFound(err) {
			response.BadRequest(c, "account must pass platform verification before it can join a room")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	if verification.TestedAt == nil {
		response.BadRequest(c, "account verification is missing its test time; run the account test again")
		return
	}
	if _, err := h.entClient.ContributionRoomAccount.Query().Where(contributionroomaccount.AccountIDEQ(account.ID)).Only(c.Request.Context()); err == nil {
		response.Error(c, http.StatusConflict, "account already belongs to a contribution room")
		return
	} else if !dbent.IsNotFound(err) {
		response.ErrorFrom(c, err)
		return
	}
	previousGroupIDs, err := h.accountGroupIDs(c.Request.Context(), []int64{account.ID})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	tx, err := h.entClient.Tx(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	member, err := tx.ContributionRoomAccount.Create().
		SetRoomID(room.ID).
		SetAccountID(account.ID).
		SetEnabled(true).
		SetShareConcurrency(req.ShareConcurrency).
		SetShareBudgetUsd(req.ShareBudgetUSD).
		SetVerifiedAt(*verification.TestedAt).
		Save(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if _, err := tx.AccountGroup.Delete().
		Where(accountgroup.AccountIDEQ(account.ID)).
		Exec(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := tx.Commit(); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.notifyAccountGroupsChanged(c.Request.Context(), account.ID, previousGroupIDs[account.ID]); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.restoreContributionScheduling(c.Request.Context(), account.ID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	view, err := h.contributionRoomAccountView(c.Request.Context(), member, nil)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

func (h *AccountContributionHandler) accountGroupIDs(ctx context.Context, accountIDs []int64) (map[int64][]int64, error) {
	result := make(map[int64][]int64, len(accountIDs))
	if h == nil || h.entClient == nil || len(accountIDs) == 0 {
		return result, nil
	}
	bindings, err := h.entClient.AccountGroup.Query().
		Where(accountgroup.AccountIDIn(accountIDs...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		result[binding.AccountID] = append(result[binding.AccountID], binding.GroupID)
	}
	return result, nil
}

func (h *AccountContributionHandler) notifyAccountGroupsChanged(ctx context.Context, accountID int64, groupIDs []int64) error {
	if len(groupIDs) == 0 || h == nil || h.adminService == nil {
		return nil
	}
	notifier, ok := h.adminService.(interface {
		NotifyAccountGroupsChanged(context.Context, int64, []int64) error
	})
	if !ok {
		return nil
	}
	return notifier.NotifyAccountGroupsChanged(ctx, accountID, groupIDs)
}

func (h *AccountContributionHandler) UpdateOwnRoomAccount(c *gin.Context) {
	_, room, ok := h.ownedContributionRoom(c)
	if !ok {
		return
	}
	accountID, ok := parseContributionRoomAccountID(c)
	if !ok {
		return
	}
	var req UpdateContributionRoomAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Enabled == nil && req.ShareBudgetUSD == nil && req.ShareConcurrency == nil {
		response.BadRequest(c, "at least one room account setting is required")
		return
	}
	if req.ShareBudgetUSD != nil && !validContributionRoomBudget(*req.ShareBudgetUSD) {
		response.BadRequest(c, "share_budget_usd must be between 0 and 1000000")
		return
	}
	member, err := h.findRoomMember(c.Request.Context(), room.ID, accountID)
	if err != nil {
		if dbent.IsNotFound(err) {
			response.NotFound(c, "Contribution room account not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	if req.ShareConcurrency != nil {
		maxConcurrency := 0
		if member.Edges.Account != nil {
			maxConcurrency = member.Edges.Account.Concurrency
		}
		if !validContributionRoomShareConcurrency(*req.ShareConcurrency, maxConcurrency) {
			response.BadRequest(c, fmt.Sprintf("share_concurrency must be between 1 and the account maximum concurrency %d", maxConcurrency))
			return
		}
	}
	update := member.Update()
	if req.Enabled != nil {
		update.SetEnabled(*req.Enabled)
	}
	if req.ShareBudgetUSD != nil {
		update.SetShareBudgetUsd(*req.ShareBudgetUSD)
	}
	if req.ShareConcurrency != nil {
		update.SetShareConcurrency(*req.ShareConcurrency)
	}
	updated, err := update.Save(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	view, err := h.contributionRoomAccountView(c.Request.Context(), updated, nil)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

func (h *AccountContributionHandler) DeleteOwnRoomAccount(c *gin.Context) {
	_, room, ok := h.ownedContributionRoom(c)
	if !ok {
		return
	}
	accountID, ok := parseContributionRoomAccountID(c)
	if !ok {
		return
	}
	member, err := h.findRoomMember(c.Request.Context(), room.ID, accountID)
	if err != nil {
		if dbent.IsNotFound(err) {
			response.NotFound(c, "Contribution room account not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	if err := h.entClient.ContributionRoomAccount.DeleteOne(member).Exec(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *AccountContributionHandler) ListSelectableContributionRooms(c *gin.Context) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	if h.entClient == nil {
		response.Error(c, http.StatusServiceUnavailable, "Contribution room service unavailable")
		return
	}
	apiKeyID, ok := parseContributionRoomAPIKeyID(c)
	if !ok || !h.ensureOwnedContributionRoomAPIKey(c, subject.UserID, apiKeyID) {
		return
	}
	page := parsePositiveQueryInt(c.Query("page"), 1)
	limit := parsePositiveQueryInt(c.Query("limit"), 24)
	if limit > 100 {
		limit = 100
	}
	query := h.entClient.ContributionRoom.Query().
		Where(
			contributionroom.StatusEQ(contributionRoomStatusActive),
			contributionroom.VisibilityEQ(contributionRoomVisibilityOpen),
			contributionroom.OwnerUserIDNEQ(subject.UserID),
		)
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		query.Where(contributionroom.NameContainsFold(keyword))
	}
	total, err := query.Count(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	rooms, err := query.
		Order(dbent.Desc(contributionroom.FieldUpdatedAt)).
		Offset((page - 1) * limit).
		Limit(limit).
		WithOwner().
		WithAccounts(func(query *dbent.ContributionRoomAccountQuery) { query.WithAccount() }).
		All(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]ContributionRoomView, 0, len(rooms))
	for _, room := range rooms {
		view, err := h.contributionRoomView(c.Request.Context(), room)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if view.Selectable {
			items = append(items, view)
		}
	}
	preference, err := h.contributionRoomPreference(c.Request.Context(), subject.UserID, apiKeyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, ContributionRoomCatalogResponse{Items: items, Total: total, Page: page, Limit: limit, Preference: preference})
}

func (h *AccountContributionHandler) GetContributionRoomPreference(c *gin.Context) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	apiKeyID, ok := parseContributionRoomAPIKeyID(c)
	if !ok || !h.ensureOwnedContributionRoomAPIKey(c, subject.UserID, apiKeyID) {
		return
	}
	preference, err := h.contributionRoomPreference(c.Request.Context(), subject.UserID, apiKeyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, preference)
}

func (h *AccountContributionHandler) UpdateContributionRoomPreference(c *gin.Context) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	var req UpdateContributionRoomPreferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.APIKeyID <= 0 || !h.ensureOwnedContributionRoomAPIKey(c, subject.UserID, req.APIKeyID) {
		return
	}
	if len(req.RoomIDs) == 0 || len(req.RoomIDs) > maxSelectedContributionRooms {
		response.BadRequest(c, fmt.Sprintf("room_ids must contain 1 to %d rooms", maxSelectedContributionRooms))
		return
	}
	var fallbackGroupID *int64
	if req.FallbackGroupID != nil {
		if *req.FallbackGroupID <= 0 {
			response.BadRequest(c, "fallback_group_id must be positive")
			return
		}
		if err := h.validateContributionRoomFallbackGroup(c.Request.Context(), subject.UserID, *req.FallbackGroupID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
		value := *req.FallbackGroupID
		fallbackGroupID = &value
	}
	if req.AllowPoolFallback {
		if fallbackGroupID == nil {
			response.BadRequest(c, "fallback_group_id is required when pool fallback is enabled")
			return
		}
	}
	roomIDs := make([]int64, 0, len(req.RoomIDs))
	seen := make(map[int64]struct{}, len(req.RoomIDs))
	for _, roomID := range req.RoomIDs {
		if roomID <= 0 {
			response.BadRequest(c, "room_ids must contain positive IDs")
			return
		}
		if _, duplicate := seen[roomID]; duplicate {
			continue
		}
		seen[roomID] = struct{}{}
		room, err := h.loadContributionRoom(c.Request.Context(), roomID)
		if err != nil {
			if dbent.IsNotFound(err) {
				response.NotFound(c, "Contribution room not found")
				return
			}
			response.ErrorFrom(c, err)
			return
		}
		view, err := h.contributionRoomView(c.Request.Context(), room)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if room.OwnerUserID == subject.UserID {
			response.BadRequest(c, "contributors cannot select their own contribution room")
			return
		}
		if room.Status != contributionRoomStatusActive || !view.Selectable {
			response.BadRequest(c, "room is not available for selection")
			return
		}
		roomIDs = append(roomIDs, room.ID)
	}
	if err := h.saveContributionRoomPreference(c.Request.Context(), subject.UserID, req.APIKeyID, roomIDs, req.AllowPoolFallback, fallbackGroupID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, ContributionRoomPreferenceView{APIKeyID: req.APIKeyID, RoomIDs: roomIDs, AllowPoolFallback: req.AllowPoolFallback, FallbackGroupID: fallbackGroupID})
}

func (h *AccountContributionHandler) DeleteContributionRoomPreference(c *gin.Context) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	if h.entClient == nil {
		response.Error(c, http.StatusServiceUnavailable, "Contribution room service unavailable")
		return
	}
	apiKeyID, ok := parseContributionRoomAPIKeyID(c)
	if !ok || !h.ensureOwnedContributionRoomAPIKey(c, subject.UserID, apiKeyID) {
		return
	}
	if _, err := h.entClient.UserContributionRoomPreference.Delete().
		Where(
			usercontributionroompreference.UserIDEQ(subject.UserID),
			usercontributionroompreference.APIKeyIDEQ(apiKeyID),
		).
		Exec(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, ContributionRoomPreferenceView{APIKeyID: apiKeyID, RoomIDs: []int64{}, AllowPoolFallback: false})
}

// ListContributionRoomsForAdmin is a separate, credential-free view for the
// administrator's shared-account workspace.
func (h *AccountContributionHandler) ListContributionRoomsForAdmin(c *gin.Context) {
	if h.entClient == nil {
		response.Error(c, http.StatusServiceUnavailable, "Contribution room service unavailable")
		return
	}
	page := contributionRoomQueryInt(c, "page", 1, 1, 1_000_000)
	pageSize := contributionRoomQueryInt(c, "page_size", 20, 1, 100)
	keyword := strings.TrimSpace(c.Query("keyword"))
	status := strings.TrimSpace(strings.ToLower(c.Query("status")))
	visibility := strings.TrimSpace(strings.ToLower(c.Query("visibility")))
	if status != "" && status != contributionRoomStatusActive && status != contributionRoomStatusPaused {
		response.BadRequest(c, "status must be active or paused")
		return
	}
	if visibility != "" && visibility != contributionRoomVisibilityOpen && visibility != contributionRoomVisibilityHide {
		response.BadRequest(c, "visibility must be public or private")
		return
	}

	query := h.entClient.ContributionRoom.Query()
	if keyword != "" {
		query.Where(contributionroom.Or(
			contributionroom.NameContainsFold(keyword),
			contributionroom.HasOwnerWith(user.Or(user.UsernameContainsFold(keyword), user.EmailContainsFold(keyword))),
		))
	}
	if status != "" {
		query.Where(contributionroom.StatusEQ(status))
	}
	if visibility != "" {
		query.Where(contributionroom.VisibilityEQ(visibility))
	}
	total, err := query.Clone().Count(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	rooms, err := query.WithOwner().
		WithAccounts(func(query *dbent.ContributionRoomAccountQuery) { query.WithAccount() }).
		Order(dbent.Desc(contributionroom.FieldUpdatedAt), dbent.Desc(contributionroom.FieldID)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		All(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]ContributionRoomView, 0, len(rooms))
	for _, room := range rooms {
		view, err := h.contributionRoomView(c.Request.Context(), room)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		items = append(items, view)
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func contributionRoomQueryInt(c *gin.Context, key string, fallback, minimum, maximum int) int {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func (h *AccountContributionHandler) GetContributionRoomForAdmin(c *gin.Context) {
	room, ok := h.adminContributionRoom(c)
	if !ok {
		return
	}
	view, err := h.contributionRoomView(c.Request.Context(), room)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

func (h *AccountContributionHandler) UpdateContributionRoomForAdmin(c *gin.Context) {
	room, ok := h.adminContributionRoom(c)
	if !ok {
		return
	}
	var req UpdateContributionRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	update := room.Update()
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len([]rune(name)) > 100 {
			response.BadRequest(c, "room name must contain 1 to 100 characters")
			return
		}
		update.SetName(name)
	}
	if req.ConsumerRateMultiplier != nil {
		if !validContributionRoomMultiplier(*req.ConsumerRateMultiplier) {
			response.BadRequest(c, "consumer_rate_multiplier must be greater than 0 and no more than 100")
			return
		}
		update.SetConsumerRateMultiplier(*req.ConsumerRateMultiplier)
	}
	if req.Status != nil {
		status, valid := normalizeContributionRoomStatus(*req.Status)
		if !valid {
			response.BadRequest(c, "status must be active or paused")
			return
		}
		update.SetStatus(status)
	}
	if req.Visibility != nil {
		visibility, valid := normalizeContributionRoomVisibility(*req.Visibility)
		if !valid {
			response.BadRequest(c, "visibility must be public or private")
			return
		}
		update.SetVisibility(visibility)
	}
	updated, err := update.Save(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	updated, err = h.loadContributionRoom(c.Request.Context(), updated.ID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	view, err := h.contributionRoomView(c.Request.Context(), updated)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

func (h *AccountContributionHandler) UpdateContributionRoomAccountForAdmin(c *gin.Context) {
	room, ok := h.adminContributionRoom(c)
	if !ok {
		return
	}
	accountID, ok := parseContributionRoomAccountID(c)
	if !ok {
		return
	}
	var req UpdateContributionRoomAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	member, err := h.findRoomMember(c.Request.Context(), room.ID, accountID)
	if err != nil {
		if dbent.IsNotFound(err) {
			response.NotFound(c, "Contribution room account not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	updated, err := member.Update().SetEnabled(*req.Enabled).Save(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	view, err := h.contributionRoomAccountView(c.Request.Context(), updated, nil)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

func (h *AccountContributionHandler) TestContributionRoomAccountForAdmin(c *gin.Context) {
	room, ok := h.adminContributionRoom(c)
	if !ok {
		return
	}
	accountID, ok := parseContributionRoomAccountID(c)
	if !ok {
		return
	}
	member, err := h.findRoomMember(c.Request.Context(), room.ID, accountID)
	if err != nil {
		if dbent.IsNotFound(err) {
			response.NotFound(c, "Contribution room account not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	var req struct {
		ModelID string `json:"model_id"`
	}
	_ = c.ShouldBindJSON(&req)
	ctx := context.WithValue(c.Request.Context(), ctxkey.AllowContributionAccountManagement, true)
	account, err := h.adminService.GetAccount(ctx, member.AccountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	modelID := contributionVerificationTestModel(account, req.ModelID)
	result, err := h.accountTestRunner.RunTestBackground(ctx, member.AccountID, modelID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.recordContributionVerification(ctx, account, modelID, result); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if result != nil && result.Status == "success" && h.rateLimitService != nil {
		_, _ = h.rateLimitService.RecoverAccountAfterSuccessfulTest(ctx, member.AccountID)
	}
	if result != nil && result.Status == "success" {
		if err := h.restoreContributionScheduling(ctx, member.AccountID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	response.Success(c, result)
}

func (h *AccountContributionHandler) recordContributionVerification(ctx context.Context, account *service.Account, modelID string, result *service.ScheduledTestResult) error {
	if h.entClient == nil {
		return nil
	}
	if account == nil || account.ID <= 0 {
		return fmt.Errorf("contribution verification account is required")
	}
	status := contributionVerificationFailed
	now := time.Now().UTC()
	platform := strings.TrimSpace(account.Platform)
	modelID = strings.TrimSpace(modelID)
	modelFamily := contributionModelFamily(platform, modelID)
	sourceKind := contributionVerificationSourceKind(account)
	if result != nil && result.Status == "success" {
		status = contributionVerificationStatus
	}

	tx, err := h.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := tx.ContributionAccountVerification.Query().
		Where(contributionaccountverification.AccountIDEQ(account.ID)).
		Only(ctx)
	if err == nil {
		update := existing.Update().SetPlatform(platform).SetStatus(status).SetModelFamily(modelFamily).SetSourceKind(sourceKind).SetTestedAt(now)
		if modelID == "" {
			update.ClearTestedModel()
		} else {
			update.SetTestedModel(modelID)
		}
		if status == contributionVerificationStatus {
			update.ClearRedactedErrorSummary()
		} else {
			// Never persist raw upstream errors: they can contain authentication data.
			update.SetRedactedErrorSummary("verification failed")
		}
		if err := update.Exec(ctx); err != nil {
			return err
		}
	} else {
		if !dbent.IsNotFound(err) {
			return err
		}
		create := tx.ContributionAccountVerification.Create().
			SetAccountID(account.ID).
			SetPlatform(platform).
			SetStatus(status).
			SetModelFamily(modelFamily).
			SetSourceKind(sourceKind).
			SetTestedAt(now)
		if modelID != "" {
			create.SetTestedModel(modelID)
		}
		if status != contributionVerificationStatus {
			create.SetRedactedErrorSummary("verification failed")
		}
		if err := create.Exec(ctx); err != nil {
			return err
		}
	}

	members := tx.ContributionRoomAccount.Update().
		Where(contributionroomaccount.AccountIDEQ(account.ID))
	if status == contributionVerificationStatus {
		members.SetVerifiedAt(now)
	} else {
		members.ClearVerifiedAt()
	}
	if _, err := members.Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *AccountContributionHandler) invalidateContributionVerification(ctx context.Context, accountID int64) error {
	if h == nil || h.entClient == nil || accountID <= 0 {
		return nil
	}
	tx, err := h.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ContributionAccountVerification.Update().
		Where(contributionaccountverification.AccountIDEQ(accountID)).
		SetStatus(contributionVerificationPending).
		SetModelFamily("unknown").
		SetSourceKind("unknown").
		ClearTestedModel().
		ClearTestedAt().
		ClearRedactedErrorSummary().
		Save(ctx); err != nil {
		return err
	}
	if _, err := tx.ContributionRoomAccount.Update().
		Where(contributionroomaccount.AccountIDEQ(accountID)).
		ClearVerifiedAt().
		Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func contributionVerificationTestModel(account *service.Account, requestedModel string) string {
	if account != nil && account.Platform == service.PlatformOpenAI {
		// User-contributed OpenAI-compatible credentials must prove a canonical
		// GPT request before they are eligible for sharing. Free accounts use
		// the matching lightweight model.
		return service.OpenAITestModelForAccount(account)
	}
	return strings.TrimSpace(requestedModel)
}

func contributionModelFamily(platform, modelID string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case service.PlatformOpenAI:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelID)), "gpt-") {
			return "gpt"
		}
	case service.PlatformAnthropic:
		return "claude"
	case service.PlatformGemini:
		return "gemini"
	case service.PlatformGrok:
		return "grok"
	}
	return "unknown"
}

func contributionVerificationSourceKind(account *service.Account) string {
	if account == nil {
		return "unknown"
	}
	if account.Platform != service.PlatformOpenAI {
		return "platform_verified"
	}
	if account.Type == service.AccountTypeOAuth {
		return "official_openai_oauth"
	}
	baseURL := strings.TrimSpace(account.GetOpenAIBaseURL())
	if baseURL == "" {
		return "official_openai"
	}
	parsed, err := url.Parse(baseURL)
	if err == nil && strings.EqualFold(parsed.Hostname(), "api.openai.com") {
		return "official_openai"
	}
	return "openai_compatible"
}

func (h *AccountContributionHandler) ownedContributionRoom(c *gin.Context) (int64, *dbent.ContributionRoom, bool) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return 0, nil, false
	}
	room, err := h.findRoomByOwner(c.Request.Context(), subject.UserID)
	if err != nil {
		if dbent.IsNotFound(err) {
			response.NotFound(c, "Contribution room not found")
		} else {
			response.ErrorFrom(c, err)
		}
		return 0, nil, false
	}
	return subject.UserID, room, true
}

func (h *AccountContributionHandler) ownedAccountByID(c *gin.Context, accountID int64) (*service.Account, bool) {
	if accountID <= 0 {
		response.BadRequest(c, "invalid account id")
		return nil, false
	}
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return nil, false
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, false
	}
	if account == nil {
		response.NotFound(c, "Contribution account not found")
		return nil, false
	}
	if subject.UserID <= 0 || !account.IsContributedBy(subject.UserID) {
		response.Error(c, http.StatusForbidden, "You can only add accounts you submitted")
		return nil, false
	}
	return account, true
}

func (h *AccountContributionHandler) findRoomByOwner(ctx context.Context, ownerID int64) (*dbent.ContributionRoom, error) {
	if h.entClient == nil {
		return nil, fmt.Errorf("contribution room service unavailable")
	}
	return h.entClient.ContributionRoom.Query().
		Where(contributionroom.OwnerUserIDEQ(ownerID)).
		WithOwner().
		WithAccounts(func(query *dbent.ContributionRoomAccountQuery) { query.WithAccount() }).
		First(ctx)
}

func (h *AccountContributionHandler) loadContributionRoom(ctx context.Context, roomID int64) (*dbent.ContributionRoom, error) {
	if h.entClient == nil {
		return nil, fmt.Errorf("contribution room service unavailable")
	}
	return h.entClient.ContributionRoom.Query().
		Where(contributionroom.IDEQ(roomID)).
		WithOwner().
		WithAccounts(func(query *dbent.ContributionRoomAccountQuery) { query.WithAccount() }).
		Only(ctx)
}

func (h *AccountContributionHandler) findRoomMember(ctx context.Context, roomID, accountID int64) (*dbent.ContributionRoomAccount, error) {
	return h.entClient.ContributionRoomAccount.Query().
		Where(contributionroomaccount.RoomIDEQ(roomID), contributionroomaccount.AccountIDEQ(accountID)).
		WithAccount().
		Only(ctx)
}

func (h *AccountContributionHandler) verifiedContributionAccount(ctx context.Context, accountID int64, platform string) (*dbent.ContributionAccountVerification, error) {
	query := h.entClient.ContributionAccountVerification.Query().Where(
		contributionaccountverification.AccountIDEQ(accountID),
		contributionaccountverification.PlatformEQ(platform),
		contributionaccountverification.StatusEQ(contributionVerificationStatus),
	)
	if family := contributionExpectedModelFamily(platform); family != "" {
		query.Where(contributionaccountverification.ModelFamilyEQ(family))
	}
	return query.Only(ctx)
}

func (h *AccountContributionHandler) contributionRoomView(ctx context.Context, room *dbent.ContributionRoom) (ContributionRoomView, error) {
	if room == nil {
		return ContributionRoomView{}, fmt.Errorf("contribution room not found")
	}
	owner := ContributionRoomOwnerView{UserID: room.OwnerUserID}
	if room.Edges.Owner != nil {
		owner.Username = strings.TrimSpace(room.Edges.Owner.Username)
	}
	members := make([]ContributionRoomAccountView, 0, len(room.Edges.Accounts))
	selectable := false
	for _, member := range room.Edges.Accounts {
		view, err := h.contributionRoomAccountView(ctx, member, nil)
		if err != nil {
			return ContributionRoomView{}, err
		}
		if !view.NeedsAttention {
			selectable = true
		}
		members = append(members, view)
	}
	return ContributionRoomView{
		ID:                     room.ID,
		Name:                   room.Name,
		Owner:                  owner,
		ConsumerRateMultiplier: room.ConsumerRateMultiplier,
		Status:                 room.Status,
		Visibility:             room.Visibility,
		Selectable:             room.Status == contributionRoomStatusActive && room.Visibility == contributionRoomVisibilityOpen && selectable,
		Accounts:               members,
		CreatedAt:              room.CreatedAt,
		UpdatedAt:              room.UpdatedAt,
	}, nil
}

func (h *AccountContributionHandler) contributionRoomAccountView(ctx context.Context, member *dbent.ContributionRoomAccount, verification *dbent.ContributionAccountVerification) (ContributionRoomAccountView, error) {
	if member == nil {
		return ContributionRoomAccountView{}, fmt.Errorf("contribution room account not found")
	}
	if member.Edges.Account == nil {
		loaded, err := h.findRoomMember(ctx, member.RoomID, member.AccountID)
		if err != nil {
			return ContributionRoomAccountView{}, err
		}
		member = loaded
	}
	if verification == nil {
		var err error
		verification, err = h.entClient.ContributionAccountVerification.Query().
			Where(contributionaccountverification.AccountIDEQ(member.AccountID)).
			Only(ctx)
		if err != nil && !dbent.IsNotFound(err) {
			return ContributionRoomAccountView{}, err
		}
		if dbent.IsNotFound(err) {
			verification = nil
		}
	}
	account := member.Edges.Account
	view := ContributionRoomAccountView{
		AccountID:        member.AccountID,
		Name:             account.Name,
		Platform:         account.Platform,
		Type:             account.Type,
		Status:           account.Status,
		Schedulable:      account.Schedulable,
		Concurrency:      account.Concurrency,
		ShareConcurrency: member.ShareConcurrency,
		Enabled:          member.Enabled,
		ShareBudgetUSD:   member.ShareBudgetUsd,
		ShareUsedUSD:     member.ShareUsedUsd,
		MemberVerifiedAt: member.VerifiedAt,
	}
	view.ShareRemainingUSD = member.ShareBudgetUsd - member.ShareUsedUsd
	if view.ShareRemainingUSD < 0 {
		view.ShareRemainingUSD = 0
	}
	if verification != nil {
		view.VerificationStatus = verification.Status
		view.VerificationPlatform = verification.Platform
		view.VerificationModelFamily = verification.ModelFamily
		view.VerificationSourceKind = verification.SourceKind
		view.VerificationTestedAt = verification.TestedAt
		if verification.TestedModel != nil {
			view.VerificationTestModel = *verification.TestedModel
		}
	}
	view.NeedsAttention = !view.Enabled || view.Status != service.StatusActive || !view.Schedulable ||
		view.ShareRemainingUSD <= 0 || view.ShareConcurrency <= 0 || (view.Concurrency > 0 && view.ShareConcurrency > view.Concurrency) ||
		view.VerificationStatus != contributionVerificationStatus || view.VerificationPlatform != view.Platform ||
		!contributionModelFamilyMatchesPlatform(view.VerificationModelFamily, view.Platform)
	return view, nil
}

func (h *AccountContributionHandler) contributionRoomPreference(ctx context.Context, userID, apiKeyID int64) (ContributionRoomPreferenceView, error) {
	if h.entClient == nil {
		return ContributionRoomPreferenceView{}, fmt.Errorf("contribution room service unavailable")
	}
	prefs, err := h.entClient.UserContributionRoomPreference.Query().
		Where(
			usercontributionroompreference.UserIDEQ(userID),
			usercontributionroompreference.APIKeyIDEQ(apiKeyID),
		).
		Order(dbent.Asc(usercontributionroompreference.FieldRoomID)).
		All(ctx)
	if err != nil {
		return ContributionRoomPreferenceView{}, err
	}
	roomIDs := make([]int64, 0, len(prefs))
	allowPoolFallback := false
	var fallbackGroupID *int64
	for _, pref := range prefs {
		roomIDs = append(roomIDs, pref.RoomID)
		allowPoolFallback = allowPoolFallback || pref.AllowPoolFallback
		if pref.FallbackGroupID != nil && fallbackGroupID == nil {
			value := *pref.FallbackGroupID
			fallbackGroupID = &value
		}
	}
	return ContributionRoomPreferenceView{APIKeyID: apiKeyID, RoomIDs: roomIDs, AllowPoolFallback: allowPoolFallback, FallbackGroupID: fallbackGroupID}, nil
}

func (h *AccountContributionHandler) saveContributionRoomPreference(ctx context.Context, userID, apiKeyID int64, roomIDs []int64, allowPoolFallback bool, fallbackGroupID *int64) error {
	if h.entClient == nil {
		return fmt.Errorf("contribution room service unavailable")
	}
	tx, err := h.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.UserContributionRoomPreference.Delete().
		Where(
			usercontributionroompreference.UserIDEQ(userID),
			usercontributionroompreference.APIKeyIDEQ(apiKeyID),
		).
		Exec(ctx); err != nil {
		return err
	}
	for _, roomID := range roomIDs {
		create := tx.UserContributionRoomPreference.Create().
			SetUserID(userID).
			SetAPIKeyID(apiKeyID).
			SetRoomID(roomID).
			SetAllowPoolFallback(allowPoolFallback)
		if fallbackGroupID != nil {
			create.SetFallbackGroupID(*fallbackGroupID)
		}
		if err := create.Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func parseContributionRoomAPIKeyID(c *gin.Context) (int64, bool) {
	apiKeyID, err := strconv.ParseInt(strings.TrimSpace(c.Query("api_key_id")), 10, 64)
	if err != nil || apiKeyID <= 0 {
		response.BadRequest(c, "api_key_id must be positive")
		return 0, false
	}
	return apiKeyID, true
}

func (h *AccountContributionHandler) ensureOwnedContributionRoomAPIKey(c *gin.Context, userID, apiKeyID int64) bool {
	if h.entClient == nil {
		response.Error(c, http.StatusServiceUnavailable, "Contribution room service unavailable")
		return false
	}
	if apiKeyID <= 0 {
		response.BadRequest(c, "api_key_id must be positive")
		return false
	}
	exists, err := h.entClient.APIKey.Query().
		Where(apikey.IDEQ(apiKeyID), apikey.UserIDEQ(userID)).
		Exist(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	if !exists {
		response.NotFound(c, "API key not found")
		return false
	}
	return true
}

func (h *AccountContributionHandler) validateContributionRoomFallbackGroup(ctx context.Context, userID, groupID int64) error {
	if h.apiKeyService == nil {
		return nil
	}
	groups, err := h.apiKeyService.GetAvailableGroups(ctx, userID)
	if err != nil {
		return fmt.Errorf("validate fallback group: %w", err)
	}
	for _, group := range groups {
		if group.ID == groupID {
			return nil
		}
	}
	return fmt.Errorf("fallback group is not available")
}

func validContributionRoomBudget(value float64) bool {
	return value >= 0 && value <= 1000000
}

func validContributionRoomShareConcurrency(value, accountMaximum int) bool {
	return value > 0 && value <= 1000 && (accountMaximum <= 0 || value <= accountMaximum)
}

func contributionModelFamilyMatchesPlatform(family, platform string) bool {
	expected := contributionExpectedModelFamily(platform)
	return expected != "" && strings.EqualFold(strings.TrimSpace(family), expected)
}

func contributionExpectedModelFamily(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case service.PlatformOpenAI:
		return "gpt"
	case service.PlatformAnthropic:
		return "claude"
	case service.PlatformGemini:
		return "gemini"
	case service.PlatformGrok:
		return "grok"
	default:
		return ""
	}
}

func (h *AccountContributionHandler) adminContributionRoom(c *gin.Context) (*dbent.ContributionRoom, bool) {
	roomID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || roomID <= 0 {
		response.BadRequest(c, "invalid contribution room id")
		return nil, false
	}
	room, err := h.loadContributionRoom(c.Request.Context(), roomID)
	if err != nil {
		if dbent.IsNotFound(err) {
			response.NotFound(c, "Contribution room not found")
		} else {
			response.ErrorFrom(c, err)
		}
		return nil, false
	}
	return room, true
}

func parseContributionRoomAccountID(c *gin.Context) (int64, bool) {
	accountID, err := strconv.ParseInt(c.Param("account_id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "invalid account id")
		return 0, false
	}
	return accountID, true
}

func validContributionRoomMultiplier(value float64) bool {
	return value > 0 && value <= 100
}

func normalizeContributionRoomStatus(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	return value, value == contributionRoomStatusActive || value == contributionRoomStatusPaused
}

func normalizeContributionRoomVisibility(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	return value, value == contributionRoomVisibilityOpen || value == contributionRoomVisibilityHide
}
