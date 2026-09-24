// 这些回归用例来自 2026-09-24 对自动分组/兜底池改动的外部评审复现。

package routes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Runs the real middleware and auto-group service. The endpoint reproduces the
// passthrough forwarding contract: an attempt records raw 503, returns a
// retryable error, switches group, and the final attempt returns HTTP 200.
// Production passthrough writes the stale status at
// openai_gateway_passthrough.go:924 and returns failover at :943.
func TestRecoveredAutoGroupDoesNotObserveEarlierUpstreamFailure(t *testing.T) {
	for _, earlierStatus := range []int{0, http.StatusServiceUnavailable, http.StatusBadGateway} {
		t.Run(fmt.Sprintf("earlier_status_%d", earlierStatus), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			key := &service.APIKey{
				ID: 891, UserID: 7, AutoGroup: true, AutoGroupStrategy: "price", AutoGroupIDs: []int64{10, 20},
				User: &service.User{ID: 7, Status: service.StatusActive},
			}
			svc := newPlaygroundRouteAPIKeyService(key, nil, &config.Config{},
				service.Group{ID: 10, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: .1, ActiveAccountCount: 1},
				service.Group{ID: 20, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: .2, ActiveAccountCount: 1},
			)
			router := gin.New()
			router.Use(func(c *gin.Context) {
				middleware.ReplaceAuthenticatedAPIKey(c, key, nil)
				c.Next()
			})
			router.Use(autoGroupModelRoutingMiddleware(svc, nil))
			var successfulKey *service.APIKey
			router.POST("/v1/responses", func(c *gin.Context) {
				initial, ok := middleware.GetAPIKeyFromContext(c)
				require.True(t, ok)
				require.Equal(t, int64(10), *initial.GroupID)
				if earlierStatus != 0 {
					service.SetOpsUpstreamError(c, earlierStatus, "earlier group attempt failed", "")
				}
				var err error
				successfulKey, err = svc.ResolveAutoGroupForModelExcluding(c.Request.Context(), initial, "gpt-regression", map[int64]struct{}{10: {}})
				require.NoError(t, err)
				require.Equal(t, int64(20), *successfulKey.GroupID)
				middleware.ReplaceAuthenticatedAPIKey(c, successfulKey, nil)
				c.JSON(http.StatusOK, gin.H{"ok": true})
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-regression"}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusOK, recorder.Code)

			// Two actual failures must remain below the three-failure threshold.
			// If the successful request above was counted as a failure, the next
			// resolve incorrectly moves B back to the previously failed A.
			svc.ObserveAutoGroupRequestResult(successfulKey, "gpt-regression", 503, nil)
			svc.ObserveAutoGroupRequestResult(successfulKey, "gpt-regression", 503, nil)
			next, err := svc.ResolveAutoGroupForModel(context.Background(), key, "gpt-regression")
			require.NoError(t, err)
			require.Equal(t, int64(20), *next.GroupID, "a recovered HTTP 200 must not count the earlier attempt's failure against group B")
		})
	}
}
