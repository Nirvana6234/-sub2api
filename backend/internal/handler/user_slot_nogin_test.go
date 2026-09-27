//go:build unit

package handler

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// scriptedUserSlotCache：前 failFirst 次抢用户槽失败；可让等待队列已满。
type scriptedUserSlotCache struct {
	fakeConcurrencyCache
	failFirst int64
	attempts  atomic.Int64
	queueFull bool
	releases  atomic.Int64
	waitDecrs atomic.Int64
}

func (c *scriptedUserSlotCache) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return c.attempts.Add(1) > c.failFirst, nil
}

func (c *scriptedUserSlotCache) ReleaseUserSlot(context.Context, int64, string) error {
	c.releases.Add(1)
	return nil
}

func (c *scriptedUserSlotCache) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return !c.queueFull, nil
}

func (c *scriptedUserSlotCache) DecrementWaitCount(context.Context, int64) error {
	c.waitDecrs.Add(1)
	return nil
}

// 主从分流主节点用的用户并发槽：与网关处理函数同样的立即抢、排队、队列满规则，释放不绑定 ctx。
func TestAcquireUserSlotWithWaitNoGin(t *testing.T) {
	helper := func(c service.ConcurrencyCache) *ConcurrencyHelper {
		return NewConcurrencyHelper(service.NewConcurrencyService(c), SSEPingFormatClaude, 0)
	}

	cache := &scriptedUserSlotCache{}
	ctx, cancel := context.WithCancel(context.Background())
	release, err := helper(cache).AcquireUserSlotWithWaitNoGin(ctx, 7, 0, 2)
	require.NoError(t, err)
	cancel()
	require.Zero(t, cache.releases.Load(), "the slot outlives the call's context")
	release()
	require.Equal(t, int64(1), cache.releases.Load())

	full := &scriptedUserSlotCache{failFirst: 100, queueFull: true}
	_, err = helper(full).AcquireUserSlotWithWaitNoGin(context.Background(), 7, 0, 2)
	var queueFull *WaitQueueFullError
	require.ErrorAs(t, err, &queueFull)
	require.Equal(t, "user", queueFull.SlotType)
	status, _, code, _ := concurrencyErrorResponse(err, "user")
	require.Equal(t, 429, status)
	require.Equal(t, gatewayQueueFullCode, code)

	waits := &scriptedUserSlotCache{failFirst: 2}
	release, err = helper(waits).AcquireUserSlotWithWaitNoGin(context.Background(), 7, 0, 2)
	require.NoError(t, err, "queues and gets the slot")
	require.Equal(t, int64(1), waits.waitDecrs.Load(), "leaves the wait queue")
	release()
}
