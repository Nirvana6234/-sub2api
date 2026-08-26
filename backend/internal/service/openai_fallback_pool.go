package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// 分组兜底：当前分组无可用账号时，从它指定的 fallback_group_id 继续挑账号。
//
// 设计上最要紧的一点是「换候选来源，但不换分组身份」。
//
// 利润门是按 groupID 装配并缓存在 ctx 里的（见 withGatewayProfitControlGate：
// 命中同一 groupID 才复用，否则按新分组重装）。所以兜底递归只能改变账号候选
// 来源，不能重新装配利润门；否则 A→B 时会错误地按 B 的利润配置放行账号。
//
// 下面的 ctx 标记只表达「当前候选来自兜底链路」，并携带 visited/depth 做防环。
// 计费、利润门、限额仍由请求原分组上下文决定。

const openAIFallbackGroupMaxHops = 3

type openAIFallbackPoolSourcingCtxKey struct{}
type openAIFallbackGroupStateCtxKey struct{}

type openAIFallbackGroupState struct {
	visited map[int64]struct{}
	hops    int
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

func openAIFallbackGroupStateFromContext(ctx context.Context) openAIFallbackGroupState {
	if ctx == nil {
		return openAIFallbackGroupState{}
	}
	state, _ := ctx.Value(openAIFallbackGroupStateCtxKey{}).(openAIFallbackGroupState)
	return state
}

func withOpenAIFallbackGroupState(ctx context.Context, state openAIFallbackGroupState) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIFallbackGroupStateCtxKey{}, state)
}

func cloneOpenAIFallbackVisited(in map[int64]struct{}) map[int64]struct{} {
	out := make(map[int64]struct{}, len(in)+1)
	for id := range in {
		out[id] = struct{}{}
	}
	return out
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

	state := openAIFallbackGroupStateFromContext(ctx)
	visited := cloneOpenAIFallbackVisited(state.visited)
	currentID := *currentGroupID
	if _, seen := visited[currentID]; seen {
		slog.Warn("openai_fallback_group_cycle_detected", "group_id", currentID)
		return ctx, nil
	}
	visited[currentID] = struct{}{}
	if state.hops >= openAIFallbackGroupMaxHops {
		slog.Warn("openai_fallback_group_max_hops_reached", "group_id", currentID, "max_hops", openAIFallbackGroupMaxHops)
		return ctx, nil
	}

	group := s.resolveOpenAIFallbackGroupConfig(ctx, currentID)
	if group == nil || group.Status != StatusActive || group.FallbackGroupID == nil || *group.FallbackGroupID <= 0 {
		return ctx, nil
	}
	fallbackID := *group.FallbackGroupID
	if _, seen := visited[fallbackID]; seen {
		slog.Warn("openai_fallback_group_cycle_detected", "group_id", currentID, "fallback_group_id", fallbackID)
		return ctx, nil
	}
	fallbackGroup := s.resolveOpenAIFallbackGroupConfig(ctx, fallbackID)
	if fallbackGroup == nil || fallbackGroup.Status != StatusActive {
		return ctx, nil
	}
	if !fallbackGroup.IsFallbackPool {
		slog.Warn("openai_fallback_group_target_not_pool", "group_id", currentID, "fallback_group_id", fallbackID)
		return ctx, nil
	}
	if normalizeOpenAICompatiblePlatform(fallbackGroup.Platform) != platform {
		slog.Warn("openai_fallback_group_platform_mismatch", "group_id", currentID, "fallback_group_id", fallbackID, "platform", platform, "fallback_platform", fallbackGroup.Platform)
		return ctx, nil
	}
	state = openAIFallbackGroupState{visited: visited, hops: state.hops + 1}
	nextCtx := withOpenAIFallbackGroupState(withOpenAIFallbackPoolSourcing(ctx), state)
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
//
// 兜底比常规调度更严，只多这一条：成本从未声明过的账号不参与兜底。
//
// 常规调度对未声明成本的账号是「放行并告警」，理由是利润门没有可依据的事实，
// 不该替运营假设一个成本再据此否决（见 openAIProfitControlVetoReason）。那个
// 取舍在常规路径上成立 —— 拦掉它等于让本来该服务的请求直接失败。
//
// 但兜底是额外的救济路径：这些账号本不属于目标分组，是被临时借调过来的。
// 拿一个不知道成本的号去顶，等于用未知成本换未知收益，而代价还落在目标分组
// 的利润上。宁可不兜底，也不做这种交易。
//
// 其余判定（平台、模型、能力、限流、利润门阈值）与常规完全一致，不在这里重复，
// 由 openAICompatibleAccountEligibilityFailureReason 统一施加。
func openAIFallbackPoolRejectReason(ctx context.Context, account *Account) string {
	if !isOpenAIFallbackPoolSourcing(ctx) || account == nil {
		return ""
	}
	// 定价时刻取自已装配的利润门，保证与门内阈值用的是同一个 D 侧时刻；
	// 取不到就留零值，profitControlAccountUpstreamRate 会回退到当前时间。
	var pricingAt time.Time
	if gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate); gate != nil {
		pricingAt = gate.pricingAt
	}
	if _, _, state := profitControlAccountUpstreamRate(account, pricingAt); state != profitControlRateDeclared {
		return openAIFallbackFilterReasonUndeclaredRate
	}
	return ""
}

// openAIFallbackFilterReasonUndeclaredRate 是兜底专属的拒绝原因，与利润门自身的
// 原因分开命名，便于在「没有可用账号」的诊断统计里区分是常规门否决还是兜底门否决。
const openAIFallbackFilterReasonUndeclaredRate = "fallback_rate_undeclared"
