package relayselect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
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
		autoGroupChoice{pinned: req.GetAutoGroupId()}, anthropicServedPlatforms...)
	if err != nil || rej != nil {
		return rej, err
	}
	apiKey := adm.APIKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	ctx = middleware.RelayRequestContext(ctx, adm)
	subscription := adm.Billing.Subscription
	userID, groupID := apiKey.User.ID, apiKey.Group.ID
	reqModel := req.GetModel()
	platform := apiKey.Group.Platform
	log := zap.NewNop()

	channelMapping, _ := gw.ResolveChannelMappingAndRestrict(ctx, apiKey.GroupID, reqModel)
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
			record.stickyBound, _ = gw.GetCachedSessionAccountID(ctx, apiKey.GroupID, record.sessionKey)
		}
	}

	attemptCtx, cancel := context.WithCancel(record.pricingCtx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if record.stickyBound > 0 {
		attemptCtx = service.WithPrefetchedStickySession(attemptCtx, record.stickyBound, groupID, s.metadataBridgeEnabled())
	}
	excluded := make(map[int64]struct{}, len(req.GetExcludedAccountIds()))
	for _, id := range req.GetExcludedAccountIds() {
		excluded[id] = struct{}{}
	}
	outcome := s.anthropicAdmitter.SelectAndAdmit(attemptCtx, handler.AnthropicSelectRequest{
		GroupID: apiKey.GroupID, SessionKey: record.sessionKey, Model: reqModel, Excluded: excluded,
		MetadataUserID: req.GetMetadataUserId(), UserID: userID,
		Intercept: func() handler.InterceptType { return handler.InterceptType(req.GetInterceptType()) },
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

	if !s.anthropicServed(outcome.Account) {
		// 过渡：从节点还不能转发这种账号（OAuth、Bedrock、Vertex、Antigravity，开发计划总账）。放掉槽位和会话注册，
		// 交给主节点转发。
		if outcome.Release != nil {
			outcome.Release()
		}
		gw.ReleaseAccountSession(context.Background(), outcome.Account, record.sessionKey)
		return unsupported(), nil
	}

	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: outcome.Account, release: outcome.Release,
		createdAt: s.now(), quota: quotaReq, groupID: groupID, userID: userID, apiKeyID: apiKey.ID, apiKey: apiKey,
		anthropic: true,
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
	if err := s.attachIdentity(ctx, selection, outcome.Account, req.GetFingerprintHeaders()); err != nil {
		s.ungrant(sel.userID, nodeID, selection.GetGrants())
		if outcome.Release != nil {
			outcome.Release()
		}
		return nil, err
	}
	selection.StickyBoundAccountId = record.stickyBound
	selection.MaxAccountSwitches = int32(anthropicDefaultMaxAccountSwitches)
	if cfg := s.deps.Config; cfg != nil && cfg.Gateway.MaxAccountSwitches > 0 {
		selection.MaxAccountSwitches = int32(cfg.Gateway.MaxAccountSwitches)
	}
	s.addSelection(sel)
	selected = true
	return resp, nil
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
// 服务账号（Vertex）、Bedrock 账号（用户消息串行队列的锁和计数经 UserMsgQueue 在主节点）；Antigravity 账号随后接入。
func (s *selector) nodeServesAnthropicAccount(a *service.Account) bool {
	if a == nil || a.Platform != service.PlatformAnthropic {
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
		autoGroupChoice{pinned: req.GetAutoGroupId()}, anthropicServedPlatforms...)
	if err != nil || rej != nil {
		return rej, err
	}
	apiKey := adm.APIKey
	s.admitted.note(nodeID, apiKey.User.ID, s.now())
	ctx = middleware.RelayRequestContext(ctx, adm)
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
		groupID: apiKey.Group.ID, userID: apiKey.User.ID, apiKeyID: apiKey.ID, apiKey: apiKey, anthropic: true, countTokens: true,
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
