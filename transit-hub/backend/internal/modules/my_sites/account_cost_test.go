package my_sites

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"transithub/backend/internal/modules/upstream"
)

// fakeStateRepo 只需实现 StateRepository 的两个方法。
type fakeStateRepo struct {
	state *State
}

type fakeCostConnectionRepo struct {
	connections []RealConnection
}

func (f *fakeCostConnectionRepo) SaveRealConnection(context.Context, RealConnection) error {
	return nil
}
func (f *fakeCostConnectionRepo) ListRealConnections(context.Context, string, string) ([]RealConnection, error) {
	return f.connections, nil
}
func (f *fakeCostConnectionRepo) GetRealConnection(context.Context, string, string, string) (*RealConnection, error) {
	return nil, nil
}
func (f *fakeCostConnectionRepo) DeleteRealConnection(context.Context, string, string, string) error {
	return nil
}

func (f *fakeStateRepo) Get(_ context.Context, _ string, _ string) (*State, error) {
	return f.state, nil
}

func (f *fakeStateRepo) Save(_ context.Context, _ State) error { return nil }

// costTestServer 模拟本方 Sub2API：/admin/groups 返回自有分组，
// /admin/dashboard/groups 按 account_id + group_id 返回账号成本。
// costs 的 key 是 "accountID|groupID"。
func costTestServer(t *testing.T, costs map[string]float64, calls *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/groups":
			writeCostJSON(w, map[string]any{"data": []map[string]any{
				{"id": "7", "name": "vip"},
				{"id": "9", "name": "basic"},
			}})
		case "/api/v1/admin/dashboard/groups":
			key := r.URL.Query().Get("account_id") + "|" + r.URL.Query().Get("group_id")
			if calls != nil {
				*calls = append(*calls, key)
			}
			cost, ok := costs[key]
			if !ok {
				// 没配的组合返回缺 account_cost 的记录，模拟"成本口径不可用"
				writeCostJSON(w, map[string]any{"data": []map[string]any{
					{"group_name": "vip", "actual_cost": 99.0},
				}})
				return
			}
			writeCostJSON(w, map[string]any{"data": []map[string]any{
				{"group_name": "vip", "actual_cost": 99.0, "account_cost": cost},
			}})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
}

// reasonCounts 汇总「算不出成本」的原因，便于断言缺口被如实报了出来。
func reasonCounts(result AccountCostResult) map[UnresolvedReason]int {
	counts := make(map[UnresolvedReason]int)
	for _, item := range result.Unresolved {
		counts[item.Reason]++
	}
	return counts
}

func writeCostJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func costTestService(t *testing.T, server *httptest.Server, mappings []GroupMapping) *Service {
	t.Helper()
	repo := &fakeStateRepo{state: &State{
		Session: upstream.Session{
			Platform:    upstream.PlatformSub2API,
			BaseURL:     server.URL,
			AccessToken: "token",
			AdminAPIKey: "admin-key",
		},
		Mappings: mappings,
	}}
	service := NewService(repo, upstream.NewPlatformService(upstream.NewHTTPClient(server.Client())), nil)
	service.connRepository = &fakeCostConnectionRepo{}
	return service
}

func costTestServiceWithConnections(t *testing.T, server *httptest.Server, mappings []GroupMapping, connections []RealConnection) *Service {
	service := costTestService(t, server, mappings)
	service.connRepository = &fakeCostConnectionRepo{connections: connections}
	return service
}

// 正常绑定：金额原样进 CostCNY。
// sub2api 返回的账号成本本身就是人民币口径，这里绝不能再乘任何系数——
// 乘一次汇率会把整套账放大 7 倍，那是生产上出过的事故。
func TestTargetAccountCostRangeUsesAccountCostVerbatim(t *testing.T) {
	server := costTestServer(t, map[string]float64{"119|7": 12.34}, nil)
	defer server.Close()

	service := costTestService(t, server, []GroupMapping{{
		OwnGroup: "vip",
		UpstreamTargets: []UpstreamGroupRef{
			{SiteID: "site-a", GroupName: "上游A组", Sub2APIAccountID: strPtr("119")},
		},
	}})

	result := service.TargetAccountCostRange(context.Background(), "u1", "a1", "2026-08-14", "2026-08-20")
	if len(result.Costs) != 1 {
		t.Fatalf("期望 1 条成本，实际 %d 条：%+v", len(result.Costs), result.Costs)
	}
	if result.Costs[0].CostCNY != 12.34 {
		t.Errorf("CostCNY = %v，期望 12.34（原样，不做任何换算）", result.Costs[0].CostCNY)
	}
	if result.Costs[0].SiteID != "site-a" || result.Costs[0].GroupName != "上游A组" {
		t.Errorf("归属信息不对：%+v", result.Costs[0])
	}
	if len(result.Unresolved) != 0 {
		t.Errorf("全部归集成功时不该有缺口，实际 %+v", result.Unresolved)
	}
}

// 没绑定账号的上游目标不该产生成本，也不该发出请求——
// 拿不到真实成本时宁可缺席，也不能显示假数字。
func TestTargetAccountCostRangeSkipsUnboundTarget(t *testing.T) {
	calls := make([]string, 0)
	server := costTestServer(t, map[string]float64{"119|7": 12.34}, &calls)
	defer server.Close()

	service := costTestService(t, server, []GroupMapping{{
		OwnGroup: "vip",
		UpstreamTargets: []UpstreamGroupRef{
			{SiteID: "site-a", GroupName: "上游A组"},                               // 未绑定
			{SiteID: "site-b", GroupName: "上游B组", Sub2APIAccountID: strPtr("")}, // 空串同样视为未绑定
		},
	}})

	result := service.TargetAccountCostRange(context.Background(), "u1", "a1", "2026-08-14", "2026-08-20")
	if len(result.Costs) != 0 {
		t.Fatalf("未绑定账号不应产生成本，实际 %+v", result.Costs)
	}
	if len(calls) != 0 {
		t.Errorf("未绑定时不应发出成本查询，实际发出 %v", calls)
	}
	// 两个目标都要作为缺口报出来，不能从统计里悄悄消失。
	if got := reasonCounts(result)[ReasonUnbound]; got != 2 {
		t.Errorf("未绑定缺口 = %d，期望 2：%+v", got, result.Unresolved)
	}
}

func TestTargetAccountCostRangeFallsBackToActiveRealConnection(t *testing.T) {
	server := costTestServer(t, map[string]float64{"119|7": 12.34}, nil)
	defer server.Close()
	service := costTestServiceWithConnections(t, server, []GroupMapping{{OwnGroup: "vip", UpstreamTargets: []UpstreamGroupRef{{SiteID: "site-a", GroupName: "上游A组"}}}}, []RealConnection{{UpstreamSiteID: "site-a", UpstreamGroupName: "上游A组", AdminAccountID: "119", Status: ConnectionStatusActive}})
	result := service.TargetAccountCostRange(context.Background(), "u1", "a1", "2026-08-14", "2026-08-20")
	if len(result.Costs) != 1 || result.Costs[0].CostCNY != 12.34 || len(result.Unresolved) != 0 {
		t.Fatalf("active real connection fallback failed: %+v", result)
	}
}

func TestTargetAccountCostRangeExplicitBindingWinsOverRealConnection(t *testing.T) {
	server := costTestServer(t, map[string]float64{"119|7": 12.34, "145|7": 5.66}, nil)
	defer server.Close()
	service := costTestServiceWithConnections(t, server, []GroupMapping{{OwnGroup: "vip", UpstreamTargets: []UpstreamGroupRef{{SiteID: "site-a", GroupName: "上游A组", Sub2APIAccountID: strPtr("119")}}}}, []RealConnection{{UpstreamSiteID: "site-a", UpstreamGroupName: "上游A组", AdminAccountID: "145", Status: ConnectionStatusActive}})
	result := service.TargetAccountCostRange(context.Background(), "u1", "a1", "2026-08-14", "2026-08-20")
	if len(result.Costs) != 1 || result.Costs[0].CostCNY != 12.34 {
		t.Fatalf("explicit binding did not win: %+v", result)
	}
}

func TestTargetAccountCostRangeFallbackStillDetectsAmbiguousAccount(t *testing.T) {
	server := costTestServer(t, map[string]float64{"119|7": 12.34}, nil)
	defer server.Close()
	service := costTestServiceWithConnections(t, server, []GroupMapping{{OwnGroup: "vip", UpstreamTargets: []UpstreamGroupRef{{SiteID: "site-a", GroupName: "上游A组"}, {SiteID: "site-b", GroupName: "上游B组"}}}}, []RealConnection{{UpstreamSiteID: "site-a", UpstreamGroupName: "上游A组", AdminAccountID: "119", Status: ConnectionStatusActive}, {UpstreamSiteID: "site-b", UpstreamGroupName: "上游B组", AdminAccountID: "119", Status: ConnectionStatusActive}})
	result := service.TargetAccountCostRange(context.Background(), "u1", "a1", "2026-08-14", "2026-08-20")
	if got := reasonCounts(result)[ReasonAmbiguous]; got != 2 {
		t.Fatalf("fallback ambiguity = %d, want 2: %+v", got, result.Unresolved)
	}
}

// 同一个「账号 + 自有分组」被两个不同上游目标引用时归属不明，
// 两条都必须跳过，否则同一笔成本会被算到错误的站点头上。
func TestTargetAccountCostRangeSkipsAmbiguousBinding(t *testing.T) {
	server := costTestServer(t, map[string]float64{"119|7": 12.34}, nil)
	defer server.Close()

	service := costTestService(t, server, []GroupMapping{{
		OwnGroup: "vip",
		UpstreamTargets: []UpstreamGroupRef{
			{SiteID: "site-a", GroupName: "上游A组", Sub2APIAccountID: strPtr("119")},
			{SiteID: "site-b", GroupName: "上游B组", Sub2APIAccountID: strPtr("119")},
		},
	}})

	result := service.TargetAccountCostRange(context.Background(), "u1", "a1", "2026-08-14", "2026-08-20")
	if len(result.Costs) != 0 {
		t.Fatalf("绑定归属冲突时不应产生成本，实际 %+v", result.Costs)
	}
	// 冲突涉及的两个目标都要报，只报一个会让人以为另一个正常。
	if got := reasonCounts(result)[ReasonAmbiguous]; got != 2 {
		t.Errorf("冲突缺口 = %d，期望 2：%+v", got, result.Unresolved)
	}
}

// 接口没返回 account_cost 时要整条跳过，而不是记成 0。
// 记 0 会让简报显示「这个分组没花钱」，比缺席更容易误导。
func TestTargetAccountCostRangeSkipsMissingAccountCost(t *testing.T) {
	server := costTestServer(t, map[string]float64{}, nil) // 任何组合都缺 account_cost
	defer server.Close()

	service := costTestService(t, server, []GroupMapping{{
		OwnGroup: "vip",
		UpstreamTargets: []UpstreamGroupRef{
			{SiteID: "site-a", GroupName: "上游A组", Sub2APIAccountID: strPtr("119")},
		},
	}})

	result := service.TargetAccountCostRange(context.Background(), "u1", "a1", "2026-08-14", "2026-08-20")
	if len(result.Costs) != 0 {
		t.Fatalf("缺少 account_cost 时不应产生成本，实际 %+v", result.Costs)
	}
	if got := reasonCounts(result)[ReasonQueryFailed]; got != 1 {
		t.Errorf("查询失败缺口 = %d，期望 1：%+v", got, result.Unresolved)
	}
}

// 自有分组在 admin 分组列表里找不到（已被删除或改名）时跳过，
// 否则会拿着空 group_id 去查，得到整个账号的成本而非该分组的。
func TestTargetAccountCostRangeSkipsUnknownOwnGroup(t *testing.T) {
	calls := make([]string, 0)
	server := costTestServer(t, map[string]float64{"119|7": 12.34}, &calls)
	defer server.Close()

	service := costTestService(t, server, []GroupMapping{{
		OwnGroup: "已经不存在的分组",
		UpstreamTargets: []UpstreamGroupRef{
			{SiteID: "site-a", GroupName: "上游A组", Sub2APIAccountID: strPtr("119")},
		},
	}})

	result := service.TargetAccountCostRange(context.Background(), "u1", "a1", "2026-08-14", "2026-08-20")
	if len(result.Costs) != 0 {
		t.Fatalf("自有分组不存在时不应产生成本，实际 %+v", result.Costs)
	}
	if len(calls) != 0 {
		t.Errorf("分组不存在时不应发出成本查询，实际发出 %v", calls)
	}
	if got := reasonCounts(result)[ReasonGroupMissing]; got != 1 {
		t.Errorf("分组缺失缺口 = %d，期望 1：%+v", got, result.Unresolved)
	}
}

// 成本口径只对本方 Sub2API 成立，其他平台一律不出数。
func TestTargetAccountCostRangeRejectsNonSub2API(t *testing.T) {
	server := costTestServer(t, map[string]float64{"119|7": 12.34}, nil)
	defer server.Close()

	repo := &fakeStateRepo{state: &State{
		Session: upstream.Session{
			Platform: upstream.PlatformNewAPI,
			BaseURL:  server.URL,
			Cookie:   "session=x",
			UserID:   "1",
		},
		Mappings: []GroupMapping{{
			OwnGroup: "vip",
			UpstreamTargets: []UpstreamGroupRef{
				{SiteID: "site-a", GroupName: "上游A组", Sub2APIAccountID: strPtr("119")},
			},
		}},
	}}
	service := NewService(repo, upstream.NewPlatformService(upstream.NewHTTPClient(server.Client())), nil)

	if result := service.TargetAccountCostRange(context.Background(), "u1", "a1", "2026-08-14", "2026-08-20"); len(result.Costs) != 0 || len(result.Unresolved) != 0 {
		t.Fatalf("非 sub2api 平台不应出任何结果，实际 %+v", result)
	}
}

// 多个自有分组各自绑定不同账号时，各查各的，互不串味。
func TestTargetAccountCostRangeHandlesMultipleBindings(t *testing.T) {
	server := costTestServer(t, map[string]float64{"119|7": 12.34, "145|9": 5.66}, nil)
	defer server.Close()

	service := costTestService(t, server, []GroupMapping{
		{
			OwnGroup: "vip",
			UpstreamTargets: []UpstreamGroupRef{
				{SiteID: "site-a", GroupName: "上游A组", Sub2APIAccountID: strPtr("119")},
			},
		},
		{
			OwnGroup: "basic",
			UpstreamTargets: []UpstreamGroupRef{
				{SiteID: "site-b", GroupName: "上游B组", Sub2APIAccountID: strPtr("145")},
			},
		},
	})

	result := service.TargetAccountCostRange(context.Background(), "u1", "a1", "2026-08-14", "2026-08-20")
	if len(result.Costs) != 2 {
		t.Fatalf("期望 2 条成本，实际 %d 条：%+v", len(result.Costs), result.Costs)
	}
	bySite := map[string]float64{}
	for _, cost := range result.Costs {
		bySite[cost.SiteID] = cost.CostCNY
	}
	if bySite["site-a"] != 12.34 || bySite["site-b"] != 5.66 {
		t.Errorf("成本归属错乱：%+v", bySite)
	}
}
