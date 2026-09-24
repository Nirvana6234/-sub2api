package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 诊断只观测、不改行为：它必须在"账号确实不属于被请求分组"时才认定异常，
// 且不能因为兜底借号或分组归属缺失而误报——误报会刷屏，反而盖住真问题。
func TestSelectionGroupAuditDetectsOnlyRealMismatch(t *testing.T) {
	svc := &OpenAIGatewayService{}
	groupID := int64(34)

	acctIn34 := &Account{ID: 225, GroupIDs: []int64{34}}
	acctNotIn34 := &Account{ID: 221, GroupIDs: []int64{20, 29}}
	acctNoGroups := &Account{ID: 999}

	t.Run("账号属于该分组时不算异常", func(t *testing.T) {
		require.True(t, openAIStickyAccountMatchesGroup(acctIn34, &groupID))
	})

	t.Run("账号不属于该分组时才算异常", func(t *testing.T) {
		require.False(t, openAIStickyAccountMatchesGroup(acctNotIn34, &groupID),
			"221 只属于 20/29，对 34 必须判为不匹配——这正是生产要定位的现象")
	})

	t.Run("兜底链路不得误报", func(t *testing.T) {
		// 走兜底时账号本来就该来自兜底组，服务分组随之变成 29。
		ctx := withOpenAIStickyFallbackContext(context.Background(), 34, 29)
		serving := OpenAIServingGroupID(ctx, groupID)
		require.Equal(t, int64(29), serving, "兜底后判定应改用服务分组")
		require.True(t, openAIStickyAccountMatchesGroup(acctNotIn34, &serving),
			"221 属于 29，走兜底拿到它是设计内行为，不得报异常")
	})

	t.Run("分组归属未水合时不下结论", func(t *testing.T) {
		// 这类账号无从判定，审计必须跳过而不是报异常。
		require.Empty(t, acctNoGroups.GroupIDs)
		require.Empty(t, acctNoGroups.AccountGroups)
	})

	t.Run("同一分组账号对按分钟节流", func(t *testing.T) {
		require.True(t, svc.shouldLogSelectionGroupAudit(34, 221), "首次必须放行")
		require.False(t, svc.shouldLogSelectionGroupAudit(34, 221), "一分钟内不得重复输出")
		require.True(t, svc.shouldLogSelectionGroupAudit(34, 195), "不同账号各自计时")
	})

	t.Run("审计不修改选号结果", func(t *testing.T) {
		selection := &AccountSelectionResult{Account: acctNotIn34, Acquired: true}
		svc.auditSelectedAccountGroupMembership(
			context.Background(), &groupID, "sess", "", "gpt-6-astra", selection)
		require.Equal(t, acctNotIn34, selection.Account, "诊断不得改动选号结果")
		require.True(t, selection.Acquired)
	})
}
