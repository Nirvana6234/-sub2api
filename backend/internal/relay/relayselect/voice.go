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

// grokVoiceSelectionModel 是语音 HTTP 入口选号用的模型（本地 GrokVoice 的 selectionModel；realtime 按能力选，不带模型）。
const grokVoiceSelectionModel = "grok-4.5"

// selectGrokVoice 按本地 GrokVoice / GrokRealtime 的顺序：中间件复查 → 计费资格（没有用户并发槽）→ 选号与准入（按聊天能力选，
// 没有会话，不装利润门）。换号状态在从节点。错误按这个入口自己的格式（`{"error":{"type","message"}}`）由从节点写；准入失败：第一次
// 尝试（还没排除过账号）回错误，之后的尝试只把这个账号排除再选（本地同样）。
func (s *selector) selectGrokVoice(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	endpoint := strings.TrimSpace(req.GetMediaEndpoint())
	if endpoint == "" {
		return unsupported(), nil
	}
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), nil,
		autoGroupChoice{pinned: req.GetAutoGroupId()}, service.PlatformGrok)
	if err != nil || rej != nil {
		return rej, err
	}
	apiKey := adm.APIKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	ctx = middleware.RelayRequestContext(ctx, adm)
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
		// 不装利润门、不固定计价时间（入账时按当时的价）。
		record.pricingCtx, record.pricingAt = context.WithoutCancel(ctx), time.Time{}
	}

	attemptCtx, cancel := context.WithCancel(record.pricingCtx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	for _, id := range req.GetExcludedAccountIds() {
		record.excluded[id] = struct{}{}
	}
	nodeExcluded := len(req.GetExcludedAccountIds()) > 0
	forwardModel := grokVoiceSelectionModel
	if endpoint == "realtime" {
		// 语音模型不是文本模型能力：带具体模型会把只映射了别的文本模型的账号提前拒掉，按能力选。
		forwardModel = ""
	}
	outcome := s.admitter.SelectAndAdmit(attemptCtx, handler.OpenAISelectRequest{
		GroupID:             apiKey.GroupID,
		ForwardModel:        forwardModel,
		RequestPlatform:     service.PlatformGrok,
		RequiredCapability:  service.OpenAIEndpointCapabilityChatCompletions,
		Transport:           service.OpenAIUpstreamTransportHTTPSSE,
		NoUpstreamTokenCost: true,
		Excluded:            record.excluded,
	}, &record.state, zap.NewNop())
	if ctx.Err() != nil {
		if outcome.Kind == handler.OpenAISelected && outcome.Release != nil {
			outcome.Release()
		}
		return nil, ctx.Err()
	}
	switch outcome.Kind {
	case handler.OpenAISelected:
	case handler.OpenAISelectAborted:
		return nil, context.Canceled
	case handler.OpenAISelectFailed, handler.OpenAISelectNone:
		if nodeExcluded {
			// 之前换过号：从节点按最近一次上游错误写。
			return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
				Format: relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED,
			}}}, nil
		}
		return gatewayRejection(handler.OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: "No available Grok accounts"}), nil
	default:
		if !nodeExcluded || outcome.Account == nil {
			// 第一次尝试：准入失败按错误回给客户端。
			return gatewayRejection(handler.OpenAISelectOutcomeRejection(outcome)), nil
		}
		keep = true
		return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
			Format: relayv1.RejectionFormat_REJECTION_FORMAT_PROFIT_VETOED, VetoedAccountId: outcome.Account.ID,
		}}}, nil
	}
	if !openAICompatAccountServed(service.PlatformGrok, outcome.Account) {
		// Grok OAuth 的凭据刷新与失败处理在主节点：放掉槽位，交给主节点转发。
		if outcome.Release != nil {
			outcome.Release()
		}
		return unsupported(), nil
	}

	// 用量行的模型：HTTP 入口是入口名（custom-voices 带语音 ID 路径时取第一段），realtime 是语音模型。
	requested := strings.SplitN(endpoint, "/", 2)[0]
	if endpoint == "realtime" {
		requested = req.GetMediaRequestModel()
	}
	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: outcome.Account, release: outcome.Release,
		createdAt: s.now(), quota: quotaReq, groupID: groupIDOf(apiKey), userID: apiKey.User.ID, apiKeyID: apiKey.ID, apiKey: apiKey,
		channelGroupID: groupIDOf(apiKey), extraModels: []string{grokVoiceSelectionModel},
	}
	resp, brej, err := s.buildSelection(ctx, nodeID, req, sel, outcome, forwardModel, requested, service.ChannelMappingResult{}, subscription, false)
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
