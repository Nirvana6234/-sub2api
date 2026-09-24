package handler

import (
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func isAutoGroupSelectionFailoverError(err error) bool {
	return errors.Is(err, service.ErrNoAvailableAccounts) || errors.Is(err, service.ErrNoAvailableCompactAccounts)
}

func shouldTryOpenAIAutoGroupAfterTerminalFailover(err *service.UpstreamFailoverError) bool {
	if err == nil || err.ShouldRetryNextAccount() {
		return false
	}
	return err.IsCredentialFailure() || err.StatusCode == 401 || err.StatusCode == 403 || err.StatusCode >= 500
}

// tryOpenAIAutoGroupFailover advances an automatic API key to a candidate that
// has not been attempted in this request. Account failover is intentionally
// handled before this helper is called; reaching it means the current group's
// model-specific scheduling pool is exhausted or the group cannot serve the
// requested capability.
//
// The returned key is a request snapshot. The API key row and its configured
// candidate list are never rewritten. The authenticated context is refreshed
// so downstream billing, usage recording, and platform checks see the same
// group that the scheduler will use on the next loop iteration.
func tryOpenAIAutoGroupFailover(
	c *gin.Context,
	apiKeyService *service.APIKeyService,
	apiKey **service.APIKey,
	model string,
	failedGroupIDs map[int64]struct{},
	subscription **service.UserSubscription,
) bool {
	if c == nil || c.Request == nil || apiKeyService == nil || apiKey == nil || *apiKey == nil || !(*apiKey).AutoGroup {
		return false
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	if failedGroupIDs == nil {
		return false
	}
	previousGroupID := int64(0)
	if (*apiKey).GroupID != nil {
		previousGroupID = *(*apiKey).GroupID
		failedGroupIDs[previousGroupID] = struct{}{}
	}

	resolved, err := apiKeyService.ResolveAutoGroupForModelExcluding(c.Request.Context(), *apiKey, model, failedGroupIDs)
	if err != nil || resolved == nil || resolved.GroupID == nil {
		return false
	}
	if _, alreadyTried := failedGroupIDs[*resolved.GroupID]; alreadyTried {
		return false
	}

	if c != nil {
		var currentSubscription *service.UserSubscription
		if subscription != nil {
			currentSubscription = *subscription
		}
		if resolved.Group != nil && resolved.Group.IsSubscriptionType() {
			currentSubscription, _ = apiKeyService.GetActiveSubscriptionForGroup(c.Request.Context(), resolved.UserID, *resolved.GroupID)
		} else if currentSubscription != nil && currentSubscription.GroupID != *resolved.GroupID {
			currentSubscription = nil
		}
		if subscription != nil {
			*subscription = currentSubscription
		}
		middleware2.ReplaceAuthenticatedAPIKey(c, resolved, currentSubscription)
	}
	logger.FromContext(c.Request.Context()).Warn("openai.auto_group_failover",
		zap.Int64("api_key_id", (*apiKey).ID),
		zap.String("model", model),
		zap.Int64("from_group_id", previousGroupID),
		zap.Int64("to_group_id", *resolved.GroupID),
		zap.Int("failed_group_count", len(failedGroupIDs)),
	)
	*apiKey = resolved
	return true
}
