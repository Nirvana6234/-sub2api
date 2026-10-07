package relayselect

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// selectSystemOne 按本地 GatewayHandler.SystemOne（TypeSafe 的 Jev 判断请求）的顺序：中间件复查 → 模型有没有价格 → 计价上下文
// （固定计价时间）→ 渠道映射 → 用户并发槽 → 计费资格 → 选号与准入（一轮，不带会话，装利润门）。换号状态（已失败的账号）在从节点的
// 处理函数里，最多换几次由主节点的 gateway.max_account_switches 随选号带下来。
//
// 准入失败（没有等待计划、队列满、抢槽出错）：第一次尝试时按并发错误回给客户端，之后的尝试只把这个账号排除再选（本地同样），
// 这里分别回 Gateway 拒绝和"否决"格式（从节点把它当成"排除这个账号接着选"）。错误由从节点按 TypeSafe 自己的格式写（code 在
// Gateway 拒绝的 code 里）。
func (s *selector) selectSystemOne(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	gw := s.deps.AnthropicGateway
	if gw == nil {
		return unsupported(), nil
	}
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), modelCandidates(req),
		autoGroupChoice{pinned: req.GetAutoGroupId()}, service.PlatformTypeSafe)
	if err != nil || rej != nil {
		return rej, err
	}
	apiKey := adm.APIKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	ctx = middleware.RelayRequestContext(ctx, adm)
	model := req.GetModel()
	subscription := adm.Billing.Subscription
	quotaReq := service.QuotaRequest{User: apiKey.User, APIKey: apiKey, Group: apiKey.Group, Subscription: subscription, Platform: service.QuotaPlatform(ctx, apiKey)}
	channelMapping, _ := gw.ResolveChannelMappingAndRestrict(ctx, apiKey.GroupID, model)

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
		// 两条网关找不到价格时都按 0 元入账，Jev 不能这样免费用：没配价格就不转发。
		if !gw.HasTypeSafePricing(ctx, model, apiKey) {
			return gatewayRejection(handler.OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, ErrType: "api_error", Code: "PRICING_UNAVAILABLE", Message: "Pricing is not configured for this model"}), nil
		}
		// 用户并发槽、计费资格；计价时间在请求开头固定（本地 service.WithGatewayTokenRequestPricing），入账时按它计价。
		pricing := func(ctx context.Context, _ *int64) (context.Context, time.Time) {
			return service.WithGatewayTokenRequestPricing(ctx)
		}
		if rej := s.startRequest(ctx, record, req, adm, quotaReq, false, false, pricing); rej != nil {
			return rej, nil
		}
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
		GroupID: apiKey.GroupID, Model: model, Excluded: excluded,
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
			// 之前换过号：从节点按最近一次上游错误写（本地：break 后 writeTypeSafeFailoverExhausted）。
			keep = true
			return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
				Format: relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED, AnthropicMessages: true,
			}}}, nil
		}
		return gatewayRejection(handler.TypeSafeFirstSelectFailureRejection(ctx, gw, apiKey, model, outcome.Err)), nil
	default:
		// 准入失败：第一次尝试按并发错误回给客户端（只有抢槽出错时）；其余把这个账号排除接着选。
		if outcome.Kind == handler.AnthropicSelectSlotError && req.GetAttempt() <= 1 {
			return gatewayRejection(handler.OpenAIConcurrencyRejection(outcome.Err, "account")), nil
		}
		keep = true
		return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
			Format: relayv1.RejectionFormat_REJECTION_FORMAT_PROFIT_VETOED, VetoedAccountId: outcome.Account.ID,
		}}}, nil
	}
	if a := outcome.Account; a == nil || a.Platform != service.PlatformTypeSafe || a.Type != service.AccountTypeAPIKey {
		if outcome.Release != nil {
			outcome.Release()
		}
		return unsupported(), nil
	}

	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: outcome.Account, release: outcome.Release,
		createdAt: s.now(), quota: quotaReq, groupID: groupIDOf(apiKey), userID: apiKey.User.ID, apiKeyID: apiKey.ID, apiKey: apiKey,
		channelGroupID: groupIDOf(apiKey),
	}
	picked := handler.OpenAISelectOutcome{Kind: handler.OpenAISelected, Account: outcome.Account, Ctx: outcome.Ctx}
	resp, brej, err := s.buildSelection(ctx, nodeID, req, sel, picked, model, model, channelMapping, subscription, false)
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
	resp.GetSelection().MaxAccountSwitches = int32(typeSafeMaxAccountSwitches)
	if cfg := s.deps.Config; cfg != nil && cfg.Gateway.MaxAccountSwitches > 0 {
		resp.GetSelection().MaxAccountSwitches = int32(cfg.Gateway.MaxAccountSwitches)
	}
	s.addSelection(sel)
	selected = true
	return resp, nil
}

// typeSafeMaxAccountSwitches 是没配置 gateway.max_account_switches 时的换号上限（与 NewGatewayHandler 的默认值一致）。
const typeSafeMaxAccountSwitches = anthropicDefaultMaxAccountSwitches
