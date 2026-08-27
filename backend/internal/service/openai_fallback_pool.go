package service

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"log/slog"
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

func (s *OpenAIGatewayService) nextOpenAIFallbackGroup(ctx context.Context, currentGroupID *int64, platform string) (context.Context, *int64) {
	if s == nil || currentGroupID == nil || *currentGroupID <= 0 {
		return ctx, nil
	}
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

	nextCtx := withOpenAIFallbackGroupState(withOpenAIFallbackPoolSourcing(ctx), nextState)
	if trace, ok := fallbackPoolUsageTraceFromState(nextState); ok {
		notifyFallbackPoolSelection(ctx, s.cfg, trace)
	}
	return nextCtx, &fallbackID
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
