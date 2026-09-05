package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const PlaygroundKeyIDHeader = "X-Sub2API-Playground-Key-ID"

// PlaygroundFeatureGate fails closed until the setting service exposes an enabled flag.
func PlaygroundFeatureGate(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !playgroundEnabled(settingService, c.Request.Context()) {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Playground is not enabled",
				},
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

func playgroundEnabled(settingService *service.SettingService, ctx context.Context) bool {
	if settingService == nil {
		return false
	}
	return settingService.IsPlaygroundEnabled(ctx)
}

// PlaygroundRequestContext preserves browser cancellation through upstream relay calls.
func PlaygroundRequestContext(c *gin.Context) {
	if c.Request != nil {
		ctx := context.WithValue(c.Request.Context(), ctxkey.PlaygroundRequest, true)
		c.Request = c.Request.WithContext(ctx)
	}
	c.Next()
}

// PlaygroundSelectedAPIKeyAuth resolves the numeric key selector after JWT auth.
func PlaygroundSelectedAPIKeyAuth(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKeyService == nil {
			AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "API key service is unavailable")
			return
		}

		if strings.TrimSpace(c.GetHeader("x-api-key")) != "" ||
			strings.TrimSpace(c.GetHeader("x-goog-api-key")) != "" ||
			strings.TrimSpace(c.Query("key")) != "" ||
			strings.TrimSpace(c.Query("api_key")) != "" ||
			strings.TrimSpace(c.Query("key_id")) != "" ||
			strings.TrimSpace(c.Query("api_key_id")) != "" {
			AbortWithError(c, http.StatusBadRequest, "PLAYGROUND_KEY_SELECTOR_ONLY", "Playground accepts only X-Sub2API-Playground-Key-ID")
			return
		}

		keyIDText := strings.TrimSpace(c.GetHeader(PlaygroundKeyIDHeader))
		keyID, err := strconv.ParseInt(keyIDText, 10, 64)
		if keyIDText == "" || err != nil || keyID <= 0 {
			AbortWithError(c, http.StatusBadRequest, "INVALID_PLAYGROUND_KEY_ID", "X-Sub2API-Playground-Key-ID must be a positive integer")
			return
		}
		c.Request.Header.Del(PlaygroundKeyIDHeader)

		subject, ok := GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authenticated user context is required")
			return
		}

		ownedIDs, err := apiKeyService.VerifyOwnership(c.Request.Context(), subject.UserID, []int64{keyID})
		if err != nil {
			AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to validate API key ownership")
			return
		}
		if len(ownedIDs) != 1 || ownedIDs[0] != keyID {
			AbortWithError(c, http.StatusNotFound, "NOT_FOUND", "API key not found")
			return
		}

		apiKey, err := apiKeyService.GetByIDForAuth(c.Request.Context(), keyID)
		if err != nil {
			if errors.Is(err, service.ErrAPIKeyNotFound) {
				AbortWithError(c, http.StatusNotFound, "NOT_FOUND", "API key not found")
				return
			}
			if errors.Is(err, service.ErrAutoGroupUnavailable) {
				AbortWithError(c, http.StatusForbidden, "AUTO_GROUP_UNAVAILABLE", "No available group satisfies the automatic routing requirements")
				return
			}
			AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load API key")
			return
		}
		if apiKey == nil || apiKey.UserID != subject.UserID {
			AbortWithError(c, http.StatusNotFound, "NOT_FOUND", "API key not found")
			return
		}

		SetOpsFallbackAPIKey(c, apiKey)
		authenticateResolvedAPIKey(c, apiKey, apiKeyService, subscriptionService, cfg)
	}
}

// PlaygroundCredentialBodyGuard rejects credential selectors outside the required header.
func PlaygroundCredentialBodyGuard(c *gin.Context) {
	if c.Request == nil || c.Request.Body == nil {
		c.Next()
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": gin.H{
					"type":    "invalid_request_error",
					"message": pkghttputil.BodyTooLargeMessage(maxErr.Limit),
				},
			})
			return
		}
		AbortWithError(c, http.StatusBadRequest, "INVALID_REQUEST", "Failed to read request body")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))

	if len(body) == 0 || !json.Valid(body) {
		c.Next()
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		c.Next()
		return
	}
	for _, field := range []string{"key", "api_key", "key_id", "api_key_id"} {
		if _, exists := fields[field]; exists {
			AbortWithError(c, http.StatusBadRequest, "PLAYGROUND_KEY_SELECTOR_ONLY", "Playground accepts only X-Sub2API-Playground-Key-ID")
			return
		}
	}
	c.Next()
}
