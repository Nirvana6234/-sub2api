package transport_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/relaytest"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ---- 测试用的主节点 ----

type testMaster struct {
	t        *testing.T
	pki      *relaytest.PKI
	srv      *transport.Server
	lis      net.Listener
	addr     string
	pings    atomic.Int64
	lastPeer atomic.Value // transport.PeerIdentity
	revoked  sync.Map     // nodeID -> struct{}
}

type masterConfig struct {
	epoch    string
	addr     string
	limits   map[transport.CallClass]transport.Limit
	maxBytes map[transport.CallClass]int
	cert     *tls.Certificate
}

func startMaster(t *testing.T, pki *relaytest.PKI, cfg masterConfig) *testMaster {
	t.Helper()
	m := &testMaster{t: t, pki: pki}
	cert := cfg.cert
	if cert == nil {
		cert = pki.MasterCert()
	}
	all := []transport.PeerClass{transport.PeerAnonymous, transport.PeerLongTerm, transport.PeerIssued}
	srv, err := transport.NewServer(transport.ServerOptions{
		TLS: transport.ServerTLSOptions{
			Certificate: func() (*tls.Certificate, error) { return cert, nil },
			Roots:       func() *x509.CertPool { return pki.RootPool },
		},
		Epoch: cfg.epoch,
		Authorizer: transport.AuthorizerFunc(func(_ context.Context, p transport.PeerIdentity) error {
			if _, bad := m.revoked.Load(p.NodeID); bad {
				return errors.New("certificate revoked")
			}
			return nil
		}),
		Policies:        map[string][]transport.PeerClass{relayv1.RelayEnrollment_Hello_FullMethodName: all},
		Limits:          cfg.limits,
		MaxMessageBytes: cfg.maxBytes,
	})
	require.NoError(t, err)
	relayv1.RegisterRelayEnrollmentServer(srv.GRPC(), &enrollmentService{srv: srv})
	relayv1.RegisterRelayControlServer(srv.GRPC(), &controlService{m: m})
	relayv1.RegisterRelayEventsServer(srv.GRPC(), &eventsService{})

	addr := cfg.addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	var lis net.Listener
	require.Eventually(t, func() bool {
		lis, err = net.Listen("tcp4", addr)
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "listen %s", addr)
	m.srv, m.lis, m.addr = srv, lis, lis.Addr().String()
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return m
}

type enrollmentService struct {
	relayv1.UnimplementedRelayEnrollmentServer
	srv *transport.Server
}

func (s *enrollmentService) Hello(ctx context.Context, _ *relayv1.HelloRequest) (*relayv1.HelloResponse, error) {
	return s.srv.Hello(ctx)
}

type controlService struct {
	relayv1.UnimplementedRelayControlServer
	m *testMaster
}

func (s *controlService) Ping(ctx context.Context, req *relayv1.PingRequest) (*relayv1.PingResponse, error) {
	s.m.pings.Add(1)
	if p, ok := transport.PeerFromContext(ctx); ok {
		s.m.lastPeer.Store(p)
	}
	if bytes.Equal(req.Payload, []byte("slow")) {
		time.Sleep(300 * time.Millisecond)
	}
	return &relayv1.PingResponse{Payload: req.Payload, MasterTimeUnixMs: time.Now().UnixMilli()}, nil
}

type eventsService struct {
	relayv1.UnimplementedRelayEventsServer
}

func (eventsService) Stream(stream relayv1.RelayEvents_StreamServer) error {
	for {
		in, err := stream.Recv()
		if err != nil {
			return err
		}
		if err := stream.Send(&relayv1.MasterEnvelope{Seq: in.Seq, Body: &relayv1.MasterEnvelope_Ping{Ping: in.GetPing()}}); err != nil {
			return err
		}
	}
}

// ---- 测试用的从节点 ----

type clientConfig struct {
	pinned   []string
	cert     func() *tls.Certificate
	onEpoch  func(old, new string)
	maxBytes map[transport.Tier]int
}

func newClient(t *testing.T, addr string, pki *relaytest.PKI, cfg clientConfig) *transport.Client {
	t.Helper()
	pinned := cfg.pinned
	if pinned == nil {
		pinned = []string{pki.RootFingerprint()}
	}
	c, err := transport.NewClient(transport.ClientOptions{
		Address: addr,
		TLS: transport.ClientTLSOptions{
			PinnedRootFingerprints: func() []string { return pinned },
			Certificate:            cfg.cert,
		},
		ProgramVersion:  "test",
		OnEpochChange:   cfg.onEpoch,
		MaxMessageBytes: cfg.maxBytes,
		RotateGrace:     time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func staticCert(c *tls.Certificate) func() *tls.Certificate {
	return func() *tls.Certificate { return c }
}

func hello(ctx context.Context, c *transport.Client) (*relayv1.HelloResponse, error) {
	return relayv1.NewRelayEnrollmentClient(c.Conn(transport.TierControl)).Hello(ctx, &relayv1.HelloRequest{})
}

func ping(ctx context.Context, c *transport.Client, payload []byte) (*relayv1.PingResponse, error) {
	return relayv1.NewRelayControlClient(c.Conn(transport.TierControl)).Ping(ctx, &relayv1.PingRequest{Payload: payload})
}

func ctxTimeout(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// ---- 对端分类与访问策略 ----

func TestIssuedPeerCanCallControlAndIsIdentified(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	c := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(7))})

	h, err := hello(ctxTimeout(t, 5*time.Second), c)
	require.NoError(t, err)
	require.Equal(t, relayv1.PeerClass_PEER_CLASS_ISSUED, h.PeerClass)
	require.EqualValues(t, 7, h.NodeId)
	require.Equal(t, m.srv.Epoch(), h.MasterEpoch)
	require.Equal(t, m.srv.Epoch(), c.Epoch())

	resp, err := ping(ctxTimeout(t, 5*time.Second), c, []byte("hi"))
	require.NoError(t, err)
	require.Equal(t, []byte("hi"), resp.Payload)
	peer := lastPeer(m)
	require.Equal(t, transport.PeerIssued, peer.Class)
	require.EqualValues(t, 7, peer.NodeID)
}

func TestAnonymousAndLongTermPeersOnlyReachEnrollment(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	longTerm, fp := relaytest.LongTermCert(t)

	for _, tc := range []struct {
		name  string
		cert  func() *tls.Certificate
		class relayv1.PeerClass
	}{
		{"anonymous", nil, relayv1.PeerClass_PEER_CLASS_ANONYMOUS},
		{"long-term", staticCert(longTerm), relayv1.PeerClass_PEER_CLASS_LONG_TERM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t, m.addr, pki, clientConfig{cert: tc.cert})
			h, err := hello(ctxTimeout(t, 5*time.Second), c)
			require.NoError(t, err)
			require.Equal(t, tc.class, h.PeerClass)
			require.Zero(t, h.NodeId)

			_, err = ping(ctxTimeout(t, 5*time.Second), c, []byte("x"))
			require.Equal(t, codes.PermissionDenied, status.Code(err))
		})
	}
	require.NotEmpty(t, fp)
}

func TestRevokedNodeIsDenied(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	m.revoked.Store(int64(9), struct{}{})
	c := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(9))})
	_, err := ping(ctxTimeout(t, 5*time.Second), c, []byte("x"))
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestServerRejectsForgedAndExpiredNodeCertificates(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	other := relaytest.NewPKI(t) // 同名的另一张根证书

	for name, cert := range map[string]*tls.Certificate{
		"signed by another root": other.NodeCert(7),
		"expired issued cert":    pki.NodeCert(7, time.Now().Add(-time.Minute)),
	} {
		t.Run(name, func(t *testing.T) {
			c := newClient(t, m.addr, pki, clientConfig{cert: staticCert(cert)})
			_, err := hello(ctxTimeout(t, 5*time.Second), c)
			require.Error(t, err)
			require.Equal(t, codes.Unavailable, status.Code(err))
		})
	}
}

func lastPeer(m *testMaster) transport.PeerIdentity {
	p, _ := m.lastPeer.Load().(transport.PeerIdentity)
	return p
}

// ---- 从节点按固定指纹验证主节点 ----

func TestClientRejectsMasterThatDoesNotMatchThePinnedRoot(t *testing.T) {
	pki := relaytest.NewPKI(t)
	sameSubject := relaytest.NewPKI(t)

	cases := map[string]struct {
		master *tls.Certificate
		pinned []string
		want   string
	}{
		"wrong fingerprint":            {pki.MasterCert(), []string{strings.Repeat("ab", 32)}, "pinned root"},
		"other root with same subject": {sameSubject.MasterCert(), []string{pki.RootFingerprint()}, "pinned root"},
		"leaf without the master SAN":  {pki.MasterCert(relaytest.MasterOptions{DNSNames: []string{"api.example.com"}}), nil, "does not verify"},
		"expired leaf":                 {pki.MasterCert(relaytest.MasterOptions{DNSNames: []string{transport.MasterServerName}, NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: time.Now().Add(-time.Hour)}), nil, "does not verify"},
		"chain without the root":       {pki.MasterCert(relaytest.MasterOptions{DNSNames: []string{transport.MasterServerName}, OmitRoot: true}), nil, "pinned root"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := startMaster(t, pki, masterConfig{cert: tc.master})
			c := newClient(t, m.addr, pki, clientConfig{pinned: tc.pinned, cert: staticCert(pki.NodeCert(7))})
			_, err := hello(ctxTimeout(t, 5*time.Second), c)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
			require.Zero(t, m.pings.Load())
		})
	}
}

func TestMasterPortRefusesTLS12AndPlaintext(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})

	conn, err := tls.Dial("tcp4", m.addr, &tls.Config{MaxVersion: tls.VersionTLS12, InsecureSkipVerify: true}) //nolint:gosec // 故意用 TLS 1.2 测试拒绝
	if err == nil {
		_ = conn.Close()
	}
	require.Error(t, err, "TLS 1.2 handshake must fail")

	plain, err := grpc.NewClient("passthrough:///"+m.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = plain.Close() }()
	_, err = relayv1.NewRelayEnrollmentClient(plain).Hello(ctxTimeout(t, 3*time.Second), &relayv1.HelloRequest{})
	require.Error(t, err)
	require.Zero(t, m.pings.Load())
}

// ---- 线路上没有明文 ----

func TestNoPlaintextOnTheWire(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	proxy := newTCPProxy(t, m.addr)
	c := newClient(t, proxy.addr(), pki, clientConfig{cert: staticCert(pki.NodeCert(7))})

	marker := []byte("PLAINTEXT-MARKER-relay-" + strings.Repeat("z", 40))
	_, err := hello(ctxTimeout(t, 5*time.Second), c)
	require.NoError(t, err)
	resp, err := ping(ctxTimeout(t, 5*time.Second), c, marker)
	require.NoError(t, err)
	require.Equal(t, marker, resp.Payload)

	captured := proxy.captured()
	require.Greater(t, len(captured), 1000)
	require.False(t, bytes.Contains(captured, marker), "request/response payload must not appear on the wire")
	require.False(t, bytes.Contains(captured, []byte("x-relay-proto")), "gRPC metadata must not appear on the wire")
	require.False(t, bytes.Contains(captured, []byte("/sub2api.relay.v1.")), "method names must not appear on the wire")
}

// ---- 断线重连、按节点断开、证书轮换 ----

func TestClientReconnectsAfterConnectionsAreCut(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	proxy := newTCPProxy(t, m.addr)
	c := newClient(t, proxy.addr(), pki, clientConfig{cert: staticCert(pki.NodeCert(7))})

	_, err := ping(ctxTimeout(t, 5*time.Second), c, []byte("before"))
	require.NoError(t, err)
	require.Positive(t, proxy.cut())

	require.Eventually(t, func() bool {
		_, err := ping(ctxTimeout(t, time.Second), c, []byte("after"))
		return err == nil
	}, 10*time.Second, 100*time.Millisecond)
}

func TestRegistryClosesANodesConnections(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	c := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(7))})
	other := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(8))})

	_, err := ping(ctxTimeout(t, 5*time.Second), c, []byte("x"))
	require.NoError(t, err)
	_, err = ping(ctxTimeout(t, 5*time.Second), other, []byte("x"))
	require.NoError(t, err)
	require.NotEmpty(t, m.srv.Registry().Connections(7))

	closed := m.srv.Registry().CloseNode(7)
	require.Positive(t, closed)
	require.Eventually(t, func() bool { return len(m.srv.Registry().Connections(7)) == 0 }, 5*time.Second, 20*time.Millisecond)
	require.NotEmpty(t, m.srv.Registry().Connections(8), "other nodes stay connected")
}

func TestRotateMovesCallsToTheNewCertificate(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	first, second := pki.NodeCert(7), pki.NodeCert(7)
	var current atomic.Pointer[tls.Certificate]
	current.Store(first)
	c := newClient(t, m.addr, pki, clientConfig{cert: func() *tls.Certificate { return current.Load() }})

	_, err := ping(ctxTimeout(t, 5*time.Second), c, []byte("x"))
	require.NoError(t, err)
	require.Equal(t, first.Leaf.SerialNumber.Text(16), lastPeer(m).CertSerial)

	current.Store(second)
	require.NoError(t, c.Rotate())
	_, err = ping(ctxTimeout(t, 5*time.Second), c, []byte("x"))
	require.NoError(t, err)
	require.Equal(t, second.Leaf.SerialNumber.Text(16), lastPeer(m).CertSerial)

	// 旧证书的连接在宽限期后关闭。
	require.Eventually(t, func() bool {
		for _, ci := range m.srv.Registry().Connections(7) {
			if ci.Peer.CertSerial == first.Leaf.SerialNumber.Text(16) {
				return false
			}
		}
		return true
	}, 5*time.Second, 50*time.Millisecond)
}

func TestEventStreamLoopReopensAfterDisconnect(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	c := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(7))})

	var opens atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := &transport.Backoff{Base: 50 * time.Millisecond, Max: 200 * time.Millisecond, Factor: 2, Jitter: 0.2}
		transport.RunStreamLoop(ctx, b, time.Minute, func(ctx context.Context) error {
			stream, err := relayv1.NewRelayEventsClient(c.Conn(transport.TierEvents)).Stream(ctx)
			if err != nil {
				return err
			}
			opens.Add(1)
			if err := stream.Send(&relayv1.NodeEnvelope{Seq: 1, Body: &relayv1.NodeEnvelope_Ping{Ping: &relayv1.EventPing{}}}); err != nil {
				return err
			}
			for {
				if _, err := stream.Recv(); err != nil {
					return err
				}
			}
		}, nil)
	}()

	require.Eventually(t, func() bool { return opens.Load() >= 1 }, 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool { return m.srv.Registry().CloseNode(7) > 0 }, 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool { return opens.Load() >= 2 }, 10*time.Second, 20*time.Millisecond)
	cancel()
	<-done
}

// ---- 纪元与幂等 ----

func TestEpochChangeIsReportedAndOldIdempotencyKeysAreRefused(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m1 := startMaster(t, pki, masterConfig{epoch: "epoch-1"})
	var changes []string
	var mu sync.Mutex
	c := newClient(t, m1.addr, pki, clientConfig{
		cert: staticCert(pki.NodeCert(7)),
		onEpoch: func(old, new string) {
			mu.Lock()
			changes = append(changes, old+"->"+new)
			mu.Unlock()
		},
	})
	_, err := hello(ctxTimeout(t, 5*time.Second), c)
	require.NoError(t, err)
	require.Equal(t, "epoch-1", c.Epoch())

	addr := m1.addr
	m1.srv.Stop()
	_ = m1.lis.Close()
	m2 := startMaster(t, pki, masterConfig{epoch: "epoch-2", addr: addr})

	// 重启后第一个带幂等键的调用仍带着旧纪元：主节点拒绝，客户端同时得知新纪元。
	var callErr error
	require.Eventually(t, func() bool {
		_, callErr = ping(transport.WithIdempotencyKey(ctxTimeout(t, time.Second), "k-1"), c, []byte("x"))
		return errors.Is(callErr, transport.ErrEpochChanged)
	}, 10*time.Second, 100*time.Millisecond, "last error: %v", callErr)
	require.Equal(t, "epoch-2", c.Epoch())
	require.Zero(t, m2.pings.Load(), "a call with an old-epoch key must not execute")

	mu.Lock()
	require.Equal(t, []string{"->epoch-1", "epoch-1->epoch-2"}, changes)
	mu.Unlock()

	// 按新纪元重新发起就能执行。
	_, err = ping(transport.WithIdempotencyKey(ctxTimeout(t, 5*time.Second), "k-2"), c, []byte("x"))
	require.NoError(t, err)
	require.EqualValues(t, 1, m2.pings.Load())
}

func TestIdempotentRetryReturnsTheFirstResultWithoutReexecuting(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	c := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(7))})
	other := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(8))})
	for _, cl := range []*transport.Client{c, other} {
		_, err := hello(ctxTimeout(t, 5*time.Second), cl)
		require.NoError(t, err)
	}

	// 并发重发（第一个还在执行）也只执行一次。
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := ping(transport.WithIdempotencyKey(ctxTimeout(t, 5*time.Second), "same-key"), c, []byte("slow"))
			require.NoError(t, err)
			require.Equal(t, []byte("slow"), resp.Payload)
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, m.pings.Load())

	// 超时后重发，返回第一次的结果。
	_, err := ping(transport.WithIdempotencyKey(ctxTimeout(t, 5*time.Second), "same-key"), c, []byte("slow"))
	require.NoError(t, err)
	require.EqualValues(t, 1, m.pings.Load())

	// 幂等键按节点隔离：另一台节点用同样的键会执行。
	_, err = ping(transport.WithIdempotencyKey(ctxTimeout(t, 5*time.Second), "same-key"), other, []byte("x"))
	require.NoError(t, err)
	require.EqualValues(t, 2, m.pings.Load())
}

// ---- 过载、消息上限、协议版本 ----

func TestOverloadReturnsRetryAfter(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{limits: map[transport.CallClass]transport.Limit{
		transport.ClassControl: {RatePerSecond: 1, Burst: 1, RetryAfter: 500 * time.Millisecond},
	}})
	c := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(7))})

	_, err := ping(ctxTimeout(t, 5*time.Second), c, []byte("1"))
	require.NoError(t, err)
	_, err = ping(ctxTimeout(t, 5*time.Second), c, []byte("2"))
	var overloaded *transport.OverloadedError
	require.ErrorAs(t, err, &overloaded)
	require.GreaterOrEqual(t, overloaded.RetryAfter, 500*time.Millisecond)
	require.EqualValues(t, 1, m.pings.Load())

	// 限流按节点：另一台节点不受影响。
	other := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(8))})
	_, err = ping(ctxTimeout(t, 5*time.Second), other, []byte("3"))
	require.NoError(t, err)
}

func TestOversizeMessagesFailClearly(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{maxBytes: map[transport.CallClass]int{transport.ClassControl: 1024}})

	// 主节点侧上限。
	c := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(7))})
	_, err := ping(ctxTimeout(t, 5*time.Second), c, bytes.Repeat([]byte("a"), 4096))
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Contains(t, err.Error(), "exceeds")

	// 从节点侧上限：不发出去。
	small := newClient(t, m.addr, pki, clientConfig{cert: staticCert(pki.NodeCert(7)), maxBytes: map[transport.Tier]int{transport.TierControl: 512}})
	_, err = ping(ctxTimeout(t, 5*time.Second), small, bytes.Repeat([]byte("a"), 2048))
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Zero(t, m.pings.Load())
}

func TestCallsWithoutAProtocolVersionAreRefused(t *testing.T) {
	pki := relaytest.NewPKI(t)
	m := startMaster(t, pki, masterConfig{})
	cfg, err := transport.ClientTLSConfig(transport.ClientTLSOptions{
		PinnedRootFingerprints: func() []string { return []string{pki.RootFingerprint()} },
		Certificate:            staticCert(pki.NodeCert(7)),
	})
	require.NoError(t, err)
	raw, err := grpc.NewClient("passthrough:///"+m.addr, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()

	var trailer metadata.MD
	_, err = relayv1.NewRelayControlClient(raw).Ping(ctxTimeout(t, 5*time.Second), &relayv1.PingRequest{}, grpc.Trailer(&trailer))
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, []string{"PROTOCOL_TOO_OLD"}, trailer.Get("x-relay-reason"))

	ctx := metadata.AppendToOutgoingContext(ctxTimeout(t, 5*time.Second), "x-relay-proto", "0.9")
	_, err = relayv1.NewRelayControlClient(raw).Ping(ctx, &relayv1.PingRequest{})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Zero(t, m.pings.Load())
}

// ---- 进程内 TCP 代理：抓包、掐断连接 ----

type tcpProxy struct {
	t      *testing.T
	lis    net.Listener
	target string
	mu     sync.Mutex
	conns  []net.Conn
	buf    bytes.Buffer
}

func newTCPProxy(t *testing.T, target string) *tcpProxy {
	t.Helper()
	lis, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	p := &tcpProxy{t: t, lis: lis, target: target}
	go p.serve()
	t.Cleanup(func() { _ = lis.Close(); p.cut() })
	return p
}

func (p *tcpProxy) addr() string { return p.lis.Addr().String() }

func (p *tcpProxy) serve() {
	for {
		in, err := p.lis.Accept()
		if err != nil {
			return
		}
		out, err := net.Dial("tcp4", p.target)
		if err != nil {
			_ = in.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, in, out)
		p.mu.Unlock()
		go p.pipe(out, in)
		go p.pipe(in, out)
	}
}

func (p *tcpProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			p.mu.Lock()
			_, _ = p.buf.Write(buf[:n])
			p.mu.Unlock()
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			_ = dst.Close()
			return
		}
	}
}

func (p *tcpProxy) captured() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.buf.Bytes()...)
}

func (p *tcpProxy) cut() int {
	p.mu.Lock()
	conns := p.conns
	p.conns = nil
	p.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	return len(conns)
}
