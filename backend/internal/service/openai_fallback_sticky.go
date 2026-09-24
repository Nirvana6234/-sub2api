package service

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	openAIFallbackRecoveryProbeCooldown = 10 * time.Minute
	openAIFallbackRecoveryHealthyProbes = 2

	// openAIFallbackPassiveRecoveryMargin 是被动恢复判定的余量系数：源组账号的
	// p90 必须低于 阈值 × 该系数 才算健康。
	//
	// 留 20% 余量是为了防抖。恰好卡在阈值上下的组会被"切回去 → 立刻又超时 →
	// 再兜底"反复横跳，每次横跳都由真实用户请求买单；要求明显好于阈值才切回，
	// 换来的是一次切回大概率能站住。
	openAIFallbackPassiveRecoveryMargin = 0.8

	// openAIFallbackPassiveAccountsTTL 是源组账号清单的缓存时长。被动判定只在
	// 分组已经粘在兜底组时才跑，这里再加一层节流，避免热路径上反复查库。
	openAIFallbackPassiveAccountsTTL = 30 * time.Second
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
	// sourceAccountIDs 是源组的可调度账号清单，供被动恢复判定使用；
	// accountsFetchedAt 为其取用时刻，按 openAIFallbackPassiveAccountsTTL 过期。
	sourceAccountIDs  []int64
	accountsFetchedAt time.Time
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

// openAISourceGroupPassivelyHealthy 用「别人已经跑过的请求」判断源组是否恢复，
// 不再额外拿真实请求去试探。
//
// 主动探测（每 10 分钟放行一个请求、连续 2 次健康才解除）在源组账号与其他分组
// 共用时是多余的：那些账号一直在给别的分组干活，openAILatencyTracker 的账号级
// 窗口一直在更新。生产上 plus-free 的 175/183 同时属于 plus，粘在兜底组期间它们
// 从没停过工，却因为只看分组级窗口（粘住后不再有流量、永不刷新）而必须干等
// 20 分钟起步，且任何一次探测超时就归零重来，实际表现为再也切不回来。
//
// 判定口径：源组可调度账号中，有读数（样本数达到 openAILatencyMinSamples）
// 且 p90 <= 阈值 × margin 的账号必须过半。单个账号恢复不算数——池子整体能扛住
// 才值得把流量切回去。
func (s *OpenAIGatewayService) openAISourceGroupPassivelyHealthy(
	ctx context.Context,
	state *openAIFallbackStickyState,
	sourceGroupID int64,
	thresholdMs int,
) (healthy bool, healthyCount, total int) {
	if s == nil || state == nil || sourceGroupID <= 0 || thresholdMs <= 0 {
		return false, 0, 0
	}
	accountIDs := s.openAIStickySourceAccountIDs(ctx, state, sourceGroupID)
	if len(accountIDs) == 0 {
		return false, 0, 0
	}
	tracker := s.getOpenAILatencyTracker()
	if tracker == nil {
		return false, 0, len(accountIDs)
	}
	budget := int(float64(thresholdMs) * openAIFallbackPassiveRecoveryMargin)
	for _, accountID := range accountIDs {
		tail, ok := tracker.AccountTail(accountID)
		if !ok {
			// 没有足够样本：既不算健康也不算不健康，但它仍计入分母——
			// 一个长期没人用的池子不该仅凭个别账号的读数被判定为已恢复。
			continue
		}
		if tail <= budget {
			healthyCount++
		}
	}
	total = len(accountIDs)
	return healthyCount*2 > total, healthyCount, total
}

// openAIStickySourceAccountIDs 取源组可调度账号清单，按 TTL 缓存在粘性状态里。
func (s *OpenAIGatewayService) openAIStickySourceAccountIDs(
	ctx context.Context,
	state *openAIFallbackStickyState,
	sourceGroupID int64,
) []int64 {
	now := time.Now()
	state.mu.Lock()
	if len(state.sourceAccountIDs) > 0 && now.Sub(state.accountsFetchedAt) < openAIFallbackPassiveAccountsTTL {
		cached := append([]int64(nil), state.sourceAccountIDs...)
		state.mu.Unlock()
		return cached
	}
	state.mu.Unlock()

	if s.accountRepo == nil {
		return nil
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, &sourceGroupID, []string{PlatformOpenAI}, true)
	if err != nil {
		slog.Warn("openai_fallback_sticky_source_accounts_failed",
			"source_group_id", sourceGroupID, "error", err)
		return nil
	}
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].ID)
	}

	state.mu.Lock()
	state.sourceAccountIDs = append([]int64(nil), ids...)
	state.accountsFetchedAt = now
	state.mu.Unlock()
	return ids
}

func (s *OpenAIGatewayService) openAIStickyFallbackCandidate(ctx context.Context, sourceGroupID int64) (int64, bool) {
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
	// 被动恢复优先：源组账号被其他分组用过且延迟达标，就直接切回去，
	// 不必再等主动探测的冷却。判定本身不持 state.mu（内部要取账号清单）。
	if threshold, _, enabled := s.openAILatencyAwareFallbackSettings(ctx); enabled {
		if healthy, healthyCount, total := s.openAISourceGroupPassivelyHealthy(ctx, state, sourceGroupID, threshold); healthy {
			s.openaiFallbackStickyStates.Delete(sourceGroupID)
			// 同时清掉源分组陈旧的分组级延迟窗口。不清的话，
			// shouldTriggerOpenAILatencyFallback 会在粘性刚解除的下一个请求上
			// 读到触发兜底那一刻冻结的慢读数，立刻把流量又踢回兜底池——而请求
			// 一旦跑在兜底组，样本就记到兜底组去了，源分组的窗口永远刷新不了。
			// 生产上这表现为每分钟恢复一次、每次立刻被打回，流量始终停在兜底池。
			s.getOpenAILatencyTracker().ResetGroup(sourceGroupID)
			slog.Info("openai_fallback_sticky_recovered_passively",
				"source_group_id", sourceGroupID,
				"healthy_accounts", healthyCount,
				"total_accounts", total,
				"threshold_ms", threshold,
				"margin", openAIFallbackPassiveRecoveryMargin)
			return sourceGroupID, false
		}
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
	recovered := state.healthyProbeCount >= openAIFallbackRecoveryHealthyProbes
	if recovered {
		s.openaiFallbackStickyStates.Delete(groupID)
	}
	state.mu.Unlock()

	// 与被动恢复路径对齐：解除粘性的同时必须清掉源分组陈旧的分组级延迟窗口。
	// 源组被兜底期间流量都记到兜底组，它的窗口冻结在触发那一刻的慢读数上；
	// 只删粘性状态而不清窗口，shouldTriggerOpenAILatencyFallback 会在下一个
	// 请求上立刻读到这份陈旧读数并重新兜底，表现为反复"恢复→被打回"，流量
	// 始终停在兜底池（2026-09-15 生产实况：探测两次健康后仍被立刻打回）。
	//
	// ResetGroup 放在解锁之后调用：它要取 tracker 自己的锁，嵌套在 state.mu
	// 内会引入不必要的锁序约束。
	if recovered {
		s.getOpenAILatencyTracker().ResetGroup(groupID)
		slog.Info("openai_fallback_sticky_recovered_by_probe",
			"source_group_id", groupID,
			"healthy_probes", openAIFallbackRecoveryHealthyProbes,
			"threshold_ms", threshold)
	}
}
