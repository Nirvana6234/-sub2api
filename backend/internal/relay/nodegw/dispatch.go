// Package nodegw 是主从分流从节点上的 OpenAI 网关装配（开发计划 WP9）：准入中间件、处理函数的远程选号
// 分发（handler.OpenAIRelayDispatcher）、响应归属收集、交给主节点转发。处理函数本身与单机是同一份。
package nodegw

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/relay/accountcodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/proto"
)

// Deps 是分发用到的从节点部件。
type Deps struct {
	NodeID  func() int64
	Select  *node.SelectClient
	Quota   *node.LocalQuota
	Secrets *accountcodec.SecretCache
	Open    accountcodec.Opener
	WAL     *node.UsageWAL
	// Kick 让扣费发送尽快发一批。
	Kick func()
	// EnsureConfig 在选号回复里的配置版本比本机新时同步拉取（设计 6.2）。
	EnsureConfig func(ctx context.Context, version string) error
	// AfterEpochChange 在主节点纪元变化后调用：核对完租约（ReportLeases）才返回，之后才能再选号（开发计划 WP9）。
	AfterEpochChange func(ctx context.Context) error
	// CyberEnabled 报告 cyber 会话屏蔽是否打开（打开时从节点算查询键随选号带上）。
	CyberEnabled func(ctx context.Context) bool
	// HandOff 把请求交给主节点转发（见 handoff.go）；nil 时按 503 写。
	HandOff func(c *gin.Context, body []byte)
}

// Dispatcher 实现 handler.OpenAIRelayDispatcher。
type Dispatcher struct {
	deps Deps
	// scopes 记每个 Key 上次选号用到的子额度，下次选号报"手里还没用掉的"（主节点据此决定补不补）。
	scopes sync.Map // apiKeyID -> []node.QuotaScope
	// pending 是已写进扣费队列、等主节点确认的预扣（按队列序号）。
	pending sync.Map // seq -> *node.Reservation
}

// NewDispatcher 创建分发。
func NewDispatcher(deps Deps) *Dispatcher { return &Dispatcher{deps: deps} }

var _ handler.OpenAIRelayDispatcher = (*Dispatcher)(nil)

const requestStateKey = "relay.nodegw.request"

// requestState 是一次客户端请求在从节点上的选号状态。
type requestState struct {
	mu      sync.Mutex
	id      string
	attempt uint32
	rawBody []byte
	// routeModel：组合平台分组选目标用的公开模型（改写请求体之前的），选号时带给主节点。
	routeModel string
	// handedOff：这次请求已交给主节点转发（主节点照本地处理，自动分组的结果也由它自己记）。
	handedOff bool
	// startGroupID：自动分组 Key 这次请求第一次选号时的分组（请求开头那几项检查按它做）。
	startGroupID int64
	current      *attemptState
}

// attemptState 是一次选中的尝试。WebSocket 连接上，连接选号一份（收 response id、释放），每一轮另有一份
// （这一轮的凭证和预扣，同一个选号 ID）。
type attemptState struct {
	selectionID string
	voucher     []byte
	reservation *node.Reservation
	// userID、apiKeyID：WebSocket 每一轮报手里的额度用。
	userID, apiKeyID int64
	// turnCalls：这条 WebSocket 连接选号上调过几次 BeginTurn（幂等键用）。
	turnCalls atomic.Uint32
	// turnID：WebSocket 这一轮的 ID（主节点回的，轮结束时带回）。
	turnID string

	mu             sync.Mutex
	responseIDs    []string
	finished       bool
	usageSubmitted bool
	released       bool
	// cyberDone 在 cyber 命中报告发完（或放弃）时关闭：释放要等它，主节点按进行中的选号认这份报告。
	cyberDone chan struct{}
}

func (a *attemptState) addResponseID(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, have := range a.responseIDs {
		if have == id {
			return
		}
	}
	a.responseIDs = append(a.responseIDs, id)
}

// stateOf 取这次请求的状态（准入中间件建的；没有时现建一个）。
func stateOf(c *gin.Context) *requestState {
	if v, ok := c.Get(requestStateKey); ok {
		if st, ok := v.(*requestState); ok {
			return st
		}
	}
	st := &requestState{id: node.NewRequestID()}
	c.Set(requestStateKey, st)
	return st
}

// Select 远程选号（handler.OpenAIRelayDispatcher）。
func (d *Dispatcher) Select(c *gin.Context, req handler.OpenAIRelaySelectRequest) handler.OpenAIRelaySelectResult {
	st := stateOf(c)
	d.flush(st, false)
	ctx := c.Request.Context()

	sreq := d.selectRequest(c, st, req)
	st.mu.Lock()
	st.attempt++
	sreq.Attempt = st.attempt
	if st.startGroupID == 0 {
		st.startGroupID = sreq.AutoGroupId
	}
	sreq.AutoGroupStartId = st.startGroupID
	st.mu.Unlock()
	resp, err := d.deps.Select.Select(ctx, sreq)
	if errors.Is(err, transport.ErrEpochChanged) && d.deps.AfterEpochChange != nil {
		// 主节点重启过：先核对租约，再以新的一次选号重来（旧纪元的请求记录已不在）。
		if err = d.deps.AfterEpochChange(ctx); err == nil {
			st.mu.Lock()
			st.attempt++
			sreq.Attempt = st.attempt
			st.mu.Unlock()
			resp, err = d.deps.Select.Select(ctx, sreq)
		}
	}
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("relay select failed", "error", err)
		}
		return rejection(&handler.OpenAIRelayRejection{Kind: handler.OpenAIRelayRejectUnavailable})
	}
	if r := resp.GetRejection(); r != nil {
		return rejection(convertRejection(r))
	}
	sel := resp.GetSelection()
	if v := sel.GetConfigVersion(); v != "" && d.deps.EnsureConfig != nil {
		if err := d.deps.EnsureConfig(ctx, v); err != nil {
			slog.Warn("relay config sync before forwarding failed", "version", v, "error", err)
		}
	}
	return d.admitSelection(c, st, req, sel)
}

// selectRequest 组装选号请求：单机处理函数交给主节点那几步的原始事实。
func (d *Dispatcher) selectRequest(c *gin.Context, st *requestState, req handler.OpenAIRelaySelectRequest) *relayv1.SelectRequest {
	clientIP := strings.TrimSpace(ip.GetClientIP(c))
	endpoint := relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_RESPONSES
	switch {
	case req.Chat:
		endpoint = relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_CHAT
	case req.Messages:
		endpoint = relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_MESSAGES
	case req.WS:
		endpoint = relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_RESPONSES_WS
	}
	sreq := &relayv1.SelectRequest{
		RequestId:               st.id,
		Credential:              &relayv1.SelectRequest_ApiKey{ApiKey: req.APIKey.Key},
		ClientIp:                clientIP,
		Method:                  c.Request.Method,
		Path:                    c.Request.URL.Path,
		Endpoint:                endpoint,
		Model:                   req.Model,
		Stream:                  req.Stream,
		SessionHash:             req.SessionHash,
		PreviousResponseId:      req.PreviousResponseID,
		ImageIntent:             req.ImageIntent,
		LegacyCompact:           req.LegacyCompact,
		NativeCompactionV2:      req.NativeCompactionV2,
		UserAgent:               c.GetHeader("User-Agent"),
		HttpRequestId:           c.Writer.Header().Get("X-Request-Id"),
		PreviousResponseCanMove: req.PreviousResponseCanMove,
		RouteModel:              st.routeModel,
		AutoGroupId:             autoGroupID(req.APIKey),
	}
	sreq.ClientRequestId, _ = c.Request.Context().Value(ctxkey.ClientRequestID).(string)
	sreq.GuardianParentSessionHash, sreq.GuardianParentLegacySessionHash = service.OpenAIGuardianParentSessionHashes(c.Request.Context())
	for id := range req.Excluded {
		sreq.ExcludedAccountIds = append(sreq.ExcludedAccountIds, id)
	}
	if d.deps.CyberEnabled != nil && d.deps.CyberEnabled(c.Request.Context()) {
		l := service.NewCyberSessionLookup(req.APIKey.ID, c, req.Body, clientIP, c.GetHeader("User-Agent"))
		sreq.Cyber = &relayv1.CyberSessionLookup{
			ExplicitKey: l.ExplicitKey, ScopeKey: l.ScopeKey, TranscriptKeys: l.TranscriptKeys, TranscriptTruncated: l.TranscriptTruncated,
			PreLatestUserKey: l.PreLatestUserKey,
		}
	}
	if st.rawBody != nil {
		sreq.ModelCandidates = requestmodel.FromBodyCandidates(c.FullPath(), c.GetHeader("Content-Type"), st.rawBody)
	}
	if req.APIKey.User != nil {
		sreq.HeldQuota = d.heldQuota(req.APIKey.User.ID, req.APIKey.ID)
	}
	return sreq
}

// heldQuota 是这个 Key 上次用到的各项子额度里、手里还没用掉的金额（主节点据此决定补不补）。
func (d *Dispatcher) heldQuota(userID, apiKeyID int64) []*relayv1.HeldQuota {
	v, ok := d.scopes.Load(apiKeyID)
	if !ok {
		return nil
	}
	scopes, _ := v.([]node.QuotaScope)
	out := make([]*relayv1.HeldQuota, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, &relayv1.HeldQuota{
			Scope:  &relayv1.QuotaScope{Dimension: s.Dimension, ScopeId: s.ScopeID, ScopeKey: s.ScopeKey},
			Unused: max(d.deps.Quota.Unused(userID, s), 0),
		})
	}
	return out
}

// admitSelection 用上选号结果：额度、预扣、账号凭据、ctx。失败时放掉这次选号并按主节点不可用处理。
func (d *Dispatcher) admitSelection(c *gin.Context, st *requestState, req handler.OpenAIRelaySelectRequest, sel *relayv1.Selection) handler.OpenAIRelaySelectResult {
	a := &attemptState{selectionID: sel.GetSelectionId(), voucher: sel.GetVoucher(), userID: sel.GetUserId(), apiKeyID: req.APIKey.ID}
	st.mu.Lock()
	st.current = a
	st.mu.Unlock()
	fail := func(msg string, err error, r *handler.OpenAIRelayRejection) handler.OpenAIRelaySelectResult {
		slog.Warn(msg, "selection_id", a.selectionID, "error", err)
		d.flush(st, false)
		return rejection(r)
	}

	d.deps.Quota.ApplyGrants(sel.GetGrants())
	scopes := make([]node.QuotaScope, 0, len(sel.GetQuotaScopes()))
	for _, s := range sel.GetQuotaScopes() {
		scopes = append(scopes, node.QuotaScope{Dimension: s.GetDimension(), ScopeID: s.GetScopeId(), ScopeKey: s.GetScopeKey()})
	}
	if !req.WS {
		// WebSocket 连接选号不带额度（每一轮在 BeginTurn）。
		d.scopes.Store(req.APIKey.ID, scopes)
	}
	ctx := node.WithSelectionID(c.Request.Context(), a.selectionID)
	if len(scopes) > 0 {
		res, err := d.deps.Quota.Reserve(ctx, sel.GetUserId(), scopes, sel.GetQuotaNeed())
		if err != nil {
			return fail("relay local quota reservation failed", err, &handler.OpenAIRelayRejection{
				Kind: handler.OpenAIRelayRejectGateway, Gateway: handler.OpenAIBillingRejection(service.ErrBillingServiceUnavailable.WithCause(err)),
			})
		}
		a.reservation = res
	}

	account, parent, err := d.decodeAccounts(ctx, a.selectionID, sel.GetAccount(), sel.GetCredentialParent())
	if err != nil {
		return fail("relay account credentials unavailable", err, &handler.OpenAIRelayRejection{Kind: handler.OpenAIRelayRejectUnavailable})
	}
	if parent != nil {
		ctx = service.WithCredentialParent(ctx, parent)
	}

	c.Request = c.Request.WithContext(withAttempt(ctx, a))
	sessionHash := sel.GetSessionHash()
	if sessionHash == "" {
		sessionHash = req.SessionHash
	}
	return handler.OpenAIRelaySelectResult{Attempt: &handler.OpenAIRelayAttempt{
		Account:     account,
		SessionHash: sessionHash,
		ChannelMapping: service.ChannelMappingResult{
			MappedModel: sel.GetChannelMappedModel(), ChannelID: sel.GetChannelId(), Mapped: sel.GetChannelMapped(),
			BillingModelSource: sel.GetBillingModelSource(),
		},
		ForwardModel:       sel.GetForwardModel(),
		MaxAccountSwitches: int(sel.GetMaxAccountSwitches()),
		StickyPreviousHit:  sel.GetStickyPreviousHit(),
		State:              a,
	}}
}

// AttemptDone 这次尝试的转发结束（handler.OpenAIRelayDispatcher）。释放在下一次选号前或请求结束时发出：
// 那时这次转发产生的 response id、用量是否已入队都已知道。
func (d *Dispatcher) AttemptDone(_ *gin.Context, attempt *handler.OpenAIRelayAttempt) {
	if a, ok := attempt.State.(*attemptState); ok {
		a.mu.Lock()
		a.finished = true
		a.mu.Unlock()
	}
}

// RequestDone 请求结束（handler.OpenAIRelayDispatcher）。
func (d *Dispatcher) RequestDone(c *gin.Context) {
	d.flush(stateOf(c), true)
}

// flush 放掉当前的尝试：没入队用量的预扣全部退回，释放带上 response id 和凭证。
func (d *Dispatcher) flush(st *requestState, requestDone bool) {
	st.mu.Lock()
	a := st.current
	st.current = nil
	st.mu.Unlock()
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.released {
		a.mu.Unlock()
		return
	}
	a.released = true
	ids := append([]string(nil), a.responseIDs...)
	submitted := a.usageSubmitted
	cyberDone := a.cyberDone
	a.mu.Unlock()
	if !submitted && a.reservation != nil {
		a.reservation.Cancel()
	}
	rel := &relayv1.SelectionRelease{SelectionId: a.selectionID, RequestDone: requestDone, ResponseIds: ids, Voucher: a.voucher}
	if cyberDone != nil {
		// cyber 命中报告还没发完：等它（最长 node.CyberPolicyTimeout）再释放，否则主节点查不到这次选号。
		go func() {
			<-cyberDone
			d.deps.Select.Release(rel)
		}()
		return
	}
	d.deps.Select.Release(rel)
}

// SubmitUsage 把转发结果写进本地扣费队列（handler.OpenAIRelayDispatcher）。
func (d *Dispatcher) SubmitUsage(c *gin.Context, attempt *handler.OpenAIRelayAttempt, facts handler.OpenAIUsageFacts, result *service.OpenAIForwardResult) {
	d.submitUsage(c, attempt, facts, result, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI)
}

func (d *Dispatcher) submitUsage(c *gin.Context, attempt *handler.OpenAIRelayAttempt, facts handler.OpenAIUsageFacts, result *service.OpenAIForwardResult, kind relayv1.UsageRecordKind) {
	a, ok := attempt.State.(*attemptState)
	if !ok || result == nil {
		return
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		slog.Error("relay usage record: encode forward result", "error", err)
		return
	}
	ctx := context.WithoutCancel(c.Request.Context())
	rec := &relayv1.UsageRecord{
		Voucher: a.voucher, Kind: kind, ResultJson: resultJSON,
		InboundEndpoint: facts.InboundEndpoint, UpstreamEndpoint: facts.UpstreamEndpoint, UserAgent: facts.UserAgent,
		IpAddress: facts.IPAddress, SessionId: facts.SessionID, RequestPayloadHash: facts.RequestPayloadHash,
		CyberBlocked: facts.CyberBlocked, NativeCompactionV2: facts.NativeCompactionV2,
	}
	rec.ClientRequestId, _ = ctx.Value(ctxkey.ClientRequestID).(string)
	rec.RequestId, _ = ctx.Value(ctxkey.RequestID).(string)
	seq, err := d.deps.WAL.Append(ctx, rec)
	if err != nil {
		// 队列写不进去（磁盘故障、积压超限）：这笔记不上，节点应已不健康、停止接新请求（设计 3.1）。
		slog.Error("relay usage record could not be queued", "selection_id", a.selectionID, "error", err)
		return
	}
	a.mu.Lock()
	a.usageSubmitted = true
	a.mu.Unlock()
	if a.reservation != nil {
		d.pending.Store(seq, a.reservation)
	}
	if d.deps.Kick != nil {
		d.deps.Kick()
	}
}

// OnUsageResult 是扣费发送的结果回调（node.UsageSenderOptions.OnResult）：按主节点确认的消耗结算预扣。
// 重启后重放的记录没有预扣（本地额度已随重启重置），跳过。
func (d *Dispatcher) OnUsageResult(rec *relayv1.UsageRecord, res *relayv1.UsageRecordResult) {
	v, ok := d.pending.LoadAndDelete(rec.GetSeq())
	if !ok {
		return
	}
	r, ok := v.(*node.Reservation)
	if !ok {
		return
	}
	switch res.GetStatus() {
	case relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_ALREADY_SETTLED:
		r.SettleConsumed(res.GetConsumed())
	default:
		r.Cancel()
	}
}

// HandOff 交给主节点转发（handler.OpenAIRelayDispatcher）。
func (d *Dispatcher) HandOff(c *gin.Context) {
	st := stateOf(c)
	st.mu.Lock()
	st.handedOff = true
	st.mu.Unlock()
	if d.deps.HandOff == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "Service temporarily unavailable"}})
		return
	}
	d.deps.HandOff(c, st.rawBody)
}

func rejection(r *handler.OpenAIRelayRejection) handler.OpenAIRelaySelectResult {
	return handler.OpenAIRelaySelectResult{Rejection: r}
}

// cyberPolicyWait 是处理函数等 cyber 命中报告的最长时间：单机同步写会话屏蔽标记的上限（500ms），
// 超过后报告在后台继续发，释放等它发完。
const cyberPolicyWait = 500 * time.Millisecond

// RecordCyberPolicy 上游 cyber 策略命中（handler.OpenAIRelayDispatcher，设计 3.4）：会话屏蔽标记由主节点写；
// 转发返回错误时用量行写本地扣费队列（主节点按 RecordCyberPolicyUsageLog 的口径入账）。风控记录由处理函数写本机。
func (d *Dispatcher) RecordCyberPolicy(c *gin.Context, attempt *handler.OpenAIRelayAttempt, hit handler.CyberPolicyHit, usage *handler.OpenAIRelayCyberUsage) {
	a, ok := attempt.State.(*attemptState)
	if !ok {
		return
	}
	if usage != nil {
		d.submitUsage(c, attempt, usage.Facts, usage.Result, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI_CYBER_POLICY)
	}
	req := &relayv1.CyberPolicyHitRequest{
		SelectionId: a.selectionID, AccountId: attempt.Account.ID,
		Message: hit.Mark.Message, Body: hit.Mark.Body, UpstreamStatus: int32(hit.Mark.UpstreamStatus),
		UpstreamInputTokens: int64(hit.Mark.UpstreamInTok), UpstreamOutputTokens: int64(hit.Mark.UpstreamOutTok),
		Model: hit.Model, Stream: hit.Stream, Platform: hit.Platform, RequestPath: hit.RequestPath,
		InboundEndpoint: hit.InboundEndpoint, UserAgent: hit.UserAgent, ClientIp: hit.ClientIP,
		RequestId: hit.RequestID, ClientRequestId: hit.ClientRequestID, CreatedAtUnixMs: hit.CreatedAt.UnixMilli(),
	}
	done := make(chan struct{})
	a.mu.Lock()
	if a.released || a.cyberDone != nil {
		a.mu.Unlock()
		return
	}
	a.cyberDone = done
	a.mu.Unlock()
	ctx := context.WithoutCancel(c.Request.Context())
	go func() {
		defer close(done)
		if err := d.deps.Select.CyberPolicyHit(ctx, req); err != nil {
			// 主节点不可达：这次的屏蔽标记丢失（风控记录在本机、用量行在扣费队列里，都不丢）。
			slog.Warn("relay cyber policy hit could not be reported", "selection_id", a.selectionID, "error", err)
		}
	}()
	timer := time.NewTimer(cyberPolicyWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

// convertRejection 把主节点的拒绝转成处理函数的写法。
func convertRejection(r *relayv1.SelectRejection) *handler.OpenAIRelayRejection {
	out := convertRejectionKind(r)
	out.AutoGroupFailover = r.GetAutoGroupFailover()
	return out
}

func convertRejectionKind(r *relayv1.SelectRejection) *handler.OpenAIRelayRejection {
	switch r.GetFormat() {
	case relayv1.RejectionFormat_REJECTION_FORMAT_GATEWAY:
		return &handler.OpenAIRelayRejection{Kind: handler.OpenAIRelayRejectGateway, CyberBlockKey: r.GetCyberBlockKey(), Gateway: gatewayOf(r)}
	case relayv1.RejectionFormat_REJECTION_FORMAT_RAW:
		return &handler.OpenAIRelayRejection{Kind: handler.OpenAIRelayRejectRaw, Raw: capturedRejection(r)}
	case relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED:
		return &handler.OpenAIRelayRejection{Kind: handler.OpenAIRelayRejectFailoverExhausted, ContinuationUnsupported: r.GetContinuationUnsupported()}
	case relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED:
		return &handler.OpenAIRelayRejection{Kind: handler.OpenAIRelayRejectUnsupported}
	case relayv1.RejectionFormat_REJECTION_FORMAT_WS_CLOSE:
		return &handler.OpenAIRelayRejection{
			Kind: handler.OpenAIRelayRejectWSClose, WSCloseStatus: int(r.GetStatus()), WSCloseReason: r.GetMessage(), CyberBlockKey: r.GetCyberBlockKey(),
		}
	default:
		return &handler.OpenAIRelayRejection{Kind: handler.OpenAIRelayRejectUnavailable}
	}
}

// gatewayOf 是 GATEWAY 格式拒绝的内容。
func gatewayOf(r *relayv1.SelectRejection) handler.OpenAIGatewayRejection {
	return handler.OpenAIGatewayRejection{
		Status: int(r.GetStatus()), ErrType: r.GetErrorType(), Code: r.GetCode(), Message: r.GetMessage(),
		RetryAfter: int(r.GetRetryAfterSeconds()), RoutingCapacityLimited: r.GetRoutingCapacityLimited(),
		OpsBusinessLimitedReason: r.GetOpsBusinessLimitedReason(), Anthropic: r.GetAnthropicFormat(),
	}
}

func capturedRejection(r *relayv1.SelectRejection) *middleware2.CapturedRejection {
	h := http.Header{}
	for k, v := range r.GetHeaders() {
		h.Set(k, v)
	}
	return &middleware2.CapturedRejection{
		Status: int(r.GetStatus()), Header: h, Body: r.GetBody(),
		IngressReason: r.GetIngressRejectReason(), OpsReason: r.GetOpsBusinessLimitedReason(),
	}
}

// ---- 响应归属收集 ----

type attemptKey struct{}

func withAttempt(ctx context.Context, a *attemptState) context.Context {
	return context.WithValue(ctx, attemptKey{}, a)
}

func attemptFrom(ctx context.Context) *attemptState {
	if ctx == nil {
		return nil
	}
	a, _ := ctx.Value(attemptKey{}).(*attemptState)
	return a
}

// decodeAccounts 解开选中的账号（和影子账号的母账号）。本机缓存里没有这个凭据版本（刚重启、被清掉）时按这次选号取一次。
func (d *Dispatcher) decodeAccounts(ctx context.Context, selectionID string, snap, parentSnap *relayv1.AccountSnapshot) (*service.Account, *service.Account, error) {
	decode := func(s *relayv1.AccountSnapshot) (*service.Account, error) {
		if s == nil {
			return nil, nil
		}
		return accountcodec.Decode(s, d.deps.NodeID(), d.deps.Open, d.deps.Secrets)
	}
	account, err := decode(snap)
	var parent *service.Account
	if err == nil {
		parent, err = decode(parentSnap)
	}
	if !errors.Is(err, accountcodec.ErrSecretsMissing) {
		return account, parent, err
	}
	creds, err := d.deps.Select.FetchCredentials(ctx, selectionID)
	if err != nil {
		return nil, nil, err
	}
	withSecrets := func(s, from *relayv1.AccountSnapshot) *relayv1.AccountSnapshot {
		if s == nil {
			return nil
		}
		cp, _ := proto.Clone(s).(*relayv1.AccountSnapshot)
		cp.CredentialVersion, cp.SealedCredentials = from.GetCredentialVersion(), from.GetSealedCredentials()
		return cp
	}
	if account, err = decode(withSecrets(snap, creds.GetAccount())); err != nil {
		return nil, nil, err
	}
	if parent, err = decode(withSecrets(parentSnap, creds.GetCredentialParent())); err != nil {
		return nil, nil, err
	}
	return account, parent, nil
}
