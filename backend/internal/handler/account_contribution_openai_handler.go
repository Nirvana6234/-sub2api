package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const openAIMobileRefreshTokenClientID = "app_LlGpXReQgckcGGUo2JrYvtJK"

// OpenAIContributionAuthRequest carries the account settings shared by every
// user-owned OpenAI authorization path. Tokens are deliberately write-only.
type OpenAIContributionAuthRequest struct {
	Name               string  `json:"name"`
	Concurrency        *int    `json:"concurrency"`
	Priority           *int    `json:"priority"`
	LoadFactor         *int    `json:"load_factor"`
	GroupIDs           []int64 `json:"group_ids"`
	PoolGroupID        *int64  `json:"pool_group_id"`
	AutoPauseOnExpired *bool   `json:"auto_pause_on_expired"`
	TestModelID        string  `json:"test_model_id"`
	ProxyID            *int64  `json:"proxy_id"`
	SessionID          string  `json:"session_id"`
	Code               string  `json:"code"`
	State              string  `json:"state"`
	RedirectURI        string  `json:"redirect_uri"`
	RefreshToken       string  `json:"refresh_token"`
	AccessToken        string  `json:"access_token"`
}

type openAIContributionAuthURLRequest struct {
	ProxyID     *int64 `json:"proxy_id"`
	RedirectURI string `json:"redirect_uri"`
}

func (h *AccountContributionHandler) GenerateOpenAIContributionAuthURL(c *gin.Context) {
	_, user, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	if h.openaiOAuthService == nil {
		response.Error(c, http.StatusServiceUnavailable, "OpenAI authorization service unavailable")
		return
	}
	var req openAIContributionAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.validateOpenAIContributionProxy(c, user.ID, req.ProxyID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.ProxyID != nil && *req.ProxyID == 0 {
		req.ProxyID = nil
	}
	result, err := h.openaiOAuthService.GenerateAuthURL(c.Request.Context(), req.ProxyID, strings.TrimSpace(req.RedirectURI), service.PlatformOpenAI)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountContributionHandler) CreateOpenAIContributionFromCode(c *gin.Context) {
	user, req, ok := h.bindOpenAIContributionAuthRequest(c)
	if !ok {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.Code) == "" || strings.TrimSpace(req.State) == "" {
		response.BadRequest(c, "session_id, code, and state are required")
		return
	}
	tokenInfo, err := h.openaiOAuthService.ExchangeCode(c.Request.Context(), &service.OpenAIExchangeCodeInput{
		SessionID: strings.TrimSpace(req.SessionID), Code: strings.TrimSpace(req.Code), State: strings.TrimSpace(req.State),
		RedirectURI: strings.TrimSpace(req.RedirectURI), ProxyID: req.ProxyID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accountNames, ok := h.lockAndLoadContributionAccountNames(c, user.ID)
	if !ok {
		return
	}
	defer accountNames.unlock()
	response.Success(c, contributionResultFromItem(h.createOpenAIAuthContributionWithNames(c, user, req, h.openaiOAuthService.BuildAccountCredentials(tokenInfo), tokenInfo, "manual_authorization", 1, accountNames.names)))
}

func (h *AccountContributionHandler) CreateOpenAIContributionFromRefreshToken(c *gin.Context) {
	h.createOpenAIContributionFromRefreshToken(c, "refresh_token", "")
}

func (h *AccountContributionHandler) CreateOpenAIContributionFromMobileRefreshToken(c *gin.Context) {
	h.createOpenAIContributionFromRefreshToken(c, "mobile_refresh_token", openAIMobileRefreshTokenClientID)
}

func (h *AccountContributionHandler) createOpenAIContributionFromRefreshToken(c *gin.Context, importSource, clientID string) {
	user, req, ok := h.bindOpenAIContributionAuthRequest(c)
	if !ok {
		return
	}
	refreshTokens := nonEmptyLines(req.RefreshToken)
	if len(refreshTokens) == 0 {
		response.BadRequest(c, "refresh_token is required")
		return
	}
	if len(refreshTokens) > maxAccountContributionItems {
		response.BadRequest(c, fmt.Sprintf("at most %d refresh tokens can be submitted at once", maxAccountContributionItems))
		return
	}
	proxyURL, err := h.openAIContributionProxyURL(c, user.ID, req.ProxyID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result := AccountContributionResult{Total: len(refreshTokens), Limit: 0, Used: 0, Remaining: -1, Items: make([]AccountContributionResultItem, 0, len(refreshTokens))}
	accountNames, ok := h.lockAndLoadContributionAccountNames(c, user.ID)
	if !ok {
		return
	}
	defer accountNames.unlock()
	for index, refreshToken := range refreshTokens {
		item := AccountContributionResultItem{Index: index + 1, Status: "failed"}
		tokenInfo, tokenErr := h.openaiOAuthService.RefreshTokenWithClientID(c.Request.Context(), refreshToken, proxyURL, clientID)
		if tokenErr != nil {
			item.Message = contributionAuthFailureMessage(tokenErr, refreshToken)
		} else {
			item = h.createOpenAIAuthContributionWithNames(c, user, req, h.openaiOAuthService.BuildAccountCredentials(tokenInfo), tokenInfo, importSource, index+1, accountNames.names)
		}
		countContributionResultItem(&result, item)
		result.Items = append(result.Items, item)
	}
	response.Success(c, result)
}

func (h *AccountContributionHandler) CreateOpenAIContributionFromCodexPAT(c *gin.Context) {
	user, req, ok := h.bindOpenAIContributionAuthRequest(c)
	if !ok {
		return
	}
	accessToken := strings.TrimSpace(req.AccessToken)
	if accessToken == "" {
		response.BadRequest(c, "access_token is required")
		return
	}
	proxyURL, err := h.openAIContributionProxyURL(c, user.ID, req.ProxyID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	tokenInfo, err := h.openaiOAuthService.ValidateCodexPersonalAccessToken(c.Request.Context(), accessToken, proxyURL)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accountNames, ok := h.lockAndLoadContributionAccountNames(c, user.ID)
	if !ok {
		return
	}
	defer accountNames.unlock()
	response.Success(c, contributionResultFromItem(h.createOpenAIAuthContributionWithNames(c, user, req, h.openaiOAuthService.BuildAccountCredentials(tokenInfo), tokenInfo, "codex_personal_access_token", 1, accountNames.names)))
}

func (h *AccountContributionHandler) bindOpenAIContributionAuthRequest(c *gin.Context) (*service.User, OpenAIContributionAuthRequest, bool) {
	_, user, ok := h.authenticatedUser(c)
	if !ok {
		return nil, OpenAIContributionAuthRequest{}, false
	}
	if h.openaiOAuthService == nil {
		response.Error(c, http.StatusServiceUnavailable, "OpenAI authorization service unavailable")
		return nil, OpenAIContributionAuthRequest{}, false
	}
	var req OpenAIContributionAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return nil, OpenAIContributionAuthRequest{}, false
	}
	if err := h.validateOpenAIContributionSettings(c, user.ID, req); err != nil {
		response.BadRequest(c, err.Error())
		return nil, OpenAIContributionAuthRequest{}, false
	}
	if req.ProxyID != nil && *req.ProxyID == 0 {
		req.ProxyID = nil
	}
	groupIDs, err := h.resolveContributionGroupBinding(c.Request.Context(), service.PlatformOpenAI, service.AccountTypeOAuth, req.GroupIDs, req.PoolGroupID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return nil, OpenAIContributionAuthRequest{}, false
	}
	req.GroupIDs = groupIDs
	baseReq := SubmitAccountContributionRequest{Concurrency: req.Concurrency, Priority: req.Priority, LoadFactor: req.LoadFactor, AutoPauseOnExpired: req.AutoPauseOnExpired, PoolGroupID: req.PoolGroupID}
	if err := validateContributionPoolPriority(req.PoolGroupID != nil && *req.PoolGroupID > 0, contributionPriority(baseReq)); err != nil {
		response.BadRequest(c, err.Error())
		return nil, OpenAIContributionAuthRequest{}, false
	}
	if err := validateContributionPoolConcurrency(req.PoolGroupID != nil && *req.PoolGroupID > 0, contributionConcurrency(baseReq)); err != nil {
		response.BadRequest(c, err.Error())
		return nil, OpenAIContributionAuthRequest{}, false
	}
	return user, req, true
}

func (h *AccountContributionHandler) validateOpenAIContributionSettings(c *gin.Context, userID int64, req OpenAIContributionAuthRequest) error {
	if req.Concurrency != nil && (*req.Concurrency < 1 || *req.Concurrency > 1000) {
		return fmt.Errorf("concurrency must be between 1 and 1000")
	}
	if req.LoadFactor != nil && (*req.LoadFactor < 0 || *req.LoadFactor > 10000) {
		return fmt.Errorf("load_factor must be between 0 and 10000")
	}
	if err := h.validateOpenAIContributionProxy(c, userID, req.ProxyID); err != nil {
		return err
	}
	return nil
}

func (h *AccountContributionHandler) validateOpenAIContributionProxy(c *gin.Context, userID int64, proxyID *int64) error {
	if proxyID == nil {
		return nil
	}
	if *proxyID < 0 {
		return fmt.Errorf("proxy_id must be zero for direct connection or a positive user proxy id")
	}
	if *proxyID == 0 {
		return nil
	}
	_, err := h.getOwnedContributionProxy(c.Request.Context(), userID, *proxyID, true)
	return err
}

func (h *AccountContributionHandler) openAIContributionProxyURL(c *gin.Context, userID int64, proxyID *int64) (string, error) {
	if proxyID == nil || *proxyID == 0 {
		return "", nil
	}
	proxy, err := h.getOwnedContributionProxy(c.Request.Context(), userID, *proxyID, true)
	if err != nil {
		return "", err
	}
	return contributionProxyServiceValue(proxy).URL(), nil
}

func (h *AccountContributionHandler) createOpenAIAuthContribution(c *gin.Context, user *service.User, req OpenAIContributionAuthRequest, credentials map[string]any, tokenInfo *service.OpenAITokenInfo, importSource string, index int) AccountContributionResultItem {
	return h.createOpenAIAuthContributionWithNames(c, user, req, credentials, tokenInfo, importSource, index, nil)
}

func (h *AccountContributionHandler) createOpenAIAuthContributionWithNames(c *gin.Context, user *service.User, req OpenAIContributionAuthRequest, credentials map[string]any, tokenInfo *service.OpenAITokenInfo, importSource string, index int, accountNames map[string]struct{}) AccountContributionResultItem {
	name := strings.TrimSpace(req.Name)
	if name == "" && tokenInfo != nil {
		for _, candidate := range []string{tokenInfo.Email, tokenInfo.ChatGPTAccountID, tokenInfo.ChatGPTUserID} {
			if name = strings.TrimSpace(candidate); name != "" {
				break
			}
		}
	}
	if name == "" {
		name = "OpenAI OAuth Account"
	}
	if accountNames != nil && !reserveContributionAccountName(accountNames, name) {
		return duplicateContributionItem(index, name)
	}
	concurrency := 30
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	baseReq := SubmitAccountContributionRequest{Concurrency: req.Concurrency, Priority: req.Priority, LoadFactor: req.LoadFactor, AutoPauseOnExpired: req.AutoPauseOnExpired, PoolGroupID: req.PoolGroupID}
	extra := contributionExtra(nil, user, time.Now(), baseReq)
	extra[service.AccountContributionImportMethodKey] = importSource
	account, err := h.adminService.CreateAccount(c.Request.Context(), &service.CreateAccountInput{
		Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: credentials, Extra: extra,
		ProxyID: req.ProxyID, Concurrency: concurrency, Priority: contributionPriority(baseReq), LoadFactor: contributionLoadFactor(baseReq),
		GroupIDs: req.GroupIDs, AutoPauseOnExpired: req.AutoPauseOnExpired, SkipDefaultGroupBind: true,
	})
	item := AccountContributionResultItem{Index: index, Name: name, Status: "failed"}
	if err != nil {
		item.Message = err.Error()
		return item
	}
	return createdContributionItem(account, item)
}

func nonEmptyLines(value string) []string {
	values := strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' })
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func contributionResultFromItem(item AccountContributionResultItem) AccountContributionResult {
	result := AccountContributionResult{Total: 1, Limit: 0, Used: 0, Remaining: -1, Items: []AccountContributionResultItem{item}}
	countContributionResultItem(&result, item)
	return result
}

type lockedContributionAccountNames struct {
	names  map[string]struct{}
	unlock func()
}

func (h *AccountContributionHandler) lockAndLoadContributionAccountNames(c *gin.Context, userID int64) (lockedContributionAccountNames, bool) {
	unlock := lockAccountContributionUser(userID)
	accounts, err := h.listByContributor(c.Request.Context(), userID)
	if err != nil {
		unlock()
		response.ErrorFrom(c, err)
		return lockedContributionAccountNames{}, false
	}
	return lockedContributionAccountNames{names: contributionAccountNameSet(accounts), unlock: unlock}, true
}

// contributionAuthFailureMessage preserves the upstream diagnostic so a user
// can correct a rejected credential, while preventing the submitted secret
// from being reflected back in the response.
func contributionAuthFailureMessage(err error, submittedSecret string) string {
	if err == nil {
		return "authorization failed"
	}
	message := strings.TrimSpace(err.Error())
	if secret := strings.TrimSpace(submittedSecret); secret != "" {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	if message == "" {
		return "authorization failed"
	}
	return message
}
