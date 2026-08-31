package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// OpenAIGatewayService 这一侧的分组兜底池：OpenAI 与 Grok。
//
// 遍历算法、防环、跳数上限和准入加严都在 fallback_pool.go 的共享内核里，这里只负责
// 本侧的 ctx key、分组怎么读、以及平台归一化后的比较。
//
// 设计上最要紧的一点是「换候选来源，但不换分组身份」。利润门是按 groupID 装配并缓存
// 在 ctx 里的（见 withGatewayProfitControlGate：命中同一 groupID 才复用，否则按新分组
// 重装）。所以兜底递归只能改变账号候选来源，不能重新装配利润门；否则 A→B 时会错误地
// 按 B 的利润配置放行账号。ctx 标记只表达「当前候选来自兜底链路」，计费、利润门、限额
// 仍由请求原分组上下文决定。

type openAIFallbackPoolSourcingCtxKey struct{}
type openAIFallbackGroupStateCtxKey struct{}
type openAILatencyFallbackTriggerCtxKey struct{}
type openAILatencyFallbackSuppressedCtxKey struct{}

// withOpenAILatencyFallbackTrigger 标记本次选号是由「源组变慢」触发的，
// 并带上触发的强度分桶，供后续与兜底组做同桶对齐比较。
func withOpenAILatencyFallbackTrigger(ctx context.Context, bucket string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(bucket) == "" {
		bucket = openAILatencyBucketNormal
	}
	return context.WithValue(ctx, openAILatencyFallbackTriggerCtxKey{}, bucket)
}

// isOpenAILatencyFallbackTrigger 返回触发分桶及是否处于延迟触发链路。
func isOpenAILatencyFallbackTrigger(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	bucket, _ := ctx.Value(openAILatencyFallbackTriggerCtxKey{}).(string)
	suppressed, _ := ctx.Value(openAILatencyFallbackSuppressedCtxKey{}).(bool)
	if bucket == "" || suppressed {
		return "", false
	}
	return bucket, true
}

func withOpenAILatencyFallbackSuppressed(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAILatencyFallbackSuppressedCtxKey{}, true)
}

// withOpenAIFallbackPoolSourcing 标记本次选号来自兜底分组链路。
//
// 只影响候选来源与准入严格度，不影响计费、限流和利润门的分组归属 ——
// 那些一律按用户原本所属的分组走。
func withOpenAIFallbackPoolSourcing(ctx context.Context) context.Context {
	return context.WithValue(ctx, openAIFallbackPoolSourcingCtxKey{}, true)
}

// isOpenAIFallbackPoolSourcing 报告本次选号是否处于兜底取号模式。
func isOpenAIFallbackPoolSourcing(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	sourcing, _ := ctx.Value(openAIFallbackPoolSourcingCtxKey{}).(bool)
	return sourcing
}

func openAIFallbackGroupStateFromContext(ctx context.Context) fallbackGroupState {
	if ctx == nil {
		return fallbackGroupState{}
	}
	state, _ := ctx.Value(openAIFallbackGroupStateCtxKey{}).(fallbackGroupState)
	return state
}

func openAIFallbackPoolUsageTraceFromContext(ctx context.Context) (fallbackPoolUsageTrace, bool) {
	return fallbackPoolUsageTraceFromState(openAIFallbackGroupStateFromContext(ctx))
}

func withOpenAIFallbackGroupState(ctx context.Context, state fallbackGroupState) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIFallbackGroupStateCtxKey{}, state)
}

func isNoAvailableOpenAIAccountError(err error) bool {
	return err != nil && (errors.Is(err, ErrNoAvailableAccounts) || errors.Is(err, ErrNoAvailableCompactAccounts))
}

// shouldUseOpenAIFallbackForModel allows fallback only when the candidate
// group has at least one persistently eligible account that supports the
// requested model. The caller must pass the group being entered, not the
// group being left: a source group may legitimately reject a model while its
// configured fallback pool is the intended provider for that model.
func (s *OpenAIGatewayService) shouldUseOpenAIFallbackForModel(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	platform string,
) bool {
	if s == nil || groupID == nil || *groupID <= 0 || strings.TrimSpace(requestedModel) == "" {
		return true
	}

	platform = NormalizeOpenAICompatiblePlatform(platform)
	requestedModel = strings.TrimSpace(requestedModel)
	if cache := openAIModelAvailabilityCacheFromContext(ctx); cache != nil {
		key := openAIModelAvailabilityCacheEntryKey{
			groupID:  *groupID,
			platform: platform,
			model:    requestedModel,
		}
		// Hold the lock across the query so concurrent fallback branches in
		// the same request cannot stampede the database for one key.
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if cache.entries == nil {
			cache.entries = make(map[openAIModelAvailabilityCacheEntryKey]ModelAvailabilityDiagnosis)
		}
		if diagnosis, ok := cache.entries[key]; ok {
			return !diagnosis.HasAccountsInPool || diagnosis.HasModelSupport
		}
		diagnosis := s.DiagnoseModelAvailabilityForPlatform(ctx, groupID, requestedModel, platform)
		cache.entries[key] = diagnosis
		if !diagnosis.HasAccountsInPool || diagnosis.HasModelSupport {
			return true
		}
		slog.Warn(
			"openai_fallback_blocked_model_unsupported",
			"group_id", *groupID,
			"platform", platform,
			"model", requestedModel,
		)
		return false
	}

	diagnosis := s.DiagnoseModelAvailabilityForPlatform(ctx, groupID, requestedModel, platform)
	if !diagnosis.HasAccountsInPool || diagnosis.HasModelSupport {
		return true
	}

	slog.Warn(
		"openai_fallback_blocked_model_unsupported",
		"group_id", *groupID,
		"platform", normalizeOpenAICompatiblePlatform(platform),
		"model", strings.TrimSpace(requestedModel),
	)
	return false
}

func (s *OpenAIGatewayService) nextOpenAIFallbackGroup(ctx context.Context, currentGroupID *int64, platform string, requestedModel string) (context.Context, *int64) {
	if s == nil || currentGroupID == nil || *currentGroupID <= 0 {
		return ctx, nil
	}
	ctx = withOpenAIModelAvailabilityCache(ctx)
	platform = normalizeOpenAICompatiblePlatform(platform)
	if platform != PlatformOpenAI && platform != PlatformGrok {
		return ctx, nil
	}

	fallbackID, nextState, ok := nextFallbackGroupID(
		ctx,
		*currentGroupID,
		openAIFallbackGroupStateFromContext(ctx),
		fallbackTraversal{
			logNS:        "openai",
			resolveGroup: s.resolveOpenAIFallbackGroupConfig,
			currentGroupOK: func(group *Group) bool {
				return group.Status == StatusActive
			},
			fallbackGroupOK: func(group *Group) (bool, string) {
				if group.Status != StatusActive {
					return false, ""
				}
				if !group.IsFallbackPool {
					return false, "target_not_fallback_pool"
				}
				if normalizeOpenAICompatiblePlatform(group.Platform) != platform {
					return false, "platform_mismatch"
				}
				return true, ""
			},
		},
	)
	if !ok {
		return ctx, nil
	}
	// Model support is a property of the target pool. Checking currentGroupID
	// here incorrectly blocks a valid fallback when the source group is only a
	// routing alias and the fallback pool owns the requested model.
	if !s.shouldUseOpenAIFallbackForModel(ctx, &fallbackID, requestedModel, platform) {
		return ctx, nil
	}
	if bucket, triggered := isOpenAILatencyFallbackTrigger(ctx); triggered && !s.shouldUseOpenAILatencyFallbackGroup(*currentGroupID, fallbackID, bucket) {
		return ctx, nil
	}

	nextCtx := withOpenAIFallbackGroupState(withOpenAIFallbackPoolSourcing(ctx), nextState)
	if trace, ok := fallbackPoolUsageTraceFromState(nextState); ok {
		notifyFallbackPoolSelection(ctx, s.cfg, trace)
	}
	return nextCtx, &fallbackID
}

// shouldUseOpenAILatencyFallbackGroup 判断「源组变慢」时是否值得切到兜底组。
//
// 两处与历史实现的关键差异：
//
//  1. 比较在**同一强度分桶**内进行。各组的流量构成不同（有的组 XHigh 密集、
//     有的以低强度为主），直接比两组的整体读数是拿苹果比橘子，比出来的
//     「快 40%」可能只反映流量构成差异，不反映健康度。
//  2. 兜底组在该分桶上**没有样本时放行**，而不是拒绝。历史实现在这里返回 false，
//     结果是：兜底组没流量 → 攒不到样本 → 不给读数 → 不切过去 → 永远没流量，
//     冷启动的兜底池永远用不上。源组已经确认变慢的前提下，切到一个未知的池子
//     至少不会更糟，而且能把样本喂起来，让下一次判断有据可依。
func (s *OpenAIGatewayService) shouldUseOpenAILatencyFallbackGroup(sourceGroupID, fallbackGroupID int64, bucket string) bool {
	if s == nil || sourceGroupID <= 0 || fallbackGroupID <= 0 {
		return false
	}
	threshold, speedupRatio, enabled := s.openAILatencyAwareFallbackSettings(context.Background())
	if !enabled {
		return false
	}
	if strings.TrimSpace(bucket) == "" {
		bucket = openAILatencyBucketNormal
	}
	tracker := s.getOpenAILatencyTracker()
	sourceTail, sourceOK := tracker.GroupTailForBucket(sourceGroupID, bucket)
	if !sourceOK || sourceTail <= threshold {
		return false
	}
	fallbackTail, fallbackOK := tracker.GroupTailForBucket(fallbackGroupID, bucket)
	if !fallbackOK {
		slog.Info(
			"openai_fallback_allowed_target_unmeasured",
			"source_group_id", sourceGroupID,
			"source_ttft_ms", sourceTail,
			"target_group_id", fallbackGroupID,
			"bucket", bucket,
		)
		return true
	}
	if float64(fallbackTail) >= float64(sourceTail)*speedupRatio {
		slog.Info(
			"openai_fallback_skipped_target_not_faster",
			"source_group_id", sourceGroupID,
			"source_ttft_ms", sourceTail,
			"target_group_id", fallbackGroupID,
			"target_ttft_ms", fallbackTail,
			"speedup_ratio", speedupRatio,
			"bucket", bucket,
		)
		return false
	}
	return true
}

func (s *OpenAIGatewayService) resolveOpenAIFallbackGroupConfig(ctx context.Context, groupID int64) *Group {
	if groupID <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctxGroup, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(ctxGroup) && ctxGroup.ID == groupID {
		return ctxGroup
	}
	if s != nil && s.schedulerSnapshot != nil {
		group, err := s.schedulerSnapshot.GetGroupByIDLite(ctx, groupID)
		if err != nil {
			slog.Warn("openai_fallback_group_load_failed", "group_id", groupID, "error", err)
			return nil
		}
		return group
	}
	return nil
}

// openAIFallbackPoolRejectReason 报告某账号在兜底取号时是否应被拒绝。
// 规则见 fallbackPoolRejectReasonWhenSourcing。
func openAIFallbackPoolRejectReason(ctx context.Context, account *Account) string {
	return fallbackPoolRejectReasonWhenSourcing(ctx, account, isOpenAIFallbackPoolSourcing(ctx))
}

// OpenAIServingGroupID 返回本次请求实际被调度到的分组：走过兜底链路时是兜底组，
// 否则是请求本身的分组。延迟样本必须按这个值归属，否则一次请求的耗时会被算到
// 没有参与调度的组头上。
func OpenAIServingGroupID(ctx context.Context, requestedGroupID int64) int64 {
	state := openAIFallbackGroupStateFromContext(ctx)
	if state.targetGroupID > 0 {
		return state.targetGroupID
	}
	return requestedGroupID
}
