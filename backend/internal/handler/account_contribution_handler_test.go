package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type contributionUserRepoStub struct {
	service.UserRepository
	user *service.User
}

type contributionSettingRepoStub struct {
	service.SettingRepository
	values map[string]string
}

func (s *contributionSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (r *contributionUserRepoStub) GetByID(_ context.Context, id int64) (*service.User, error) {
	if r.user != nil && r.user.ID == id {
		return r.user, nil
	}
	return nil, service.ErrUserNotFound
}

func (r *contributionUserRepoStub) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type contributionAdminServiceStub struct {
	service.AdminService
	accounts    []service.Account
	account     *service.Account
	createInput *service.CreateAccountInput
	groups      []service.Group
	proxies     []service.Proxy
	updateInput *service.UpdateAccountInput
	deletedIDs  []int64
}

func (s *contributionAdminServiceStub) CreateAccount(_ context.Context, input *service.CreateAccountInput) (*service.Account, error) {
	s.createInput = input
	return &service.Account{
		ID:          99,
		Name:        input.Name,
		Platform:    input.Platform,
		Type:        input.Type,
		Credentials: input.Credentials,
		Extra:       input.Extra,
		Concurrency: input.Concurrency,
		Priority:    input.Priority,
		GroupIDs:    input.GroupIDs,
		Schedulable: true,
	}, nil
}

func (s *contributionAdminServiceStub) SetAccountSchedulable(_ context.Context, id int64, schedulable bool) (*service.Account, error) {
	if s.account != nil && s.account.ID == id {
		s.account.Schedulable = schedulable
		return s.account, nil
	}
	return &service.Account{ID: id, Schedulable: schedulable}, nil
}

func (s *contributionAdminServiceStub) ListAccounts(_ context.Context, page, pageSize int, _, _, _, _ string, _ int64, _, _, _ string) ([]service.Account, int64, error) {
	if page > 1 {
		return nil, int64(len(s.accounts)), nil
	}
	return s.accounts, int64(len(s.accounts)), nil
}

func (s *contributionAdminServiceStub) GetAccount(_ context.Context, _ int64) (*service.Account, error) {
	return s.account, nil
}

func (s *contributionAdminServiceStub) GetAllGroups(context.Context) ([]service.Group, error) {
	return s.groups, nil
}

func (s *contributionAdminServiceStub) GetGroup(_ context.Context, id int64) (*service.Group, error) {
	for i := range s.groups {
		if s.groups[i].ID == id {
			return &s.groups[i], nil
		}
	}
	return nil, service.ErrGroupNotFound
}

func (s *contributionAdminServiceStub) GetAllProxies(context.Context) ([]service.Proxy, error) {
	return s.proxies, nil
}

func (s *contributionAdminServiceStub) UpdateAccount(_ context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.updateInput = input
	if s.account != nil {
		if input.ProxyID != nil {
			s.account.ProxyID = input.ProxyID
			for i := range s.proxies {
				if s.proxies[i].ID == *input.ProxyID {
					s.account.Proxy = &s.proxies[i]
					break
				}
			}
		}
		return s.account, nil
	}
	return &service.Account{ID: id}, nil
}

func (s *contributionAdminServiceStub) DeleteAccount(_ context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return nil
}

type contributionTestRunnerStub struct {
	results []service.ScheduledTestResult
	calls   int
}

type contributionOpenAIOAuthClientStub struct {
	refreshErr error
}

func (s *contributionOpenAIOAuthClientStub) ExchangeCode(context.Context, string, string, string, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *contributionOpenAIOAuthClientStub) RefreshToken(context.Context, string, string) (*openai.TokenResponse, error) {
	return nil, s.refreshErr
}

func (s *contributionOpenAIOAuthClientStub) RefreshTokenWithClientID(context.Context, string, string, string) (*openai.TokenResponse, error) {
	return nil, s.refreshErr
}

func (s *contributionTestRunnerStub) RunTestBackground(context.Context, int64, string) (*service.ScheduledTestResult, error) {
	s.calls++
	if len(s.results) == 0 {
		return &service.ScheduledTestResult{Status: "failed", ErrorMessage: "missing test result"}, nil
	}
	result := s.results[0]
	s.results = s.results[1:]
	return &result, nil
}

func newContributionHandlerForTest(admin service.AdminService) *AccountContributionHandler {
	userService := service.NewUserService(&contributionUserRepoStub{user: &service.User{ID: 42, Email: "owner@example.com"}}, nil, nil, nil)
	return NewAccountContributionHandler(userService, admin, nil, &service.AccountTestService{}, nil, nil, nil, nil)
}

func contributionTestContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	return c, recorder
}

func TestContributionAuthFailureMessagePreservesDiagnosticAndRedactsSecret(t *testing.T) {
	message := contributionAuthFailureMessage(fmt.Errorf("OpenAI rejected refresh token %s: expired", "rt-secret"), "rt-secret")
	require.Equal(t, "OpenAI rejected refresh token [REDACTED]: expired", message)
}

func TestSuccessfulContributionTestRestoresScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &service.Account{
		ID: 71, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Schedulable: false,
		Extra: contributionExtra(nil, &service.User{ID: 42}, accountContributionTZNow()),
	}
	admin := &contributionAdminServiceStub{account: account}
	h := newContributionHandlerForTest(admin)
	h.accountTestRunner = &contributionTestRunnerStub{results: []service.ScheduledTestResult{{Status: "success"}}}
	c, recorder := contributionTestContext(http.MethodPost, "/account-contributions/71/test")
	c.Params = gin.Params{{Key: "id", Value: "71"}}

	h.Test(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.True(t, account.Schedulable)
}

func TestContributionOwnerCanReadAvailableModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &service.Account{
		ID: 72, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Extra: contributionExtra(nil, &service.User{ID: 42}, accountContributionTZNow()),
	}
	h := newContributionHandlerForTest(&contributionAdminServiceStub{account: account})
	c, recorder := contributionTestContext(http.MethodGet, "/account-contributions/72/models")
	c.Params = gin.Params{{Key: "id", Value: "72"}}

	h.GetAvailableModels(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), `"id":"gpt-`)
}

func TestContributionModelsRejectAnotherUsersAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &service.Account{
		ID: 73, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Extra: contributionExtra(nil, &service.User{ID: 99}, accountContributionTZNow()),
	}
	h := newContributionHandlerForTest(&contributionAdminServiceStub{account: account})
	c, recorder := contributionTestContext(http.MethodGet, "/account-contributions/73/models")
	c.Params = gin.Params{{Key: "id", Value: "73"}}

	h.GetAvailableModels(c)

	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "You can only manage accounts you submitted")
}

func TestRefreshTokenContributionReturnsDiagnosticWithoutEchoingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newContributionHandlerForTest(&contributionAdminServiceStub{})
	h.openaiOAuthService = service.NewOpenAIOAuthService(nil, &contributionOpenAIOAuthClientStub{
		refreshErr: errors.New("OpenAI rejected refresh token rt-secret: expired"),
	})
	c, recorder := contributionTestContext(http.MethodPost, "/account-contributions/openai/create-from-refresh-token")
	c.Request = httptest.NewRequest(http.MethodPost, "/account-contributions/openai/create-from-refresh-token", strings.NewReader(`{"refresh_token":"rt-secret"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})

	h.CreateOpenAIContributionFromRefreshToken(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var envelope struct {
		Data AccountContributionResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, 1, envelope.Data.Failed)
	require.Len(t, envelope.Data.Items, 1)
	require.Equal(t, "OpenAI rejected refresh token [REDACTED]: expired", envelope.Data.Items[0].Message)
	require.NotContains(t, recorder.Body.String(), "rt-secret")
}

func TestOpenAIContributionKeepsUserOwnershipAndStoresImportMethodSeparately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &contributionAdminServiceStub{}
	h := newContributionHandlerForTest(admin)
	h.accountTestRunner = &contributionTestRunnerStub{results: []service.ScheduledTestResult{{Status: "success"}}}
	c, _ := contributionTestContext(http.MethodPost, "/account-contributions/openai/create-from-code")
	user := &service.User{ID: 42, Email: "owner@example.com", Username: "owner"}

	item := h.createOpenAIAuthContribution(c, user, OpenAIContributionAuthRequest{}, map[string]any{
		"access_token": "test-access-token",
		"plan_type":    "free",
	}, &service.OpenAITokenInfo{Email: "owner@example.com"}, "manual_authorization", 1)

	require.Equal(t, "created", item.Status)
	require.NotNil(t, admin.createInput)
	require.Equal(t, service.AccountContributionSourceValue, admin.createInput.Extra[service.AccountContributionSourceKey])
	require.Equal(t, "manual_authorization", admin.createInput.Extra[service.AccountContributionImportMethodKey])
	require.EqualValues(t, 42, admin.createInput.Extra[service.AccountContributorUserIDKey])
	require.Equal(t, service.AccountShareModePrivate, admin.createInput.Extra[service.AccountShareModeKey])
	created := &service.Account{Extra: admin.createInput.Extra}
	require.True(t, created.IsContributedBy(42))
}

func TestAnthropicContributionKeepsUserOwnershipAndStoresImportMethodSeparately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &contributionAdminServiceStub{}
	h := newContributionHandlerForTest(admin)
	c, _ := contributionTestContext(http.MethodPost, "/account-contributions/anthropic/create-from-code")
	user := &service.User{ID: 42, Email: "owner@example.com", Username: "owner"}

	item := h.createAnthropicAuthContributionWithNames(c, user, AnthropicContributionAuthRequest{}, map[string]any{
		"access_token":  "claude-access-token",
		"refresh_token": "claude-refresh-token",
	}, &service.TokenInfo{EmailAddress: "claude-owner@example.com"}, "manual_authorization", nil)

	require.Equal(t, "created", item.Status)
	require.NotNil(t, admin.createInput)
	require.Equal(t, "claude-owner@example.com", admin.createInput.Name)
	require.Equal(t, service.PlatformAnthropic, admin.createInput.Platform)
	require.Equal(t, service.AccountTypeOAuth, admin.createInput.Type)
	require.Equal(t, service.AccountContributionSourceValue, admin.createInput.Extra[service.AccountContributionSourceKey])
	require.Equal(t, "manual_authorization", admin.createInput.Extra[service.AccountContributionImportMethodKey])
	require.EqualValues(t, 42, admin.createInput.Extra[service.AccountContributorUserIDKey])
	require.Equal(t, service.AccountShareModePrivate, admin.createInput.Extra[service.AccountShareModeKey])
	created := &service.Account{Extra: admin.createInput.Extra}
	require.True(t, created.IsContributedBy(42))
}

func TestContributionExtraPreservesCodexImportMethod(t *testing.T) {
	extra := contributionExtra(map[string]any{
		service.AccountContributionSourceKey: "codex_session",
	}, &service.User{ID: 42}, accountContributionTZNow())

	require.Equal(t, service.AccountContributionSourceValue, extra[service.AccountContributionSourceKey])
	require.Equal(t, "codex_session", extra[service.AccountContributionImportMethodKey])
}

func TestAccountContributionListOnlyReturnsOwnedAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owned := service.Account{ID: 1, Name: "owned", Extra: contributionExtra(nil, &service.User{ID: 42}, accountContributionTZNow())}
	other := service.Account{ID: 2, Name: "other", Extra: contributionExtra(nil, &service.User{ID: 7}, accountContributionTZNow())}
	h := newContributionHandlerForTest(&contributionAdminServiceStub{accounts: []service.Account{owned, other}})
	c, recorder := contributionTestContext(http.MethodGet, "/account-contributions")

	h.List(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Data AccountContributionList `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, 1, envelope.Data.Total)
	require.Len(t, envelope.Data.Items, 1)
	require.Equal(t, int64(1), envelope.Data.Items[0].ID)
	require.Equal(t, service.AccountShareRewardRateDefaultPercent, envelope.Data.IncomeRates.ShareRewardRatePercent)
	require.Equal(t, 100-service.AccountOwnUsageFeeRateDefaultPercent, envelope.Data.IncomeRates.OwnIncomeRatePercent)
}

func TestAccountContributionListReturnsConfiguredActualIncomeRates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userService := service.NewUserService(
		&contributionUserRepoStub{user: &service.User{ID: 42, Email: "owner@example.com"}},
		&contributionSettingRepoStub{values: map[string]string{
			service.SettingKeyAccountShareRewardRate: "72.5",
			service.SettingKeyAccountOwnUsageFeeRate: "3",
		}},
		nil,
		nil,
	)
	h := NewAccountContributionHandler(userService, &contributionAdminServiceStub{}, nil, &service.AccountTestService{}, nil, nil, nil, nil)
	c, recorder := contributionTestContext(http.MethodGet, "/account-contributions")

	h.List(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Data AccountContributionList `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, 72.5, envelope.Data.IncomeRates.ShareRewardRatePercent)
	require.Equal(t, 97.0, envelope.Data.IncomeRates.OwnIncomeRatePercent)
}

func TestOwnedAccountRejectsAnotherContributorsAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	other := &service.Account{ID: 2, Extra: contributionExtra(nil, &service.User{ID: 7}, accountContributionTZNow())}
	h := newContributionHandlerForTest(&contributionAdminServiceStub{account: other})
	c, recorder := contributionTestContext(http.MethodPut, "/account-contributions/2")
	c.Params = gin.Params{{Key: "id", Value: "2"}}

	account, ok := h.ownedAccount(c)

	require.False(t, ok)
	require.Nil(t, account)
	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestNormalizeContributionPlatform(t *testing.T) {
	require.Equal(t, service.PlatformAnthropic, normalizeContributionPlatform(" Anthropic "))
	require.Equal(t, service.PlatformOpenAI, normalizeContributionPlatform("OPENAI"))
	require.Empty(t, normalizeContributionPlatform("unsupported"))
}

func TestAccountContributionSubmitSkipsExistingNameIgnoringCaseAndWhitespace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	existing := service.Account{
		ID:    7,
		Name:  "Existing@Example.com",
		Extra: contributionExtra(nil, &service.User{ID: 42}, accountContributionTZNow()),
	}
	admin := &contributionAdminServiceStub{accounts: []service.Account{existing}}
	h := newContributionHandlerForTest(admin)
	c, recorder := contributionTestContext(http.MethodPost, "/account-contributions")
	c.Request = httptest.NewRequest(http.MethodPost, "/account-contributions", strings.NewReader(`{
		"mode":"api_key","platform":"openai","name":" existing@example.COM ","api_key":"sk-test"
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})

	h.Submit(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var envelope struct {
		Data AccountContributionResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, 1, envelope.Data.Total)
	require.Equal(t, 0, envelope.Data.Created)
	require.Equal(t, 0, envelope.Data.Failed)
	require.Equal(t, 1, envelope.Data.Skipped)
	require.Len(t, envelope.Data.Items, 1)
	require.Equal(t, "skipped", envelope.Data.Items[0].Status)
	require.Nil(t, admin.createInput)
}

func TestAccountContributionSubmitAllowsDirectConnectionProxyZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &contributionAdminServiceStub{}
	h := newContributionHandlerForTest(admin)
	c, recorder := contributionTestContext(http.MethodPost, "/account-contributions")
	c.Request = httptest.NewRequest(http.MethodPost, "/account-contributions", strings.NewReader(`{
		"mode":"api_key","platform":"anthropic","name":"direct-account",
		"api_key":"sk-ant-test","proxy_id":0,"concurrency":30
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})

	h.Submit(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotNil(t, admin.createInput)
	require.Nil(t, admin.createInput.ProxyID)
	require.Equal(t, 30, admin.createInput.Concurrency)
}

func TestOpenAIContributionProxyZeroUsesDirectConnection(t *testing.T) {
	h := &AccountContributionHandler{}
	c, _ := contributionTestContext(http.MethodPost, "/account-contributions/openai/create-from-refresh-token")
	proxyID := int64(0)

	require.NoError(t, h.validateOpenAIContributionProxy(c, 42, &proxyID))
	proxyURL, err := h.openAIContributionProxyURL(c, 42, &proxyID)
	require.NoError(t, err)
	require.Empty(t, proxyURL)
}

func TestOpenAIContributionSkipsTokenDerivedExistingName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &contributionAdminServiceStub{}
	h := newContributionHandlerForTest(admin)
	c, _ := contributionTestContext(http.MethodPost, "/account-contributions/openai/create-from-code")
	names := map[string]struct{}{normalizeContributionAccountName("owner@example.com"): {}}

	item := h.createOpenAIAuthContributionWithNames(
		c,
		&service.User{ID: 42},
		OpenAIContributionAuthRequest{},
		map[string]any{"access_token": "test-access-token"},
		&service.OpenAITokenInfo{Email: " Owner@Example.com "},
		"manual_authorization",
		2,
		names,
	)

	require.Equal(t, "skipped", item.Status)
	require.Contains(t, item.Message, "同名账号")
	require.Nil(t, admin.createInput)
	result := contributionResultFromItem(item)
	require.Equal(t, 1, result.Skipped)
	require.Zero(t, result.Failed)
}

func TestContributionNameReservationRejectsDuplicatesWithinOneBatch(t *testing.T) {
	names := make(map[string]struct{})
	require.True(t, reserveContributionAccountName(names, " Batch@Example.com "))
	require.False(t, reserveContributionAccountName(names, "batch@example.COM"))
}

func TestContributionGroupSelectionRequiresMatchingPlatform(t *testing.T) {
	h := newContributionHandlerForTest(&contributionAdminServiceStub{groups: []service.Group{
		{ID: 1, Name: "Claude", Platform: service.PlatformAnthropic},
		{ID: 2, Name: "GPT", Platform: service.PlatformOpenAI},
	}})

	require.Error(t, h.validateContributionGroupSelection(context.Background(), service.PlatformOpenAI, service.AccountTypeOAuth, []int64{1}))
	require.NoError(t, h.validateContributionGroupSelection(context.Background(), service.PlatformOpenAI, service.AccountTypeOAuth, []int64{2}))
	require.Error(t, h.validateContributionGroupSelection(context.Background(), service.PlatformOpenAI, service.AccountTypeOAuth, []int64{2, 2}))
}

func TestContributionPoolBindingRequiresAdministratorEnabledMatchingGroup(t *testing.T) {
	poolID := int64(2)
	h := newContributionHandlerForTest(&contributionAdminServiceStub{groups: []service.Group{
		{ID: 1, Name: "Closed GPT", Platform: service.PlatformOpenAI, Status: service.StatusActive},
		{ID: 2, Name: "Open GPT", Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowContributionPool: true},
		{ID: 3, Name: "Claude", Platform: service.PlatformAnthropic, Status: service.StatusActive, AllowContributionPool: true},
	}})

	groups, err := h.resolveContributionGroupBinding(context.Background(), service.PlatformOpenAI, service.AccountTypeOAuth, nil, &poolID)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, groups)

	closedID := int64(1)
	_, err = h.resolveContributionGroupBinding(context.Background(), service.PlatformOpenAI, service.AccountTypeOAuth, nil, &closedID)
	require.Error(t, err)

	wrongPlatformID := int64(3)
	_, err = h.resolveContributionGroupBinding(context.Background(), service.PlatformOpenAI, service.AccountTypeOAuth, nil, &wrongPlatformID)
	require.Error(t, err)

	_, err = h.resolveContributionGroupBinding(context.Background(), service.PlatformOpenAI, service.AccountTypeOAuth, []int64{2}, &poolID)
	require.Error(t, err)
}

func TestContributionPoolPriorityHasMinimum(t *testing.T) {
	require.NoError(t, validateContributionPoolPriority(false, 0))
	require.Error(t, validateContributionPoolPriority(true, minimumContributionPoolPriority-1))
	require.NoError(t, validateContributionPoolPriority(true, minimumContributionPoolPriority))
}

func TestContributionPriorityOnlyUsesUserInputForAdministratorPoolAccounts(t *testing.T) {
	privatePriority := 1
	poolPriority := 21
	privateGroupID := int64(0)
	poolGroupID := int64(9)

	require.Equal(t, 0, defaultContributionPriority)
	require.Equal(t, defaultContributionPriority, contributionPriority(SubmitAccountContributionRequest{
		Priority:    &privatePriority,
		PoolGroupID: &privateGroupID,
	}))
	require.Equal(t, poolPriority, contributionPriority(SubmitAccountContributionRequest{
		Priority:    &poolPriority,
		PoolGroupID: &poolGroupID,
	}))
}

func TestContributionPoolConcurrencyHasMinimum(t *testing.T) {
	require.NoError(t, validateContributionPoolConcurrency(false, 1))
	require.Error(t, validateContributionPoolConcurrency(true, minimumContributionPoolConcurrency-1))
	require.NoError(t, validateContributionPoolConcurrency(true, minimumContributionPoolConcurrency))
}

func TestContributionUpdateResetsPrivateAccountPriorityToTheSystemDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &service.Account{
		ID:       1,
		Name:     "owned",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		GroupIDs: []int64{2},
		Extra:    contributionExtra(nil, &service.User{ID: 42}, accountContributionTZNow()),
	}
	admin := &contributionAdminServiceStub{account: account}
	h := newContributionHandlerForTest(admin)
	c, recorder := contributionTestContext(http.MethodPut, "/account-contributions/1")
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/account-contributions/1", strings.NewReader(`{"priority": 20, "load_factor": 4}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})

	h.Update(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, admin.updateInput)
	require.Nil(t, admin.updateInput.GroupIDs)
	require.NotNil(t, admin.updateInput.Priority)
	require.Equal(t, defaultContributionPriority, *admin.updateInput.Priority)
	require.NotNil(t, admin.updateInput.LoadFactor)
	require.Equal(t, 4, *admin.updateInput.LoadFactor)
}

func TestContributionSubmissionBindsFirstWorkingProxyAfterNetworkFailure(t *testing.T) {
	expired := time.Now().Add(-time.Hour)
	account := &service.Account{ID: 10, Platform: service.PlatformOpenAI}
	admin := &contributionAdminServiceStub{
		account: account,
		proxies: []service.Proxy{
			{ID: 8, Name: "disabled", Status: service.StatusDisabled},
			{ID: 4, Name: "expired", Status: service.StatusActive, ExpiresAt: &expired},
			{ID: 2, Name: "working-proxy", Status: service.StatusActive},
		},
	}
	h := newContributionHandlerForTest(admin)
	runner := &contributionTestRunnerStub{results: []service.ScheduledTestResult{
		{Status: "failed", ErrorMessage: "Request failed: dial tcp: connection refused"},
		{Status: "success", LatencyMs: 42},
	}}
	h.accountTestRunner = runner

	item := h.testCreatedContribution(context.Background(), account, "", AccountContributionResultItem{Index: 1}, true)

	require.Equal(t, "created", item.Status)
	require.Equal(t, int64(10), item.AccountID)
	require.Equal(t, int64(42), item.LatencyMs)
	require.Contains(t, item.Message, "working-proxy")
	require.NotNil(t, account.ProxyID)
	require.Equal(t, int64(2), *account.ProxyID)
	require.Empty(t, admin.deletedIDs)
	require.Equal(t, 2, runner.calls)
}

func TestContributionSubmissionDoesNotRetryProxyForUpstreamFailure(t *testing.T) {
	account := &service.Account{ID: 11, Platform: service.PlatformOpenAI}
	admin := &contributionAdminServiceStub{
		account: account,
		proxies: []service.Proxy{{ID: 2, Name: "working-proxy", Status: service.StatusActive}},
	}
	h := newContributionHandlerForTest(admin)
	runner := &contributionTestRunnerStub{results: []service.ScheduledTestResult{
		{Status: "failed", ErrorMessage: "API returned 401: invalid API key"},
	}}
	h.accountTestRunner = runner

	item := h.testCreatedContribution(context.Background(), account, "", AccountContributionResultItem{Index: 1}, true)

	require.Equal(t, "failed", item.Status)
	require.Equal(t, []int64{11}, admin.deletedIDs)
	require.Nil(t, account.ProxyID)
	require.Equal(t, 1, runner.calls)
}

func TestContributionExplicitProxyFailureDoesNotFallBackToAdminProxy(t *testing.T) {
	proxyID := int64(9)
	account := &service.Account{
		ID: 101, Name: "explicit-proxy", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		ProxyID: &proxyID,
		Extra:   contributionExtra(nil, &service.User{ID: 42}, accountContributionTZNow()),
	}
	admin := &contributionAdminServiceStub{
		account: account,
		proxies: []service.Proxy{{ID: 11, Name: "admin fallback", Protocol: "http", Host: "127.0.0.1", Port: 8080, Status: service.StatusActive}},
	}
	h := newContributionHandlerForTest(admin)
	runner := &contributionTestRunnerStub{results: []service.ScheduledTestResult{
		{Status: "failed", ErrorMessage: "dial tcp: proxy refused"},
		{Status: "success"},
	}}
	h.accountTestRunner = runner

	item := h.testCreatedContribution(context.Background(), account, "", AccountContributionResultItem{Index: 1}, false)

	require.Equal(t, "failed", item.Status)
	require.Equal(t, 1, runner.calls)
	require.Equal(t, []int64{account.ID}, admin.deletedIDs)
	require.Equal(t, proxyID, *account.ProxyID)
}

func accountContributionTZNow() (nowTime time.Time) {
	return time.Now().In(accountContributionTZ)
}
