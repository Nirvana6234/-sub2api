package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the real route registration and authentication middleware. A route
// that accidentally skips authentication reaches a nil data service and fails
// this test instead of silently exposing a new read endpoint to crawlers.
func TestSensitiveDataRoutesRejectUnauthenticatedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	auth := service.NewAuthService(nil, nil, nil, nil, &config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil)
	jwtAuth := middleware.NewJWTAuthMiddleware(auth, nil, nil, nil)
	adminAuth := middleware.NewAdminAuthMiddleware(auth, nil, nil, nil)
	audit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{}}
	v1 := router.Group("/api/v1")
	RegisterUserRoutes(v1, handlers, jwtAuth, audit, nil, nil)
	RegisterAdminRoutes(v1, handlers, adminAuth, audit, stepUp, nil, nil)
	RegisterPaymentRoutes(v1, nil, nil, nil, jwtAuth, adminAuth, audit, nil, nil)

	for _, path := range []string{
		"/api/v1/user/profile",
		"/api/v1/keys",
		"/api/v1/keys/1",
		"/api/v1/usage",
		"/api/v1/usage/1",
		"/api/v1/usage/errors/1",
		"/api/v1/usage/dashboard/stats",
		"/api/v1/tickets",
		"/api/v1/tickets/1",
		"/api/v1/account-contributions",
		"/api/v1/account-contributions/proxies",
		"/api/v1/channel-monitor-v2/users",
		"/api/v1/payment/orders/my",
		"/api/v1/payment/orders/1",
		"/api/v1/admin/users",
		"/api/v1/admin/accounts",
		"/api/v1/admin/settings",
		"/api/v1/admin/usage",
		"/api/v1/admin/tickets",
		"/api/v1/admin/payment/orders",
	} {
		for _, tc := range []struct {
			name string
			auth string
		}{
			{name: "anonymous"},
			{name: "empty bearer", auth: "Bearer "},
			{name: "invalid token", auth: "Bearer invalid.jwt.token"},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, path+"?user_id=1&role=admin&token=forged", nil)
				req.Header.Set("Authorization", tc.auth)
				req.Header.Set("X-User-ID", "1")
				req.Header.Set("X-User-Role", "admin")
				req.Header.Set("X-Forwarded-For", "127.0.0.1")
				router.ServeHTTP(rec, req)
				require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
			})
		}
	}
}
