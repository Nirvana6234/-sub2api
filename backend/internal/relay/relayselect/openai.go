package relayselect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/relay/keycodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// Select 选号。目前接入 OpenAI 分组的 Responses、Chat Completions、Messages + API Key，其余返回"暂不支持"，
// 由从节点交给主节点转发。
func (s *selector) Select(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	ws := false
	switch req.GetEndpoint() {
	case relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_RESPONSES, relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_CHAT,
		relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_MESSAGES, relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_EMBEDDINGS,
		relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_IMAGES:
	case relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_RESPONSES_WS:
		ws = true
	case relayv1.SelectEndpoint_SELECT_ENDPOINT_ANTHROPIC_MESSAGES, relayv1.SelectEndpoint_SELECT_ENDPOINT_ANTHROPIC_COUNT_TOKENS,
		relayv1.SelectEndpoint_SELECT_ENDPOINT_GEMINI_NATIVE, relayv1.SelectEndpoint_SELECT_ENDPOINT_GATEWAY_RESPONSES,
		relayv1.SelectEndpoint_SELECT_ENDPOINT_GATEWAY_CHAT:
	default:
		return unsupported(), nil
	}
	if req.GetApiKey() == "" {
		return unsupported(), nil
	}
	var resp *relayv1.SelectResponse
	var err error
	switch {
	case ws:
		resp, err = s.selectOpenAIWS(ctx, nodeID, req)
	case req.GetEndpoint() == relayv1.SelectEndpoint_SELECT_ENDPOINT_ANTHROPIC_MESSAGES || isGatewayCompatEndpoint(req.GetEndpoint()):
		resp, err = s.selectAnthropic(ctx, nodeID, req)
	case req.GetEndpoint() == relayv1.SelectEndpoint_SELECT_ENDPOINT_ANTHROPIC_COUNT_TOKENS:
		resp, err = s.selectAnthropicCountTokens(ctx, nodeID, req)
	case req.GetEndpoint() == relayv1.SelectEndpoint_SELECT_ENDPOINT_GEMINI_NATIVE:
		resp, err = s.selectGeminiNative(ctx, nodeID, req)
	default:
		resp, err = s.selectOpenAI(ctx, nodeID, req)
	}
	if err == nil && ctx.Err() != nil && resp.GetSelection() == nil {
		// 调用已取消时的拒绝多半是取消造成的（查 Key 失败等）；按错误返回，幂等缓存不会记住它，
		// 从节点超时重发时重新判断。
		return nil, ctx.Err()
	}
	if !ws && resp.GetRejection() != nil && !requestContinues(resp.GetRejection()) {
		// 拒绝了这次请求就到此为止（查到请求之后的拒绝已经放过了；这里补上之前就拒的）。
		s.endRequest(nodeID, req.GetRequestId())
	}
	return resp, err
}

// requestContinues 报告这个拒绝之后从节点是否照本地接着这次请求选号（自动分组换组、Anthropic 的利润否决与
// 选号耗尽后的重选）：这时请求记录（用户槽、计价时间、粘性会话起点）留着，由之后的选号或请求结束消息收尾。
func requestContinues(r *relayv1.SelectRejection) bool {
	switch {
	case r.GetAutoGroupFailover():
		return true
	case r.GetFormat() == relayv1.RejectionFormat_REJECTION_FORMAT_PROFIT_VETOED:
		return true
	case r.GetFormat() == relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED && r.GetAnthropicMessages():
		return true
	}
	return false
}

// selectOpenAI 按本地处理函数的顺序做 OpenAI 的选号：
//   - Responses（handler.OpenAIGatewayHandler.Responses）：中间件链的检查 → previous_response_id 检查 →
//     分组是否允许生图 → 渠道映射 → 用户并发槽 → 计费资格 → cyber 会话屏蔽 → 计价上下文 → 选号与准入；
//   - Chat Completions（ChatCompletions）：中间件链的检查 → cyber 会话屏蔽 → 渠道映射 → 用户并发槽 →
//     计费资格 → 计价上下文 → 选号与准入（没有续链和生图，要求账号支持 Chat Completions）；
//   - Messages（OpenAIGatewayHandler.Messages）：中间件链的检查 → 分组是否允许 /v1/messages 派发 → 渠道映射 →
//     用户并发槽 → 计费资格 → cyber 会话屏蔽 → 计价上下文 → 选号与准入（要求账号支持 Chat Completions，
//     按分组的派发映射模型选号；计费和选不出账号的错误按 Anthropic 格式）。
//
// 安全审计在这之前由从节点经 SecurityAudit 做完（设计 3.4）。
func (s *selector) selectOpenAI(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	chat := req.GetEndpoint() == relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_CHAT
	messages := req.GetEndpoint() == relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_MESSAGES
	embeddings := req.GetEndpoint() == relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_EMBEDDINGS
	images := req.GetEndpoint() == relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_IMAGES
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), modelCandidates(req),
		autoGroupChoice{pinned: req.GetAutoGroupId()})
	if err != nil || rej != nil {
		return rej, err
	}
	apiKey := adm.APIKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	// 组合平台分组：按改写前的公开模型选目标（本地 compositeTarget 中间件），与 ResolveRoute 同一个输入。
	composite, err := s.resolveComposite(ctx, apiKey, req.GetRouteModel(), req.GetPath())
	if err != nil {
		return nil, err
	}
	if !compositeServedByNode(apiKey, composite) {
		return unsupported(), nil
	}
	ctx = service.WithCompositeRouteDecision(ctx, composite)
	// 请求开头的检查（/v1/messages 派发、previous_response_id 归属、生图）本地只在开头按当时的分组做一次，
	// 自动分组中途换组后不重做：这里同样按请求开始时的分组。
	startGroup := s.requestStartGroup(ctx, apiKey, req.GetAutoGroupStartId())
	if messages && !startGroup.AllowMessagesDispatch {
		// 本地在读请求体之前就查（分组平台只会是 OpenAI：其余平台在准入时已回"暂不支持"）。
		return gatewayRejection(handler.OpenAIMessagesDispatchDeniedRejection()), nil
	}
	ctx = middleware.RelayRequestContext(ctx, adm)
	// Codex 审查子代理跟随父会话的账号（本地由处理函数放进请求 ctx）：请求开始时固定进计价上下文，之后每次选号都带着。
	ctx = service.WithOpenAIGuardianParentSessionHashes(ctx, req.GetGuardianParentSessionHash(), req.GetGuardianParentLegacySessionHash())
	subscription := adm.Billing.Subscription
	userID := apiKey.User.ID
	groupID := apiKey.Group.ID
	reqModel := req.GetModel()
	requestPlatform := service.PlatformOpenAI
	log := zap.NewNop()

	// Chat 没有续链、生图和 compact：从节点报了也不认。
	previousResponseID, imageIntent := strings.TrimSpace(req.GetPreviousResponseId()), req.GetImageIntent()
	legacyCompact, nativeV2 := req.GetLegacyCompact(), req.GetNativeCompactionV2()
	capability := handler.OpenAIResponsesRequiredCapability(imageIntent, nativeV2 || legacyCompact, requestPlatform)
	if chat || messages || embeddings || images {
		previousResponseID, imageIntent, legacyCompact = "", false, false
		capability = service.OpenAIEndpointCapabilityChatCompletions
	}
	transport := service.OpenAIUpstreamTransport("")
	if embeddings {
		// Embeddings 只走 HTTP/SSE，按 Embeddings 能力选，没有会话、没有 cyber 屏蔽。
		capability, transport = service.OpenAIEndpointCapabilityEmbeddings, service.OpenAIUpstreamTransportHTTPSSE
	}
	if chat {
		// Chat 在占用户槽之前查 cyber 屏蔽（本地 ChatCompletions 的顺序）。
		if rej := s.cyberRejection(ctx, nodeID, req, apiKey); rej != nil {
			return rej, nil
		}
	}
	if previousResponseID != "" {
		if service.ClassifyOpenAIPreviousResponseIDKind(previousResponseID) == service.OpenAIPreviousResponseIDKindMessageID {
			return gatewayRejection(handler.OpenAIGatewayRejection{Status: http.StatusBadRequest, ErrType: "invalid_request_error", Message: "previous_response_id must be a response.id (resp_*), not a message id"}), nil
		}
		owned, ownershipErr := s.deps.Gateway.ValidateOpenAIHTTPResponseOwner(ctx, startGroup.ID, previousResponseID, userID, apiKey.ID)
		if ownershipErr != nil {
			slog.Warn("relay: previous response owner lookup failed", "error", ownershipErr)
		}
		if !owned {
			return gatewayRejection(handler.OpenAIGatewayRejection{Status: http.StatusBadRequest, ErrType: "invalid_request_error", Message: "previous_response_id is not available for this user"}), nil
		}
	}
	if imageIntent && !service.GroupAllowsImageGeneration(startGroup) {
		return gatewayRejection(handler.OpenAIGatewayRejection{Status: http.StatusForbidden, ErrType: "permission_error", Message: service.ImageGenerationPermissionMessage()}), nil
	}
	channelMapping, _ := s.deps.Gateway.ResolveChannelMappingAndRestrict(ctx, apiKey.GroupID, reqModel)
	forwardModel := handler.OpenAIChannelForwardModel(channelMapping, reqModel)
	if images {
		// 图片入口按路由模型选号，渠道映射只在转发时用（本地 Images）。
		forwardModel = reqModel
	}
	if messages {
		// Messages 按分组的派发映射（或规范化后的请求模型）选号，渠道映射只改请求体。
		forwardModel = handler.OpenAIMessagesRoutingModel(apiKey, reqModel)
	}
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
	// 没有成功选出账号就结束这次请求（放掉用户槽）：与本地一致，被拒、出错、调用被取消时请求都到此为止。
	// 从节点超时重发同一次选号时从头来过。
	// keep：这次没选出账号，但从节点照本地接着这次请求（自动分组换组后再选），请求记录（用户槽、计价时间）留着，
	// 由之后的选号或请求结束消息收尾。
	selected, keep := false, false
	defer func() {
		if !selected && !keep {
			s.dropRequest(record)
		}
	}()
	if first {
		var pricing func(context.Context, *int64) (context.Context, time.Time)
		if images {
			// 图片入口不装利润门、不固定计价时间（入账时按当时的价）。
			pricing = func(ctx context.Context, _ *int64) (context.Context, time.Time) { return ctx, time.Time{} }
		}
		if rej := s.startRequest(ctx, record, req, adm, quotaReq, !chat && !embeddings && !images, messages, pricing); rej != nil {
			return rej, nil
		}
		record.groupID = groupID
	} else if record.groupID != groupID {
		// 自动分组 Key 在这次请求里换了分组（从节点经 SwitchAutoGroup 换的，本地 tryOpenAIAutoGroupFailover）：与本地
		// 换组之后一样，换号记录和选号状态从头来，计价上下文按新分组。
		record.groupID = groupID
		record.excluded = map[int64]struct{}{}
		record.state = handler.OpenAISelectState{}
		record.pricingCtx, record.pricingAt = s.deps.Gateway.WithOpenAIRequestPricingContext(context.WithoutCancel(ctx), apiKey.GroupID)
	}

	// 每次选号：计价上下文沿用请求开始时固定的，取消跟着这次调用。
	attemptCtx, cancel := context.WithCancel(record.pricingCtx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	var imagesCapability service.OpenAIImagesCapability
	if images {
		attemptCtx = service.WithOpenAIImagesEndpoint(service.WithOpenAIImageGenerationIntent(attemptCtx))
		imagesCapability = service.OpenAIImagesCapability(req.GetRequiredCapability())
	}

	for _, id := range req.GetExcludedAccountIds() {
		record.excluded[id] = struct{}{}
	}
	lastFailover := record.state.LastFailoverErr
	outcome := s.admitter.SelectAndAdmit(attemptCtx, handler.OpenAISelectRequest{
		GroupID:            apiKey.GroupID,
		PreviousResponseID: previousResponseID,
		SessionHash:        req.GetSessionHash(),
		ForwardModel:       forwardModel,
		RequestPlatform:    requestPlatform,
		RequiredCapability: capability,
		Transport:          transport,
		ImagesCapability:   imagesCapability,
		RequireCompact:     legacyCompact,
		ImageIntent:        imageIntent,
		Excluded:           record.excluded,
	}, &record.state, log)

	if ctx.Err() != nil {
		// 从节点已经不等了（本地：failoverClientGone 先于一切错误处理）。
		if outcome.Kind == handler.OpenAISelected && outcome.Release != nil {
			outcome.Release()
		}
		return nil, ctx.Err()
	}
	switch outcome.Kind {
	case handler.OpenAISelected:
	case handler.OpenAISelectAborted:
		return nil, context.Canceled
	case handler.OpenAISelectFailed:
		if len(record.excluded) > 0 {
			// 换号用完（本地：handleFailoverExhausted）。续链不支持是这次选号里刚记下的才算最后的错误。
			// 自动分组 Key：本地这时换到下一个候选分组再试（从节点问 SwitchAutoGroup）。
			continuation := record.state.LastFailoverErr != nil && record.state.LastFailoverErr != lastFailover
			keep = apiKey.AutoGroup
			return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
				Format: relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED, ContinuationUnsupported: continuation,
				AutoGroupFailover: apiKey.AutoGroup,
			}}}, nil
		}
		var rej *relayv1.SelectResponse
		if embeddings {
			rej = gatewayRejection(handler.OpenAIEmbeddingsFirstSelectFailureRejection(ctx, s.deps.Gateway, apiKey, reqModel, outcome.Err))
		} else if images {
			rej = gatewayRejection(handler.OpenAIImagesNoAccountRejection(ctx, s.deps.Gateway, apiKey, reqModel, outcome.Err))
		} else if messages {
			rej = gatewayRejection(handler.OpenAIMessagesNoAccountRejection(ctx, s.deps.Gateway, apiKey, forwardModel, reqModel, requestPlatform, outcome.Err))
		} else {
			rej = gatewayRejection(handler.OpenAIFirstSelectFailureRejection(ctx, s.deps.Gateway, apiKey, reqModel, requestPlatform, legacyCompact, outcome.Err))
		}
		// 第一次就选不出账号（没有可用账号）：本地同样先换组再试。
		rej.GetRejection().AutoGroupFailover = apiKey.AutoGroup && handler.IsAutoGroupSelectionFailoverError(outcome.Err)
		keep = rej.GetRejection().GetAutoGroupFailover()
		return rej, nil
	case handler.OpenAISelectNone:
		if messages {
			return gatewayRejection(handler.OpenAIMessagesNoAccountRejection(ctx, s.deps.Gateway, apiKey, forwardModel, reqModel, requestPlatform, nil)), nil
		}
		return gatewayRejection(handler.OpenAINoAccountRejection(ctx, s.deps.Gateway, apiKey, reqModel, requestPlatform, nil)), nil
	default:
		return gatewayRejection(handler.OpenAISelectOutcomeRejection(outcome)), nil
	}

	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: outcome.Account, release: outcome.Release,
		createdAt: s.now(), quota: quotaReq, groupID: groupID, userID: userID, apiKeyID: apiKey.ID,
		apiKey: apiKey, cyber: cyberLookup(req.GetCyber()),
	}
	resp, rej, err := s.buildSelection(ctx, nodeID, req, sel, outcome, forwardModel, reqModel, channelMapping, subscription, messages)
	if err == nil && rej == nil && ctx.Err() != nil {
		// 额度已经给了但从节点不等了：原样收回（节点不知道这笔，不收回就一直锁到租约到期）。
		s.ungrant(sel.userID, nodeID, resp.GetSelection().GetGrants())
		err = ctx.Err()
	}
	if err != nil || rej != nil {
		// 这次选号没人会用：立刻放槽；幂等缓存只存成功的结果。
		if outcome.Release != nil {
			outcome.Release()
		}
		if rej != nil {
			return rej, nil
		}
		return nil, err
	}
	s.addSelection(sel)
	selected = true
	return resp, nil
}

// Admit 准入（设计 3.2）：中间件链的检查，不含分组模型白名单（从节点用快照里的分组在本地跑那个中间件，
// 保持与单机相同的检查顺序；选号时这里再按请求里的模型名查一遍）。
func (s *selector) Admit(ctx context.Context, nodeID int64, req *relayv1.AdmitRequest) (*relayv1.AdmitResponse, error) {
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), nil, autoGroupChoice{coldStart: true}, relayServedFor(req.GetPath())...)
	if err != nil {
		return nil, err
	}
	if rej != nil {
		return &relayv1.AdmitResponse{Result: &relayv1.AdmitResponse_Rejection{Rejection: rej.GetRejection()}}, nil
	}
	s.admitted.note(nodeID, adm.APIKey.User.ID, s.now())
	key, err := keycodec.EncodeAPIKey(adm.APIKey)
	if err != nil {
		return nil, err
	}
	sub, err := keycodec.EncodeSubscription(adm.Billing.Subscription)
	if err != nil {
		return nil, err
	}
	return &relayv1.AdmitResponse{Result: &relayv1.AdmitResponse_Admission{Admission: &relayv1.Admission{ApiKey: key, Subscription: sub}}}, nil
}

// requestStartGroup 是这次请求开始时的分组：自动分组 Key 中途换过组时是从节点带来的开始分组（核对是这把 Key 的
// 候选；已经不是时按现在的分组），其余就是 Key 现在的分组。
func (s *selector) requestStartGroup(ctx context.Context, apiKey *service.APIKey, startGroupID int64) *service.Group {
	if !apiKey.AutoGroup || startGroupID == 0 || startGroupID == apiKey.Group.ID {
		return apiKey.Group
	}
	start, err := s.deps.APIKeys.AutoGroupCandidate(ctx, apiKey, startGroupID)
	if err != nil || start == nil || start.Group == nil {
		return apiKey.Group
	}
	return start.Group
}

// autoGroupChoice 是自动分组 Key 这次请求怎么定分组（本地 autoGroupModelRoutingMiddleware，设计 3.2）：
//   - pinned 非 0：从节点带来的、这次请求已定下的分组（按模型选定或切换后的），核对是这把 Key 的候选再用；
//   - 否则 model 非空：按模型选（与本地同一个选组器，选定的分组记在主节点）；
//   - 都没有：保留鉴权时的冷启动分组（本地没取到模型、WebSocket 升级请求时也是这样）。
//
// coldStart：准入（还不知道模型）时保留冷启动分组、不查平台，分组由从节点接着按模型问 ResolveRoute 定。
type autoGroupChoice struct {
	pinned    int64
	model     string
	coldStart bool
}

func (s *selector) resolveAutoGroup(choice autoGroupChoice) func(context.Context, *service.APIKey) (*service.APIKey, error) {
	return func(ctx context.Context, apiKey *service.APIKey) (*service.APIKey, error) {
		switch {
		case choice.coldStart:
			return apiKey, nil
		case choice.pinned != 0:
			return s.deps.APIKeys.AutoGroupCandidate(ctx, apiKey, choice.pinned)
		case choice.model != "":
			return s.deps.APIKeys.ResolveAutoGroupForModel(ctx, apiKey, choice.model)
		default:
			return apiKey, nil
		}
	}
}

// admitAPIKey 按本地网关中间件链复查 Key（EvaluateRelayAPIKeyAdmission），再挡掉还不能经从节点处理的分组。
// 被拒时返回拒绝回复。
// served 是这次允许的分组平台（不传时是 OpenAI 入口能接的 OpenAI、组合平台）；分组不在其中时回"暂不支持"。
func (s *selector) admitAPIKey(ctx context.Context, rawKey, clientIP, method, path string, models []string, auto autoGroupChoice, served ...string) (middleware.RelayAPIKeyAdmission, *relayv1.SelectResponse, error) {
	adm, raw, err := middleware.EvaluateRelayAPIKeyAdmission(ctx, middleware.RelayAPIKeyAdmissionInput{
		APIKeyAuthInput: middleware.APIKeyAuthInput{
			APIKeys: s.deps.APIKeys, Subscriptions: s.deps.Subscriptions, Config: s.deps.Config,
			ClientIP: clientIP, Method: method, Path: path,
		},
		RawKey:    rawKey,
		Settings:  s.deps.Settings,
		Models:    models,
		AutoGroup: s.resolveAutoGroup(auto),
		Google:    middleware.IsGoogleRelayPath(path),
	})
	if errors.Is(err, middleware.ErrRelayAdmissionUnsupported) {
		return adm, unsupported(), nil
	}
	if err != nil {
		return adm, nil, err
	}
	if raw != nil {
		return adm, rawRejection(raw), nil
	}
	if auto.coldStart && adm.APIKey.AutoGroup {
		return adm, nil, nil
	}
	if len(served) == 0 {
		served = openAIServedPlatforms
	}
	platform := noGroupPlatform
	if g := adm.APIKey.Group; g != nil {
		platform = g.Platform
	}
	if !slices.Contains(served, platform) {
		// 其他 OpenAI 兼容平台（Grok 等）还没接入；未分组 Key 只有 Anthropic 入口接（走 Anthropic 网关）。
		return adm, unsupported(), nil
	}
	return adm, nil, nil
}

// noGroupPlatform 代表没有分组的 Key（后台允许未分组 Key 调度时，本地走 Anthropic 网关按"未分组账号"选号）。
const noGroupPlatform = ""

var (
	// openAIServedPlatforms 是 OpenAI 入口经从节点能接的分组平台。
	openAIServedPlatforms = []string{service.PlatformOpenAI, service.PlatformComposite}
	// anthropicServedPlatforms 是 Anthropic Messages 入口经从节点能接的分组平台（组合平台分组只接选到 Anthropic 目标的）。
	// Gemini 平台的 Messages（本地 platform == gemini 分支）和 Antigravity 平台分组的 Messages 也走这个入口。
	anthropicServedPlatforms = []string{service.PlatformAnthropic, service.PlatformGemini, service.PlatformAntigravity, service.PlatformComposite, noGroupPlatform}
	// relayServedPlatforms 是准入、定走向时放行的分组平台（哪个入口接由从节点的路由按分组平台再分）。
	relayServedPlatforms = []string{service.PlatformOpenAI, service.PlatformComposite, service.PlatformAnthropic, service.PlatformGemini, service.PlatformAntigravity, noGroupPlatform}
	// geminiServedPlatforms 是 Gemini 原生入口（/v1beta）经从节点能接的分组平台（组合平台分组只接选到 Gemini 或没有匹配目标的）。
	geminiServedPlatforms = []string{service.PlatformGemini, service.PlatformComposite}
)

// relayServedFor 是准入、定走向时放行的分组平台：/antigravity/* 强制 Antigravity 平台（不看分组平台，见 antigravityRouteServed），
// Gemini 原生入口只放 Gemini（及组合平台），其余入口按 relayServedPlatforms。
func relayServedFor(path string) []string {
	switch {
	case isAntigravityRoute(path):
		return antigravityRouteServedPlatforms
	case middleware.IsGoogleRelayPath(path):
		return geminiServedPlatforms
	}
	return relayServedPlatforms
}

// antigravityRouteServedPlatforms 是 /antigravity/* 入口经从节点能接的分组平台：这些入口强制 Antigravity 平台（本地
// middleware.ForcePlatform），调度不看分组平台；只接 Antigravity、Anthropic、Gemini 分组和未分组 Key，其余分组（OpenAI、组合平台等）
// 交给主节点。
var antigravityRouteServedPlatforms = []string{service.PlatformAntigravity, service.PlatformAnthropic, service.PlatformGemini, noGroupPlatform}

// isAntigravityRoute 报告这是 /antigravity/* 入口（本地路由强制 Antigravity 平台，由主节点按路径定，不信从节点的说法）。
func isAntigravityRoute(path string) bool {
	return strings.HasPrefix(path, "/antigravity/")
}

// withForcedPlatform 给选号的 ctx 加上路由强制的平台（本地 middleware.ForcePlatform 放进请求 ctx 的同一个值）。
func withForcedPlatform(ctx context.Context, path string) context.Context {
	if isAntigravityRoute(path) {
		return context.WithValue(ctx, ctxkey.ForcePlatform, service.PlatformAntigravity)
	}
	return ctx
}

// servedAnthropicFor 是 Messages / count_tokens 入口放行的分组平台。
func servedAnthropicFor(path string) []string {
	if isAntigravityRoute(path) {
		return antigravityRouteServedPlatforms
	}
	return anthropicServedPlatforms
}

// servedGeminiFor 是 Gemini 原生入口放行的分组平台。
func servedGeminiFor(path string) []string {
	if isAntigravityRoute(path) {
		return antigravityRouteServedPlatforms
	}
	return geminiServedPlatforms
}

// startRequest 是一次请求的第一次选号时做的：用户并发槽、计费资格、（cyberAfterBilling 时）cyber 会话屏蔽、
// 计价上下文。被拒时返回拒绝（调用方放掉用户槽）。
// anthropicBilling：计费资格的拒绝按 Anthropic 格式写（Messages 入口）。pricing 定计价上下文（nil 时按 OpenAI 的）。
func (s *selector) startRequest(ctx context.Context, record *requestRecord, req *relayv1.SelectRequest, adm middleware.RelayAPIKeyAdmission, quotaReq service.QuotaRequest, cyberAfterBilling, anthropicBilling bool,
	pricing func(context.Context, *int64) (context.Context, time.Time),
) *relayv1.SelectResponse {
	apiKey := adm.APIKey
	record.userID, record.apiKeyID = apiKey.User.ID, apiKey.ID
	release, err := s.helper.AcquireUserSlotWithWaitNoGin(ctx, apiKey.User.ID, apiKey.ID, apiKey.User.Concurrency)
	if err != nil {
		if middleware.IsGoogleRelayPath(req.GetPath()) {
			// Gemini 原生入口的用户槽错误取自错误本身（本地 GeminiV1BetaModels）。
			return gatewayRejection(handler.GeminiUserSlotRejection(err))
		}
		return gatewayRejection(handler.OpenAIConcurrencyRejection(err, "user"))
	}
	record.userRelease = release

	if err := s.checkBilling(ctx, record.key.nodeID, req.GetHeldQuota(), apiKey, quotaReq.Subscription, quotaReq.Platform); err != nil {
		return gatewayRejection(billingRejectionFor(req, err, anthropicBilling))
	}
	if cyberAfterBilling {
		if rej := s.cyberRejection(ctx, record.key.nodeID, req, apiKey); rej != nil {
			return rej
		}
	}
	if pricing == nil {
		pricing = s.deps.Gateway.WithOpenAIRequestPricingContext
	}
	pricingCtx, pricingAt := pricing(context.WithoutCancel(ctx), apiKey.GroupID)
	record.pricingCtx, record.pricingAt = pricingCtx, pricingAt
	return nil
}

// checkBilling 是计费资格检查（本地 CheckBillingEligibility）。从节点手里还没用掉的余额算作这个请求方的
// （不信节点报的数：不超过主节点记着的、锁在这台上的）。
func (s *selector) checkBilling(ctx context.Context, nodeID int64, heldQuota []*relayv1.HeldQuota, apiKey *service.APIKey, subscription *service.UserSubscription, platform string) error {
	held := heldBalance(heldQuota)
	if held > 0 && s.env.Quotas != nil {
		locked, err := s.env.Quotas.NodeReservedBalance(ctx, apiKey.User.ID, nodeID)
		if err != nil {
			return service.ErrBillingServiceUnavailable.WithCause(err)
		}
		held = min(held, locked)
	}
	billingCtx := service.WithRelayRequesterHeldBalance(ctx, master.FromMicros(held))
	return s.deps.Billing.CheckBillingEligibility(billingCtx, apiKey.User, apiKey, apiKey.Group, subscription, platform)
}

// cyberRejection 查 cyber 会话屏蔽（从节点算好的键）；命中时记运维日志（与单机 writeCyberSessionBlocked 一样）
// 并返回拒绝，从节点按 cyber 屏蔽的写法写响应。
func (s *selector) cyberRejection(ctx context.Context, nodeID int64, req *relayv1.SelectRequest, apiKey *service.APIKey) *relayv1.SelectResponse {
	c := req.GetCyber()
	if c == nil {
		return nil
	}
	key := s.findCyberBlocked(ctx, cyberLookup(c))
	if key == "" {
		return nil
	}
	node := nodeID
	s.recordCyberBlocked(ctx, apiKey, handler.CyberSessionBlockedRequest{
		RequestID: req.GetHttpRequestId(), ClientRequestID: req.GetClientRequestId(), Model: req.GetModel(),
		RequestPath: req.GetPath(), Stream: req.GetStream(), InboundEndpoint: handler.NormalizeInboundEndpoint(req.GetPath()),
		UserAgent: req.GetUserAgent(), ClientIP: req.GetClientIp(), SessionBlockKey: key, NodeID: &node,
	})
	rej := gatewayRejection(handler.OpenAICyberSessionBlockedRejection())
	rej.GetRejection().CyberBlockKey = key
	return rej
}

// channelFeatures 是分组所属渠道的功能配置（JSON，选号结果带给从节点）；没有渠道时为空。
func (s *selector) channelFeatures(ctx context.Context, groupID int64) ([]byte, error) {
	if groupID == 0 {
		return nil, nil // 未分组 Key 没有渠道
	}
	features, err := s.deps.Gateway.ChannelFeaturesForGroup(ctx, groupID)
	if err != nil || features == nil {
		return nil, err
	}
	return json.Marshal(features)
}

// buildSelection 组装选号结果：额度、账号快照、扣费凭证。
func (s *selector) buildSelection(ctx context.Context, nodeID int64, req *relayv1.SelectRequest, sel *selectionRecord, outcome handler.OpenAISelectOutcome,
	forwardModel, reqModel string, mapping service.ChannelMappingResult, subscription *service.UserSubscription, anthropicBilling bool,
) (*relayv1.SelectResponse, *relayv1.SelectResponse, error) {
	snap, err := s.encodeAccount(ctx, nodeID, sel.account, s.nodeHas(nodeID))
	if err != nil {
		return nil, nil, err
	}
	var parentSnap *relayv1.AccountSnapshot
	if sel.account.IsShadow() {
		if parentSnap, err = s.encodeCredentialParent(ctx, nodeID, sel.account, s.nodeHas(nodeID)); err != nil {
			return nil, nil, err
		}
	}
	mode := relayv1.BillingMode_BILLING_MODE_BALANCE
	if sel.quota.Group != nil && sel.quota.Group.IsSubscriptionType() && subscription != nil {
		mode = relayv1.BillingMode_BILLING_MODE_SUBSCRIPTION
	}
	allowed := allowedBillingModels(reqModel, forwardModel, sel.account)
	// 用量行的"请求模型"与单机一样取客户端写的模型：组合平台分组是改写前的公开模型（clientRequestedModel）。
	requested := reqModel
	if public, ok := service.RequestedPublicModelFromContext(ctx); ok {
		requested = public
	}
	voucher, _, err := s.env.IssueVoucher(&relayv1.Voucher{
		NodeId: nodeID, SelectionId: sel.id, UserId: sel.userID, ApiKeyId: sel.apiKeyID, AccountId: sel.account.ID,
		GroupId: sel.groupID, BillingMode: mode, RequestedModel: requested, AllowedBillingModels: allowed,
		Quote:   &relayv1.Quote{},
		Context: selectionContext(outcome, sel, mapping, subscription),
	})
	if err != nil {
		return nil, nil, err
	}
	version, err := s.env.ConfigVersion(ctx, nodeID)
	if err != nil {
		return nil, nil, err
	}
	switches := 3
	if cfg := s.deps.Config; cfg != nil && cfg.Gateway.MaxAccountSwitches > 0 {
		switches = cfg.Gateway.MaxAccountSwitches
	}
	// 额度放在最后：之后不会再有失败的步骤，给出的额度不会因为回复做不出来而白锁着。
	grants, scopes, err := s.acquireQuota(ctx, nodeID, sel.quota, req.GetHeldQuota(), quotaNeed, false)
	if err != nil {
		var insufficient *master.QuotaInsufficientError
		if errors.As(err, &insufficient) {
			return nil, gatewayRejection(billingRejectionFor(req, quotaError(insufficient.Scope.Dimension), anthropicBilling)), nil
		}
		if errors.Is(err, service.ErrSubscriptionInvalid) || errors.Is(err, service.ErrBillingServiceUnavailable) {
			// 与本地计费检查在同样的情况下返回的一致（订阅已失效、计费数据取不到）。
			return nil, gatewayRejection(billingRejectionFor(req, err, anthropicBilling)), nil
		}
		return nil, nil, err
	}
	featureGroup := sel.groupID
	if sel.channelGroupID != 0 {
		featureGroup = sel.channelGroupID
	}
	features, err := s.channelFeatures(ctx, featureGroup)
	if err != nil {
		return nil, nil, err
	}
	return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Selection{Selection: &relayv1.Selection{
		ChannelFeatures: features,
		SelectionId:     sel.id, UserId: sel.userID, ApiKeyId: sel.apiKeyID, GroupId: sel.groupID, BillingMode: mode,
		Account: snap, ForwardModel: forwardModel, ChannelMapped: mapping.Mapped, ChannelMappedModel: mapping.MappedModel,
		ChannelId: mapping.ChannelID, BillingModelSource: mapping.BillingModelSource, SessionHash: outcome.SessionHash,
		Voucher: voucher, QuotaScopes: scopes, QuotaNeed: quotaNeed, Grants: grants,
		MaxAccountSwitches: int32(switches), ConfigVersion: version, PricingAtUnixMs: sel.request.pricingAt.UnixMilli(),
		CredentialParent: parentSnap,
	}}}, nil, nil
}

func modelCandidates(req *relayv1.SelectRequest) []string {
	if len(req.GetModelCandidates()) > 0 {
		return req.GetModelCandidates()
	}
	if req.GetModel() == "" {
		return nil
	}
	return []string{req.GetModel()}
}

func unsupported() *relayv1.SelectResponse {
	return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
		Format: relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED, Status: http.StatusServiceUnavailable,
		Code: "relay_unsupported", Message: "this request is not served by relay nodes yet",
	}}}
}

func rawRejection(r *middleware.CapturedRejection) *relayv1.SelectResponse {
	headers := make(map[string]string, len(r.Header))
	for k := range r.Header {
		headers[k] = r.Header.Get(k)
	}
	return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
		Format: relayv1.RejectionFormat_REJECTION_FORMAT_RAW, Status: int32(r.Status), Headers: headers, Body: r.Body,
		IngressRejectReason: r.IngressReason, OpsBusinessLimitedReason: r.OpsReason,
	}}}
}

func gatewayRejection(r handler.OpenAIGatewayRejection) *relayv1.SelectResponse {
	return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
		Format: relayv1.RejectionFormat_REJECTION_FORMAT_GATEWAY, Status: int32(r.Status), ErrorType: r.ErrType, Code: r.Code,
		Message: r.Message, RetryAfterSeconds: int32(r.RetryAfter), RoutingCapacityLimited: r.RoutingCapacityLimited,
		OpsBusinessLimitedReason: r.OpsBusinessLimitedReason, AnthropicFormat: r.Anthropic, OpenaiCompatFormat: r.Compat,
	}}}
}

// billingRejection 是计费资格的拒绝；Messages 入口按 Anthropic 格式写（与本地一致）。
func billingRejection(err error, anthropic bool) handler.OpenAIGatewayRejection {
	r := handler.OpenAIBillingRejection(err)
	r.Anthropic = anthropic
	return r
}

// billingRejectionFor 同 billingRejection，/v1/responses、/v1/chat/completions 入口按处理函数自己的格式写。
func billingRejectionFor(req *relayv1.SelectRequest, err error, anthropic bool) handler.OpenAIGatewayRejection {
	r := billingRejection(err, anthropic)
	r.Compat = isGatewayCompatEndpoint(req.GetEndpoint())
	return r
}

// selectionContext 是凭证里的选号上下文：入账要用、由这次选号定下的值（字段清单见 fields_guard_test.go）。
func selectionContext(outcome handler.OpenAISelectOutcome, sel *selectionRecord, mapping service.ChannelMappingResult, subscription *service.UserSubscription) *relayv1.SelectionContext {
	c := &relayv1.SelectionContext{
		PricingAtUnixMs:    sel.request.pricingAt.UnixMilli(),
		QuotaPlatform:      sel.quota.Platform,
		ChannelId:          mapping.ChannelID,
		ChannelMappedModel: mapping.MappedModel,
		BillingModelSource: mapping.BillingModelSource,
		ChannelMapped:      mapping.Mapped,
	}
	if subscription != nil {
		c.SubscriptionId = subscription.ID
	}
	if trace, ok := service.FallbackPoolTraceFromContext(outcome.Ctx); ok {
		c.FallbackSourceGroupId, c.FallbackSourceGroupName = trace.SourceGroupID, trace.SourceGroupName
		c.FallbackTargetGroupId, c.FallbackTargetGroupName = trace.TargetGroupID, trace.TargetGroupName
	}
	if a := outcome.Account; a != nil {
		c.ContributionRouteSource, c.ContributionRoomId = a.ContributionRouteSource, a.ContributionRoomID
		if a.ContributionRateMultiplierOverride != nil {
			c.HasContributionRateMultiplierOverride, c.ContributionRateMultiplierOverride = true, *a.ContributionRateMultiplierOverride
		}
	}
	return c
}

// allowedBillingModels 是凭证允许的计费模型（设计 5.3）：请求模型、渠道映射后的模型、账号映射后发给上游的模型。
// 转发中还可能出现别的合法模型（compact 映射、Codex 归一、上游拒绝后的回退模型），入账时对不上只报警
// 并记为待复核，照上报的计费，不改价（改价会让正常请求和单机算得不一样）。
func allowedBillingModels(reqModel, forwardModel string, account *service.Account) []string {
	out := []string{}
	seen := map[string]bool{}
	candidates := []string{reqModel, forwardModel, account.GetMappedModel(forwardModel)}
	if account.Platform == service.PlatformAntigravity {
		// Antigravity 转发时按账号映射表另选目标（裸 Gemini 名按 thinking 档位换成映射表里的 -low/-high 变体、Claude 名换成
		// 思考版等），上报的模型是映射表里的名字：映射表里出现的名字都算允许的（范围仍限于这个账号的映射）。
		var extra []string
		for from, to := range account.GetModelMapping() {
			extra = append(extra, from, to)
		}
		slices.Sort(extra)
		candidates = append(candidates, extra...)
	}
	for _, m := range candidates {
		if m = strings.TrimSpace(m); m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}
