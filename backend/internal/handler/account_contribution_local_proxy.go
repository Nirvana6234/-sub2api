package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// localProxyAccessTokenFunc returns a currently valid access token for an OAuth
// account, refreshing it server-side when due. The token providers are the only
// refreshers of these accounts: a refresh token rotates on use, so a second
// refresher (the client) would invalidate this server's copy and break the account.
type localProxyAccessTokenFunc func(ctx context.Context, account *service.Account) (string, error)

// LocalProxyCredential is what the novice client needs to send its own tool's
// traffic straight to the official API with one of the user's own accounts.
//
// It deliberately never carries a refresh token, and only OAuth accounts qualify:
// their access tokens are short-lived. Setup tokens are long-lived secrets and are
// not handed out.
type LocalProxyCredential struct {
	AccountID        int64      `json:"account_id"`
	Name             string     `json:"name"`
	Platform         string     `json:"platform"`
	AccessToken      string     `json:"access_token"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	ChatGPTAccountID string     `json:"chatgpt_account_id,omitempty"`
	FedRAMP          bool       `json:"fedramp,omitempty"`
}

// localProxyPlatform reports which local-proxy flavour an account supports, or ""
// when it supports none.
func localProxyPlatform(account *service.Account) string {
	if account == nil || account.Type != service.AccountTypeOAuth || account.IsShadow() {
		return ""
	}
	switch {
	case account.IsOpenAI():
		return service.PlatformOpenAI
	case account.Platform == service.PlatformAnthropic:
		return service.PlatformAnthropic
	default:
		return ""
	}
}

// IssueLocalProxyToken hands the owner of an OAuth contribution a short-lived access
// token so the novice client can talk to the official API directly.
//
// POST so nothing on the way caches it; the response is marked no-store as well.
// Ownership is enforced by ownedAccount. The route's audit middleware records who
// asked for which account and when — it captures request bodies only, never this
// response, so the token does not reach the audit log.
func (h *AccountContributionHandler) IssueLocalProxyToken(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	account, ok := h.ownedAccount(c)
	if !ok {
		return
	}

	platform := localProxyPlatform(account)
	var tokenOf localProxyAccessTokenFunc
	switch platform {
	case service.PlatformOpenAI:
		tokenOf = h.openAILocalProxyToken
	case service.PlatformAnthropic:
		tokenOf = h.claudeLocalProxyToken
	default:
		response.BadRequest(c, "Local proxy supports only OpenAI (Codex) or Anthropic (Claude) OAuth accounts")
		return
	}
	if tokenOf == nil {
		response.Error(c, http.StatusServiceUnavailable, "Local proxy is unavailable on this server")
		return
	}
	if !account.IsActive() {
		response.Error(c, http.StatusConflict, "This account is not active")
		return
	}

	ctx := c.Request.Context()
	token, err := tokenOf(ctx, account)
	if err != nil || strings.TrimSpace(token) == "" {
		// The provider's reason stays in the server log; the client gets a stable
		// message that cannot echo anything credential-shaped.
		slog.Warn("local_proxy_token_failed", "account_id", account.ID, "platform", platform, "error", err)
		response.Error(c, http.StatusBadGateway, "Could not obtain an access token for this account")
		return
	}

	// Re-read after the provider may have refreshed, so expires_at describes the
	// token actually returned rather than the one it replaced.
	current := account
	if refreshed, readErr := h.adminService.GetAccount(ctx, account.ID); readErr == nil && refreshed != nil {
		current = refreshed
	}

	credential := LocalProxyCredential{
		AccountID:   account.ID,
		Name:        account.Name,
		Platform:    platform,
		AccessToken: token,
	}
	if expiresAt := current.GetCredentialAsTime("expires_at"); expiresAt != nil && expiresAt.After(time.Now()) {
		credential.ExpiresAt = expiresAt
	}
	if platform == service.PlatformOpenAI {
		credential.ChatGPTAccountID = current.GetChatGPTAccountID()
		credential.FedRAMP = current.IsChatGPTAccountFedRAMP()
	}
	response.Success(c, credential)
}
