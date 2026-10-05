package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 「会话粘性绑在一个死号上，用户一直重试一直失败」的回归矩阵。
//
// 死号分两类，处理方式不同：
//  1. 账号状态已经反映出来（禁用 / 报错 / 限流 / 过载 / 冷却 / 过期 / 被删 / 移出分组）：
//     调度必须立刻换号，而且不能把绑定留在死号上，否则下一次请求又先撞它。
//  2. 账号状态看起来正常但请求一直失败（上游挂了、代理断了）：靠错误率逃逸换号，
//     最多容忍有限次失败，不能无限粘下去。

func stickyDeadScenarioService(t *testing.T, groupID int64, stale, fresh []*Account, binding int64, key string) (*OpenAIGatewayService, *schedulerTestGatewayCache) {
	t.Helper()
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{key: binding}}
	byID := make(map[int64]*Account, len(fresh))
	repoAccounts := make([]Account, 0, len(fresh))
	for _, a := range fresh {
		byID[a.ID] = a
		repoAccounts = append(repoAccounts, *a)
	}
	snapshotCache := &openAISnapshotCacheStub{snapshotAccounts: stale, accountsByID: byID}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: repoAccounts},
		cache:              cache,
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		schedulerSnapshot:  &SchedulerSnapshotService{cache: snapshotCache, accountRepo: schedulerTestOpenAIAccountRepo{accounts: repoAccounts}},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
		openaiAccountStats: newOpenAIAccountRuntimeStats(),
	}
	return svc, cache
}

func TestOpenAISticky_DeadBoundAccountIsAbandonedAndBindingLeavesIt(t *testing.T) {
	future := time.Now().Add(30 * time.Minute)
	past := time.Now().Add(-time.Minute)

	deadStates := map[string]func(a *Account){
		"status_error":         func(a *Account) { a.Status = StatusError },
		"status_disabled":      func(a *Account) { a.Status = StatusDisabled },
		"schedulable_off":      func(a *Account) { a.Schedulable = false },
		"rate_limited":         func(a *Account) { a.RateLimitResetAt = &future },
		"overloaded":           func(a *Account) { a.OverloadUntil = &future },
		"temp_unschedulable":   func(a *Account) { a.TempUnschedulableUntil = &future },
		"oauth_token_expired":  func(a *Account) { a.AutoPauseOnExpired = true; a.ExpiresAt = &past },
		"removed_from_group":   func(a *Account) { a.GroupIDs = []int64{999999} },
		"deleted_from_db":      nil, // 账号记录不存在
		"status_error_in_both": func(a *Account) { a.Status = StatusError },
	}

	for name, kill := range deadStates {
		for _, snapshotAlsoShowsDead := range []bool{false, true} {
			if name == "status_error_in_both" && !snapshotAlsoShowsDead {
				continue
			}
			t.Run(fmt.Sprintf("%s/snapshot_dead=%v", name, snapshotAlsoShowsDead), func(t *testing.T) {
				ctx := context.Background()
				groupID := int64(51000)
				const key = "openai:session_dead_sticky"
				mk := func(id int64, priority int) *Account {
					return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: priority, GroupIDs: []int64{groupID}}
				}
				staleA, staleB := mk(51001, 0), mk(51002, 5)
				freshA, freshB := mk(51001, 0), mk(51002, 5)

				var fresh []*Account
				if kill == nil {
					fresh = []*Account{freshB} // A 已被删除
				} else {
					kill(freshA)
					fresh = []*Account{freshA, freshB}
				}
				stale := []*Account{staleA, staleB}
				if snapshotAlsoShowsDead && kill != nil {
					kill(staleA)
				}

				svc, cache := stickyDeadScenarioService(t, groupID, stale, fresh, 51001, key)
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_dead_sticky", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.NotNil(t, selection, "死号不能让请求选不出账号")
				require.NotNil(t, selection.Account)
				require.Equal(t, int64(51002), selection.Account.ID, "必须换到健康账号，不能继续选死号")
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				// 绑定不能还指着死号，否则下一次请求又先撞它
				require.NotEqual(t, int64(51001), cache.sessionBindings[key], "粘性绑定仍然指向死号")
			})
		}
	}
}

// 账号状态正常、但上游一直失败：粘性不能无限粘下去。
// 模拟「客户端每次请求 -> 先撞到粘性账号失败 -> 换号成功」，看要撞几次才不再撞。
func TestOpenAISticky_FailingButSchedulableAccountIsEscapedAfterFewFailures(t *testing.T) {
	ctx := context.Background()
	groupID := int64(51100)
	const key = "openai:session_failing_sticky"
	a := &Account{ID: 51101, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 5, Priority: 0, GroupIDs: []int64{groupID}}
	b := &Account{ID: 51102, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 5, Priority: 5, GroupIDs: []int64{groupID}}
	svc, _ := stickyDeadScenarioService(t, groupID, []*Account{a, b}, []*Account{a, b}, 51101, key)

	hitsOnDeadAccount := 0
	var timeline []string
	for request := 1; request <= 12; request++ {
		selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_failing_sticky", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		require.NotNil(t, selection)
		first := selection.Account.ID
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		if first == 51101 {
			// 先撞死号：失败，记入健康度，handler 排除它后换号重试
			hitsOnDeadAccount++
			svc.openaiAccountStats.report(51101, false, nil)
			second, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_failing_sticky", "gpt-5.1", map[int64]struct{}{51101: {}}, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, second, "排除死号后必须还能选出别的账号，否则用户无法重试")
			require.Equal(t, int64(51102), second.Account.ID)
			if second.ReleaseFunc != nil {
				second.ReleaseFunc()
			}
			timeline = append(timeline, fmt.Sprintf("req%d: 先撞死号 -> 换号成功", request))
			continue
		}
		timeline = append(timeline, fmt.Sprintf("req%d: 直接走健康账号", request))
	}
	t.Log("\n" + joinLines(timeline))
	require.LessOrEqual(t, hitsOnDeadAccount, 4, "上游一直失败的账号最多容忍 4 次失败就必须被逃逸，不能无限粘")
	require.Greater(t, hitsOnDeadAccount, 0)
}

// 所有候选都被排除（比如唯一的健康号也失败）时，不能 panic，必须给出可识别的错误，
// 并且之后的请求在号恢复后能正常选出，而不是被旧绑定卡住。
func TestOpenAISticky_AfterAllCandidatesFailedNextRequestRecovers(t *testing.T) {
	ctx := context.Background()
	groupID := int64(51200)
	const key = "openai:session_all_fail"
	a := &Account{ID: 51201, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 5, Priority: 0, GroupIDs: []int64{groupID}}
	svc, cache := stickyDeadScenarioService(t, groupID, []*Account{a}, []*Account{a}, 51201, key)

	// 本次请求里唯一的账号失败并被排除
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_all_fail", "gpt-5.1", map[int64]struct{}{51201: {}}, OpenAIUpstreamTransportAny, false)
	if selection != nil && selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	require.Error(t, err, "没有可选账号时应返回错误让上层处理")
	// 失败不应破坏绑定：下一次请求（不再排除）仍能选到恢复后的账号
	next, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_all_fail", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, int64(51201), next.Account.ID)
	if next.ReleaseFunc != nil {
		next.ReleaseFunc()
	}
	_ = cache
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

// 续链（previous_response_id）绑在死号上：不能继续把请求送给死号，绑定也要清掉。
// 注意：续链状态活在原账号上，换到别的账号上游会报 previous_response_not_found，
// 能不能继续由 forward 层的恢复逻辑决定（见 openai_gateway_forward.go 的 recoverPrevResponseNotFound）。
func TestOpenAISticky_PreviousResponseChainOnDeadAccountIsReleased(t *testing.T) {
	future := time.Now().Add(30 * time.Minute)
	cases := map[string]func(a *Account){
		"status_error":       func(a *Account) { a.Status = StatusError },
		"status_disabled":    func(a *Account) { a.Status = StatusDisabled },
		"schedulable_off":    func(a *Account) { a.Schedulable = false },
		"rate_limited":       func(a *Account) { a.RateLimitResetAt = &future },
		"overloaded":         func(a *Account) { a.OverloadUntil = &future },
		"temp_unschedulable": func(a *Account) { a.TempUnschedulableUntil = &future },
	}
	for name, kill := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			groupID := int64(51300)
			account := Account{
				ID: 51301, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
				GroupIDs: []int64{groupID},
				Extra:    map[string]any{"openai_apikey_responses_websockets_v2_enabled": true},
			}
			cache := &stubGatewayCache{}
			store := NewOpenAIWSStateStore(cache)
			svc := &OpenAIGatewayService{
				accountRepo:        stubOpenAIAccountRepo{accounts: []Account{account}},
				cache:              cache,
				cfg:                newOpenAIWSV2TestConfig(),
				concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
				openaiWSStateStore: store,
			}
			require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_chain_dead", account.ID, time.Hour))

			// 健康时：续链命中绑定账号
			alive, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_chain_dead", "gpt-5.1", nil, false)
			require.NoError(t, err)
			require.NotNil(t, alive)
			require.Equal(t, account.ID, alive.Account.ID)
			if alive.ReleaseFunc != nil {
				alive.ReleaseFunc()
			}

			// 账号死了：不再命中，绑定被清理
			dead := account
			kill(&dead)
			svc.accountRepo = stubOpenAIAccountRepo{accounts: []Account{dead}}
			selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_chain_dead", "gpt-5.1", nil, false)
			require.NoError(t, err)
			require.Nil(t, selection, "死号不能继续接续链请求")
			boundTo, getErr := store.GetResponseAccount(ctx, groupID, "resp_chain_dead")
			require.NoError(t, getErr)
			require.Zero(t, boundTo, "续链绑定应已清除，否则每次重试都先查到死号")
		})
	}
}
