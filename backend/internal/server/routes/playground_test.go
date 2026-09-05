package routes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	serverhandler "github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type playgroundRouteAPIKeyRepo struct {
	apiKey          *service.APIKey
	verifyOwnership func(context.Context, int64, []int64) ([]int64, error)
	updateLastUsed  func(context.Context, int64, time.Time) error
}

type playgroundRouteUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *playgroundRouteUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	if r.user == nil {
		return nil, service.ErrUserNotFound
	}
	clone := *r.user
	return &clone, nil
}

type playgroundRouteGroupRepo struct {
	service.GroupRepository
	groups []service.Group
}

func (r *playgroundRouteGroupRepo) ListActive(context.Context) ([]service.Group, error) {
	return append([]service.Group(nil), r.groups...), nil
}

type playgroundRouteSubscriptionRepo struct {
	service.UserSubscriptionRepository
}

func (playgroundRouteSubscriptionRepo) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

func (r *playgroundRouteAPIKeyRepo) Create(context.Context, *service.APIKey) error {
	return errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) GetByID(context.Context, int64) (*service.APIKey, error) {
	if r.apiKey == nil {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *r.apiKey
	return &clone, nil
}
func (r *playgroundRouteAPIKeyRepo) GetKeyAndOwnerID(context.Context, int64) (string, int64, error) {
	if r.apiKey == nil {
		return "", 0, service.ErrAPIKeyNotFound
	}
	return r.apiKey.Key, r.apiKey.UserID, nil
}
func (r *playgroundRouteAPIKeyRepo) GetByKey(context.Context, string) (*service.APIKey, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) GetByKeyForAuth(context.Context, string) (*service.APIKey, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) Update(context.Context, *service.APIKey, service.APIKeyUpdateFields) error {
	return errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) Delete(context.Context, int64) error {
	return errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) DeleteWithAudit(context.Context, int64) error {
	return errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) ListByUserID(context.Context, int64, pagination.PaginationParams, service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	if r.verifyOwnership != nil {
		return r.verifyOwnership(ctx, userID, apiKeyIDs)
	}
	if r.apiKey != nil && userID == r.apiKey.UserID && len(apiKeyIDs) == 1 && apiKeyIDs[0] == r.apiKey.ID {
		return []int64{r.apiKey.ID}, nil
	}
	return []int64{}, nil
}
func (r *playgroundRouteAPIKeyRepo) CountByUserID(context.Context, int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) ExistsByKey(context.Context, string) (bool, error) {
	return false, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]service.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) SearchAPIKeys(context.Context, int64, string, int) ([]service.APIKey, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) ClearGroupIDByGroupID(context.Context, int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) UpdateGroupIDByUserAndGroup(context.Context, int64, int64, int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) CountByGroupID(context.Context, int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) ListKeysByUserID(context.Context, int64) ([]string, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) ListKeysByGroupID(context.Context, int64) ([]string, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) IncrementQuotaUsed(context.Context, int64, float64) (float64, error) {
	return 0, errors.New("not implemented")
}
func (r *playgroundRouteAPIKeyRepo) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	if r.updateLastUsed != nil {
		return r.updateLastUsed(ctx, id, usedAt)
	}
	return nil
}
func (r *playgroundRouteAPIKeyRepo) IncrementRateLimitUsage(context.Context, int64, float64) error {
	return nil
}
func (r *playgroundRouteAPIKeyRepo) ResetRateLimitWindows(context.Context, int64) error { return nil }
func (r *playgroundRouteAPIKeyRepo) GetRateLimitData(context.Context, int64) (*service.APIKeyRateLimitData, error) {
	return nil, nil
}

type playgroundRouteSettingRepo struct{ values map[string]string }

func (r *playgroundRouteSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundRouteSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}
func (r *playgroundRouteSettingRepo) Set(context.Context, string, string) error {
	return errors.New("not implemented")
}
func (r *playgroundRouteSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundRouteSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return errors.New("not implemented")
}
func (r *playgroundRouteSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundRouteSettingRepo) Delete(context.Context, string) error {
	return errors.New("not implemented")
}

func newPlaygroundRouteSettingService(playgroundEnabled, backendMode bool) *service.SettingService {
	return service.NewSettingService(&playgroundRouteSettingRepo{values: map[string]string{
		service.SettingKeyPlaygroundEnabled:  strconv.FormatBool(playgroundEnabled),
		service.SettingKeyBackendModeEnabled: strconv.FormatBool(backendMode),
		"panel_rate_limit_settings":          `{"enabled":false}`,
	}}, &config.Config{})
}

func newPlaygroundRouteAPIKeyService(apiKey *service.APIKey, verifyOwnership func(context.Context, int64, []int64) ([]int64, error), cfg *config.Config, autoGroups ...service.Group) *service.APIKeyService {
	apiKeyRepo := &playgroundRouteAPIKeyRepo{apiKey: apiKey, verifyOwnership: verifyOwnership}
	if apiKey == nil || !apiKey.AutoGroup {
		return service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	}
	return service.NewAPIKeyService(
		apiKeyRepo,
		&playgroundRouteUserRepo{user: apiKey.User},
		&playgroundRouteGroupRepo{groups: autoGroups},
		playgroundRouteSubscriptionRepo{},
		nil,
		nil,
		cfg,
	)
}

func newPlaygroundRoutesTestRouter(
	t *testing.T,
	apiKey *service.APIKey,
	playgroundEnabled, backendMode bool,
	verifyOwnership func(context.Context, int64, []int64) ([]int64, error),
	dispatch playgroundGatewayDispatch,
	autoGroups ...service.Group,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeSimple, Gateway: config.GatewayConfig{MaxBodySize: 1024 * 1024, TextMaxBodySize: 1024 * 1024}}
	apiKeyService := newPlaygroundRouteAPIKeyService(apiKey, verifyOwnership, cfg, autoGroups...)
	settingService := newPlaygroundRouteSettingService(playgroundEnabled, backendMode)
	panelRateLimiter := servermiddleware.NewPanelRateLimiter(nil, settingService)
	router := gin.New()
	v1 := router.Group("/api/v1")
	registerPlaygroundRoutes(
		v1,
		dispatch,
		servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 7, Concurrency: 5})
			c.Set(string(servermiddleware.ContextKeyUserRole), service.RoleUser)
			c.Next()
		}),
		apiKeyService,
		nil,
		nil,
		settingService,
		nil,
		cfg,
		panelRateLimiter,
	)
	return router
}

func noopPlaygroundGatewayDispatch() playgroundGatewayDispatch {
	return playgroundGatewayDispatch{
		models:            func(c *gin.Context) { c.Status(http.StatusNoContent) },
		chatCompletions:   func(c *gin.Context) { c.Status(http.StatusNoContent) },
		imagesGenerations: func(c *gin.Context) { c.Status(http.StatusNoContent) },
		videoGeneration:   func(c *gin.Context) { c.Status(http.StatusNoContent) },
		videoStatus:       func(c *gin.Context) { c.Status(http.StatusNoContent) },
		videoContent:      func(c *gin.Context) { c.Status(http.StatusNoContent) },
	}
}

func newPlaygroundRouteAPIKey(id int64, platform string) *service.APIKey {
	groupID := int64(88)
	return &service.APIKey{
		ID:      id,
		UserID:  7,
		Status:  service.StatusActive,
		User:    &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 10, Concurrency: 3},
		GroupID: &groupID,
		Group:   &service.Group{ID: groupID, Status: service.StatusActive, Platform: platform, Hydrated: true},
	}
}

func TestPlaygroundRoutesDisabledGateReturnsNotFound(t *testing.T) {
	router := newPlaygroundRoutesTestRouter(t, newPlaygroundRouteAPIKey(101, service.PlatformOpenAI), false, false, nil, noopPlaygroundGatewayDispatch())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playground/models", nil)
	req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "Playground is not enabled")
}

func TestPlaygroundRoutesForeignKeyReturnsNotFound(t *testing.T) {
	router := newPlaygroundRoutesTestRouter(t, newPlaygroundRouteAPIKey(101, service.PlatformOpenAI), true, false, func(context.Context, int64, []int64) ([]int64, error) {
		return []int64{}, nil
	}, noopPlaygroundGatewayDispatch())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playground/models", nil)
	req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestPlaygroundRoutesModelsUseSelectedKeyContextAndStripSelectorHeader(t *testing.T) {
	dispatch := noopPlaygroundGatewayDispatch()
	dispatch.models = func(c *gin.Context) {
		apiKey, ok := servermiddleware.GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.Equal(t, int64(101), apiKey.ID)
		require.Equal(t, serverhandler.EndpointModels, serverhandler.GetInboundEndpoint(c))
		require.Equal(t, true, c.Request.Context().Value(ctxkey.PlaygroundRequest))
		require.NotNil(t, apiKey.Group)
		require.Equal(t, service.PlatformOpenAI, apiKey.Group.Platform)

		subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
		require.True(t, ok)
		require.Equal(t, int64(7), subject.UserID)
		require.Equal(t, 3, subject.Concurrency)
		require.Empty(t, c.GetHeader(servermiddleware.PlaygroundKeyIDHeader))

		c.JSON(http.StatusOK, gin.H{"owned_by": apiKey.Group.Platform})
	}

	router := newPlaygroundRoutesTestRouter(t, newPlaygroundRouteAPIKey(101, service.PlatformOpenAI), true, false, nil, dispatch)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playground/models", nil)
	req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"owned_by":"openai"`)
	require.Empty(t, req.Header.Get(servermiddleware.PlaygroundKeyIDHeader))
}

func TestPlaygroundRoutesModelsResolveAutomaticKeyBeforeDispatch(t *testing.T) {
	autoKey := &service.APIKey{
		ID:                102,
		UserID:            7,
		Status:            service.StatusActive,
		User:              &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 10, Concurrency: 3},
		AutoGroup:         true,
		AutoGroupStrategy: "balanced",
		AutoGroupIDs:      []int64{88, 89},
	}
	dispatch := noopPlaygroundGatewayDispatch()
	dispatch.models = func(c *gin.Context) {
		apiKey, ok := servermiddleware.GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.True(t, apiKey.AutoGroup)
		require.NotNil(t, apiKey.GroupID)
		require.Equal(t, int64(89), *apiKey.GroupID)
		require.NotNil(t, apiKey.Group)
		require.Equal(t, service.PlatformOpenAI, apiKey.Group.Platform)
		c.Status(http.StatusNoContent)
	}

	router := newPlaygroundRoutesTestRouter(
		t,
		autoKey,
		true,
		false,
		nil,
		dispatch,
		service.Group{ID: 88, Status: service.StatusActive, Platform: service.PlatformOpenAI, RateMultiplier: 0.7, ActiveAccountCount: 1, Hydrated: true},
		service.Group{ID: 89, Status: service.StatusActive, Platform: service.PlatformOpenAI, RateMultiplier: 0.2, ActiveAccountCount: 1, Hydrated: true},
	)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playground/models", nil)
	req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "102")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestPlaygroundRoutesModelsRejectAutomaticKeyWithoutAvailableCandidate(t *testing.T) {
	autoKey := &service.APIKey{
		ID:                103,
		UserID:            7,
		Status:            service.StatusActive,
		User:              &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 10, Concurrency: 3},
		AutoGroup:         true,
		AutoGroupStrategy: "price",
		AutoGroupIDs:      []int64{88},
	}
	dispatch := noopPlaygroundGatewayDispatch()
	dispatch.models = func(c *gin.Context) {
		t.Fatal("models handler must not run without an available automatic candidate")
	}

	router := newPlaygroundRoutesTestRouter(t, autoKey, true, false, nil, dispatch)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playground/models", nil)
	req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "103")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "AUTO_GROUP_UNAVAILABLE")
}

func TestPlaygroundHistoryRouteUsesUserContext(t *testing.T) {
	dispatch := noopPlaygroundGatewayDispatch()
	dispatch.historyGet = func(c *gin.Context) {
		subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
		require.True(t, ok)
		require.Equal(t, int64(7), subject.UserID)
		require.Empty(t, c.GetHeader(servermiddleware.PlaygroundKeyIDHeader))
		c.Status(http.StatusNoContent)
	}
	dispatch.historySave = func(c *gin.Context) { c.Status(http.StatusNoContent) }

	router := newPlaygroundRoutesTestRouter(t, newPlaygroundRouteAPIKey(101, service.PlatformOpenAI), true, false, nil, dispatch)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playground/history", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestPlaygroundHistorySaveUsesDedicatedBodyLimit(t *testing.T) {
	dispatch := noopPlaygroundGatewayDispatch()
	dispatch.historyGet = func(c *gin.Context) { c.Status(http.StatusNoContent) }
	dispatch.historySave = func(c *gin.Context) {
		t.Fatal("history handler must not receive an oversized request")
	}

	router := newPlaygroundRoutesTestRouter(t, newPlaygroundRouteAPIKey(101, service.PlatformOpenAI), true, false, nil, dispatch)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/playground/history",
		strings.NewReader(strings.Repeat("x", service.PlaygroundHistoryMaxBytes+1)),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestPlaygroundRoutesChatCompletionsPreserveBodyAndSelectedKeyContext(t *testing.T) {
	dispatch := noopPlaygroundGatewayDispatch()
	dispatch.chatCompletions = func(c *gin.Context) {
		apiKey, ok := servermiddleware.GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.Equal(t, int64(101), apiKey.ID)
		require.Equal(t, serverhandler.EndpointChatCompletions, serverhandler.GetInboundEndpoint(c))
		require.Equal(t, true, c.Request.Context().Value(ctxkey.PlaygroundRequest))
		require.NotNil(t, apiKey.Group)
		require.Equal(t, service.PlatformAnthropic, apiKey.Group.Platform)

		subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
		require.True(t, ok)
		require.Equal(t, int64(7), subject.UserID)
		require.Equal(t, 3, subject.Concurrency)
		require.Empty(t, c.GetHeader(servermiddleware.PlaygroundKeyIDHeader))

		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.Data(http.StatusOK, "application/json", body)
	}

	router := newPlaygroundRoutesTestRouter(t, newPlaygroundRouteAPIKey(101, service.PlatformAnthropic), true, false, nil, dispatch)

	for _, body := range []string{
		`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"hi"}]}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "101")
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		require.JSONEq(t, body, w.Body.String())
		require.Empty(t, req.Header.Get(servermiddleware.PlaygroundKeyIDHeader))
	}
}

func TestPlaygroundRoutesImagesGenerationsUseSelectedKeyContextAndPreserveBody(t *testing.T) {
	dispatch := noopPlaygroundGatewayDispatch()
	dispatch.imagesGenerations = func(c *gin.Context) {
		apiKey, ok := servermiddleware.GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.Equal(t, int64(101), apiKey.ID)
		require.Equal(t, serverhandler.EndpointImagesGenerations, serverhandler.GetInboundEndpoint(c))
		require.Equal(t, true, c.Request.Context().Value(ctxkey.PlaygroundRequest))
		require.Equal(t, service.PlatformOpenAI, apiKey.Group.Platform)
		require.Empty(t, c.GetHeader(servermiddleware.PlaygroundKeyIDHeader))

		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.Data(http.StatusOK, "application/json", body)
	}

	router := newPlaygroundRoutesTestRouter(t, newPlaygroundRouteAPIKey(101, service.PlatformOpenAI), true, false, nil, dispatch)
	body := `{"model":"gpt-image-1.5","prompt":"a blue bird","size":"1024x1024"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, body, w.Body.String())
	require.Empty(t, req.Header.Get(servermiddleware.PlaygroundKeyIDHeader))
}

func TestPlaygroundRoutesImagesAutoKeySelectsGroupSupportingRequestedModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "explicit model", body: `{"model":"gpt-image-2","prompt":"a blue bird"}`},
		{name: "default image model", body: `{"prompt":"a blue bird"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			autoKey := &service.APIKey{
				ID:                104,
				UserID:            7,
				Status:            service.StatusActive,
				User:              &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 10, Concurrency: 3},
				AutoGroup:         true,
				AutoGroupStrategy: "price",
				AutoGroupIDs:      []int64{88, 89},
			}
			dispatch := noopPlaygroundGatewayDispatch()
			dispatch.imagesGenerations = func(c *gin.Context) {
				apiKey, ok := servermiddleware.GetAPIKeyFromContext(c)
				require.True(t, ok)
				require.NotNil(t, apiKey.GroupID)
				require.Equal(t, int64(89), *apiKey.GroupID)
				require.True(t, apiKey.Group.AllowImageGeneration)

				body, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				c.Data(http.StatusOK, "application/json", body)
			}

			router := newPlaygroundRoutesTestRouter(
				t,
				autoKey,
				true,
				false,
				nil,
				dispatch,
				service.Group{
					ID: 88, Status: service.StatusActive, Platform: service.PlatformOpenAI,
					RateMultiplier: 0.1, ActiveAccountCount: 1, Hydrated: true,
					ModelsListConfig: service.GroupModelsListConfig{Enabled: true, Models: []string{"gpt-5.6"}},
				},
				service.Group{
					ID: 89, Status: service.StatusActive, Platform: service.PlatformOpenAI,
					RateMultiplier: 0.2, ActiveAccountCount: 1, Hydrated: true, AllowImageGeneration: true,
					ModelsListConfig: service.GroupModelsListConfig{Enabled: true, Models: []string{"gpt-image-2"}},
				},
			)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/images/generations", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "104")
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			require.JSONEq(t, tc.body, w.Body.String())
		})
	}
}

func TestPlaygroundRoutesImagesGenerationsRejectUpstreamCredentialInBody(t *testing.T) {
	dispatch := noopPlaygroundGatewayDispatch()
	dispatch.imagesGenerations = func(c *gin.Context) {
		t.Fatal("image handler must not receive a body credential")
	}
	router := newPlaygroundRoutesTestRouter(t, newPlaygroundRouteAPIKey(101, service.PlatformOpenAI), true, false, nil, dispatch)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/images/generations", strings.NewReader(`{"model":"gpt-image-1.5","prompt":"x","api_key":"sk-upstream"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPlaygroundRoutesImageEditsUseSelectedKeyContextAndPreserveMultipartBody(t *testing.T) {
	dispatch := noopPlaygroundGatewayDispatch()
	dispatch.imagesGenerations = func(c *gin.Context) {
		apiKey, ok := servermiddleware.GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.Equal(t, int64(101), apiKey.ID)
		require.Equal(t, serverhandler.EndpointImagesEdits, serverhandler.GetInboundEndpoint(c))
		require.Equal(t, true, c.Request.Context().Value(ctxkey.PlaygroundRequest))
		require.Equal(t, service.PlatformOpenAI, apiKey.Group.Platform)
		require.Empty(t, c.GetHeader(servermiddleware.PlaygroundKeyIDHeader))

		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.Data(http.StatusOK, c.GetHeader("Content-Type"), body)
	}

	router := newPlaygroundRoutesTestRouter(t, newPlaygroundRouteAPIKey(101, service.PlatformOpenAI), true, false, nil, dispatch)
	body := "--playground-boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\ngpt-image-1.5\r\n--playground-boundary--\r\n"
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/images/edits", strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=playground-boundary")
	req.Header.Set(servermiddleware.PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, body, w.Body.String())
	require.Empty(t, req.Header.Get(servermiddleware.PlaygroundKeyIDHeader))
}
