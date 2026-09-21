package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 延迟兜底的去留判断用的是分组级 p90，而调度最终只挑一个账号。分组 p90 会被
// 组里最慢的成员拉高，使一个仍有快号的分组被整体判为"慢"，流量被送去别的
// 池子——哪怕那个池子里每个账号都比本组最快的账号更慢。
//
// 2026-09-15 生产实况（plus-free 20 → 兜底池 29）：
//
//	183（原生）   首字  7.5s   ← 组内最快，只拿到 1 次请求
//	155（原生）   p50  27.5s   最大 93.5s ← 把组 p90 拉到 93s
//	198（兜底）   p50  30.3s
//	223（兜底）   p50  36.7s
//	221（兜底）   p50  38.3s   最大 1m43s
//
// 组级比较（53 < 93×0.6）通过了"目标更快"的检查，于是流量被导向一个每个成员
// 都超过 30 秒阈值的池子，既慢又亏——那一单的上游成本是收入的 16 倍。
func TestLatencyFallbackNotTriggeredWhenGroupStillHasHealthyAccount(t *testing.T) {
	setup := func(t *testing.T) {
		resetOpenAIAdvancedSchedulerSettingCacheForTest()
		openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
			latencyAwareFallbackEnabled: true, latencyThresholdMs: 30000,
			fallbackSpeedupRatio: 0.6, expiresAt: time.Now().Add(time.Hour).UnixNano(),
		})
		t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	}
	groupID := int64(20)

	t.Run("组内还有快号时不得兜底", func(t *testing.T) {
		setup(t)
		svc, _ := stickyPassiveTestService(155, 183)
		tracker := svc.getOpenAILatencyTracker()

		// 组级窗口被最慢的成员拉高到 93 秒，越过 30 秒阈值。
		for i := 0; i < openAILatencyMinSamples; i++ {
			tracker.ObserveGroup(groupID, openAILatencyBucketNormal, 93000)
		}
		// 但账号级证据显示 183 只要 7.5 秒。
		observeHealthy(svc, 155, 93000)
		observeHealthy(svc, 183, 7500)

		_, triggered := svc.shouldTriggerOpenAILatencyFallback(context.Background(), &groupID)
		require.False(t, triggered,
			"组里还有 7.5 秒的账号，不该去借一个每个成员都超过 30 秒的池子")
	})

	t.Run("组内账号全慢才允许兜底", func(t *testing.T) {
		setup(t)
		svc, _ := stickyPassiveTestService(155, 183)
		tracker := svc.getOpenAILatencyTracker()
		for i := 0; i < openAILatencyMinSamples; i++ {
			tracker.ObserveGroup(groupID, openAILatencyBucketNormal, 93000)
		}
		observeHealthy(svc, 155, 93000)
		observeHealthy(svc, 183, 88000)

		_, triggered := svc.shouldTriggerOpenAILatencyFallback(context.Background(), &groupID)
		require.True(t, triggered, "源组确实全慢时，兜底仍应正常触发")
	})

	t.Run("组本身不慢就不该走到这一步", func(t *testing.T) {
		setup(t)
		svc, _ := stickyPassiveTestService(155, 183)
		tracker := svc.getOpenAILatencyTracker()
		for i := 0; i < openAILatencyMinSamples; i++ {
			tracker.ObserveGroup(groupID, openAILatencyBucketNormal, 5000)
		}
		_, triggered := svc.shouldTriggerOpenAILatencyFallback(context.Background(), &groupID)
		require.False(t, triggered)
	})

	t.Run("没有样本的账号不算健康", func(t *testing.T) {
		setup(t)
		svc, _ := stickyPassiveTestService(155, 183)
		tracker := svc.getOpenAILatencyTracker()
		for i := 0; i < openAILatencyMinSamples; i++ {
			tracker.ObserveGroup(groupID, openAILatencyBucketNormal, 93000)
		}
		observeHealthy(svc, 155, 93000) // 183 一个样本都没有

		_, triggered := svc.shouldTriggerOpenAILatencyFallback(context.Background(), &groupID)
		require.True(t, triggered,
			"没人用过的号不能凭空当作健康，否则会挡住本该发生的兜底")
	})

	t.Run("账号清单按TTL缓存不打爆数据库", func(t *testing.T) {
		setup(t)
		svc, repo := stickyPassiveTestService(155, 183)
		tracker := svc.getOpenAILatencyTracker()
		for i := 0; i < openAILatencyMinSamples; i++ {
			tracker.ObserveGroup(groupID, openAILatencyBucketNormal, 93000)
		}
		observeHealthy(svc, 155, 93000)
		observeHealthy(svc, 183, 7500)

		for i := 0; i < 5; i++ {
			svc.shouldTriggerOpenAILatencyFallback(context.Background(), &groupID)
		}
		require.Equal(t, 1, repo.calls, "TTL 内只应查一次账号清单")
	})
}
