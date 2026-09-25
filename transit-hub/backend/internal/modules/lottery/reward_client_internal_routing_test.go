package lottery

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"transithub/backend/internal/modules/upstream"
)

func TestRewardInternalRoutingRedeemsAndCleansUpThroughConfiguredTarget(t *testing.T) {
	var calls atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-admin-token" {
			t.Error("missing admin authentication")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/api/v1/admin/redeem-codes/create-and-redeem":
			if r.Method != http.MethodPost || body["user_id"] != float64(123) || body["value"] != float64(2) {
				t.Errorf("unexpected redemption: %s %#v", r.Method, body)
			}
		case "/api/v1/admin/users/123":
			if r.Method != http.MethodPut {
				t.Errorf("unexpected update method: %s", r.Method)
			}
			rates, ok := body["group_rates"].(map[string]any)
			if !ok {
				t.Error("missing dedicated rates")
			} else if value, exists := rates["456"]; !exists || value != nil {
				t.Errorf("expected expired rate removal: %#v", rates)
			}
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"id":"mock-reward"}}`))
	}))
	defer internal.Close()
	client, err := NewRewardClientWithInternalRouting(nil, false, []string{"https://own.example"}, internal.URL)
	if err != nil {
		t.Fatal(err)
	}
	session := upstream.Session{Platform: upstream.PlatformSub2API, BaseURL: "https://own.example", AccessToken: "test-admin-token", TokenType: "Bearer"}
	job := RewardJob{IdempotencyKey: "test-reward", Winner: Winner{Sub2apiUserID: "123"}, Prize: Prize{Type: PrizeTypeBalance, BalanceAmount: "2"}}
	if result := client.Redeem(context.Background(), session, job); result.Status != RewardFulfilled {
		t.Fatalf("redemption: %#v", result)
	}
	cleanup := RateCleanupJob{RewardJob: RewardJob{Winner: Winner{Sub2apiUserID: "123"}, Prize: Prize{Type: PrizeTypeSubscription, GroupID: "456"}}}
	if result := client.CleanupDedicatedRate(context.Background(), session, cleanup, nil); result.Status != RewardFulfilled {
		t.Fatalf("cleanup: %#v", result)
	}
	if calls.Load() != 2 || session.BaseURL != "https://own.example" {
		t.Fatal("internal routing did not preserve public identity")
	}
}

func TestRewardInternalRoutingPreservesPublicSSRFDNSProtection(t *testing.T) {
	var calls atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer internal.Close()
	// An unconfigured public-looking domain resolves to a forbidden private IP.
	// Internal routing must retain the already-installed DNS/IP validation.
	safe := newViewerTransport(lotteryFakeResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}, false)
	client, err := NewRewardClientWithInternalRouting(&http.Client{Transport: safe}, false, []string{"https://own.example"}, internal.URL)
	if err != nil {
		t.Fatal(err)
	}
	result := client.Redeem(context.Background(), upstream.Session{Platform: upstream.PlatformSub2API, BaseURL: "https://third-party.example", AccessToken: "test-token", TokenType: "Bearer"}, RewardJob{Winner: Winner{Sub2apiUserID: "123"}, Prize: Prize{Type: PrizeTypeBalance, BalanceAmount: "2"}})
	if result.Status != RewardRetryableFailed || !strings.Contains(result.Detail, "no allowed addresses") || calls.Load() != 0 {
		t.Fatalf("SSRF protection bypassed: %#v calls=%d", result, calls.Load())
	}
	plain, err := NewRewardClientWithInternalRouting(nil, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := plain.client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil || transport.Proxy != nil {
		t.Fatal("default SSRF-safe transport was not installed before wrapping")
	}
}

func TestRewardInternalRoutingRejectsCredentialRedirect(t *testing.T) {
	var leaked atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1); w.WriteHeader(200) }))
	defer external.Close()
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, external.URL, http.StatusTemporaryRedirect)
	}))
	defer internal.Close()
	client, err := NewRewardClientWithInternalRouting(nil, false, []string{"https://own.example"}, internal.URL)
	if err != nil {
		t.Fatal(err)
	}
	result := client.Redeem(context.Background(), upstream.Session{Platform: upstream.PlatformSub2API, BaseURL: "https://own.example", AccessToken: "test-token", TokenType: "Bearer"}, RewardJob{Winner: Winner{Sub2apiUserID: "123"}, Prize: Prize{Type: PrizeTypeBalance, BalanceAmount: "2"}})
	if result.Status != RewardRetryableFailed || !strings.Contains(result.Detail, "redirects are not allowed") || leaked.Load() != 0 {
		t.Fatalf("redirect leaked: %#v calls=%d", result, leaked.Load())
	}
}
