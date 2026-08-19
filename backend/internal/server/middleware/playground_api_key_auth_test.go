package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type playgroundAuthAPIKeyRepo struct {
	getByID         func(context.Context, int64) (*service.APIKey, error)
	verifyOwnership func(context.Context, int64, []int64) ([]int64, error)
	updateLastUsed  func(context.Context, int64, time.Time) error
}

func (r *playgroundAuthAPIKeyRepo) Create(context.Context, *service.APIKey) error {
	return errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) GetByID(ctx context.Context, id int64) (*service.APIKey, error) {
	if r.getByID != nil {
		return r.getByID(ctx, id)
	}
	return nil, service.ErrAPIKeyNotFound
}
func (r *playgroundAuthAPIKeyRepo) GetKeyAndOwnerID(context.Context, int64) (string, int64, error) {
	return "", 0, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) GetByKey(context.Context, string) (*service.APIKey, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) GetByKeyForAuth(context.Context, string) (*service.APIKey, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) Update(context.Context, *service.APIKey, service.APIKeyUpdateFields) error {
	return errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) Delete(context.Context, int64) error {
	return errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) DeleteWithAudit(context.Context, int64) error {
	return errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) ListByUserID(context.Context, int64, pagination.PaginationParams, service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	if r.verifyOwnership != nil {
		return r.verifyOwnership(ctx, userID, apiKeyIDs)
	}
	return nil, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) CountByUserID(context.Context, int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) ExistsByKey(context.Context, string) (bool, error) {
	return false, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]service.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) SearchAPIKeys(context.Context, int64, string, int) ([]service.APIKey, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) ClearGroupIDByGroupID(context.Context, int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) UpdateGroupIDByUserAndGroup(context.Context, int64, int64, int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) CountByGroupID(context.Context, int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) ListKeysByUserID(context.Context, int64) ([]string, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) ListKeysByGroupID(context.Context, int64) ([]string, error) {
	return nil, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) IncrementQuotaUsed(context.Context, int64, float64) (float64, error) {
	return 0, errors.New("not implemented")
}
func (r *playgroundAuthAPIKeyRepo) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	if r.updateLastUsed != nil {
		return r.updateLastUsed(ctx, id, usedAt)
	}
	return nil
}
func (r *playgroundAuthAPIKeyRepo) IncrementRateLimitUsage(context.Context, int64, float64) error {
	return nil
}
func (r *playgroundAuthAPIKeyRepo) ResetRateLimitWindows(context.Context, int64) error { return nil }
func (r *playgroundAuthAPIKeyRepo) GetRateLimitData(context.Context, int64) (*service.APIKeyRateLimitData, error) {
	return nil, nil
}

func newPlaygroundAuthRouter(t *testing.T, apiKeyService *service.APIKeyService, cfg *config.Config) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 7, Concurrency: 999})
		c.Set(string(ContextKeyUserRole), service.RoleUser)
		c.Next()
	})
	r.Use(PlaygroundSelectedAPIKeyAuth(apiKeyService, nil, cfg))
	r.POST("/playground", func(c *gin.Context) {
		apiKey, ok := GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.Equal(t, int64(101), apiKey.ID)

		subject, ok := GetAuthSubjectFromContext(c)
		require.True(t, ok)
		require.Equal(t, int64(7), subject.UserID)
		require.Equal(t, 3, subject.Concurrency)

		role, ok := GetUserRoleFromContext(c)
		require.True(t, ok)
		require.Equal(t, service.RoleAdmin, role)

		userIDFromCtx, ok := c.Request.Context().Value(ctxkey.UserID).(int64)
		require.True(t, ok)
		require.Equal(t, int64(7), userIDFromCtx)
		apiKeyIDFromCtx, ok := c.Request.Context().Value(ctxkey.APIKeyID).(int64)
		require.True(t, ok)
		require.Equal(t, int64(101), apiKeyIDFromCtx)
		groupFromCtx, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group)
		require.True(t, ok)
		require.NotNil(t, groupFromCtx)
		require.Equal(t, int64(88), groupFromCtx.ID)
		require.Empty(t, c.GetHeader(PlaygroundKeyIDHeader))

		c.Status(http.StatusNoContent)
	})
	return r
}

func TestPlaygroundSelectedAPIKeyAuthRejectsMissingAndInvalidSelectors(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeSimple}
	apiKeyService := service.NewAPIKeyService(&playgroundAuthAPIKeyRepo{}, nil, nil, nil, nil, nil, cfg)
	router := newPlaygroundAuthRouter(t, apiKeyService, cfg)

	for _, tc := range []struct {
		name   string
		header string
		value  string
		query  string
	}{
		{name: "missing"},
		{name: "non_numeric", header: PlaygroundKeyIDHeader, value: "abc"},
		{name: "zero", header: PlaygroundKeyIDHeader, value: "0"},
		{name: "raw_key_header", header: "x-api-key", value: "sk-secret"},
		{name: "query_key", query: "?key=legacy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/playground"+tc.query, nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestPlaygroundSelectedAPIKeyAuthReturnsNotFoundForForeignKeyBeforeLoading(t *testing.T) {
	var getByIDCalls int
	repo := &playgroundAuthAPIKeyRepo{
		verifyOwnership: func(context.Context, int64, []int64) ([]int64, error) {
			return []int64{}, nil
		},
		getByID: func(context.Context, int64) (*service.APIKey, error) {
			getByIDCalls++
			return nil, service.ErrAPIKeyNotFound
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	apiKeyService := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	router := newPlaygroundAuthRouter(t, apiKeyService, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/playground", nil)
	req.Header.Set(PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Zero(t, getByIDCalls)
}

func TestPlaygroundSelectedAPIKeyAuthSetsSameContextAsResolvedAPIKeyFlow(t *testing.T) {
	groupID := int64(88)
	apiKey := &service.APIKey{
		ID:     101,
		UserID: 7,
		Status: service.StatusActive,
		User: &service.User{
			ID:          7,
			Role:        service.RoleAdmin,
			Status:      service.StatusActive,
			Balance:     10,
			Concurrency: 3,
		},
		GroupID: &groupID,
		Group: &service.Group{
			ID:       groupID,
			Status:   service.StatusActive,
			Platform: service.PlatformOpenAI,
			Hydrated: true,
		},
	}
	var touchCalls int
	repo := &playgroundAuthAPIKeyRepo{
		verifyOwnership: func(context.Context, int64, []int64) ([]int64, error) {
			return []int64{101}, nil
		},
		getByID: func(context.Context, int64) (*service.APIKey, error) {
			clone := *apiKey
			return &clone, nil
		},
		updateLastUsed: func(context.Context, int64, time.Time) error {
			touchCalls++
			return nil
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	apiKeyService := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	router := newPlaygroundAuthRouter(t, apiKeyService, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/playground", nil)
	req.Header.Set(PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, 1, touchCalls)
	require.Empty(t, req.Header.Get(PlaygroundKeyIDHeader))
}

func TestPlaygroundSelectedAPIKeyAuthReusesResolvedKeyDenials(t *testing.T) {
	apiKey := &service.APIKey{
		ID:     101,
		UserID: 7,
		Status: service.StatusAPIKeyDisabled,
		User:   &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 10, Concurrency: 1},
	}
	repo := &playgroundAuthAPIKeyRepo{
		verifyOwnership: func(context.Context, int64, []int64) ([]int64, error) { return []int64{101}, nil },
		getByID: func(context.Context, int64) (*service.APIKey, error) {
			clone := *apiKey
			return &clone, nil
		},
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	apiKeyService := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	router := newPlaygroundAuthRouter(t, apiKeyService, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/playground", nil)
	req.Header.Set(PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "API_KEY_DISABLED")
}

func TestPlaygroundSelectedAPIKeyAuthKeepsGatewayChatQuotaErrorFormat(t *testing.T) {
	groupID := int64(88)
	apiKey := &service.APIKey{
		ID:      101,
		UserID:  7,
		Status:  service.StatusAPIKeyQuotaExhausted,
		User:    &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 10, Concurrency: 1},
		GroupID: &groupID,
		Group: &service.Group{
			ID:       groupID,
			Status:   service.StatusActive,
			Platform: service.PlatformOpenAI,
			Hydrated: true,
		},
	}
	repo := &playgroundAuthAPIKeyRepo{
		verifyOwnership: func(context.Context, int64, []int64) ([]int64, error) { return []int64{101}, nil },
		getByID: func(context.Context, int64) (*service.APIKey, error) {
			clone := *apiKey
			return &clone, nil
		},
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	apiKeyService := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	router := newPlaygroundAuthRouter(t, apiKeyService, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/playground", nil)
	req.Header.Set(PlaygroundKeyIDHeader, "101")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.JSONEq(t, `{"code":"API_KEY_QUOTA_EXHAUSTED","message":"API key 额度已用完"}`, w.Body.String())
}

func TestPlaygroundCredentialBodyGuardRejectsCredentialFieldsInBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/chat", PlaygroundCredentialBodyGuard, func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, body := range []string{
		`{"api_key":"sk-secret"}`,
		`{"key_id":101}`,
		`{"api_key_id":101}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"model":"claude-sonnet-4-5"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestPlaygroundCredentialBodyGuardPreservesBodyLimitErrorContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestBodyLimit(16))
	router.POST("/chat", PlaygroundCredentialBodyGuard, func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"model":"long-model-name"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	require.JSONEq(t, `{"error":{"type":"invalid_request_error","message":"Request body too large, limit is 16B"}}`, w.Body.String())
}
