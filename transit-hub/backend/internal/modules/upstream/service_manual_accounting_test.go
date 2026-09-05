package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type manualAccountingTestRepository struct {
	recharges []RechargeEntry
	usages    []struct {
		usageDate  string
		groupName  string
		rawAmount  float64
		multiplier float64
		adjusted   float64
	}
}

func (r *manualAccountingTestRepository) ListSites(context.Context) ([]Site, error) { return nil, nil }
func (r *manualAccountingTestRepository) ListSitesForUser(context.Context, string) ([]Site, error) {
	return nil, nil
}
func (r *manualAccountingTestRepository) SaveSite(context.Context, Site) error { return nil }
func (r *manualAccountingTestRepository) DeleteSite(context.Context, string, string) error {
	return nil
}
func (r *manualAccountingTestRepository) AddRecharge(_ context.Context, _, _, _ string, entry RechargeEntry) error {
	r.recharges = append(r.recharges, entry)
	return nil
}
func (r *manualAccountingTestRepository) ListRecharges(context.Context, string, string, string) ([]RechargeEntry, error) {
	return append([]RechargeEntry(nil), r.recharges...), nil
}
func (r *manualAccountingTestRepository) UpsertDailyUsage(_ context.Context, _, usageDate, groupName string, rawAmount, multiplier, adjustedAmount float64) error {
	usage := struct {
		usageDate  string
		groupName  string
		rawAmount  float64
		multiplier float64
		adjusted   float64
	}{usageDate, groupName, rawAmount, multiplier, adjustedAmount}
	for index, existing := range r.usages {
		if existing.usageDate == usageDate && existing.groupName == groupName {
			r.usages[index] = usage
			return nil
		}
	}
	r.usages = append(r.usages, usage)
	return nil
}
func (r *manualAccountingTestRepository) ManualAccountingSummary(context.Context, string, string, string) (ManualAccountingSummary, error) {
	summary := ManualAccountingSummary{}
	for _, recharge := range r.recharges {
		summary.RechargeTotal += recharge.Amount
	}
	for _, usage := range r.usages {
		summary.ConsumedTotal += usage.adjusted
	}
	return summary, nil
}

func TestManualAccountingUsesSub2APIGroupMultiplier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": 1, "name": "free"}}})
		case "/api/v1/admin/groups/usage-summary":
			writeJSON(w, map[string]any{"data": []map[string]any{{"group_id": 1, "today_actual_cost": 5}}})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	repository := &manualAccountingTestRepository{recharges: []RechargeEntry{{Amount: 100}}}
	service := NewService(NewPlatformService(NewHTTPClient(server.Client())), repository, nil, newFakeSiteCache())
	site := &Site{ID: "site-1", UserID: "user-1", AdminAccountID: "account-1", Platform: PlatformSub2API}
	metrics := defaultMetrics()
	zero := 0.0
	metrics.Groups = []GroupInfo{{Name: "free", Multiplier: &zero}}

	if err := service.refreshManualAccounting(context.Background(), site, Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"}, &metrics); err != nil {
		t.Fatalf("refresh manual accounting: %v", err)
	}
	if len(repository.usages) != 1 || repository.usages[0].multiplier != 0 || repository.usages[0].adjusted != 0 {
		t.Fatalf("manual zero multiplier must remain zero, got %+v", repository.usages)
	}
	if metrics.TodayConsume.Value == nil || *metrics.TodayConsume.Value != 0 {
		t.Fatalf("today consume = %+v, want 0", metrics.TodayConsume)
	}
	if metrics.Balance.Value == nil || *metrics.Balance.Value != 100 {
		t.Fatalf("balance = %+v, want 100", metrics.Balance)
	}
}

func TestAddRechargeCreatesHistoricalBalanceEntry(t *testing.T) {
	cache := newFakeSiteCache()
	cache.add(&Site{ID: "site-1", UserID: "user-1", AdminAccountID: "account-1", Metrics: defaultMetrics()})
	repository := &manualAccountingTestRepository{}
	service := NewService(NewPlatformService(NewHTTPClient(http.DefaultClient)), repository, nil, cache)
	service.SetAdminAccountResolver(&fakeAccountResolver{current: map[string]string{"user-1": "account-1"}})

	response, err := service.AddRecharge(context.Background(), "user-1", "site-1", CreateRechargeRequest{Amount: 88.5, Note: "historical balance"})
	if err != nil {
		t.Fatalf("add recharge: %v", err)
	}
	if len(repository.recharges) != 1 || repository.recharges[0].Note != "historical balance" {
		t.Fatalf("unexpected recharge entries: %+v", repository.recharges)
	}
	if !response.Settings.ManualAccountingEnabled {
		t.Fatal("manual accounting mode was not enabled")
	}
	if response.Metrics.HistoryRecharge.Value == nil || *response.Metrics.HistoryRecharge.Value != 88.5 {
		t.Fatalf("history recharge = %+v, want 88.5", response.Metrics.HistoryRecharge)
	}
}

func TestSyncManualAccountingFallsBackToSub2APIKeyUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/me":
			w.WriteHeader(http.StatusForbidden)
			writeJSON(w, map[string]any{"message": "profile access denied"})
		case "/api/v1/keys":
			writeJSON(w, map[string]any{"data": []map[string]any{
				{"id": "key-1", "name": "first", "group": map[string]any{"name": "gpt-plus"}},
				{"id": "key-2", "name": "second", "group": map[string]any{"name": "gpt-plus"}},
			}})
		case "/api/v1/usage/stats":
			cost := 1.25
			if r.URL.Query().Get("api_key_id") == "key-2" {
				cost = 2.75
			}
			writeJSON(w, map[string]any{"data": map[string]any{"total_actual_cost": cost}})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	repository := &manualAccountingTestRepository{recharges: []RechargeEntry{{Amount: 100}}}
	cache := newFakeSiteCache()
	cache.add(&Site{
		ID: "site-1", UserID: "user-1", AdminAccountID: "account-1", Platform: PlatformSub2API,
		Status: StatusConnected, Metrics: defaultMetrics(),
		Settings: SiteSettings{ManualAccountingEnabled: true, ManualGroupMultipliers: map[string]float64{"gpt-plus": 0.08}},
		Session:  &Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"},
	})
	service := NewService(NewPlatformService(NewHTTPClient(server.Client())), repository, nil, cache)

	response, err := service.sync(context.Background(), "site-1")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if response.Status != StatusError {
		t.Fatalf("normal metadata sync should remain an error, got %s", response.Status)
	}
	if len(repository.usages) != 1 {
		t.Fatalf("expected aggregated group usage, got %+v", repository.usages)
	}
	usage := repository.usages[0]
	if usage.groupName != "gpt-plus" || usage.rawAmount != 4 || usage.multiplier != 0.08 || usage.adjusted != 0.32 {
		t.Fatalf("unexpected fallback usage: %+v", usage)
	}
	if response.Metrics.Balance.Value == nil || *response.Metrics.Balance.Value != 99.68 {
		t.Fatalf("manual balance = %+v, want 99.68", response.Metrics.Balance)
	}

	if _, err := service.sync(context.Background(), "site-1"); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(repository.usages) != 1 {
		t.Fatalf("fallback usage must be idempotent, got %+v", repository.usages)
	}
}

func TestManualAccountingFallbackSkipsUnconfiguredGroups(t *testing.T) {
	server := sub2APIKeyServer(t, "key-1", "only-key", "unconfigured", 5)
	defer server.Close()
	repository := &manualAccountingTestRepository{recharges: []RechargeEntry{{Amount: 100}}}
	service := NewService(NewPlatformService(NewHTTPClient(server.Client())), repository, nil, newFakeSiteCache())
	site := &Site{
		ID: "site-1", UserID: "user-1", AdminAccountID: "account-1", Platform: PlatformSub2API,
		Settings: SiteSettings{ManualAccountingEnabled: true, ManualGroupMultipliers: map[string]float64{"configured": 0.08}},
		Metrics:  defaultMetrics(),
	}
	if err := service.refreshManualAccountingFromKeyUsage(context.Background(), site, Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"}); err != nil {
		t.Fatalf("fallback accounting: %v", err)
	}
	if len(repository.usages) != 0 {
		t.Fatalf("an unconfigured group must not be written with a guessed multiplier: %+v", repository.usages)
	}
	if site.Metrics.Balance.Value == nil || *site.Metrics.Balance.Value != 100 {
		t.Fatalf("balance = %+v, want 100", site.Metrics.Balance)
	}
}
