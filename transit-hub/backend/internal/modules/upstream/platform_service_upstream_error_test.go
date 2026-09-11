package upstream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchSub2APIUpstreamErrorEventsFiltersAndParsesRows(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/ops/errors" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "admin-key" {
			t.Fatal("missing admin api key")
		}
		if got := r.URL.Query().Get("status_codes"); got != "502,503" {
			t.Fatalf("unexpected status filter: %q", got)
		}
		if !strings.Contains(r.URL.Query().Get("start_time"), "2026-09-03") {
			t.Fatalf("missing lookback: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":{"items":[
			{"id":9001,"group_id":9,"group_name":"plus-free","status_code":502,"message":"Upstream request failed","requested_model":"gpt-5.6-sol","created_at":%q},
			{"id":9002,"group_id":9,"group_name":"plus-free","status_code":503,"message":"Service temporarily unavailable","model":"gpt-5.5","created_at":%q},
			{"group_id":10,"group_name":"ignored","status_code":500,"created_at":%q},
			{"group_id":11,"group_name":"old","status_code":502,"created_at":%q}
		],"total":4}}`, now.Add(-time.Minute).Format(time.RFC3339), now.Add(-2*time.Minute).Format(time.RFC3339), now.Format(time.RFC3339), now.Add(-4*time.Hour).Format(time.RFC3339))
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	events, err := service.FetchSub2APIUpstreamErrorEvents(Session{Platform: PlatformSub2API, BaseURL: server.URL, AdminAPIKey: "admin-key"}, now.Add(-3*time.Hour), now)
	if err != nil {
		t.Fatalf("FetchSub2APIUpstreamErrorEvents returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected two matching events, got %d: %+v", len(events), events)
	}
	if events[0].GroupID != "9" || events[0].GroupName != "plus-free" || events[0].StatusCode != 502 || events[0].Model != "gpt-5.6-sol" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
}
