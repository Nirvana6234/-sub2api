package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCPASchedulingAuthenticationIsDedicatedAndFailsClosed(t *testing.T) {
	token := strings.Repeat("test-secret-", 3)
	for _, tc := range []struct {
		name, configured, bearer, apiKey string
		duplicate                        bool
		want                             int
	}{
		{name: "unconfigured", bearer: token, want: 401},
		{name: "missing", configured: token, want: 401},
		{name: "wrong", configured: token, bearer: "wrong-token", want: 401},
		{name: "admin header rejected", configured: token, apiKey: token, want: 401},
		{name: "duplicate authorization", configured: token, bearer: token, duplicate: true, want: 401},
		{name: "dedicated read credential", configured: token, bearer: token, want: 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			called := false
			r.GET("/profile", cpaSchedulingAuth(&config.Config{CPAScheduling: config.CPASchedulingConfig{SyncToken: tc.configured}}), func(c *gin.Context) { called = true; c.Status(204) })
			req := httptest.NewRequest(http.MethodGet, "/profile", nil)
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.duplicate {
				req.Header.Add("Authorization", "Bearer "+tc.bearer)
			}
			if tc.apiKey != "" {
				req.Header.Set("x-api-key", tc.apiKey)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code)
			require.Equal(t, tc.want == 204, called)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.NotContains(t, w.Body.String(), token)
		})
	}
}

func TestCPASchedulingRegistersOnlyReadRouteAndHandlesUnavailableService(t *testing.T) {
	token := strings.Repeat("read-only-", 4)
	r := gin.New()
	RegisterCPASchedulingRoutes(r.Group("/api/v1"), nil, &config.Config{CPAScheduling: config.CPASchedulingConfig{SyncToken: token}})
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/internal/cpa/scheduling-profile", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if method == http.MethodGet {
			require.Equal(t, 503, w.Code)
		} else {
			require.Equal(t, 404, w.Code)
		}
	}
}
