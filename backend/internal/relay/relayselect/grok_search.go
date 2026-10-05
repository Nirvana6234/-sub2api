package relayselect

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// selectGrokSearch 按本地 GatewayHandler.WebSearch / XSearch（Grok 分组的独立搜索入口）的顺序：中间件复查 → 计费资格（没有用户并发槽）→
// 选号与准入（一轮，不带会话，不做利润终检）。换号状态在从节点的处理函数里。错误按这个入口自己的格式（`{"error":{"type","message"}}`）
// 由从节点写；准入失败的处理同 TypeSafe（第一次尝试的抢槽出错按并发错误回，其余排除这个账号接着选）。
func (s *selector) selectGrokSearch(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	gw := s.deps.AnthropicGateway
	if gw == nil {
		return unsupported(), nil
	}
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), modelCandidates(req),
		autoGroupChoice{pinned: req.GetAutoGroupId()}, service.PlatformGrok)
	if err != nil || rej != nil {
		return rej, err
	}
	apiKey := adm.APIKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	ctx = middleware.RelayRequestContext(ctx, adm)
	searchModel := req.GetModel()
	label := "grok-web-search"
	if strings.Contains(req.GetPath(), "x_search") {
		label = "grok-x-search"
	}
	subscription := adm.Billing.Subscription
	quotaReq := service.QuotaRequest{User: apiKey.User, APIKey: apiKey, Group: apiKey.Group, Subscription: subscription, Platform: service.QuotaPlatform(ctx, apiKey)}

	record, first, err := s.requestFor(nodeID, req.GetRequestId())
	if err != nil {
		return nil, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if !first && (record.userID != apiKey.User.ID || record.apiKeyID != apiKey.ID) {
		return nil, errors.New("relay request id reused by a different API key")
	}
	selected, keep := false, false
	defer func() {
		if !selected && !keep {
			s.dropRequest(record)
		}
	}()
	if first {
		record.userID, record.apiKeyID, record.groupID = apiKey.User.ID, apiKey.ID, groupIDOf(apiKey)
		if err := s.checkBilling(ctx, nodeID, req.GetHeldQuota(), apiKey, subscription, quotaReq.Platform); err != nil {
			return gatewayRejection(billingRejection(err, false)), nil
		}
		record.pricingCtx, record.pricingAt = context.WithoutCancel(ctx), time.Time{}
	}

	attemptCtx, cancel := context.WithCancel(record.pricingCtx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	excluded := make(map[int64]struct{}, len(req.GetExcludedAccountIds()))
	for _, id := range req.GetExcludedAccountIds() {
		excluded[id] = struct{}{}
	}
	outcome := s.anthropicAdmitter.SelectAndAdmit(attemptCtx, handler.AnthropicSelectRequest{
		GroupID: apiKey.GroupID, Model: searchModel, Excluded: excluded, NoProfitVeto: true,
	}, zap.NewNop())
	if ctx.Err() != nil {
		if outcome.Kind == handler.AnthropicSelected && outcome.Release != nil {
			outcome.Release()
		}
		return nil, ctx.Err()
	}
	switch outcome.Kind {
	case handler.AnthropicSelected:
	case handler.AnthropicSelectFailed:
		if len(excluded) > 0 {
			// 之前换过号、再选不出账号：本地 break，按最近一次错误写。
			keep = true
			return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
				Format: relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED, AnthropicMessages: true,
			}}}, nil
		}
		return gatewayRejection(handler.OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, ErrType: "scheduling_error", Message: outcome.Err.Error()}), nil
	default:
		if outcome.Kind == handler.AnthropicSelectSlotError && req.GetAttempt() <= 1 {
			return gatewayRejection(handler.OpenAIConcurrencyRejection(outcome.Err, "account")), nil
		}
		keep = true
		return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
			Format: relayv1.RejectionFormat_REJECTION_FORMAT_PROFIT_VETOED, VetoedAccountId: outcome.Account.ID,
		}}}, nil
	}
	if !openAICompatAccountServed(service.PlatformGrok, outcome.Account) || outcome.Account.Platform != service.PlatformGrok {
		// Grok OAuth 的凭据失败处理在主节点：放掉槽位，交给主节点转发。
		if outcome.Release != nil {
			outcome.Release()
		}
		return unsupported(), nil
	}

	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: outcome.Account, release: outcome.Release,
		createdAt: s.now(), quota: quotaReq, groupID: groupIDOf(apiKey), userID: apiKey.User.ID, apiKeyID: apiKey.ID, apiKey: apiKey,
		channelGroupID: groupIDOf(apiKey), omitChannelFields: true,
	}
	picked := handler.OpenAISelectOutcome{Kind: handler.OpenAISelected, Account: outcome.Account, Ctx: outcome.Ctx}
	// 用量的模型是固定的搜索名（grok-web-search / grok-x-search），选号用的是搜索模型：两个都进凭证允许的范围。
	resp, brej, err := s.buildSelection(ctx, nodeID, req, sel, picked, searchModel, label, service.ChannelMappingResult{}, subscription, false)
	if err == nil && brej == nil && ctx.Err() != nil {
		s.ungrant(sel.userID, nodeID, resp.GetSelection().GetGrants())
		err = ctx.Err()
	}
	if err != nil || brej != nil {
		if outcome.Release != nil {
			outcome.Release()
		}
		if brej != nil {
			return brej, nil
		}
		return nil, err
	}
	s.addSelection(sel)
	selected = true
	return resp, nil
}
