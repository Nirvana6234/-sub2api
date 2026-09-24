package service

import (
	"context"
	"testing"
)

// 这组用例不带 unit build tag：带 tag 的 service 单测目前整体编译不过
// （多个 AccountRepository stub 缺 UpdateGroupPriorities），放进去等于不跑。
// 所需的 repo stub 在这里就地定义，名字加前缀避免与 unit 用例里的同名 stub 冲突。

type failoverPersistUserRepo struct{ UserRepository }

func (failoverPersistUserRepo) GetByID(_ context.Context, id int64) (*User, error) {
	return &User{ID: id, Status: StatusActive}, nil
}

type failoverPersistGroupRepo struct {
	GroupRepository
	groups []Group
}

func (r failoverPersistGroupRepo) ListActive(context.Context) ([]Group, error) {
	return append([]Group(nil), r.groups...), nil
}

type failoverPersistSubRepo struct{ UserSubscriptionRepository }

func (failoverPersistSubRepo) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	return nil, nil
}

func newFailoverPersistService(groups []Group) *APIKeyService {
	return NewAPIKeyService(nil, failoverPersistUserRepo{}, failoverPersistGroupRepo{groups: groups}, failoverPersistSubRepo{}, nil, nil, nil)
}

// 回归：请求内自动分组故障转移选出的分组，必须延续到下一个请求。
//
// 旧实现把排除后的候选列表写进 key 的副本再去解析，副本的配置指纹与原 key 不同，
// 提交的选择在下一个请求上被判为「配置已变」而丢弃，于是又回到刚失败的最便宜分组。
func TestAutoGroupFailoverSelectionSurvivesNextRequest(t *testing.T) {
	cases := []struct {
		name         string
		autoGroupIDs []int64
	}{
		{name: "explicit candidates", autoGroupIDs: []int64{10, 20}},
		{name: "all available groups", autoGroupIDs: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFailoverPersistService([]Group{
				{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.1, ActiveAccountCount: 1},
				{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.2, ActiveAccountCount: 1},
			})
			apiKey := &APIKey{ID: 91, UserID: 7, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyPrice, AutoGroupIDs: tc.autoGroupIDs}
			ctx := context.Background()
			const model = "gpt-5.5"

			first, err := svc.ResolveAutoGroupForModel(ctx, apiKey, model)
			if err != nil {
				t.Fatalf("initial resolve: %v", err)
			}
			if got := *first.GroupID; got != 10 {
				t.Fatalf("initial group = %d, want cheapest 10", got)
			}

			failedOver, err := svc.ResolveAutoGroupForModelExcluding(ctx, first, model, map[int64]struct{}{10: {}})
			if err != nil {
				t.Fatalf("failover resolve: %v", err)
			}
			if got := *failedOver.GroupID; got != 20 {
				t.Fatalf("failover group = %d, want 20", got)
			}
			// 请求快照上的候选列表必须保持原样：它是配置指纹的一部分，
			// 中间件用这个快照做请求后观察。
			if autoGroupConfigFingerprint(failedOver) != autoGroupConfigFingerprint(apiKey) {
				t.Fatalf("failover snapshot fingerprint = %q, want original %q",
					autoGroupConfigFingerprint(failedOver), autoGroupConfigFingerprint(apiKey))
			}

			stored := svc.autoGroupSelection(autoGroupSelectionKey(apiKey, model))
			if stored.configFingerprint != autoGroupConfigFingerprint(apiKey) {
				t.Fatalf("stored fingerprint = %q, want original %q", stored.configFingerprint, autoGroupConfigFingerprint(apiKey))
			}
			if stored.groupID != 20 || !stored.settled {
				t.Fatalf("stored selection = group %d settled=%v, want group 20 settled", stored.groupID, stored.settled)
			}

			next, err := svc.ResolveAutoGroupForModel(ctx, apiKey, model)
			if err != nil {
				t.Fatalf("next request resolve: %v", err)
			}
			if got := *next.GroupID; got != 20 {
				t.Fatalf("next request group = %d, want 20 (must not bounce back to the failed group 10)", got)
			}
		})
	}
}

// 故障转移是「确认故障」触发的切换，不能占用每小时一次的主动切换额度；
// 否则一次故障转移之后，后续真正的故障转移会被额度挡回失败分组。
func TestAutoGroupFailoverDoesNotConsumeVoluntarySwitchBudget(t *testing.T) {
	svc := newFailoverPersistService([]Group{
		{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.1, ActiveAccountCount: 1},
		{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.2, ActiveAccountCount: 1},
		{ID: 30, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.3, ActiveAccountCount: 1},
	})
	apiKey := &APIKey{ID: 92, UserID: 8, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyPrice, AutoGroupIDs: []int64{10, 20, 30}}
	ctx := context.Background()
	const model = "gpt-5.5"

	first, err := svc.ResolveAutoGroupForModel(ctx, apiKey, model)
	if err != nil {
		t.Fatalf("initial resolve: %v", err)
	}
	second, err := svc.ResolveAutoGroupForModelExcluding(ctx, first, model, map[int64]struct{}{10: {}})
	if err != nil || *second.GroupID != 20 {
		t.Fatalf("first failover = %v, %v; want group 20", second, err)
	}
	// 下一个请求落在 20 上，20 也耗尽，在同一小时内再次故障转移。
	onTwenty, err := svc.ResolveAutoGroupForModel(ctx, apiKey, model)
	if err != nil || *onTwenty.GroupID != 20 {
		t.Fatalf("follow-up request = %v, %v; want group 20", onTwenty, err)
	}
	third, err := svc.ResolveAutoGroupForModelExcluding(ctx, onTwenty, model, map[int64]struct{}{20: {}})
	if err != nil {
		t.Fatalf("second failover: %v", err)
	}
	if *third.GroupID == 20 {
		t.Fatalf("second failover returned the exhausted group 20")
	}

	value, _ := svc.autoGroupSwitchStates.Load(autoGroupSwitchStateKey(apiKey))
	state, _ := value.(autoGroupSwitchState)
	if len(state.switchedAt) != 0 {
		t.Fatalf("failover consumed %d voluntary switch slots, want 0", len(state.switchedAt))
	}
}
