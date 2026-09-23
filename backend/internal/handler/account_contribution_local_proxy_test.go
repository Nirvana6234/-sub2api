package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type localProxyTokenStub struct {
	token string
	err   error
	calls int
}

func (s *localProxyTokenStub) get(context.Context, *service.Account) (string, error) {
	s.calls++
	return s.token, s.err
}

func localProxyTestHandler(account *service.Account, openAI, claude *localProxyTokenStub) *AccountContributionHandler {
	h := newContributionHandlerForTest(&contributionAdminServiceStub{account: account})
	h.accountTestRunner = &contributionTestRunnerStub{}
	if openAI != nil {
		h.openAILocalProxyToken = openAI.get
	}
	if claude != nil {
		h.claudeLocalProxyToken = claude.get
	}
	return h
}

func ownedOAuthAccount(id int64, platform string) *service.Account {
	return &service.Account{
		ID: id, Name: "mine", Platform: platform, Type: service.AccountTypeOAuth, Status: service.StatusActive,
		Extra: contributionExtra(nil, &service.User{ID: 42}, accountContributionTZNow()),
	}
}

func callIssueLocalProxyToken(h *AccountContributionHandler, id string) (int, string, http.Header) {
	c, recorder := contributionTestContext(http.MethodPost, "/account-contributions/"+id+"/local-proxy-token")
	c.Params = gin.Params{{Key: "id", Value: id}}
	h.IssueLocalProxyToken(c)
	return recorder.Code, recorder.Body.String(), recorder.Header()
}

func TestLocalProxyTokenIssuesOpenAIAccessTokenToTheOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	account := ownedOAuthAccount(81, service.PlatformOpenAI)
	account.Credentials = map[string]any{
		"access_token":               "at-server-copy",
		"refresh_token":              "rt-must-never-leave",
		"expires_at":                 expires.Format(time.RFC3339),
		"chatgpt_account_id":         "acct-123",
		"chatgpt_account_is_fedramp": true,
	}
	openAI := &localProxyTokenStub{token: "at-fresh"}
	h := localProxyTestHandler(account, openAI, nil)

	code, body, headers := callIssueLocalProxyToken(h, "81")

	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, "no-store", headers.Get("Cache-Control"))
	require.NotContains(t, body, "rt-must-never-leave")
	require.NotContains(t, body, "refresh_token")
	var envelope struct {
		Data LocalProxyCredential `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &envelope))
	require.Equal(t, int64(81), envelope.Data.AccountID)
	require.Equal(t, service.PlatformOpenAI, envelope.Data.Platform)
	require.Equal(t, "at-fresh", envelope.Data.AccessToken)
	require.Equal(t, "acct-123", envelope.Data.ChatGPTAccountID)
	require.True(t, envelope.Data.FedRAMP)
	require.NotNil(t, envelope.Data.ExpiresAt)
	require.True(t, expires.Equal(*envelope.Data.ExpiresAt))
	require.Equal(t, 1, openAI.calls)
}

func TestLocalProxyTokenIssuesClaudeAccessTokenWithoutOpenAIFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := ownedOAuthAccount(82, service.PlatformAnthropic)
	account.Credentials = map[string]any{"refresh_token": "rt-claude"}
	claude := &localProxyTokenStub{token: "sk-ant-oat-fresh"}
	h := localProxyTestHandler(account, &localProxyTokenStub{token: "wrong-provider"}, claude)

	code, body, _ := callIssueLocalProxyToken(h, "82")

	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"platform":"anthropic"`)
	require.Contains(t, body, "sk-ant-oat-fresh")
	require.NotContains(t, body, "rt-claude")
	require.NotContains(t, body, "chatgpt_account_id")
	require.Equal(t, 1, claude.calls)
}

func TestLocalProxyTokenRejectsAnotherUsersAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := ownedOAuthAccount(83, service.PlatformOpenAI)
	account.Extra = contributionExtra(nil, &service.User{ID: 99}, accountContributionTZNow())
	openAI := &localProxyTokenStub{token: "at"}
	h := localProxyTestHandler(account, openAI, nil)

	code, _, _ := callIssueLocalProxyToken(h, "83")

	require.Equal(t, http.StatusForbidden, code)
	require.Zero(t, openAI.calls)
}

func TestLocalProxyTokenRejectsAccountsThatAreNotShortLivedOAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parent := int64(1)
	cases := map[string]*service.Account{
		"openai setup token": {Platform: service.PlatformOpenAI, Type: service.AccountTypeSetupToken},
		"claude setup token": {Platform: service.PlatformAnthropic, Type: service.AccountTypeSetupToken},
		"api key":            {Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
		"gemini oauth":       {Platform: service.PlatformGemini, Type: service.AccountTypeOAuth},
		"spark shadow":       {Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, ParentAccountID: &parent},
	}
	for name, account := range cases {
		t.Run(name, func(t *testing.T) {
			account.ID = 84
			account.Status = service.StatusActive
			account.Extra = contributionExtra(nil, &service.User{ID: 42}, accountContributionTZNow())
			stub := &localProxyTokenStub{token: "at"}
			h := localProxyTestHandler(account, stub, stub)

			code, body, _ := callIssueLocalProxyToken(h, "84")

			require.Equal(t, http.StatusBadRequest, code, body)
			require.Zero(t, stub.calls)
		})
	}
}

func TestLocalProxyTokenRejectsAnInactiveAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := ownedOAuthAccount(85, service.PlatformOpenAI)
	account.Status = "error"
	openAI := &localProxyTokenStub{token: "at"}
	h := localProxyTestHandler(account, openAI, nil)

	code, _, _ := callIssueLocalProxyToken(h, "85")

	require.Equal(t, http.StatusConflict, code)
	require.Zero(t, openAI.calls)
}

func TestLocalProxyTokenFailureDoesNotEchoTheProvidersError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := ownedOAuthAccount(86, service.PlatformOpenAI)
	h := localProxyTestHandler(account, &localProxyTokenStub{err: errors.New("refresh with rt-secret failed")}, nil)

	code, body, _ := callIssueLocalProxyToken(h, "86")

	require.Equal(t, http.StatusBadGateway, code)
	require.NotContains(t, body, "rt-secret")
}

func TestLocalProxyTokenIsUnavailableWithoutAProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := localProxyTestHandler(ownedOAuthAccount(87, service.PlatformAnthropic), nil, nil)

	code, _, _ := callIssueLocalProxyToken(h, "87")

	require.Equal(t, http.StatusServiceUnavailable, code)
}
