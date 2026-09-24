package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 一个分组配置了多个兜底池 [B, C] 时，必须按顺序逐个试：B（连同它的下游链路）
// 取不到号，才轮到 C。历史实现只返回第一个通过准入的 B，B 没号时递归走的是
// B 自己的兜底配置，A 配置里的 C 永远轮不到。

func TestFallbackGroupHopsListsEveryAdmittedTargetInOrder(t *testing.T) {
	groups := map[int64]*Group{
		1: {ID: 1, Name: "src", Platform: PlatformOpenAI, Status: StatusActive, FallbackGroupIDs: []int64{2, 3, 4}},
		2: {ID: 2, Name: "b", Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true, FallbackGroupIDs: []int64{3}},
		3: {ID: 3, Name: "c", Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
		4: {ID: 4, Name: "not-pool", Platform: PlatformOpenAI, Status: StatusActive},
	}
	traversal := fallbackTraversal{
		logNS:          "test",
		resolveGroup:   func(_ context.Context, id int64) *Group { return groups[id] },
		currentGroupOK: func(group *Group) bool { return group.Status == StatusActive },
		fallbackGroupOK: func(group *Group) (bool, string) {
			return group.Status == StatusActive && group.IsFallbackPool, ""
		},
	}

	hops := fallbackGroupHops(context.Background(), 1, fallbackGroupState{}, traversal)
	require.Len(t, hops, 2, "未标记兜底池的 4 不得出现")
	require.Equal(t, int64(2), hops[0].groupID)
	require.Equal(t, int64(3), hops[1].groupID)

	for _, hop := range hops {
		require.Equal(t, 1, hop.state.hops)
		require.Equal(t, int64(1), hop.state.originGroupID)
		require.Equal(t, hop.groupID, hop.state.targetGroupID)
		_, selfVisited := hop.state.visited[hop.groupID]
		require.False(t, selfVisited, "目标自身不能预先记为已访问，否则它自己的下游链路会被误判成环")
	}

	// B 的下游配置了 C，但 C 是 B 的兄弟，由源分组这一层亲自尝试，B 的链路不得再绕过去。
	downstream := fallbackGroupHops(context.Background(), 2, hops[0].state, traversal)
	require.Empty(t, downstream, "兄弟目标不应在下游链路里被重复尝试")

	id, _, ok := nextFallbackGroupID(context.Background(), 1, fallbackGroupState{}, traversal)
	require.True(t, ok)
	require.Equal(t, int64(2), id, "nextFallbackGroupID 仍返回第一个目标")
}

// orderedFallbackScheduler 按分组返回结果：noAccounts 里的分组报「没号」，
// 其余分组返回 accounts 里对应的号。calls 记录被调度到的分组顺序。
type orderedFallbackScheduler struct {
	noAccounts map[int64]bool
	accounts   map[int64]*Account
	releases   map[int64]int
	calls      []int64
}

func (s *orderedFallbackScheduler) Select(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	groupID := derefGroupID(req.GroupID)
	s.calls = append(s.calls, groupID)
	if s.noAccounts[groupID] {
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	}
	account := s.accounts[groupID]
	// 与真实调度器一致：经 attachSelectionProfitGate 把兜底事实带出调度栈。
	return attachSelectionProfitGate(ctx, &AccountSelectionResult{
		Account:  account,
		Acquired: true,
		ReleaseFunc: func() {
			s.releases[groupID]++
		},
	}), OpenAIAccountScheduleDecision{SelectedAccountID: account.ID}, nil
}

func (*orderedFallbackScheduler) ReportResult(int64, bool, *int) {}
func (*orderedFallbackScheduler) ReportSwitch()                  {}
func (*orderedFallbackScheduler) SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	return OpenAIAccountSchedulerMetricsSnapshot{}
}

// enableOpenAIAdvancedSchedulerWithoutLatencyFallback 打开高级调度器（否则走 legacy
// 选号路径，不经过 scheduler stub），同时关掉延迟兜底，只测「没号」这条触发路径。
func enableOpenAIAdvancedSchedulerWithoutLatencyFallback(t *testing.T) {
	t.Helper()
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		enabled:   true,
		expiresAt: time.Now().Add(time.Hour).UnixNano(),
	})
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
}

func orderedFallbackOpenAIService(scheduler OpenAIAccountScheduler) *OpenAIGatewayService {
	svc := fallbackTestService(
		&Group{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, FallbackGroupIDs: []int64{20, 30}},
		&Group{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
		&Group{ID: 30, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
	)
	svc.openaiScheduler = scheduler
	return svc
}

func TestOpenAIFallbackTriesSecondPoolWhenFirstHasNoAccounts(t *testing.T) {
	enableOpenAIAdvancedSchedulerWithoutLatencyFallback(t)

	account := &Account{ID: 301, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	scheduler := &orderedFallbackScheduler{
		noAccounts: map[int64]bool{10: true, 20: true},
		accounts:   map[int64]*Account{30: account},
		releases:   map[int64]int{},
	}
	svc := orderedFallbackOpenAIService(scheduler)

	groupID := int64(10)
	selection, _, err := svc.SelectAccountWithScheduler(
		context.Background(), &groupID, "", "", "", nil, OpenAIUpstreamTransportAny, false,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Same(t, account, selection.Account)
	require.Equal(t, []int64{10, 20, 30}, scheduler.calls, "应按 源组 → 第一个兜底池 → 第二个兜底池 的顺序尝试")
	require.NotNil(t, selection.fallbackTrace)
	require.Equal(t, int64(10), selection.fallbackTrace.SourceGroupID)
	require.Equal(t, int64(30), selection.fallbackTrace.TargetGroupID)
}

func TestOpenAIFallbackReportsNoAccountsWhenEveryPoolIsEmpty(t *testing.T) {
	enableOpenAIAdvancedSchedulerWithoutLatencyFallback(t)

	scheduler := &orderedFallbackScheduler{
		noAccounts: map[int64]bool{10: true, 20: true, 30: true},
		releases:   map[int64]int{},
	}
	svc := orderedFallbackOpenAIService(scheduler)

	groupID := int64(10)
	_, _, err := svc.SelectAccountWithScheduler(
		context.Background(), &groupID, "", "", "", nil, OpenAIUpstreamTransportAny, false,
	)

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, []int64{10, 20, 30}, scheduler.calls, "每个兜底池只试一次")
}

// 延迟触发的兜底：源组有号但慢。第一个兜底池没号时要试第二个；换过去后，
// 源组那次选号占的并发槽必须归还。
func TestOpenAILatencyFallbackTriesNextPoolAndReleasesSourceSlot(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		latencyAwareFallbackEnabled: true,
		latencyThresholdMs:          30000,
		fallbackSpeedupRatio:        0.6,
		expiresAt:                   time.Now().Add(time.Hour).UnixNano(),
	})
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

	sourceAccount := &Account{ID: 101, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	fallbackAccount := &Account{ID: 301, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	scheduler := &orderedFallbackScheduler{
		noAccounts: map[int64]bool{20: true},
		accounts:   map[int64]*Account{10: sourceAccount, 30: fallbackAccount},
		releases:   map[int64]int{},
	}
	svc := orderedFallbackOpenAIService(scheduler)
	tracker := svc.getOpenAILatencyTracker()
	for i := 0; i < openAILatencyMinSamples; i++ {
		tracker.ObserveGroup(10, openAILatencyBucketNormal, 93000)
	}

	groupID := int64(10)
	selection, _, err := svc.SelectAccountWithScheduler(
		context.Background(), &groupID, "", "", "", nil, OpenAIUpstreamTransportAny, false,
	)

	require.NoError(t, err)
	require.Same(t, fallbackAccount, selection.Account, "第一个兜底池没号时应落到第二个")
	require.Equal(t, []int64{10, 20, 30}, scheduler.calls)
	require.Equal(t, 1, scheduler.releases[10], "源组选号占的并发槽必须归还")

	value, ok := svc.openaiFallbackStickyStates.Load(int64(10))
	require.True(t, ok, "延迟兜底成功后应粘在兜底池上")
	require.Equal(t, int64(30), value.(*openAIFallbackStickyState).targetGroupID)
}

func TestGatewayFallbackTriesSecondPoolWhenFirstHasNoAccounts(t *testing.T) {
	t.Parallel()

	sourceID, firstID, secondID := int64(130), int64(230), int64(330)
	groups := map[int64]*Group{
		sourceID: {ID: sourceID, Platform: PlatformAnthropic, Status: StatusActive, FallbackGroupIDs: []int64{firstID, secondID}},
		firstID:  {ID: firstID, Platform: PlatformAnthropic, Status: StatusActive, IsFallbackPool: true},
		secondID: {ID: secondID, Platform: PlatformAnthropic, Status: StatusActive, IsFallbackPool: true},
	}
	newService := func() *GatewayService {
		return &GatewayService{
			accountRepo: &anthropicFallbackAccountRepo{byGroup: map[int64][]Account{
				sourceID: nil,
				firstID:  nil,
				secondID: {{ID: 903, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true,
					RateMultiplier: gatewayFallbackFloatPtr(1)}},
			}},
			groupRepo: &anthropicFallbackGroupRepo{groups: groups},
			cfg:       &config.Config{RunMode: config.RunModeStandard},
		}
	}

	account, err := newService().SelectAccountForModelWithExclusions(context.Background(), &sourceID, "", "", nil)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(903), account.ID)

	result, err := newService().SelectAccountWithLoadAwareness(context.Background(), &sourceID, "", "", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Account)
	require.Equal(t, int64(903), result.Account.ID)
}
