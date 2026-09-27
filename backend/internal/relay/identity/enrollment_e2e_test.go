package identity_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/identity"
	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---- 测试用的主节点：真实的 keystore、CA、节点管理和接入服务 ----

type testMaster struct {
	store *master.MemoryStore
	keys  *keystore.Store
	ca    *master.CA
	nodes *master.Nodes
	srv   *transport.Server
	addr  string
	pings atomic.Int64
}

func startMaster(t *testing.T, opts master.NodesOptions) *testMaster {
	t.Helper()
	kek := make([]byte, keystore.KEKLength)
	_, err := rand.Read(kek)
	require.NoError(t, err)
	keys, err := keystore.Open(t.TempDir(), kek)
	require.NoError(t, err)
	ca, err := master.NewCA(keys)
	require.NoError(t, err)

	m := &testMaster{store: master.NewMemoryStore(), keys: keys, ca: ca}
	if opts.HeartbeatInterval == 0 {
		opts.HeartbeatInterval = 50 * time.Millisecond // 生产默认 5 秒；测试里让激活后尽快领证
	}
	m.nodes = master.NewNodes(m.store, ca, nil, opts)
	require.NoError(t, m.nodes.Load(context.Background()))

	srv, err := transport.NewServer(transport.ServerOptions{
		TLS:        transport.ServerTLSOptions{Certificate: ca.MasterCertificate, Roots: ca.RootPool},
		Authorizer: m.nodes,
		AdmitConn:  m.nodes.AdmitConn,
		OnConnect:  m.nodes.OnConnect,
		Policies:   master.EnrollmentPolicies(),
	})
	require.NoError(t, err)
	m.nodes.AttachRegistry(srv.Registry())
	relayv1.RegisterRelayEnrollmentServer(srv.GRPC(), master.NewEnrollment(m.nodes, srv))
	relayv1.RegisterRelayControlServer(srv.GRPC(), &pingService{m: m})

	lis, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	m.srv, m.addr = srv, lis.Addr().String()
	return m
}

type pingService struct {
	relayv1.UnimplementedRelayControlServer
	m *testMaster
}

func (p *pingService) Ping(_ context.Context, req *relayv1.PingRequest) (*relayv1.PingResponse, error) {
	p.m.pings.Add(1)
	return &relayv1.PingResponse{Payload: req.Payload}, nil
}

// ---- 测试用的从节点 ----

type testNode struct {
	id       *identity.Identity
	client   *transport.Client
	enroller *identity.Enroller
}

func startNode(t *testing.T, m *testMaster, dir, localIP string) *testNode {
	t.Helper()
	return startNodeVia(t, m, dir, m.addr, localIP)
}

func startNodeVia(t *testing.T, m *testMaster, dir, addr, localIP string) *testNode {
	t.Helper()
	id, err := identity.Load(dir)
	require.NoError(t, err)
	fps := m.ca.RootFingerprints()
	client, err := transport.NewClient(transport.ClientOptions{
		Address:        addr,
		LocalIP:        localIP,
		ProgramVersion: "test",
		RotateGrace:    200 * time.Millisecond,
		TLS: transport.ClientTLSOptions{
			PinnedRootFingerprints: func() []string { return fps },
			Certificate:            id.TLSCertificate,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return &testNode{id: id, client: client, enroller: identity.NewEnroller(id, client, identity.EnrollerOptions{
		Hostname: "relay-test", ProgramVersion: "test", DisplayName: "test node", RenewGrace: 200 * time.Millisecond,
	})}
}

func (n *testNode) ping(t *testing.T) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := relayv1.NewRelayControlClient(n.client.Conn(transport.TierControl)).Ping(ctx, &relayv1.PingRequest{Payload: []byte("x")})
	return err
}

// enroll 跑完注册 → 管理员激活 → 领证，返回节点 ID。
func enroll(t *testing.T, m *testMaster, n *testNode) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- n.enroller.EnsureCertificate(ctx) }()

	var node *master.Node
	require.Eventually(t, func() bool {
		var err error
		node, err = m.store.GetByFingerprint(context.Background(), n.id.Fingerprint())
		return err == nil
	}, 10*time.Second, 20*time.Millisecond, "node never registered")
	require.Equal(t, master.NodePending, node.Status)
	require.Equal(t, "relay-test", node.Hostname)

	require.NoError(t, m.nodes.Activate(context.Background(), node.ID, n.id.Fingerprint(), master.Activation{
		Name: "node", PublicDomain: "relay-" + n.id.Fingerprint()[:8] + ".example.com", ActorUserID: 1,
	}))
	require.NoError(t, <-done)
	require.Equal(t, node.ID, n.id.NodeID())
	return node.ID
}

func hasAudit(m *testMaster, nodeID int64, action string) bool {
	for _, a := range m.store.Audits() {
		if a.NodeID == nodeID && a.Action == action {
			return true
		}
	}
	return false
}

// ---- 用例 ----

func TestEnrollmentLifecycle(t *testing.T) {
	m := startMaster(t, master.NodesOptions{})
	n := startNode(t, m, t.TempDir(), "")

	// 注册后、激活前：只能走接入接口，业务 RPC 被拒。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := relayv1.NewRelayEnrollmentClient(n.client.Conn(transport.TierControl)).Register(ctx, &relayv1.RegisterRequest{Hostname: "relay-test"})
	require.NoError(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(n.ping(t)), "a pending node must not reach business RPCs")

	nodeID := enroll(t, m, n)
	require.NoError(t, n.ping(t))
	require.True(t, hasAudit(m, nodeID, master.AuditRegistered))
	require.True(t, hasAudit(m, nodeID, master.AuditActivated))
	require.True(t, hasAudit(m, nodeID, master.AuditCertIssued))

	stored, err := m.store.GetByID(context.Background(), nodeID)
	require.NoError(t, err)
	require.Len(t, stored.EncryptionPublicKey, 32, "the node's encryption key is recorded for sealing credentials")
	require.Equal(t, stored.EncryptionPublicKey, n.id.EncryptionKeys()[0].PublicKey().Bytes())
	cached, ok := m.nodes.EncryptionKey(nodeID)
	require.True(t, ok, "selection reads the key from memory, not the database")
	require.Equal(t, stored.EncryptionPublicKey, cached.Bytes())
}

func TestActivationRequiresTheRegisteredFingerprint(t *testing.T) {
	m := startMaster(t, master.NodesOptions{})
	n := startNode(t, m, t.TempDir(), "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := relayv1.NewRelayEnrollmentClient(n.client.Conn(transport.TierControl)).Register(ctx, &relayv1.RegisterRequest{Hostname: "h"})
	require.NoError(t, err)
	node, err := m.store.GetByFingerprint(ctx, n.id.Fingerprint())
	require.NoError(t, err)

	err = m.nodes.Activate(ctx, node.ID, "00"+n.id.Fingerprint()[2:], master.Activation{PublicDomain: "a.example.com"})
	require.ErrorContains(t, err, "fingerprint does not match")
	err = m.nodes.Activate(ctx, node.ID, n.id.Fingerprint(), master.Activation{})
	require.ErrorContains(t, err, "public domain is required")
}

func TestRenewalRotatesKeysAndRetiresTheOldCertificate(t *testing.T) {
	m := startMaster(t, master.NodesOptions{RenewGrace: 200 * time.Millisecond})
	n := startNode(t, m, t.TempDir(), "")
	nodeID := enroll(t, m, n)
	require.NoError(t, n.ping(t))

	oldCert := n.id.TLSCertificate()
	oldEnc := n.id.EncryptionKeys()[0].PublicKey().Bytes()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, n.enroller.Renew(ctx))
	newCert := n.id.TLSCertificate()
	require.NotEqual(t, oldCert.Leaf.SerialNumber, newCert.Leaf.SerialNumber)
	require.NotEqual(t, oldEnc, n.id.EncryptionKeys()[0].PublicKey().Bytes(), "renewal changes the encryption key")
	cached, ok := m.nodes.EncryptionKey(nodeID)
	require.True(t, ok)
	require.Equal(t, n.id.EncryptionKeys()[0].PublicKey().Bytes(), cached.Bytes(), "credentials are sealed to the new key after renewal")
	reloaded := master.NewNodes(m.store, nil, nil, master.NodesOptions{})
	require.NoError(t, reloaded.Load(ctx))
	fromStore, ok := reloaded.EncryptionKey(nodeID)
	require.True(t, ok)
	require.Equal(t, cached.Bytes(), fromStore.Bytes(), "a master restart loads the same key")
	require.NoError(t, n.ping(t))
	require.True(t, hasAudit(m, nodeID, master.AuditCertRenewed))

	// 旧证书的连接在宽限期后被主节点关闭。
	oldSerial := oldCert.Leaf.SerialNumber.Text(16)
	require.Eventually(t, func() bool {
		for _, c := range m.srv.Registry().Connections(nodeID) {
			if c.Peer.CertSerial == oldSerial {
				return false
			}
		}
		return true
	}, 5*time.Second, 20*time.Millisecond)

	// 旧证书不能再建新连接。
	stale := rawClient(t, m, oldCert, "")
	require.Error(t, pingWith(stale))
}

func TestRestartReusesThePersistedCertificate(t *testing.T) {
	m := startMaster(t, master.NodesOptions{})
	dir := t.TempDir()
	first := startNode(t, m, dir, "")
	nodeID := enroll(t, m, first)
	require.NoError(t, first.client.Close())

	again := startNode(t, m, dir, "")
	require.True(t, again.id.HasValidIssued())
	require.Equal(t, nodeID, again.id.NodeID())
	require.Equal(t, first.id.Fingerprint(), again.id.Fingerprint())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, again.enroller.EnsureCertificate(ctx))
	require.NoError(t, again.ping(t))
}

func TestExpiredCertificateIsRecoveredWithTheLongTermKey(t *testing.T) {
	m := startMaster(t, master.NodesOptions{})
	dir := t.TempDir()
	first := startNode(t, m, dir, "")
	nodeID := enroll(t, m, first)
	require.NoError(t, first.client.Close())

	// 模拟证书过期（例如主节点长时间不可用）：删掉证书文件。
	require.NoError(t, os.Remove(filepath.Join(dir, "node.crt")))
	again := startNode(t, m, dir, "")
	require.False(t, again.id.HasValidIssued())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, again.enroller.EnsureCertificate(ctx), "an active node recovers without re-activation")
	require.Equal(t, nodeID, again.id.NodeID())
	require.NoError(t, again.ping(t))
	require.True(t, hasAudit(m, nodeID, master.AuditCertRecovered))
}

func TestCopiedCertificateOnASecondMachineDisconnectsTheNode(t *testing.T) {
	m := startMaster(t, master.NodesOptions{})
	n := startNode(t, m, t.TempDir(), "127.0.0.1")
	nodeID := enroll(t, m, n)
	require.NoError(t, n.ping(t))

	// 第二台机器（另一个地址）拿着同一张证书连过来。它先被当成"换了出口"放行，
	// 原节点被挤掉后自动重连——旧地址在新地址还连着时回来，判定为两份身份。
	thief := rawClient(t, m, n.id.TLSCertificate(), "127.0.0.2")
	_ = pingWith(thief)

	// 真实节点一直有心跳和选号请求，断开后马上重连；这里用持续的调用模拟。
	require.Eventually(t, func() bool {
		_ = n.ping(t)
		node, err := m.store.GetByID(context.Background(), nodeID)
		return err == nil && node.Status == master.NodePending
	}, 10*time.Second, 20*time.Millisecond, "the node goes back to pending")
	require.True(t, hasAudit(m, nodeID, master.AuditIdentityDuplicate))
	require.Eventually(t, func() bool { return n.ping(t) != nil && pingWith(thief) != nil }, 5*time.Second, 50*time.Millisecond,
		"both copies are cut off and the certificate is revoked")
}

func TestNodeThatChangesItsAddressStaysActive(t *testing.T) {
	m := startMaster(t, master.NodesOptions{})
	// 旧地址的连接经过一个会"冻结"的代理：节点换出口后，主节点上留着半开的旧连接。
	proxy := newFreezingProxy(t, m.addr)
	old := startNodeVia(t, m, t.TempDir(), proxy.addr(), "")
	nodeID := enroll(t, m, old)
	require.NoError(t, old.ping(t))
	require.NotEmpty(t, m.srv.Registry().Connections(nodeID))

	proxy.freeze()
	cert := old.id.TLSCertificate()
	require.NoError(t, old.client.Close())

	moved := rawClient(t, m, cert, "127.0.0.2")
	require.NoError(t, pingWith(moved), "the node reconnects from its new address")
	node, err := m.store.GetByID(context.Background(), nodeID)
	require.NoError(t, err)
	require.Equal(t, master.NodeActive, node.Status, "an address change alone only raises an alert")
	require.True(t, hasAudit(m, nodeID, master.AuditAddressMoved))
	require.Eventually(t, func() bool {
		for _, c := range m.srv.Registry().Connections(nodeID) {
			if strings.HasPrefix(c.Peer.RemoteAddr.String(), "127.0.0.1:") {
				return false
			}
		}
		return true
	}, 5*time.Second, 20*time.Millisecond, "half-open connections on the old address are closed")
}

func TestRenewingTheSameCertificateTwiceWithDifferentKeysDisconnectsTheNode(t *testing.T) {
	m := startMaster(t, master.NodesOptions{RenewGrace: 5 * time.Second})
	n := startNode(t, m, t.TempDir(), "")
	nodeID := enroll(t, m, n)

	// 同一张旧证书，先后带两把不同的新公钥续签：旧证书在两处被使用。
	stolen := rawClient(t, m, n.id.TLSCertificate(), "")
	enroll := relayv1.NewRelayEnrollmentClient(stolen.Conn(transport.TierControl))
	first, _, err := n.id.PrepareRequest()
	require.NoError(t, err)
	other, err := identity.Load(t.TempDir())
	require.NoError(t, err)
	second, _, err := other.PrepareRequest()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = enroll.RenewCertificate(ctx, first)
	require.NoError(t, err)
	_, err = enroll.RenewCertificate(ctx, second)
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	node, err := m.store.GetByID(context.Background(), nodeID)
	require.NoError(t, err)
	require.Equal(t, master.NodePending, node.Status)
	require.True(t, hasAudit(m, nodeID, master.AuditIdentityDuplicate))
}

func TestLostRenewalResponseIsReplayedNotTreatedAsTheft(t *testing.T) {
	m := startMaster(t, master.NodesOptions{RenewGrace: 5 * time.Second})
	dir := t.TempDir()
	n := startNode(t, m, dir, "")
	nodeID := enroll(t, m, n)

	// 续签请求到了主节点、签发了证书，但回复丢了：从节点没装上。
	req, _, err := n.id.PrepareRequest()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lost, err := relayv1.NewRelayEnrollmentClient(n.client.Conn(transport.TierControl)).RenewCertificate(ctx, req)
	require.NoError(t, err)

	// 从节点重启后重试：复用落盘的待用密钥，主节点原样返回同一张证书。
	require.NoError(t, n.client.Close())
	again := startNode(t, m, dir, "")
	require.NoError(t, again.enroller.Renew(ctx))
	require.Equal(t, lost.Certificate, again.id.TLSCertificate().Certificate[0], "the replay returns the certificate issued the first time")
	require.NoError(t, again.ping(t))

	node, err := m.store.GetByID(context.Background(), nodeID)
	require.NoError(t, err)
	require.Equal(t, master.NodeActive, node.Status, "a retried renewal is not a theft signal")
	require.False(t, hasAudit(m, nodeID, master.AuditIdentityDuplicate))
}

func TestRevokedNodeCannotRecoverUntilReactivated(t *testing.T) {
	m := startMaster(t, master.NodesOptions{})
	n := startNode(t, m, t.TempDir(), "")
	nodeID := enroll(t, m, n)
	require.NoError(t, n.ping(t))

	require.NoError(t, m.nodes.RevokeCertificates(context.Background(), nodeID, 1, "suspected compromise"))
	require.Eventually(t, func() bool { return n.ping(t) != nil }, 5*time.Second, 50*time.Millisecond)

	// 长期密钥也不能自己领新证书：节点已退回待激活，必须管理员重新激活。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _, err := n.id.PrepareRequest()
	require.NoError(t, err)
	lt := rawClient(t, m, n.id.LongTermCertificate(), "")
	_, err = relayv1.NewRelayEnrollmentClient(lt.Conn(transport.TierControl)).ObtainCertificate(ctx, req)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestRejectedKeyCannotRegisterAgain(t *testing.T) {
	m := startMaster(t, master.NodesOptions{})
	n := startNode(t, m, t.TempDir(), "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := relayv1.NewRelayEnrollmentClient(n.client.Conn(transport.TierControl)).Register(ctx, &relayv1.RegisterRequest{Hostname: "h"})
	require.NoError(t, err)
	node, err := m.store.GetByFingerprint(ctx, n.id.Fingerprint())
	require.NoError(t, err)
	require.NoError(t, m.nodes.Reject(ctx, node.ID, 1))

	require.ErrorIs(t, n.enroller.EnsureCertificate(ctx), identity.ErrRejected)
}

func TestPendingLimitAndStalePendingPurge(t *testing.T) {
	m := startMaster(t, master.NodesOptions{MaxPending: 2, RegisterBurst: 100})
	register := func(n *testNode) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := relayv1.NewRelayEnrollmentClient(n.client.Conn(transport.TierControl)).Register(ctx, &relayv1.RegisterRequest{Hostname: "h"})
		return err
	}
	a, b, c := startNode(t, m, t.TempDir(), ""), startNode(t, m, t.TempDir(), ""), startNode(t, m, t.TempDir(), "")
	require.NoError(t, register(a))
	require.NoError(t, register(b))
	require.Equal(t, codes.ResourceExhausted, status.Code(register(c)), "at most MaxPending nodes can wait")

	nodeA, err := m.store.GetByFingerprint(context.Background(), a.id.Fingerprint())
	require.NoError(t, err)
	m.store.SetCreatedAt(nodeA.ID, time.Now().Add(-25*time.Hour))
	purged, err := m.nodes.PurgeStalePending(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, purged)
	require.NoError(t, register(c), "room freed after the stale registration was purged")
}

// ---- 辅助 ----

func rawClient(t *testing.T, m *testMaster, cert *tls.Certificate, localIP string) *transport.Client {
	t.Helper()
	fps := m.ca.RootFingerprints()
	c, err := transport.NewClient(transport.ClientOptions{
		Address: m.addr,
		LocalIP: localIP,
		TLS: transport.ClientTLSOptions{
			PinnedRootFingerprints: func() []string { return fps },
			Certificate:            func() *tls.Certificate { return cert },
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func pingWith(c *transport.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := relayv1.NewRelayControlClient(c.Conn(transport.TierControl)).Ping(ctx, &relayv1.PingRequest{})
	return err
}

// freezingProxy 转发 TCP；freeze 之后停止转发但不关连接，模拟节点换出口后
// 主节点上残留的半开连接。
type freezingProxy struct {
	lis    net.Listener
	target string
	frozen atomic.Bool
}

func newFreezingProxy(t *testing.T, target string) *freezingProxy {
	t.Helper()
	lis, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	p := &freezingProxy{lis: lis, target: target}
	t.Cleanup(func() { _ = lis.Close() })
	go func() {
		for {
			in, err := lis.Accept()
			if err != nil {
				return
			}
			out, err := net.Dial("tcp4", target)
			if err != nil {
				_ = in.Close()
				continue
			}
			t.Cleanup(func() { _ = in.Close(); _ = out.Close() })
			go p.pipe(out, in)
			go p.pipe(in, out)
		}
	}()
	return p
}

func (p *freezingProxy) addr() string { return p.lis.Addr().String() }

func (p *freezingProxy) freeze() { p.frozen.Store(true) }

func (p *freezingProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if err != nil {
			if !p.frozen.Load() {
				_ = dst.Close()
			}
			return
		}
		if p.frozen.Load() {
			continue
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return
		}
	}
}

// 根证书轮换（设计 7.4，方案 B）：待激活节点在预备期间从心跳拿到新根指纹；主节点切到
// 新根签发、停用旧根后，这台节点仍能认出主节点证书、领证并连上，而本机只配置了旧指纹。
func TestPendingNodeLearnsTheStagedRootAndSurvivesTheSwitch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	m := startMaster(t, master.NodesOptions{})
	oldFPs := m.ca.RootFingerprints()
	require.Len(t, oldFPs, 1)
	oldRing, err := m.keys.Ring(keystore.PurposeRootCA)
	require.NoError(t, err)

	dir := t.TempDir()
	id, err := identity.Load(dir)
	require.NoError(t, err)
	pins, err := identity.LoadRootPins(dir, oldFPs)
	require.NoError(t, err)
	client, err := transport.NewClient(transport.ClientOptions{
		Address:        m.addr,
		ProgramVersion: "test",
		RotateGrace:    200 * time.Millisecond,
		TLS:            transport.ClientTLSOptions{PinnedRootFingerprints: pins.Pinned, Certificate: id.TLSCertificate},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	enroller := identity.NewEnroller(id, client, identity.EnrollerOptions{
		Hostname: "relay-test", ProgramVersion: "test", RenewGrace: 200 * time.Millisecond, Pins: pins,
	})

	done := make(chan error, 1)
	go func() { done <- enroller.EnsureCertificate(ctx) }()
	require.Eventually(t, func() bool {
		_, err := m.store.GetByFingerprint(ctx, id.Fingerprint())
		return err == nil
	}, 10*time.Second, 20*time.Millisecond, "node never registered")

	// 节点已在待激活、按心跳查询状态时才预备新根：新指纹只能经心跳送到。
	staged, err := m.keys.Stage(keystore.PurposeRootCA)
	require.NoError(t, err)
	require.NoError(t, m.ca.Reload())
	newFP := transport.CertificateFingerprint(staged.Certificate)
	require.Eventually(t, func() bool {
		for _, fp := range pins.Pinned() {
			if fp == newFP {
				return true
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond, "the pending node never learned the staged root")

	// 切换签发并停用旧根：之后主节点出示的证书只由新根签发。
	require.NoError(t, m.keys.Activate(keystore.PurposeRootCA, staged.Version))
	require.NoError(t, m.keys.Retire(keystore.PurposeRootCA, oldRing.Active.Version))
	require.NoError(t, m.ca.Reload())
	require.Equal(t, []string{newFP}, m.ca.RootFingerprints())

	node, err := m.store.GetByFingerprint(ctx, id.Fingerprint())
	require.NoError(t, err)
	require.NoError(t, m.nodes.Activate(ctx, node.ID, id.Fingerprint(), master.Activation{PublicDomain: "relay-b.example.com", ActorUserID: 1}))
	require.NoError(t, <-done, "obtaining the certificate reconnects with the new root")
	n := &testNode{id: id, client: client}
	require.NoError(t, n.ping(t))

	// 存下来的列表已替换成主节点当前的列表：停用的旧根不再来自存储。
	reloaded, err := identity.LoadRootPins(dir, []string{newFP})
	require.NoError(t, err)
	require.Equal(t, []string{newFP}, reloaded.Pinned())
}
