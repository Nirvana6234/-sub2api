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
func (r *manualAccountingTestRepository) UpsertDailyUsage(_ context.Context, _, _, groupName string, rawAmount, multiplier, adjustedAmount float64) error {
	r.usages = append(r.usages, struct {
		groupName  string
		rawAmount  float64
		multiplier float64
		adjusted   float64
	}{groupName, rawAmount, multiplier, adjustedAmount})
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
