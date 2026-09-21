package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 兜底事实只存在于调度栈内部的局部 ctx（nextOpenAIFallbackGroup 返回的
// fallbackCtx 随函数栈一起丢弃），handler 手里的请求 ctx 从来看不到它。
// 而记用量跑在 detached worker 上，usageRecordContext →
// PropagateFallbackPoolUsageContext 只能从请求 ctx 搬运。
//
// 缺了「选号结果携带 + handler 重放」这一环，搬到的就是空的：生产上
// usage_logs 的 fallback_* 字段一直正常写到 2026-09-10，之后三天全为 NULL
// （1787 条历史记录之后再无新增），兜底告警也跟着失效。
//
// 修法沿用利润门早就用过的同款范式（profitGate + ContextWithSelectionProfitGate）。
func TestSelectionCarriesFallbackTraceOutOfSchedulerStack(t *testing.T) {
	// 调度栈内部的 ctx：带着「20 号组兜底到 29 号组」这个事实。
	schedulerCtx := withOpenAIFallbackGroupState(context.Background(), fallbackGroupState{
		originGroupID:   20,
		originGroupName: "plus-free",
		targetGroupID:   29,
		targetGroupName: "puls-兜底",
		hops:            1,
	})

	t.Run("选号结果必须带出兜底事实", func(t *testing.T) {
		sel := attachSelectionProfitGate(schedulerCtx, &AccountSelectionResult{Account: &Account{ID: 221}})
		require.NotNil(t, sel)
		require.NotNil(t, sel.fallbackTrace,
			"兜底事实不随返回值带出调度栈，handler 就无从得知这次走了兜底")
		require.Equal(t, int64(20), sel.fallbackTrace.SourceGroupID)
		require.Equal(t, int64(29), sel.fallbackTrace.TargetGroupID)
	})

	t.Run("重放到请求ctx后用量日志才能拿到", func(t *testing.T) {
		sel := attachSelectionProfitGate(schedulerCtx, &AccountSelectionResult{Account: &Account{ID: 221}})

		// handler 手里的请求 ctx：干净的，没有任何兜底状态。
		requestCtx := context.Background()
		_, ok := fallbackPoolUsageTraceFromContext(requestCtx)
		require.False(t, ok, "构造前提：请求 ctx 本身看不到兜底事实")

		replayed := ContextWithSelectionFallbackTrace(requestCtx, sel)

		// detached worker 的 ctx 由 PropagateFallbackPoolUsageContext 从请求 ctx 搬运。
		workerCtx := PropagateFallbackPoolUsageContext(replayed, context.Background())
		trace, ok := fallbackPoolUsageTraceFromContext(workerCtx)
		require.True(t, ok, "搬运后 worker 必须看得到兜底事实")

		log := &UsageLog{}
		applyFallbackPoolUsageTrace(log, trace, ok)
		require.True(t, log.FallbackPoolUsed)
		require.NotNil(t, log.FallbackSourceGroupID)
		require.Equal(t, int64(20), *log.FallbackSourceGroupID)
		require.NotNil(t, log.FallbackTargetGroupID)
		require.Equal(t, int64(29), *log.FallbackTargetGroupID)
		require.NotNil(t, log.FallbackSourceGroupName)
		require.Equal(t, "plus-free", *log.FallbackSourceGroupName)
		require.NotNil(t, log.FallbackTargetGroupName)
		require.Equal(t, "puls-兜底", *log.FallbackTargetGroupName)
	})

	t.Run("粘性兜底链路同样带出事实", func(t *testing.T) {
		// 延迟触发的粘性兜底走的是另一条 ctx 装配路径，也必须被捕获。
		stickyCtx := withOpenAIStickyFallbackContext(context.Background(), 20, 29)
		sel := attachSelectionProfitGate(stickyCtx, &AccountSelectionResult{Account: &Account{ID: 221}})
		require.NotNil(t, sel.fallbackTrace)
		require.Equal(t, int64(20), sel.fallbackTrace.SourceGroupID)
		require.Equal(t, int64(29), sel.fallbackTrace.TargetGroupID)
	})

	t.Run("没走兜底就不该留痕", func(t *testing.T) {
		sel := attachSelectionProfitGate(context.Background(), &AccountSelectionResult{Account: &Account{ID: 175}})
		require.Nil(t, sel.fallbackTrace, "常规选号不得被标成兜底")

		requestCtx := context.Background()
		require.Equal(t, requestCtx, ContextWithSelectionFallbackTrace(requestCtx, sel),
			"无兜底事实时不得污染 ctx")

		log := &UsageLog{}
		trace, ok := fallbackPoolUsageTraceFromContext(ContextWithSelectionFallbackTrace(requestCtx, sel))
		applyFallbackPoolUsageTrace(log, trace, ok)
		require.False(t, log.FallbackPoolUsed)
		require.Nil(t, log.FallbackSourceGroupID)
	})
}
