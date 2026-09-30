package node

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/nodestore"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/stretchr/testify/require"
)

// 从节点的异步审计队列：状态流转与 PostgreSQL 实现相同（容量、领取顺序、租约、重试、过期回收），事件写本机。
func TestPromptAuditStoreQueue(t *testing.T) {
	ctx := context.Background()
	st, err := nodestore.Open(t.TempDir(), nodestore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	q := NewPromptAuditStore(st)
	now := time.Now()

	a, err := q.CreateStagingWithCapacity(ctx, securityaudit.PromptSnapshot{RequestID: "a", ScanText: "secret"}, 1, 2, 2)
	require.NoError(t, err)
	require.Empty(t, a.Snapshot.ScanText, "the job keeps only the redacted snapshot")
	b, err := q.CreateStagingWithCapacity(ctx, securityaudit.PromptSnapshot{RequestID: "b"}, 1, 2, 2)
	require.NoError(t, err)
	_, err = q.CreateStagingWithCapacity(ctx, securityaudit.PromptSnapshot{RequestID: "c"}, 1, 2, 2)
	require.ErrorIs(t, err, securityaudit.ErrQueueFull)

	_, ok, _ := q.ClaimNextJob(ctx, now)
	require.False(t, ok, "staging jobs are not claimable")
	require.NoError(t, q.PublishQueued(ctx, a.ID))
	require.NoError(t, q.PublishQueued(ctx, b.ID))
	first, ok, err := q.ClaimNextJob(ctx, now.Add(time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, a.ID, first.ID, "oldest first")
	require.Equal(t, 1, first.Attempts)

	// 租约：旧的领取版本不能再动它。
	require.ErrorIs(t, q.RefreshLease(ctx, first.ID, first.ClaimVersion+1, now), securityaudit.ErrLeaseLost)
	require.NoError(t, q.Retry(ctx, first.ID, first.ClaimVersion, now.Add(time.Hour), "guard_unavailable", ""))
	second, ok, _ := q.ClaimNextJob(ctx, now.Add(2*time.Second))
	require.True(t, ok)
	require.Equal(t, b.ID, second.ID, "a job waiting for its retry time is skipped")

	// 处理太久：过期回收，还有次数就改成重试。
	reclaimed, err := q.ReclaimStale(ctx, now, now.Add(time.Minute), 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), reclaimed)
	stats, _ := q.QueueStats(ctx)
	require.Equal(t, int64(2), stats.Retry)

	again, ok, _ := q.ClaimNextJob(ctx, now.Add(2*time.Hour))
	require.True(t, ok)
	event, err := q.Complete(ctx, again, &securityaudit.NormalizedResult{Decision: securityaudit.EventFlag}, false)
	require.NoError(t, err)
	require.NotNil(t, event, "risk events are always recorded")
	_, err = q.Complete(ctx, again, &securityaudit.NormalizedResult{Decision: securityaudit.EventFlag}, false)
	require.ErrorIs(t, err, securityaudit.ErrLeaseLost, "completed once")
	passEvent, err := q.RecordBlocking(ctx, securityaudit.PromptSnapshot{RequestID: "p"}, 1, &securityaudit.NormalizedResult{Decision: securityaudit.EventPass}, false)
	require.NoError(t, err)
	require.Nil(t, passEvent, "pass events follow store_pass_events")

	page, err := q.ListEvents(ctx, securityaudit.EventFilter{}, 1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	got, err := q.GetEvent(ctx, event.ID)
	require.NoError(t, err)
	require.Equal(t, event.ID, got.ID)
}

// 提示词审计配置来自加密下发的部分；解不开按降级处理（阻断模式下拒绝放行）；没有这一段按关闭。
func TestPromptAuditConfigFromTheSnapshot(t *testing.T) {
	cache := NewConfigCache()
	cache.SetOpener(func(sealed, _ []byte) ([]byte, error) { return sealed, nil })
	load := PromptAuditConfig(cache)
	store := securityaudit.NewRelayConfigStore(load)

	_, ok := load()
	require.False(t, ok, "no snapshot yet")
	require.NoError(t, cache.Apply(&relayv1.ConfigSnapshot{Version: "v1", NodeConfig: []byte(`{"node_id":1}`), Sealed: []byte(`{}`)}))
	require.Equal(t, securityaudit.ModeOff, store.EffectiveMode())

	require.NoError(t, cache.Apply(&relayv1.ConfigSnapshot{Version: "v2", NodeConfig: []byte(`{"node_id":1}`),
		Sealed: []byte(`{"sections":{"prompt_audit":"` + b64(`{"active":{"RiskControlEnabled":true,"Enabled":true,"BlockingEnabled":true}}`) + `"}}`)}))
	require.Equal(t, securityaudit.ModeBlocking, store.EffectiveMode())
	_, active := store.Active()
	require.True(t, active)

	require.NoError(t, cache.Apply(&relayv1.ConfigSnapshot{Version: "v3", NodeConfig: []byte(`{"node_id":1}`),
		Sealed: []byte(`{"sections":{"prompt_audit":"` + b64(`not json`) + `"}}`)}))
	require.True(t, store.BlockingActivationDegraded(), "an unreadable config fails closed")
	require.Equal(t, securityaudit.ModeBlocking, store.EffectiveMode())
	_, err := store.Save(context.Background(), securityaudit.UpdateConfigRequest{}, 1)
	require.Error(t, err, "configuration is managed on the master")
}
