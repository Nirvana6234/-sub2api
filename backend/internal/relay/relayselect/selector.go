// Package relayselect 是主节点的选号实现（设计 3.1 第 5 步、开发计划 WP7）。
//
// 它复用本地网关处理函数的选号代码（handler.OpenAIAccountAdmitter.SelectAndAdmit 等）和
// 中间件的检查（middleware.EvaluateRelayAPIKeyAdmission），所以单独成包：master 不能导入 handler
// （handler → handler/admin → master 会循环）。只被 relaywire 引用。
package relayselect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/accountcodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Deps 是选号用到的服务（relaywire 注入）。
type Deps struct {
	Config        *config.Config
	APIKeys       *service.APIKeyService
	Subscriptions *service.SubscriptionService
	Settings      *service.SettingService
	Billing       *service.BillingCacheService
	Gateway       *service.OpenAIGatewayService
	Concurrency   *service.ConcurrencyService
	// Moderation、PromptAudit 用来判断请求会不会被安全审计处理：会的请求暂时留在主节点
	// （审核接入主从通信之前，设计 3.4）。nil 表示没有这个功能。
	Moderation  *service.ContentModerationService
	PromptAudit interface{ EffectiveMode() securityaudit.Mode }
}

// holdLimit 是一次选号最长占着槽位的时间：从节点的释放消息丢了、从节点下线时由定时清理放掉。
// 与 Redis 并发槽的默认过期时间一致（repository/concurrency_cache.go），不会比单机更早放掉。
const holdLimit = 15 * time.Minute

// NewFactory 返回运行时用来新建选号实现的函数（master.RuntimeDeps.NewSelector）。
func NewFactory(d Deps) func(master.SelectEnv) master.Selector {
	return func(env master.SelectEnv) master.Selector { return newSelector(d, env) }
}

type selector struct {
	deps     Deps
	env      master.SelectEnv
	admitter handler.OpenAIAccountAdmitter
	helper   *handler.ConcurrencyHelper
	now      func() time.Time

	mu         sync.Mutex
	requests   map[requestKey]*requestRecord
	selections map[string]*selectionRecord
	// delivered：每台节点已经拿到的账号凭据版本（设计 9.1：同一版本不重复下发）。只是省流量的提示：
	// 节点缓存里没有时用 FetchCredentials 取。
	delivered map[int64]map[int64]string
	// recent：节点刚用过的账号（账号事件在放槽之后到达）。
	recent map[nodeAccount]recentUse
	events chan queuedAccountEvent
	closed bool
	// localReporter 执行账号事件（默认网关服务的本机实现；测试替换）。
	localReporter func() service.OpenAIAccountReporter

	stopReaper context.CancelFunc
}

type requestKey struct {
	nodeID    int64
	requestID string
}

// requestRecord 是一次客户端请求在主节点上按请求保留的状态（多次选号共用）。
type requestRecord struct {
	key requestKey
	// mu 串行化同一请求的多次选号。
	mu sync.Mutex

	userID   int64
	apiKeyID int64
	// userRelease 放掉用户并发槽（请求结束时）。
	userRelease func()
	// pricingCtx 带着本请求固定的计价时间和利润门（不带取消），每次选号在它上面挂上调用的取消。
	pricingCtx context.Context
	pricingAt  time.Time
	state      handler.OpenAISelectState
	excluded   map[int64]struct{}
	active     map[string]struct{}
	lastSeen   time.Time
}

// selectionRecord 是一次进行中的选号。
type selectionRecord struct {
	id        string
	nodeID    int64
	request   *requestRecord
	account   *service.Account
	release   func()
	createdAt time.Time
	// 补额度、写响应归属用。
	quota    service.QuotaRequest
	groupID  int64
	userID   int64
	apiKeyID int64
}

func newSelector(d Deps, env master.SelectEnv) *selector {
	helper := handler.NewConcurrencyHelper(d.Concurrency, "", 0)
	s := &selector{
		deps:       d,
		env:        env,
		admitter:   handler.OpenAIAccountAdmitter{Gateway: d.Gateway, Concurrency: helper},
		helper:     helper,
		now:        time.Now,
		requests:   map[requestKey]*requestRecord{},
		selections: map[string]*selectionRecord{},
		delivered:  map[int64]map[int64]string{},
		recent:     map[nodeAccount]recentUse{},
		events:     make(chan queuedAccountEvent, accountEventQueue),
	}
	s.localReporter = d.Gateway.LocalAccountReporter
	ctx, cancel := context.WithCancel(context.Background())
	s.stopReaper = cancel
	go s.runReaper(ctx)
	for i := 0; i < accountEventWorkers; i++ {
		go s.runAccountEvents(ctx)
	}
	return s
}

func newSelectionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// requestFor 取这次请求的记录；第一次选号时新建（调用方负责占用户槽等初始化，失败时 dropRequest）。
func (s *selector) requestFor(nodeID int64, requestID string) (*requestRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false, status.Error(codes.Unavailable, "relay selection is shutting down")
	}
	key := requestKey{nodeID: nodeID, requestID: requestID}
	if r, ok := s.requests[key]; ok {
		r.lastSeen = s.now()
		return r, false, nil
	}
	r := &requestRecord{key: key, excluded: map[int64]struct{}{}, active: map[string]struct{}{}, lastSeen: s.now()}
	s.requests[key] = r
	return r, true, nil
}

// dropRequest 结束一次请求：放掉用户槽和它还占着的选号。
func (s *selector) dropRequest(r *requestRecord) {
	s.mu.Lock()
	if cur, ok := s.requests[r.key]; ok && cur == r {
		delete(s.requests, r.key)
	}
	var sels []*selectionRecord
	for id := range r.active {
		if sel, ok := s.selections[id]; ok {
			delete(s.selections, id)
			s.rememberUseLocked(sel)
			sels = append(sels, sel)
		}
	}
	r.active = map[string]struct{}{}
	release := r.userRelease
	r.userRelease = nil
	s.mu.Unlock()
	for _, sel := range sels {
		if sel.release != nil {
			sel.release()
		}
	}
	if release != nil {
		release()
	}
}

func (s *selector) addSelection(sel *selectionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selections[sel.id] = sel
	sel.request.active[sel.id] = struct{}{}
}

// takeSelection 取出并删除一次选号（释放时）。节点对不上时不动。
func (s *selector) takeSelection(nodeID int64, id string) *selectionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	sel, ok := s.selections[id]
	if !ok || sel.nodeID != nodeID {
		return nil
	}
	delete(s.selections, id)
	delete(sel.request.active, id)
	s.rememberUseLocked(sel)
	return sel
}

func (s *selector) lookupSelection(nodeID int64, id string) *selectionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	sel, ok := s.selections[id]
	if !ok || sel.nodeID != nodeID {
		return nil
	}
	return sel
}

func (s *selector) nodeHas(nodeID int64) func(accountID int64, version string) bool {
	return func(accountID int64, version string) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.delivered[nodeID][accountID] == version
	}
}

func (s *selector) markDelivered(nodeID, accountID int64, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.delivered[nodeID]
	if m == nil {
		m = map[int64]string{}
		s.delivered[nodeID] = m
	}
	m[accountID] = version
}

// Release 处理释放消息：放掉账号槽，记下响应归属；请求结束时放掉用户槽。在自己的协程里做（不阻塞事件流）。
func (s *selector) Release(nodeID int64, rel *relayv1.SelectionRelease) {
	go s.release(nodeID, rel)
}

func (s *selector) release(nodeID int64, rel *relayv1.SelectionRelease) {
	sel := s.takeSelection(nodeID, rel.GetSelectionId())
	if sel == nil {
		// 已释放（重发）、不属于这台节点，或占用太久已被清理（槽早已放掉）。
		// 最后一种情况下响应归属还要记：按凭证里的值，凭证是主节点签的，节点改不了。
		s.bindFromVoucher(nodeID, rel)
		return
	}
	if sel.release != nil {
		sel.release()
	}
	if len(rel.GetResponseIds()) > 0 && sel.account != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		for _, id := range rel.GetResponseIds() {
			s.deps.Gateway.BindRelayHTTPResponse(ctx, sel.groupID, sel.account.ID, id, sel.userID, sel.apiKeyID)
		}
		cancel()
	}
	if rel.GetRequestDone() {
		s.dropRequest(sel.request)
	}
}

// FetchCredentials 给进行中的选号所选账号的上游凭据（总是加密下发）。
func (s *selector) FetchCredentials(ctx context.Context, nodeID int64, req *relayv1.FetchCredentialsRequest) (*relayv1.FetchCredentialsResponse, error) {
	sel := s.lookupSelection(nodeID, req.GetSelectionId())
	if sel == nil {
		return nil, master.ErrSelectionNotFound
	}
	snap, err := s.encodeAccount(ctx, nodeID, sel.account, nil)
	if err != nil {
		return nil, err
	}
	return &relayv1.FetchCredentialsResponse{Account: &relayv1.AccountSnapshot{
		Id: snap.GetId(), CredentialVersion: snap.GetCredentialVersion(), SealedCredentials: snap.GetSealedCredentials(),
	}}, nil
}

// encodeAccount 编码账号快照；OAuth 账号带上主节点刚取的短期 access token（refresh token 不下发，设计第 9 节）。
func (s *selector) encodeAccount(ctx context.Context, nodeID int64, account *service.Account, nodeHas func(int64, string) bool) (*relayv1.AccountSnapshot, error) {
	key, ok := s.env.NodeEncryptionKey(nodeID)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "the node has no encryption key; obtain a certificate first")
	}
	var overrides map[string]any
	if account.Type == service.AccountTypeOAuth {
		token, _, err := s.deps.Gateway.GetAccessToken(ctx, account)
		if err != nil {
			return nil, err
		}
		if token != "" {
			overrides = map[string]any{"access_token": token}
		}
	}
	snap, err := accountcodec.Encode(account, overrides, nodeID, key, nodeHas)
	if err != nil {
		return nil, err
	}
	if len(snap.GetSealedCredentials()) > 0 {
		s.markDelivered(nodeID, account.ID, snap.GetCredentialVersion())
	}
	return snap, nil
}

// RefillQuota 按进行中的选号补充额度（提前补充）。
func (s *selector) RefillQuota(ctx context.Context, nodeID int64, req *relayv1.RefillQuotaRequest) (*relayv1.RefillQuotaResponse, error) {
	sel := s.lookupSelection(nodeID, req.GetSelectionId())
	if sel == nil {
		return nil, master.ErrSelectionNotFound
	}
	need := req.GetNeed()
	if need < 1 {
		need = 1
	}
	grants, _, err := s.acquireQuota(ctx, nodeID, sel.quota, req.GetHeldQuota(), need, true)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		s.ungrant(sel.userID, nodeID, grants)
		return nil, ctx.Err()
	}
	return &relayv1.RefillQuotaResponse{Grants: grants}, nil
}

// ungrant 收回刚给出、但没送到从节点的额度：每份租约减去这次新给的金额（不动累计退回，
// 节点之后按自己知道的累计值退回不受影响）。
func (s *selector) ungrant(userID, nodeID int64, grants []*relayv1.QuotaGrant) {
	if s.env.Quotas == nil || len(grants) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, g := range grants {
		if g.GetAmount() <= 0 {
			continue
		}
		if err := s.env.Quotas.Release(ctx, nodeID, userID, g.GetLeaseId(), g.GetAmount()); err != nil {
			slog.Warn("relay: give back an undelivered quota grant failed", "lease_id", g.GetLeaseId(), "amount", g.GetAmount(), "error", err)
		}
	}
}

// Close 放掉所有还占着的槽（运行时停止时）。
func (s *selector) Close() {
	s.stopReaper()
	s.mu.Lock()
	s.closed = true
	requests := make([]*requestRecord, 0, len(s.requests))
	for _, r := range s.requests {
		requests = append(requests, r)
	}
	s.mu.Unlock()
	for _, r := range requests {
		s.dropRequest(r)
	}
}

func (s *selector) runReaper(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reap()
		}
	}
}

// reap 放掉占用超过 holdLimit 的选号和请求（释放消息丢了、从节点下线）。
func (s *selector) reap() {
	cutoff := s.now().Add(-holdLimit)
	s.mu.Lock()
	for k, r := range s.recent {
		if !s.now().Before(r.until) {
			delete(s.recent, k)
		}
	}
	var stale []*requestRecord
	for _, r := range s.requests {
		if r.lastSeen.Before(cutoff) {
			stale = append(stale, r)
		}
	}
	s.mu.Unlock()
	for _, r := range stale {
		slog.Warn("relay selection held too long, releasing", "node_id", r.key.nodeID, "request_id", r.key.requestID)
		s.dropRequest(r)
	}
}

// bindFromVoucher 按释放消息带回的凭证记响应归属（选号记录已被清理时）。
func (s *selector) bindFromVoucher(nodeID int64, rel *relayv1.SelectionRelease) {
	if len(rel.GetResponseIds()) == 0 || len(rel.GetVoucher()) == 0 || s.env.VerifyVoucher == nil {
		return
	}
	v, err := s.env.VerifyVoucher(rel.GetVoucher(), nodeID)
	if err != nil || v.GetSelectionId() != rel.GetSelectionId() {
		slog.Warn("relay: release carries an invalid voucher", "node_id", nodeID, "selection_id", rel.GetSelectionId(), "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, id := range rel.GetResponseIds() {
		s.deps.Gateway.BindRelayHTTPResponse(ctx, v.GetGroupId(), v.GetAccountId(), id, v.GetUserId(), v.GetApiKeyId())
	}
}
