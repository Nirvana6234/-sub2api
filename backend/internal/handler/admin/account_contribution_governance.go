package admin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const contributionGovernanceSourcePageSize = 500

type contributionGovernanceContributor struct {
	ID       int64  `json:"id"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

type contributionGovernanceShare struct {
	Mode        string     `json:"mode"`
	TotalBudget float64    `json:"total_budget"`
	DailyBudget float64    `json:"daily_budget"`
	UsedTotal   float64    `json:"used_total"`
	UsedToday   float64    `json:"used_today"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type contributionGovernanceHealth struct {
	ErrorMessage string     `json:"error_message,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}

// ContributionGovernanceAccount deliberately has no generic Account or Extra
// field. Governance APIs must remain safe even while legacy credentials are
// still stored alongside runtime account records during this transition.
type ContributionGovernanceAccount struct {
	ID               int64                             `json:"id"`
	Name             string                            `json:"name"`
	Platform         string                            `json:"platform"`
	Type             string                            `json:"type"`
	Status           string                            `json:"status"`
	Schedulable      bool                              `json:"schedulable"`
	Concurrency      int                               `json:"concurrency"`
	Groups           []contributionGovernanceGroup     `json:"groups"`
	SubmittedAt      time.Time                         `json:"submitted_at"`
	Contributor      contributionGovernanceContributor `json:"contributor"`
	Share            contributionGovernanceShare       `json:"share"`
	Health           contributionGovernanceHealth      `json:"health"`
	GovernanceState  string                            `json:"governance_state"`
	GovernanceReason string                            `json:"governance_reason,omitempty"`
}

type contributionGovernanceGroup struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

type ContributionGovernanceSummary struct {
	Total     int `json:"total"`
	Shared    int `json:"shared"`
	Attention int `json:"attention"`
	Paused    int `json:"paused"`
}

type ContributionGovernanceListResponse struct {
	Items    []ContributionGovernanceAccount `json:"items"`
	Total    int                             `json:"total"`
	Page     int                             `json:"page"`
	PageSize int                             `json:"page_size"`
	Summary  ContributionGovernanceSummary   `json:"summary"`
}

type UpdateContributionGovernanceRequest struct {
	Action string `json:"action" binding:"required,oneof=pause resume"`
	Reason string `json:"reason"`
}

// UpdateManagedContributionRequest exposes the same operational controls the
// contributor has, while credentials remain write-only for administrators.
type UpdateManagedContributionRequest struct {
	Name        string   `json:"name"`
	APIKey      *string  `json:"api_key"`
	BaseURL     *string  `json:"base_url"`
	Concurrency *int     `json:"concurrency"`
	Priority    *int     `json:"priority"`
	LoadFactor  *int     `json:"load_factor"`
	GroupIDs    *[]int64 `json:"group_ids"`
	Status      string   `json:"status"`
}

func contributionGovernanceContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxkey.AllowContributionAccountManagement, true)
}

// ListContributions exposes only a deliberately whitelisted governance view.
func (h *AccountHandler) ListContributions(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	if pageSize > 100 {
		pageSize = 100
	}
	platform := strings.TrimSpace(c.Query("platform"))
	status := strings.TrimSpace(c.Query("status"))
	search := strings.TrimSpace(c.Query("search"))
	shareMode := strings.TrimSpace(c.Query("share_mode"))

	accounts, err := h.listContributionGovernanceAccounts(c.Request.Context(), platform, search)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	items := make([]ContributionGovernanceAccount, 0, len(accounts))
	summary := ContributionGovernanceSummary{}
	for i := range accounts {
		account := accounts[i]
		item := contributionGovernanceAccountFromService(&account)
		if !matchesContributionGovernanceStatus(item, status) {
			continue
		}
		if shareMode != "" && item.Share.Mode != shareMode {
			continue
		}
		summary.Total++
		if item.Share.Mode == service.AccountShareModePool {
			summary.Shared++
		}
		if item.GovernanceState == service.AccountContributionGovernancePaused {
			summary.Paused++
		}
		if item.GovernanceState == service.AccountContributionGovernancePaused || item.Health.ErrorMessage != "" || item.Status != service.StatusActive {
			summary.Attention++
		}
		items = append(items, item)
	}

	start := (page - 1) * pageSize
	if start > len(items) {
		start = len(items)
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	response.Success(c, ContributionGovernanceListResponse{
		Items: items[start:end], Total: len(items), Page: page, PageSize: pageSize, Summary: summary,
	})
}

func (h *AccountHandler) GetContribution(c *gin.Context) {
	account, ok := h.contributionGovernanceAccount(c)
	if !ok {
		return
	}
	response.Success(c, contributionGovernanceAccountFromService(account))
}

func (h *AccountHandler) UpdateManagedContribution(c *gin.Context) {
	account, ok := h.contributionGovernanceAccount(c)
	if !ok {
		return
	}
	var req UpdateManagedContributionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Concurrency != nil && (*req.Concurrency < 1 || *req.Concurrency > 1000) {
		response.BadRequest(c, "concurrency must be between 1 and 1000")
		return
	}
	if req.LoadFactor != nil && (*req.LoadFactor < 0 || *req.LoadFactor > 10000) {
		response.BadRequest(c, "load_factor must be between 0 and 10000")
		return
	}
	if req.Status != "" && req.Status != service.StatusActive && req.Status != service.StatusDisabled {
		response.BadRequest(c, "status must be active or disabled")
		return
	}
	if req.GroupIDs != nil {
		if err := h.validateManagedContributionGroups(c.Request.Context(), account.Platform, *req.GroupIDs); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	}
	credentials := map[string]any(nil)
	if req.APIKey != nil || req.BaseURL != nil {
		if account.Type != service.AccountTypeAPIKey {
			response.BadRequest(c, "OAuth credentials cannot be edited here")
			return
		}
		credentials = map[string]any{}
		if req.APIKey != nil {
			if strings.TrimSpace(*req.APIKey) == "" {
				response.BadRequest(c, "api_key cannot be empty")
				return
			}
			credentials["api_key"] = strings.TrimSpace(*req.APIKey)
		}
		if req.BaseURL != nil {
			credentials["base_url"] = strings.TrimRight(strings.TrimSpace(*req.BaseURL), "/")
		}
	}
	updated, err := h.adminService.UpdateAccount(contributionGovernanceContext(c.Request.Context()), account.ID, &service.UpdateAccountInput{
		Name: strings.TrimSpace(req.Name), Credentials: credentials, Concurrency: req.Concurrency,
		Priority: req.Priority, LoadFactor: req.LoadFactor, GroupIDs: req.GroupIDs, Status: req.Status,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, contributionGovernanceAccountFromService(updated))
}

func (h *AccountHandler) validateManagedContributionGroups(ctx context.Context, platform string, groupIDs []int64) error {
	return h.validateManagedContributionGroupsForType(ctx, platform, "apikey", groupIDs)
}

func (h *AccountHandler) validateManagedContributionGroupsForType(ctx context.Context, platform, accountType string, groupIDs []int64) error {
	for _, groupID := range groupIDs {
		group, err := h.adminService.GetGroup(ctx, groupID)
		if err != nil {
			return err
		}
		if group == nil || !strings.EqualFold(group.Platform, platform) {
			return fmt.Errorf("group %d does not support the %s platform", groupID, platform)
		}
		if group.RequireOAuthOnly && accountType != service.AccountTypeOAuth && accountType != service.AccountTypeSetupToken {
			return fmt.Errorf("group %d only accepts OAuth accounts", groupID)
		}
	}
	return nil
}

// prepareManagedContributionCreate applies the same owner metadata as the
// user self-service flow while retaining the full administrator account form.
func (h *AccountHandler) prepareManagedContributionCreate(ctx context.Context, contributorUserID *int64, platform, accountType string, groupIDs []int64, extra map[string]any) (map[string]any, error) {
	return prepareManagedContributionExtra(ctx, h.adminService, contributorUserID, platform, accountType, groupIDs, extra)
}

func prepareManagedContributionExtra(ctx context.Context, adminService service.AdminService, contributorUserID *int64, platform, accountType string, groupIDs []int64, extra map[string]any) (map[string]any, error) {
	if contributorUserID == nil {
		return extra, nil
	}
	if *contributorUserID <= 0 {
		return nil, fmt.Errorf("contributor_user_id must be positive")
	}
	user, err := adminService.GetUser(ctx, *contributorUserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("contributor user not found")
	}
	for _, groupID := range groupIDs {
		group, groupErr := adminService.GetGroup(ctx, groupID)
		if groupErr != nil {
			return nil, groupErr
		}
		if group == nil || !strings.EqualFold(group.Platform, strings.ToLower(strings.TrimSpace(platform))) {
			return nil, fmt.Errorf("group %d does not support the %s platform", groupID, platform)
		}
		if group.RequireOAuthOnly && accountType != service.AccountTypeOAuth && accountType != service.AccountTypeSetupToken {
			return nil, fmt.Errorf("group %d only accepts OAuth accounts", groupID)
		}
	}
	merged := make(map[string]any, len(extra)+6)
	for key, value := range extra {
		merged[key] = value
	}
	for key, value := range managedContributionExtra(user, time.Now()) {
		merged[key] = value
	}
	return merged, nil
}

func managedContributionExtra(user *service.User, now time.Time) map[string]any {
	extra := map[string]any{
		service.AccountContributionSourceKey:      service.AccountContributionSourceValue,
		service.AccountContributionSubmittedAtKey: now.UTC().Format(time.RFC3339),
		service.AccountShareModeKey:               service.AccountShareModePrivate,
	}
	if user != nil {
		extra[service.AccountContributorUserIDKey] = user.ID
		extra[service.AccountContributorEmailKey] = strings.TrimSpace(user.Email)
		extra[service.AccountContributorUsernameKey] = strings.TrimSpace(user.Username)
	}
	return extra
}

// GetContributionUsageSummary exposes only runtime usage metrics for a
// contributed account. It deliberately omits account credentials and mapping
// configuration, consistent with the rest of the governance API.
func (h *AccountHandler) GetContributionUsageSummary(c *gin.Context) {
	account, ok := h.contributionGovernanceAccount(c)
	if !ok {
		return
	}
	if h.accountUsageService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account usage service unavailable")
		return
	}

	now := timezone.Now()
	endTime := timezone.StartOfDay(now.AddDate(0, 0, 1))
	startTime := timezone.StartOfDay(now.AddDate(0, 0, -29))
	stats, err := h.accountUsageService.GetAccountUsageStats(c.Request.Context(), account.ID, startTime, endTime)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// API Key accounts do not have a portable upstream usage-window endpoint.
	// Keep this governance view useful by returning the local account rollup
	// without probing the provider, which would otherwise yield a misleading
	// unsupported-query error.
	usage := &service.UsageInfo{Source: "local"}
	if account.Type != service.AccountTypeAPIKey {
		usage, err = h.accountUsageService.GetUsage(c.Request.Context(), account.ID, c.Query("force") == "true")
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	response.Success(c, gin.H{
		"upstream": usage,
		"stats":    stats,
		"days":     30,
	})
}

func (h *AccountHandler) UpdateContributionGovernance(c *gin.Context) {
	account, ok := h.contributionGovernanceAccount(c)
	if !ok {
		return
	}
	var req UpdateContributionGovernanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	updates := map[string]any{
		service.AccountContributionGovernanceUpdatedAtKey: time.Now().UTC().Format(time.RFC3339),
		service.AccountContributionGovernanceUpdatedByKey: getAdminIDFromContext(c),
	}
	switch req.Action {
	case "pause":
		reason := strings.TrimSpace(req.Reason)
		if reason == "" {
			response.BadRequest(c, "reason is required when pausing a shared contribution")
			return
		}
		updates[service.AccountContributionGovernanceStateKey] = service.AccountContributionGovernancePaused
		updates[service.AccountContributionGovernanceReasonKey] = reason
	case "resume":
		updates[service.AccountContributionGovernanceStateKey] = service.AccountContributionGovernanceActive
		updates[service.AccountContributionGovernanceReasonKey] = ""
	default:
		response.BadRequest(c, "unsupported governance action")
		return
	}

	ctx := contributionGovernanceContext(c.Request.Context())
	if err := h.adminService.UpdateAccountExtra(ctx, account.ID, updates); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	updated, err := h.adminService.GetAccount(ctx, account.ID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, contributionGovernanceAccountFromService(updated))
}

func (h *AccountHandler) TestContribution(c *gin.Context) {
	account, ok := h.contributionGovernanceAccount(c)
	if !ok {
		return
	}
	if h.accountTestService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account test service unavailable")
		return
	}
	var req struct {
		ModelID string `json:"model_id"`
	}
	_ = c.ShouldBindJSON(&req)
	result, err := h.accountTestService.RunTestBackground(c.Request.Context(), account.ID, strings.TrimSpace(req.ModelID))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// DeleteContribution permanently removes a user-contributed account. The
// account lookup above proves it is a contribution record before the generic
// account deletion path removes scheduler and relational references.
func (h *AccountHandler) DeleteContribution(c *gin.Context) {
	account, ok := h.contributionGovernanceAccount(c)
	if !ok {
		return
	}
	if err := h.adminService.DeleteAccount(contributionGovernanceContext(c.Request.Context()), account.ID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "contribution account deleted"})
}

func (h *AccountHandler) contributionGovernanceAccount(c *gin.Context) (*service.Account, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid contribution account ID")
		return nil, false
	}
	account, err := h.adminService.GetAccount(contributionGovernanceContext(c.Request.Context()), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, false
	}
	if account == nil || account.ContributorUserID() <= 0 {
		response.NotFound(c, "Contribution account not found")
		return nil, false
	}
	return account, true
}

func (h *AccountHandler) listContributionGovernanceAccounts(ctx context.Context, platform, search string) ([]service.Account, error) {
	ctx = contributionGovernanceContext(ctx)
	page := 1
	accounts := make([]service.Account, 0)
	for {
		items, total, err := h.adminService.ListAccounts(ctx, page, contributionGovernanceSourcePageSize, platform, "", "", search, 0, "", "created_at", "desc")
		if err != nil {
			return nil, err
		}
		for i := range items {
			if items[i].ContributorUserID() > 0 {
				accounts = append(accounts, items[i])
			}
		}
		if len(items) == 0 || int64(page*contributionGovernanceSourcePageSize) >= total {
			return accounts, nil
		}
		page++
	}
}

func matchesContributionGovernanceStatus(item ContributionGovernanceAccount, status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return true
	}
	if status == service.AccountContributionGovernancePaused {
		return item.GovernanceState == service.AccountContributionGovernancePaused
	}
	if status == service.AccountContributionGovernanceActive {
		return item.Status == service.StatusActive && item.GovernanceState != service.AccountContributionGovernancePaused
	}
	return strings.EqualFold(item.Status, status)
}

func contributionGovernanceAccountFromService(account *service.Account) ContributionGovernanceAccount {
	if account == nil {
		return ContributionGovernanceAccount{}
	}
	groups := make([]contributionGovernanceGroup, 0, len(account.Groups))
	for _, group := range account.Groups {
		if group != nil {
			groups = append(groups, contributionGovernanceGroup{ID: group.ID, Name: group.Name, Platform: group.Platform})
		}
	}
	submittedAt := account.CreatedAt
	if value := strings.TrimSpace(account.GetExtraString(service.AccountContributionSubmittedAtKey)); value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			submittedAt = parsed
		}
	}
	governanceState := strings.TrimSpace(account.GetExtraString(service.AccountContributionGovernanceStateKey))
	if governanceState == "" {
		governanceState = service.AccountContributionGovernanceActive
	}
	return ContributionGovernanceAccount{
		ID:          account.ID,
		Name:        account.Name,
		Platform:    account.Platform,
		Type:        account.Type,
		Status:      account.Status,
		Schedulable: account.Schedulable,
		Concurrency: account.Concurrency,
		Groups:      groups,
		SubmittedAt: submittedAt,
		Contributor: contributionGovernanceContributor{
			ID:       account.ContributorUserID(),
			Email:    maskContributionEmail(account.GetExtraString(service.AccountContributorEmailKey)),
			Username: account.GetExtraString(service.AccountContributorUsernameKey),
		},
		Share: contributionGovernanceShare{
			Mode:        account.GetExtraString(service.AccountShareModeKey),
			TotalBudget: contributionExtraNumber(account.Extra, service.AccountShareTotalBudgetKey),
			DailyBudget: contributionExtraNumber(account.Extra, service.AccountShareDailyBudgetKey),
			UsedTotal:   contributionExtraNumber(account.Extra, service.AccountShareUsedTotalKey),
			UsedToday:   contributionExtraNumber(account.Extra, service.AccountShareUsedTodayKey),
			ExpiresAt:   contributionExtraTime(account.GetExtraString(service.AccountShareExpiresAtKey)),
		},
		Health:           contributionGovernanceHealth{ErrorMessage: account.ErrorMessage, LastUsedAt: account.LastUsedAt},
		GovernanceState:  governanceState,
		GovernanceReason: account.GetExtraString(service.AccountContributionGovernanceReasonKey),
	}
}

func contributionExtraNumber(extra map[string]any, key string) float64 {
	if extra == nil {
		return 0
	}
	switch value := extra[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed
	default:
		return 0
	}
}

func contributionExtraTime(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	return &parsed
}

func maskContributionEmail(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return ""
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "***"
	}
	local := []rune(parts[0])
	if len(local) == 1 {
		return string(local) + "***"
	}
	return string(local[:1]) + "***@" + parts[1]
}
