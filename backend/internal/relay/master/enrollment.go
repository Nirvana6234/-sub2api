package master

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"golang.org/x/time/rate"
	"google.golang.org/grpc/codes"
	grpcpeer "google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// EnrollmentPolicies 是接入服务各 RPC 允许的对端类型，交给 transport.ServerOptions.Policies。
// 没列出的方法（所有业务 RPC）只允许签发证书。
func EnrollmentPolicies() map[string][]transport.PeerClass {
	anyPeer := []transport.PeerClass{transport.PeerAnonymous, transport.PeerLongTerm, transport.PeerIssued}
	longTerm := []transport.PeerClass{transport.PeerLongTerm}
	return map[string][]transport.PeerClass{
		relayv1.RelayEnrollment_Hello_FullMethodName:             anyPeer,
		relayv1.RelayEnrollment_Register_FullMethodName:          longTerm,
		relayv1.RelayEnrollment_NodeStatus_FullMethodName:        longTerm,
		relayv1.RelayEnrollment_ObtainCertificate_FullMethodName: longTerm,
		relayv1.RelayEnrollment_RenewCertificate_FullMethodName:  {transport.PeerIssued},
	}
}

// 注册信息的大小限制（设计 11.1：限制大小，主节点不对它做耗时处理）。
const (
	maxRegisterField    = 255
	maxSystemInfoFields = 32
	maxSystemInfoValue  = 512
)

// Enrollment 实现 RelayEnrollment 服务。
type Enrollment struct {
	relayv1.UnimplementedRelayEnrollmentServer
	nodes  *Nodes
	server *transport.Server

	registerMu     sync.Mutex
	registerLimits map[string]*rate.Limiter
}

// NewEnrollment 创建接入服务。server 用于生成 Hello 回复。
func NewEnrollment(nodes *Nodes, server *transport.Server) *Enrollment {
	return &Enrollment{nodes: nodes, server: server, registerLimits: map[string]*rate.Limiter{}}
}

func (e *Enrollment) Hello(ctx context.Context, _ *relayv1.HelloRequest) (*relayv1.HelloResponse, error) {
	return e.server.Hello(ctx)
}

// Register 登记新节点。重复注册（同一把长期密钥）是幂等的：待激活时更新登记信息。
func (e *Enrollment) Register(ctx context.Context, req *relayv1.RegisterRequest) (*relayv1.RegisterResponse, error) {
	peer, _ := transport.PeerFromContext(ctx)
	ip := hostOf(peer.RemoteAddr)
	if !e.allowRegister(ip) {
		return nil, status.Error(codes.ResourceExhausted, "too many registrations from this address")
	}
	if err := validateRegistration(req); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	pubDER, err := e.peerPublicKey(ctx)
	if err != nil {
		return nil, err
	}

	existing, err := e.nodes.store.GetByFingerprint(ctx, peer.KeyFingerprint)
	switch {
	case err == nil:
		if existing.Status == NodePending {
			if err := e.nodes.store.UpdateRegistration(ctx, existing.ID, req.Hostname, req.ProgramVersion, req.DisplayName, req.SystemInfo, ip); err != nil {
				return nil, status.Error(codes.Internal, "update registration failed")
			}
		}
		return &relayv1.RegisterResponse{Status: toProtoStatus(existing.Status), KeyFingerprint: peer.KeyFingerprint, RootFingerprints: e.nodes.ca.RootFingerprints()}, nil
	case !errors.Is(err, ErrNodeNotFound):
		return nil, status.Error(codes.Internal, "lookup failed")
	}

	node, err := e.nodes.store.CreatePending(ctx, &Node{
		Name:                req.DisplayName,
		Hostname:            req.Hostname,
		Status:              NodePending,
		IdentityPublicKey:   pubDER,
		IdentityFingerprint: peer.KeyFingerprint,
		RegisteredIP:        ip,
		ProgramVersion:      req.ProgramVersion,
		SystemInfo:          req.SystemInfo,
	}, e.nodes.opts.MaxPending)
	if errors.Is(err, ErrPendingLimit) {
		return nil, status.Error(codes.ResourceExhausted, ErrPendingLimit.Error())
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "registration failed")
	}
	e.nodes.setStatus(node.ID, NodePending)
	_ = e.nodes.store.Audit(ctx, AuditEntry{NodeID: node.ID, Action: AuditRegistered, SourceIP: ip, Detail: map[string]any{
		"fingerprint": peer.KeyFingerprint, "hostname": req.Hostname, "program_version": req.ProgramVersion,
	}})
	e.nodes.notify(ctx, Event{Kind: EventNodeRegistered, Severity: SeverityInfo, NodeID: node.ID, Detail: map[string]any{"hostname": req.Hostname, "ip": ip}})
	return &relayv1.RegisterResponse{Status: relayv1.NodeStatus_NODE_STATUS_PENDING, KeyFingerprint: peer.KeyFingerprint, RootFingerprints: e.nodes.ca.RootFingerprints()}, nil
}

// NodeStatus 返回这把长期密钥对应节点的状态；也是待激活期间的心跳。
func (e *Enrollment) NodeStatus(ctx context.Context, _ *relayv1.NodeStatusRequest) (*relayv1.NodeStatusResponse, error) {
	peer, _ := transport.PeerFromContext(ctx)
	resp := &relayv1.NodeStatusResponse{
		HeartbeatIntervalMs: e.nodes.opts.HeartbeatInterval.Milliseconds(),
		KeyFingerprint:      peer.KeyFingerprint,
		// 待激活、已停用的节点也一直按心跳查询，靠它提前拿到预备中的新根（设计 7.4）。
		RootFingerprints: e.nodes.ca.RootFingerprints(),
	}
	node, err := e.nodes.store.GetByFingerprint(ctx, peer.KeyFingerprint)
	if errors.Is(err, ErrNodeNotFound) {
		resp.Status = relayv1.NodeStatus_NODE_STATUS_UNKNOWN
		return resp, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "lookup failed")
	}
	_, _ = e.nodes.store.TouchSeen(ctx, node.ID, hostOf(peer.RemoteAddr), e.nodes.now())
	resp.Status = toProtoStatus(node.Status)
	return resp, nil
}

// ObtainCertificate 已激活节点用长期密钥领取证书。领过证书的节点再来领，就是
// "证书过期或重启后的恢复"，记审计并通知（设计 7.2 第 4 条）。
func (e *Enrollment) ObtainCertificate(ctx context.Context, req *relayv1.CertificateRequest) (*relayv1.CertificateResponse, error) {
	peer, _ := transport.PeerFromContext(ctx)
	node, err := e.nodes.store.GetByFingerprint(ctx, peer.KeyFingerprint)
	if errors.Is(err, ErrNodeNotFound) {
		return nil, status.Error(codes.NotFound, "this key is not registered")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "lookup failed")
	}
	if !node.Status.Serving() {
		return nil, status.Errorf(codes.FailedPrecondition, "relay node is %s", node.Status)
	}
	// 恢复时同样检查"同一身份出现两份"：这台正从别的地址用签发证书连着。
	if e.nodes.registry != nil && !node.AllowMultiIP {
		ip := hostOf(peer.RemoteAddr)
		for _, c := range e.nodes.registry.Connections(node.ID) {
			if hostOf(c.Peer.RemoteAddr) != ip {
				e.nodes.duplicateIdentity(ctx, node.ID, map[string]any{
					"reason": "long-term key used from another address while the node is connected",
					"ips":    []string{hostOf(c.Peer.RemoteAddr), ip},
				})
				return nil, status.Error(codes.PermissionDenied, "relay node identity is in use from another address")
			}
		}
	}
	prior, err := e.nodes.store.CountCertificates(ctx, node.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "lookup failed")
	}
	resp, err := e.issue(ctx, node, req, "")
	if err != nil {
		return nil, err
	}
	action := AuditCertIssued
	if prior > 0 {
		action = AuditCertRecovered
		e.nodes.notify(ctx, Event{Kind: EventCertificateRecovered, Severity: SeverityWarning, NodeID: node.ID, Detail: map[string]any{"ip": hostOf(peer.RemoteAddr)}})
	}
	_ = e.nodes.store.Audit(ctx, AuditEntry{NodeID: node.ID, Action: action, SourceIP: hostOf(peer.RemoteAddr)})
	return resp, nil
}

// RenewCertificate 用当前证书续签：签发新证书。新证书一被使用，旧证书就不能再建新连接，
// 已有连接最多保留 RenewGrace 后由主节点关闭（设计 7.2 第 1 条，见 recordRenewal）。
//
// 同一张旧证书带着**同一把新公钥**再来，是回复丢失后的重发（从节点把待用的新密钥
// 落了盘，重试和重启都复用它）：原样返回上次签发的证书。带着**另一把公钥**再来，
// 说明旧证书在两处被使用：按"同一身份出现两份"处理。
func (e *Enrollment) RenewCertificate(ctx context.Context, req *relayv1.CertificateRequest) (*relayv1.CertificateResponse, error) {
	peer, _ := transport.PeerFromContext(ctx)
	node, err := e.nodes.store.GetByID(ctx, peer.NodeID)
	if err != nil {
		return nil, status.Error(codes.Internal, "lookup failed")
	}
	resp, err := e.issue(ctx, node, req, peer.CertSerial)
	if errors.Is(err, ErrDuplicateRenewal) {
		prior, lookupErr := e.nodes.store.GetRenewalOf(ctx, peer.CertSerial)
		if lookupErr == nil && prior.RevokedAt == nil && len(prior.DER) > 0 && bytes.Equal(prior.PublicKey, req.GetTlsPublicKey()) {
			if err := e.nodes.store.SetEncryptionKey(ctx, node.ID, req.GetEncryptionPublicKey()); err != nil {
				return nil, status.Error(codes.Internal, "store encryption key failed")
			}
			return &relayv1.CertificateResponse{Certificate: prior.DER, NotAfterUnixMs: prior.NotAfter.UnixMilli(), NodeId: node.ID, RootFingerprints: e.nodes.ca.RootFingerprints()}, nil
		}
		e.nodes.duplicateIdentity(ctx, node.ID, map[string]any{"reason": "the same certificate was renewed twice with different keys", "serial": peer.CertSerial})
		return nil, status.Error(codes.PermissionDenied, "relay certificate was already renewed")
	}
	if err != nil {
		return nil, err
	}
	e.nodes.recordRenewal(peer.CertSerial, serialOf(resp))
	_ = e.nodes.store.Audit(ctx, AuditEntry{NodeID: node.ID, Action: AuditCertRenewed, SourceIP: hostOf(peer.RemoteAddr), Detail: map[string]any{"from": peer.CertSerial}})
	return resp, nil
}

// issue 校验新密钥、签发证书、记下证书和节点的新加密公钥。
func (e *Enrollment) issue(ctx context.Context, node *Node, req *relayv1.CertificateRequest, renewedFrom string) (*relayv1.CertificateResponse, error) {
	pub, err := x509.ParsePKIXPublicKey(req.GetTlsPublicKey())
	if err != nil || !allowedNodeKey(pub) {
		return nil, status.Error(codes.InvalidArgument, "tls_public_key must be an Ed25519 or ECDSA P-256 PKIX key")
	}
	if _, err := sealbox.ParsePublicKey(req.GetEncryptionPublicKey()); err != nil {
		return nil, status.Error(codes.InvalidArgument, "encryption_public_key must be a 32-byte X25519 key")
	}
	cert, err := e.nodes.ca.IssueNodeCertificate(node.ID, pub, e.nodes.opts.CertificateLifetime)
	if err != nil {
		return nil, status.Error(codes.Internal, "issue certificate failed")
	}
	record := &Certificate{
		NodeID:            node.ID,
		Serial:            cert.SerialNumber.Text(16),
		PublicKey:         req.GetTlsPublicKey(),
		DER:               cert.Raw,
		NotBefore:         cert.NotBefore,
		NotAfter:          cert.NotAfter,
		RenewedFromSerial: renewedFrom,
	}
	if err := e.nodes.store.InsertCertificate(ctx, record); err != nil {
		if errors.Is(err, ErrDuplicateRenewal) {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "store certificate failed")
	}
	if err := e.nodes.store.SetEncryptionKey(ctx, node.ID, req.GetEncryptionPublicKey()); err != nil {
		return nil, status.Error(codes.Internal, "store encryption key failed")
	}
	return &relayv1.CertificateResponse{
		Certificate:      cert.Raw,
		NotAfterUnixMs:   cert.NotAfter.UnixMilli(),
		NodeId:           node.ID,
		RootFingerprints: e.nodes.ca.RootFingerprints(),
	}, nil
}

// peerPublicKey 取对端长期密钥证书的公钥（PKIX DER）。
func (e *Enrollment) peerPublicKey(ctx context.Context) ([]byte, error) {
	info, ok := peerTLS(ctx)
	if !ok || len(info) == 0 {
		return nil, status.Error(codes.Unauthenticated, "no long-term certificate")
	}
	der, err := x509.MarshalPKIXPublicKey(info[0].PublicKey)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "unsupported key")
	}
	return der, nil
}

// allowRegister 按来源 IP 限制注册频率（设计 11.1），默认每 10 秒 1 次、突发 3 次。
func (e *Enrollment) allowRegister(ip string) bool {
	e.registerMu.Lock()
	defer e.registerMu.Unlock()
	l := e.registerLimits[ip]
	if l == nil {
		if len(e.registerLimits) > 4096 {
			e.registerLimits = map[string]*rate.Limiter{}
		}
		l = rate.NewLimiter(rate.Every(e.nodes.opts.RegisterInterval), e.nodes.opts.RegisterBurst)
		e.registerLimits[ip] = l
	}
	return l.Allow()
}

func validateRegistration(req *relayv1.RegisterRequest) error {
	for _, f := range []string{req.Hostname, req.ProgramVersion, req.DisplayName} {
		if len(f) > maxRegisterField {
			return errors.New("registration field is too long")
		}
	}
	if len(req.SystemInfo) > maxSystemInfoFields {
		return errors.New("too many system_info entries")
	}
	for k, v := range req.SystemInfo {
		if len(k) > maxRegisterField || len(v) > maxSystemInfoValue || strings.TrimSpace(k) == "" {
			return errors.New("invalid system_info entry")
		}
	}
	return nil
}

func toProtoStatus(s NodeStatus) relayv1.NodeStatus {
	switch s {
	case NodePending:
		return relayv1.NodeStatus_NODE_STATUS_PENDING
	case NodeActive:
		return relayv1.NodeStatus_NODE_STATUS_ACTIVE
	case NodeDraining:
		return relayv1.NodeStatus_NODE_STATUS_DRAINING
	case NodeDisabled:
		return relayv1.NodeStatus_NODE_STATUS_DISABLED
	case NodeRejected:
		return relayv1.NodeStatus_NODE_STATUS_REJECTED
	default:
		return relayv1.NodeStatus_NODE_STATUS_UNKNOWN
	}
}

func peerTLS(ctx context.Context) ([]*x509.Certificate, bool) {
	p, ok := grpcpeer.FromContext(ctx)
	if !ok || p == nil {
		return nil, false
	}
	info, ok := p.AuthInfo.(transport.PeerAuthInfo)
	if !ok {
		return nil, false
	}
	return info.State.PeerCertificates, true
}

func serialOf(resp *relayv1.CertificateResponse) string {
	cert, err := x509.ParseCertificate(resp.GetCertificate())
	if err != nil {
		return ""
	}
	return cert.SerialNumber.Text(16)
}
