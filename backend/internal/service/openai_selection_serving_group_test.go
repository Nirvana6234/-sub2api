package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 2026-09-20 事故复盘：第一版闸门只从 ctx 判断"是不是兜底"，而兜底状态存在调度栈
// 内部的局部 ctx 上，传不到选号汇合点。结果每一次合法兜底都被判成越界并作废重选，
// 上线后实时打掉了 plus(2) 走兜底池(29) 的正常选号。
//
// 当时那条"兜底不得误伤"的测试用 withOpenAIStickyFallbackContext 人工造了个 ctx，
// 生产里这个 ctx 根本不存在——所以它通过了，却没拦住事故。下面的用例刻意**不**构造
// 那种 ctx，只用生产里真实能拿到的信号（selection.fallbackTrace / 分组兜底配置）。
func TestOpenAISelectionServingGroupID(t *testing.T) {
	svc := &OpenAIGatewayService{}
	ctx := context.Background()

	t.Run("兜底事实由选号结果带出时采信目标组", func(t *testing.T) {
		sel := &AccountSelectionResult{
			Account:                &Account{ID: 212, GroupIDs: []int64{29}},
			fallbackPoolUsageTrace: &fallbackPoolUsageTrace{SourceGroupID: 2, TargetGroupID: 29},
		}
		serving, ok := svc.openAISelectionServingGroupID(ctx, 2, sel)
		require.True(t, ok)
		require.Equal(t, int64(29), serving, "走了兜底就该按兜底组判定归属")
	})

	t.Run("没有兜底迹象且拿不到分组配置时必须放行", func(t *testing.T) {
		// schedulerSnapshot 为 nil —— 读不到配置，无从判定。这正是生产事故的形态：
		// 兜底真的发生了，但 trace 没带出来。此时必须放行，不能作废。
		sel := &AccountSelectionResult{Account: &Account{ID: 212, GroupIDs: []int64{29}}}
		_, ok := svc.openAISelectionServingGroupID(ctx, 2, sel)
		require.False(t, ok,
			"无从判定时必须报告不可信，否则会把合法兜底误杀——这就是 09-20 事故")
	})

	t.Run("空结果不下结论", func(t *testing.T) {
		_, ok := svc.openAISelectionServingGroupID(ctx, 2, nil)
		require.False(t, ok)
	})

	t.Run("无效请求分组不下结论", func(t *testing.T) {
		sel := &AccountSelectionResult{Account: &Account{ID: 1, GroupIDs: []int64{1}}}
		_, ok := svc.openAISelectionServingGroupID(ctx, 0, sel)
		require.False(t, ok)
	})
}

// 闸门本身：配了兜底的分组一律放行（宁可漏拦不可误杀），没配兜底的分组才拦。
func TestSelectionEscapeGuardNeverFiresWhenGroupMayFallback(t *testing.T) {
	svc := &OpenAIGatewayService{} // schedulerSnapshot 为 nil → openAIGroupMayFallback 保守返回 true
	group2 := int64(2)

	released := false
	sel := &AccountSelectionResult{
		Account:     &Account{ID: 212, GroupIDs: []int64{29}}, // 29 组的号，服务 plus(2)
		Acquired:    true,
		ReleaseFunc: func() { released = true },
	}
	require.False(t, svc.selectionEscapedRequestedGroup(
		context.Background(), &group2, "sess", "", "gpt-5.3-codex-spark", sel),
		"plus 配着兜底 [29]，用 29 的号是兜底在正常工作，绝不能作废")
	require.False(t, released, "合法兜底的并发槽不得被释放")
	require.Equal(t, int64(212), sel.Account.ID, "选号结果不得被改动")
}

// 走了兜底但账号连兜底组也不属于——这才是真越界，仍然要拦。
func TestSelectionEscapeGuardStillCatchesEscapeBeyondFallbackTarget(t *testing.T) {
	svc := &OpenAIGatewayService{}
	group2 := int64(2)

	released := false
	sel := &AccountSelectionResult{
		Account:                &Account{ID: 999, GroupIDs: []int64{77}}, // 既不属于 2 也不属于 29
		Acquired:               true,
		ReleaseFunc:            func() { released = true },
		fallbackPoolUsageTrace: &fallbackPoolUsageTrace{SourceGroupID: 2, TargetGroupID: 29},
	}
	require.True(t, svc.selectionEscapedRequestedGroup(
		context.Background(), &group2, "", "", "gpt-5.3-codex-spark", sel),
		"账号连兜底目标组都不属于，是真越界")
	require.True(t, released, "作废时必须归还并发槽")
}
