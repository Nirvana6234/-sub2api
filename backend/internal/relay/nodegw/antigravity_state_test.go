package nodegw

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 从节点转发路径上写的 Antigravity 账号状态（模型级 / 账号级限流、清除积分耗尽标记）都是账号事件，不碰库；
// 别的 extra 写入不支持（转发路径不该有）。
func TestRelayAccountRepoSendsAntigravityStateAsEvents(t *testing.T) {
	outbox := node.NewEventOutbox(0)
	repo := relayAccountRepo{reporter: node.NewRemoteAccountReporter(outbox)}
	ctx := context.Background()

	require.NoError(t, repo.SetModelRateLimit(ctx, 1, "claude-sonnet-4-5", time.Now().Add(time.Minute)))
	require.NoError(t, repo.SetRateLimited(ctx, 1, time.Now().Add(time.Minute)))
	require.NoError(t, repo.UpdateExtra(ctx, 1, map[string]any{service.ModelRateLimitsExtraKey: map[string]any{"k": map[string]any{}}}))
	require.Equal(t, 3, outbox.Len())
	require.Error(t, repo.UpdateExtra(ctx, 1, map[string]any{"other": 1}))
	require.Error(t, repo.UpdateExtra(ctx, 1, map[string]any{service.ModelRateLimitsExtraKey: 1, "other": 1}))
	require.Equal(t, 3, outbox.Len())
}

// INTERNAL 500 计数在主节点：重试耗尽发事件、本地不惩罚；成功清零只在这台节点报过耗尽或距上次清零超过一分钟时才发。
func TestRelayInternal500ReportsToTheMaster(t *testing.T) {
	outbox := node.NewEventOutbox(0)
	c := newRelayInternal500(node.NewRemoteAccountReporter(outbox))
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()

	count, err := c.IncrementInternal500Count(ctx, 7)
	require.NoError(t, err)
	require.Zero(t, count, "the penalty is applied on the master, not here")
	require.Equal(t, 1, outbox.Len())

	require.NoError(t, c.ResetInternal500Count(ctx, 7))
	require.Equal(t, 2, outbox.Len(), "a success after an exhaustion clears the counter at once")
	require.NoError(t, c.ResetInternal500Count(ctx, 7))
	require.Equal(t, 2, outbox.Len(), "ordinary successes do not flood the master")

	now = now.Add(2 * time.Minute)
	require.NoError(t, c.ResetInternal500Count(ctx, 7))
	require.Equal(t, 3, outbox.Len(), "counts other nodes reported are cleared at most a minute late")
}
