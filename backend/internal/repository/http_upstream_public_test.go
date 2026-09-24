package repository

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicUpstreamDialerRejectsPrivateResolvedAddress(t *testing.T) {
	dialer := newPublicUpstreamDialer(nil, false)
	dialer.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.168.1.10")}}, nil
	}
	dialer.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("private address must be rejected before dialing")
		return nil, nil
	}

	_, err := dialer.DialContext(context.Background(), "tcp", "provider.example:443")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not public")
}

func TestPublicUpstreamDialerPinsDialToCheckedIP(t *testing.T) {
	dialer := newPublicUpstreamDialer(nil, false)
	dialer.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}}, nil
	}
	var dialed string
	dialer.dial = func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		server, client := net.Pipe()
		go func() { _ = server.Close() }()
		return client, nil
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "provider.example:443")
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, "203.0.113.7:443", dialed)
}

func TestPublicUpstreamHTTPConnectUsesLiteralCheckedTarget(t *testing.T) {
	proxyURL, err := url.Parse("http://proxy.example:3128")
	require.NoError(t, err)
	dialer := newPublicUpstreamDialer(proxyURL, false)
	seen := make(chan [2]string, 1)
	dialer.lookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host == "provider.example" {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.8")}}, nil
		}
		return nil, nil
	}
	dialer.dial = func(_ context.Context, _, address string) (net.Conn, error) {
		require.Equal(t, "proxy.example:3128", address)
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			req, readErr := http.ReadRequest(bufio.NewReader(server))
			if readErr != nil {
				return
			}
			seen <- [2]string{req.Host, req.RequestURI}
			_, _ = io.WriteString(server, "HTTP/1.1 200 Connection Established\r\n\r\n")
			_, _ = io.Copy(io.Discard, server)
		}()
		return client, nil
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "provider.example:443")
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	got := <-seen
	require.Equal(t, "203.0.113.8:443", got[0])
	require.Equal(t, "203.0.113.8:443", got[1])
}

func TestPublicUpstreamSOCKS5UsesLiteralCheckedTarget(t *testing.T) {
	proxyURL, err := url.Parse("socks5://proxy.example:1080")
	require.NoError(t, err)
	dialer := newPublicUpstreamDialer(proxyURL, false)
	seen := make(chan string, 1)
	dialer.lookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host == "provider.example" {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
		}
		return nil, nil
	}
	dialer.dial = func(_ context.Context, _, address string) (net.Conn, error) {
		require.Equal(t, "proxy.example:1080", address)
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			header := make([]byte, 2)
			if _, err := io.ReadFull(server, header); err != nil {
				return
			}
			methods := make([]byte, int(header[1]))
			if _, err := io.ReadFull(server, methods); err != nil {
				return
			}
			_, _ = server.Write([]byte{5, 0})
			request := make([]byte, 4)
			if _, err := io.ReadFull(server, request); err != nil || request[3] != 1 {
				return
			}
			ip := make([]byte, 4)
			port := make([]byte, 2)
			if _, err := io.ReadFull(server, ip); err != nil {
				return
			}
			if _, err := io.ReadFull(server, port); err != nil {
				return
			}
			seen <- net.JoinHostPort(net.IP(ip).String(), strconv.Itoa(int(binary.BigEndian.Uint16(port))))
			_, _ = server.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
			_, _ = io.Copy(io.Discard, server)
		}()
		return client, nil
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "provider.example:443")
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, "203.0.113.10:443", <-seen)
}

func TestPublicUpstreamDialerKeepsOriginTLSServerName(t *testing.T) {
	dialer := newPublicUpstreamDialer(nil, false)
	dialer.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.11")}}, nil
	}
	serverName := make(chan string, 1)
	dialer.dial = func(_ context.Context, _, address string) (net.Conn, error) {
		require.Equal(t, "203.0.113.11:443", address)
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			tlsServer := tls.Server(server, &tls.Config{
				GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
					serverName <- hello.ServerName
					return nil, errors.New("stop after observing client hello")
				},
			})
			_ = tlsServer.Handshake()
		}()
		return client, nil
	}

	transport := &http.Transport{DialContext: dialer.DialContext}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://provider.example/image.png", nil)
	require.NoError(t, err)
	_, err = client.Do(request)
	require.Error(t, err)
	require.Equal(t, "provider.example", <-serverName)
}

func TestPublicUpstreamDialerRejectsPrivateProxyEndpoint(t *testing.T) {
	proxyURL, err := url.Parse("http://proxy.example:3128")
	require.NoError(t, err)
	dialer := newPublicUpstreamDialer(proxyURL, true)
	dialer.lookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch strings.TrimSpace(host) {
		case "provider.example":
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.9")}}, nil
		case "proxy.example":
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.9")}}, nil
		default:
			return nil, nil
		}
	}
	dialer.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("private proxy must be rejected before dialing")
		return nil, nil
	}

	_, err = dialer.DialContext(context.Background(), "tcp", "provider.example:443")
	require.Error(t, err)
	require.Contains(t, err.Error(), "proxy")
	require.Contains(t, err.Error(), "not public")
}

func TestPublicUpstreamRejectsReservedAndMixedDNSAnswers(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1", "240.0.0.1", "fc00::1", "fe80::1", "64:ff9b::a00:1"} {
		t.Run(address, func(t *testing.T) {
			dialer := newPublicUpstreamDialer(nil, false)
			dialer.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP(address)}}, nil
			}
			dialer.dial = func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("must reject all addresses before dialing")
				return nil, nil
			}
			_, err := dialer.DialContext(t.Context(), "tcp", "mixed.example:443")
			require.ErrorContains(t, err, "not public")
		})
	}
}

func TestPublicUpstreamHTTPSProxyUsesProxyTLSName(t *testing.T) {
	proxyURL, err := url.Parse("https://proxy.example:8443")
	require.NoError(t, err)
	dialer := newPublicUpstreamDialer(proxyURL, false)
	dialer.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	seen := make(chan string, 1)
	dialer.dial = func(_ context.Context, _, address string) (net.Conn, error) {
		require.Equal(t, "proxy.example:8443", address)
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			tlsServer := tls.Server(server, &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				seen <- hello.ServerName
				return nil, errors.New("stop after proxy client hello")
			}})
			_ = tlsServer.Handshake()
		}()
		return client, nil
	}
	_, err = dialer.DialContext(t.Context(), "tcp", "provider.example:443")
	require.Error(t, err)
	require.Equal(t, "proxy.example", <-seen)
}
