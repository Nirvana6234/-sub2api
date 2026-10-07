package relayselect

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// geminiDefaultMaxAccountSwitches 是 Gemini 原生入口的换号上限默认值（与 NewGatewayHandler 一致）。
const geminiDefaultMaxAccountSwitches = 3

// geminiDigestSession 是 Gemini 内容摘要会话在这次请求里的状态（本地 GeminiV1BetaModels 的 useDigestFallback 那一段）：
// 请求开始时查一次（会话没有绑定账号时按摘要链匹配），转发成功时保存。
type geminiDigestSession struct {
	// fallback：请求开始时粘性会话没有绑定账号（本地 useDigestFallback）。
	fallback  bool
	chain     string
	prefix    string
	uuid      string
	matchedAs string
}

// selectGeminiNative 按本地 GatewayHandler.GeminiV1BetaModels 主循环的顺序做一轮选号：
// 中间件链的检查 → 渠道映射 → 用户并发槽 → 计费资格 → 计价上下文 → 会话（CLI / 通用会话哈希、粘性会话绑定、内容摘要会话匹配，
// 请求开始时做一次）→ 选号与准入（handler.AnthropicAccountAdmitter，一轮，Gemini 不使用会话数限制）。
//
// 与 Messages 一样，换号状态（已失败的账号、利润否决次数、选号耗尽后的退避）在从节点的处理函数里。错误按 Google 格式由从节点写，
// 这里只给状态码、文案、Retry-After 和运维标记。
func (s *selector) selectGeminiNative(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	gw := s.deps.AnthropicGateway
	if gw == nil || s.deps.Gemini == nil {
		return unsupported(), nil
	}
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), modelCandidates(req),
		autoGroupChoice{pinned: req.GetAutoGroupId()}, servedGeminiFor(req.GetPath())...)
	if err != nil || rej != nil {
		return rej, err
	}
	apiKey := adm.APIKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	ctx = middleware.RelayRequestContext(ctx, adm)
	// /antigravity/v1beta：强制 Antigravity 平台（由路径定）。
	ctx = withForcedPlatform(ctx, req.GetPath())
	subscription := adm.Billing.Subscription
	// 组合平台分组：按改写前的公开模型选目标（本地 compositeGeminiTarget 中间件）；没有匹配的目标时按 Gemini，选到别的平台的交给主节点。
	composite, err := s.resolveComposite(ctx, apiKey, req.GetRouteModel(), req.GetPath())
	if err != nil {
		return nil, err
	}
	if !isAntigravityRoute(req.GetPath()) && apiKey.Group != nil && apiKey.Group.Platform == service.PlatformComposite {
		if composite.Matched && composite.TargetPlatform != service.PlatformGemini {
			return unsupported(), nil
		}
		ctx = service.WithCompositeRouteDecision(ctx, composite)
		if _, resolved := service.ResolvedTargetPlatformFromContext(ctx); !resolved {
			ctx = service.WithResolvedTargetPlatform(ctx, service.PlatformGemini)
		}
	}
	userID, groupID := apiKey.User.ID, groupIDOf(apiKey)
	reqModel := req.GetModel()
	log := zap.NewNop()

	// 渠道映射在选号之前（本地先映射，再用映射后的模型选号、转发）。
	channelMapping, _ := gw.ResolveChannelMappingAndRestrict(ctx, apiKey.GroupID, reqModel)
	if err := s.deps.Gateway.CheckBillablePricing(ctx, apiKey, reqModel, channelMapping.MappedModel); err != nil {
		slog.Warn("relay: model pricing is not available", "model", reqModel, "error", err)
		return gatewayRejection(handler.GeminiPricingUnavailableRejection()), nil
	}
	selectModel := reqModel
	if channelMapping.Mapped {
		selectModel = channelMapping.MappedModel
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
	// 没有成功选出账号就结束这次请求（放掉用户槽）；keep：利润否决、选号耗尽后从节点照本地接着选（同一请求）。
	selected, keep := false, false
	defer func() {
		if !selected && !keep {
			s.dropRequest(record)
		}
	}()
	if first {
		pricing := func(ctx context.Context, _ *int64) (context.Context, time.Time) {
			return service.WithGatewayTokenRequestPricing(ctx)
		}
		if rej := s.startRequest(ctx, record, req, adm, quotaReq, false, false, pricing); rej != nil {
			return rej, nil
		}
		record.groupID = groupID
		s.geminiStartSession(ctx, record, apiKey, req, selectModel)
		// 单账号分组提前设 SingleAccountRetry（Antigravity 单账号分组收到 503 时不设模型限流）：本地在请求开始时查一次。
		record.singleAccountRetry = gw.IsSingleAntigravityAccountGroup(ctx, apiKey.GroupID)
	}

	attemptCtx, cancel := context.WithCancel(record.pricingCtx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if record.stickyPrefetch > 0 {
		attemptCtx = service.WithPrefetchedStickySession(attemptCtx, record.stickyPrefetch, derefGroupID(apiKey.GroupID), s.metadataBridgeEnabled())
	}
	excluded := make(map[int64]struct{}, len(req.GetExcludedAccountIds()))
	for _, id := range req.GetExcludedAccountIds() {
		excluded[id] = struct{}{}
	}
	outcome := s.anthropicAdmitter.SelectAndAdmit(attemptCtx, handler.AnthropicSelectRequest{
		GroupID: apiKey.GroupID, SessionKey: record.sessionKey, Model: selectModel, Excluded: excluded, // Gemini 不使用会话限制
	}, log)

	if ctx.Err() != nil {
		// 从节点已经不等了（本地：failoverClientGone 先于一切错误处理）。
		if outcome.Kind == handler.AnthropicSelected && outcome.Release != nil {
			outcome.Release()
		}
		return nil, ctx.Err()
	}
	switch outcome.Kind {
	case handler.AnthropicSelected:
	case handler.AnthropicSelectFailed:
		if len(excluded) > 0 {
			// 选号耗尽：从节点按本地的 HandleSelectionExhausted 决定退避重试还是按最近的上游错误写。
			keep = true
			return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
				Format: relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED, AnthropicMessages: true,
			}}}, nil
		}
		return gatewayRejection(handler.GeminiFirstSelectFailureRejection(ctx, gw, apiKey, selectModel, outcome.Err)), nil
	case handler.AnthropicSelectProfitVetoed:
		keep = true
		return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
			Format: relayv1.RejectionFormat_REJECTION_FORMAT_PROFIT_VETOED, VetoedAccountId: outcome.Account.ID,
		}}}, nil
	default:
		return gatewayRejection(handler.GeminiSelectOutcomeRejection(outcome)), nil
	}

	if !s.geminiServed(outcome.Account, true) {
		// 过渡闸门：从节点接不了这种账号。放掉槽位，交给主节点转发。
		if outcome.Release != nil {
			outcome.Release()
		}
		return unsupported(), nil
	}

	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: outcome.Account, release: outcome.Release,
		createdAt: s.now(), quota: quotaReq, groupID: groupID, userID: userID, apiKeyID: apiKey.ID, apiKey: apiKey,
		gemini: true, channelGroupID: groupIDOf(apiKey),
	}
	picked := handler.OpenAISelectOutcome{Kind: handler.OpenAISelected, Account: outcome.Account, Ctx: outcome.Ctx, SessionHash: record.sessionKey}
	resp, rej, err := s.buildSelection(ctx, nodeID, req, sel, picked, selectModel, reqModel, channelMapping, subscription, false)
	if err == nil && rej == nil && ctx.Err() != nil {
		s.ungrant(sel.userID, nodeID, resp.GetSelection().GetGrants())
		err = ctx.Err()
	}
	if err != nil || rej != nil {
		if outcome.Release != nil {
			outcome.Release()
		}
		if rej != nil {
			return rej, nil
		}
		return nil, err
	}
	selection := resp.GetSelection()
	selection.StickyBoundAccountId = record.stickyBound
	selection.SingleAccountRetry = record.singleAccountRetry
	selection.MaxAccountSwitches = int32(geminiDefaultMaxAccountSwitches)
	if cfg := s.deps.Config; cfg != nil && cfg.Gateway.MaxAccountSwitchesGemini > 0 {
		selection.MaxAccountSwitches = int32(cfg.Gateway.MaxAccountSwitchesGemini)
	}
	s.addSelection(sel)
	selected = true
	return resp, nil
}

// geminiStartSession 做 GeminiV1BetaModels 在选号循环之前的会话那一段：会话键（从节点带来 CLI / 通用会话哈希）、粘性会话绑定的
// 账号（请求开始时查一次）、没有绑定时的内容摘要会话匹配（匹配到时按它定会话键、补绑定）。结果记在请求上。
func (s *selector) geminiStartSession(ctx context.Context, record *requestRecord, apiKey *service.APIKey, req *relayv1.SelectRequest, model string) {
	gw := s.deps.AnthropicGateway
	sessionKey := ""
	if hash := req.GetSessionHash(); hash != "" {
		sessionKey = "gemini:" + hash
	}
	var bound int64
	if sessionKey != "" {
		bound, _ = gw.GetCachedSessionAccountID(ctx, apiKey.GroupID, sessionKey)
		if bound > 0 {
			record.stickyPrefetch = bound
		}
	}
	digest := &geminiDigestSession{fallback: bound == 0}
	if digest.fallback && req.GetGeminiDigestChain() != "" {
		digest.chain = req.GetGeminiDigestChain()
		digest.prefix = service.GenerateGeminiPrefixHash(apiKey.User.ID, apiKey.ID, req.GetClientIp(), req.GetUserAgent(), groupPlatformOf(apiKey), model)
		foundUUID, foundAccountID, foundChain, found := gw.FindGeminiSession(ctx, derefGroupID(apiKey.GroupID), digest.prefix, digest.chain)
		if found {
			digest.matchedAs, bound, digest.uuid = foundChain, foundAccountID, foundUUID
			// 如果原会话键为空，用前缀哈希 + uuid 作会话键，粘性会话的逻辑会优先用匹配到的账号。
			if sessionKey == "" {
				sessionKey = service.GenerateGeminiDigestSessionKey(digest.prefix, foundUUID)
			}
			_ = gw.BindStickySession(ctx, apiKey.GroupID, sessionKey, foundAccountID)
		} else {
			digest.uuid = uuid.New().String()
			if sessionKey == "" {
				sessionKey = service.GenerateGeminiDigestSessionKey(digest.prefix, digest.uuid)
			}
		}
	}
	record.sessionKey, record.stickyBound, record.geminiDigest = sessionKey, bound, digest
}

// releaseGeminiAttempt 是 Gemini 一次尝试结束时本地在处理函数里做的主节点那一步：转发成功后保存内容摘要会话（用于
// 之后的摘要匹配）。粘性会话绑定在选号准入时已经做了，这里不再刷新。
func (s *selector) releaseGeminiAttempt(sel *selectionRecord, rel *relayv1.SelectionRelease) {
	record := sel.request
	if !rel.GetForwardSucceeded() || record == nil || record.geminiDigest == nil {
		return
	}
	d := record.geminiDigest
	if !d.fallback || d.chain == "" || d.prefix == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.deps.AnthropicGateway.SaveGeminiSession(ctx, derefGroupID(sel.apiKey.GroupID), d.prefix, d.chain, d.uuid, sel.account.ID, d.matchedAs); err != nil {
		slog.Warn("relay: save gemini digest session failed", "account_id", sel.account.ID, "error", err)
	}
}

// geminiServed 报告从节点现在能不能转发这个账号（Gemini 原生入口 native 为 true；Gemini 平台的 Messages 为 false，那里 Antigravity
// 账号一律走 Antigravity 转发）：Gemini 平台的 API Key、OAuth（Code Assist / Google One /
// AI Studio）、服务账号（Vertex，token 由主节点换好随凭据下发），以及混合调度进来的 Antigravity 账号（Google token 由主节点给，
// 转发路径上的账号状态写入交主节点；API Key 类型的 Antigravity 账号走 Gemini 转发）。
func (s *selector) geminiServed(a *service.Account, native bool) bool {
	if a == nil {
		return false
	}
	switch a.Platform {
	case service.PlatformGemini:
		return s.deps.Gemini != nil
	case service.PlatformAntigravity:
		if native && a.Type == service.AccountTypeAPIKey {
			return s.deps.Gemini != nil
		}
		return s.deps.Antigravity != nil
	}
	return false
}

func derefGroupID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}
