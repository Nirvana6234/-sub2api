package service

import (
	"context"
	"testing"
	"time"
)

func TestOpenAIFallbackStickyCandidateKeepsTargetUntilProbe(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{latencyAwareFallbackEnabled: true, latencyThresholdMs: 30000, fallbackSpeedupRatio: 0.6, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	svc := &OpenAIGatewayService{}
	svc.markOpenAIFallbackSticky(2, 29, openAILatencyBucketNormal)
	if got, probing := svc.openAIStickyFallbackCandidate(2); got != 29 || probing {
		t.Fatalf("sticky candidate = (%d, %t), want (29, false)", got, probing)
	}

	value, _ := svc.openaiFallbackStickyStates.Load(int64(2))
	state := value.(*openAIFallbackStickyState)
	state.mu.Lock()
	state.lastProbeAt = time.Now().Add(-openAIFallbackRecoveryProbeCooldown - time.Second)
	state.mu.Unlock()
	if got, probing := svc.openAIStickyFallbackCandidate(2); got != 2 || !probing {
		t.Fatalf("probe candidate = (%d, %t), want (2, true)", got, probing)
	}
}

func TestOpenAIFallbackStickyClearsAfterHealthyProbeResults(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{latencyAwareFallbackEnabled: true, latencyThresholdMs: 30000, fallbackSpeedupRatio: 0.6, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	svc := &OpenAIGatewayService{}
	svc.markOpenAIFallbackSticky(2, 29, openAILatencyBucketNormal)
	value, _ := svc.openaiFallbackStickyStates.Load(int64(2))
	state := value.(*openAIFallbackStickyState)
	state.mu.Lock()
	state.probePending = true
	state.mu.Unlock()
	svc.markOpenAIStickyProbeResult(2, openAILatencyBucketNormal, 1000)
	state.mu.Lock()
	state.probePending = true
	state.mu.Unlock()
	svc.markOpenAIStickyProbeResult(2, openAILatencyBucketNormal, 1000)
	if _, ok := svc.openaiFallbackStickyStates.Load(int64(2)); ok {
		t.Fatal("sticky relation should clear after two healthy probes")
	}
}

func TestOpenAIStickyFallbackContextMarksCurrentTargetOnly(t *testing.T) {
	ctx := withOpenAIStickyFallbackContext(context.Background(), 2, 29)
	if !isOpenAIFallbackPoolSourcing(ctx) || !isOpenAIStickyFallbackRequest(ctx) {
		t.Fatal("sticky fallback context should mark fallback sourcing")
	}
	if got := OpenAIServingGroupID(ctx, 2); got != 29 {
		t.Fatalf("serving group = %d, want 29", got)
	}
}
