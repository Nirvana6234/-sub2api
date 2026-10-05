package relayselect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/relay/keycodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// anthropicDefaultMaxAccountSwitches 是 Messages 的换号上限默认值（与 NewGatewayHandler 一致）。
const anthropicDefaultMaxAccountSwitches = 10

// selectAnthropic 按本地 GatewayHandler.Messages 主循环（Anthropic 分组）的顺序做一轮选号：
// 中间件链的检查 → 渠道映射 → 用户并发槽 → 计费资格 → 计价上下文 → 粘性会话（请求开始时查一次）→
// 选号与准入（handler.AnthropicAccountAdmitter，一轮）。
//
// 与 OpenAI 不同，换号状态（已失败的账号、利润否决次数、选号耗尽后的退避）在从节点的处理函数里，与单机同一段
// 代码：这里按从节点带来的已排除账号选、不累计；否决、选号失败都回给从节点，由它照本地的循环处理。
func (s *selector) selectAnthropic(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	gw := s.deps.AnthropicGateway
	if gw == nil {
		return unsupported(), nil
	}
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), modelCandidates(req),
		autoGroupChoice{pinned: req.GetAutoGroupId()}, servedAnthropicFor(req.GetPath())...)
	if err != nil || rej != nil {
		return rej, err
	}
	origKey := adm.APIKey
	apiKey := origKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	ctx = middleware.RelayRequestContext(ctx, adm)
	subscription := adm.Billing.Subscription
	forced := ""
	if req.GetFallbackGroupId() == 0 && isAntigravityRoute(req.GetPath()) {
		// /antigravity/v1：强制 Antigravity 平台（由路径定）；切到兜底分组后本地清掉强制平台，按分组平台调度。
		ctx, forced = withForcedPlatform(ctx, req.GetPath()), service.PlatformAntigravity
	}
	if fb := req.GetFallbackGroupId(); fb != 0 {
		// 请求中途已经切到兜底分组（Antigravity 回 prompt 过长，本地 currentAPIKey = fallbackAPIKey）：选号、计费、
		// 凭证里的分组按兜底分组，订阅清空；渠道映射、渠道功能配置、粘性会话起点仍按原来的分组（本地只在请求开头算一次）。
		fallbackKey, ok, err := s.fallbackAPIKey(ctx, origKey, fb)
		if err != nil {
			return nil, err
		}
		if !ok {
			return gatewayRejection(handler.OpenAIGatewayRejection{Status: http.StatusBadRequest, ErrType: "invalid_request_error", Message: "Invalid fallback group"}), nil
		}
		apiKey, subscription = fallbackKey, nil
	}
	// 组合平台分组：按改写前的公开模型选目标（本地 compositeTarget 中间件），只接选到 Anthropic 或 Gemini 的，其余交给主节点。
	composite, err := s.resolveComposite(ctx, apiKey, req.GetRouteModel(), req.GetPath())
	if err != nil {
		return nil, err
	}
	if !compositeServedBy(apiKey, composite, service.PlatformAnthropic) && !compositeServedBy(apiKey, composite, service.PlatformGemini) {
		return unsupported(), nil
	}
	ctx = service.WithCompositeRouteDecision(ctx, composite)
	userID, groupID := apiKey.User.ID, groupIDOf(apiKey)
	reqModel := req.GetModel()
	platform := groupPlatformOf(apiKey)
	if forced != "" {
		platform = forced
	} else if target, ok := service.ResolvedTargetPlatformFromContext(ctx); ok {
		platform = target
	}
	// Gemini 平台的 Messages（本地 GatewayHandler.Messages 的 platform == gemini 分支）：Gemini 不使用会话数限制、换号上限用
	// Gemini 的、没有指纹，账号用完不做粘性续期、不放会话数注册。
	isGemini := platform == service.PlatformGemini
	log := zap.NewNop()

	channelMapping, _ := gw.ResolveChannelMappingAndRestrict(ctx, origKey.GroupID, reqModel)
	forwardModel := reqModel
	if channelMapping.Mapped {
		forwardModel = channelMapping.MappedModel
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
	// 没有成功选出账号就结束这次请求（放掉用户槽），与 OpenAI 一致：从节点照本地的循环接着选时从头占。
	// keep：利润否决、选号耗尽后从节点照本地接着选（同一请求），请求记录（用户槽、计价时间、粘性会话起点）留着，
	// 由之后的选号或请求结束消息收尾。
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
		if rej := s.startRequest(ctx, record, req, adm, quotaReq, false, true, pricing); rej != nil {
			return rej, nil
		}
		record.groupID = groupID
		// 粘性会话：本地在选号循环之前按会话键查一次绑定的账号。
		record.sessionKey = req.GetSessionHash()
		if record.sessionKey != "" {
			record.stickyBound, _ = gw.GetCachedSessionAccountID(ctx, origKey.GroupID, record.sessionKey)
		}
		// 单账号分组提前设 SingleAccountRetry（Antigravity 单账号分组收到 503 时不设模型限流）：本地在请求开始时查一次。
		record.singleAccountRetry = gw.IsSingleAntigravityAccountGroup(ctx, origKey.GroupID)
	}

	attemptCtx, cancel := context.WithCancel(record.pricingCtx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if isAntigravityRoute(req.GetPath()) {
		// 强制平台以这次选号为准（请求的计价上下文来自第一次选号：切到兜底分组之后要清掉）。
		attemptCtx = context.WithValue(attemptCtx, ctxkey.ForcePlatform, forced)
	}
	if record.stickyBound > 0 {
		attemptCtx = service.WithPrefetchedStickySession(attemptCtx, record.stickyBound, groupIDOf(origKey), s.metadataBridgeEnabled())
	}
	excluded := make(map[int64]struct{}, len(req.GetExcludedAccountIds()))
	for _, id := range req.GetExcludedAccountIds() {
		excluded[id] = struct{}{}
	}
	selectReq := handler.AnthropicSelectRequest{
		GroupID: apiKey.GroupID, SessionKey: record.sessionKey, Model: reqModel, Excluded: excluded,
		MetadataUserID: req.GetMetadataUserId(), UserID: userID,
		Intercept: func() handler.InterceptType { return handler.InterceptType(req.GetInterceptType()) },
	}
	if isGemini {
		selectReq.MetadataUserID, selectReq.UserID = "", 0 // Gemini 不使用会话限制
	}
	outcome := s.anthropicAdmitter.SelectAndAdmit(attemptCtx, selectReq, log)

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
		r, _ := handler.AnthropicFirstSelectFailureRejection(ctx, gw, apiKey, reqModel, platform, outcome.Err)
		return gatewayRejection(r), nil
	case handler.AnthropicSelectIntercepted:
		return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
			Format: relayv1.RejectionFormat_REJECTION_FORMAT_INTERCEPTED, InterceptType: int32(outcome.Intercept),
		}}}, nil
	case handler.AnthropicSelectProfitVetoed:
		// 尝试被否决（从未转发），立即释放该账号的会话注册（本地同一处）。
		gw.ReleaseAccountSession(context.Background(), outcome.Account, record.sessionKey)
		keep = true
		return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
			Format: relayv1.RejectionFormat_REJECTION_FORMAT_PROFIT_VETOED, VetoedAccountId: outcome.Account.ID,
		}}}, nil
	default:
		return gatewayRejection(handler.AnthropicSelectOutcomeRejection(outcome)), nil
	}

	served := s.anthropicServed(outcome.Account)
	if isGemini {
		served = s.geminiServed(outcome.Account, false)
	}
	if !served {
		// 过渡：从节点还不能转发这种账号。放掉槽位和会话注册，交给主节点转发。
		if outcome.Release != nil {
			outcome.Release()
		}
		if !isGemini {
			gw.ReleaseAccountSession(context.Background(), outcome.Account, record.sessionKey)
		}
		return unsupported(), nil
	}

	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: outcome.Account, release: outcome.Release,
		createdAt: s.now(), quota: quotaReq, groupID: groupID, userID: userID, apiKeyID: apiKey.ID, apiKey: apiKey,
		anthropic: !isGemini, gemini: isGemini, channelGroupID: groupIDOf(origKey),
	}
	picked := handler.OpenAISelectOutcome{Kind: handler.OpenAISelected, Account: outcome.Account, Ctx: outcome.Ctx, SessionHash: record.sessionKey}
	resp, rej, err := s.buildSelection(ctx, nodeID, req, sel, picked, forwardModel, reqModel, channelMapping, subscription, true)
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
	if !isGemini {
		if err := s.attachIdentity(ctx, selection, outcome.Account, req.GetFingerprintHeaders()); err != nil {
			s.ungrant(sel.userID, nodeID, selection.GetGrants())
			if outcome.Release != nil {
				outcome.Release()
			}
			return nil, err
		}
	}
	selection.StickyBoundAccountId = record.stickyBound
	selection.SingleAccountRetry = record.singleAccountRetry
	selection.MaxAccountSwitches = int32(anthropicDefaultMaxAccountSwitches)
	if cfg := s.deps.Config; cfg != nil && cfg.Gateway.MaxAccountSwitches > 0 {
		selection.MaxAccountSwitches = int32(cfg.Gateway.MaxAccountSwitches)
	}
	if isGemini {
		selection.MaxAccountSwitches = int32(geminiDefaultMaxAccountSwitches)
		if cfg := s.deps.Config; cfg != nil && cfg.Gateway.MaxAccountSwitchesGemini > 0 {
			selection.MaxAccountSwitches = int32(cfg.Gateway.MaxAccountSwitchesGemini)
		}
	}
	s.addSelection(sel)
	selected = true
	return resp, nil
}

// groupIDOf、groupPlatformOf：Key 所属分组的 ID 和平台；未分组的 Key 是 0 和空。
func groupIDOf(k *service.APIKey) int64 {
	if k == nil || k.Group == nil {
		return 0
	}
	return k.Group.ID
}

func groupPlatformOf(k *service.APIKey) string {
	if k == nil || k.Group == nil {
		return noGroupPlatform
	}
	return k.Group.Platform
}

// fallbackAPIKey 解析 Key 当前分组配置的兜底分组并换上（本地 Messages 里 PromptTooLongError 的分支）：id 必须是分组的
// FallbackGroupIDOnInvalidRequest，兜底分组必须是 Anthropic 平台、非订阅、自己没有兜底。不合规时 ok 为 false。
func (s *selector) fallbackAPIKey(ctx context.Context, apiKey *service.APIKey, id int64) (*service.APIKey, bool, error) {
	gw := s.deps.AnthropicGateway
	if gw == nil || apiKey == nil || apiKey.Group == nil || apiKey.Group.FallbackGroupIDOnInvalidRequest == nil ||
		*apiKey.Group.FallbackGroupIDOnInvalidRequest != id || id <= 0 {
		return nil, false, nil
	}
	group, err := gw.ResolveGroupByID(ctx, id)
	if err != nil {
		return nil, false, nil
	}
	if group.Platform != service.PlatformAnthropic || group.SubscriptionType == service.SubscriptionTypeSubscription ||
		group.FallbackGroupIDOnInvalidRequest != nil {
		return nil, false, nil
	}
	return handler.CloneAPIKeyWithGroup(apiKey, group), true, nil
}

// attachIdentity 给 OAuth / setup-token 账号带上主节点定下的指纹和伪装会话 ID（service.GatewayService.RelayIdentity）。
func (s *selector) attachIdentity(ctx context.Context, selection *relayv1.Selection, account *service.Account, headers map[string]string) error {
	h := http.Header{}
	for _, name := range service.FingerprintHeaderNames {
		if v := headers[name]; v != "" {
			h.Set(name, v)
		}
	}
	fp, masked, err := s.deps.AnthropicGateway.RelayIdentity(ctx, account, h)
	if err != nil || fp == nil {
		return err
	}
	raw, err := json.Marshal(fp)
	if err != nil {
		return err
	}
	selection.Fingerprint, selection.MaskedSessionId = raw, masked
	return nil
}

// nodeServesAnthropicAccount 报告从节点现在能不能转发这个账号：Anthropic 平台的 API Key、OAuth / setup-token、
// 服务账号（Vertex）、Bedrock 账号（用户消息串行队列的锁和计数经 UserMsgQueue 在主节点），以及混合调度进来的
// Antigravity 账号（Google token 由主节点给，转发路径上的账号状态写入交主节点）。
func (s *selector) nodeServesAnthropicAccount(a *service.Account) bool {
	if a == nil {
		return false
	}
	if a.Platform == service.PlatformAntigravity {
		// 开了混合调度进 Anthropic 分组的 Antigravity 账号：主节点装了 Antigravity 转发服务（取 token、照写账号状态）才接。
		return s.deps.Antigravity != nil
	}
	if a.Platform == service.PlatformGemini {
		// count_tokens 选到的 Gemini 账号（本地转发时回"不支持"，同一段代码在从节点上跑）。
		return s.geminiServed(a, false)
	}
	if a.Platform != service.PlatformAnthropic {
		return false
	}
	switch {
	case a.Type == service.AccountTypeAPIKey, a.Type == service.AccountTypeServiceAccount, a.Type == service.AccountTypeBedrock:
		return true
	case a.IsAnthropicOAuthOrSetupToken():
		return true
	}
	return false
}

// metadataBridgeEnabled 与 GatewayHandler.metadataBridgeEnabled 一致。
func (s *selector) metadataBridgeEnabled() bool {
	return s.deps.Config == nil || s.deps.Config.Gateway.OpenAIWS.MetadataBridgeEnabled
}

// selectAnthropicCountTokens 按本地 GatewayHandler.CountTokens 的顺序：中间件复查 → 计费资格（不占用户槽）→ 按模型选
// 一个账号（SelectAccountForModel，不占账号槽）。不计费：没有凭证、不给额度（设计 5.3 不计费请求不签凭证）。
// 选号结果带账号快照、渠道功能配置、身份信息，从节点照本地转发。
func (s *selector) selectAnthropicCountTokens(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	gw := s.deps.AnthropicGateway
	if gw == nil {
		return unsupported(), nil
	}
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), modelCandidates(req),
		autoGroupChoice{pinned: req.GetAutoGroupId()}, servedAnthropicFor(req.GetPath())...)
	if err != nil || rej != nil {
		return rej, err
	}
	apiKey := adm.APIKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	ctx = middleware.RelayRequestContext(ctx, adm)
	ctx = withForcedPlatform(ctx, req.GetPath())
	composite, err := s.resolveComposite(ctx, apiKey, req.GetRouteModel(), req.GetPath())
	if err != nil {
		return nil, err
	}
	if !compositeServedBy(apiKey, composite, service.PlatformAnthropic) {
		return unsupported(), nil
	}
	ctx = service.WithCompositeRouteDecision(ctx, composite)
	if err := s.checkBilling(ctx, nodeID, req.GetHeldQuota(), apiKey, adm.Billing.Subscription, service.QuotaPlatform(ctx, apiKey)); err != nil {
		return gatewayRejection(billingRejection(err, false)), nil
	}
	sessionHash, model := req.GetSessionHash(), req.GetModel()
	account, err := gw.SelectAccountForModel(ctx, apiKey.GroupID, sessionHash, model)
	if err != nil {
		return gatewayRejection(handler.AnthropicCountTokensSelectFailureRejection(ctx, gw, apiKey, model, err)), nil
	}
	if !s.anthropicServed(account) {
		gw.ReleaseAccountSession(context.Background(), account, sessionHash)
		return unsupported(), nil
	}

	record, _, err := s.requestFor(nodeID, req.GetRequestId())
	if err != nil {
		return nil, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	record.userID, record.apiKeyID, record.sessionKey = apiKey.User.ID, apiKey.ID, sessionHash
	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: account, createdAt: s.now(),
		groupID: groupIDOf(apiKey), userID: apiKey.User.ID, apiKeyID: apiKey.ID, apiKey: apiKey, anthropic: true, countTokens: true,
	}
	fail := func(err error) (*relayv1.SelectResponse, error) {
		gw.ReleaseAccountSession(context.Background(), account, sessionHash)
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
	features, err := s.channelFeatures(ctx, sel.groupID)
	if err != nil {
		return fail(err)
	}
	selection := &relayv1.Selection{
		SelectionId: sel.id, UserId: sel.userID, ApiKeyId: sel.apiKeyID, GroupId: sel.groupID, Account: snap, ForwardModel: model,
		SessionHash: sessionHash, ConfigVersion: version, CredentialParent: parentSnap, ChannelFeatures: features,
	}
	if err := s.attachIdentity(ctx, selection, account, req.GetFingerprintHeaders()); err != nil {
		return fail(err)
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	s.addSelection(sel)
	return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Selection{Selection: selection}}, nil
}

// releaseAnthropicAttempt 是 Messages 一次尝试结束时本地在处理函数里做的主节点那几步：
//   - 转发成功：刷新粘性会话绑定（bindAnthropicSticky）、OAuth 账号有基础 RPM 时计一次 RPM；
//   - 上游没服务这次尝试：放掉这个账号为这次会话做的会话数注册（本地 failover 继续、请求最终失败时的释放）。
func (s *selector) releaseAnthropicAttempt(sel *selectionRecord, rel *relayv1.SelectionRelease) {
	gw := s.deps.AnthropicGateway
	if gw == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if rel.GetForwardSucceeded() && !sel.countTokens {
		s.bindAnthropicSticky(sel)
		if sel.account.IsAnthropicOAuthOrSetupToken() && sel.account.GetBaseRPM() > 0 {
			if err := gw.IncrementAccountRPM(ctx, sel.account.ID); err != nil {
				slog.Warn("relay: increment account rpm failed", "account_id", sel.account.ID, "error", err)
			}
		}
	}
	if !rel.GetUpstreamServed() && sel.request != nil {
		gw.ReleaseAccountSession(ctx, sel.account, sel.request.sessionKey)
	}
}

// bindAnthropicSticky 是 Messages 转发成功后的粘性会话绑定（本地同一条件）：请求开始时没有绑定，或者绑定的就是
// 这次的账号时创建/刷新；粘性账号因负载被跳过、选中了别的账号时不覆盖原绑定。
func (s *selector) bindAnthropicSticky(sel *selectionRecord) {
	gw := s.deps.AnthropicGateway
	record := sel.request
	if gw == nil || record == nil || record.sessionKey == "" {
		return
	}
	if record.stickyBound != 0 && record.stickyBound != sel.account.ID {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	groupID := sel.groupID
	if err := gw.BindStickySession(ctx, &groupID, record.sessionKey, sel.account.ID); err != nil {
		slog.Warn("relay: bind sticky session failed", "account_id", sel.account.ID, "error", err)
	}
}

// SwitchFallbackGroup 见 RelayControl.SwitchFallbackGroup：本地 Messages 里 PromptTooLongError 分支在主节点的那一半。
// Key 按准入同一段复查，解析当前分组配置的兜底分组（不合规就当没有），做计费资格复查（订阅为空，平台取兜底分组的），
// 回兜底分组的 Key 快照；计费复查不过时回那个错误，从节点按它写。
func (s *selector) SwitchFallbackGroup(ctx context.Context, nodeID int64, req *relayv1.SwitchFallbackGroupRequest) (*relayv1.SwitchFallbackGroupResponse, error) {
	none := &relayv1.SwitchFallbackGroupResponse{}
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), nil,
		autoGroupChoice{pinned: req.GetAutoGroupId()}, servedAnthropicFor(req.GetPath())...)
	if err != nil {
		return nil, err
	}
	if rej != nil || adm.APIKey.Group == nil || adm.APIKey.Group.FallbackGroupIDOnInvalidRequest == nil {
		return none, nil
	}
	s.admitted.note(nodeID, adm.APIKey.User.ID, s.now())
	ctx = middleware.RelayRequestContext(ctx, adm)
	id := *adm.APIKey.Group.FallbackGroupIDOnInvalidRequest
	fallbackKey, ok, err := s.fallbackAPIKey(ctx, adm.APIKey, id)
	if err != nil || !ok {
		return none, err
	}
	if err := s.checkBilling(ctx, nodeID, req.GetHeldQuota(), fallbackKey, nil, service.PlatformFromAPIKey(fallbackKey)); err != nil {
		return &relayv1.SwitchFallbackGroupResponse{BillingRejection: gatewayRejection(billingRejection(err, false)).GetRejection()}, nil
	}
	encoded, err := keycodec.EncodeAPIKey(fallbackKey)
	if err != nil {
		return nil, err
	}
	return &relayv1.SwitchFallbackGroupResponse{Switched: true, ApiKey: encoded, FallbackGroupId: id}, nil
}
