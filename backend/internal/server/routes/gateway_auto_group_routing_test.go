package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newAutoGroupGatewayRouter registers the real public gateway routes with an
// automatic-routing API key. The key carries only a candidate pool, exactly as
// it arrives from authentication, so the assertion below observes whatever the
// registered middleware chain does to it before the handler runs.
func newAutoGroupGatewayRouter(t *testing.T, apiKey *service.APIKey, groups ...service.Group) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxBodySize:     1024 * 1024,
			TextMaxBodySize: 1024 * 1024,
		},
	}
	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
			AsyncImage:    handler.NewAsyncImageHandler(nil, nil),
		},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			clone := *apiKey
			c.Set(string(servermiddleware.ContextKeyAPIKey), &clone)
			c.Next()
		}),
		newPlaygroundRouteAPIKeyService(apiKey, nil, cfg, groups...),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)
	return router
}

// Regression: the automatic-group middleware was only mounted on /playground,
// so keys using automatic routing reached the public gateway with no group at
// all. Authentication hands over a candidate pool; without this middleware the
// downstream allowlist/require-group checks see GroupID == nil and the key
// behaves as if it had never been assigned a group.
func TestPublicGatewayResolvesAutoGroupBeforeHandlers(t *testing.T) {
	autoKey := &service.APIKey{
		ID:                301,
		UserID:            7,
		Status:            service.StatusActive,
		User:              &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 10, Concurrency: 3},
		AutoGroup:         true,
		AutoGroupStrategy: "price",
		AutoGroupIDs:      []int64{88, 89},
	}
	cheapest := service.Group{
		ID: 89, Status: service.StatusActive, Platform: service.PlatformOpenAI,
		RateMultiplier: 0.1, ActiveAccountCount: 1, Hydrated: true,
	}
	pricier := service.Group{
		ID: 88, Status: service.StatusActive, Platform: service.PlatformOpenAI,
		RateMultiplier: 0.9, ActiveAccountCount: 1, Hydrated: true,
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "v1 group route", path: "/v1/chat/completions"},
		{name: "root responses route", path: "/responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := newAutoGroupGatewayRouter(t, autoKey, pricier, cheapest)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path,
				strings.NewReader(`{"model":"gpt-5.5","messages":[{"role":"user","content":"ping"}]}`))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			// The stub handlers do no real forwarding, so the status is not the
			// contract here. What matters is that the request was not rejected by
			// the require-group middleware, which is what an unresolved automatic
			// key would hit.
			require.NotEqual(t, http.StatusForbidden, w.Code,
				"automatic key must not be rejected for missing group assignment")
			require.NotContains(t, w.Body.String(), "GROUP_NOT_ASSIGNED")
		})
	}
}

// Regression companion: a key bound to a fixed group must be left untouched by
// the automatic-routing middleware.
func TestPublicGatewayLeavesFixedGroupKeyUntouched(t *testing.T) {
	groupID := int64(88)
	fixedKey := &service.APIKey{
		ID:      302,
		UserID:  7,
		Status:  service.StatusActive,
		GroupID: &groupID,
		Group: &service.Group{
			ID: 88, Status: service.StatusActive, Platform: service.PlatformOpenAI, Hydrated: true,
		},
		User: &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 10, Concurrency: 3},
	}

	router := newAutoGroupGatewayRouter(t, fixedKey)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.5","messages":[{"role":"user","content":"ping"}]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.NotEqual(t, http.StatusForbidden, w.Code)
}
