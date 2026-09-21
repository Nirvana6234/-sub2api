package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// fallbackChainGroupRepo 提供一张真实形态的分组表：plus(2) 兜底到 puls-兜底(29)，
// 29 自己不再兜底，gpt-pro(34) 也没配兜底。
type fallbackChainGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *fallbackChainGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	if group, ok := r.groups[id]; ok {
		return group, nil
	}
	return nil, nil
}

func newProductionFallbackChainService() *OpenAIGatewayService {
	group29 := int64(29)
	repo := &fallbackChainGroupRepo{groups: map[int64]*Group{
		2: {
			ID: 2, Platform: PlatformOpenAI, Status: StatusActive,
			FallbackGroupIDs: []int64{29}, FallbackGroupID: &group29,
		},
		29: {ID: 29, Platform: PlatformOpenAI, Status: StatusActive},
		34: {ID: 34, Platform: PlatformOpenAI, Status: StatusActive},
	}}
	return &OpenAIGatewayService{schedulerSnapshot: &SchedulerSnapshotService{groupRepo: repo}}
}

// 2026-09-20 生产实测的漏洞：gpt-6-astra 请求 plus(2)，落到只属 gpt-pro(34) 的账号
// 320（20:30–20:47 共 10 次，横跨 402 和 11 两个 auto-group Key）。
//
// 旧闸门的判断是「plus 配了兜底 [29] → 无从判定 → 放行」，于是这类越界一次都没拦住，
// 连审计日志都没留下。但 34 既不是请求分组，也不在 2 的兜底链上——无论兜底有没有发生，
// 这个结果都不可能合法。
//
// 可证伪性：把 selectionEscapedRequestedGroup 里 ok==false 分支改回 `return false`，
// 第一个子用例必炸。
func TestSelectionEscapeGuardCatchesAccountOutsideFallbackChain(t *testing.T) {
	svc := newProductionFallbackChainService()
	group2 := int64(2)

	t.Run("落到兜底链之外的分组必须作废", func(t *testing.T) {
		released := false
		sel := &AccountSelectionResult{
			Account:     &Account{ID: 320, GroupIDs: []int64{34}},
			Acquired:    true,
			ReleaseFunc: func() { released = true },
		}
		require.True(t, svc.selectionEscapedRequestedGroup(
			context.Background(), &group2, "", "", "gpt-6-astra", sel),
			"320 只属 gpt-pro(34)，而 plus 的兜底链只有 [29]，这是确定的越界")
		require.True(t, released, "作废时必须归还并发槽")
	})

	t.Run("兜底组的账号是设计内借号，绝不能误伤", func(t *testing.T) {
		released := false
		sel := &AccountSelectionResult{
			Account:     &Account{ID: 212, GroupIDs: []int64{29}},
			Acquired:    true,
			ReleaseFunc: func() { released = true },
		}
		require.False(t, svc.selectionEscapedRequestedGroup(
			context.Background(), &group2, "sess", "", "gpt-5.6-sol", sel),
			"29 就在 plus 的兜底链上，用它的号是兜底在正常工作")
		require.False(t, released)
	})

	t.Run("请求分组自己的成员当然放行", func(t *testing.T) {
		sel := &AccountSelectionResult{Account: &Account{ID: 120, GroupIDs: []int64{2}}}
		require.False(t, svc.selectionEscapedRequestedGroup(
			context.Background(), &group2, "", "", "gpt-5.6-sol", sel))
	})

	t.Run("同时属于请求分组和兜底组的账号放行", func(t *testing.T) {
		// 生产账号 221 的形态：既是 plus(2) 成员，也在 puls-兜底(29) 里。
		sel := &AccountSelectionResult{Account: &Account{ID: 221, GroupIDs: []int64{2, 29, 37}}}
		require.False(t, svc.selectionEscapedRequestedGroup(
			context.Background(), &group2, "", "", "gpt-5.6-sol", sel))
	})
}

func TestOpenAIAccountOutsideGroupAndFallbacks(t *testing.T) {
	svc := newProductionFallbackChainService()
	ctx := context.Background()

	t.Run("兜底链读不全时一律放行", func(t *testing.T) {
		// 分组 77 不在表里 → GetByID 返回 nil → 集合不完整，不得下越界结论。
		require.False(t, svc.openAIAccountOutsideGroupAndFallbacks(ctx, 77,
			&Account{ID: 1, GroupIDs: []int64{99}}),
			"读不到配置时必须保守放行，宁可漏拦不可误杀")
	})

	t.Run("没有分组快照服务时不下结论", func(t *testing.T) {
		require.False(t, (&OpenAIGatewayService{}).openAIAccountOutsideGroupAndFallbacks(
			ctx, 2, &Account{ID: 1, GroupIDs: []int64{99}}))
	})

	t.Run("空账号不崩", func(t *testing.T) {
		require.False(t, svc.openAIAccountOutsideGroupAndFallbacks(ctx, 2, nil))
	})

	t.Run("AccountGroups 水合的归属同样算数", func(t *testing.T) {
		require.False(t, svc.openAIAccountOutsideGroupAndFallbacks(ctx, 2,
			&Account{ID: 212, AccountGroups: []AccountGroup{{GroupID: 29}}}),
			"归属可能走 AccountGroups 而不是 GroupIDs，两条都要认")
	})
}
