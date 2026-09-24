package service

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
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

// fallbackGroupMaxAttempts 是一次选号里兜底分组的总尝试预算。
//
// fallbackGroupMaxHops 只限深度不限宽度：宽度 b、深度 3 的配置最坏要串行选号
// 1+b+b²+b³ 次，每次都在拖首字时间。兜底是救急路径，试到这个数还没号，
// 再往下试的收益远小于它给这次请求增加的等待。
const fallbackGroupMaxAttempts = 6

// fallbackAttemptLedger 是一次选号请求内共享的兜底尝试记录。
//
// visited 沿链路向下传递，兄弟分支之间互不可见：A→[B,C]、B→D、C→D 时，D 会在
// B 和 C 两条分支下各被试一次。账本挂在请求 ctx 上、以指针共享，让任何分支都
// 能看到别的分支已经试过哪些池子。
//
// 记的是「在多深的位置试过」：同一个池子若以更浅的深度再次到达，仍允许再试，
// 因为更浅的位置还剩更多跳数，能展开此前被深度上限截断的下游。
type fallbackAttemptLedger struct {
	mu       sync.Mutex
	depth    map[int64]int
	attempts int
}

type fallbackAttemptLedgerCtxKey struct{}

// withFallbackAttemptLedger 为本次选号装上账本；已有账本时原样返回，保证嵌套的
// 选号包装层（legacy 路径里有好几层各自带兜底的入口）共用同一本。
func withFallbackAttemptLedger(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(fallbackAttemptLedgerCtxKey{}).(*fallbackAttemptLedger); ok {
		return ctx
	}
	return context.WithValue(ctx, fallbackAttemptLedgerCtxKey{}, &fallbackAttemptLedger{depth: make(map[int64]int)})
}

// claimFallbackAttempt 报告这个兜底目标能否在本次请求里尝试，能则立即记账。
// 没有账本（不经选号入口的内部调用、单元测试）时一律放行，保持旧行为。
func claimFallbackAttempt(ctx context.Context, logNS string, groupID int64, hops int) bool {
	if ctx == nil {
		return true
	}
	ledger, _ := ctx.Value(fallbackAttemptLedgerCtxKey{}).(*fallbackAttemptLedger)
	if ledger == nil {
		return true
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if depth, seen := ledger.depth[groupID]; seen && depth <= hops {
		return false
	}
	if ledger.attempts >= fallbackGroupMaxAttempts {
		slog.Warn(logNS+"_fallback_group_attempt_budget_exhausted",
			"fallback_group_id", groupID, "max_attempts", fallbackGroupMaxAttempts)
		return false
	}
	ledger.depth[groupID] = hops
	ledger.attempts++
	return true
}

// preferNoAccountError 在多个池子都取不到号时决定对外报哪个错误。
//
// handler 只从错误里读两类信息：带 rate_limited=N 的诊断会被分类成 429（稍后重试
// 可能成功），ErrNoAvailableCompactAccounts 会被分类成「不支持 compact」。普通的
// 「没号」不携带额外信息。所以按信息量取优先级最高的那个，同级保留先出现的——
// 否则最后一个空池的普通错误会把前面池子的限流诊断盖掉，429 变成 503。
func preferNoAccountError(current, candidate error) error {
	if current == nil {
		return candidate
	}
	if candidate == nil {
		return current
	}
	if noAccountErrorPriority(candidate) > noAccountErrorPriority(current) {
		return candidate
	}
	return current
}

var noAccountRateLimitedPattern = regexp.MustCompile(`(?:model_rate_limited|rate_limited)=([1-9]\d*)`)

func noAccountErrorPriority(err error) int {
	switch {
	case err == nil:
		return -1
	case noAccountRateLimitedPattern.MatchString(strings.ToLower(err.Error())):
		return 2
	case errors.Is(err, ErrNoAvailableCompactAccounts):
		return 1
	default:
		return 0
	}
}

// fallbackGroupState 记录兜底链路已经过的分组与跳数，用于防环和限制深度。
type fallbackGroupState struct {
	visited         map[int64]struct{}
	hops            int
	originGroupID   int64
	originGroupName string
	targetGroupID   int64
	targetGroupName string
}

type fallbackPoolUsageTrace struct {
	SourceGroupID   int64
	SourceGroupName string
	TargetGroupID   int64
	TargetGroupName string
}

type fallbackPoolUsageTraceContextKey struct{}

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

// fallbackHop 是当前分组的一个兜底目标，以及进入它时应写进 ctx 的链路状态。
type fallbackHop struct {
	groupID int64
	state   fallbackGroupState
}

// nextFallbackGroupID 计算兜底链路的下一跳，即 fallbackGroupHops 的第一个目标。
//
// 返回 (下一跳分组 ID, 推进后的状态, 是否成功)。不成功时状态无意义，调用方应原样
// 返回原 ctx —— 不要把失败的一跳写进 ctx，否则 visited 会被污染。
func nextFallbackGroupID(
	ctx context.Context,
	currentGroupID int64,
	state fallbackGroupState,
	t fallbackTraversal,
) (int64, fallbackGroupState, bool) {
	hops := fallbackGroupHops(ctx, currentGroupID, state, t)
	if len(hops) == 0 {
		return 0, state, false
	}
	return hops[0].groupID, hops[0].state, true
}

// fallbackGroupHops 按配置顺序返回当前分组全部可准入的兜底目标。
//
// 调用方应逐个尝试：前一个目标（连同它自己的下游链路）取不到号，再试下一个。
// 历史实现只返回第一个通过准入的目标，它没号时递归走的是「它自己的」兜底配置，
// 当前分组配置里排在后面的目标永远轮不到。
//
// 每个目标的 visited 里都预先放入它的兄弟目标：兄弟由本层按顺序亲自尝试，
// 下游链路不必、也不应再绕过去——那样同一个池子会在一次请求里被试两遍，
// 而且多占一跳。
func fallbackGroupHops(
	ctx context.Context,
	currentGroupID int64,
	state fallbackGroupState,
	t fallbackTraversal,
) []fallbackHop {
	visited := cloneFallbackVisited(state.visited)
	if _, seen := visited[currentGroupID]; seen {
		slog.Warn(t.logNS+"_fallback_group_cycle_detected", "group_id", currentGroupID)
		return nil
	}
	visited[currentGroupID] = struct{}{}

	if state.hops >= fallbackGroupMaxHops {
		slog.Warn(t.logNS+"_fallback_group_max_hops_reached",
			"group_id", currentGroupID, "max_hops", fallbackGroupMaxHops)
		return nil
	}

	currentGroup := t.resolveGroup(ctx, currentGroupID)
	if currentGroup == nil || !t.currentGroupOK(currentGroup) {
		return nil
	}
	fallbackGroupIDs := normalizeFallbackGroupIDs(currentGroup.FallbackGroupIDs, currentGroup.FallbackGroupID)
	if len(fallbackGroupIDs) == 0 {
		return nil
	}

	admitted := make([]*Group, 0, len(fallbackGroupIDs))
	for _, candidateID := range fallbackGroupIDs {
		if _, seen := visited[candidateID]; seen {
			slog.Warn(t.logNS+"_fallback_group_cycle_detected",
				"group_id", currentGroupID, "fallback_group_id", candidateID)
			continue
		}
		candidate := t.resolveGroup(ctx, candidateID)
		if candidate == nil {
			continue
		}
		if ok, reason := t.fallbackGroupOK(candidate); !ok {
			if reason != "" {
				slog.Warn(t.logNS+"_fallback_group_invalid_target",
					"group_id", currentGroupID,
					"fallback_group_id", candidateID,
					"reason", reason,
					"fallback_platform", candidate.Platform,
					"is_fallback_pool", candidate.IsFallbackPool)
			}
			continue
		}
		admitted = append(admitted, candidate)
	}
	if len(admitted) == 0 {
		return nil
	}

	originID := state.originGroupID
	originName := state.originGroupName
	if originID <= 0 {
		originID = currentGroup.ID
		originName = currentGroup.Name
	}

	hops := make([]fallbackHop, 0, len(admitted))
	for _, target := range admitted {
		hopVisited := cloneFallbackVisited(visited)
		for _, sibling := range admitted {
			if sibling.ID != target.ID {
				hopVisited[sibling.ID] = struct{}{}
			}
		}
		hops = append(hops, fallbackHop{
			groupID: target.ID,
			state: fallbackGroupState{
				visited:         hopVisited,
				hops:            state.hops + 1,
				originGroupID:   originID,
				originGroupName: originName,
				targetGroupID:   target.ID,
				targetGroupName: target.Name,
			},
		})
	}
	return hops
}

func fallbackPoolUsageTraceFromState(state fallbackGroupState) (fallbackPoolUsageTrace, bool) {
	if state.originGroupID <= 0 || state.targetGroupID <= 0 {
		return fallbackPoolUsageTrace{}, false
	}
	return fallbackPoolUsageTrace{
		SourceGroupID:   state.originGroupID,
		SourceGroupName: state.originGroupName,
		TargetGroupID:   state.targetGroupID,
		TargetGroupName: state.targetGroupName,
	}, true
}

// fallbackPoolUsageTraceFromContext 读取本次请求命中的兜底事实。优先读通用 key
// （由 withFallbackPoolUsageTrace 写入，用于跨 goroutine 的 detached worker context），
// 否则回退到 OpenAI/Gateway 各自链路在请求 ctx 上挂的 key。
func fallbackPoolUsageTraceFromContext(ctx context.Context) (fallbackPoolUsageTrace, bool) {
	if ctx == nil {
		return fallbackPoolUsageTrace{}, false
	}
	if trace, ok := ctx.Value(fallbackPoolUsageTraceContextKey{}).(fallbackPoolUsageTrace); ok {
		return trace, trace.SourceGroupID > 0 && trace.TargetGroupID > 0
	}
	if trace, ok := openAIFallbackPoolUsageTraceFromContext(ctx); ok {
		return trace, true
	}
	if trace, ok := gatewayFallbackPoolUsageTraceFromContext(ctx); ok {
		return trace, true
	}
	return fallbackPoolUsageTrace{}, false
}

func withFallbackPoolUsageTrace(ctx context.Context, trace fallbackPoolUsageTrace) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, fallbackPoolUsageTraceContextKey{}, trace)
}

// PropagateFallbackPoolUsageContext copies the immutable fallback fact from a
// request context into a detached usage-record worker context.
func PropagateFallbackPoolUsageContext(parent, base context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	if trace, ok := fallbackPoolUsageTraceFromContext(parent); ok {
		return withFallbackPoolUsageTrace(base, trace)
	}
	return base
}

func applyFallbackPoolUsageTrace(log *UsageLog, trace fallbackPoolUsageTrace, ok bool) {
	if log == nil || !ok || trace.SourceGroupID <= 0 || trace.TargetGroupID <= 0 {
		return
	}
	log.FallbackPoolUsed = true
	log.FallbackSourceGroupID = fallbackPoolInt64Ptr(trace.SourceGroupID)
	log.FallbackTargetGroupID = fallbackPoolInt64Ptr(trace.TargetGroupID)
	log.FallbackSourceGroupName = optionalTrimmedStringPtr(trace.SourceGroupName)
	log.FallbackTargetGroupName = optionalTrimmedStringPtr(trace.TargetGroupName)
}

func fallbackPoolInt64Ptr(v int64) *int64 {
	return &v
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
