package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 2026-09-15 生产实况（plus-free 20 → 兜底池 29）：
//
//	11:29~11:34  183 首字 28/29/31/32/145 秒 → 组级窗口越过 30 秒阈值，进入粘性兜底
//	11:44:37     探测① 305 = 5.1 秒 ✓
//	11:55:47     探测② 305 = 3.7 秒 ✓ → healthyProbeCount 达标，粘性解除
//	11:56:21     流量却立刻回到兜底池的 198（首字 338 秒）
//
// 原因：主动探测恢复只删粘性状态、没清分组级延迟窗口，而源组被兜底期间流量都
// 记到兜底组，它的窗口冻结在触发那一刻的慢读数上。粘性一解除，下一个请求就被
// 这份陈旧读数重新踢回兜底，表现为反复「恢复→打回」，流量永久停在兜底池——
// 而那时兜底池的 198/221 已经慢到 60~396 秒，比源组的 3.7 秒慢两个数量级。
func setupLatencyFallbackSettingsForTest(t *testing.T) {
	t.Helper()
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		latencyAwareFallbackEnabled: true, latencyThresholdMs: 30000,
		fallbackSpeedupRatio: 0.6, expiresAt: time.Now().Add(time.Hour).UnixNano(),
	})
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
}

func TestOpenAIFallbackStickyProbeRecoveryResetsGroupWindow(t *testing.T) {
	setupLatencyFallbackSettingsForTest(t)

	svc := &OpenAIGatewayService{}
	tracker := svc.getOpenAILatencyTracker()
	groupID := int64(20)

	// 组级窗口冻结在触发兜底那一刻的慢读数。
	for i := 0; i < openAILatencyMinSamples; i++ {
		tracker.ObserveGroup(groupID, openAILatencyBucketNormal, 145000)
	}
	svc.markOpenAIFallbackSticky(groupID, 29, openAILatencyBucketNormal)

	// 连续两次健康探测（生产上的 5.1s / 3.7s）。
	for _, ttft := range []int{5149, 3755} {
		value, _ := svc.openaiFallbackStickyStates.Load(groupID)
		state, _ := value.(*openAIFallbackStickyState)
		require.NotNil(t, state, "粘性状态应在探测达标前存在")
		state.mu.Lock()
		state.probePending = true
		state.mu.Unlock()
		svc.markOpenAIStickyProbeResult(groupID, openAILatencyBucketNormal, ttft)
	}

	_, stillSticky := svc.openaiFallbackStickyStates.Load(groupID)
	require.False(t, stillSticky, "两次健康探测后粘性应解除")

	// 核心断言：陈旧的分组级慢读数必须一并清掉，否则下一个请求立刻重新兜底。
	_, _, ok := tracker.GroupTail(groupID)
	require.False(t, ok, "探测恢复必须清空源组冻结的延迟窗口，否则会被立刻打回兜底")
}

// 被动恢复路径同样要清窗口——这条路径原本就有 ResetGroup，这里把它钉住，
// 防止后续重构把两条恢复路径之一改回"只删粘性"。
func TestOpenAIFallbackStickyPassiveRecoveryAlsoResetsGroupWindow(t *testing.T) {
	setupLatencyFallbackSettingsForTest(t)

	groupID := int64(20)
	svc, _ := stickyPassiveTestService(155, 305)
	tracker := svc.getOpenAILatencyTracker()
	for i := 0; i < openAILatencyMinSamples; i++ {
		tracker.ObserveGroup(groupID, openAILatencyBucketNormal, 145000)
	}
	// 两个账号都健康，满足「过半」判定。
	observeHealthy(svc, 155, 4000)
	observeHealthy(svc, 305, 3755)
	svc.markOpenAIFallbackSticky(groupID, 29, openAILatencyBucketNormal)

	got, probing := svc.openAIStickyFallbackCandidate(context.Background(), groupID)
	require.Equal(t, groupID, got, "过半账号健康时应回到源组")
	require.False(t, probing)

	_, _, ok := tracker.GroupTail(groupID)
	require.False(t, ok, "被动恢复同样必须清掉陈旧的分组级窗口")
}
