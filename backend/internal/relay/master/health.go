package master

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 从节点的健康判断（设计 10.3）：心跳只能证明"从节点到主节点"是通的；对外端口被挡、HTTPS 证书过期、域名解析错时心跳照样正常。
//  1. 外部探测：主节点每分钟从公网探测每台从节点的对外地址（直连这台的 IP、按它的域名做 TLS 握手），连续失败标为对外不可达；
//     握手成功后才开始给它分配用户和新 Key；探测顺带读到证书到期时间，7 天内到期告警。
//  2. 客户端失败报告：在 user_assign.go（同一台被很多用户报告连不上，暂停分配）。
//  3. 错误率降级：某台错误率明显高于其他节点（比如连不上上游），自动停止分配并告警，恢复后自动恢复。
//  4. 域名解析检查：每分钟解析各从节点的域名，和预期不符（解析到别处、解析失败）告警；解析到主节点是管理员手动切换，不告警。
// 以上状态影响小白端和新 Key 的分配；已分配到这台的 API Key 不移走。

const (
	// probeFailuresToUnreachable：连续这么多次探测失败才算对外不可达。
	probeFailuresToUnreachable = 3
	// certExpiryWarning：证书剩余不到这么久告警（设计 13：7 天）。
	certExpiryWarning = 7 * 24 * time.Hour
	// 错误率降级：近 1 分钟请求数达到 degradeMinRequests、错误率达到 degradeErrorRate，且比其他节点的中位数高 degradeFactor 倍以上，
	// 连续 degradeTicks 次检查都如此才降级；错误率回到 recoverErrorRate 以下连续 degradeTicks 次恢复。
	degradeMinRequests = 50
	degradeErrorRate   = 0.30
	recoverErrorRate   = 0.15
	degradeFactor      = 3.0
	degradeTicks       = 2
	// domainResolveTimeout、probeTimeout：单次解析 / 探测的超时。
	domainResolveTimeout = 5 * time.Second
	probeTimeout         = 8 * time.Second
)

// DomainState 是域名当前解析到哪里。
type DomainState string

const (
	DomainUnknown DomainState = "unknown" // 还没查过，或不知道这台的 IP
	DomainNode    DomainState = "node"    // 这台
	DomainDirect  DomainState = "direct"  // 直接填写了 IP，不需要 DNS 核对
	DomainMaster  DomainState = "master"  // 主节点（管理员手动切换，设计 10.3）
	DomainOther   DomainState = "other"   // 别处
	DomainFailed  DomainState = "failed"  // 解析失败
)

// ProbeResult 是一次外部探测读到的信息。
type ProbeResult struct {
	CertNotAfter time.Time
}

// ProbeFunc 做一次外部探测：直连 ip:port，按 domain 做 TLS 握手并请求 /health。
type ProbeFunc func(ctx context.Context, ip string, port int, domain string) (ProbeResult, error)

// LookupFunc 解析域名。
type LookupFunc func(ctx context.Context, host string) ([]string, error)

// NodeHealthState 是一台从节点的外部健康状态（管理页）。
type NodeHealthState struct {
	// ProbeChecked 是否已经探测过；ProbeOK 最近一次探测是否成功（失败没到阈值时保持上一次的结果）。
	ProbeChecked  bool       `json:"probe_checked"`
	ProbeOK       bool       `json:"probe_ok"`
	ProbeFailures int        `json:"probe_failures"`
	ProbeError    string     `json:"probe_error,omitempty"`
	ProbeAt       *time.Time `json:"probe_at,omitempty"`
	// Unreachable：连续探测失败达到阈值（对外不可达）。
	Unreachable  bool        `json:"unreachable"`
	CertNotAfter *time.Time  `json:"cert_not_after,omitempty"`
	DNS          DomainState `json:"dns"`
	DNSResolved  []string    `json:"dns_resolved,omitempty"`
	DNSAt        *time.Time  `json:"dns_checked_at,omitempty"`
	Degraded     bool        `json:"degraded"`
}

type nodeHealth struct {
	state          NodeHealthState
	certAlerted    bool
	dnsAlerted     bool
	unreachAlerted bool
	degradeStreak  int
	recoverStreak  int
}

// HealthDeps 是健康监控的依赖。
type HealthDeps struct {
	Nodes      func(ctx context.Context) ([]*Node, error)
	Config     func(ctx context.Context) GeneralConfig
	Probe      ProbeFunc
	Lookup     LookupFunc
	MasterIPs  func(ctx context.Context) []string
	Heartbeats *Heartbeats
	Notifier   Notifier
	Now        func() time.Time
}

// HealthMonitor 记各节点的外部健康状态。并发安全。
type HealthMonitor struct {
	deps HealthDeps

	mu    sync.Mutex
	nodes map[int64]*nodeHealth
}

// NewHealthMonitor 创建健康监控。
func NewHealthMonitor(d HealthDeps) *HealthMonitor {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Lookup == nil {
		d.Lookup = func(ctx context.Context, host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, host)
		}
	}
	if d.Probe == nil {
		d.Probe = RealProbe
	}
	return &HealthMonitor{deps: d, nodes: map[int64]*nodeHealth{}}
}

func (m *HealthMonitor) get(id int64) *nodeHealth {
	h := m.nodes[id]
	if h == nil {
		h = &nodeHealth{state: NodeHealthState{DNS: DomainUnknown}}
		m.nodes[id] = h
	}
	return h
}

// State 返回一台节点的外部健康状态。
func (m *HealthMonitor) State(nodeID int64) NodeHealthState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h := m.nodes[nodeID]; h != nil {
		return h.state
	}
	return NodeHealthState{DNS: DomainUnknown}
}

// Assignable 报告这台节点现在能不能分配新用户和新 Key（设计 10.3）：开着外部探测时必须握手成功过、没有连续失败到不可达、没有被错误率降级。
// 关着外部探测（通用配置 probe_enabled=false，本机开发、内网部署）时不看探测，只看降级。
func (m *HealthMonitor) Assignable(ctx context.Context, nodeID int64) bool {
	probing := m.deps.Config != nil && m.deps.Config(ctx).WithDefaults().ProbeEnabled()
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.nodes[nodeID]
	if h == nil {
		return !probing
	}
	if h.state.Degraded {
		return false
	}
	if !probing {
		return true
	}
	return h.state.ProbeChecked && h.state.ProbeOK && !h.state.Unreachable
}

// Forget 忘掉一台节点（停用 / 吊销后）。
func (m *HealthMonitor) Forget(nodeID int64) {
	m.mu.Lock()
	delete(m.nodes, nodeID)
	m.mu.Unlock()
}

// Run 按通用配置的间隔做检查，直到 ctx 结束。
func (m *HealthMonitor) Run(ctx context.Context) {
	for {
		interval := 60 * time.Second
		if m.deps.Config != nil {
			interval = time.Duration(m.deps.Config(ctx).WithDefaults().ProbeIntervalSeconds) * time.Second
		}
		m.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// Tick 做一轮检查：外部探测（并发）、域名解析、错误率降级。
func (m *HealthMonitor) Tick(ctx context.Context) {
	nodes, err := m.deps.Nodes(ctx)
	if err != nil {
		return
	}
	cfg := m.deps.Config(ctx).WithDefaults()
	var wg sync.WaitGroup
	for _, n := range nodes {
		if !n.Status.Serving() || n.PublicDomain == "" {
			continue
		}
		n := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cfg.ProbeEnabled() && n.Status == NodeActive {
				m.probe(ctx, n, cfg)
			}
			m.checkDomain(ctx, n)
		}()
	}
	wg.Wait()
	m.checkDegrade(ctx, nodes)
}

func nodeIP(n *Node) string {
	if n.LastSeenIP != "" {
		return n.LastSeenIP
	}
	return n.RegisteredIP
}

func (m *HealthMonitor) notify(ctx context.Context, e Event) {
	if m.deps.Notifier != nil {
		m.deps.Notifier.Notify(ctx, e)
	}
}

// probe 做一次外部探测并更新状态、发事件。
func (m *HealthMonitor) probe(ctx context.Context, n *Node, cfg GeneralConfig) {
	endpoint, err := ParseRelayEndpoint(n.PublicDomain)
	if err != nil {
		return
	}
	ip := nodeIP(n)
	if endpointIP := net.ParseIP(endpoint.Host); endpointIP != nil {
		ip = endpointIP.String()
	}
	if ip == "" {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	res, err := m.deps.Probe(pctx, ip, endpoint.PortOr(cfg.ProbePortOrDefault()), endpoint.Host)
	cancel()
	now := m.deps.Now()

	var events []Event
	m.mu.Lock()
	h := m.get(n.ID)
	s := &h.state
	s.ProbeChecked, s.ProbeAt = true, &now
	if err == nil {
		wasDown := s.Unreachable
		s.ProbeOK, s.ProbeFailures, s.ProbeError, s.Unreachable = true, 0, "", false
		if !res.CertNotAfter.IsZero() {
			t := res.CertNotAfter
			s.CertNotAfter = &t
			expiring := res.CertNotAfter.Sub(now) < certExpiryWarning
			if expiring && !h.certAlerted {
				events = append(events, Event{Kind: EventNodeCertExpiring, Severity: SeverityWarning, NodeID: n.ID,
					Detail: map[string]any{"expires_at": res.CertNotAfter.Format(time.RFC3339)}})
			}
			h.certAlerted = expiring
		}
		if wasDown {
			h.unreachAlerted = false
			events = append(events, Event{Kind: EventNodeOnline, Severity: SeverityInfo, NodeID: n.ID, Detail: map[string]any{"reason": "probe_ok"}})
		}
	} else {
		s.ProbeFailures++
		s.ProbeError = truncateErr(err)
		if s.ProbeFailures >= probeFailuresToUnreachable {
			s.ProbeOK, s.Unreachable = false, true
			if !h.unreachAlerted {
				h.unreachAlerted = true
				kind := EventNodeUnreachable
				if isCertError(err) {
					kind = EventNodeCertFailed
				}
				events = append(events, Event{Kind: kind, Severity: SeverityCritical, NodeID: n.ID, Detail: map[string]any{"error": s.ProbeError}})
			}
		}
	}
	m.mu.Unlock()
	for _, e := range events {
		m.notify(ctx, e)
	}
}

func isCertError(err error) bool {
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &unknown) || errors.As(err, &host) || errors.As(err, &invalid)
}

func truncateErr(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// DomainCheck 是一次域名解析检查的结果。
type DomainCheck struct {
	State    DomainState `json:"state"`
	Resolved []string    `json:"resolved,omitempty"`
	// Expected 是预期的 IP（这台的地址）。
	Expected string `json:"expected,omitempty"`
}

// CheckDomain 解析一个域名并和这台的 IP 比较（激活前的核对和每分钟的检查共用）：解析到这台、解析到主节点、解析到别处、解析失败。
func (m *HealthMonitor) CheckDomain(ctx context.Context, domain, expectedIP string) DomainCheck {
	out := DomainCheck{State: DomainUnknown, Expected: expectedIP}
	endpoint, err := ParseRelayEndpoint(domain)
	if err != nil {
		out.State = DomainFailed
		return out
	}
	if ip := net.ParseIP(endpoint.Host); ip != nil {
		out.Resolved = []string{ip.String()}
		out.State = DomainDirect
		return out
	}
	rctx, cancel := context.WithTimeout(ctx, domainResolveTimeout)
	defer cancel()
	addrs, err := m.deps.Lookup(rctx, endpoint.Host)
	if err != nil || len(addrs) == 0 {
		out.State = DomainFailed
		return out
	}
	sort.Strings(addrs)
	out.Resolved = addrs
	if expectedIP == "" {
		return out
	}
	if allEqual(addrs, []string{expectedIP}) {
		out.State = DomainNode
		return out
	}
	if m.deps.MasterIPs != nil {
		if masterIPs := m.deps.MasterIPs(ctx); len(masterIPs) > 0 && allEqual(addrs, masterIPs) {
			out.State = DomainMaster
			return out
		}
	}
	out.State = DomainOther
	return out
}

// allEqual 报告 addrs 里的每个地址都在 allowed 里。
func allEqual(addrs, allowed []string) bool {
	set := map[string]bool{}
	for _, a := range allowed {
		set[normalizeIP(a)] = true
	}
	for _, a := range addrs {
		if !set[normalizeIP(a)] {
			return false
		}
	}
	return len(addrs) > 0
}

func normalizeIP(s string) string {
	if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
		return ip.String()
	}
	return strings.TrimSpace(s)
}

func (m *HealthMonitor) checkDomain(ctx context.Context, n *Node) {
	ip := nodeIP(n)
	res := m.CheckDomain(ctx, n.PublicDomain, ip)
	now := m.deps.Now()
	var events []Event
	m.mu.Lock()
	h := m.get(n.ID)
	h.state.DNS, h.state.DNSResolved, h.state.DNSAt = res.State, res.Resolved, &now
	bad := res.State == DomainOther || res.State == DomainFailed
	if bad && !h.dnsAlerted {
		events = append(events, Event{Kind: EventNodeDNSMismatch, Severity: SeverityCritical, NodeID: n.ID,
			Detail: map[string]any{"domain": n.PublicDomain, "state": string(res.State), "resolved": strings.Join(res.Resolved, ",")}})
	} else if !bad && h.dnsAlerted {
		events = append(events, Event{Kind: EventNodeOnline, Severity: SeverityInfo, NodeID: n.ID, Detail: map[string]any{"reason": "dns_ok"}})
	}
	h.dnsAlerted = bad
	m.mu.Unlock()
	for _, e := range events {
		m.notify(ctx, e)
	}
}

// checkDegrade 错误率降级（设计 10.3 第 3 条）。
func (m *HealthMonitor) checkDegrade(ctx context.Context, nodes []*Node) {
	if m.deps.Heartbeats == nil {
		return
	}
	type rate struct {
		id       int64
		requests int32
		rate     float64
	}
	var rates []rate
	for _, n := range nodes {
		if n.Status != NodeActive {
			continue
		}
		h := m.deps.Heartbeats.Health(n.ID, 0)
		if !h.Online || h.Beat == nil {
			continue
		}
		req, errs := h.Beat.GetRequests_1M(), h.Beat.GetErrors_1M()
		r := rate{id: n.ID, requests: req}
		if req > 0 {
			r.rate = float64(errs) / float64(req)
		}
		rates = append(rates, r)
	}
	var events []Event
	m.mu.Lock()
	for _, r := range rates {
		var others []float64
		for _, o := range rates {
			if o.id != r.id && o.requests >= degradeMinRequests {
				others = append(others, o.rate)
			}
		}
		median := 0.0
		if len(others) > 0 {
			sort.Float64s(others)
			median = others[len(others)/2]
		}
		h := m.get(r.id)
		bad := r.requests >= degradeMinRequests && r.rate >= degradeErrorRate && (len(others) == 0 || r.rate >= degradeFactor*median+0.05)
		good := r.requests < degradeMinRequests || r.rate < recoverErrorRate
		switch {
		case bad:
			h.degradeStreak++
			h.recoverStreak = 0
			if h.degradeStreak >= degradeTicks && !h.state.Degraded {
				h.state.Degraded = true
				events = append(events, Event{Kind: EventNodeDegraded, Severity: SeverityCritical, NodeID: r.id,
					Detail: map[string]any{"error_rate_percent": int(r.rate * 100), "requests_1m": r.requests}})
			}
		case good:
			h.recoverStreak++
			h.degradeStreak = 0
			if h.recoverStreak >= degradeTicks && h.state.Degraded {
				h.state.Degraded = false
				events = append(events, Event{Kind: EventNodeOnline, Severity: SeverityInfo, NodeID: r.id, Detail: map[string]any{"reason": "error_rate_ok"}})
			}
		default:
			h.degradeStreak, h.recoverStreak = 0, 0
		}
	}
	m.mu.Unlock()
	for _, e := range events {
		m.notify(ctx, e)
	}
}

// RealProbe 是真实的外部探测：直连 ip:port（不走域名解析），按域名做 TLS 握手（按系统根证书校验证书链和主机名），
// 再请求 https://<域名>/health，要求 200；返回证书的到期时间。
func RealProbe(ctx context.Context, ip string, port int, domain string) (ProbeResult, error) {
	return ProbeWithRoots(ctx, ip, port, domain, nil)
}

// ProbeWithRoots 同 RealProbe，roots 非 nil 时用它代替系统根证书（自建 CA、测试）。
func ProbeWithRoots(ctx context.Context, ip string, port int, domain string, roots *x509.CertPool) (ProbeResult, error) {
	var res ProbeResult
	if endpoint, err := ParseRelayEndpoint(domain); err == nil {
		domain = endpoint.Host
		port = endpoint.PortOr(port)
	}
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	var notAfter time.Time
	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := &net.Dialer{Timeout: probeTimeout}
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			conn := tls.Client(raw, &tls.Config{ServerName: domain, MinVersion: tls.VersionTLS12, RootCAs: roots})
			if err := conn.HandshakeContext(ctx); err != nil {
				_ = raw.Close()
				return nil, err
			}
			if certs := conn.ConnectionState().PeerCertificates; len(certs) > 0 {
				notAfter = certs[0].NotAfter
			}
			return conn, nil
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: probeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	urlHost := domain
	if parsedIP := net.ParseIP(domain); parsedIP != nil && strings.Contains(domain, ":") {
		urlHost = "[" + domain + "]"
	}
	// DialTLSContext supplies the explicit probe port. Keep the HTTP Host header
	// as the registered host so existing virtual-host configurations continue to
	// work when the service listens on a non-default port.
	u := url.URL{Scheme: "https", Host: urlHost, Path: "/health"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return res, err
	}
	resp, err := client.Do(req)
	res.CertNotAfter = notAfter
	if err != nil {
		return res, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}
	return res, nil
}
