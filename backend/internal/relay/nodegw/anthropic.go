package nodegw

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// AnthropicDeps 是从节点 Anthropic Messages 处理函数额外用到的部件。
type AnthropicDeps struct {
	// AccountState 是转发路径上的账号状态判定（node.RemoteAccountState：限流、流超时、错误策略同步问主节点）。
	AccountState service.AccountStateDecider
	// TempUnschedulable 把转发路径上直接写账号仓储的临时不可调度交给主节点照写（账号事件）。
	TempUnschedulable func(accountID int64, until time.Time, reason string)
	// MaskedSession 把转发时用到的伪装会话 ID 交给主节点写入、续期（账号事件）。
	MaskedSession func(accountID int64, sessionID string)
	// Reporter 发 Antigravity 转发路径上的账号状态事件（模型级限流、账号级限流、积分耗尽标记、INTERNAL 500 惩罚）；
	// nil 时从节点不建 Antigravity 转发服务（混合调度进 Anthropic 分组的 Antigravity 账号主节点不会交给它）。
	Reporter *node.RemoteAccountReporter
}

type relayIdentityKey struct{}

// relayIdentity 是主节点选号时给这次尝试的账号定下的指纹和伪装会话 ID。
type relayIdentity struct {
	accountID       int64
	fingerprint     *service.Fingerprint
	maskedSessionID string
}

func withRelayIdentity(ctx context.Context, id relayIdentity) context.Context {
	return context.WithValue(ctx, relayIdentityKey{}, id)
}

// errNoRelayFingerprint：主节点没给这个账号的指纹（不是 OAuth 账号不会问到）。
var errNoRelayFingerprint = errors.New("relay: the master did not provide a fingerprint for this account")

// relayIdentityCache 是从节点上的身份缓存（service.IdentityCache）：指纹和伪装会话 ID 在主节点，这里读主节点
// 选号时给的；指纹的写入主节点已经做过（同一段 GetOrCreateFingerprint），伪装会话 ID 用到时报回主节点。
type relayIdentityCache struct {
	maskedSession func(accountID int64, sessionID string)
}

func (relayIdentityCache) GetFingerprint(ctx context.Context, accountID int64) (*service.Fingerprint, error) {
	if id, ok := ctx.Value(relayIdentityKey{}).(relayIdentity); ok && id.accountID == accountID && id.fingerprint != nil {
		fp := *id.fingerprint
		return &fp, nil
	}
	return nil, errNoRelayFingerprint
}

func (relayIdentityCache) SetFingerprint(context.Context, int64, *service.Fingerprint) error {
	return nil
}

func (relayIdentityCache) GetMaskedSessionID(ctx context.Context, accountID int64) (string, error) {
	if id, ok := ctx.Value(relayIdentityKey{}).(relayIdentity); ok && id.accountID == accountID {
		return id.maskedSessionID, nil
	}
	return "", nil
}

func (c relayIdentityCache) SetMaskedSessionID(_ context.Context, accountID int64, sessionID string) error {
	if c.maskedSession != nil {
		c.maskedSession(accountID, sessionID)
	}
	return nil
}

// relayAccountRepo 是从节点上网关服务的账号仓储：转发路径只写几类账号状态（临时不可调度、Antigravity 的模型级 / 账号级
// 限流和积分耗尽标记），作为账号事件交给主节点照写。其余方法没有实现（从节点不连库），转发路径不该调到
// （源码守卫 TestAnthropicForwardPathOnlyTempUnschedulesAccounts 限制转发文件只能用这些）。
type relayAccountRepo struct {
	service.AccountRepository
	tempUnschedulable func(accountID int64, until time.Time, reason string)
	reporter          *node.RemoteAccountReporter
}

func (r relayAccountRepo) SetModelRateLimit(_ context.Context, id int64, modelKey string, resetAt time.Time, _ ...string) error {
	if r.reporter != nil {
		r.reporter.ModelRateLimit(id, modelKey, resetAt)
	}
	return nil
}

func (r relayAccountRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	if r.reporter != nil {
		r.reporter.RateLimited(id, resetAt)
	}
	return nil
}

// UpdateExtra 只支持清除积分耗尽标记后写回模型级限流表（Antigravity clearCreditsExhausted）。
func (r relayAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	limits, ok := updates[service.ModelRateLimitsExtraKey]
	if !ok || len(updates) != 1 {
		return errors.New("relay: only the model rate limit table can be updated on the forward path")
	}
	raw, err := json.Marshal(limits)
	if err != nil {
		return err
	}
	if r.reporter != nil {
		r.reporter.ModelRateLimitsExtra(id, raw)
	}
	return nil
}

// relayInternal500 是从节点上 Antigravity 的 INTERNAL 500 计数（service.Internal500CounterCache）：计数和惩罚在主节点
// （跨节点累计）。重试耗尽时发事件、本地不惩罚（返回 0 轮）；成功清零很频繁，只在这台节点自己报过耗尽、或者距上次
// 清零超过一分钟时才发，其他节点报的计数最多晚一分钟清掉（与本地每次成功都清零的差别，已记在总账）。
type relayInternal500 struct {
	reporter *node.RemoteAccountReporter
	now      func() time.Time
	mu       sync.Mutex
	dirty    map[int64]bool
	lastSent map[int64]time.Time
}

func newRelayInternal500(reporter *node.RemoteAccountReporter) *relayInternal500 {
	return &relayInternal500{reporter: reporter, now: time.Now, dirty: map[int64]bool{}, lastSent: map[int64]time.Time{}}
}

func (c *relayInternal500) IncrementInternal500Count(_ context.Context, accountID int64) (int64, error) {
	c.mu.Lock()
	c.dirty[accountID] = true
	c.mu.Unlock()
	c.reporter.Internal500(accountID, false)
	return 0, nil
}

func (c *relayInternal500) ResetInternal500Count(_ context.Context, accountID int64) error {
	c.mu.Lock()
	now := c.now()
	send := c.dirty[accountID] || now.Sub(c.lastSent[accountID]) >= time.Minute
	if send {
		delete(c.dirty, accountID)
		c.lastSent[accountID] = now
	}
	c.mu.Unlock()
	if send {
		c.reporter.Internal500(accountID, true)
	}
	return nil
}

func (r relayAccountRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	if r.tempUnschedulable != nil {
		r.tempUnschedulable(id, until, reason)
	}
	return nil
}

// NewAnthropicHandler 组装从节点上的 Messages 处理函数（Anthropic 分组）：与单机同一个处理函数和转发服务，
// 换上远程选号与记账（Dispatcher）、远程账号状态判定、只写临时不可调度的账号仓储。仓储、调度、并发、计费一律
// 不给（这些在主节点）。
func NewAnthropicHandler(d GatewayDeps, a AnthropicDeps) *handler.GatewayHandler {
	tlsProfiles := d.TLSProfiles
	if tlsProfiles == nil {
		// 模板服务不能是 nil：账号开了 TLS 指纹伪装时转发要按它解析（没有模板时用内置默认）。
		tlsProfiles = service.NewStaticTLSFingerprintProfileService(nil)
	}
	repo := relayAccountRepo{tempUnschedulable: a.TempUnschedulable, reporter: a.Reporter}
	gw := service.NewGatewayService(repo, nil, nil, nil, nil, nil, nil,
		NoopGatewayCache{}, d.Config, nil, nil, nil, nil, nil, service.NewIdentityService(relayIdentityCache{maskedSession: a.MaskedSession}), d.HTTPUpstream, nil, nil, nil, nil,
		service.NewDigestSessionStore(), d.Settings, tlsProfiles, nil, nil, nil, nil, nil)
	gw.SetAccountStateDecider(a.AccountState)
	var moderation *service.ContentModerationService
	if d.Moderation != nil {
		moderation = d.Moderation.Service
	}
	// 用户消息串行队列：与本地同一段排队代码（含排队期间的 SSE 保活），锁和计数每一步问主节点。
	var userMsgQueue *service.UserMessageQueueService
	if d.Dispatcher != nil && d.Dispatcher.deps.Select != nil && d.Config != nil {
		call := d.Dispatcher.deps.Select.UserMsgQueue
		userMsgQueue = service.NewUserMessageQueueService(remoteUserMsgQueue{call: call}, remoteRPM{call: call}, &d.Config.Gateway.UserMessageQueue)
	}
	// Antigravity 账号（混合调度进 Anthropic 分组的）：与单机同一个转发服务；Google token 由主节点随凭据下发
	// （这里的 token 提供者只读下发的 access_token），账号状态写入经 relayAccountRepo 交主节点。
	// Gemini 原生入口（GeminiV1BetaModels）：Gemini 转发服务只拿转发要用的（上游 HTTP、Gemini token 提供者读下发的 access_token）；
	// 429 的账号级限流、清粘性会话绑定是账号事件，档位冷却的时长由主节点算。
	var antigravity *service.AntigravityGatewayService
	var geminiCompat *service.GeminiMessagesCompatService
	if a.Reporter != nil {
		antigravity = service.NewAntigravityGatewayService(repo, relayStickyCache{reporter: a.Reporter}, nil,
			service.NewAntigravityTokenProvider(nil, nil, nil), nil, d.HTTPUpstream, d.Settings, newRelayInternal500(a.Reporter))
		antigravity.SetAccountStateDecider(a.AccountState)
		geminiCompat = service.NewGeminiMessagesCompatService(repo, nil, NoopGatewayCache{}, nil,
			service.NewGeminiTokenProvider(nil, nil, nil), nil, d.HTTPUpstream, antigravity, d.Config)
		geminiCompat.SetAccountStateDecider(a.AccountState)
		geminiCompat.SetCooldownReporter(a.Reporter)
	}
	h := handler.NewGatewayHandler(gw, nil, geminiCompat, antigravity, nil, nil, nil, nil, nil, nil, d.ErrorPassthrough, moderation, userMsgQueue, d.Config, d.Settings)
	h.SetRelayDispatcher(d.Dispatcher)
	if d.Moderation != nil {
		// 安全审计在从节点本地判定（设计 3.4），与单机同一个协调器。
		h.SetSecurityAuditCoordinator(d.Moderation.Coordinator)
	}
	return h
}
