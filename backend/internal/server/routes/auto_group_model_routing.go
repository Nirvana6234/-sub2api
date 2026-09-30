package routes

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// autoGroupModelRoutingMiddleware runs after API-key authentication but before
// platform dispatch. It restores the already body-limited request after
// extracting model, then replaces the cold-start group with the final
// model-aware automatic choice.
func autoGroupModelRoutingMiddleware(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || !apiKey.AutoGroup || c.Request == nil || c.Request.Method == http.MethodGet {
			c.Next()
			return
		}

		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			status := http.StatusBadRequest
			message := "Failed to read request body"
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				status = http.StatusRequestEntityTooLarge
				message = "Request body is too large"
			}
			c.JSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "message": message}})
			c.Abort()
			return
		}
		model := compositeRequestModelFromBody(c.GetHeader("Content-Type"), body)
		if strings.TrimSpace(model) == "" {
			model = compositeGeminiModelFromParams(c)
		}
		if strings.TrimSpace(model) == "" {
			model = requestmodel.DefaultAutoGroupModel(c.Request.URL.Path)
		}
		resetRequestBody(c, body)
		if strings.TrimSpace(model) == "" {
			c.Next()
			return
		}

		resolved, err := apiKeyService.ResolveAutoGroupForModel(c.Request.Context(), apiKey, model)
		if err != nil {
			if errors.Is(err, service.ErrAutoGroupUnavailable) {
				middleware.AbortWithError(c, http.StatusForbidden, "AUTO_GROUP_UNAVAILABLE", "No available group satisfies the automatic routing requirements")
			} else {
				middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve automatic API key group")
			}
			return
		}
		if resolved.GroupID != nil && (apiKey.GroupID == nil || *resolved.GroupID != *apiKey.GroupID) {
			var subscription *service.UserSubscription
			if resolved.Group != nil && resolved.Group.IsSubscriptionType() && subscriptionService != nil {
				subscription, _ = subscriptionService.GetActiveSubscription(c.Request.Context(), resolved.UserID, resolved.Group.ID)
			}
			middleware.ReplaceAuthenticatedAPIKey(c, resolved, subscription)
		}
		c.Next()

		status, firstTokenMs := middleware.AutoGroupObservedResult(c)
		// A downstream handler may have switched an automatic key to another
		// candidate group after account failover. Observe the final request
		// snapshot from the context, otherwise a successful fallback request is
		// incorrectly recorded against the exhausted group that was selected at
		// middleware entry and the next request immediately pins it again.
		observedAPIKey := apiKey
		if current, ok := middleware.GetAPIKeyFromContext(c); ok && current != nil {
			observedAPIKey = current
		}
		apiKeyService.ObserveAutoGroupRequestResult(observedAPIKey, model, status, firstTokenMs)
	}
}
