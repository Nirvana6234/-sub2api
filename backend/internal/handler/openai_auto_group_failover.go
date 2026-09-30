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

// IsAutoGroupSelectionFailoverError 见 isAutoGroupSelectionFailoverError（主从分流的主节点判断要不要让从节点换组）。
func IsAutoGroupSelectionFailoverError(err error) bool {
	return isAutoGroupSelectionFailoverError(err)
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
	if apiKeyService == nil {
		return false
	}
	return tryAutoGroupFailoverWith(c, func(key *service.APIKey, current *service.UserSubscription) (*service.APIKey, *service.UserSubscription, bool) {
		resolved, err := apiKeyService.ResolveAutoGroupForModelExcluding(c.Request.Context(), key, strings.TrimSpace(model), failedGroupIDs)
		if err != nil || resolved == nil || resolved.GroupID == nil {
			return nil, nil, false
		}
		if _, alreadyTried := failedGroupIDs[*resolved.GroupID]; alreadyTried {
			return nil, nil, false
		}
		if resolved.Group != nil && resolved.Group.IsSubscriptionType() {
			current, _ = apiKeyService.GetActiveSubscriptionForGroup(c.Request.Context(), resolved.UserID, *resolved.GroupID)
		} else if current != nil && current.GroupID != *resolved.GroupID {
			current = nil
		}
		return resolved, current, true
	}, apiKey, model, failedGroupIDs, subscription)
}

// relayAutoGroupBillingKey 存从节点上最近一次换组时主节点顺带做的计费资格复查结果（*OpenAIGatewayRejection）。
const relayAutoGroupBillingKey = "openai.relay.auto_group_billing"

// tryAutoGroupFailover 是处理函数里的自动分组换组：单机按本机的选组器选，从节点经主节点选
// （h.relay.SwitchAutoGroup）。换组之后的步骤两边相同。
func (h *OpenAIGatewayHandler) tryAutoGroupFailover(c *gin.Context, apiKey **service.APIKey, model string, failedGroupIDs map[int64]struct{}, subscription **service.UserSubscription) bool {
	if h.relay == nil {
		return tryOpenAIAutoGroupFailover(c, h.apiKeyService, apiKey, model, failedGroupIDs, subscription)
	}
	return tryAutoGroupFailoverWith(c, func(key *service.APIKey, _ *service.UserSubscription) (*service.APIKey, *service.UserSubscription, bool) {
		sw, ok := h.relay.SwitchAutoGroup(c, key, strings.TrimSpace(model), failedGroupIDs)
		if !ok || sw.APIKey == nil || sw.APIKey.GroupID == nil {
			return nil, nil, false
		}
		if _, alreadyTried := failedGroupIDs[*sw.APIKey.GroupID]; alreadyTried {
			return nil, nil, false
		}
		c.Set(relayAutoGroupBillingKey, sw.BillingRejection)
		return sw.APIKey, sw.Subscription, true
	}, apiKey, model, failedGroupIDs, subscription)
}

// relayBillingRejectionError 是主节点复查计费资格不过时回的拒绝（billingErrorDetails 原样取出它的状态码和文案）。
type relayBillingRejectionError struct {
	rejection OpenAIGatewayRejection
}

func (e *relayBillingRejectionError) Error() string { return e.rejection.Message }

// autoGroupFailoverBillingError 是"换号用完后换组"之后的计费资格复查：单机本地查；从节点用换组时主节点查好的结果。
func (h *OpenAIGatewayHandler) autoGroupFailoverBillingError(c *gin.Context, apiKey *service.APIKey, subscription *service.UserSubscription) error {
	if h.relay == nil {
		return h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey))
	}
	v, _ := c.Get(relayAutoGroupBillingKey)
	if r, ok := v.(*OpenAIGatewayRejection); ok && r != nil {
		return &relayBillingRejectionError{rejection: *r}
	}
	return nil
}

// autoGroupSwitchFunc 选下一个候选分组：返回换到的分组的 Key 快照和它的订阅（current 是换组前的订阅）；没有可换的
// 返回 false。
type autoGroupSwitchFunc func(apiKey *service.APIKey, current *service.UserSubscription) (*service.APIKey, *service.UserSubscription, bool)

func tryAutoGroupFailoverWith(
	c *gin.Context,
	switchGroup autoGroupSwitchFunc,
	apiKey **service.APIKey,
	model string,
	failedGroupIDs map[int64]struct{},
	subscription **service.UserSubscription,
) bool {
	if c == nil || c.Request == nil || apiKey == nil || *apiKey == nil || !(*apiKey).AutoGroup {
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

	var currentSubscription *service.UserSubscription
	if subscription != nil {
		currentSubscription = *subscription
	}
	resolved, currentSubscription, ok := switchGroup(*apiKey, currentSubscription)
	if !ok {
		return false
	}
	if subscription != nil {
		*subscription = currentSubscription
	}
	middleware2.ReplaceAuthenticatedAPIKey(c, resolved, currentSubscription)
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
