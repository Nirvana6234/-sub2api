package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stickyPassiveAccountRepo struct {
	AccountRepository
	accounts []Account
	calls    int
}

func (r *stickyPassiveAccountRepo) ListModelAvailabilityCandidates(
	_ context.Context, groupID *int64, _ []string, _ bool,
) ([]Account, error) {
	r.calls++
	if groupID == nil {
		return nil, nil
	}
	return r.accounts, nil
}

func stickyPassiveTestService(accountIDs ...int64) (*OpenAIGatewayService, *stickyPassiveAccountRepo) {
	accounts := make([]Account, 0, len(accountIDs))
	for _, id := range accountIDs {
		accounts = append(accounts, Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey})
	}
	repo := &stickyPassiveAccountRepo{accounts: accounts}
	return &OpenAIGatewayService{accountRepo: repo}, repo
}

// observeHealthy 灌够 openAILatencyMinSamples 个样本，让 AccountTail 给出读数。
func observeHealthy(svc *OpenAIGatewayService, accountID int64, ttftMs int) {
	tracker := svc.getOpenAILatencyTracker()
	for i := 0; i < openAILatencyMinSamples; i++ {
		tracker.ObserveAccount(accountID, openAILatencyBucketNormal, ttftMs)
	}
}

// 源组粘在兜底组期间，它的账号往往还在给别的分组干活（生产上 plus-free 的
// 175/183 同时属于 plus）。那些请求已经产生了账号级延迟样本，没必要再每 10 分钟
// 拿一个真实用户请求去撞一次墙、还要连撞 2 次成功才肯切回来。
//
// 判定口径：源组可调度账号中 p90 <= 阈值×0.8 的必须过半。留 20% 余量是防抖——
// 恰好卡在阈值上的组会「切回去→立刻超时→再兜底」反复横跳。
func TestOpenAIFallbackStickyRecoversFromPassiveSamples(t *testing.T) {
	setup := func(t *testing.T) {
		resetOpenAIAdvancedSchedulerSettingCacheForTest()
		openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
			latencyAwareFallbackEnabled: true, latencyThresholdMs: 30000,
			fallbackSpeedupRatio: 0.6, expiresAt: time.Now().Add(time.Hour).UnixNano(),
		})
		t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	}

	t.Run("过半账号健康立即切回不等冷却", func(t *testing.T) {
		setup(t)
		svc, _ := stickyPassiveTestService(175, 183)
		svc.markOpenAIFallbackSticky(20, 29, openAILatencyBucketNormal)

		// 两个号都被别的分组用过，首字 5 秒，远低于 30 秒×0.8=24 秒。
		observeHealthy(svc, 175, 5000)
		observeHealthy(svc, 183, 5000)

		got, probing := svc.openAIStickyFallbackCandidate(context.Background(), 20)
		require.Equal(t, int64(20), got, "源组已恢复就该直接切回源组")
		require.False(t, probing, "被动恢复不需要再走主动探测")
		_, stillSticky := svc.openaiFallbackStickyStates.Load(int64(20))
		require.False(t, stillSticky, "粘性关系必须被清除")
	})

	t.Run("只有少数健康仍留在兜底组", func(t *testing.T) {
		setup(t)
		svc, _ := stickyPassiveTestService(175, 183, 97)
		svc.markOpenAIFallbackSticky(20, 29, openAILatencyBucketNormal)

		observeHealthy(svc, 175, 5000)  // 健康
		observeHealthy(svc, 183, 90000) // 仍然很慢
		observeHealthy(svc, 97, 90000)  // 仍然很慢

		got, _ := svc.openAIStickyFallbackCandidate(context.Background(), 20)
		require.Equal(t, int64(29), got, "1/3 健康不算恢复，必须继续兜底")
	})

	t.Run("卡在阈值边界不切回防止横跳", func(t *testing.T) {
		setup(t)
		svc, _ := stickyPassiveTestService(175, 183)
		svc.markOpenAIFallbackSticky(20, 29, openAILatencyBucketNormal)

		// 28 秒：低于 30 秒阈值，但高于 24 秒的余量线。
		observeHealthy(svc, 175, 28000)
		observeHealthy(svc, 183, 28000)

		got, _ := svc.openAIStickyFallbackCandidate(context.Background(), 20)
		require.Equal(t, int64(29), got,
			"仅仅勉强达标不足以切回：切回去大概率立刻又超时，来回横跳由真实用户买单")
	})

	t.Run("没有样本的账号计入分母", func(t *testing.T) {
		setup(t)
		svc, _ := stickyPassiveTestService(175, 183, 97, 207)
		svc.markOpenAIFallbackSticky(20, 29, openAILatencyBucketNormal)

		// 只有两个号有读数，另外两个从没被用过——2/4 不过半。
		observeHealthy(svc, 175, 3000)
		observeHealthy(svc, 183, 3000)

		got, _ := svc.openAIStickyFallbackCandidate(context.Background(), 20)
		require.Equal(t, int64(29), got,
			"没人用过的号不能当作健康，否则一个空池会被判成已恢复")
	})

	t.Run("账号清单按TTL缓存不打爆数据库", func(t *testing.T) {
		setup(t)
		svc, repo := stickyPassiveTestService(175, 183)
		svc.markOpenAIFallbackSticky(20, 29, openAILatencyBucketNormal)
		observeHealthy(svc, 175, 90000)
		observeHealthy(svc, 183, 90000)

		for i := 0; i < 5; i++ {
			svc.openAIStickyFallbackCandidate(context.Background(), 20)
		}
		require.Equal(t, 1, repo.calls, "TTL 内只应查一次账号清单")
	})

	t.Run("没有账号仓库时退回主动探测", func(t *testing.T) {
		setup(t)
		svc := &OpenAIGatewayService{}
		svc.markOpenAIFallbackSticky(20, 29, openAILatencyBucketNormal)

		got, probing := svc.openAIStickyFallbackCandidate(context.Background(), 20)
		require.Equal(t, int64(29), got)
		require.False(t, probing, "冷却未到仍留在兜底组，行为与改动前一致")
	})
}

// 被动恢复必须同时清掉源分组陈旧的分组级延迟窗口，否则会出现"每分钟恢复一次、
// 每次立刻被打回"的抖动，流量实际始终停在兜底池。
//
// 2026-09-15 生产实况：plus-free(20) 近一小时 51 个请求全部落在兜底池 29 的
// 账号上，原生号 175/183 一次没用到——而它们的首字 p90 只有 14 秒，比兜底池的
// 65-95 秒快 5 倍。日志显示 recovered_passively 每分钟触发一次（healthy 2/3），
// 恢复确实发生了，但 shouldTriggerOpenAILatencyFallback 读到 group 20 冻结在
// 触发时刻的慢读数，下一个请求立刻又把它踢回兜底。
//
// 自锁的关键：observeOpenAILatency 按 servingGroupID 记样本，被兜底期间样本全
// 记到兜底组，源分组的窗口永远刷新不了——被兜底这件事本身阻止了能证明它已恢复
// 的证据产生。
func TestOpenAIFallbackStickyPassiveRecoveryClearsStaleGroupWindow(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		latencyAwareFallbackEnabled: true, latencyThresholdMs: 30000,
		fallbackSpeedupRatio: 0.6, expiresAt: time.Now().Add(time.Hour).UnixNano(),
	})
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

	svc, _ := stickyPassiveTestService(175, 183)
	svc.markOpenAIFallbackSticky(20, 29, openAILatencyBucketNormal)

	// 分组级窗口停留在触发兜底那一刻的慢读数（90 秒），并且不会再更新。
	tracker := svc.getOpenAILatencyTracker()
	for i := 0; i < openAILatencyMinSamples; i++ {
		tracker.ObserveGroup(20, openAILatencyBucketNormal, 90000)
	}
	tail, _, ok := tracker.GroupTail(20)
	require.True(t, ok)
	require.Equal(t, 90000, tail, "构造前提：分组级窗口确实是陈旧的慢读数")

	// 账号级证据：原生号其实很快（对应生产的 p90 14 秒）。
	observeHealthy(svc, 175, 5000)
	observeHealthy(svc, 183, 5000)

	got, _ := svc.openAIStickyFallbackCandidate(context.Background(), 20)
	require.Equal(t, int64(20), got, "账号级证据成立就该切回源组")

	_, _, stillHasReading := tracker.GroupTail(20)
	require.False(t, stillHasReading,
		"陈旧的分组级慢读数必须一并清掉，否则下一个请求会立刻把流量又踢回兜底池")

	// 清掉之后不得再触发兜底——这正是抖动被打断的地方。
	groupID := int64(20)
	_, triggered := svc.shouldTriggerOpenAILatencyFallback(context.Background(), &groupID)
	require.False(t, triggered, "窗口已清空，不得再凭陈旧读数触发兜底")
}
