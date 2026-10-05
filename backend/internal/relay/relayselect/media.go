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

// MediaEligibility 检查 Grok 账号有没有媒体生成资格（billing_unobserved 时探测一次；service.GrokQuotaService.ProbeMediaEligibility）。
type MediaEligibility interface {
	ProbeMediaEligibility(ctx context.Context, accountID int64) (bool, string, error)
}

// selectMedia 按本地 OpenAIGatewayHandler.handleGrokMedia（Grok 图片 / 视频、Seedance 任务）的顺序做选号：中间件复查 →
// 用户并发槽 → 计费资格 → 任务查询按任务绑定的账号（没有绑定回 404）/ 生成类按媒体能力选号 → 生成资格检查（没有资格的账号
// 排除、按换号次数重选）→ 准入。不装利润门（显式豁免）、不固定计价时间、不按上游 token 成本比较。换号状态在从节点。
//
// 与单机的差别：生成资格不满足时的换号次数按这一次选号调用计（单机与上游失败换号共用一个计数）；组合平台分组交给主节点。
func (s *selector) selectMedia(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	endpoint := service.GrokMediaEndpoint(req.GetMediaEndpoint())
	if !knownMediaEndpoint(endpoint) {
		return unsupported(), nil
	}
	platform := service.PlatformGrok
	if endpoint.IsSeedance() {
		platform = service.PlatformOpenAI
	}
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), modelCandidates(req),
		autoGroupChoice{pinned: req.GetAutoGroupId()}, platform)
	if err != nil || rej != nil {
		return rej, err
	}
	apiKey := adm.APIKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	ctx = middleware.RelayRequestContext(ctx, adm)
	subscription := adm.Billing.Subscription
	userID := apiKey.User.ID
	taskID := strings.TrimSpace(req.GetTaskId())
	routingModel := req.GetModel()
	requestModel := req.GetMediaRequestModel()
	quotaReq := service.QuotaRequest{User: apiKey.User, APIKey: apiKey, Group: apiKey.Group, Subscription: subscription, Platform: service.QuotaPlatform(ctx, apiKey)}

	record, first, err := s.requestFor(nodeID, req.GetRequestId())
	if err != nil {
		return nil, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if !first && (record.userID != userID || record.apiKeyID != apiKey.ID) {
		return nil, errors.New("relay request id reused by a different API key")
	}
	selected := false
	defer func() {
		if !selected {
			s.dropRequest(record)
		}
	}()
	if first {
		// 媒体计费不在 token 利润门范围内：显式豁免，不固定计价时间（入账时按当时的价）。
		suppress := func(ctx context.Context, _ *int64) (context.Context, time.Time) {
			return service.WithOpenAIProfitControlSuppressed(ctx), time.Time{}
		}
		if rej := s.startRequest(ctx, record, req, adm, quotaReq, false, false, suppress); rej != nil {
			return rej, nil
		}
		record.groupID = groupIDOf(apiKey)
	}

	sessionHash := req.GetSessionHash()
	boundAccountID := int64(0)
	if endpoint.IsVideoLookupRequest() {
		if taskID == "" {
			return gatewayRejection(handler.OpenAIGatewayRejection{Status: http.StatusBadRequest, ErrType: "invalid_request_error", Message: "request_id is required"}), nil
		}
		sessionHash = service.GrokMediaVideoRequestSessionHash(taskID, userID, apiKey.ID)
		boundAccountID, err = s.deps.Gateway.ResolveGrokMediaVideoRequestAccount(ctx, apiKey.GroupID, taskID, userID, apiKey.ID)
		if err != nil || boundAccountID <= 0 {
			return gatewayRejection(handler.GrokMediaVideoNotFoundRejection()), nil
		}
	}

	attemptCtx, cancel := context.WithCancel(record.pricingCtx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	for _, id := range req.GetExcludedAccountIds() {
		record.excluded[id] = struct{}{}
	}
	nodeFailed := len(req.GetExcludedAccountIds()) > 0
	maxSwitches := 3
	if cfg := s.deps.Config; cfg != nil && cfg.Gateway.MaxAccountSwitches > 0 {
		maxSwitches = cfg.Gateway.MaxAccountSwitches
	}
	var eligibility func(context.Context, *service.Account) (bool, string, error)
	if endpoint.IsGenerationRequest() && !endpoint.IsSeedance() {
		eligibility = s.mediaEligibility
	}
	var capability service.OpenAIEndpointCapability
	switch {
	case endpoint.IsSeedance():
		capability = service.OpenAIEndpointCapabilitySeedance
	case endpoint.IsGenerationRequest():
		capability = service.OpenAIEndpointCapabilityGrokMediaGeneration
	}

	var outcome handler.OpenAISelectOutcome
	for switches := 0; ; switches++ {
		outcome = s.admitter.SelectAndAdmit(attemptCtx, handler.OpenAISelectRequest{
			GroupID:             apiKey.GroupID,
			SessionHash:         sessionHash,
			ForwardModel:        routingModel,
			RequestPlatform:     platform,
			RequiredCapability:  capability,
			Transport:           service.OpenAIUpstreamTransportHTTPSSE,
			NoUpstreamTokenCost: true,
			BoundAccountID:      boundAccountID,
			Eligibility:         eligibility,
			Excluded:            record.excluded,
		}, &record.state, zap.NewNop())
		if outcome.Kind != handler.OpenAISelectIneligible {
			break
		}
		record.mediaIneligible = true
		if switches >= maxSwitches {
			return gatewayRejection(handler.GrokMediaEligibilityExhaustedRejection()), nil
		}
	}
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
		r, ok := handler.GrokMediaSelectFailureRejection(ctx, s.deps.Gateway, apiKey, handler.GrokMediaSelectFailure{
			Endpoint: endpoint, RequestModel: requestModel, RoutingModel: routingModel, Platform: platform,
			Bound: boundAccountID > 0, NoneExcluded: len(record.excluded) == 0,
			EligibilityOnly: record.mediaIneligible && !nodeFailed, SelectErr: outcome.Err,
		})
		if !ok {
			// 之前换过号：从节点按最近一次上游错误写。
			return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
				Format: relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED,
			}}}, nil
		}
		return gatewayRejection(r), nil
	default:
		return gatewayRejection(handler.OpenAISelectOutcomeRejection(outcome)), nil
	}

	if !openAICompatAccountServed(platform, outcome.Account) {
		// 从节点还接不了这种账号（Grok OAuth 的凭据刷新与失败处理在主节点）：放掉槽位，交给主节点转发。
		if outcome.Release != nil {
			outcome.Release()
		}
		return unsupported(), nil
	}

	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: outcome.Account, release: outcome.Release,
		createdAt: s.now(), quota: quotaReq, groupID: groupIDOf(apiKey), userID: userID, apiKeyID: apiKey.ID,
		apiKey: apiKey, channelGroupID: groupIDOf(apiKey), mediaChannelFields: true,
	}
	if endpoint.IsVideoLookupRequest() {
		// 完成时入账的模型取自创建时的快照（或状态里的）：都算凭证允许的范围。
		sel.extraModels = s.videoTaskModels(ctx, taskID, userID, apiKey.ID)
	}
	resp, brej, err := s.buildSelection(ctx, nodeID, req, sel, outcome, routingModel, requestModel, service.ChannelMappingResult{}, subscription, false)
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

// knownMediaEndpoint 报告入口名是不是媒体入口（从节点报的名字不认就交给主节点转发）。
func knownMediaEndpoint(e service.GrokMediaEndpoint) bool {
	switch e {
	case service.GrokMediaEndpointImagesGenerations, service.GrokMediaEndpointImagesEdits,
		service.GrokMediaEndpointVideosGenerations, service.GrokMediaEndpointVideosEdits, service.GrokMediaEndpointVideosExtensions,
		service.GrokMediaEndpointVideoStatus, service.GrokMediaEndpointVideoContent,
		service.SeedanceEndpointCreate, service.SeedanceEndpointStatus, service.SeedanceEndpointDelete:
		return true
	}
	return false
}

// mediaEligibility 是 handleGrokMedia 的 ensureGrokMediaAccountEligibility：账号没有媒体生成资格；billing_unobserved 时
// 探测一次，没有探测器时按不满足。
func (s *selector) mediaEligibility(ctx context.Context, account *service.Account) (bool, string, error) {
	if account == nil {
		return false, "missing_account", errors.New("grok media account is required")
	}
	eligible, reason := account.GrokMediaGenerationEligibility()
	if eligible || reason != "billing_unobserved" {
		return eligible, reason, nil
	}
	if s.deps.MediaEligibility == nil {
		return false, "billing_probe_unavailable", errors.New("grok media eligibility probe is not configured")
	}
	return s.deps.MediaEligibility.ProbeMediaEligibility(ctx, account.ID)
}

// videoTaskModels 是任务创建时的待计费快照里的模型（凭证允许范围的补充）。
func (s *selector) videoTaskModels(ctx context.Context, taskID string, userID, apiKeyID int64) []string {
	models := []string{"grok-imagine-video"}
	pending, err := s.deps.Gateway.LoadGrokVideoPendingBilling(ctx, taskID, userID, apiKeyID)
	if err != nil || pending == nil {
		return models
	}
	return append(models, pending.Model, pending.BillingModel, pending.UpstreamModel, pending.OriginalModel)
}
