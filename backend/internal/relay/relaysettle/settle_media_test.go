package relaysettle

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// videoCache 是主节点上的视频任务状态（真实现在 Redis）：会话绑定、待计费快照、一次性认领。
type videoCache struct {
	service.GatewayCache
	mu      sync.Mutex
	bound   map[string]int64
	pending map[string][]byte
	claimed map[string]bool
}

func newVideoCache() *videoCache {
	return &videoCache{bound: map[string]int64{}, pending: map[string][]byte{}, claimed: map[string]bool{}}
}

func (c *videoCache) SetSessionAccountID(_ context.Context, _ int64, key string, id int64, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bound[key] = id
	return nil
}

func (c *videoCache) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.bound[key]; ok {
		return id, nil
	}
	return 0, service.ErrStickySessionNotFound
}

func (c *videoCache) SetGrokVideoPendingBilling(_ context.Context, key string, payload []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[key] = payload
	return nil
}

func (c *videoCache) GetGrokVideoPendingBilling(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending[key], nil
}

func (c *videoCache) ClaimGrokVideoBilled(_ context.Context, key string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.claimed[key] {
		return false, nil
	}
	c.claimed[key] = true
	return true, nil
}

func (c *videoCache) ReleaseGrokVideoBilled(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.claimed, key)
	return nil
}

// newMediaWorld 是带视频任务状态的入账环境。
func newMediaWorld(t *testing.T) (*world, *videoCache) {
	t.Helper()
	w := newWorld(t)
	cache := newVideoCache()
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	w.gateway = service.NewOpenAIGatewayService(nil, w.logs, w.billing, nil, nil, nil, cache, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, &service.BillingCacheService{}, nil, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil)
	w.settler.deps.Gateway = w.gateway
	return w, cache
}

func (w *world) mediaVoucher(t *testing.T, model string, allowed ...string) []byte {
	t.Helper()
	raw, _, err := sign.IssueVoucher(w.signer, &relayv1.Voucher{
		NodeId: node, SelectionId: "sel-m", UserId: 3, ApiKeyId: 11, AccountId: 7, GroupId: 5,
		BillingMode: relayv1.BillingMode_BILLING_MODE_BALANCE, RequestedModel: model, AllowedBillingModels: allowed,
		Context: &relayv1.SelectionContext{QuotaPlatform: service.PlatformOpenAI, MediaChannelUsageFields: true},
	}, time.Now())
	require.NoError(t, err)
	return raw
}

const videoTask = "vid_task_1"

func videoPending() service.GrokVideoPendingBilling {
	return service.GrokVideoPendingBilling{
		Model: "grok-imagine-video", BillingModel: "grok-imagine-video", UpstreamModel: "grok-imagine-video",
		VideoResolution: "720p", VideoDurationSeconds: 6, OriginalModel: "grok-imagine-video",
		CreatedAt: time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339Nano),
	}
}

func videoStatusResult() *service.OpenAIForwardResult {
	return &service.OpenAIForwardResult{
		RequestID: "req-status", ResponseID: videoTask, Model: "grok-imagine-video", BillingModel: "grok-imagine-video",
		VideoCount: 1, VideoDurationSeconds: 6, Duration: 80 * time.Millisecond,
	}
}

func (w *world) videoTaskRecord(t *testing.T, seq uint64, pending service.GrokVideoPendingBilling) *relayv1.UsageRecord {
	t.Helper()
	raw, err := json.Marshal(pending)
	require.NoError(t, err)
	return &relayv1.UsageRecord{
		Seq: seq, Voucher: w.mediaVoucher(t, "grok-imagine-video", "grok-imagine-video"), Kind: relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI_VIDEO_TASK,
		ResultJson: []byte("{}"), TaskId: videoTask, TaskPendingJson: raw,
	}
}

func (w *world) videoCompletionRecord(t *testing.T, seq uint64, result *service.OpenAIForwardResult) *relayv1.UsageRecord {
	t.Helper()
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	return &relayv1.UsageRecord{
		Seq: seq, Voucher: w.mediaVoucher(t, "", "grok-imagine-video"), Kind: relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI_VIDEO_COMPLETION,
		ResultJson: raw, TaskId: videoTask, InboundEndpoint: "/v1/videos/vid_task_1", UpstreamEndpoint: "/v1/videos/:id",
		UserAgent: "sdk/1", IpAddress: "5.6.7.8", RequestPayloadHash: service.HashUsageRequestPayload([]byte(videoTask)),
	}
}

// 视频任务：创建时登记（账号绑定、待计费快照），完成时按任务认领计费、只入账一次；入账与单机的 prepare + RecordUsage 一致。
func TestRelayVideoTaskRegistrationAndCompletionMatchLocalBilling(t *testing.T) {
	// 单机：创建时存快照，轮询看到完成时认领、合并、入账（本地 recordGrokMediaUsage 的输入）。
	local, _ := newMediaWorld(t)
	ctx := context.Background()
	pending := videoPending()
	require.NoError(t, local.gateway.StoreGrokVideoPendingBilling(ctx, videoTask, 3, 11, pending))
	billed := local.gateway.PrepareGrokVideoCompletionBilling(ctx, 3, 11, videoTask, videoStatusResult())
	require.NotNil(t, billed)
	account := local.account
	require.NoError(t, local.gateway.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
		Result: billed, APIKey: local.key, User: local.key.User, Account: &account,
		InboundEndpoint: "/v1/videos/vid_task_1", UpstreamEndpoint: "/v1/videos/:id", UserAgent: "sdk/1", IPAddress: "5.6.7.8",
		RequestPayloadHash: service.HashUsageRequestPayload([]byte(videoTask)), QuotaPlatform: service.PlatformOpenAI,
		ChannelUsageFields: service.ChannelUsageFields{OriginalModel: billed.Model, ChannelMappedModel: billed.Model},
	}))
	require.Len(t, local.billing.commands, 1)

	// 主从：创建记录登记任务，完成记录入账。
	w, cache := newMediaWorld(t)
	res := w.settler.Settle(ctx, node, w.videoTaskRecord(t, 1, pending))
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, res.GetStatus(), res.GetReason())
	require.Len(t, w.billing.commands, 0, "registering a task bills nothing")
	require.Len(t, cache.bound, 1, "the task is bound to the account that served it")
	for _, id := range cache.bound {
		require.Equal(t, int64(7), id)
	}
	stored, err := w.gateway.LoadGrokVideoPendingBilling(ctx, videoTask, 3, 11)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, "720p", stored.VideoResolution)

	res = w.settler.Settle(ctx, node, w.videoCompletionRecord(t, 2, videoStatusResult()))
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, res.GetStatus(), res.GetReason())
	require.Len(t, w.billing.commands, 1)
	require.Equal(t, local.billing.commands[0].BalanceCost, w.billing.commands[0].BalanceCost)
	require.Greater(t, w.billing.commands[0].BalanceCost, 0.0)
	require.Len(t, w.logs.logs, 1)
	require.Equal(t, local.logs.logs[0].RequestID, w.logs.logs[0].RequestID, "the durable task id is the billing request id")
	require.NotContains(t, w.billing.reviews, "pending_review")

	// 同一个任务再被轮询到（另一张凭证）：已认领，不再入账。
	res = w.settler.Settle(ctx, node, w.videoCompletionRecord(t, 3, videoStatusResult()))
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, res.GetStatus(), res.GetReason())
	require.Len(t, w.billing.commands, 1, "billed once per task")
}

// 入账失败时放掉认领，下一次轮询能再入账（与单机一致）。
func TestRelayVideoCompletionReleasesTheClaimWhenBillingFails(t *testing.T) {
	w, _ := newMediaWorld(t)
	ctx := context.Background()
	require.NoError(t, w.gateway.StoreGrokVideoPendingBilling(ctx, videoTask, 3, 11, videoPending()))

	w.billing.fail = errors.New("db down")
	res := w.settler.Settle(ctx, node, w.videoCompletionRecord(t, 1, videoStatusResult()))
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, res.GetStatus(), "recorded without charge like a single server would")
	require.Empty(t, w.billing.commands)

	w.billing.fail = nil
	res = w.settler.Settle(ctx, node, w.videoCompletionRecord(t, 2, videoStatusResult()))
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, res.GetStatus(), res.GetReason())
	require.Len(t, w.billing.commands, 1, "the next poll bills the task")
}

// 任务登记不进缓存（Redis 故障）时让从节点重发，而不是按"没扣费"记下；快照不是合法 JSON 的记录隔离。
func TestRelayVideoTaskRegistrationRetriesOnCacheFailure(t *testing.T) {
	w, _ := newMediaWorld(t)
	ctx := context.Background()
	rec := w.videoTaskRecord(t, 1, videoPending())
	rec.TaskPendingJson = []byte("{not json")
	res := w.settler.Settle(ctx, node, rec)
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED, res.GetStatus())

	rec = w.videoTaskRecord(t, 2, videoPending())
	rec.TaskId = ""
	res = w.settler.Settle(ctx, node, rec)
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED, res.GetStatus())

	// 没有缓存（Bind 失败）：重试。
	w.gateway = service.NewOpenAIGatewayService(nil, w.logs, w.billing, nil, nil, nil, nil, &config.Config{}, nil, nil,
		service.NewBillingService(&config.Config{}, nil), nil, &service.BillingCacheService{}, nil, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil)
	w.settler.deps.Gateway = w.gateway
	res = w.settler.Settle(ctx, node, w.videoTaskRecord(t, 3, videoPending()))
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_RETRY, res.GetStatus())
}
