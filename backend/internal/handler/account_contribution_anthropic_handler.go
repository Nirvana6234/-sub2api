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

// AnthropicContributionAuthRequest carries the shared account settings for
// user-owned Claude OAuth contributions. Tokens stay server-side.
type AnthropicContributionAuthRequest struct {
	Name        string  `json:"name"`
	Concurrency *int    `json:"concurrency"`
	Priority    *int    `json:"priority"`
	LoadFactor  *int    `json:"load_factor"`
	GroupIDs    []int64 `json:"group_ids"`
	PoolGroupID *int64  `json:"pool_group_id"`
	TestModelID string  `json:"test_model_id"`
	ProxyID     *int64  `json:"proxy_id"`
	SessionID   string  `json:"session_id"`
	Code        string  `json:"code"`
}

type anthropicContributionAuthURLRequest struct {
	ProxyID *int64 `json:"proxy_id"`
}

func (h *AccountContributionHandler) GenerateAnthropicContributionAuthURL(c *gin.Context) {
	_, user, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	if h.oauthService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Claude authorization service unavailable")
		return
	}
	var req anthropicContributionAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = anthropicContributionAuthURLRequest{}
	}
	if err := h.validateContributionAuthProxy(c, user.ID, req.ProxyID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.ProxyID != nil && *req.ProxyID == 0 {
		req.ProxyID = nil
	}
	result, err := h.oauthService.GenerateAuthURL(c.Request.Context(), req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountContributionHandler) CreateAnthropicContributionFromCode(c *gin.Context) {
	user, req, ok := h.bindAnthropicContributionAuthRequest(c)
	if !ok {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.Code) == "" {
		response.BadRequest(c, "session_id and code are required")
		return
	}
	tokenInfo, err := h.oauthService.ExchangeCode(c.Request.Context(), &service.ExchangeCodeInput{
		SessionID: strings.TrimSpace(req.SessionID),
		Code:      strings.TrimSpace(req.Code),
		ProxyID:   req.ProxyID,
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
	item := h.createAnthropicAuthContributionWithNames(c, user, req, service.BuildClaudeAccountCredentials(tokenInfo), tokenInfo, "manual_authorization", accountNames.names)
	response.Success(c, contributionResultFromItem(item))
}

func (h *AccountContributionHandler) bindAnthropicContributionAuthRequest(c *gin.Context) (*service.User, AnthropicContributionAuthRequest, bool) {
	_, user, ok := h.authenticatedUser(c)
	if !ok {
		return nil, AnthropicContributionAuthRequest{}, false
	}
	if h.oauthService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Claude authorization service unavailable")
		return nil, AnthropicContributionAuthRequest{}, false
	}
	var req AnthropicContributionAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return nil, AnthropicContributionAuthRequest{}, false
	}
	if err := h.validateContributionAuthProxy(c, user.ID, req.ProxyID); err != nil {
		response.BadRequest(c, err.Error())
		return nil, AnthropicContributionAuthRequest{}, false
	}
	if req.Concurrency != nil && (*req.Concurrency < 1 || *req.Concurrency > 1000) {
		response.BadRequest(c, "concurrency must be between 1 and 1000")
		return nil, AnthropicContributionAuthRequest{}, false
	}
	if req.LoadFactor != nil && (*req.LoadFactor < 0 || *req.LoadFactor > 10000) {
		response.BadRequest(c, "load_factor must be between 0 and 10000")
		return nil, AnthropicContributionAuthRequest{}, false
	}
	if req.ProxyID != nil && *req.ProxyID == 0 {
		req.ProxyID = nil
	}
	groupIDs, err := h.resolveContributionGroupBinding(c.Request.Context(), service.PlatformAnthropic, service.AccountTypeOAuth, req.GroupIDs, req.PoolGroupID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return nil, AnthropicContributionAuthRequest{}, false
	}
	req.GroupIDs = groupIDs
	baseReq := SubmitAccountContributionRequest{Concurrency: req.Concurrency, Priority: req.Priority, LoadFactor: req.LoadFactor, PoolGroupID: req.PoolGroupID}
	if err := validateContributionPoolPriority(req.PoolGroupID != nil && *req.PoolGroupID > 0, contributionPriority(baseReq)); err != nil {
		response.BadRequest(c, err.Error())
		return nil, AnthropicContributionAuthRequest{}, false
	}
	if err := validateContributionPoolConcurrency(req.PoolGroupID != nil && *req.PoolGroupID > 0, contributionConcurrency(baseReq)); err != nil {
		response.BadRequest(c, err.Error())
		return nil, AnthropicContributionAuthRequest{}, false
	}
	return user, req, true
}

func (h *AccountContributionHandler) createAnthropicAuthContributionWithNames(c *gin.Context, user *service.User, req AnthropicContributionAuthRequest, credentials map[string]any, tokenInfo *service.TokenInfo, importSource string, accountNames map[string]struct{}) AccountContributionResultItem {
	name := strings.TrimSpace(req.Name)
	if name == "" && tokenInfo != nil {
		for _, candidate := range []string{tokenInfo.EmailAddress, tokenInfo.AccountUUID, tokenInfo.OrgUUID} {
			if name = strings.TrimSpace(candidate); name != "" {
				break
			}
		}
	}
	if name == "" {
		name = "Claude OAuth Account"
	}
	if accountNames != nil && !reserveContributionAccountName(accountNames, name) {
		return duplicateContributionItem(1, name)
	}
	concurrency := 30
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	baseReq := SubmitAccountContributionRequest{Concurrency: req.Concurrency, Priority: req.Priority, LoadFactor: req.LoadFactor, PoolGroupID: req.PoolGroupID}
	extra := contributionExtra(nil, user, time.Now(), baseReq)
	extra[service.AccountContributionImportMethodKey] = importSource
	account, err := h.adminService.CreateAccount(c.Request.Context(), &service.CreateAccountInput{
		Name: name, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth, Credentials: credentials, Extra: extra,
		ProxyID: req.ProxyID, Concurrency: concurrency, Priority: contributionPriority(baseReq), LoadFactor: contributionLoadFactor(baseReq),
		GroupIDs: req.GroupIDs, SkipDefaultGroupBind: true,
	})
	item := AccountContributionResultItem{Index: 1, Name: name, Status: "failed"}
	if err != nil {
		item.Message = err.Error()
		return item
	}
	return createdContributionItem(account, item)
}

func (h *AccountContributionHandler) validateContributionAuthProxy(c *gin.Context, userID int64, proxyID *int64) error {
	if proxyID == nil || *proxyID == 0 {
		return nil
	}
	if *proxyID < 0 {
		return fmt.Errorf("proxy_id must be zero for direct connection or a positive user proxy id")
	}
	_, err := h.getOwnedContributionProxy(c.Request.Context(), userID, *proxyID, true)
	return err
}
