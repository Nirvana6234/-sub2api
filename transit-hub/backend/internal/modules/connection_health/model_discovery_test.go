package connection_health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"transithub/backend/internal/modules/upstream"
)

func newModelDiscoveryTestService(reader PlatformGroupReader, mySites MySitesReader, repo *fakeRepository) *Service {
	svc := newAdminGroupsService(reader, mySites, repo)
	svc.modelDiscovery = NewModelDiscoveryRunner()
	return svc
}

func TestListModels_ParsesOpenAICompatibleResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-key" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.5","owned_by":"acme"},{"id":"gpt-4o-mini","owned_by":""},{"id":"gpt-5.5","owned_by":"dup"},{"id":""}]}`))
	}))
	defer server.Close()

	runner := NewModelDiscoveryRunner()
	models, err := runner.ListModels(context.Background(), server.URL, "secret-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 deduped models, got %+v", models)
	}
	if models[0].ID != "gpt-4o-mini" || models[1].ID != "gpt-5.5" {
		t.Fatalf("expected sorted by id, got %+v", models)
	}
	if models[1].OwnedBy != "acme" {
		t.Fatalf("expected first-seen owned_by kept, got %+v", models[1])
	}
}

func TestListModels_UpstreamErrorStatusReturnsUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		runner := NewModelDiscoveryRunner()
		_, err := runner.ListModels(context.Background(), server.URL, "k")
		server.Close()
		if err == nil || err.Error() != ErrorModelListUnavailable {
			t.Fatalf("status %d: expected ErrorModelListUnavailable, got %v", status, err)
		}
	}
}

func TestListModels_InvalidBodyReturnsInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	runner := NewModelDiscoveryRunner()
	_, err := runner.ListModels(context.Background(), server.URL, "k")
	if err == nil || err.Error() != ErrorModelListInvalid {
		t.Fatalf("expected ErrorModelListInvalid, got %v", err)
	}
}

func TestListModels_EmptyDataReturnsEmptySlice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	runner := NewModelDiscoveryRunner()
	models, err := runner.ListModels(context.Background(), server.URL, "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected empty slice, got %+v", models)
	}
}

func TestDiscoverTargetModels_RejectsForeignWorkspaceTarget(t *testing.T) {
	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	svc := newModelDiscoveryTestService(fakePlatformGroupReader{}, mySites, repo)

	_, err := svc.DiscoverTargetModels(context.Background(), "user1", "newapi:ws2:100")
	if err == nil || err.Error() != ErrorProbeTargetNotFound {
		t.Fatalf("expected target not found for foreign workspace, got %v", err)
	}
}

func TestDiscoverTargetModels_CredentialUnavailableReturnsStructuredError(t *testing.T) {
	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	reader := fakePlatformGroupReader{
		groups:        []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{"g1": {{ID: "100", Name: "ch", BaseURL: "https://up"}}},
		credErr:       map[string]error{"100": &upstream.ProbeCredentialError{Reason: upstream.ReasonSecureVerificationRequired}},
	}
	svc := newModelDiscoveryTestService(reader, mySites, repo)

	_, err := svc.DiscoverTargetModels(context.Background(), "user1", "newapi:ws1:100")
	if err == nil || err.Error() != ErrorSecureVerificationRequired {
		t.Fatalf("expected secure verification error, got %v", err)
	}
}

func TestDiscoverTargetModels_SuccessReturnsModelsFromUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini","owned_by":"acme"}]}`))
	}))
	defer server.Close()

	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	reader := fakePlatformGroupReader{
		groups:        []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{"g1": {{ID: "100", Name: "ch", BaseURL: server.URL}}},
		credByAccount: map[string]upstream.ProbeCredential{"100": {BaseURL: server.URL, Key: "secret"}},
	}
	svc := newModelDiscoveryTestService(reader, mySites, repo)

	models, err := svc.DiscoverTargetModels(context.Background(), "user1", "newapi:ws1:100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gpt-4o-mini" {
		t.Fatalf("expected gpt-4o-mini discovered, got %+v", models)
	}
}

func TestDiscoverTargetModels_UsesKnownAccountModelsBeforeUpstreamList(t *testing.T) {
	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}}
	reader := fakePlatformGroupReader{
		groups:        []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{"g1": {{ID: "100", Name: "acc", Models: "gpt-5.6-sol,gpt-5.4,gpt-5.6-sol"}}},
	}
	svc := newModelDiscoveryTestService(reader, mySites, repo)

	models, err := svc.DiscoverTargetModels(context.Background(), "user1", "sub2api:ws1:100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 2 || models[0].ID != "gpt-5.4" || models[1].ID != "gpt-5.6-sol" {
		t.Fatalf("expected sorted known models, got %+v", models)
	}
}
