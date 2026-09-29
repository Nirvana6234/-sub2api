package relayselect

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// wsIdleLimit 是 WebSocket 连接记录在两轮之间（不占槽，只占内存）最长保留的时间。占着槽时仍按 holdLimit 清理。
const wsIdleLimit = 6 * time.Hour

// wsReconnectReason 是主节点丢了这条连接的记录（清理、纪元变化）或配置变了、需要客户端重连时的关闭原因。
const wsReconnectReason = "relay session expired, please reconnect"

// isResponsesWebSocketAdmission 报告准入的是 Responses WebSocket 的升级请求（GET /v1/responses、/responses）。
func isResponsesWebSocketAdmission(method, path string) bool {
	return strings.EqualFold(method, "GET") && strings.HasSuffix(strings.TrimRight(path, "/"), "/responses")
}

// wsAuditMayApply 报告这个分组的 WebSocket 连接可能被安全审计处理（接受升级之前还不知道模型，按分组判断）。
func (s *selector) wsAuditMayApply(ctx context.Context, groupID *int64) bool {
	if p := s.deps.PromptAudit; p != nil && p.EffectiveMode() != securityaudit.ModeOff {
		return true
	}
	return s.deps.Moderation.AppliesToGroup(ctx, groupID)
}

func wsClose(code coderws.StatusCode, reason string) *relayv1.SelectResponse {
	return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
		Format: relayv1.RejectionFormat_REJECTION_FORMAT_WS_CLOSE, Status: int32(code), Message: reason,
	}}}
}

func wsFailoverExhausted() *relayv1.SelectResponse {
	return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
		Format: relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED,
	}}}
}

// selectOpenAIWS 是 Responses WebSocket 连接的一次选号，按本地 ResponsesWebSocket 的顺序：
// 第一次（建连）：cyber 会话屏蔽 → 渠道映射 → 用户槽（不排队）→ 计费资格 → 计价上下文 → 选号与准入（SelectAndAdmitWS）；
// 之后（连接内换号）：补占用户槽（不排队）→ 选号与准入。选中的账号绑定这条连接，每一轮用 BeginTurn 签凭证。
func (s *selector) selectOpenAIWS(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), modelCandidates(req))
	if err != nil {
		return nil, err
	}
	if rej != nil {
		// 连接已经接受了（准入在升级之前通过过）：Key 这时才被拒（停用、删除）或配置变了，只能关闭。
		if rej.GetRejection().GetFormat() == relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED {
			return wsClose(coderws.StatusTryAgainLater, wsReconnectReason), nil
		}
		return wsClose(coderws.StatusPolicyViolation, "request rejected"), nil
	}
	apiKey := adm.APIKey
	if s.auditApplies(ctx, apiKey.GroupID, req.GetModel()) {
		// 升级之前按分组判断过不会被审计；配置变了。重连时会交给主节点。
		return wsClose(coderws.StatusTryAgainLater, wsReconnectReason), nil
	}
	ctx = middleware.RelayRequestContext(ctx, adm)
	ctx = service.WithOpenAIGuardianParentSessionHashes(ctx, req.GetGuardianParentSessionHash(), req.GetGuardianParentLegacySessionHash())
	subscription := adm.Billing.Subscription
	userID, groupID := apiKey.User.ID, apiKey.Group.ID
	reqModel := req.GetModel()

	record, first, err := s.requestFor(nodeID, req.GetRequestId())
	if err != nil {
		return nil, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if !first && (record.userID != userID || record.apiKeyID != apiKey.ID || !record.ws) {
		return nil, errors.New("relay request id reused by a different API key")
	}
	// 选不出账号时连接随之关闭：结束这次请求（放掉用户槽），与本地一样。
	selected := false
	defer func() {
		if !selected {
			s.dropRequest(record)
		}
	}()
	quotaReq := service.QuotaRequest{User: apiKey.User, APIKey: apiKey, Group: apiKey.Group, Subscription: subscription, Platform: service.QuotaPlatform(ctx, apiKey)}
	mapping, _ := s.deps.Gateway.ResolveChannelMappingAndRestrict(ctx, apiKey.GroupID, reqModel)
	forwardModel := handler.OpenAIChannelForwardModel(mapping, reqModel)
	if first {
		record.ws = true
		record.userID, record.apiKeyID = userID, apiKey.ID
		if rej := s.cyberRejection(ctx, nodeID, req, apiKey); rej != nil {
			rej.GetRejection().Format = relayv1.RejectionFormat_REJECTION_FORMAT_WS_CLOSE
			rej.GetRejection().Status = int32(coderws.StatusPolicyViolation)
			rej.GetRejection().Message = "session blocked by cyber-security policy"
			return rej, nil
		}
		if rej := s.acquireWSUserSlot(ctx, record, apiKey); rej != nil {
			return rej, nil
		}
		held := heldBalance(req.GetHeldQuota())
		if held > 0 && s.env.Quotas != nil {
			locked, err := s.env.Quotas.NodeReservedBalance(ctx, userID, nodeID)
			if err != nil {
				return wsClose(coderws.StatusPolicyViolation, "billing check failed"), nil
			}
			held = min(held, locked)
		}
		billingCtx := service.WithRelayRequesterHeldBalance(ctx, master.FromMicros(held))
		if err := s.deps.Billing.CheckBillingEligibility(billingCtx, apiKey.User, apiKey, apiKey.Group, subscription, quotaReq.Platform); err != nil {
			return wsClose(coderws.StatusPolicyViolation, "billing check failed"), nil
		}
		// 连接级的计价上下文（选号与准入用）；每一轮在 BeginTurn 按当时重新冻结计价时间。
		record.pricingCtx, record.pricingAt = s.deps.Gateway.WithOpenAIRequestPricingContext(context.WithoutCancel(ctx), apiKey.GroupID)
	} else if rej := s.acquireWSUserSlot(ctx, record, apiKey); rej != nil {
		return rej, nil
	}

	for _, id := range req.GetExcludedAccountIds() {
		record.excluded[id] = struct{}{}
	}
	requiredCapability := service.OpenAIEndpointCapabilityChatCompletions
	if req.GetImageIntent() {
		requiredCapability = service.OpenAIEndpointCapabilityResponses
	}
	// 不排队：用连接级的 ctx（不跟这次调用取消），调用取消了就把拿到的槽放掉。
	outcome := s.admitter.SelectAndAdmitWS(record.pricingCtx, handler.OpenAIWSSelectRequest{
		GroupID:                 apiKey.GroupID,
		PreviousResponseID:      strings.TrimSpace(req.GetPreviousResponseId()),
		SessionHash:             req.GetSessionHash(),
		ForwardModel:            forwardModel,
		RequiredTransport:       service.OpenAIUpstreamTransportResponsesWebsocketV2Ingress,
		RequiredCapability:      requiredCapability,
		PreviousResponseCanMove: req.GetPreviousResponseCanMove(),
		ImageIntent:             req.GetImageIntent(),
		RequestPlatform:         service.PlatformOpenAI,
		Excluded:                record.excluded,
	}, &record.state.ProfitVetoCount, zap.NewNop())
	record.pricingCtx = outcome.Ctx
	if ctx.Err() != nil {
		if outcome.Kind == handler.OpenAIWSSelected && outcome.Release != nil {
			outcome.Release()
		}
		return nil, ctx.Err()
	}
	switch outcome.Kind {
	case handler.OpenAIWSSelected:
	case handler.OpenAIWSSelectFailed:
		// 从节点有上一次换号的错误时按换号耗尽关闭，否则按"无可用账号"（本地同样）。
		return wsFailoverExhausted(), nil
	case handler.OpenAIWSSelectVetoExhausted:
		return wsClose(coderws.StatusTryAgainLater, "no available account"), nil
	case handler.OpenAIWSSelectBusy:
		return wsClose(coderws.StatusTryAgainLater, "account is busy, please retry later"), nil
	case handler.OpenAIWSSelectSlotError:
		return wsClose(coderws.StatusInternalError, "failed to acquire account concurrency slot"), nil
	default:
		return nil, context.Canceled
	}

	sel := &selectionRecord{
		id: newSelectionID(), nodeID: nodeID, request: record, account: outcome.Account, release: outcome.Release,
		createdAt: s.now(), quota: quotaReq, groupID: groupID, userID: userID, apiKeyID: apiKey.ID,
		apiKey: apiKey, cyber: cyberLookup(req.GetCyber()), maxConcurrency: outcome.MaxConcurrency,
	}
	resp, err := s.buildWSSelection(ctx, nodeID, sel, forwardModel, mapping, subscription, outcome.StickyPreviousHit, req.GetSessionHash())
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		if outcome.Release != nil {
			outcome.Release()
		}
		return nil, err
	}
	s.addSelection(sel)
	selected = true
	return resp, nil
}

// acquireWSUserSlot 不排队地占这条连接的用户槽（已占着时不动）；占不到时回关闭。
func (s *selector) acquireWSUserSlot(ctx context.Context, record *requestRecord, apiKey *service.APIKey) *relayv1.SelectResponse {
	s.mu.Lock()
	held := record.userRelease != nil
	s.mu.Unlock()
	if held {
		return nil
	}
	release, acquired, err := s.helper.TryAcquireUserSlotForAPIKey(ctx, apiKey.User.ID, apiKey.User.Concurrency, apiKey.ID)
	if err != nil {
		return wsClose(coderws.StatusInternalError, "failed to acquire user concurrency slot")
	}
	if !acquired {
		return wsClose(coderws.StatusTryAgainLater, "too many concurrent requests, please retry later")
	}
	s.mu.Lock()
	record.userRelease = release
	s.mu.Unlock()
	return nil
}

// buildWSSelection 组装 WebSocket 连接选号的回复：账号快照、第 1 轮的渠道映射；不签凭证、不给额度（每一轮在 BeginTurn）。
func (s *selector) buildWSSelection(ctx context.Context, nodeID int64, sel *selectionRecord, forwardModel string, mapping service.ChannelMappingResult,
	subscription *service.UserSubscription, stickyPreviousHit bool, sessionHash string,
) (*relayv1.SelectResponse, error) {
	snap, err := s.encodeAccount(ctx, nodeID, sel.account, s.nodeHas(nodeID))
	if err != nil {
		return nil, err
	}
	var parentSnap *relayv1.AccountSnapshot
	if sel.account.IsShadow() {
		if parentSnap, err = s.encodeCredentialParent(ctx, nodeID, sel.account, s.nodeHas(nodeID)); err != nil {
			return nil, err
		}
	}
	version, err := s.env.ConfigVersion(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Selection{Selection: &relayv1.Selection{
		SelectionId: sel.id, UserId: sel.userID, ApiKeyId: sel.apiKeyID, GroupId: sel.groupID, BillingMode: billingMode(sel, subscription),
		Account: snap, ForwardModel: forwardModel, ChannelMapped: mapping.Mapped, ChannelMappedModel: mapping.MappedModel,
		ChannelId: mapping.ChannelID, BillingModelSource: mapping.BillingModelSource, SessionHash: sessionHash,
		MaxAccountSwitches: int32(s.maxAccountSwitches()), ConfigVersion: version, CredentialParent: parentSnap,
		StickyPreviousHit: stickyPreviousHit,
	}}}, nil
}

func billingMode(sel *selectionRecord, subscription *service.UserSubscription) relayv1.BillingMode {
	if sel.quota.Group != nil && sel.quota.Group.IsSubscriptionType() && subscription != nil {
		return relayv1.BillingMode_BILLING_MODE_SUBSCRIPTION
	}
	return relayv1.BillingMode_BILLING_MODE_BALANCE
}

func (s *selector) maxAccountSwitches() int {
	if cfg := s.deps.Config; cfg != nil && cfg.Gateway.MaxAccountSwitches > 0 {
		return cfg.Gateway.MaxAccountSwitches
	}
	return 3
}

// wsSelection 取这台节点进行中的 WebSocket 选号。
func (s *selector) wsSelection(nodeID int64, id string) *selectionRecord {
	sel := s.lookupSelection(nodeID, id)
	if sel == nil || !sel.request.ws {
		return nil
	}
	return sel
}

func turnClose(code coderws.StatusCode, reason string) *relayv1.BeginTurnResponse {
	return &relayv1.BeginTurnResponse{CloseStatus: int32(code), CloseReason: reason}
}

// BeginTurn 一轮开始（本地 ResponsesWebSocket 的 BeforeTurn）：按当时的利润门复核账号、冻结这一轮的计价时间、
// 补占这一轮的用户槽和账号槽（不排队），再签这一轮的凭证、尽量补充额度。选号不在了（被清理、纪元变了）
// 回"请重连"。
func (s *selector) BeginTurn(ctx context.Context, nodeID int64, req *relayv1.BeginTurnRequest) (*relayv1.BeginTurnResponse, error) {
	sel := s.wsSelection(nodeID, req.GetSelectionId())
	if sel == nil {
		return turnClose(coderws.StatusTryAgainLater, wsReconnectReason), nil
	}
	record := sel.request
	record.mu.Lock()
	defer record.mu.Unlock()
	s.touch(record)
	apiKey := sel.apiKey

	// 长连接跨峰谷 / 倍率刷新防护：每一轮按当前时刻重装门并复核账号（本地同样在占槽之前）。
	turnCtx, turnAt := s.deps.Gateway.WithOpenAITurnPricingContext(record.pricingCtx, apiKey.GroupID)
	if _, vetoed, _ := s.deps.Gateway.ProfitControlVetoLatest(turnCtx, sel.account); vetoed {
		return turnClose(coderws.StatusTryAgainLater, "account is no longer eligible for this connection, please reconnect"), nil
	}
	if rej := s.acquireWSUserSlot(ctx, record, apiKey); rej != nil {
		r := rej.GetRejection()
		return turnClose(coderws.StatusCode(r.GetStatus()), r.GetMessage()), nil
	}
	s.mu.Lock()
	accountHeld := sel.release != nil
	s.mu.Unlock()
	if !accountHeld {
		release, acquired, err := s.helper.TryAcquireAccountSlot(ctx, sel.account.ID, sel.maxConcurrency)
		if err != nil || !acquired {
			// 本地：账号槽占不到时放掉这一轮刚占的用户槽。
			s.releaseWSUserSlot(record)
			if err != nil {
				return turnClose(coderws.StatusInternalError, "failed to acquire account concurrency slot"), nil
			}
			return turnClose(coderws.StatusTryAgainLater, "account is busy, please retry later"), nil
		}
		s.mu.Lock()
		sel.release = release
		s.mu.Unlock()
	}

	model := strings.TrimSpace(req.GetModel())
	mapping, _ := s.deps.Gateway.ResolveChannelMappingAndRestrict(record.pricingCtx, apiKey.GroupID, model)
	forwardModel := handler.OpenAIChannelForwardModel(mapping, model)
	c := selectionContext(handler.OpenAISelectOutcome{Ctx: record.pricingCtx, Account: sel.account}, sel, mapping, sel.quota.Subscription)
	c.PricingAtUnixMs = turnAt.UnixMilli()
	voucher, _, err := s.env.IssueVoucher(&relayv1.Voucher{
		NodeId: nodeID, SelectionId: sel.id, UserId: sel.userID, ApiKeyId: sel.apiKeyID, AccountId: sel.account.ID,
		GroupId: sel.groupID, BillingMode: billingMode(sel, sel.quota.Subscription), RequestedModel: model,
		AllowedBillingModels: allowedBillingModels(model, forwardModel, sel.account),
		Quote:                &relayv1.Quote{}, Context: c,
	})
	if err != nil {
		return nil, err
	}
	resp := &relayv1.BeginTurnResponse{Voucher: voucher, QuotaNeed: quotaNeed, PricingAtUnixMs: turnAt.UnixMilli()}
	// 额度：尽量补充，但不因为额度拒绝这一轮（本地只在建连时查计费资格）。
	grants, scopes, err := s.acquireQuota(ctx, nodeID, sel.quota, req.GetHeldQuota(), quotaNeed, false)
	if err != nil {
		var insufficient *master.QuotaInsufficientError
		if !errors.As(err, &insufficient) {
			slog.Warn("relay: quota for a websocket turn unavailable", "node_id", nodeID, "user_id", sel.userID, "error", err)
		}
	} else {
		resp.Grants, resp.QuotaScopes = grants, scopes
	}
	if ctx.Err() != nil {
		s.ungrant(sel.userID, nodeID, resp.Grants)
		return nil, ctx.Err()
	}
	return resp, nil
}

// releaseWSUserSlot 放掉这条连接的用户槽（占着时）。
func (s *selector) releaseWSUserSlot(record *requestRecord) {
	s.mu.Lock()
	release := record.userRelease
	record.userRelease = nil
	s.mu.Unlock()
	if release != nil {
		release()
	}
}

// endTurn 一轮结束（本地 AfterTurn 的 releaseTurnSlots）：放掉这一轮的用户槽和账号槽、记响应归属，选号留着。
func (s *selector) endTurn(nodeID int64, rel *relayv1.SelectionRelease) {
	sel := s.wsSelection(nodeID, rel.GetSelectionId())
	if sel == nil {
		s.bindFromVoucher(nodeID, rel)
		return
	}
	record := sel.request
	s.mu.Lock()
	accountRelease := sel.release
	sel.release = nil
	userRelease := record.userRelease
	record.userRelease = nil
	record.lastSeen = s.now()
	s.mu.Unlock()
	if accountRelease != nil {
		accountRelease()
	}
	if userRelease != nil {
		userRelease()
	}
	if len(rel.GetResponseIds()) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		for _, id := range rel.GetResponseIds() {
			s.deps.Gateway.BindRelayHTTPResponse(ctx, sel.groupID, sel.account.ID, id, sel.userID, sel.apiKeyID)
		}
		cancel()
	}
}

// TurnMapping 一轮的渠道映射（本地 MapRequestModel 里的 ResolveChannelMappingAndRestrict）。
func (s *selector) TurnMapping(_ context.Context, nodeID int64, req *relayv1.TurnMappingRequest) (*relayv1.TurnMappingResponse, error) {
	sel := s.wsSelection(nodeID, req.GetSelectionId())
	if sel == nil {
		return nil, master.ErrSelectionNotFound
	}
	record := sel.request
	record.mu.Lock()
	ctx := record.pricingCtx
	record.mu.Unlock()
	mapping, _ := s.deps.Gateway.ResolveChannelMappingAndRestrict(ctx, sel.apiKey.GroupID, strings.TrimSpace(req.GetModel()))
	return &relayv1.TurnMappingResponse{
		ChannelMapped: mapping.Mapped, ChannelMappedModel: mapping.MappedModel, ChannelId: mapping.ChannelID, BillingModelSource: mapping.BillingModelSource,
	}, nil
}

func (s *selector) touch(record *requestRecord) {
	s.mu.Lock()
	record.lastSeen = s.now()
	s.mu.Unlock()
}

// wsLease 是主节点代一台节点持有的 WebSocket 连接数租约。
type wsLease struct {
	nodeID    int64
	apiKeyID  int64
	unlimited bool
	seen      time.Time
}

// wsLeaseTTL 之后没续期的租约记录删掉（Redis 里的租约 60 秒过期，这里只清内存）。
const wsLeaseTTL = 5 * time.Minute

// WebSocketLease 每个 Key 的 WebSocket 连接数租约（本地 ConcurrencyService.AcquireOpenAIWSIngressLease 的存储部分）。
func (s *selector) WebSocketLease(ctx context.Context, nodeID int64, req *relayv1.WebSocketLeaseRequest) (*relayv1.WebSocketLeaseResponse, error) {
	leaseID := strings.TrimSpace(req.GetLeaseId())
	if leaseID == "" || len(leaseID) > 128 {
		return nil, status.Error(codes.InvalidArgument, "lease id is required")
	}
	switch req.GetOp() {
	case relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_ACQUIRE:
		return s.acquireWSLease(ctx, nodeID, req.GetApiKey(), leaseID)
	case relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_REFRESH, relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_RELEASE:
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown lease op")
	}
	s.mu.Lock()
	lease, ok := s.wsLeases[leaseID]
	if ok && lease.nodeID != nodeID {
		ok = false
	}
	if ok {
		lease.seen = s.now()
		s.wsLeases[leaseID] = lease
	}
	if ok && req.GetOp() == relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_RELEASE {
		delete(s.wsLeases, leaseID)
	}
	s.mu.Unlock()
	if !ok {
		return &relayv1.WebSocketLeaseResponse{}, nil
	}
	if lease.unlimited {
		return &relayv1.WebSocketLeaseResponse{Ok: true}, nil
	}
	cache, err := s.deps.Concurrency.OpenAIWSIngressLeaseCache()
	if err != nil {
		return nil, err
	}
	if req.GetOp() == relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_RELEASE {
		if err := cache.ReleaseOpenAIWSIngressLease(ctx, lease.apiKeyID, leaseID); err != nil {
			return nil, err
		}
		return &relayv1.WebSocketLeaseResponse{Ok: true}, nil
	}
	alive, err := cache.RefreshOpenAIWSIngressLease(ctx, lease.apiKeyID, leaseID)
	if err != nil {
		return nil, err
	}
	return &relayv1.WebSocketLeaseResponse{Ok: alive}, nil
}

func (s *selector) acquireWSLease(ctx context.Context, nodeID int64, rawKey, leaseID string) (*relayv1.WebSocketLeaseResponse, error) {
	apiKey, err := s.deps.APIKeys.GetByKey(ctx, rawKey)
	if err != nil {
		if errors.Is(err, service.ErrAPIKeyNotFound) {
			return nil, status.Error(codes.PermissionDenied, "invalid api key")
		}
		return nil, err
	}
	s.mu.Lock()
	if existing, ok := s.wsLeases[leaseID]; ok && existing.nodeID != nodeID {
		s.mu.Unlock()
		return nil, status.Error(codes.AlreadyExists, "lease id in use")
	}
	s.mu.Unlock()
	maxConnections := 0
	if s.deps.Config != nil {
		maxConnections = s.deps.Config.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey
	}
	lease := wsLease{nodeID: nodeID, apiKeyID: apiKey.ID, unlimited: maxConnections <= 0, seen: s.now()}
	if !lease.unlimited {
		cache, err := s.deps.Concurrency.OpenAIWSIngressLeaseCache()
		if err != nil {
			return nil, err
		}
		acquired, err := cache.AcquireOpenAIWSIngressLease(ctx, apiKey.ID, maxConnections, leaseID)
		if err != nil {
			return nil, err
		}
		if !acquired {
			return &relayv1.WebSocketLeaseResponse{}, nil
		}
	}
	s.mu.Lock()
	s.wsLeases[leaseID] = lease
	s.mu.Unlock()
	return &relayv1.WebSocketLeaseResponse{Ok: true}, nil
}
