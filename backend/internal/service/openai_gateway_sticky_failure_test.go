package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 回归测试：粘性会话绑定的账号如果连续在“上游 response.failed 终结性失败”这条
// 分支上摔跤（典型如中转商自己的排队/调度超时），账号本身又没被判定为不可调度，
// 之前会一直粘着重复失败，只能靠用户新开对话才能摆脱。达到
// openaiStickySessionFailureUnstickThreshold 次连续失败后应主动解绑。
func TestRecordStickySessionFailure_UnsticksAfterThreshold(t *testing.T) {
	cache := &stubGatewayCache{sessionBindings: map[string]int64{"sess-a": 174}}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(20)

	svc.RecordStickySessionFailure(context.Background(), &groupID, "sess-a", 174)
	require.Equal(t, int64(174), cache.sessionBindings["sess-a"], "第一次失败不应该解绑")
	require.Zero(t, cache.deletedSessions["sess-a"])

	svc.RecordStickySessionFailure(context.Background(), &groupID, "sess-a", 174)
	_, stillBound := cache.sessionBindings["sess-a"]
	require.False(t, stillBound, "连续第二次失败应该解绑粘性会话")
	require.Equal(t, 1, cache.deletedSessions["sess-a"])
	require.Zero(t, cache.failureCounts["sess-a"], "解绑后失败计数应该清零")
}

// 请求成功应该清零失败计数——不能让很久以前的一次失败和今天的一次失败叠加触发解绑。
func TestRecordStickySessionSuccess_ResetsFailureCounter(t *testing.T) {
	cache := &stubGatewayCache{sessionBindings: map[string]int64{"sess-a": 174}}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(20)

	svc.RecordStickySessionFailure(context.Background(), &groupID, "sess-a", 174)
	require.Equal(t, int64(1), cache.failureCounts["sess-a"])

	svc.RecordStickySessionSuccess(context.Background(), &groupID, "sess-a")
	require.Zero(t, cache.failureCounts["sess-a"], "请求成功应该清空失败连续计数")

	svc.RecordStickySessionFailure(context.Background(), &groupID, "sess-a", 174)
	_, stillBound := cache.sessionBindings["sess-a"]
	require.True(t, stillBound, "成功之后紧接着的一次失败不应该单独触发解绑")
}

func TestRecordStickySessionFailure_NoopWithoutSessionHash(t *testing.T) {
	cache := &stubGatewayCache{sessionBindings: map[string]int64{"": 174}}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(20)

	svc.RecordStickySessionFailure(context.Background(), &groupID, "", 174)
	require.Zero(t, cache.failureCounts[""])
}

func TestRecordStickySessionFailure_NoopWithoutTrackerSupport(t *testing.T) {
	// 不实现 StickySessionFailureTracker 的缓存（现有大部分测试桩）必须被安全忽略，
	// 不能因为类型断言失败就 panic。
	cache := &comboCacheAndStore{}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(20)

	require.NotPanics(t, func() {
		svc.RecordStickySessionFailure(context.Background(), &groupID, "sess-a", 174)
		svc.RecordStickySessionSuccess(context.Background(), &groupID, "sess-a")
	})
}
