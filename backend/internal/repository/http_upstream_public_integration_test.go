package repository

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type publicProxyObservation struct{ target, host, authorization, proxyAuthorization string }

func newPublicUpstreamTestProxy(t *testing.T, reply string) (*httptest.Server, <-chan publicProxyObservation) {
	t.Helper()
	seen := make(chan publicProxyObservation, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", 400)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := rw.Flush(); err != nil {
			return
		}
		inner, err := http.ReadRequest(rw.Reader)
		if err != nil {
			return
		}
		defer inner.Body.Close()
		seen <- publicProxyObservation{target: r.Host, host: inner.Host, authorization: inner.Header.Get("Authorization"), proxyAuthorization: inner.Header.Get("Proxy-Authorization")}
		_, _ = rw.WriteString(reply)
		_ = rw.Flush()
	}))
	t.Cleanup(server.Close)
	return server, seen
}

func TestHTTPUpstreamPublicTransportDoesNotReuseTrustedCache(t *testing.T) {
	for _, fingerprint := range []bool{false, true} {
		t.Run(fmt.Sprint("fingerprint=", fingerprint), func(t *testing.T) {
			server, seen := newPublicUpstreamTestProxy(t, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
			svc := NewHTTPUpstream(nil).(*httpUpstreamService)
			entry, err := svc.getOrCreateClient(server.URL, 7, 1)
			require.NoError(t, err)
			var cachedCalls atomic.Int64
			entry.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				cachedCalls.Add(1)
				return nil, fmt.Errorf("trusted transport was reused")
			})
			ctx := service.WithHTTPUpstreamPublicHostsOnly(t.Context())
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://93.184.216.34/image.png", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer origin-test-token")
			var resp *http.Response
			if fingerprint {
				resp, err = svc.DoWithTLS(req, server.URL, 7, 1, &tlsfingerprint.Profile{})
			} else {
				resp, err = svc.Do(req, server.URL, 7, 1)
			}
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, "ok", string(body))
			require.Zero(t, cachedCalls.Load())
			observation := <-seen
			require.Equal(t, "93.184.216.34:80", observation.target)
			require.Equal(t, "93.184.216.34", observation.host)
			require.Equal(t, "Bearer origin-test-token", observation.authorization)
			require.Empty(t, observation.proxyAuthorization)
		})
	}
}

func TestHTTPUpstreamPublicProxyRejectsPrivateRedirect(t *testing.T) {
	server, seen := newPublicUpstreamTestProxy(t, "HTTP/1.1 302 Found\r\nLocation: http://169.254.169.254/latest/meta-data/\r\nContent-Length: 0\r\n\r\n")
	svc := NewHTTPUpstream(nil)
	req, err := http.NewRequestWithContext(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), http.MethodGet, "http://93.184.216.34/", nil)
	require.NoError(t, err)
	_, err = svc.Do(req, server.URL, 7, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not allowed")
	require.Len(t, seen, 1)
}

func TestHTTPUpstreamUserOwnedPrivateProxyRejected(t *testing.T) {
	server, seen := newPublicUpstreamTestProxy(t, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	ctx := service.WithHTTPUpstreamPublicProxyOnly(service.WithHTTPUpstreamPublicHostsOnly(t.Context()))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://93.184.216.34/", nil)
	require.NoError(t, err)
	resp, err := NewHTTPUpstream(nil).Do(req, server.URL, 7, 1)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Contains(t, err.Error(), "proxy: upstream address is not public")
	require.Empty(t, seen)
}

func TestContributionProxyProbeRejectsPrivateProxyEndpoint(t *testing.T) {
	server, seen := newPublicUpstreamTestProxy(t, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	prober := &proxyProbeService{configuredProbeURLs: []configuredProbeTarget{{url: "http://93.184.216.34/", parser: "ipify"}}}
	ctx := service.WithHTTPUpstreamPublicProxyOnly(service.WithHTTPUpstreamPublicHostsOnly(t.Context()))
	_, _, err := prober.ProbeProxy(ctx, server.URL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "proxy: upstream address is not public")
	require.Empty(t, seen)
}
