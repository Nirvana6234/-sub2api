package upstream

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type adminRouteRoundTripper func(*http.Request) (*http.Response, error)

func (f adminRouteRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInternalAdminRoutingPreservesRequestAndPublicIdentity(t *testing.T) {
	var calls atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.RequestURI() != "/api/v1/admin/accounts/data?ids=1%2C2&include_proxies=true" {
			t.Errorf("unexpected internal request: %s %s", r.Method, r.URL.RequestURI())
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"enabled":true}` || r.Header.Get("x-api-key") != "test-admin-key" {
			t.Error("request body or authentication was lost")
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer internal.Close()
	client, err := NewInternalAdminHTTPClient(&http.Client{Transport: adminRouteRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Error("admin request reached public transport")
		return nil, io.EOF
	})}, []string{"https://OWN.example:443/"}, internal.URL)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "https://own.example/api/v1/admin/accounts/data?ids=1%2C2&include_proxies=true", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("x-api-key", "test-admin-key")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if calls.Load() != 1 || req.URL.Host != "own.example" || req.URL.Scheme != "https" {
		t.Fatal("request was not routed once without modifying its public URL")
	}
	if client.Transport.(*internalAdminTransport).internal.(*http.Transport).Proxy != nil {
		t.Fatal("internal requests must never use the environment proxy")
	}
}

func TestInternalAdminRoutingOnlyMatchesConfiguredOriginsAndAPIPaths(t *testing.T) {
	client, err := NewInternalAdminHTTPClient(&http.Client{}, []string{"https://own.example"}, "http://sub2api-internal:8080")
	if err != nil {
		t.Fatal(err)
	}
	router := client.Transport.(*internalAdminTransport)
	for _, test := range []struct {
		url      string
		internal bool
	}{
		{"https://own.example/api/v1/admin", true},
		{"https://own.example/api/v1/admin/groups?next=https://other.example", true},
		{"https://own.example/api/v1/auth/refresh", true},
		{"https://own.example/api/v1/auth/me", true},
		{"https://third-party.example/api/v1/admin/groups", false},
		{"https://own.example.attacker.example/api/v1/admin/groups", false},
		{"http://own.example/api/v1/admin/groups", false},
		{"https://own.example:8443/api/v1/admin/groups", false},
		{"https://attacker.example@own.example/api/v1/admin/groups", false},
		{"https://own.example@attacker.example/api/v1/admin/groups", false},
		{"https://own.example/api/v1/administer", false},
		{"https://own.example/v1/responses", false},
		{"https://own.example/api/v1/keys", false},
	} {
		t.Run(test.url, func(t *testing.T) {
			var publicCalls, internalCalls int
			router.public = adminRouteRoundTripper(func(*http.Request) (*http.Response, error) {
				publicCalls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			})
			router.internal = adminRouteRoundTripper(func(*http.Request) (*http.Response, error) {
				internalCalls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			})
			req, err := http.NewRequest(http.MethodGet, test.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if (internalCalls == 1) != test.internal || publicCalls+internalCalls != 1 {
				t.Fatalf("public=%d internal=%d", publicCalls, internalCalls)
			}
		})
	}
}

func TestInternalAdminRoutingRejectsInvalidConfiguration(t *testing.T) {
	for _, value := range []string{"", "ftp://own.example", "https://user:password@own.example", "https://own.example/path", "https://own.example?token=secret", "https://own.example?", "https://own.example#fragment", "https://"} {
		if _, err := NewInternalAdminHTTPClient(nil, []string{value}, "http://sub2api-internal:8080"); err == nil {
			t.Errorf("accepted public origin %q", value)
		}
		if _, err := NewInternalAdminHTTPClient(nil, []string{"https://own.example"}, value); err == nil {
			t.Errorf("accepted target %q", value)
		}
	}
	if _, err := NewInternalAdminHTTPClient(nil, nil, "http://sub2api-internal:8080"); err == nil {
		t.Fatal("accepted target without origin allowlist")
	}
	base := &http.Client{}
	if got, err := NewInternalAdminHTTPClient(base, nil, ""); err != nil || got != base {
		t.Fatal("disabled routing changed client")
	}
}

func TestInternalAdminRoutingBlocksCredentialRedirects(t *testing.T) {
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1); w.WriteHeader(200) }))
	defer other.Close()
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	defer internal.Close()
	client, err := NewInternalAdminHTTPClient(&http.Client{}, []string{"https://own.example"}, internal.URL)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "https://own.example/api/v1/admin/groups", strings.NewReader("{}"))
	req.Header.Set("x-api-key", "test-admin-key")
	response, err := client.Do(req)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || leaked.Load() != 0 {
		t.Fatalf("redirect not blocked: err=%v leaked=%d", err, leaked.Load())
	}

	client.Transport.(*internalAdminTransport).public = adminRouteRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://own.example/api/v1/admin/groups"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	response, err = client.Get("https://third-party.example/redirect")
	if response != nil {
		response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "redirects into internal admin") {
		t.Fatalf("incoming redirect not blocked: %v", err)
	}
}
