package upstream

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// admin 站点的营收与上游成本必须由同一次调用返回，且带上记账时区。
func TestFetchAdminUsageAccountingReadsBothFigures(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeJSON(w, map[string]any{"data": map[string]any{
			"total_actual_cost":  33.02,
			"total_account_cost": 16.5,
		}})
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	got, err := service.FetchAdminUsageAccounting(
		Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "t"}, "2026-08-09", "2026-08-09")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.RevenueUSD != 33.02 {
		t.Fatalf("revenue = %v, want 33.02", got.RevenueUSD)
	}
	if !got.HasAccountCost || got.AccountCostUSD != 16.5 {
		t.Fatalf("account cost = %+v, want 16.5 present", got)
	}
	// 日界必须显式声明，否则 admin 站点会按它自己的默认时区切分当天。
	if !containsSubstring(gotQuery, "timezone=Asia%2FShanghai") {
		t.Fatalf("expected reporting timezone in query, got %q", gotQuery)
	}
}

// 旧版 sub2api 不返回 total_account_cost：必须报告为"没有该口径"，
// 而不是当成成本 0——后者会让净利润凭空等于营收。
func TestFetchAdminUsageAccountingMarksMissingAccountCost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": map[string]any{"total_actual_cost": 33.02}})
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	got, err := service.FetchAdminUsageAccounting(
		Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "t"}, "2026-08-09", "2026-08-09")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.HasAccountCost {
		t.Fatalf("missing total_account_cost must not be reported as present: %+v", got)
	}
	if got.RevenueUSD != 33.02 {
		t.Fatalf("revenue must still be read, got %v", got.RevenueUSD)
	}
}

func TestFetchAdminUsageAccountingRejectsNegativeAccountCost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": map[string]any{
			"total_actual_cost": 1.0, "total_account_cost": -5.0,
		}})
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	got, err := service.FetchAdminUsageAccounting(
		Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "t"}, "2026-08-09", "2026-08-09")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.HasAccountCost {
		t.Fatalf("negative cost is bad data and must fall back, got %+v", got)
	}
}

func containsSubstring(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOfSubstring(haystack, needle) >= 0)
}

func indexOfSubstring(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
