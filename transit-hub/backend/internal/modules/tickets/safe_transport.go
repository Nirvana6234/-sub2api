package tickets

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

type viewerIPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// Keep the same pinned-IP transport policy used by leaderboard and lottery.
// Resolve once and dial the checked literal IP, never resolve the hostname again.
func newSafeViewerTransport(base *http.Transport, resolver viewerIPResolver) *http.Transport {
	transport := base.Clone()
	transport.Proxy = nil
	transport.DialTLS = nil
	transport.DialTLSContext = nil
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ip, err := resolveViewerIP(ctx, resolver, host)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	return transport
}

func resolveViewerIP(ctx context.Context, resolver viewerIPResolver, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !isReservedIP(ip) {
			return ip, nil
		}
		return nil, errors.New("sub2api target is not public")
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, address := range addresses {
		if !isReservedIP(address.IP) {
			return address.IP, nil
		}
	}
	return nil, errors.New("sub2api target resolved to no public addresses")
}
