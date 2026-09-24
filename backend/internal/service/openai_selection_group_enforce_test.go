package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// noFallbackGroupRepo 让 openAIGroupMayFallback 能确定"这个分组没配兜底"，
// 从而覆盖闸门真正会触发的那条路径。
type noFallbackGroupRepo struct {
	GroupRepository
	group *Group
}

func (r *noFallbackGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	if r.group != nil && r.group.ID == id {
		return r.group, nil
	}
	return nil, nil
}

func (r *noFallbackGroupRepo) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	return r.GetByID(ctx, id)
}

func newNoFallbackService(groupID int64) *OpenAIGatewayService {
	repo := &noFallbackGroupRepo{group: &Group{
		ID: groupID, Platform: PlatformOpenAI, Status: StatusActive,
		FallbackGroupIDs: nil, FallbackGroupID: nil, // 兜底已取消
	}}
	return &OpenAIGatewayService{
		schedulerSnapshot: &SchedulerSnapshotService{groupRepo: repo},
	}
}

// "选出的账号必须属于服务分组"这条不变量，只在能证明分组没配兜底时才强制执行。
//
// 场景对应 gpt-pro(34)：爸爸已取消它的兜底，此时任何非成员账号都是真越界。
func TestSelectionEscapedRequestedGroup(t *testing.T) {
	group34 := int64(34)
	svc := newNoFallbackService(group34)

	t.Run("没配兜底时非成员账号必须作废", func(t *testing.T) {
		sel := &AccountSelectionResult{
			Account: &Account{ID: 222, GroupIDs: []int64{29, 37}}, Acquired: true,
		}
		require.True(t, svc.selectionEscapedRequestedGroup(
			context.Background(), &group34, "", "", "gpt-5.6-sol", sel),
			"gpt-pro 兜底已取消，222 只属于 29/37，必须判为越界")
	})

	t.Run("作废时必须归还并发槽", func(t *testing.T) {
		released := false
		sel := &AccountSelectionResult{
			Account:     &Account{ID: 212, GroupIDs: []int64{29}},
			Acquired:    true,
			ReleaseFunc: func() { released = true },
		}
		require.True(t, svc.selectionEscapedRequestedGroup(
			context.Background(), &group34, "", "", "gpt-5.6-sol", sel))
		require.True(t, released, "不还槽会把并发位永久占住")
	})

	t.Run("账号属于请求分组时放行", func(t *testing.T) {
		sel := &AccountSelectionResult{Account: &Account{ID: 225, GroupIDs: []int64{34}}}
		require.False(t, svc.selectionEscapedRequestedGroup(
			context.Background(), &group34, "", "", "gpt-5.6-sol", sel))
	})

	t.Run("分组归属未水合时不作废", func(t *testing.T) {
		released := false
		sel := &AccountSelectionResult{
			Account:     &Account{ID: 999},
			ReleaseFunc: func() { released = true },
		}
		require.False(t, svc.selectionEscapedRequestedGroup(
			context.Background(), &group34, "", "", "gpt-5.6-sol", sel),
			"无从判定时必须放行，否则某条路径没水合就会打掉全部正常流量")
		require.False(t, released)
	})

	t.Run("没有分组的请求不参与判定", func(t *testing.T) {
		sel := &AccountSelectionResult{Account: &Account{ID: 222, GroupIDs: []int64{29}}}
		require.False(t, svc.selectionEscapedRequestedGroup(
			context.Background(), nil, "", "", "gpt-5.6-sol", sel))
	})

	t.Run("空选号结果不崩", func(t *testing.T) {
		require.False(t, svc.selectionEscapedRequestedGroup(
			context.Background(), &group34, "", "", "m", nil))
		require.False(t, svc.selectionEscapedRequestedGroup(
			context.Background(), &group34, "", "", "m", &AccountSelectionResult{}))
	})
}
