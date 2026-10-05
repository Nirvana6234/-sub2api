package relayselect

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// selectOpenAICountTokens 按本地 OpenAI 分组的两个 token 计数入口的顺序：中间件复查 → 计费资格（不占用户槽）→ 按模型选一个账号
// （SelectAccountForTokenCount，不占账号槽、不装利润门）。不计费：没有凭证、不给额度（设计 5.3 不计费请求不签凭证）。
//   - POST /v1/responses/input_tokens（OpenAIGatewayHandler.ResponsesInputTokens）：路由模型是渠道映射后的模型，错误按 OpenAI 格式；
//   - POST /v1/messages/count_tokens（OpenAIGatewayHandler.CountTokens）：路由模型是分组派发映射或规范化后的模型，错误按 Anthropic 格式。
//
// 选号结果带账号快照，从节点照本地转发。
func (s *selector) selectOpenAICountTokens(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	anthropic := req.GetEndpoint() == relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_COUNT_TOKENS
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), modelCandidates(req),
		autoGroupChoice{pinned: req.GetAutoGroupId()})
	if err != nil || rej != nil {
		return rej, err
	}
	apiKey := adm.APIKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	composite, err := s.resolveComposite(ctx, apiKey, req.GetRouteModel(), req.GetPath())
	if err != nil {
		return nil, err
	}
	requestPlatform, platformOK := openAIRequestPlatform(apiKey, composite, true)
	if !platformOK {
		return unsupported(), nil
	}
	ctx = middleware.RelayRequestContext(ctx, adm)
	ctx = service.WithCompositeRouteDecision(ctx, composite)
	reqModel := req.GetModel()
	// 不计费的请求不能被利润门排除（本地同样先豁免）。
	ctx = service.WithOpenAIProfitControlSuppressed(ctx)

	mapping, _ := s.deps.Gateway.ResolveChannelMappingAndRestrict(ctx, apiKey.GroupID, reqModel)
	var routingModel string
	if anthropic {
		routingModel = handler.OpenAIMessagesRoutingModelFor(apiKey, composite.TargetPlatform, reqModel)
	} else {
		routingModel = reqModel
		if mapping.Mapped {
			routingModel = mapping.MappedModel
		}
	}
	if err := s.checkBilling(ctx, nodeID, req.GetHeldQuota(), apiKey, adm.Billing.Subscription, service.QuotaPlatform(ctx, apiKey)); err != nil {
		return gatewayRejection(billingRejection(err, anthropic)), nil
	}
	account, err := s.deps.Gateway.SelectAccountForTokenCount(ctx, apiKey.GroupID, req.GetSessionHash(), routingModel,
		service.OpenAIEndpointCapabilityChatCompletions, requestPlatform)
	if err != nil || account == nil {
		return gatewayRejection(handler.OpenAICountTokensNoAccountRejection(ctx, s.deps.Gateway, apiKey, routingModel, reqModel, anthropic, err)), nil
	}
	if !openAICompatAccountServed(requestPlatform, account) {
		return unsupported(), nil
	}

	record, _, err := s.requestFor(nodeID, req.GetRequestId())
	if err != nil {
		return nil, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	record.userID, record.apiKeyID, record.sessionKey = apiKey.User.ID, apiKey.ID, req.GetSessionHash()
	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: account, createdAt: s.now(),
		groupID: groupIDOf(apiKey), userID: apiKey.User.ID, apiKeyID: apiKey.ID, apiKey: apiKey, countTokens: true,
	}
	fail := func(err error) (*relayv1.SelectResponse, error) {
		s.dropRequest(record)
		return nil, err
	}
	snap, err := s.encodeAccount(ctx, nodeID, account, s.nodeHas(nodeID))
	if err != nil {
		return fail(err)
	}
	var parentSnap *relayv1.AccountSnapshot
	if account.IsShadow() {
		if parentSnap, err = s.encodeCredentialParent(ctx, nodeID, account, s.nodeHas(nodeID)); err != nil {
			return fail(err)
		}
	}
	version, err := s.env.ConfigVersion(ctx, nodeID)
	if err != nil {
		return fail(err)
	}
	selection := &relayv1.Selection{
		SelectionId: sel.id, UserId: sel.userID, ApiKeyId: sel.apiKeyID, GroupId: sel.groupID, Account: snap, ForwardModel: routingModel,
		SessionHash: req.GetSessionHash(), ConfigVersion: version, CredentialParent: parentSnap,
		ChannelMapped: mapping.Mapped, ChannelMappedModel: mapping.MappedModel, ChannelId: mapping.ChannelID, BillingModelSource: mapping.BillingModelSource,
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	s.addSelection(sel)
	return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Selection{Selection: selection}}, nil
}
