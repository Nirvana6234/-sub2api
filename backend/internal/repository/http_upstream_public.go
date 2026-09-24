package repository

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"golang.org/x/net/proxy"

	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Untrusted destinations must never reuse a connection opened by a trusted
// request. Keep their transport local to the request (including redirects),
// and close its pool when the response body is closed.
func (s *httpUpstreamService) doPublicUpstream(req *http.Request, rawProxy string, concurrency int) (*http.Response, error) {
	proxyKey, proxyURL, err := normalizeProxyURL(rawProxy)
	if err != nil {
		return nil, err
	}
	profile := service.HTTPUpstreamProfileFromContext(req.Context())
	settings := s.applyProfilePoolSettings(s.resolvePoolSettings(s.getIsolationMode(), concurrency), profile)
	transport, err := buildUpstreamTransport(settings, nil, s.resolveProtocolMode(profile, proxyKey, proxyURL))
	if err != nil {
		return nil, err
	}
	dialer := newPublicUpstreamDialer(proxyURL, service.HTTPUpstreamPublicProxyOnly(req.Context()))
	transport.DialContext = dialer.DialContext
	client := &http.Client{Transport: transport, CheckRedirect: s.redirectChecker}
	client = s.httpClientForUpstreamRequest(client, req)
	client = httpClientWithGrokAccessDeniedFallback(client)
	resp, err := servertiming.Do(client, req)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	decompressResponseBody(resp)
	resp.Body = wrapTrackedBody(resp.Body, transport.CloseIdleConnections)
	return resp, nil
}

type publicUpstreamDialer struct {
	proxyURL        *url.URL
	publicProxyOnly bool
	lookupIP        func(context.Context, string) ([]net.IPAddr, error)
	dial            func(context.Context, string, string) (net.Conn, error)
}

func newPublicUpstreamDialer(proxyURL *url.URL, publicProxyOnly bool) *publicUpstreamDialer {
	return &publicUpstreamDialer{
		proxyURL: proxyURL, publicProxyOnly: publicProxyOnly,
		lookupIP: net.DefaultResolver.LookupIPAddr,
		dial:     newUpstreamDialer().DialContext,
	}
}

// Resolve locally and pass only checked IP literals to the socket or proxy.
// The request URL is unchanged, preserving Host and the origin TLS SNI.
func (d *publicUpstreamDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultUpstreamDialTimeout)
	defer cancel()
	targets, err := d.publicAddresses(ctx, address)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, target := range targets {
		var conn net.Conn
		if d.proxyURL == nil {
			conn, lastErr = d.dial(ctx, network, target)
		} else {
			conn, lastErr = d.dialProxy(ctx, network, target)
		}
		if lastErr == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func (d *publicUpstreamDialer) publicAddresses(ctx context.Context, address string) ([]string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var ips []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IPAddr{{IP: ip}}
	} else {
		ips, err = d.lookupIP(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve upstream host: %w", err)
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("upstream host has no resolved addresses")
	}
	addresses := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip.Zone != "" || !isPublicUpstreamIP(ip.IP) {
			return nil, errors.New("upstream address is not public")
		}
		addresses = append(addresses, net.JoinHostPort(ip.IP.String(), port))
	}
	return addresses, nil
}

var nonPublicUpstreamPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	// Translation mechanisms must not turn an apparently public IPv6 address
	// into a private IPv4 connection downstream.
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
}

func isPublicUpstreamIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range nonPublicUpstreamPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func (d *publicUpstreamDialer) dialProxy(ctx context.Context, network, target string) (net.Conn, error) {
	p := d.proxyURL
	port := p.Port()
	if port == "" {
		switch p.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			port = "1080"
		}
	}
	proxyAddress := net.JoinHostPort(p.Hostname(), port)
	if d.publicProxyOnly {
		addresses, err := d.publicAddresses(ctx, proxyAddress)
		if err != nil {
			return nil, fmt.Errorf("proxy: %w", err)
		}
		proxyAddress = addresses[0]
	}
	switch p.Scheme {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if p.User != nil {
			password, _ := p.User.Password()
			auth = &proxy.Auth{User: p.User.Username(), Password: password}
		}
		socks, err := proxy.SOCKS5("tcp", proxyAddress, auth, &upstreamProxyForwardDialer{dial: d.dial})
		if err != nil {
			return nil, err
		}
		contextDialer, ok := socks.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("proxy dialer does not support cancellation")
		}
		return contextDialer.DialContext(ctx, network, target)
	case "http", "https":
		return d.connectHTTPProxy(ctx, network, proxyAddress, target)
	default:
		return nil, errors.New("unsupported upstream proxy scheme")
	}
}

type upstreamProxyForwardDialer struct {
	dial func(context.Context, string, string) (net.Conn, error)
}

func (d *upstreamProxyForwardDialer) Dial(network, address string) (net.Conn, error) {
	return d.dial(context.Background(), network, address)
}

func (d *upstreamProxyForwardDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.dial(ctx, network, address)
}

// CONNECT pins the target for HTTP as well as HTTPS. A proxy that does not
// support tunneling fails closed; it must not resolve the target hostname.
func (d *publicUpstreamDialer) connectHTTPProxy(ctx context.Context, network, proxyAddress, target string) (net.Conn, error) {
	conn, err := d.dial(ctx, network, proxyAddress)
	if err != nil {
		return nil, err
	}
	rawConn := conn
	stopCancel := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	success := false
	defer func() {
		stopCancel()
		if !success {
			_ = rawConn.Close()
		}
	}()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if d.proxyURL.Scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: d.proxyURL.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = tlsConn
	}
	connect := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if d.proxyURL.User != nil {
		password, _ := d.proxyURL.User.Password()
		credentials := d.proxyURL.User.Username() + ":" + password
		connect.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	}
	if err := connect.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, connect)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream proxy CONNECT returned %d", resp.StatusCode)
	}
	if !stopCancel() && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	success = true
	return &upstreamProxyBufferedConn{Conn: conn, reader: reader}, nil
}

type upstreamProxyBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *upstreamProxyBufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
