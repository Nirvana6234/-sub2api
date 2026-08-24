package upstream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestFetchAdminGroupDailyStats_DispatchesByPlatform 验证平台中性包装方法按
// session.Platform 正确路由到 sub2api / new-api 具体实现，不重复实现底层抓取逻辑。
func TestFetchAdminGroupDailyStats_DispatchesByPlatform(t *testing.T) {
	t.Run("sub2api uses usage-summary endpoint", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/groups/available":
				writeJSON(w, map[string]any{"data": []map[string]any{
					{"id": 1, "name": "default"},
					{"id": 2, "name": "vip"},
				}})
			case "/api/v1/admin/groups/usage-summary":
				writeJSON(w, map[string]any{"data": []map[string]any{
					{"group_id": 1, "today_actual_cost": 12.5},
					{"group_id": 2, "today_cost": 7.25},
				}})
			default:
				t.Fatalf("unexpected sub2api path: %s", r.URL.Path)
			}
		}))
		defer server.Close()

		service := NewPlatformService(NewHTTPClient(server.Client()))
		session := Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"}

		stats, err := service.FetchAdminGroupDailyStats(session, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stats) != 2 {
			t.Fatalf("expected 2 stats, got %d", len(stats))
		}
		byName := map[string]float64{}
		for _, s := range stats {
			byName[s.GroupName] = s.TodayActualCost
		}
		if byName["default"] != 12.5 {
			t.Errorf("default cost = %.2f, want 12.50", byName["default"])
		}
		if byName["vip"] != 7.25 {
			t.Errorf("vip cost = %.2f, want 7.25", byName["vip"])
		}
	})

	t.Run("new-api uses per-group log stat with quota conversion", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/log/self/stat" {
				t.Fatalf("unexpected new-api path: %s", r.URL.Path)
			}
			group := r.URL.Query().Get("group")
			quota := map[string]float64{"default": 100000, "vip": 250000}[group]
			writeJSON(w, map[string]any{"data": map[string]any{"quota": quota}})
		}))
		defer server.Close()

		service := NewPlatformService(NewHTTPClient(server.Client()))
		session := Session{Platform: PlatformNewAPI, BaseURL: server.URL, Cookie: "session=abc", UserID: "1", QuotaPerUnit: 100000}
		groups := []GroupInfo{{Name: "default"}, {Name: "vip"}}

		stats, err := service.FetchAdminGroupDailyStats(session, groups)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stats) != 2 {
			t.Fatalf("expected 2 stats, got %d", len(stats))
		}
		byName := map[string]float64{}
		for _, s := range stats {
			byName[s.GroupName] = s.TodayActualCost
		}
		// quota / quotaPerUnit: 100000/100000 = 1.0, 250000/100000 = 2.5
		if byName["default"] != 1.0 {
			t.Errorf("default amount = %.4f, want 1.0000 (quota/quotaPerUnit conversion)", byName["default"])
		}
		if byName["vip"] != 2.5 {
			t.Errorf("vip amount = %.4f, want 2.5000 (quota/quotaPerUnit conversion)", byName["vip"])
		}
	})
}

// TestSub2APICostParsers 验证 sub2api 各降级路径的字段解析覆盖文档要求的
// today_actual_cost / total_actual_cost / actual_cost 语义。
func TestSub2APICostParsers(t *testing.T) {
	t.Run("group usage summary prefers today_actual_cost", func(t *testing.T) {
		got := sub2APIUsageSummaryCost(map[string]any{"today_actual_cost": 9.5})
		if got != 9.5 {
			t.Errorf("got %.2f, want 9.50", got)
		}
	})

	t.Run("per-key usage stats prefers total_actual_cost", func(t *testing.T) {
		got := sub2APIUsageStatsCost(map[string]any{"total_actual_cost": 4.2})
		if got != 4.2 {
			t.Errorf("got %.2f, want 4.20", got)
		}
	})

	t.Run("per-key usage stats falls back to actual_cost", func(t *testing.T) {
		got := sub2APIUsageStatsCost(map[string]any{"actual_cost": 3.1})
		if got != 3.1 {
			t.Errorf("got %.2f, want 3.10", got)
		}
	})

	t.Run("admin dashboard groups fallback parses today_actual_cost", func(t *testing.T) {
		got := sub2APIGroupDailyCost(map[string]any{"today_actual_cost": 6.6})
		if got != 6.6 {
			t.Errorf("got %.2f, want 6.60", got)
		}
	})

	t.Run("admin dashboard groups fallback falls back to actual_cost", func(t *testing.T) {
		got := sub2APIGroupDailyCost(map[string]any{"actual_cost": 1.23})
		if got != 1.23 {
			t.Errorf("got %.2f, want 1.23", got)
		}
	})

	t.Run("group account cost is preferred over user charged cost", func(t *testing.T) {
		got, ok := sub2APIGroupAccountCost(map[string]any{
			"actual_cost":  12.5,
			"account_cost": 3.25,
		})
		if !ok || got != 3.25 {
			t.Errorf("account cost = %.2f, available=%t, want 3.25/true", got, ok)
		}
	})

	t.Run("missing account cost is unavailable", func(t *testing.T) {
		got, ok := sub2APIGroupAccountCost(map[string]any{"actual_cost": 12.5})
		if ok || got != 0 {
			t.Errorf("missing account cost = %.2f, available=%t, want 0/false", got, ok)
		}
	})
}

// 采购成本必须按 account_id + group_id 精确过滤，并带上报表时区，
// 否则一个账号被多个分组共用时会重复计数，跨日请求也会落到错误的一天。
func TestFetchSub2APIAccountCostRangeFiltersAndSums(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/dashboard/groups" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotQuery = r.URL.Query()
		writeJSON(w, map[string]any{"data": []map[string]any{
			{"group_name": "vip", "actual_cost": 12.5, "account_cost": 3.25},
			{"group_name": "vip", "actual_cost": 4.0, "account_cost": 1.75},
		}})
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	cost, err := service.FetchSub2APIAccountCostRange(
		Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"},
		"2026-08-14", "2026-08-20", "119", "7",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 3.25 + 1.75，取的是 account_cost；actual_cost 是用户扣费，绝不能混进来。
	if cost != 5.0 {
		t.Fatalf("cost = %v, want 5.0（account_cost 求和，而非 actual_cost）", cost)
	}
	if got := gotQuery.Get("account_id"); got != "119" {
		t.Errorf("account_id = %q, want 119", got)
	}
	if got := gotQuery.Get("group_id"); got != "7" {
		t.Errorf("group_id = %q, want 7", got)
	}
	if got := gotQuery.Get("timezone"); got != "Asia/Shanghai" {
		t.Errorf("timezone = %q, want Asia/Shanghai", got)
	}
	if got := gotQuery.Get("start_date"); got != "2026-08-14" {
		t.Errorf("start_date = %q", got)
	}
}

// account_cost 字段缺失必须报错让调用方跳过，绝不能静默当成 0——
// 那会让简报显示「这个分组没花钱」，比不显示更危险。
func TestFetchSub2APIAccountCostRangeRejectsMissingField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": []map[string]any{
			{"group_name": "vip", "account_cost": 3.25},
			{"group_name": "legacy", "actual_cost": 8.0}, // 没有 account_cost
		}})
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	cost, err := service.FetchSub2APIAccountCostRange(
		Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"},
		"2026-08-14", "2026-08-20", "119", "7",
	)
	if err == nil {
		t.Fatalf("缺少 account_cost 时必须报错，却返回了 %v", cost)
	}
	if cost != 0 {
		t.Errorf("报错时金额应为 0，实际 %v", cost)
	}
}

// 账号或分组没绑定就查询是调用方的 bug，要当场挡住而不是发出一次无过滤的请求。
func TestFetchSub2APIAccountCostRangeRequiresBothFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("缺少过滤条件时不应发出请求，却打到了 %s", r.URL.Path)
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	session := Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"}

	for _, tc := range []struct{ name, accountID, groupID string }{
		{"缺账号", "", "7"},
		{"缺分组", "119", ""},
		{"都缺", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.FetchSub2APIAccountCostRange(session, "2026-08-14", "2026-08-20", tc.accountID, tc.groupID); err == nil {
				t.Error("应当返回错误")
			}
		})
	}
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
