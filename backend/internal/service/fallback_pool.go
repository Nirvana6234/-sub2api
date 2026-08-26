package service

import (
	"context"
	"log/slog"
	"time"
)

// 分组兜底池的共享内核。
//
// OpenAI/Grok 与 Anthropic 两条链路此前各有一份几乎逐函数对应的拷贝：遍历算法、
// visited 防环、跳数上限、以及「兜底比常规更严」的准入规则都一模一样。差异只有两处，
// 且都不在算法里：一是分组怎么读出来（OpenAI 走 schedulerSnapshot，Gateway 走
// groupRepo），二是平台怎么比较（OpenAI 要先归一化兼容平台）。这里把算法收成唯一
// 实现，差异部分由调用方注入。
//
// 有一件事刻意没有合并：两侧各自的 sourcing / state ctx key。
// openai_account_scheduler 用 sourcing 标记参与缓存命中和分组判断
// （见 openai_account_scheduler.go 里 isOpenAIFallbackPoolSourcing 的两处分支），
// 一旦共用同一个 key，Anthropic 的兜底就会点亮 OpenAI 调度器的那些分支。
// 两侧标记语义相同但作用域必须隔离，所以 key 留在各自的包装函数里。

const fallbackGroupMaxHops = 3

// fallbackGroupState 记录兜底链路已经过的分组与跳数，用于防环和限制深度。
type fallbackGroupState struct {
	visited map[int64]struct{}
	hops    int
}

func cloneFallbackVisited(in map[int64]struct{}) map[int64]struct{} {
	out := make(map[int64]struct{}, len(in)+1)
	for id := range in {
		out[id] = struct{}{}
	}
	return out
}

// fallbackTraversal 是一次兜底跳转需要调用方提供的全部平台相关部件。
type fallbackTraversal struct {
	// logNS 区分日志来源（"openai" / "gateway"），便于在告警里定位是哪条链路。
	logNS string
	// resolveGroup 按 ID 读出分组，读不到返回 nil。
	resolveGroup func(ctx context.Context, groupID int64) *Group
	// currentGroupOK 判断源分组本身是否具备发起兜底的资格。
	currentGroupOK func(group *Group) bool
	// fallbackGroupOK 判断目标分组是否可以作为兜底池被借号。
	// 返回的第二个值是拒绝原因，非空时会以 warn 记录。
	fallbackGroupOK func(group *Group) (bool, string)
}

// nextFallbackGroupID 计算兜底链路的下一跳。
//
// 返回 (下一跳分组 ID, 推进后的状态, 是否成功)。不成功时状态无意义，调用方应原样
// 返回原 ctx —— 不要把失败的一跳写进 ctx，否则 visited 会被污染。
func nextFallbackGroupID(
	ctx context.Context,
	currentGroupID int64,
	state fallbackGroupState,
	t fallbackTraversal,
) (int64, fallbackGroupState, bool) {
	visited := cloneFallbackVisited(state.visited)
	if _, seen := visited[currentGroupID]; seen {
		slog.Warn(t.logNS+"_fallback_group_cycle_detected", "group_id", currentGroupID)
		return 0, state, false
	}
	visited[currentGroupID] = struct{}{}

	if state.hops >= fallbackGroupMaxHops {
		slog.Warn(t.logNS+"_fallback_group_max_hops_reached",
			"group_id", currentGroupID, "max_hops", fallbackGroupMaxHops)
		return 0, state, false
	}

	currentGroup := t.resolveGroup(ctx, currentGroupID)
	if currentGroup == nil || !t.currentGroupOK(currentGroup) {
		return 0, state, false
	}
	if currentGroup.FallbackGroupID == nil || *currentGroup.FallbackGroupID <= 0 {
		return 0, state, false
	}

	fallbackID := *currentGroup.FallbackGroupID
	if _, seen := visited[fallbackID]; seen {
		slog.Warn(t.logNS+"_fallback_group_cycle_detected",
			"group_id", currentGroupID, "fallback_group_id", fallbackID)
		return 0, state, false
	}

	fallbackGroup := t.resolveGroup(ctx, fallbackID)
	if fallbackGroup == nil {
		return 0, state, false
	}
	if ok, reason := t.fallbackGroupOK(fallbackGroup); !ok {
		if reason != "" {
			slog.Warn(t.logNS+"_fallback_group_invalid_target",
				"group_id", currentGroupID,
				"fallback_group_id", fallbackID,
				"reason", reason,
				"fallback_platform", fallbackGroup.Platform,
				"is_fallback_pool", fallbackGroup.IsFallbackPool)
		}
		return 0, state, false
	}

	return fallbackID, fallbackGroupState{visited: visited, hops: state.hops + 1}, true
}

// fallbackPoolRejectReasonWhenSourcing 报告某账号在兜底取号时是否应被拒绝。
//
// 兜底比常规调度只多这一条：成本从未声明过的账号不参与兜底。
//
// 常规调度对未声明成本的账号是「放行并告警」，理由是利润门没有可依据的事实，不该
// 替运营假设一个成本再据此否决。那个取舍在常规路径上成立——拦掉它等于让本来该被
// 服务的请求直接失败。
//
// 但兜底是额外的救济路径：这些账号本不属于目标分组，是被临时借调过来的。拿一个不
// 知道成本的号去顶，等于用未知成本换未知收益，而代价落在目标分组的利润上。宁可不
// 兜底，也不做这种交易。
//
// sourcing 由调用方按自己那条链路的 ctx key 判定后传入。
func fallbackPoolRejectReasonWhenSourcing(ctx context.Context, account *Account, sourcing bool) string {
	if !sourcing || account == nil {
		return ""
	}
	// 定价时刻取自已装配的利润门，保证与门内阈值用的是同一个 D 侧时刻；
	// 取不到就留零值，profitControlAccountUpstreamRate 会回退到当前时间。
	var pricingAt time.Time
	if gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate); gate != nil {
		pricingAt = gate.pricingAt
	}
	if _, _, state := profitControlAccountUpstreamRate(account, pricingAt); state != profitControlRateDeclared {
		return fallbackFilterReasonUndeclaredRate
	}
	return ""
}

// fallbackFilterReasonUndeclaredRate 是兜底专属的拒绝原因，与利润门自身的原因分开
// 命名，便于在「没有可用账号」的诊断统计里区分是常规门否决还是兜底门否决。
const fallbackFilterReasonUndeclaredRate = "fallback_rate_undeclared"

// platformSupportsFallbackPool 报告某平台是否已接入分组兜底池。
//
// 兜底的管道本身是平台无关的（钩子挂在共享的选号入口上），这里只是一份显式白名单：
// 每接入一个平台都要确认它的利润门、分组校验和前端选择器都跟上了，而不是靠管道能跑
// 就默认开放。
func platformSupportsFallbackPool(platform string) bool {
	switch platform {
	case PlatformOpenAI, PlatformGrok, PlatformAnthropic, PlatformGemini:
		return true
	default:
		return false
	}
}
