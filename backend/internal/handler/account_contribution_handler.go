package handler

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/contributionroomaccount"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	maxAccountContributionItems        = 10
	accountContributionListPageSize    = 500
	accountContributionDayKey          = "submitted_day"
	minimumContributionPoolConcurrency = 30
	minimumContributionPoolPriority    = 20
	defaultContributionPriority        = 0
)

var (
	accountContributionTZ    = time.FixedZone("Asia/Shanghai", 8*60*60)
	accountContributionLocks sync.Map
)

type AccountContributionHandler struct {
	userService         *service.UserService
	adminService        service.AdminService
	accountUsageService *service.AccountUsageService
	accountTestService  *service.AccountTestService
	accountTestRunner   accountContributionTestRunner
	rateLimitService    *service.RateLimitService
	apiKeyService       *service.APIKeyService
	proxyProber         service.ProxyExitInfoProber
	oauthService        *service.OAuthService
	openaiOAuthService  *service.OpenAIOAuthService
	entClient           *dbent.Client
}

// ProvideAccountContributionHandler supplies the user contribution surface with
// the narrowly-scoped authorization services used by contribution imports.
func ProvideAccountContributionHandler(
	userService *service.UserService,
	adminService service.AdminService,
	accountUsageService *service.AccountUsageService,
	accountTestService *service.AccountTestService,
	rateLimitService *service.RateLimitService,
	apiKeyService *service.APIKeyService,
	proxyProber service.ProxyExitInfoProber,
	entClient *dbent.Client,
	oauthService *service.OAuthService,
	openaiOAuthService *service.OpenAIOAuthService,
) *AccountContributionHandler {
	h := NewAccountContributionHandler(userService, adminService, accountUsageService, accountTestService, rateLimitService, apiKeyService, proxyProber, entClient)
	h.oauthService = oauthService
	h.openaiOAuthService = openaiOAuthService
	return h
}

type accountContributionTestRunner interface {
	RunTestBackground(ctx context.Context, accountID int64, modelID string) (*service.ScheduledTestResult, error)
}

func NewAccountContributionHandler(
	userService *service.UserService,
	adminService service.AdminService,
	accountUsageService *service.AccountUsageService,
	accountTestService *service.AccountTestService,
	rateLimitService *service.RateLimitService,
	apiKeyService *service.APIKeyService,
	proxyProber service.ProxyExitInfoProber,
	entClient *dbent.Client,
) *AccountContributionHandler {
	return &AccountContributionHandler{
		userService: userService, adminService: adminService,
		accountUsageService: accountUsageService,
		accountTestService:  accountTestService, accountTestRunner: accountTestService,
		rateLimitService: rateLimitService, apiKeyService: apiKeyService, entClient: entClient,
		proxyProber: proxyProber,
	}
}

// SubmitAccountContributionRequest supports both the existing Codex OAuth
// session submission and direct upstream API-key accounts.
type SubmitAccountContributionRequest struct {
	Mode               string   `json:"mode"`
	Content            string   `json:"content"`
	Contents           []string `json:"contents"`
	Name               string   `json:"name"`
	Platform           string   `json:"platform"`
	APIKey             string   `json:"api_key"`
	BaseURL            string   `json:"base_url"`
	Concurrency        *int     `json:"concurrency"`
	Priority           *int     `json:"priority"`
	LoadFactor         *int     `json:"load_factor"`
	GroupIDs           []int64  `json:"group_ids"`
	PoolGroupID        *int64   `json:"pool_group_id"`
	AutoPauseOnExpired *bool    `json:"auto_pause_on_expired"`
	TestModelID        string   `json:"test_model_id"`
	ProxyID            *int64   `json:"proxy_id"`
}

type UpdateAccountContributionRequest struct {
	Name        string   `json:"name"`
	APIKey      *string  `json:"api_key"`
	BaseURL     *string  `json:"base_url"`
	Concurrency *int     `json:"concurrency"`
	Priority    *int     `json:"priority"`
	LoadFactor  *int     `json:"load_factor"`
	GroupIDs    *[]int64 `json:"group_ids"`
	PoolGroupID *int64   `json:"pool_group_id"`
	ProxyID     *int64   `json:"proxy_id"`
	Status      string   `json:"status"`
}

type AccountContributionResult struct {
	Total     int                                      `json:"total"`
	Created   int                                      `json:"created"`
	Failed    int                                      `json:"failed"`
	Skipped   int                                      `json:"skipped"`
	Limit     int                                      `json:"limit"`
	Used      int                                      `json:"used"`
	Remaining int                                      `json:"remaining"`
	Items     []AccountContributionResultItem          `json:"items"`
	Warnings  []adminhandler.CodexSessionImportMessage `json:"warnings,omitempty"`
}

type AccountContributionResultItem struct {
	Index     int    `json:"index"`
	Name      string `json:"name,omitempty"`
	AccountID int64  `json:"account_id,omitempty"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
}

type AccountContributionList struct {
	Items       []*dto.Account                   `json:"items"`
	Total       int                              `json:"total"`
	Page        int                              `json:"page"`
	Limit       int                              `json:"limit"`
	Wallet      *service.ContributionWallet      `json:"wallet"`
	IncomeRates *service.ContributionIncomeRates `json:"income_rates"`
}

func (h *AccountContributionHandler) Submit(c *gin.Context) {
	_, user, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	var req SubmitAccountContributionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Concurrency != nil && (*req.Concurrency < 1 || *req.Concurrency > 1000) {
		response.BadRequest(c, "concurrency must be between 1 and 1000")
		return
	}
	if req.LoadFactor != nil && *req.LoadFactor > 10000 {
		response.BadRequest(c, "load_factor must be no more than 10000")
		return
	}
	if req.ProxyID != nil {
		if *req.ProxyID < 0 {
			response.BadRequest(c, "proxy_id must be zero for direct connection or a positive user proxy id")
			return
		}
		if *req.ProxyID == 0 {
			req.ProxyID = nil
		} else {
			if _, err := h.getOwnedContributionProxy(c.Request.Context(), user.ID, *req.ProxyID, true); err != nil {
				response.BadRequest(c, err.Error())
				return
			}
		}
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		if strings.TrimSpace(req.APIKey) != "" {
			mode = "api_key"
		} else {
			mode = "oauth"
		}
	}
	platform := service.PlatformOpenAI
	accountType := service.AccountTypeOAuth
	if mode == "api" || mode == "apikey" || mode == "api_key" {
		platform = normalizeContributionPlatform(req.Platform)
		accountType = service.AccountTypeAPIKey
		if platform == "" {
			response.BadRequest(c, "unsupported platform; use anthropic, openai, gemini, or grok")
			return
		}
	}
	groupIDs, err := h.resolveContributionGroupBinding(c.Request.Context(), platform, accountType, req.GroupIDs, req.PoolGroupID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.GroupIDs = groupIDs
	if err := validateContributionPoolPriority(req.PoolGroupID != nil && *req.PoolGroupID > 0, contributionPriority(req)); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := validateContributionPoolConcurrency(req.PoolGroupID != nil && *req.PoolGroupID > 0, contributionConcurrency(req)); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	result := AccountContributionResult{Limit: 0, Used: 0, Remaining: -1}
	unlock := lockAccountContributionUser(user.ID)
	defer unlock()
	existingAccounts, err := h.listByContributor(c.Request.Context(), user.ID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accountNames := contributionAccountNameSet(existingAccounts)

	switch mode {
	case "api", "apikey", "api_key":
		result.Total = 1
		name := contributionAPIKeyName(req)
		item := duplicateContributionItem(1, name)
		if reserveContributionAccountName(accountNames, name) {
			item = h.createAPIKeyContribution(c.Request.Context(), req, user)
		}
		result.Items = []AccountContributionResultItem{item}
		countContributionResultItem(&result, item)
	case "oauth", "codex":
		parseReq := adminhandler.CodexSessionImportRequest{
			Content: req.Content, Contents: req.Contents, Name: req.Name,
			Concurrency: req.Concurrency, AutoPauseOnExpired: req.AutoPauseOnExpired,
		}
		accounts, warnings, parseErr := adminhandler.ParseCodexSessionAccounts(parseReq)
		if parseErr != nil {
			response.BadRequest(c, parseErr.Error())
			return
		}
		if len(accounts) > maxAccountContributionItems {
			response.BadRequest(c, fmt.Sprintf("at most %d accounts can be submitted at once", maxAccountContributionItems))
			return
		}
		result.Total, result.Warnings = len(accounts), warnings
		result.Items = make([]AccountContributionResultItem, 0, len(accounts))
		for _, parsed := range accounts {
			item := AccountContributionResultItem{Index: parsed.Index, Name: parsed.Name, Status: "failed"}
			if parsed.ErrorMessage != "" {
				item.Message = parsed.ErrorMessage
			} else if !reserveContributionAccountName(accountNames, parsed.Name) {
				item = duplicateContributionItem(parsed.Index, parsed.Name)
			} else {
				item = h.createOAuthContribution(c.Request.Context(), parsed, user, req)
			}
			countContributionResultItem(&result, item)
			result.Items = append(result.Items, item)
		}
	default:
		response.BadRequest(c, "mode must be oauth or api_key")
		return
	}
	response.Success(c, result)
}

func (h *AccountContributionHandler) List(c *gin.Context) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	page := parsePositiveQueryInt(c.Query("page"), 1)
	limit := parsePositiveQueryInt(c.Query("limit"), 20)
	if limit > 100 {
		limit = 100
	}
	accounts, err := h.listByContributor(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	start := (page - 1) * limit
	if start > len(accounts) {
		start = len(accounts)
	}
	end := start + limit
	if end > len(accounts) {
		end = len(accounts)
	}
	items := make([]*dto.Account, 0, end-start)
	for i := start; i < end; i++ {
		items = append(items, dto.AccountFromService(&accounts[i]))
	}
	wallet, err := h.userService.GetContributionWallet(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, AccountContributionList{
		Items: items, Total: len(accounts), Page: page, Limit: limit,
		Wallet: wallet, IncomeRates: h.userService.GetContributionIncomeRates(c.Request.Context()),
	})
}

// ListGroups returns the active groups that a contributor may explicitly bind
// to an account. It intentionally exposes only the user-safe group DTO; group
// routing configuration and model mappings remain administrator-only.
func (h *AccountContributionHandler) ListGroups(c *gin.Context) {
	if _, _, ok := h.authenticatedUser(c); !ok {
		return
	}
	groups, err := h.adminService.GetAllGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]*dto.Group, 0, len(groups))
	for i := range groups {
		items = append(items, dto.GroupFromService(&groups[i]))
	}
	response.Success(c, items)
}

// ListPoolGroups exposes only groups the administrator has explicitly opened
// for contributed accounts. The multiplier in this response is informational;
// billing always resolves it from the selected group at request time.
func (h *AccountContributionHandler) ListPoolGroups(c *gin.Context) {
	if _, _, ok := h.authenticatedUser(c); !ok {
		return
	}
	groups, err := h.adminService.GetAllGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]*dto.Group, 0, len(groups))
	for i := range groups {
		if groups[i].AllowContributionPool {
			items = append(items, dto.GroupFromService(&groups[i]))
		}
	}
	response.Success(c, items)
}

func (h *AccountContributionHandler) Update(c *gin.Context) {
	account, ok := h.ownedAccount(c)
	if !ok {
		return
	}
	var req UpdateAccountContributionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Concurrency != nil && (*req.Concurrency < 1 || *req.Concurrency > 1000) {
		response.BadRequest(c, "concurrency must be between 1 and 1000")
		return
	}
	if req.LoadFactor != nil && *req.LoadFactor > 10000 {
		response.BadRequest(c, "load_factor must be no more than 10000")
		return
	}
	if req.Status != "" && req.Status != service.StatusActive && req.Status != service.StatusDisabled {
		response.BadRequest(c, "status must be active or disabled")
		return
	}
	effectiveGroupIDs := req.GroupIDs
	if req.PoolGroupID != nil {
		groupIDs, err := h.resolveContributionGroupBinding(c.Request.Context(), account.Platform, account.Type, valueOrEmpty(req.GroupIDs), req.PoolGroupID)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		effectiveGroupIDs = &groupIDs
		if *req.PoolGroupID > 0 && h.entClient != nil {
			inRoom, err := h.entClient.ContributionRoomAccount.Query().Where(contributionroomaccount.AccountIDEQ(account.ID)).Exist(c.Request.Context())
			if err != nil {
				response.ErrorFrom(c, err)
				return
			}
			if inRoom {
				response.BadRequest(c, "remove the account from its contribution room before joining an administrator pool")
				return
			}
		}
	} else if account.IsSharedPoolAccount() && req.GroupIDs != nil {
		response.BadRequest(c, "select a pool group to change an account already in the administrator pool")
		return
	}
	if effectiveGroupIDs != nil {
		if err := h.validateContributionGroupSelection(c.Request.Context(), account.Platform, account.Type, *effectiveGroupIDs); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		if len(*effectiveGroupIDs) > 0 {
			if err := h.ensureContributionAccountVerified(c.Request.Context(), account); err != nil {
				response.BadRequest(c, err.Error())
				return
			}
		}
	}
	isPoolAccount := account.IsSharedPoolAccount()
	if req.PoolGroupID != nil {
		isPoolAccount = *req.PoolGroupID > 0
	}
	priority := defaultContributionPriority
	if isPoolAccount {
		priority = account.Priority
		if req.Priority != nil {
			priority = *req.Priority
		}
	}
	if err := validateContributionPoolPriority(isPoolAccount, priority); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	concurrency := account.Concurrency
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	if err := validateContributionPoolConcurrency(isPoolAccount, concurrency); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	proxyChanged := false
	if req.ProxyID != nil {
		if *req.ProxyID < 0 {
			response.BadRequest(c, "proxy_id must be zero for direct connection or a positive user proxy id")
			return
		}
		if *req.ProxyID > 0 {
			if _, err := h.getOwnedContributionProxy(c.Request.Context(), account.ContributorUserID(), *req.ProxyID, true); err != nil {
				response.BadRequest(c, err.Error())
				return
			}
		}
		currentProxyID := int64(0)
		if account.ProxyID != nil {
			currentProxyID = *account.ProxyID
		}
		proxyChanged = currentProxyID != *req.ProxyID
	}
	credentials := map[string]any(nil)
	credentialInfoChanged := req.APIKey != nil || req.BaseURL != nil
	connectionInfoChanged := credentialInfoChanged || proxyChanged
	if credentialInfoChanged {
		if account.Type != service.AccountTypeAPIKey {
			response.BadRequest(c, "OAuth credentials cannot be edited here; submit a replacement session instead")
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
	extra := cloneExtra(account.Extra)
	if req.PoolGroupID != nil {
		if *req.PoolGroupID > 0 {
			applyContributionPoolPolicy(extra)
		} else {
			applyPrivateContributionPolicy(extra)
		}
	}
	if connectionInfoChanged {
		// Connection changes invalidate the proof attached to the old endpoint.
		// Clear room eligibility before updating the secret so a failed account
		// update can only leave the account in the safer, pending-test state.
		if err := h.invalidateContributionVerification(c.Request.Context(), account.ID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	updated, err := h.adminService.UpdateAccount(c.Request.Context(), account.ID, &service.UpdateAccountInput{
		Name: strings.TrimSpace(req.Name), Credentials: credentials,
		Concurrency: req.Concurrency, Priority: &priority, LoadFactor: req.LoadFactor,
		Status: req.Status, Extra: extra, GroupIDs: effectiveGroupIDs, ProxyID: req.ProxyID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AccountFromService(updated))
}

// GetUsageSummary provides a contributor with the read-only runtime metrics of
// an account they own: current upstream rolling windows and the local 30-day
// usage rollup. It never returns credentials or model mapping configuration.
func (h *AccountContributionHandler) GetUsageSummary(c *gin.Context) {
	account, ok := h.ownedAccount(c)
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

	// API Key providers do not share a portable upstream quota-window API.
	// Return the local account rollup instead of attempting an unsupported
	// probe, so contributors can always inspect meaningful usage data.
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

func (h *AccountContributionHandler) Delete(c *gin.Context) {
	account, ok := h.ownedAccount(c)
	if !ok {
		return
	}
	if err := h.adminService.DeleteAccount(c.Request.Context(), account.ID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *AccountContributionHandler) Test(c *gin.Context) {
	account, ok := h.ownedAccount(c)
	if !ok {
		return
	}
	var req struct {
		ModelID string `json:"model_id"`
	}
	_ = c.ShouldBindJSON(&req)
	modelID := contributionVerificationTestModel(account, req.ModelID)
	result, err := h.accountTestRunner.RunTestBackground(c.Request.Context(), account.ID, modelID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if result != nil && result.Status == "success" && h.rateLimitService != nil {
		_, _ = h.rateLimitService.RecoverAccountAfterSuccessfulTest(c.Request.Context(), account.ID)
	}
	if result != nil && result.Status == "success" {
		if err := h.restoreContributionScheduling(c.Request.Context(), account.ID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	if err := h.recordContributionVerification(c.Request.Context(), account, modelID, result); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountContributionHandler) GetAvailableModels(c *gin.Context) {
	account, ok := h.ownedAccount(c)
	if !ok {
		return
	}
	response.Success(c, adminhandler.AvailableModelsForAccount(account))
}

func (h *AccountContributionHandler) TestStream(c *gin.Context) {
	account, ok := h.ownedAccount(c)
	if !ok {
		return
	}
	if h.accountTestService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account test service unavailable")
		return
	}
	var req struct {
		ModelID string `json:"model_id"`
		Prompt  string `json:"prompt"`
		Mode    string `json:"mode"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.accountTestService.TestAccountConnection(c, account.ID, req.ModelID, req.Prompt, req.Mode); err != nil {
		return
	}
	if h.rateLimitService != nil {
		_, _ = h.rateLimitService.RecoverAccountAfterSuccessfulTest(c.Request.Context(), account.ID)
	}
}

func (h *AccountContributionHandler) authenticatedUser(c *gin.Context) (middleware2.AuthSubject, *service.User, bool) {
	if h == nil || h.userService == nil || h.adminService == nil || h.accountTestRunner == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account contribution service unavailable")
		return middleware2.AuthSubject{}, nil, false
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return middleware2.AuthSubject{}, nil, false
	}
	user, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return middleware2.AuthSubject{}, nil, false
	}
	ctx := c.Request.Context()
	if _, ok := ctx.Value(ctxkey.UserID).(int64); !ok {
		ctx = context.WithValue(ctx, ctxkey.UserID, subject.UserID)
	}
	// The general admin account surface is intentionally closed to contributed
	// resources. Owner actions use this narrow, internal request capability.
	ctx = context.WithValue(ctx, ctxkey.AllowContributionAccountManagement, true)
	c.Request = c.Request.WithContext(ctx)
	return subject, user, true
}

func (h *AccountContributionHandler) ownedAccount(c *gin.Context) (*service.Account, bool) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return nil, false
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid account id")
		return nil, false
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, false
	}
	if account == nil || !account.IsContributedBy(subject.UserID) {
		response.Error(c, http.StatusForbidden, "You can only manage accounts you submitted")
		return nil, false
	}
	return account, true
}

func (h *AccountContributionHandler) createAPIKeyContribution(ctx context.Context, req SubmitAccountContributionRequest, user *service.User) AccountContributionResultItem {
	item := AccountContributionResultItem{Index: 1, Name: contributionAPIKeyName(req), Status: "failed"}
	platform := normalizeContributionPlatform(req.Platform)
	if platform == "" {
		item.Message = "unsupported platform; use anthropic, openai, gemini, or grok"
		return item
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		item.Message = "api_key is required"
		return item
	}
	credentials := map[string]any{"api_key": apiKey}
	if baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/"); baseURL != "" {
		credentials["base_url"] = baseURL
	}
	concurrency := 30
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	account, err := h.adminService.CreateAccount(ctx, &service.CreateAccountInput{
		Name: item.Name, Platform: platform, Type: service.AccountTypeAPIKey,
		Credentials: credentials, Extra: contributionExtra(nil, user, time.Now(), req),
		Concurrency: concurrency, Priority: contributionPriority(req), LoadFactor: contributionLoadFactor(req),
		GroupIDs: req.GroupIDs, ProxyID: req.ProxyID, SkipDefaultGroupBind: true,
	})
	if err != nil {
		item.Message = err.Error()
		return item
	}
	return createdContributionItem(account, item)
}

func (h *AccountContributionHandler) createOAuthContribution(ctx context.Context, parsed adminhandler.ParsedCodexSessionAccount, user *service.User, req SubmitAccountContributionRequest) AccountContributionResultItem {
	item := AccountContributionResultItem{Index: parsed.Index, Name: parsed.Name, Status: "failed"}
	concurrency := 30
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	account, err := h.adminService.CreateAccount(ctx, &service.CreateAccountInput{
		Name: parsed.Name, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: parsed.Credentials, Extra: contributionExtra(parsed.Extra, user, time.Now(), req),
		Concurrency: concurrency, Priority: contributionPriority(req), LoadFactor: contributionLoadFactor(req),
		GroupIDs: req.GroupIDs, ExpiresAt: parsed.ExpiresAt, ProxyID: req.ProxyID,
		AutoPauseOnExpired:   parsed.AutoPauseOnExpired,
		SkipDefaultGroupBind: true,
	})
	if err != nil {
		item.Message = err.Error()
		return item
	}
	return createdContributionItem(account, item)
}

func (h *AccountContributionHandler) testCreatedContribution(ctx context.Context, account *service.Account, modelID string, item AccountContributionResultItem, allowAdminProxyFallback bool) AccountContributionResultItem {
	item.Status = "failed"
	if account == nil {
		item.Message = "account creation returned empty result"
		return item
	}
	item.AccountID = account.ID
	modelID = contributionVerificationTestModel(account, modelID)
	result, err := h.accountTestRunner.RunTestBackground(ctx, account.ID, modelID)
	proxyName := ""
	if allowAdminProxyFallback && !contributionTestSucceeded(result, err) && contributionNetworkFailure(result, err) {
		var proxyErr error
		account, result, proxyName, proxyErr = h.testContributionThroughActiveProxy(ctx, account, modelID, result)
		if proxyErr != nil && err == nil {
			err = proxyErr
		}
	}
	if result != nil {
		item.LatencyMs = result.LatencyMs
	}
	if !contributionTestSucceeded(result, err) {
		_ = h.recordContributionVerification(ctx, account, modelID, result)
		item.Message = contributionTestFailureMessage(result, err)
		_ = h.adminService.DeleteAccount(ctx, account.ID)
		return item
	}
	if h.rateLimitService != nil {
		_, _ = h.rateLimitService.RecoverAccountAfterSuccessfulTest(ctx, account.ID)
	}
	if err := h.restoreContributionScheduling(ctx, account.ID); err != nil {
		item.Message = "account test passed but scheduling could not be restored"
		return item
	}
	if err := h.recordContributionVerification(ctx, account, modelID, result); err != nil {
		item.Message = "account test passed but verification could not be recorded"
		return item
	}
	item.Status = "created"
	item.Message = "test passed and account added"
	if proxyName != "" {
		item.Message = fmt.Sprintf("test passed through proxy %q and account added", proxyName)
	}
	return item
}

// restoreContributionScheduling makes a successful contributor verification
// immediately usable in the owner's room. Runtime cooldown recovery does not
// change the persistent schedulable switch, so it is restored separately.
func (h *AccountContributionHandler) restoreContributionScheduling(ctx context.Context, accountID int64) error {
	if h == nil || h.adminService == nil || accountID <= 0 {
		return fmt.Errorf("account scheduling service unavailable")
	}
	_, err := h.adminService.SetAccountSchedulable(ctx, accountID, true)
	return err
}

// testContributionThroughActiveProxy retries only a transport-level direct
// failure. The first active, non-expired proxy that passes is persisted on the
// account, so subsequent requests continue to use that same route.
func (h *AccountContributionHandler) testContributionThroughActiveProxy(
	ctx context.Context,
	account *service.Account,
	modelID string,
	lastResult *service.ScheduledTestResult,
) (*service.Account, *service.ScheduledTestResult, string, error) {
	if h == nil || h.adminService == nil || h.accountTestRunner == nil || account == nil {
		return account, lastResult, "", nil
	}
	proxies, err := h.adminService.GetAllProxies(ctx)
	if err != nil {
		return account, lastResult, "", err
	}
	sort.SliceStable(proxies, func(i, j int) bool { return proxies[i].ID < proxies[j].ID })
	now := time.Now()
	for i := range proxies {
		proxy := proxies[i]
		if !proxy.IsActive() || proxy.IsExpired(now) {
			continue
		}
		proxyID := proxy.ID
		updated, updateErr := h.adminService.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{ProxyID: &proxyID})
		if updateErr != nil {
			continue
		}
		candidateResult, testErr := h.accountTestRunner.RunTestBackground(ctx, account.ID, modelID)
		if contributionTestSucceeded(candidateResult, testErr) {
			if updated != nil {
				account = updated
			} else {
				account.ProxyID = &proxyID
				account.Proxy = &proxy
			}
			return account, candidateResult, proxy.Name, nil
		}
		lastResult = candidateResult
	}
	return account, lastResult, "", nil
}

func contributionTestSucceeded(result *service.ScheduledTestResult, err error) bool {
	return err == nil && result != nil && result.Status == "success"
}

func contributionTestFailureMessage(result *service.ScheduledTestResult, err error) string {
	if result != nil && strings.TrimSpace(result.ErrorMessage) != "" {
		return result.ErrorMessage
	}
	if err != nil {
		return err.Error()
	}
	return "account test failed"
}

func contributionNetworkFailure(result *service.ScheduledTestResult, err error) bool {
	message := contributionTestFailureMessage(result, err)
	message = strings.ToLower(message)
	for _, signal := range []string{
		"context deadline exceeded", "connection refused", "connection reset", "connection aborted",
		"dial tcp", "network is unreachable", "network unreachable", "no such host", "i/o timeout",
		"tls handshake", "tls: ", "unexpected eof", "transport error", "temporarily unavailable",
	} {
		if strings.Contains(message, signal) {
			return true
		}
	}
	return false
}

func (h *AccountContributionHandler) countDailyContributions(ctx context.Context, userID int64, now time.Time) (int, error) {
	accounts, err := h.listByContributor(ctx, userID)
	if err != nil {
		return 0, err
	}
	day := contributionDay(now)
	count := 0
	for i := range accounts {
		if accountDay(&accounts[i]) == day {
			count++
		}
	}
	return count, nil
}

func (h *AccountContributionHandler) listByContributor(ctx context.Context, userID int64) ([]service.Account, error) {
	result := make([]service.Account, 0)
	for page := 1; ; page++ {
		accounts, total, err := h.adminService.ListAccounts(ctx, page, accountContributionListPageSize, "", "", "", "", 0, "", "created_at", "desc")
		if err != nil {
			return nil, err
		}
		for i := range accounts {
			if accounts[i].IsContributedBy(userID) {
				result = append(result, accounts[i])
			}
		}
		if len(accounts) == 0 || int64(page*accountContributionListPageSize) >= total {
			break
		}
	}
	return result, nil
}

func contributionAPIKeyName(req SubmitAccountContributionRequest) string {
	name := strings.TrimSpace(req.Name)
	if name != "" {
		return name
	}
	platform := normalizeContributionPlatform(req.Platform)
	if platform == "" {
		platform = strings.ToLower(strings.TrimSpace(req.Platform))
	}
	return fmt.Sprintf("%s API contribution", platform)
}

func contributionAccountNameSet(accounts []service.Account) map[string]struct{} {
	names := make(map[string]struct{}, len(accounts))
	for i := range accounts {
		if name := normalizeContributionAccountName(accounts[i].Name); name != "" {
			names[name] = struct{}{}
		}
	}
	return names
}

func normalizeContributionAccountName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func reserveContributionAccountName(names map[string]struct{}, name string) bool {
	normalized := normalizeContributionAccountName(name)
	if normalized == "" {
		return true
	}
	if _, exists := names[normalized]; exists {
		return false
	}
	names[normalized] = struct{}{}
	return true
}

func duplicateContributionItem(index int, name string) AccountContributionResultItem {
	return AccountContributionResultItem{
		Index: index, Name: strings.TrimSpace(name), Status: "skipped",
		Message: "同名账号已存在，已跳过",
	}
}

func createdContributionItem(account *service.Account, item AccountContributionResultItem) AccountContributionResultItem {
	if account == nil {
		item.Status = "failed"
		item.Message = "account creation returned empty result"
		return item
	}
	item.AccountID = account.ID
	item.Status = "created"
	item.Message = "账号已导入，可按需手动测试"
	return item
}

func countContributionResultItem(result *AccountContributionResult, item AccountContributionResultItem) {
	switch item.Status {
	case "created":
		result.Created++
	case "skipped":
		result.Skipped++
	default:
		result.Failed++
	}
}

func contributionExtra(existing map[string]any, user *service.User, now time.Time, requests ...SubmitAccountContributionRequest) map[string]any {
	extra := make(map[string]any, len(existing)+6)
	for key, value := range existing {
		extra[key] = value
	}
	if importMethod := strings.TrimSpace(shareExtraString(extra, service.AccountContributionSourceKey)); importMethod != "" && importMethod != service.AccountContributionSourceValue {
		extra[service.AccountContributionImportMethodKey] = importMethod
	}
	extra[service.AccountContributionSourceKey] = service.AccountContributionSourceValue
	extra[service.AccountContributionSubmittedAtKey] = now.UTC().Format(time.RFC3339)
	extra[accountContributionDayKey] = contributionDay(now)
	if user != nil {
		extra[service.AccountContributorUserIDKey] = user.ID
		extra[service.AccountContributorEmailKey] = strings.TrimSpace(user.Email)
		extra[service.AccountContributorUsernameKey] = strings.TrimSpace(user.Username)
	}
	if len(requests) > 0 && requests[0].PoolGroupID != nil && *requests[0].PoolGroupID > 0 {
		applyContributionPoolPolicy(extra)
	} else {
		applyPrivateContributionPolicy(extra)
	}
	return extra
}

func applyPrivateContributionPolicy(extra map[string]any) {
	if extra == nil {
		return
	}
	extra[service.AccountShareModeKey] = service.AccountShareModePrivate
	delete(extra, service.AccountShareTotalBudgetKey)
	delete(extra, service.AccountShareDailyBudgetKey)
	delete(extra, service.AccountShareExpiresAtKey)
	delete(extra, service.AccountShareConsumerRateMultiplierKey)
	delete(extra, service.AccountShareUsedTotalKey)
	delete(extra, service.AccountShareUsedTodayKey)
	delete(extra, service.AccountShareUsageDayKey)
}

func applyContributionPoolPolicy(extra map[string]any) {
	if extra == nil {
		return
	}
	extra[service.AccountShareModeKey] = service.AccountShareModePool
	// The group multiplier is the only consumer price for a pool account.
	delete(extra, service.AccountShareConsumerRateMultiplierKey)
	delete(extra, service.AccountShareTotalBudgetKey)
	delete(extra, service.AccountShareDailyBudgetKey)
	delete(extra, service.AccountShareExpiresAtKey)
	delete(extra, service.AccountShareUsedTotalKey)
	delete(extra, service.AccountShareUsedTodayKey)
	delete(extra, service.AccountShareUsageDayKey)
}

func cloneExtra(extra map[string]any) map[string]any {
	cloned := make(map[string]any, len(extra)+4)
	for key, value := range extra {
		cloned[key] = value
	}
	return cloned
}

func shareExtraString(extra map[string]any, key string) string {
	value, _ := extra[key].(string)
	return strings.TrimSpace(value)
}

func accountDay(account *service.Account) string {
	if account == nil || account.Extra == nil {
		return ""
	}
	if value, ok := account.Extra[accountContributionDayKey].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if value, ok := account.Extra[service.AccountContributionSubmittedAtKey].(string); ok {
		if submittedAt, err := time.Parse(time.RFC3339, value); err == nil {
			return contributionDay(submittedAt)
		}
	}
	return contributionDay(account.CreatedAt)
}

func contributionDay(now time.Time) string { return now.In(accountContributionTZ).Format("2006-01-02") }

func lockAccountContributionUser(userID int64) func() {
	value, _ := accountContributionLocks.LoadOrStore(userID, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func normalizeContributionPlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case service.PlatformAnthropic:
		return service.PlatformAnthropic
	case service.PlatformOpenAI:
		return service.PlatformOpenAI
	case service.PlatformGemini:
		return service.PlatformGemini
	case service.PlatformGrok:
		return service.PlatformGrok
	default:
		return ""
	}
}

func contributionPriority(req SubmitAccountContributionRequest) int {
	if req.PoolGroupID != nil && *req.PoolGroupID > 0 && req.Priority != nil {
		return *req.Priority
	}
	return defaultContributionPriority
}

func contributionConcurrency(req SubmitAccountContributionRequest) int {
	if req.Concurrency != nil {
		return *req.Concurrency
	}
	return 30
}

func validateContributionPoolPriority(isPoolAccount bool, priority int) error {
	if isPoolAccount && priority < minimumContributionPoolPriority {
		return fmt.Errorf("priority must be at least %d when joining an administrator pool", minimumContributionPoolPriority)
	}
	return nil
}

func validateContributionPoolConcurrency(isPoolAccount bool, concurrency int) error {
	if isPoolAccount && concurrency < minimumContributionPoolConcurrency {
		return fmt.Errorf("concurrency must be at least %d when joining an administrator pool", minimumContributionPoolConcurrency)
	}
	return nil
}

func contributionLoadFactor(req SubmitAccountContributionRequest) *int {
	if req.LoadFactor != nil && *req.LoadFactor > 0 {
		return req.LoadFactor
	}
	return nil
}

func valueOrEmpty(value *[]int64) []int64 {
	if value == nil {
		return nil
	}
	return *value
}

// resolveContributionGroupBinding keeps pool admission under administrator
// control. Pool accounts receive exactly one enabled group and inherit its
// pricing; ordinary contributor accounts retain the existing group workflow.
func (h *AccountContributionHandler) resolveContributionGroupBinding(ctx context.Context, platform, accountType string, groupIDs []int64, poolGroupID *int64) ([]int64, error) {
	if poolGroupID == nil || *poolGroupID <= 0 {
		return groupIDs, h.validateContributionGroupSelection(ctx, platform, accountType, groupIDs)
	}
	if len(groupIDs) > 0 {
		return nil, fmt.Errorf("pool accounts cannot also use group_ids")
	}
	if h == nil || h.adminService == nil {
		return nil, fmt.Errorf("account contribution service unavailable")
	}
	group, err := h.adminService.GetGroup(ctx, *poolGroupID)
	if err != nil {
		return nil, err
	}
	if !group.IsActive() || !group.AllowContributionPool {
		return nil, fmt.Errorf("group %q is not open to contributed accounts", group.Name)
	}
	if group.Platform != platform {
		return nil, fmt.Errorf("group %q only accepts %s accounts", group.Name, group.Platform)
	}
	if group.RequireOAuthOnly && accountType == service.AccountTypeAPIKey {
		return nil, fmt.Errorf("group %q only accepts OAuth accounts", group.Name)
	}
	return []int64{group.ID}, nil
}

// validateContributionGroupSelection keeps contributor-managed routing
// explicit. Pool admission uses resolveContributionGroupBinding instead.
func (h *AccountContributionHandler) validateContributionGroupSelection(ctx context.Context, platform, accountType string, groupIDs []int64) error {
	if len(groupIDs) == 0 {
		return nil
	}
	if h == nil || h.adminService == nil {
		return fmt.Errorf("account contribution service unavailable")
	}
	groups, err := h.adminService.GetAllGroups(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]service.Group, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	seen := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			return fmt.Errorf("group_ids must contain positive group IDs")
		}
		if _, duplicate := seen[groupID]; duplicate {
			return fmt.Errorf("group_ids must not contain duplicates")
		}
		seen[groupID] = struct{}{}
		group, ok := byID[groupID]
		if !ok {
			return fmt.Errorf("group %d is unavailable", groupID)
		}
		if group.Platform != platform {
			return fmt.Errorf("group %q only accepts %s accounts", group.Name, group.Platform)
		}
		if group.RequireOAuthOnly && accountType == service.AccountTypeAPIKey {
			return fmt.Errorf("group %q only accepts OAuth accounts", group.Name)
		}
	}
	return nil
}

func (h *AccountContributionHandler) ensureContributionAccountVerified(ctx context.Context, account *service.Account) error {
	if h == nil || h.entClient == nil {
		return fmt.Errorf("account verification service unavailable")
	}
	if account == nil {
		return fmt.Errorf("contribution account not found")
	}
	if _, err := h.verifiedContributionAccount(ctx, account.ID, account.Platform); err != nil {
		if dbent.IsNotFound(err) {
			return fmt.Errorf("run the account test successfully before assigning it to a group")
		}
		return err
	}
	return nil
}

func parsePositiveQueryInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
