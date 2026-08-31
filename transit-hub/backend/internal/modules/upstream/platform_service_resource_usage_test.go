package upstream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchSub2APIResourceUsageReadsAdminOverview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/ops/dashboard/overview" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "admin-key" {
			t.Fatal("missing admin api key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"system_metrics":{"cpu_usage_percent":86.5,"memory_usage_percent":"91.25"}}}`)
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	usage, err := service.FetchSub2APIResourceUsage(Session{
		Platform:    PlatformSub2API,
		BaseURL:     server.URL,
		AdminAPIKey: "admin-key",
	})
	if err != nil {
		t.Fatalf("FetchSub2APIResourceUsage returned error: %v", err)
	}
	if usage.CPUUsagePercent == nil || *usage.CPUUsagePercent != 86.5 {
		t.Fatalf("unexpected CPU metric: %#v", usage.CPUUsagePercent)
	}
	if usage.MemoryUsagePercent == nil || *usage.MemoryUsagePercent != 91.25 {
		t.Fatalf("unexpected memory metric: %#v", usage.MemoryUsagePercent)
	}
}
