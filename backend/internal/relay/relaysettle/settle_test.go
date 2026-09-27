package relaysettle

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// ---- 记录下入账写了什么的假仓储 ----

type usageLogs struct {
	service.UsageLogRepository
	mu   sync.Mutex
	logs []*service.UsageLog
}

func (r *usageLogs) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := *log
	r.logs = append(r.logs, &copied)
	return true, nil
}

type billing struct {
	service.UsageBillingRepository
	mu       sync.Mutex
	commands []service.UsageBillingCommand
	settled  map[string][]service.RelayLeaseConsumption
	fail     error
	reviews  []string
}

func (b *billing) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail != nil {
		return nil, b.fail
	}
	if r := cmd.Relay; r != nil {
		b.reviews = append(b.reviews, r.ReviewStatus)
		if prior, ok := b.settled[r.VoucherID]; ok {
			r.Handled, r.AlreadySettled, r.Consumed = true, true, prior
			return &service.UsageBillingApplyResult{Applied: false}, nil
		}
		r.Consumed = []service.RelayLeaseConsumption{{LeaseID: 9, Dimension: service.QuotaDimBalance, Amount: int64(cmd.BalanceCost*1e8 + 0.5)}}
		b.settled[r.VoucherID] = r.Consumed
		r.Handled = true
	}
	copied := *cmd
	copied.Relay = nil
	b.commands = append(b.commands, copied)
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

func (b *billing) RecordRelayVoucher(_ context.Context, r *service.RelaySettlement) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if prior, ok := b.settled[r.VoucherID]; ok {
		r.AlreadySettled, r.Consumed = true, prior
	} else {
		b.settled[r.VoucherID] = nil
	}
	r.Handled = true
	return nil
}

type keys struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (k keys) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	if k.key.ID != id {
		return nil, service.ErrAPIKeyNotFound
	}
	copied := *k.key
	return &copied, nil
}

type accounts map[int64]service.Account

func (a accounts) GetByID(_ context.Context, id int64) (*service.Account, error) {
	acc, ok := a[id]
	if !ok {
		return nil, errors.New("no account")
	}
	return &acc, nil
}

type groups map[int64]*service.Group

func (g groups) GetByID(_ context.Context, id int64) (*service.Group, error) {
	if gr, ok := g[id]; ok {
		return gr, nil
	}
	return nil, errors.New("no group")
}

type subs struct{}

func (subs) GetByID(context.Context, int64) (*service.UserSubscription, error) {
	return nil, errors.New("no subscription")
}

// ---- 环境 ----

type world struct {
	logs     *usageLogs
	billing  *billing
	gateway  *service.OpenAIGatewayService
	key      *service.APIKey
	account  service.Account
	signer   *sign.Signer
	settler  *Settler
	refreshs []int64
}

const node = int64(21)

func newWorld(t *testing.T) *world {
	t.Helper()
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	group := &service.Group{ID: 5, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, RateMultiplier: 1.5, SubscriptionType: service.SubscriptionTypeStandard}
	groupID := group.ID
	key := &service.APIKey{ID: 11, Key: "sk-a", UserID: 3, GroupID: &groupID, Group: group, Status: service.StatusActive,
		User: &service.User{ID: 3, Status: service.StatusActive, Balance: 10}}
	account := service.Account{ID: 7, Name: "one", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive}
	w := &world{logs: &usageLogs{}, billing: &billing{settled: map[string][]service.RelayLeaseConsumption{}}, key: key, account: account}
	w.gateway = service.NewOpenAIGatewayService(nil, w.logs, w.billing, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, &service.BillingCacheService{}, nil, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil)

	kek := make([]byte, keystore.KEKLength)
	_, err := rand.Read(kek)
	require.NoError(t, err)
	store, err := keystore.Open(t.TempDir(), kek)
	require.NoError(t, err)
	_, err = store.EnsureActive(keystore.PurposeVoucher)
	require.NoError(t, err)
	ring, err := store.Ring(keystore.PurposeVoucher)
	require.NoError(t, err)
	w.signer, err = sign.NewSigner(ring.Active)
	require.NoError(t, err)
	pub, _, err := sign.PublicKeysFromRing(ring)
	require.NoError(t, err)

	w.settler = New(Deps{
		Gateway: w.gateway, APIKeys: service.NewAPIKeyService(keys{key: key}, nil, nil, nil, nil, nil, cfg),
		Accounts: accounts{account.ID: account}, Groups: groups{group.ID: group}, Subscriptions: subs{}, Vouchers: w.billing,
		VerifyVoucher: func(raw []byte, reportingNodeID int64) (*relayv1.Voucher, error) {
			return sign.VerifyVoucher(raw, pub, reportingNodeID, time.Now())
		},
		RefreshUser: func(_ context.Context, userID int64) error { w.refreshs = append(w.refreshs, userID); return nil },
	})
	return w
}

var pricingAt = time.Date(2026, 9, 27, 3, 4, 5, 0, time.UTC)

func (w *world) voucher(t *testing.T) []byte {
	t.Helper()
	raw, _, err := sign.IssueVoucher(w.signer, &relayv1.Voucher{
		NodeId: node, SelectionId: "sel-1", UserId: 3, ApiKeyId: 11, AccountId: 7, GroupId: 5,
		BillingMode: relayv1.BillingMode_BILLING_MODE_BALANCE, RequestedModel: "gpt-5.1", AllowedBillingModels: []string{"gpt-5.1"},
		Context: &relayv1.SelectionContext{
			PricingAtUnixMs: pricingAt.UnixMilli(), QuotaPlatform: service.PlatformOpenAI,
			ChannelId: 4, ChannelMappedModel: "gpt-5.1", BillingModelSource: service.BillingModelSourceRequested,
			FallbackSourceGroupId: 20, FallbackSourceGroupName: "free", FallbackTargetGroupId: 5, FallbackTargetGroupName: "plus",
			ContributionRouteSource: service.ContributionRouteSourcePool,
		},
	}, time.Now())
	require.NoError(t, err)
	return raw
}

func forwardResult() *service.OpenAIForwardResult {
	return &service.OpenAIForwardResult{
		RequestID: "resp_parity", Model: "gpt-5.1", UpstreamModel: "gpt-5.1", Duration: 1500 * time.Millisecond,
		Usage: service.OpenAIUsage{InputTokens: 1200, OutputTokens: 340, CacheReadInputTokens: 200},
	}
}

func (w *world) record(t *testing.T, seq uint64) *relayv1.UsageRecord {
	t.Helper()
	raw, err := json.Marshal(forwardResult())
	require.NoError(t, err)
	return &relayv1.UsageRecord{
		Seq: seq, Voucher: w.voucher(t), Kind: relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI, ResultJson: raw,
		InboundEndpoint: "/v1/responses", UpstreamEndpoint: "/v1/responses", UserAgent: "codex/1.0", IpAddress: "5.6.7.8",
		SessionId: "sess-1", RequestPayloadHash: "hash-1", ClientRequestId: "creq-1", RequestId: "req-1",
	}
}

// 单机的入账：处理函数构造的输入 + worker 上的 ctx（请求 ID、兜底事实）。
func (w *world) settleLocally(t *testing.T) {
	t.Helper()
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "creq-1")
	ctx = context.WithValue(ctx, ctxkey.RequestID, "req-1")
	ctx = service.WithFallbackPoolTrace(ctx, service.FallbackPoolTrace{SourceGroupID: 20, SourceGroupName: "free", TargetGroupID: 5, TargetGroupName: "plus"})
	account := w.account
	account.ContributionRouteSource = service.ContributionRouteSourcePool
	mapping := service.ChannelMappingResult{ChannelID: 4, MappedModel: "gpt-5.1", BillingModelSource: service.BillingModelSourceRequested}
	result := forwardResult()
	require.NoError(t, w.gateway.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
		Result: result, APIKey: w.key, User: w.key.User, Account: &account,
		InboundEndpoint: "/v1/responses", UpstreamEndpoint: "/v1/responses", UserAgent: "codex/1.0", IPAddress: "5.6.7.8",
		SessionID: "sess-1", RequestPayloadHash: "hash-1", QuotaPlatform: service.PlatformOpenAI, PricingAt: pricingAt,
		ChannelUsageFields: mapping.ToUsageFields("gpt-5.1", result.UpstreamModel),
	}))
}

func comparableLog(l *service.UsageLog) service.UsageLog {
	c := *l
	c.ID, c.CreatedAt = 0, time.Time{}
	c.User, c.APIKey, c.Account, c.Group, c.Subscription = nil, nil, nil, nil, nil
	return c
}

// 主从入账与单机入账写出同样的扣费命令和同样的用量记录（设计 5.2 的验收方式）。
func TestRelaySettlementMatchesLocalBilling(t *testing.T) {
	w := newWorld(t)
	w.settleLocally(t)
	require.Len(t, w.billing.commands, 1)
	require.Len(t, w.logs.logs, 1)

	res := w.settler.Settle(context.Background(), node, w.record(t, 1))
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, res.GetStatus(), res.GetReason())
	require.Len(t, w.billing.commands, 2)
	require.Len(t, w.logs.logs, 2)

	require.Equal(t, w.billing.commands[0], w.billing.commands[1], "same billing command")
	require.Greater(t, w.billing.commands[1].BalanceCost, 0.0)
	require.Nil(t, w.logs.logs[0].NodeID)
	require.Equal(t, node, *w.logs.logs[1].NodeID, "the relayed usage log records which node reported it")
	relayed := *w.logs.logs[1]
	relayed.NodeID = nil
	require.Equal(t, comparableLog(w.logs.logs[0]), comparableLog(&relayed), "same usage log apart from the node")
	require.True(t, w.logs.logs[1].FallbackPoolUsed)

	require.Len(t, res.GetConsumed(), 1)
	require.Equal(t, int64(9), res.GetConsumed()[0].GetLeaseId())
	require.Equal(t, []int64{3}, w.refreshs, "reserved balance is refreshed after consuming leases")
}

// 重发同一条记录：不再扣费，返回当时的消耗；凭证验不过、格式错误的隔离；主节点故障让从节点重发。
func TestRelaySettlementReplayRejectAndRetry(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	rec := w.record(t, 1)
	first := w.settler.Settle(ctx, node, rec)
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, first.GetStatus())
	again := w.settler.Settle(ctx, node, rec)
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_ALREADY_SETTLED, again.GetStatus())
	require.Equal(t, first.GetConsumed()[0].GetAmount(), again.GetConsumed()[0].GetAmount(), "the ack is replayed with the same numbers")
	require.Len(t, w.billing.commands, 1, "charged once")

	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED, w.settler.Settle(ctx, node+1, w.record(t, 2)).GetStatus(), "another node's voucher")
	bad := w.record(t, 3)
	bad.ResultJson = []byte("{not json")
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED, w.settler.Settle(ctx, node, bad).GetStatus())
	forged := w.record(t, 4)
	forged.Voucher = append([]byte(nil), forged.Voucher...)
	forged.Voucher[len(forged.Voucher)-1] ^= 0xff
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED, w.settler.Settle(ctx, node, forged).GetStatus())

	// 扣费出错：与单机一样这一笔不补扣，凭证记成零消耗（重发不会再入账）。
	w.billing.fail = errors.New("database is down")
	failed := w.record(t, 5)
	res := w.settler.Settle(ctx, node, failed)
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, res.GetStatus())
	require.Empty(t, res.GetConsumed())
	w.billing.fail = nil
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_ALREADY_SETTLED, w.settler.Settle(ctx, node, failed).GetStatus())
}

// 待复核：上报的模型不在凭证允许的范围内（照上报的计费、报警）；节点因怀疑被攻破吊销过、凭证签发在那之前。
func TestRelaySettlementMarksRecordsForReview(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, w.settler.Settle(ctx, node, w.record(t, 1)).GetStatus())

	cheap := w.record(t, 2)
	result := forwardResult()
	result.UpstreamModel = "gpt-4o-mini"
	cheap.ResultJson, _ = json.Marshal(result)
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, w.settler.Settle(ctx, node, cheap).GetStatus(), "billed as reported")

	var revokedAt time.Time
	w.settler.deps.LastSuspectRevocation = func(context.Context, int64) (time.Time, bool, error) { return revokedAt, true, nil }
	revokedAt = time.Now().Add(time.Minute)
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, w.settler.Settle(ctx, node, w.record(t, 3)).GetStatus())
	w.settler.suspects = map[int64]suspectRevocation{}
	revokedAt = time.Now().Add(-time.Hour)
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, w.settler.Settle(ctx, node, w.record(t, 4)).GetStatus())

	require.Equal(t, []string{"", "pending_review", "pending_review", ""}, w.billing.reviews,
		"reviewed: a model outside the voucher, and a voucher issued before the suspected compromise; not: one issued after it")
}
