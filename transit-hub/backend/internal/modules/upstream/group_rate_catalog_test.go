package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type groupRateSnapshotWriterSpy struct {
	calls atomic.Int32
}

func (w *groupRateSnapshotWriterSpy) SaveSiteSnapshot(context.Context, string, string, string, string, Platform, []SnapshotGroup) error {
	w.calls.Add(1)
	return nil
}

func TestCurrentGroupsSharesProbeAndPersistsSnapshot(t *testing.T) {
	var availableCalls, rateCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			availableCalls.Add(1)
			time.Sleep(10 * time.Millisecond)
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": 7, "name": "plus", "platform": "openai", "rate_multiplier": 1.25}}})
		case "/api/v1/groups/rates":
			rateCalls.Add(1)
			writeJSON(w, map[string]any{"data": map[string]any{"7": 0.75}})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cache := newFakeSiteCache()
	cache.add(&Site{
		ID: "site-1", UserID: "user-1", AdminAccountID: "workspace-1", Name: "site",
		Platform: PlatformSub2API, Session: &Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "token"},
	})
	writer := &groupRateSnapshotWriterSpy{}
	service := NewService(NewPlatformService(NewHTTPClient(server.Client())), nil, writer, cache)

	var wait sync.WaitGroup
	results := make(chan []GroupInfo, 8)
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			groups, err := service.CurrentGroups(context.Background(), "user-1", "workspace-1", "site-1")
			if err != nil {
				errors <- err
				return
			}
			results <- groups
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatalf("CurrentGroups: %v", err)
	}
	for groups := range results {
		if len(groups) != 1 || groups[0].Multiplier == nil || *groups[0].Multiplier != 0.75 {
			t.Fatalf("unexpected groups: %#v", groups)
		}
	}
	if availableCalls.Load() != 1 || rateCalls.Load() != 1 {
		t.Fatalf("expected one upstream probe, available=%d rates=%d", availableCalls.Load(), rateCalls.Load())
	}
	if writer.calls.Load() != 1 {
		t.Fatalf("expected one persisted snapshot, got %d", writer.calls.Load())
	}
}

func TestCurrentGroupsRefreshesExpiredSessionBeforeRetrying(t *testing.T) {
	var availableCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/groups/available":
			availableCalls.Add(1)
			if r.Header.Get("Authorization") == "Bearer new-access" {
				writeJSON(w, map[string]any{"data": []map[string]any{{"id": 7, "name": "plus", "platform": "openai", "rate_multiplier": 1.25}}})
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, map[string]string{"message": "expired"})
		case "/api/v1/auth/refresh":
			writeJSON(w, map[string]any{"data": map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "token_type": "Bearer", "expires_in": 3600}})
		case "/api/v1/groups/rates":
			if r.Header.Get("Authorization") != "Bearer new-access" {
				t.Fatalf("expected refreshed token for rates, got %q", r.Header.Get("Authorization"))
			}
			writeJSON(w, map[string]any{"data": map[string]any{"7": 0.75}})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cache := newFakeSiteCache()
	site := &Site{
		ID: "site-expired", UserID: "user-1", AdminAccountID: "workspace-1", Name: "site",
		Platform: PlatformSub2API,
		Session:  &Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer"},
	}
	cache.add(site)
	service := NewService(NewPlatformService(NewHTTPClient(server.Client())), nil, nil, cache)

	groups, err := service.CurrentGroups(context.Background(), "user-1", "workspace-1", "site-expired")
	if err != nil {
		t.Fatalf("CurrentGroups: %v", err)
	}
	if len(groups) != 1 || groups[0].Multiplier == nil || *groups[0].Multiplier != 0.75 {
		t.Fatalf("unexpected groups: %#v", groups)
	}
	if availableCalls.Load() != 2 {
		t.Fatalf("expected initial 401 plus refreshed retry, got %d group reads", availableCalls.Load())
	}
	cached, err := cache.Get(context.Background(), "site-expired")
	if err != nil || cached == nil || cached.Session == nil || cached.Session.AccessToken != "new-access" || cached.Session.RefreshToken != "new-refresh" {
		t.Fatalf("expected refreshed session to be cached, got site=%#v err=%v", cached, err)
	}
}
