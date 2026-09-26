//go:build unit

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// scriptedConcurrencyCache：前 failFirst 次抢账号槽失败，之后成功；可让等待队列已满、抢槽出错。
type scriptedConcurrencyCache struct {
	fakeConcurrencyCache
	failFirst  int64
	attempts   atomic.Int64
	queueFull  bool
	acquireErr error
	releases   atomic.Int64
	waitDecrs  atomic.Int64
	waitIncrOK atomic.Int64
}

func (c *scriptedConcurrencyCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	if c.acquireErr != nil {
		return false, c.acquireErr
	}
	return c.attempts.Add(1) > c.failFirst, nil
}

func (c *scriptedConcurrencyCache) ReleaseAccountSlot(context.Context, int64, string) error {
	c.releases.Add(1)
	return nil
}

func (c *scriptedConcurrencyCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	if c.queueFull {
		return false, nil
	}
	c.waitIncrOK.Add(1)
	return true, nil
}

func (c *scriptedConcurrencyCache) DecrementAccountWaitCount(context.Context, int64) error {
	c.waitDecrs.Add(1)
	return nil
}

func admissionTestSelection(id int64) *service.AccountSelectionResult {
	return &service.AccountSelectionResult{
		Account:  profitSlotTestAccount(id, 0.3),
		WaitPlan: &service.AccountWaitPlan{AccountID: id, MaxConcurrency: 2, Timeout: 2 * time.Second, MaxWaiting: 2},
	}
}

func newAdmitter(cache service.ConcurrencyCache) OpenAIAccountAdmitter {
	return OpenAIAccountAdmitter{Gateway: &service.OpenAIGatewayService{}, Concurrency: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, 0)}
}

// 主从分流主节点和本地选号循环共用的账号准入：各分支的结果。
func TestOpenAIAccountAdmitterOutcomes(t *testing.T) {
	groupID := int64(50)
	gw := &service.OpenAIGatewayService{}
	ctx := profitSlotTestContext(t, gw, groupID, false)
	log := zap.NewNop()

	t.Run("no selection", func(t *testing.T) {
		_, got := newAdmitter(&scriptedConcurrencyCache{}).Admit(ctx, &groupID, "", nil, nil, nil, log)
		require.Equal(t, OpenAIAdmissionNoAccounts, got.Kind)
	})
	t.Run("queue full", func(t *testing.T) {
		cache := &scriptedConcurrencyCache{failFirst: 100, queueFull: true}
		_, got := newAdmitter(cache).Admit(ctx, &groupID, "", admissionTestSelection(1), nil, nil, log)
		require.Equal(t, OpenAIAdmissionQueueFull, got.Kind)
	})
	t.Run("slot error", func(t *testing.T) {
		boom := errors.New("redis down")
		_, got := newAdmitter(&scriptedConcurrencyCache{acquireErr: boom}).Admit(ctx, &groupID, "", admissionTestSelection(1), nil, nil, log)
		require.Equal(t, OpenAIAdmissionSlotError, got.Kind)
		require.ErrorIs(t, got.Err, boom)
	})
	t.Run("waits in the queue and gets a slot", func(t *testing.T) {
		cache := &scriptedConcurrencyCache{failFirst: 3}
		ticks := atomic.Int64{}
		_, got := newAdmitter(cache).Admit(ctx, &groupID, "", admissionTestSelection(1), func() error { ticks.Add(1); return nil }, nil, log)
		require.Equal(t, OpenAIAdmitted, got.Kind)
		require.NotNil(t, got.Release)
		require.Equal(t, int64(1), cache.waitIncrOK.Load())
		require.Equal(t, int64(1), cache.waitDecrs.Load(), "leaves the wait queue once admitted")
		got.Release()
		require.Equal(t, int64(1), cache.releases.Load())
	})
	t.Run("cannot wait fails right after one more immediate try", func(t *testing.T) {
		cache := &scriptedConcurrencyCache{failFirst: 100}
		start := time.Now()
		_, got := newAdmitter(cache).Admit(ctx, &groupID, "", admissionTestSelection(1), nil, errors.New("streaming not supported"), log)
		require.Equal(t, OpenAIAdmissionSlotError, got.Kind)
		require.EqualError(t, got.Err, "streaming not supported")
		require.Less(t, time.Since(start), time.Second, "no queueing")
		require.Equal(t, int64(2), cache.attempts.Load(), "the fast try and one immediate try")
	})
	t.Run("the release is not tied to the caller's context", func(t *testing.T) {
		cache := &scriptedConcurrencyCache{}
		cctx, cancel := context.WithCancel(ctx)
		_, got := newAdmitter(cache).Admit(cctx, &groupID, "", admissionTestSelection(1), nil, nil, log)
		require.Equal(t, OpenAIAdmitted, got.Kind)
		cancel()
		time.Sleep(20 * time.Millisecond)
		require.Zero(t, cache.releases.Load(), "on the master the slot is held until the node releases it")
		got.Release()
		require.Equal(t, int64(1), cache.releases.Load())
	})
}

// 本地路径：准入失败时照旧写出原来的响应。
func TestAcquireResponsesAccountSlotQueueFullWritesTheSameResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(50)
	gw := &service.OpenAIGatewayService{}
	h := &OpenAIGatewayHandler{gatewayService: gw, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&scriptedConcurrencyCache{failFirst: 100, queueFull: true}), SSEPingFormatClaude, 0)}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(profitSlotTestContext(t, gw, groupID, false))
	streamStarted := false
	release, result := h.acquireResponsesAccountSlot(c, &groupID, "", admissionTestSelection(1), false, &streamStarted, zap.NewNop())
	require.Equal(t, openAISlotAcquireFailed, result)
	require.Nil(t, release)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Contains(t, w.Body.String(), "Too many pending requests")
}
