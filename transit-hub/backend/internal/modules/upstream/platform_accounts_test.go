package upstream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestListAdminGroupAccounts_Sub2APIGroupQueryPagingAndFields 验证 sub2api 分组账号读取：
//   - query 参数是 group=<分组ID>（不是 group_id）。
//   - 分页拉取直到达到 total（覆盖两页）。
//   - 基础字段与探活策略相关字段正确解析。
//   - credentials 等敏感字段不会出现在返回结构里。
func TestListAdminGroupAccounts_Sub2APIGroupQueryPagingAndFields(t *testing.T) {
	var seenGroupQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/admin/usage" {
			writeJSON(w, map[string]any{"data": map[string]any{"items": []any{}}})
			return
		}
		if r.URL.Path != "/api/v1/admin/accounts" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		seenGroupQueries = append(seenGroupQueries, r.URL.Query().Get("group"))
		if gid := r.URL.Query().Get("group_id"); gid != "" {
			t.Fatalf("must query by group=, not group_id=; got group_id=%s", gid)
		}
		if score := r.URL.Query().Get("include_scheduler_score"); score != "1" {
			t.Fatalf("group inventory must request runtime scheduler scores; got include_scheduler_score=%q", score)
		}
		page := r.URL.Query().Get("page")
		switch page {
		case "1":
			// 第 1 页返回满页（100 条），迫使读取继续翻第 2 页；第一条携带完整字段与敏感字段。
			items := make([]map[string]any, 0, 100)
			items = append(items, map[string]any{
				"id": 101, "name": "acc-a", "platform": "openai", "type": "oauth", "status": "active",
				"priority": 5, "concurrency": 3, "current_concurrency": 2, "rate_multiplier": 1.5, "load_factor": 2,
				"scheduler_scores": []any{map[string]any{"group_id": 42, "group_priority": 8, "base_score": 0.73}},
				"group_ids":        []any{7.0, 8.0}, "schedulable": true,
				"rate_limit_reset_at":       "2099-01-01T00:00:00Z",
				"overload_until":            "2099-01-02T00:00:00Z",
				"temp_unschedulable_until":  "2099-01-03T00:00:00Z",
				"temp_unschedulable_reason": "upstream_transport",
				"expires_at":                float64(4070908800), "auto_pause_on_expired": true,
				"credentials": map[string]any{"access_token": "SECRET-TOKEN-XYZ"},
				"api_key":     "sk-super-secret",
			})
			for i := 0; i < 99; i++ {
				items = append(items, map[string]any{"id": 1000 + i, "name": "filler", "status": "active"})
			}
			writeJSON(w, map[string]any{"data": items, "total": 101})
		case "2":
			writeJSON(w, map[string]any{
				"data": []map[string]any{
					{"id": 102, "name": "acc-b", "platform": "anthropic", "status": "disabled", "schedulable": false},
				},
				"total": 101,
			})
		default:
			t.Fatalf("unexpected page: %s", page)
		}
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	session := Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"}

	accounts, err := service.ListAdminGroupAccounts(session, AdminGroupInfo{ID: "42", Name: "vip"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 101 {
		t.Fatalf("expected 101 accounts across 2 pages, got %d", len(accounts))
	}
	if len(seenGroupQueries) != 2 {
		t.Fatalf("expected 2 paged requests, got %d", len(seenGroupQueries))
	}
	for _, q := range seenGroupQueries {
		if q != "42" {
			t.Fatalf("expected group query = 42 (group ID), got %q", q)
		}
	}

	a := accounts[0]
	if a.ID != "101" || a.Name != "acc-a" || a.Platform != "openai" || a.Type != "oauth" || a.Status != "active" {
		t.Fatalf("unexpected base fields: %+v", a)
	}
	if a.Priority == nil || *a.Priority != 5 {
		t.Fatalf("priority = %v, want 5", a.Priority)
	}
	if a.GroupPriority == nil || *a.GroupPriority != 8 {
		t.Fatalf("groupPriority = %v, want 8", a.GroupPriority)
	}
	if a.Concurrency == nil || *a.Concurrency != 3 {
		t.Fatalf("concurrency = %v, want 3", a.Concurrency)
	}
	if a.CurrentConcurrency == nil || *a.CurrentConcurrency != 2 {
		t.Fatalf("currentConcurrency = %v, want 2", a.CurrentConcurrency)
	}
	if a.SchedulerScore == nil || *a.SchedulerScore != 0.73 {
		t.Fatalf("schedulerScore = %v, want 0.73", a.SchedulerScore)
	}
	if a.RateMultiplier == nil || *a.RateMultiplier != 1.5 {
		t.Fatalf("rateMultiplier = %v, want 1.5", a.RateMultiplier)
	}
	if a.LoadFactor == nil || *a.LoadFactor != 2 {
		t.Fatalf("loadFactor = %v, want 2", a.LoadFactor)
	}
	if a.Schedulable == nil || *a.Schedulable != true {
		t.Fatalf("schedulable = %v, want true", a.Schedulable)
	}
	if a.RateLimitResetAt == nil || a.OverloadUntil == nil || a.TempUnschedulableUntil == nil {
		t.Fatalf("runtime block timestamps were not parsed: %+v", a)
	}
	if a.TempUnschedulableReason != "upstream_transport" {
		t.Fatalf("temp reason = %q", a.TempUnschedulableReason)
	}
	if a.ExpiresAt == nil || a.ExpiresAt.Unix() != 4070908800 || a.AutoPauseOnExpired == nil || !*a.AutoPauseOnExpired {
		t.Fatalf("expiration state was not parsed: expires=%v auto=%v", a.ExpiresAt, a.AutoPauseOnExpired)
	}
	if a.Weight != nil {
		t.Fatalf("sub2api account must not have weight, got %v", a.Weight)
	}
	if len(a.GroupIDs) != 2 || a.GroupIDs[0] != "7" || a.GroupIDs[1] != "8" {
		t.Fatalf("groupIds = %v, want [7 8]", a.GroupIDs)
	}

	// 敏感字段绝不出现在序列化结果里。
	encoded, _ := json.Marshal(accounts)
	for _, secret := range []string{"SECRET-TOKEN-XYZ", "sk-super-secret", "credentials", "access_token", "api_key"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("sensitive field %q leaked into accounts response: %s", secret, encoded)
		}
	}
}

func TestListAdminGroupAccounts_Sub2APIEnrichesRecentUsageP95(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	usageItems := make([]map[string]any, 0, 28)
	for index := 1; index <= 21; index++ {
		usageItems = append(usageItems, map[string]any{
			"account_id":     101,
			"group_id":       42,
			"first_token_ms": index * 100,
			"created_at":     now.Add(-time.Duration(index) * time.Minute).Format(time.RFC3339),
		})
	}
	usageItems = append(usageItems,
		map[string]any{"account_id": 102, "group_id": 42, "first_token_ms": 300, "created_at": now.Add(-time.Minute).Format(time.RFC3339)},
		map[string]any{"account_id": 102, "group_id": 42, "first_token_ms": 100, "created_at": now.Add(-2 * time.Minute).Format(time.RFC3339)},
		map[string]any{"account_id": 102, "group_id": 42, "first_token_ms": 200, "created_at": now.Add(-3 * time.Minute).Format(time.RFC3339)},
		map[string]any{"account_id": 102, "group_id": 42, "first_token_ms": 9999, "created_at": now.Add(-2 * time.Hour).Format(time.RFC3339)},
		map[string]any{"account_id": 102, "group_id": 99, "first_token_ms": 9999, "created_at": now.Add(-4 * time.Minute).Format(time.RFC3339)},
		map[string]any{"account_id": 102, "group_id": 42, "first_token_ms": 0, "created_at": now.Add(-5 * time.Minute).Format(time.RFC3339)},
	)

	usageCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/accounts":
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": 101}, {"id": 102}}, "total": 2})
		case "/api/v1/admin/usage":
			usageCalls++
			if r.URL.Query().Get("group_id") != "42" || r.URL.Query().Get("page_size") != "100" ||
				r.URL.Query().Get("sort_by") != "created_at" || r.URL.Query().Get("sort_order") != "desc" {
				t.Fatalf("unexpected usage query: %s", r.URL.RawQuery)
			}
			if r.Header.Get("x-api-key") != "admin-key" || r.Header.Get("Authorization") != "" {
				t.Fatalf("usage request must use only the admin key: %+v", r.Header)
			}
			writeJSON(w, map[string]any{"data": map[string]any{"items": usageItems, "total": len(usageItems)}})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	accounts, err := service.ListAdminGroupAccounts(Session{
		Platform: PlatformSub2API, BaseURL: server.URL, AdminAPIKey: "admin-key",
	}, AdminGroupInfo{ID: "42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usageCalls != 1 {
		t.Fatalf("expected exactly one usage read and no active probes, got %d", usageCalls)
	}
	if accounts[0].UsageSampleCount != 20 || accounts[0].UsageP95FirstTokenMs == nil || *accounts[0].UsageP95FirstTokenMs != 1900 {
		t.Fatalf("account 101 must use the latest 20 samples and P95=1900: %+v", accounts[0])
	}
	if accounts[1].UsageSampleCount != 3 || accounts[1].UsageP95FirstTokenMs == nil || *accounts[1].UsageP95FirstTokenMs != 300 {
		t.Fatalf("account 102 must ignore old, wrong-group, and invalid samples: %+v", accounts[1])
	}
}

func TestListAdminGroupAccounts_Sub2APIUsagePaginatesForQuieterAccounts(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	firstPage := make([]map[string]any, 0, sub2APIUsagePageSize)
	for index := 1; index <= sub2APIUsagePageSize; index++ {
		firstPage = append(firstPage, map[string]any{
			"account_id":     101,
			"group_id":       42,
			"first_token_ms": index * 100,
			"created_at":     now.Add(-time.Duration(index) * time.Second).Format(time.RFC3339),
		})
	}
	secondPage := []map[string]any{
		{"account_id": 102, "group_id": 42, "first_token_ms": 100, "created_at": now.Add(-2 * time.Minute).Format(time.RFC3339)},
		{"account_id": 102, "group_id": 42, "first_token_ms": 200, "created_at": now.Add(-3 * time.Minute).Format(time.RFC3339)},
		{"account_id": 102, "group_id": 42, "first_token_ms": 300, "created_at": now.Add(-4 * time.Minute).Format(time.RFC3339)},
	}
	usagePages := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/accounts":
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": 101}, {"id": 102}}, "total": 2})
		case "/api/v1/admin/usage":
			page := r.URL.Query().Get("page")
			usagePages = append(usagePages, page)
			switch page {
			case "1":
				writeJSON(w, map[string]any{"data": map[string]any{"items": firstPage, "total": 103}})
			case "2":
				writeJSON(w, map[string]any{"data": map[string]any{"items": secondPage, "total": 103}})
			default:
				t.Fatalf("unexpected usage page: %s", page)
			}
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	accounts, err := service.ListAdminGroupAccounts(
		Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"},
		AdminGroupInfo{ID: "42"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(usagePages, ",") != "1,2" {
		t.Fatalf("usage reader must continue past a busy account to reach quieter accounts: pages=%v", usagePages)
	}
	if accounts[1].UsageSampleCount != 3 || accounts[1].UsageP95FirstTokenMs == nil || *accounts[1].UsageP95FirstTokenMs != 300 {
		t.Fatalf("account 102 must use samples found on the next page: %+v", accounts[1])
	}
}

func TestListAdminGroupAccounts_Sub2APIUsageFallbackIsNonBlocking(t *testing.T) {
	t.Run("insufficient samples", func(t *testing.T) {
		now := time.Now().UTC()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/admin/accounts":
				writeJSON(w, map[string]any{"data": []map[string]any{{"id": 101}}, "total": 1})
			case "/api/v1/admin/usage":
				writeJSON(w, map[string]any{"data": map[string]any{"items": []map[string]any{
					{"account_id": 101, "group_id": 42, "first_token_ms": 100, "created_at": now.Add(-time.Minute)},
					{"account_id": 101, "group_id": 42, "first_token_ms": 200, "created_at": now.Add(-2 * time.Minute)},
				}}})
			}
		}))
		defer server.Close()
		service := NewPlatformService(NewHTTPClient(server.Client()))
		accounts, err := service.ListAdminGroupAccounts(Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"}, AdminGroupInfo{ID: "42"})
		if err != nil || len(accounts) != 1 {
			t.Fatalf("account inventory must survive insufficient usage samples: accounts=%+v err=%v", accounts, err)
		}
		if accounts[0].UsageSampleCount != 2 || accounts[0].UsageP95FirstTokenMs != nil {
			t.Fatalf("fewer than three samples must remain unreliable for probe fallback: %+v", accounts[0])
		}
	})

	t.Run("usage API failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/admin/accounts":
				writeJSON(w, map[string]any{"data": []map[string]any{{"id": 101}}, "total": 1})
			case "/api/v1/admin/usage":
				w.WriteHeader(http.StatusServiceUnavailable)
				writeJSON(w, map[string]any{"message": "temporarily unavailable"})
			}
		}))
		defer server.Close()
		service := NewPlatformService(NewHTTPClient(server.Client()))
		accounts, err := service.ListAdminGroupAccounts(Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"}, AdminGroupInfo{ID: "42"})
		if err != nil || len(accounts) != 1 || accounts[0].UsageP95FirstTokenMs != nil {
			t.Fatalf("usage failure must preserve account inventory for local probe fallback: accounts=%+v err=%v", accounts, err)
		}
	})
}

// TestListAdminGroupAccounts_NewAPIUsesSearchPath 验证 new-api 优先走 /api/channel/search?group=<分组名>，
// 并正确解析 channel 字段（含 weight），key 不外泄。
func TestListAdminGroupAccounts_NewAPIUsesSearchPath(t *testing.T) {
	searchCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel/search":
			searchCalled = true
			if g := r.URL.Query().Get("group"); g != "vip" {
				t.Fatalf("expected group=vip, got %q", g)
			}
			writeJSON(w, map[string]any{
				"data": []map[string]any{
					{
						"id": 9, "type": 1, "name": "ch-a", "status": 1, "base_url": "https://up.example.com",
						"models": "gpt-4o,gpt-4o-mini", "group": "vip,default", "priority": 10, "weight": 3,
						"key": "sk-channel-secret",
					},
				},
				"total": 1,
			})
		default:
			t.Fatalf("new-api should use /api/channel/search, got path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	session := Session{Platform: PlatformNewAPI, BaseURL: server.URL, Cookie: "session=x", UserID: "1"}

	channels, err := service.ListAdminGroupAccounts(session, AdminGroupInfo{ID: "vip", Name: "vip"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !searchCalled {
		t.Fatalf("expected /api/channel/search to be used")
	}
	if len(channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(channels))
	}
	c := channels[0]
	if c.ID != "9" || c.Name != "ch-a" || c.Type != "1" || c.Status != "1" {
		t.Fatalf("unexpected base fields: %+v", c)
	}
	if c.Weight == nil || *c.Weight != 3 {
		t.Fatalf("weight = %v, want 3", c.Weight)
	}
	if c.Priority == nil || *c.Priority != 10 {
		t.Fatalf("priority = %v, want 10", c.Priority)
	}
	if c.Models != "gpt-4o,gpt-4o-mini" {
		t.Fatalf("models = %q", c.Models)
	}

	encoded, _ := json.Marshal(channels)
	if strings.Contains(string(encoded), "sk-channel-secret") || strings.Contains(string(encoded), `"key"`) {
		t.Fatalf("channel key leaked into response: %s", encoded)
	}
}

// TestListAdminGroupAccounts_NewAPIFallsBackToLocalCommaFilter 验证 search 接口失败时，
// 兜底走 /api/channel/ 分页并按「逗号分组精确匹配」本地过滤：
//   - "vip" 命中 group="vip,default" 与 group="vip"。
//   - "vip" 不能命中 group="vip2"（禁止 substring 匹配）。
func TestListAdminGroupAccounts_NewAPIFallsBackToLocalCommaFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel/search":
			// 模拟旧部署没有 search 接口。
			w.WriteHeader(http.StatusNotFound)
		case "/api/channel/":
			writeJSON(w, map[string]any{
				"data": []map[string]any{
					{"id": 1, "name": "ch-vip-default", "group": "vip,default", "weight": 1},
					{"id": 2, "name": "ch-vip", "group": "vip", "weight": 2},
					{"id": 3, "name": "ch-vip2", "group": "vip2", "weight": 3},
					{"id": 4, "name": "ch-other", "group": "default,pro", "weight": 4},
				},
				"total": 4,
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	session := Session{Platform: PlatformNewAPI, BaseURL: server.URL, Cookie: "session=x", UserID: "1"}

	channels, err := service.ListAdminGroupAccounts(session, AdminGroupInfo{ID: "vip", Name: "vip"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := map[string]bool{}
	for _, c := range channels {
		got[c.Name] = true
	}
	if !got["ch-vip-default"] || !got["ch-vip"] {
		t.Fatalf("expected exact comma-group matches ch-vip-default and ch-vip, got %+v", got)
	}
	if got["ch-vip2"] {
		t.Fatalf("substring match leaked: vip must not match group vip2")
	}
	if got["ch-other"] {
		t.Fatalf("ch-other (group default,pro) must not match vip")
	}
	if len(channels) != 2 {
		t.Fatalf("expected exactly 2 matched channels, got %d", len(channels))
	}
}
