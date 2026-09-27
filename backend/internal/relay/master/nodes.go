package master

import (
	"context"
	"crypto/ecdh"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Severity 是通知的严重程度（设计 13）。
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Event 是节点相关的通知事件，WP15 接到运维告警和飞书。
type Event struct {
	Kind     string
	Severity Severity
	NodeID   int64
	Detail   map[string]any
}

// Notifier 发送节点事件。nil 表示不发。
type Notifier interface {
	Notify(ctx context.Context, e Event)
}

// 节点事件类型（设计 13 的表）。
const (
	EventNodeRegistered       = "node_registered"
	EventIdentityDuplicated   = "identity_duplicated"
	EventCertificateRecovered = "certificate_recovered"
	EventNodeIPChanged        = "node_ip_changed"
	EventPendingPurged        = "pending_purged"
)

// 审计动作。
const (
	AuditRegistered        = "registered"
	AuditActivated         = "activated"
	AuditRejected          = "rejected"
	AuditRejectedAll       = "rejected_all_pending"
	AuditDisabled          = "disabled"
	AuditEnabled           = "enabled"
	AuditCertIssued        = "certificate_issued"
	AuditCertRecovered     = "certificate_recovered"
	AuditCertRenewed       = "certificate_renewed"
	AuditCertsRevoked      = "certificates_revoked"
	AuditIdentityDuplicate = "identity_duplicated"
	AuditMultiIPChanged    = "allow_multi_ip_changed"
	AuditPendingPurged     = "pending_purged"
	AuditAddressMoved      = "address_moved"
)

// NodesOptions 配置节点管理。零值字段取设计 19 的默认值。
type NodesOptions struct {
	MaxPending          int           // 待激活上限，默认 20
	PendingTTL          time.Duration // 待激活超时，默认 24 小时
	CertificateLifetime time.Duration // 默认 24 小时
	RenewGrace          time.Duration // 续签后旧证书连接的宽限，默认 60 秒
	HeartbeatInterval   time.Duration // 默认 5 秒
	// RefreshInterval 是状态缓存与数据库对账的间隔，默认 30 秒。
	RefreshInterval time.Duration
	// RegisterInterval / RegisterBurst：同一来源 IP 的注册频率（默认每 10 秒 1 次，突发 3 次）。
	RegisterInterval time.Duration
	RegisterBurst    int
	// CloseDelay：吊销、判定身份重复之后，等多久断开这台的连接（默认 100 毫秒），
	// 让当前这次请求的拒绝回复先发出去。在此之前状态和吊销已经生效，新的调用一律被拒。
	CloseDelay time.Duration
}

func (o NodesOptions) withDefaults() NodesOptions {
	if o.MaxPending <= 0 {
		o.MaxPending = 20
	}
	if o.PendingTTL <= 0 {
		o.PendingTTL = 24 * time.Hour
	}
	if o.CertificateLifetime <= 0 {
		o.CertificateLifetime = NodeCertificateLifetime
	}
	if o.RenewGrace <= 0 {
		o.RenewGrace = 60 * time.Second
	}
	if o.HeartbeatInterval <= 0 {
		o.HeartbeatInterval = 5 * time.Second
	}
	if o.RefreshInterval <= 0 {
		o.RefreshInterval = 30 * time.Second
	}
	if o.RegisterInterval <= 0 {
		o.RegisterInterval = 10 * time.Second
	}
	if o.RegisterBurst <= 0 {
		o.RegisterBurst = 3
	}
	if o.CloseDelay <= 0 {
		o.CloseDelay = 100 * time.Millisecond
	}
	return o
}

// Nodes 管理从节点的身份和状态，实现 transport 的 Authorizer 和连接准入。
//
// 每个 RPC 都要查"节点是否已激活、证书是否被吊销"，所以状态放在内存缓存里：
// 本进程内的管理操作直接更新缓存，另有定时对账兜底（主节点只有一个进程）。
type Nodes struct {
	store    NodeStore
	ca       *CA
	notifier Notifier
	opts     NodesOptions
	now      func() time.Time

	registry *transport.ConnRegistry

	mu         sync.RWMutex
	status     map[int64]NodeStatus
	multiIP    map[int64]bool
	revoked    map[string]struct{}
	superseded map[string]time.Time      // 已被续签的证书 → 旧连接最迟关闭时间
	moves      map[int64]ipMove          // 最近一次换出口
	renewals   map[string]string         // 新证书 → 它替换的旧证书（新证书还没被用过）
	encKeys    map[int64]*ecdh.PublicKey // 节点当前的 X25519 加密公钥（选号下发凭据用，WP7）
}

// NewNodes 创建节点管理。调用 Load 之后才能用。
func NewNodes(store NodeStore, ca *CA, notifier Notifier, opts NodesOptions) *Nodes {
	return &Nodes{
		store:      sourceIPAuditStore{store},
		ca:         ca,
		notifier:   notifier,
		opts:       opts.withDefaults(),
		now:        time.Now,
		status:     map[int64]NodeStatus{},
		multiIP:    map[int64]bool{},
		revoked:    map[string]struct{}{},
		superseded: map[string]time.Time{},
		moves:      map[int64]ipMove{},
		renewals:   map[string]string{},
		encKeys:    map[int64]*ecdh.PublicKey{},
	}
}

// AttachRegistry 接上主从通信服务的连接登记表（服务创建后调用）。
func (n *Nodes) AttachRegistry(r *transport.ConnRegistry) { n.registry = r }

// Load 从数据库装入节点状态和吊销列表。
func (n *Nodes) Load(ctx context.Context) error {
	nodes, err := n.store.List(ctx)
	if err != nil {
		return err
	}
	revoked, err := n.store.ListRevokedSerials(ctx, n.now())
	if err != nil {
		return err
	}
	renewed, err := n.store.ListRenewedSerials(ctx, n.now())
	if err != nil {
		return err
	}
	status := make(map[int64]NodeStatus, len(nodes))
	multi := make(map[int64]bool, len(nodes))
	encKeys := make(map[int64]*ecdh.PublicKey, len(nodes))
	for _, node := range nodes {
		status[node.ID] = node.Status
		multi[node.ID] = node.AllowMultiIP
		if key, err := sealbox.ParsePublicKey(node.EncryptionPublicKey); err == nil {
			encKeys[node.ID] = key
		}
	}
	rev := make(map[string]struct{}, len(revoked))
	for _, s := range revoked {
		rev[s] = struct{}{}
	}
	n.mu.Lock()
	n.status, n.multiIP, n.revoked, n.encKeys = status, multi, rev, encKeys
	now := n.now()
	for serial, until := range n.superseded {
		if now.After(until.Add(n.opts.CertificateLifetime)) {
			delete(n.superseded, serial)
		}
	}
	// 被续签替换的证书以库为准：主节点重启后内存里的名单没了，从库里补回来。
	for _, serial := range renewed {
		if _, ok := n.superseded[serial]; !ok {
			n.superseded[serial] = now
		}
	}
	n.mu.Unlock()
	return nil
}

// EncryptionKey 返回节点当前的 X25519 加密公钥（选号时加密上游凭据，设计 7.1）。
func (n *Nodes) EncryptionKey(nodeID int64) (*ecdh.PublicKey, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	key, ok := n.encKeys[nodeID]
	return key, ok
}

// setEncryptionKey 记下节点的新加密公钥（领证、续签时），库和内存一起更新。
func (n *Nodes) setEncryptionKey(ctx context.Context, nodeID int64, raw []byte) error {
	key, err := sealbox.ParsePublicKey(raw)
	if err != nil {
		return err
	}
	if err := n.store.SetEncryptionKey(ctx, nodeID, raw); err != nil {
		return err
	}
	n.mu.Lock()
	n.encKeys[nodeID] = key
	n.mu.Unlock()
	return nil
}

// recordRenewal 记下"新证书替换旧证书"。旧证书在新证书第一次被使用时失效；
// 宽限期内新证书一直没被用（比如续签回复丢了、从节点重启），宽限期到了也失效。
// 这样回复丢失后，从节点还能用旧证书连上来重发续签、拿回同一张新证书（设计 7.2 第 1 条）。
func (n *Nodes) recordRenewal(oldSerial, newSerial string) {
	n.mu.Lock()
	n.renewals[newSerial] = oldSerial
	n.mu.Unlock()
	time.AfterFunc(n.opts.RenewGrace, func() {
		n.mu.Lock()
		delete(n.renewals, newSerial)
		n.mu.Unlock()
		n.markSuperseded(oldSerial)
	})
}

// markSuperseded 让被续签替换的旧证书不能再建新连接，已有连接宽限 RenewGrace 后关闭。
func (n *Nodes) markSuperseded(serial string) {
	n.mu.Lock()
	if _, already := n.superseded[serial]; already {
		n.mu.Unlock()
		return
	}
	n.superseded[serial] = n.now().Add(n.opts.RenewGrace)
	n.mu.Unlock()
	if n.registry != nil {
		n.registry.CloseSerialAfter(serial, n.opts.RenewGrace)
	}
}

// RunRefresh 定时对账，直到 ctx 结束。只在主从分流开关打开时运行。
func (n *Nodes) RunRefresh(ctx context.Context, onError func(error)) {
	t := time.NewTicker(n.opts.RefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := n.Load(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

// AuthorizeIssued 实现 transport.Authorizer：证书未吊销、节点已激活（或排空中）。
func (n *Nodes) AuthorizeIssued(_ context.Context, peer transport.PeerIdentity) error {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if _, bad := n.revoked[peer.CertSerial]; bad {
		return status.Error(codes.PermissionDenied, "relay certificate has been revoked")
	}
	if st := n.status[peer.NodeID]; !st.Serving() {
		return status.Errorf(codes.PermissionDenied, "relay node is %s", orUnknown(st))
	}
	return nil
}

// AdmitConn 在每条连接握手后、登记前检查（设计 7.2）：
//   - 被吊销、被续签替换掉的证书不能再建新连接；
//   - 节点不在服务状态时拒绝；
//   - 同一张证书正从另一个 IP 连着（且这台没有允许多出口）：同一身份出现两份，
//     断开这台的所有连接、吊销证书、退回待激活、发严重告警。
func (n *Nodes) AdmitConn(peer transport.PeerIdentity) error {
	if peer.Class != transport.PeerIssued {
		return nil
	}
	n.mu.RLock()
	_, revoked := n.revoked[peer.CertSerial]
	_, superseded := n.superseded[peer.CertSerial]
	st := n.status[peer.NodeID]
	multi := n.multiIP[peer.NodeID]
	n.mu.RUnlock()
	if revoked {
		return errors.New("relay certificate has been revoked")
	}
	if superseded {
		return errors.New("relay certificate was renewed; connect with the new certificate")
	}
	n.mu.Lock()
	oldSerial, firstUse := n.renewals[peer.CertSerial]
	delete(n.renewals, peer.CertSerial)
	n.mu.Unlock()
	if firstUse {
		n.markSuperseded(oldSerial)
	}
	if !st.Serving() {
		return fmt.Errorf("relay node is %s", orUnknown(st))
	}
	if n.registry == nil || multi {
		return nil
	}
	return n.checkAddress(peer)
}

// ipMoveWindow：节点换出口后，这段时间里旧地址又用同一张证书连回来（而新地址还连着），
// 就判定为两台机器在用同一身份。
const ipMoveWindow = 10 * time.Minute

type ipMove struct {
	from, to string
	at       time.Time
}

// checkAddress 处理同一张证书从不同 IP 连接（设计 7.2 第 2、3 条）：
//   - 节点换了出口（云上 NAT 换 IP 等），旧地址上的连接还没等到保活超时，看起来像
//     "两个 IP 同时连着"。这时按换 IP 处理：断开旧地址的连接、告警，不阻断；
//   - 旧地址在新地址还连着的时候又连回来，才是真正的两份身份：退回待激活、吊销、严重告警。
//     被偷的证书在别处使用时，原节点会被挤掉、重连，正好触发这一条。
func (n *Nodes) checkAddress(peer transport.PeerIdentity) error {
	ip := hostOf(peer.RemoteAddr)
	others := map[string]struct{}{}
	for _, c := range n.registry.Connections(peer.NodeID) {
		if c.Peer.CertSerial == peer.CertSerial {
			if other := hostOf(c.Peer.RemoteAddr); other != ip {
				others[other] = struct{}{}
			}
		}
	}
	if len(others) == 0 {
		return nil
	}
	now := n.now()
	n.mu.Lock()
	mv, moved := n.moves[peer.NodeID]
	_, newStillConnected := others[mv.to]
	returning := moved && now.Sub(mv.at) < ipMoveWindow && mv.from == ip && newStillConnected
	if returning || len(others) > 1 {
		n.mu.Unlock()
		ips := []string{ip}
		for o := range others {
			ips = append(ips, o)
		}
		n.duplicateIdentity(context.Background(), peer.NodeID, map[string]any{
			"reason": "same certificate connected from two addresses at once",
			"serial": peer.CertSerial,
			"ips":    ips,
		})
		return errors.New("relay node identity is in use from another address")
	}
	var from string
	for o := range others {
		from = o
	}
	n.moves[peer.NodeID] = ipMove{from: from, to: ip, at: now}
	n.mu.Unlock()

	go n.registry.CloseNodeAddr(peer.NodeID, from)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	detail := map[string]any{"from": from, "to": ip, "serial": peer.CertSerial}
	_ = n.store.Audit(ctx, AuditEntry{NodeID: peer.NodeID, Action: AuditAddressMoved, SourceIP: ip, Detail: detail})
	n.notify(ctx, Event{Kind: EventNodeIPChanged, Severity: SeverityWarning, NodeID: peer.NodeID, Detail: detail})
	return nil
}

// OnConnect 记录签发证书连接的来源 IP；IP 变化只告警（设计 7.2 第 3 条）。
func (n *Nodes) OnConnect(info transport.ConnInfo) {
	if info.Peer.Class != transport.PeerIssued {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ip := hostOf(info.Peer.RemoteAddr)
	changed, err := n.store.TouchSeen(ctx, info.Peer.NodeID, ip, n.now())
	if err == nil && changed {
		n.notify(ctx, Event{Kind: EventNodeIPChanged, Severity: SeverityInfo, NodeID: info.Peer.NodeID, Detail: map[string]any{"ip": ip}})
	}
}

// duplicateIdentity 处理"同一身份出现两份"：退回待激活、吊销证书、断开所有连接、严重告警。
func (n *Nodes) duplicateIdentity(ctx context.Context, nodeID int64, detail map[string]any) {
	_, _ = n.store.SetStatus(ctx, nodeID, []NodeStatus{NodeActive, NodeDraining}, NodePending)
	serials, _ := n.store.RevokeCertificates(ctx, nodeID, revokeReasonIdentityDuplicated, n.now())
	n.mu.Lock()
	n.status[nodeID] = NodePending
	for _, s := range serials {
		n.revoked[s] = struct{}{}
	}
	n.mu.Unlock()
	_ = n.store.Audit(ctx, AuditEntry{NodeID: nodeID, Action: AuditIdentityDuplicate, Detail: detail})
	n.notify(ctx, Event{Kind: EventIdentityDuplicated, Severity: SeverityCritical, NodeID: nodeID, Detail: detail})
	n.closeNodeSoon(nodeID)
}

// closeNodeSoon 稍后断开这台的所有连接。状态和吊销已经在缓存里生效，
// 这段时间里新的调用和新的连接都会被拒；延迟只为让当前请求的回复发得出去。
func (n *Nodes) closeNodeSoon(nodeID int64) {
	if n.registry == nil {
		return
	}
	time.AfterFunc(n.opts.CloseDelay, func() { n.registry.CloseNode(nodeID) })
}

// ---- 管理员操作（设计 11.5）。调用方负责权限和二次验证（stepUpAuth）。----

// Activate 激活待激活节点。管理员必须把从节点本机打印的指纹填进来，和注册时的一致才激活
// （设计 11.2：名称、主机名、IP 都能被模仿，指纹是唯一可靠的依据）。
func (n *Nodes) Activate(ctx context.Context, nodeID int64, confirmFingerprint string, a Activation) error {
	node, err := n.store.GetByID(ctx, nodeID)
	if err != nil {
		return err
	}
	if node.Status != NodePending {
		return ErrStatusConflict
	}
	if transport.NormalizeFingerprint(confirmFingerprint) != node.IdentityFingerprint {
		return ErrFingerprintMismatch
	}
	a.PublicDomain = strings.ToLower(strings.TrimSpace(a.PublicDomain))
	if a.PublicDomain == "" {
		return ErrDomainRequired
	}
	if a.At.IsZero() {
		a.At = n.now()
	}
	if err := n.store.Activate(ctx, nodeID, a); err != nil {
		return err
	}
	n.setStatus(nodeID, NodeActive)
	return n.store.Audit(ctx, AuditEntry{NodeID: nodeID, ActorUserID: a.ActorUserID, Action: AuditActivated, Detail: map[string]any{
		"fingerprint": node.IdentityFingerprint, "public_domain": a.PublicDomain, "name": a.Name,
	}})
}

// Reject 拒绝待激活节点：这把长期密钥被拉黑，同一台机器要换密钥重新注册。
func (n *Nodes) Reject(ctx context.Context, nodeID, actor int64) error {
	node, err := n.store.GetByID(ctx, nodeID)
	if err != nil {
		return err
	}
	ok, err := n.store.SetStatus(ctx, nodeID, []NodeStatus{NodePending}, NodeRejected)
	if err != nil {
		return err
	}
	if !ok {
		return ErrStatusConflict
	}
	n.setStatus(nodeID, NodeRejected)
	if n.registry != nil {
		n.registry.CloseKey(node.IdentityFingerprint)
	}
	return n.store.Audit(ctx, AuditEntry{NodeID: nodeID, ActorUserID: actor, Action: AuditRejected, Detail: map[string]any{"fingerprint": node.IdentityFingerprint}})
}

// RejectAllPending 一键拒绝所有待激活节点，返回拒绝的数量。
func (n *Nodes) RejectAllPending(ctx context.Context, actor int64) (int, error) {
	nodes, err := n.store.List(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, node := range nodes {
		if node.Status != NodePending {
			continue
		}
		if err := n.Reject(ctx, node.ID, actor); err == nil {
			count++
		}
	}
	_ = n.store.Audit(ctx, AuditEntry{ActorUserID: actor, Action: AuditRejectedAll, Detail: map[string]any{"count": count}})
	return count, nil
}

// Disable 停用节点：吊销证书、断开连接。再启用要重新激活（设计 11.2）。
func (n *Nodes) Disable(ctx context.Context, nodeID, actor int64) error {
	ok, err := n.store.SetStatus(ctx, nodeID, []NodeStatus{NodeActive, NodeDraining, NodePending}, NodeDisabled)
	if err != nil {
		return err
	}
	if !ok {
		return ErrStatusConflict
	}
	serials, err := n.store.RevokeCertificates(ctx, nodeID, "disabled", n.now())
	if err != nil {
		return err
	}
	n.afterRevoke(nodeID, NodeDisabled, serials)
	return n.store.Audit(ctx, AuditEntry{NodeID: nodeID, ActorUserID: actor, Action: AuditDisabled, Detail: map[string]any{"revoked": serials}})
}

// Enable 让停用的节点回到待激活，管理员核对指纹后重新激活。
func (n *Nodes) Enable(ctx context.Context, nodeID, actor int64) error {
	ok, err := n.store.SetStatus(ctx, nodeID, []NodeStatus{NodeDisabled}, NodePending)
	if err != nil {
		return err
	}
	if !ok {
		return ErrStatusConflict
	}
	n.setStatus(nodeID, NodePending)
	return n.store.Audit(ctx, AuditEntry{NodeID: nodeID, ActorUserID: actor, Action: AuditEnabled})
}

// RevokeCertificates 因怀疑被攻破而吊销：吊销所有证书、断开连接、退回待激活，
// 长期密钥也不能再自动领证书，必须重新激活（设计 11.5、5.4）。
func (n *Nodes) RevokeCertificates(ctx context.Context, nodeID, actor int64, reason string) error {
	if _, err := n.store.SetStatus(ctx, nodeID, []NodeStatus{NodeActive, NodeDraining}, NodePending); err != nil {
		return err
	}
	serials, err := n.store.RevokeCertificates(ctx, nodeID, revokeReasonAdminPrefix+reason, n.now())
	if err != nil {
		return err
	}
	n.afterRevoke(nodeID, NodePending, serials)
	return n.store.Audit(ctx, AuditEntry{NodeID: nodeID, ActorUserID: actor, Action: AuditCertsRevoked, Detail: map[string]any{"reason": reason, "revoked": serials}})
}

// SetAllowMultiIP 对这台单独关闭或打开"同时两个 IP"的判断（设计 7.2 末段）。
func (n *Nodes) SetAllowMultiIP(ctx context.Context, nodeID, actor int64, allow bool) error {
	if err := n.store.SetAllowMultiIP(ctx, nodeID, allow); err != nil {
		return err
	}
	n.mu.Lock()
	n.multiIP[nodeID] = allow
	n.mu.Unlock()
	return n.store.Audit(ctx, AuditEntry{NodeID: nodeID, ActorUserID: actor, Action: AuditMultiIPChanged, Detail: map[string]any{"allow": allow}})
}

// PurgeStalePending 清除超过 24 小时未激活的节点（设计 11.1）。只在开关打开时定时调用。
func (n *Nodes) PurgeStalePending(ctx context.Context) (int64, error) {
	count, err := n.store.PurgeStalePending(ctx, n.now().Add(-n.opts.PendingTTL))
	if err != nil || count == 0 {
		return count, err
	}
	_ = n.store.Audit(ctx, AuditEntry{Action: AuditPendingPurged, Detail: map[string]any{"count": count}})
	n.notify(ctx, Event{Kind: EventPendingPurged, Severity: SeverityInfo, Detail: map[string]any{"count": count}})
	return count, n.Load(ctx)
}

func (n *Nodes) afterRevoke(nodeID int64, st NodeStatus, serials []string) {
	n.mu.Lock()
	n.status[nodeID] = st
	for _, s := range serials {
		n.revoked[s] = struct{}{}
	}
	n.mu.Unlock()
	n.closeNodeSoon(nodeID)
}

func (n *Nodes) setStatus(nodeID int64, st NodeStatus) {
	n.mu.Lock()
	n.status[nodeID] = st
	n.mu.Unlock()
}

func (n *Nodes) notify(ctx context.Context, e Event) {
	if n.notifier != nil {
		n.notifier.Notify(ctx, e)
	}
}

func orUnknown(st NodeStatus) string {
	if st == "" {
		return "unknown"
	}
	return string(st)
}

func hostOf(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr.String()); err == nil {
		return host
	}
	return addr.String()
}
