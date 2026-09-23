package tickets

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type viewerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f viewerRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSub2APIClientRejectsPrivateTargets(t *testing.T) {
	for _, origin := range []string{"http://localhost:8080", "http://127.0.0.1", "http://[::1]", "http://169.254.169.254", "http://10.0.0.1", "http://100.64.0.1", "http://224.0.0.1"} {
		t.Run(origin, func(t *testing.T) {
			calls := 0
			client := NewSub2APIClient(&http.Client{Transport: viewerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"id":"1"}}`)), Request: r}, nil
			})})
			if _, err := client.FetchCurrentUser(origin, "test-viewer-token"); err == nil {
				t.Fatal("private target must be rejected")
			}
			if calls != 0 {
				t.Fatalf("private target reached the transport %d times", calls)
			}
		})
	}
}

func TestSub2APIClientDoesNotFollowRedirects(t *testing.T) {
	calls := 0
	client := NewSub2APIClient(&http.Client{Transport: viewerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"http://127.0.0.1/internal"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"id":"1"}}`)), Request: r}, nil
	})})
	if _, err := client.FetchCurrentUser("https://example.com", "test-viewer-token"); err == nil {
		t.Fatal("redirect must not yield a verified identity")
	}
	if calls != 1 {
		t.Fatalf("redirect followed: got %d requests", calls)
	}
}

func TestSub2APIClientVerifiesPublicOrigin(t *testing.T) {
	client := NewSub2APIClient(&http.Client{Transport: viewerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://example.com/api/v1/auth/me" || r.Header.Get("Authorization") != "Bearer test-viewer-token" {
			t.Fatal("identity verification request lost its origin or authentication")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"id":7,"email":"viewer@example.com","role":"user"}}`)), Request: r}, nil
	})})
	user, err := client.FetchCurrentUser("https://example.com", "test-viewer-token")
	if err != nil || user.ID != "7" || user.Email != "viewer@example.com" {
		t.Fatalf("valid identity rejected: user=%+v err=%v", user, err)
	}
}

type viewerResolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f viewerResolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

func TestViewerTransportRejectsPrivateDNS(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "224.0.0.1"} {
		t.Run(ip, func(t *testing.T) {
			lookups := 0
			transport := newSafeViewerTransport(http.DefaultTransport.(*http.Transport), viewerResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
				lookups++
				return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
			}))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := transport.DialContext(ctx, "tcp", "attacker.example:80")
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "no public addresses") || lookups != 1 {
				t.Fatalf("private DNS target was not rejected before dialing: lookups=%d err=%v", lookups, err)
			}
		})
	}
}

func TestViewerResolverSelectsCheckedPublicIP(t *testing.T) {
	lookups := 0
	ip, err := resolveViewerIP(context.Background(), viewerResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}), "example.com")
	if err != nil || ip.String() != "8.8.8.8" || lookups != 1 {
		t.Fatalf("expected checked literal IP from one DNS lookup, got %v, %v (%d lookups)", ip, err, lookups)
	}
}

func TestSub2APIClientSecuresClonedTransport(t *testing.T) {
	original := &http.Client{Transport: http.DefaultTransport}
	client := NewSub2APIClient(original)
	transport := client.client.Transport.(*http.Transport)
	if original.Transport != http.DefaultTransport || original.CheckRedirect != nil {
		t.Fatal("caller-owned client was mutated")
	}
	if transport == http.DefaultTransport || transport.Proxy != nil || transport.DialTLSContext != nil || transport.DialTLS != nil {
		t.Fatal("viewer transport can bypass public-IP checks")
	}
}
