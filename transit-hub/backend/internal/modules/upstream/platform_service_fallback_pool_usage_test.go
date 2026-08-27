package upstream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchSub2APIFallbackPoolUsageEventsFiltersAndParsesUsage(t *testing.T) {
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/usage" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "admin-key" {
			t.Fatalf("missing admin api key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"data": {
				"items": [
					{
						"request_id": "req-fallback",
						"account_id": 88,
						"account": {"name": "acct-a"},
						"model": "gpt-5",
						"created_at": %q,
						"actual_cost": 0.123456,
						"fallback_pool_used": true,
						"fallback_source_group_id": 10,
						"fallback_source_group_name": "primary",
						"fallback_target_group_id": 20,
						"fallback_target_group_name": "pool"
					},
					{
						"request_id": "req-normal",
						"created_at": %q,
						"fallback_pool_used": false
					},
					{
						"request_id": "req-old",
						"created_at": %q,
						"fallback_pool_used": true,
						"fallback_source_group_id": 11,
						"fallback_target_group_id": 21
					}
				],
				"total": 3
			}
		}`, now.Add(-30*time.Minute).Format(time.RFC3339), now.Add(-20*time.Minute).Format(time.RFC3339), now.Add(-4*time.Hour).Format(time.RFC3339))
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	events, err := service.FetchSub2APIFallbackPoolUsageEvents(Session{
		Platform:    PlatformSub2API,
		BaseURL:     server.URL,
		AdminAPIKey: "admin-key",
	}, now.Add(-3*time.Hour), now)

	if err != nil {
		t.Fatalf("FetchSub2APIFallbackPoolUsageEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one fallback usage event, got %d: %+v", len(events), events)
	}
	event := events[0]
	if event.RequestID != "req-fallback" || event.AccountID != "88" || event.AccountName != "acct-a" || event.Model != "gpt-5" {
		t.Fatalf("unexpected event identity: %+v", event)
	}
	if event.SourceGroupID != "10" || event.SourceGroupName != "primary" || event.TargetGroupID != "20" || event.TargetGroupName != "pool" {
		t.Fatalf("unexpected fallback trace: %+v", event)
	}
	if event.ActualCost != 0.123456 {
		t.Fatalf("unexpected cost: %v", event.ActualCost)
	}
}
