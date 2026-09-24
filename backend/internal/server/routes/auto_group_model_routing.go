package routes

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
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
			model = defaultAutoGroupModelForRequest(c.Request.URL.Path)
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

		status := c.Writer.Status()
		if streamErr, ok := service.GetOpsStreamError(c); ok && streamErr.IntendedStatus >= http.StatusBadRequest {
			status = streamErr.IntendedStatus
		}
		// Handlers map upstream 529 to a client-facing 503. Preserve the raw
		// upstream status for auto-group observation so overload is not mistaken
		// for a confirmed group failure.
		//
		// Only a failed request may take the raw upstream status. The key is
		// written by every upstream attempt and is not cleared when a later
		// attempt (often on another group after auto-group failover) succeeds;
		// letting it override a final 2xx would record the earlier group's 503
		// against the group that actually served the request.
		if rawStatus, ok := c.Get(service.OpsUpstreamStatusCodeKey); ok && status >= http.StatusBadRequest {
			switch typed := rawStatus.(type) {
			case int:
				if typed > 0 {
					status = typed
				}
			case int32:
				if typed > 0 {
					status = int(typed)
				}
			case int64:
				if typed > 0 {
					status = int(typed)
				}
			}
		}
		// A downstream handler may have switched an automatic key to another
		// candidate group after account failover. Observe the final request
		// snapshot from the context, otherwise a successful fallback request is
		// incorrectly recorded against the exhausted group that was selected at
		// middleware entry and the next request immediately pins it again.
		observedAPIKey := apiKey
		if current, ok := middleware.GetAPIKeyFromContext(c); ok && current != nil {
			observedAPIKey = current
		}
		apiKeyService.ObserveAutoGroupRequestResult(observedAPIKey, model, status, autoGroupFirstTokenMs(c))
	}
}

func defaultAutoGroupModelForRequest(path string) string {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	switch {
	case strings.HasSuffix(path, "/images/generations"),
		strings.HasSuffix(path, "/images/edits"),
		strings.HasSuffix(path, "/images/generations/async"),
		strings.HasSuffix(path, "/images/edits/async"):
		return "gpt-image-2"
	default:
		return ""
	}
}

func autoGroupFirstTokenMs(c *gin.Context) *int64 {
	if c == nil {
		return nil
	}
	value, ok := c.Get(service.OpsTimeToFirstTokenMsKey)
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case int64:
		return &typed
	case int:
		converted := int64(typed)
		return &converted
	case int32:
		converted := int64(typed)
		return &converted
	default:
		return nil
	}
}
