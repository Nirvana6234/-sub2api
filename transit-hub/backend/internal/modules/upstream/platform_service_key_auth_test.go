package upstream

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginAdminWithKeySub2APIUsesXAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/groups" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "admin-key" {
			t.Fatalf("expected x-api-key, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("admin key must not be sent as Authorization, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	session, err := service.LoginAdminWithKey(server.URL, PlatformSub2API, "admin-key", "")
	if err != nil {
		t.Fatalf("LoginAdminWithKey returned error: %v", err)
	}
	if session.AdminAPIKey != "admin-key" || session.AccessToken != "" {
		t.Fatalf("unexpected session: %+v", session)
	}
}

func TestLoginWithUserKeyNewAPIUsesBearerAndUserID(t *testing.T) {
	seenSelf := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/status" {
			if got := r.Header.Get("Authorization"); got != "Bearer system-token" {
				t.Fatalf("expected bearer system token for %s, got %q", r.URL.Path, got)
			}
			if got := r.Header.Get("New-Api-User"); got != "42" {
				t.Fatalf("expected New-Api-User=42 for %s, got %q", r.URL.Path, got)
			}
		}
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"data":{"quota_per_unit":500000}}`))
		case "/api/user/self":
			seenSelf = true
			_, _ = w.Write([]byte(`{"data":{"id":42,"role":1,"quota":500000,"used_quota":100000,"group":"default"}}`))
		case "/api/log/self/stat":
			_, _ = w.Write([]byte(`{"data":{"quota":1000}}`))
		case "/api/user/self/groups":
			_, _ = w.Write([]byte(`{"data":{"default":1}}`))
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	result, err := service.LoginWithUserKey(server.URL, "42", "system-token")
	if err != nil {
		t.Fatalf("LoginWithUserKey returned error: %v", err)
	}
	if !seenSelf {
		t.Fatal("expected /api/user/self to be requested")
	}
	if !result.Session.IsAuthenticated() || result.Session.UserID != "42" || result.Session.AccessToken != "system-token" {
		t.Fatalf("unexpected session: %+v", result.Session)
	}
}

func TestLoginWithTokenUsesValidAccessTokenBeforeRefreshToken(t *testing.T) {
	refreshRequested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("expected the supplied access token for %s, got %q", r.URL.Path, got)
		}

		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			refreshRequested = true
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"refresh token expired"}`))
		case "/api/v1/auth/me":
			_, _ = w.Write([]byte(`{"data":{"balance":12,"total_recharged":30}}`))
		case "/api/v1/usage/dashboard/stats":
			_, _ = w.Write([]byte(`{"data":{"today_actual_cost":3}}`))
		case "/api/v1/groups/available", "/api/v1/groups/rates":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	result, err := service.LoginWithToken(server.URL, PlatformAuto, "", "access-token", "expired-refresh-token", "Bearer")
	if err != nil {
		t.Fatalf("LoginWithToken returned error: %v", err)
	}
	if refreshRequested {
		t.Fatal("valid access token must not be blocked by an unverified refresh token")
	}
	if result.Platform != PlatformSub2API || result.Session.AccessToken != "access-token" {
		t.Fatalf("unexpected token login result: %+v", result)
	}
	if _, err := service.RefreshSession(result.Session); err != nil {
		t.Fatalf("RefreshSession must retain a validated external access token: %v", err)
	}
	if refreshRequested {
		t.Fatal("unknown access-token expiry must not force a refresh request")
	}
}

func TestLoginWithTokenNormalizesBearerPrefixAndReadsJWTExpiry(t *testing.T) {
	expiresAtSeconds := time.Now().Add(30 * time.Minute).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expiresAtSeconds)))
	token := "header." + payload + ".signature"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("expected one normalized Bearer prefix, got %q", got)
		}
		switch r.URL.Path {
		case "/api/v1/auth/me":
			_, _ = w.Write([]byte(`{"data":{"balance":12}}`))
		case "/api/v1/usage/dashboard/stats":
			_, _ = w.Write([]byte(`{"data":{}}`))
		case "/api/v1/groups/available", "/api/v1/groups/rates":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	result, err := service.LoginWithToken(server.URL, PlatformSub2API, "", "Bearer "+token, "", "")
	if err != nil {
		t.Fatalf("LoginWithToken returned error: %v", err)
	}
	if result.Session.AccessToken != token || result.Session.TokenType != "Bearer" {
		t.Fatalf("unexpected normalized session: %+v", result.Session)
	}
	if result.Session.ExpiresAt == nil || *result.Session.ExpiresAt != expiresAtSeconds*1000 {
		t.Fatalf("expected JWT exp in milliseconds, got %+v", result.Session.ExpiresAt)
	}
}

func TestLoginWithTokenAcceptsValidatedTokenWhenOptionalMetricsFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("unexpected authorization for %s: %q", r.URL.Path, got)
		}
		if r.URL.Path == "/api/v1/auth/me" {
			_, _ = w.Write([]byte(`{"data":{"balance":12}}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"TEMPORARY_UNAVAILABLE"}`))
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	result, err := service.LoginWithToken(server.URL, PlatformSub2API, "", "access-token", "", "Bearer")
	if err != nil {
		t.Fatalf("a valid /auth/me token must survive optional metrics failures: %v", err)
	}
	if result.Session.AccessToken != "access-token" || !result.Session.IsAuthenticated() {
		t.Fatalf("unexpected session: %+v", result.Session)
	}
}

func TestSub2APIPasswordLoginReportsInteractiveTurnstileRequirement(t *testing.T) {
	loginRequested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/settings/public":
			_, _ = w.Write([]byte(`{"data":{"turnstile_enabled":true}}`))
		case "/api/v1/auth/login":
			loginRequested = true
			t.Fatal("password login must not be submitted without a browser Turnstile token")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	_, err := service.Login(server.URL, PlatformSub2API, "admin@example.com", "secret")
	if err == nil || err.Error() != ErrorInteractiveLoginRequired {
		t.Fatalf("expected interactive login requirement, got %v", err)
	}
	if loginRequested {
		t.Fatal("login request was unexpectedly sent")
	}
}

func TestVerifySub2APIAdminDistinguishesValidNonAdminToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"role":"user"}}`))
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	err := service.VerifySub2APIAdmin(Session{Platform: PlatformSub2API, BaseURL: server.URL, AccessToken: "valid-user-token", TokenType: "Bearer"})
	if err == nil || err.Error() != ErrorAdminRequired {
		t.Fatalf("expected admin-required classification, got %v", err)
	}
}

func TestResolvedPlatformForTokenAuth(t *testing.T) {
	if got := resolvedPlatformForAuthMode(PlatformAuto, AuthModeToken); got != PlatformSub2API {
		t.Fatalf("auto token site must be labeled sub2api, got %q", got)
	}
	if got := resolvedPlatformForAuthMode(PlatformAuto, AuthModePassword); got != PlatformNewAPI {
		t.Fatalf("password auto site must keep existing fallback label, got %q", got)
	}
}

func TestLoginAdminWithKeyNewAPIRejectsNonAdminRole(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/status" {
			_, _ = w.Write([]byte(`{"data":{"quota_per_unit":500000}}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer root-token" || r.Header.Get("New-Api-User") != "7" {
			t.Fatalf("missing new-api key headers: %+v", r.Header)
		}
		_, _ = w.Write([]byte(`{"data":{"id":7,"role":1}}`))
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	_, err := service.LoginAdminWithKey(server.URL, PlatformNewAPI, "root-token", "7")
	if err == nil || !strings.Contains(err.Error(), ErrorAuth) {
		t.Fatalf("expected admin role rejection, got %v", err)
	}
}

func TestLoginWithUserKeyRejectsSuccessFalseEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/status" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":false,"message":"access token invalid"}`))
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	if _, err := service.LoginWithUserKey(server.URL, "42", "invalid-token"); err == nil {
		t.Fatal("expected success=false response to reject the user key")
	}
}

func TestFetchSub2APIAdminUsageStatsUsesAdminAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "admin-key" {
			t.Fatalf("expected admin key header, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected Authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"total_actual_cost":12.5}}`))
	}))
	defer server.Close()

	service := NewPlatformService(NewHTTPClient(server.Client()))
	value, err := service.FetchSub2APIAdminUsageStats(Session{
		Platform: PlatformSub2API, BaseURL: server.URL, AdminAPIKey: "admin-key",
	}, "2026-07-14", "2026-07-14")
	if err != nil || value != 12.5 {
		t.Fatalf("unexpected result value=%v err=%v", value, err)
	}
}
