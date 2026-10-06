package master_test

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type healthEnv struct {
	monitor  *master.HealthMonitor
	hb       *master.Heartbeats
	clock    *hbClock
	notifier *captureNotifier
	nodes    []*master.Node
	probeErr error
	probeRes master.ProbeResult
	probes   int
	lookup   map[string][]string
	lookErr  error
	cfg      master.GeneralConfig
}

func newHealthEnv(t *testing.T) *healthEnv {
	t.Helper()
	e := &healthEnv{clock: &hbClock{t: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}, notifier: &captureNotifier{},
		lookup: map[string][]string{}, nodes: []*master.Node{
			{ID: 1, Status: master.NodeActive, PublicDomain: "a.example.com", LastSeenIP: "203.0.113.1", BandwidthLimitMbps: 100},
			{ID: 2, Status: master.NodeActive, PublicDomain: "b.example.com", RegisteredIP: "203.0.113.2", BandwidthLimitMbps: 100},
		}}
	e.hb = master.NewHeartbeats(master.HeartbeatsOptions{Now: e.clock.now, Notifier: e.notifier})
	e.monitor = master.NewHealthMonitor(master.HealthDeps{
		Nodes:  func(context.Context) ([]*master.Node, error) { return e.nodes, nil },
		Config: func(context.Context) master.GeneralConfig { return e.cfg },
		Probe: func(_ context.Context, ip string, port int, domain string) (master.ProbeResult, error) {
			e.probes++
			return e.probeRes, e.probeErr
		},
		Lookup: func(_ context.Context, host string) ([]string, error) {
			if e.lookErr != nil {
				return nil, e.lookErr
			}
			return e.lookup[host], nil
		},
		MasterIPs:  func(context.Context) []string { return []string{"198.51.100.9"} },
		Heartbeats: e.hb, Notifier: e.notifier, Now: e.clock.now,
	})
	e.lookup["a.example.com"], e.lookup["b.example.com"] = []string{"203.0.113.1"}, []string{"203.0.113.2"}
	return e
}

func (e *healthEnv) kinds() map[string]int {
	out := map[string]int{}
	for _, k := range e.notifier.kinds() {
		out[k]++
	}
	return out
}

// 设计 10.3 第 1 条：外部探测握手成功后才开始分配；连续失败达到阈值标为对外不可达（停止分配并告警），恢复后自动恢复并通知。
func TestExternalProbeGatesAssignmentAndDetectsUnreachableNodes(t *testing.T) {
	ctx := context.Background()
	e := newHealthEnv(t)
	require.False(t, e.monitor.Assignable(ctx, 1), "not probed yet: no assignment before the first successful handshake")

	e.monitor.Tick(ctx)
	require.True(t, e.monitor.Assignable(ctx, 1))
	require.True(t, e.monitor.Assignable(ctx, 2))
	require.Equal(t, 2, e.probes)

	// 偶发失败（没到 3 次）不改变分配状态；连续 3 次失败才算不可达。
	e.probeErr = errors.New("connection refused")
	e.monitor.Tick(ctx)
	e.monitor.Tick(ctx)
	require.True(t, e.monitor.Assignable(ctx, 1))
	require.Zero(t, e.kinds()[master.EventNodeUnreachable])
	e.monitor.Tick(ctx)
	require.False(t, e.monitor.Assignable(ctx, 1))
	require.True(t, e.monitor.State(1).Unreachable)
	require.Equal(t, "connection refused", e.monitor.State(1).ProbeError)
	require.Equal(t, 2, e.kinds()[master.EventNodeUnreachable], "both nodes, once each")
	e.monitor.Tick(ctx)
	require.Equal(t, 2, e.kinds()[master.EventNodeUnreachable], "announced once while it lasts")

	// 恢复。
	e.probeErr = nil
	e.monitor.Tick(ctx)
	require.True(t, e.monitor.Assignable(ctx, 1))
	require.Equal(t, 2, e.kinds()[master.EventNodeOnline])

	// 关掉外部探测（本机开发、内网部署）：不看探测结果。
	fresh := newHealthEnv(t)
	off := false
	fresh.cfg.ProbeEnabledFlag = &off
	require.True(t, fresh.monitor.Assignable(ctx, 1))
	fresh.monitor.Tick(ctx)
	require.Zero(t, fresh.probes, "no probing when it is switched off")
}

// 证书问题和连不上分开通知：证书链 / 主机名 / 过期的握手错误是"证书申请或续期失败"；探测顺带读到证书到期时间，7 天内到期告警一次。
func TestProbeDistinguishesCertificateProblemsAndWarnsBeforeExpiry(t *testing.T) {
	ctx := context.Background()
	e := newHealthEnv(t)
	e.probeErr = x509.UnknownAuthorityError{}
	for i := 0; i < 3; i++ {
		e.monitor.Tick(ctx)
	}
	require.Equal(t, 2, e.kinds()[master.EventNodeCertFailed])
	require.Zero(t, e.kinds()[master.EventNodeUnreachable])

	e = newHealthEnv(t)
	e.probeRes = master.ProbeResult{CertNotAfter: e.clock.now().Add(10 * 24 * time.Hour)}
	e.monitor.Tick(ctx)
	require.Zero(t, e.kinds()[master.EventNodeCertExpiring])
	require.NotNil(t, e.monitor.State(1).CertNotAfter)
	e.probeRes = master.ProbeResult{CertNotAfter: e.clock.now().Add(6 * 24 * time.Hour)}
	e.monitor.Tick(ctx)
	e.monitor.Tick(ctx)
	require.Equal(t, 2, e.kinds()[master.EventNodeCertExpiring], "once per node")
}

// 设计 10.3 第 4 条：域名解析检查——解析到这台、解析到主节点（管理员手动切换，不告警）、解析到别处、解析失败（告警）。
func TestDomainResolutionStates(t *testing.T) {
	ctx := context.Background()
	e := newHealthEnv(t)
	e.monitor.Tick(ctx)
	require.Equal(t, master.DomainNode, e.monitor.State(1).DNS)
	require.Equal(t, master.DomainNode, e.monitor.State(2).DNS, "falls back to the registered IP when no connection IP is known")
	require.Zero(t, e.kinds()[master.EventNodeDNSMismatch])

	// 管理员把 a 的解析改到主节点：显示为主节点，不告警。
	e.lookup["a.example.com"] = []string{"198.51.100.9"}
	e.monitor.Tick(ctx)
	require.Equal(t, master.DomainMaster, e.monitor.State(1).DNS)
	require.Zero(t, e.kinds()[master.EventNodeDNSMismatch])

	// 解析到别处、解析失败：告警一次；改回来后恢复通知。
	e.lookup["a.example.com"] = []string{"192.0.2.77"}
	e.monitor.Tick(ctx)
	require.Equal(t, master.DomainOther, e.monitor.State(1).DNS)
	require.Equal(t, 1, e.kinds()[master.EventNodeDNSMismatch])
	e.monitor.Tick(ctx)
	require.Equal(t, 1, e.kinds()[master.EventNodeDNSMismatch], "announced once while it lasts")
	e.lookup["a.example.com"] = []string{"203.0.113.1"}
	e.monitor.Tick(ctx)
	require.Equal(t, master.DomainNode, e.monitor.State(1).DNS)
	require.Equal(t, 1, e.kinds()[master.EventNodeOnline])

	e.lookErr = errors.New("no such host")
	e.monitor.Tick(ctx)
	require.Equal(t, master.DomainFailed, e.monitor.State(2).DNS)
	require.Equal(t, 3, e.kinds()[master.EventNodeDNSMismatch], "both nodes now fail to resolve")

	// 激活前的核对（管理员填的域名要解析到这台的注册 IP）。
	e.lookErr = nil
	e.lookup["new.example.com"] = []string{"203.0.113.50", "203.0.113.50"}
	check := e.monitor.CheckDomain(ctx, "new.example.com", "203.0.113.50")
	require.Equal(t, master.DomainNode, check.State)
	require.Equal(t, master.DomainOther, e.monitor.CheckDomain(ctx, "new.example.com", "203.0.113.99").State)
	require.Equal(t, master.DomainUnknown, e.monitor.CheckDomain(ctx, "new.example.com", "").State, "no registered IP to compare with")
}

// 设计 10.3 第 3 条：某台错误率明显高于其他节点，自动停止分配并告警，恢复后自动恢复。
func TestErrorRateDegradeAndRecovery(t *testing.T) {
	ctx := context.Background()
	e := newHealthEnv(t)
	off := false
	e.cfg.ProbeEnabledFlag = &off
	beat := func(node int64, requests, errs int32) {
		e.hb.Record(ctx, node, &relayv1.HeartbeatRequest{StartedAtUnixMs: 1, SentAtUnixMs: e.clock.now().UnixMilli(), Requests_1M: requests, Errors_1M: errs})
	}

	// 都正常。
	beat(1, 200, 4)
	beat(2, 200, 6)
	e.monitor.Tick(ctx)
	require.True(t, e.monitor.Assignable(ctx, 1))

	// 节点 1 错误率 60%（节点 2 约 3%）：第一次检查还没降级，第二次降级。
	beat(1, 200, 120)
	beat(2, 200, 6)
	e.monitor.Tick(ctx)
	require.True(t, e.monitor.Assignable(ctx, 1), "one bad minute is not enough")
	e.monitor.Tick(ctx)
	require.False(t, e.monitor.Assignable(ctx, 1))
	require.True(t, e.monitor.Assignable(ctx, 2))
	require.True(t, e.monitor.State(1).Degraded)
	require.Equal(t, 1, e.kinds()[master.EventNodeDegraded])
	e.monitor.Tick(ctx)
	require.Equal(t, 1, e.kinds()[master.EventNodeDegraded])

	// 错误率降下来连续两次检查后恢复。
	onlineBefore := e.kinds()[master.EventNodeOnline]
	beat(1, 200, 10)
	e.monitor.Tick(ctx)
	require.False(t, e.monitor.Assignable(ctx, 1), "recovery needs two good checks")
	e.monitor.Tick(ctx)
	require.True(t, e.monitor.Assignable(ctx, 1))
	require.Equal(t, onlineBefore+1, e.kinds()[master.EventNodeOnline])

	// 所有节点都差（比如上游整体出问题）：不是某台的问题，只有明显高于其他节点才降级——没有对照时按绝对阈值。
	e2 := newHealthEnv(t)
	e2.cfg.ProbeEnabledFlag = &off
	for i := 0; i < 3; i++ {
		e2.hb.Record(ctx, 1, &relayv1.HeartbeatRequest{StartedAtUnixMs: 1, SentAtUnixMs: e2.clock.now().UnixMilli(), Requests_1M: 100, Errors_1M: 40})
		e2.hb.Record(ctx, 2, &relayv1.HeartbeatRequest{StartedAtUnixMs: 1, SentAtUnixMs: e2.clock.now().UnixMilli(), Requests_1M: 100, Errors_1M: 38})
		e2.monitor.Tick(ctx)
	}
	require.True(t, e2.monitor.Assignable(ctx, 1), "both are equally bad: not a node problem")
	require.True(t, e2.monitor.Assignable(ctx, 2))
}

// 真实探测按域名做 TLS 握手并按系统根证书校验：自签名证书的节点握手失败，错误能被认成证书问题。
func TestRealProbeRejectsUntrustedCertificates(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(srv.Close)
	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)
	port, _ := strconv.Atoi(portStr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = master.RealProbe(ctx, host, port, "relay.example.com")
	require.Error(t, err)
	var unknown x509.UnknownAuthorityError
	require.ErrorAs(t, err, &unknown, "the probe verifies the certificate chain against the system roots")

	// 端口没人听：连不上。
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := closed.Addr().(*net.TCPAddr).Port
	_ = closed.Close()
	_, err = master.RealProbe(ctx, "127.0.0.1", closedPort, "relay.example.com")
	require.Error(t, err)
	require.NotErrorAs(t, err, &unknown)
}

// 外部探测接进分配：没握手成功过的节点不分配新用户和新 Key，握手成功后才开始。
func TestUnprobedNodesAreNotAssigned(t *testing.T) {
	ctx := context.Background()
	n := &captureNotifier{}
	users := memUsers{users: map[int64]*service.User{1: {ID: 1, Email: "u@test", Status: service.StatusActive, PasswordHash: "h"}}}
	// 运行时自己的第一轮检查在后台协程里立刻跑，可能赶在测试里的节点建好之后：探测在测试放行之前一律失败，这样结果不看调度先后。
	var allow atomic.Bool
	probe := func(context.Context, string, int, string) (master.ProbeResult, error) {
		if !allow.Load() {
			return master.ProbeResult{}, errors.New("not reachable yet")
		}
		return master.ProbeResult{}, nil
	}
	lookup := func(context.Context, string) ([]string, error) { return []string{"203.0.113.1"}, nil }
	h := newRuntimeWith(t, nil, func(d *master.RuntimeDeps) {
		d.Users, d.Notifier, d.Probe, d.Lookup = users, n, probe, lookup
	})
	zero := 0
	_, err := h.runtime.SetGeneralConfig(ctx, 1, master.GeneralConfig{MasterRatioPercent: &zero})
	require.NoError(t, err)
	st, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)
	node, err := h.store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp", IdentityPublicKey: []byte{1}, RegisteredIP: "203.0.113.1"}, 20)
	require.NoError(t, err)
	require.NoError(t, h.store.Activate(ctx, node.ID, master.Activation{PublicDomain: "a.example.com", BandwidthLimitMbps: 100, At: time.Now()}))
	giveEncryptionKey(t, h, node.ID)
	master.HeartbeatsOf(h.runtime).Record(ctx, node.ID, &relayv1.HeartbeatRequest{StartedAtUnixMs: 1, SentAtUnixMs: time.Now().UnixMilli()})

	_, err = h.runtime.AssignUser(ctx, 1, 0)
	require.ErrorIs(t, err, service.ErrRelayUnavailable, "the node has not completed a probe handshake yet")

	allow.Store(true)
	master.HealthOf(h.runtime).Tick(ctx)
	res, err := h.runtime.AssignUser(ctx, 1, 0)
	require.NoError(t, err)
	require.Equal(t, node.ID, res.NodeID)
	healths, err := h.runtime.NodeHealths(ctx)
	require.NoError(t, err)
	require.Len(t, healths, 1)
	require.True(t, healths[0].External.ProbeOK)
	require.Equal(t, master.DomainNode, healths[0].External.DNS)
}

// 设计 11.1 第 5 步：激活前检查域名当前解析到这台的注册 IP；不一致时返回提示，管理员确认后才激活。
func TestActivationChecksTheDomainResolvesToTheRegisteredIP(t *testing.T) {
	ctx := context.Background()
	resolves := map[string][]string{"ok.example.com": {"203.0.113.5"}, "moved.example.com": {"192.0.2.9"}}
	lookup := func(_ context.Context, host string) ([]string, error) {
		if v, ok := resolves[host]; ok {
			return v, nil
		}
		return nil, errors.New("no such host")
	}
	h := newRuntimeWith(t, nil, func(d *master.RuntimeDeps) { d.Lookup = lookup })
	st, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)

	newNode := func(fp string) *master.Node {
		n, err := h.store.CreatePending(ctx, &master.Node{IdentityFingerprint: fp, IdentityPublicKey: []byte{1}, RegisteredIP: "203.0.113.5"}, 20)
		require.NoError(t, err)
		require.NoError(t, h.runtime.Nodes().Load(ctx))
		return n
	}
	n := newNode("fp1")
	check, err := h.runtime.CheckNodeDomain(ctx, n.ID, "Moved.Example.com ")
	require.NoError(t, err)
	require.Equal(t, master.DomainOther, check.State)
	require.Equal(t, []string{"192.0.2.9"}, check.Resolved)

	err = h.runtime.ActivateNode(ctx, n.ID, "fp1", master.Activation{PublicDomain: "moved.example.com", ActorUserID: 1}, false)
	require.ErrorIs(t, err, master.ErrDomainMismatch)
	require.Contains(t, err.Error(), "192.0.2.9")
	err = h.runtime.ActivateNode(ctx, n.ID, "fp1", master.Activation{PublicDomain: "nowhere.example.com", ActorUserID: 1}, false)
	require.ErrorIs(t, err, master.ErrDomainMismatch, "an unresolvable domain does not match either")
	got, err := h.store.GetByID(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, master.NodePending, got.Status, "a refused activation changes nothing")

	require.NoError(t, h.runtime.ActivateNode(ctx, n.ID, "fp1", master.Activation{PublicDomain: "ok.example.com", ActorUserID: 1}, false))
	got, err = h.store.GetByID(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, master.NodeActive, got.Status)

	// 管理员确认后可以在解析还没生效时先激活。
	n2 := newNode("fp2")
	require.NoError(t, h.runtime.ActivateNode(ctx, n2.ID, "fp2", master.Activation{PublicDomain: "moved.example.com", ActorUserID: 1}, true))
}
