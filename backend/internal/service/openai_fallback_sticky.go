package service

import (
	"context"
	"sync"
	"time"
)

const (
	openAIFallbackRecoveryProbeCooldown = 10 * time.Minute
	openAIFallbackRecoveryHealthyProbes = 2
)

type openAIFallbackStickyState struct {
	mu                sync.Mutex
	sourceGroupID     int64
	targetGroupID     int64
	bucket            string
	enteredAt         time.Time
	lastProbeAt       time.Time
	healthyProbeCount int
	probePending      bool
}

type openAIStickyFallbackCtxKey struct{}

func isOpenAIStickyFallbackRequest(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(openAIStickyFallbackCtxKey{}).(bool)
	return v
}

func withOpenAIStickyFallbackContext(ctx context.Context, sourceGroupID, targetGroupID int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	state := fallbackGroupState{visited: map[int64]struct{}{sourceGroupID: {}, targetGroupID: {}}, hops: 1, originGroupID: sourceGroupID, targetGroupID: targetGroupID}
	ctx = withOpenAIFallbackGroupState(ctx, state)
	ctx = withOpenAIFallbackPoolSourcing(ctx)
	return context.WithValue(ctx, openAIStickyFallbackCtxKey{}, true)
}

func (s *OpenAIGatewayService) markOpenAIFallbackSticky(sourceGroupID, targetGroupID int64, bucket string) {
	if s == nil || sourceGroupID <= 0 || targetGroupID <= 0 || sourceGroupID == targetGroupID {
		return
	}
	now := time.Now()
	value, _ := s.openaiFallbackStickyStates.LoadOrStore(sourceGroupID, &openAIFallbackStickyState{sourceGroupID: sourceGroupID, targetGroupID: targetGroupID, bucket: bucket, enteredAt: now, lastProbeAt: now})
	state, _ := value.(*openAIFallbackStickyState)
	if state == nil {
		return
	}
	state.mu.Lock()
	state.targetGroupID, state.bucket = targetGroupID, bucket
	if state.enteredAt.IsZero() {
		state.enteredAt = now
	}
	if state.lastProbeAt.IsZero() {
		state.lastProbeAt = now
	}
	state.healthyProbeCount = 0
	state.mu.Unlock()
}

func (s *OpenAIGatewayService) clearOpenAIFallbackSticky(sourceGroupID int64) {
	if s != nil && sourceGroupID > 0 {
		s.openaiFallbackStickyStates.Delete(sourceGroupID)
	}
}

func (s *OpenAIGatewayService) openAIStickyFallbackCandidate(sourceGroupID int64) (int64, bool) {
	if s == nil || sourceGroupID <= 0 {
		return sourceGroupID, false
	}
	value, ok := s.openaiFallbackStickyStates.Load(sourceGroupID)
	if !ok {
		return sourceGroupID, false
	}
	state, _ := value.(*openAIFallbackStickyState)
	if state == nil {
		return sourceGroupID, false
	}
	now := time.Now()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.targetGroupID <= 0 {
		return sourceGroupID, false
	}
	_, _, enabled := s.openAILatencyAwareFallbackSettings(context.Background())
	if !enabled {
		s.openaiFallbackStickyStates.Delete(sourceGroupID)
		return sourceGroupID, false
	}
	bucket := state.bucket
	if bucket == "" {
		bucket = openAILatencyBucketNormal
	}
	probeDue := state.lastProbeAt.IsZero() || now.Sub(state.lastProbeAt) >= openAIFallbackRecoveryProbeCooldown
	if !probeDue {
		return state.targetGroupID, false
	}
	state.lastProbeAt = now
	state.probePending = true
	return sourceGroupID, true
}

func (s *OpenAIGatewayService) markOpenAIStickyProbeResult(groupID int64, bucket string, ttftMs int) {
	if s == nil || groupID <= 0 || ttftMs <= 0 {
		return
	}
	value, ok := s.openaiFallbackStickyStates.Load(groupID)
	if !ok {
		return
	}
	state, _ := value.(*openAIFallbackStickyState)
	if state == nil {
		return
	}
	threshold, _, enabled := s.openAILatencyAwareFallbackSettings(context.Background())
	if !enabled {
		return
	}
	state.mu.Lock()
	if !state.probePending || (state.bucket != "" && state.bucket != bucket) {
		state.mu.Unlock()
		return
	}
	state.probePending = false
	if ttftMs > threshold {
		state.healthyProbeCount = 0
		state.mu.Unlock()
		return
	}
	state.healthyProbeCount++
	if state.healthyProbeCount >= openAIFallbackRecoveryHealthyProbes {
		s.openaiFallbackStickyStates.Delete(groupID)
	}
	state.mu.Unlock()
}
