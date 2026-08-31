package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pawRouteStore struct {
	defaults service.PawDefaults
	err      error
}

func (s *pawRouteStore) GetPawDefaults(context.Context, int64) (service.PawDefaults, error) {
	return s.defaults, nil
}
func (s *pawRouteStore) SavePawDefaults(_ context.Context, _ int64, d service.PawDefaults) error {
	if s.err != nil {
		return s.err
	}
	s.defaults = d
	return nil
}

type pawRouteGroups struct{}

func (pawRouteGroups) AvailableGroups(context.Context, int64) ([]service.Group, error) {
	return []service.Group{{ID: 7, Name: "Group", Status: service.StatusActive, Platform: service.PlatformOpenAI}}, nil
}

type pawRouteUsers struct{}

func (pawRouteUsers) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 42, Username: "user", Email: "user@example.com"}, nil
}

type pawRouteChannels struct{}

func (pawRouteChannels) GetChannelForGroup(context.Context, int64) (*service.Channel, error) {
	return &service.Channel{Status: service.StatusActive, ModelPricing: []service.ChannelModelPricing{{Platform: service.PlatformOpenAI, Models: []string{"gpt-5"}}}}, nil
}

func pawRouteEngine(auth middleware.JWTAuthMiddleware, store *pawRouteStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var setting *service.SettingService
	svc := service.NewPawConfigService(pawRouteGroups{}, pawRouteUsers{}, pawRouteChannels{}, store)
	RegisterPawRoutes(r.Group("/api/v1"), svc, auth, setting, middleware.NewPanelRateLimiter(nil, setting))
	return r
}

func TestPawConfigRouteRequiresAuthentication(t *testing.T) {
	r := pawRouteEngine(func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) }, &pawRouteStore{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/paw/config", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPawConfigRouteReturnsEnvelopeAndPersistsDefaults(t *testing.T) {
	store := &pawRouteStore{}
	r := pawRouteEngine(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	}, store)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/paw/config", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"groups"`)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/paw/config/defaults", strings.NewReader(`{"group_id":7,"model_id":"gpt-5"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, service.PawDefaults{GroupID: 7, ModelID: "gpt-5"}, store.defaults)
}

func TestPawConfigRouteInvalidDefaultsReturnConfigUnavailable(t *testing.T) {
	store := &pawRouteStore{}
	r := pawRouteEngine(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	}, store)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/paw/config/defaults", strings.NewReader(`{"group_id":7,"model_id":"missing"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), PawErrorCodeConfigUnavailable)
}
