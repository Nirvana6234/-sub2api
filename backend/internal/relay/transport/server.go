package transport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Authorizer 在每个要求签发证书的 RPC 前检查：证书没有被吊销、节点处于"已激活"
// （设计 7.2："主节点对所有从节点接口都要求证书有效、未吊销，且节点处于已激活"）。
// 由 WP3 的节点管理实现。
type Authorizer interface {
	AuthorizeIssued(ctx context.Context, peer PeerIdentity) error
}

// AuthorizerFunc 让普通函数实现 Authorizer。
type AuthorizerFunc func(ctx context.Context, peer PeerIdentity) error

func (f AuthorizerFunc) AuthorizeIssued(ctx context.Context, peer PeerIdentity) error {
	return f(ctx, peer)
}

// ServerOptions 配置主节点的主从通信服务。
type ServerOptions struct {
	TLS ServerTLSOptions
	// Epoch 是本次启动的纪元；为空时随机生成。
	Epoch string
	// Authorizer 必填：没有它，所有要求签发证书的 RPC 一律拒绝（fail closed）。
	Authorizer Authorizer
	// Policies 指定某个方法（完整方法名）允许哪些对端类型。没列出的方法只允许签发证书。
	Policies map[string][]PeerClass
	// Limits 是按对端、按类别的限流。
	Limits map[CallClass]Limit
	// MaxMessageBytes 覆盖各类调用的单条消息上限（见 defaultMaxMessageBytes）。
	MaxMessageBytes map[CallClass]int
	Idempotency     IdempotencyOptions
	// AdmitConn 在每条连接握手成功、登记之前调用；返回错误则断开这条连接。
	// 用于"旧证书续签后不能再建新连接"、吊销、同一身份两份的检测（设计 7.2）。
	// 已经建立的连接不受影响（它们由 Registry 按宽限期关闭）。
	AdmitConn func(PeerIdentity) error
	// OnConnect 在每条连接握手成功后调用。
	OnConnect func(ConnInfo)
}

// 各类调用单条消息的默认上限：审核带最多 1 张图、事件连接要下发配置快照，所以比控制连接大。
var defaultMaxMessageBytes = map[CallClass]int{
	ClassEnrollment: 64 << 10,
	ClassControl:    4 << 20,
	ClassEvents:     32 << 20,
	ClassBilling:    16 << 20,
	ClassModeration: 16 << 20,
	ClassLogs:       4 << 20,
}

// Server 是主节点端的主从通信服务。端口上只有 TLS，没有明文（设计 7.1）。
type Server struct {
	grpc        *grpc.Server
	epoch       string
	registry    *ConnRegistry
	authorizer  Authorizer
	policies    map[string][]PeerClass
	limiter     *limiter
	idempotency *idempotencyStore
	maxBytes    map[CallClass]int
}

// NewServer 创建主从通信服务。业务服务通过 GRPC() 注册。
func NewServer(opts ServerOptions) (*Server, error) {
	epoch := opts.Epoch
	if epoch == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		epoch = hex.EncodeToString(b[:])
	}
	maxBytes := make(map[CallClass]int, len(defaultMaxMessageBytes))
	largest := 0
	for class, n := range defaultMaxMessageBytes {
		if override, ok := opts.MaxMessageBytes[class]; ok && override > 0 {
			n = override
		}
		maxBytes[class] = n
		if n > largest {
			largest = n
		}
	}
	registry := NewConnRegistry(opts.OnConnect)
	creds, err := newServerCreds(opts.TLS, registry, opts.AdmitConn)
	if err != nil {
		return nil, err
	}
	s := &Server{
		epoch:       epoch,
		registry:    registry,
		authorizer:  opts.Authorizer,
		policies:    opts.Policies,
		limiter:     newLimiter(opts.Limits),
		idempotency: newIdempotencyStore(opts.Idempotency),
		maxBytes:    maxBytes,
	}
	s.grpc = grpc.NewServer(
		grpc.Creds(creds),
		grpc.ConnectionTimeout(handshakeTimeout),
		grpc.MaxRecvMsgSize(largest),
		grpc.MaxSendMsgSize(largest),
		grpc.MaxConcurrentStreams(1024),
		// 从节点空闲的事件、日志连接也会发保活 ping；MinTime 必须不大于客户端的保活间隔，
		// 否则会被主节点以 too_many_pings 断开。
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
		grpc.ChainUnaryInterceptor(s.unaryInterceptor),
		grpc.ChainStreamInterceptor(s.streamInterceptor),
	)
	return s, nil
}

// GRPC 返回底层 gRPC 服务，用于注册业务服务。
func (s *Server) GRPC() *grpc.Server { return s.grpc }

// Epoch 返回本次启动的纪元。
func (s *Server) Epoch() string { return s.epoch }

// Registry 返回连接登记表。
func (s *Server) Registry() *ConnRegistry { return s.registry }

// Serve 在 lis 上提供服务，直到 Stop。
func (s *Server) Serve(lis net.Listener) error { return s.grpc.Serve(lis) }

// GracefulStop 等进行中的调用结束后停止。
func (s *Server) GracefulStop() { s.grpc.GracefulStop() }

// Stop 立即停止并断开所有连接。
func (s *Server) Stop() { s.grpc.Stop() }

// Hello 按当前连接生成握手回复，供 RelayEnrollment 的实现直接使用。
func (s *Server) Hello(ctx context.Context) (*relayv1.HelloResponse, error) {
	peer, ok := PeerFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "relay peer is not identified")
	}
	resp := &relayv1.HelloResponse{
		MasterEpoch:      s.epoch,
		MinSupported:     &relayv1.ProtocolVersion{Major: ProtocolMinSupported.Major, Minor: ProtocolMinSupported.Minor},
		Current:          &relayv1.ProtocolVersion{Major: ProtocolCurrent.Major, Minor: ProtocolCurrent.Minor},
		MasterTimeUnixMs: time.Now().UnixMilli(),
	}
	switch peer.Class {
	case PeerAnonymous:
		resp.PeerClass = relayv1.PeerClass_PEER_CLASS_ANONYMOUS
	case PeerLongTerm:
		resp.PeerClass = relayv1.PeerClass_PEER_CLASS_LONG_TERM
	case PeerIssued:
		resp.PeerClass = relayv1.PeerClass_PEER_CLASS_ISSUED
		resp.NodeId = peer.NodeID
	}
	return resp, nil
}

// classOf 按服务名决定调用类别。
func classOf(fullMethod string) CallClass {
	switch {
	case strings.HasPrefix(fullMethod, "/sub2api.relay.v1.RelayEnrollment/"):
		return ClassEnrollment
	case strings.HasPrefix(fullMethod, "/sub2api.relay.v1.RelayControl/"):
		return ClassControl
	case strings.HasPrefix(fullMethod, "/sub2api.relay.v1.RelayEvents/"):
		return ClassEvents
	case strings.HasPrefix(fullMethod, "/sub2api.relay.v1.RelayBilling/"):
		return ClassBilling
	case strings.HasPrefix(fullMethod, "/sub2api.relay.v1.RelayModeration/"):
		return ClassModeration
	case strings.HasPrefix(fullMethod, "/sub2api.relay.v1.RelayLogs/"):
		return ClassLogs
	default:
		return ClassControl
	}
}

// admit 做每个 RPC 共有的检查：协议版本、对端类型、签发证书的授权、限流。
func (s *Server) admit(ctx context.Context, fullMethod string) (PeerIdentity, func(), error) {
	md, _ := metadata.FromIncomingContext(ctx)
	v, ok := parseVersion(firstMD(md, mdProtocolVersion))
	if !ok || v.Less(ProtocolMinSupported) || v.Major != ProtocolCurrent.Major {
		return PeerIdentity{}, nil, reasonError(ctx, codes.FailedPrecondition, reasonProtocolTooOld,
			"relay protocol "+firstMD(md, mdProtocolVersion)+" is not supported; minimum is "+ProtocolMinSupported.String())
	}
	peer, ok := PeerFromContext(ctx)
	if !ok {
		return PeerIdentity{}, nil, status.Error(codes.Unauthenticated, "relay peer is not identified")
	}
	if !s.allowed(fullMethod, peer.Class) {
		return PeerIdentity{}, nil, status.Errorf(codes.PermissionDenied, "relay method %s is not allowed for %s peers", fullMethod, peer.Class)
	}
	if peer.Class == PeerIssued {
		if s.authorizer == nil {
			return PeerIdentity{}, nil, status.Error(codes.PermissionDenied, "relay node authorization is not configured")
		}
		if err := s.authorizer.AuthorizeIssued(ctx, peer); err != nil {
			if _, isStatus := status.FromError(err); isStatus {
				return PeerIdentity{}, nil, err
			}
			return PeerIdentity{}, nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	release, retryAfter, ok := s.limiter.acquire(peer.limiterKey(), classOf(fullMethod))
	if !ok {
		return PeerIdentity{}, nil, reasonError(ctx, codes.ResourceExhausted, reasonOverloaded, "relay master is busy, retry later",
			mdRetryAfterMs, strconv.FormatInt(retryAfter.Milliseconds(), 10))
	}
	return peer, release, nil
}

func (s *Server) allowed(fullMethod string, class PeerClass) bool {
	classes, ok := s.policies[fullMethod]
	if !ok {
		return class == PeerIssued
	}
	for _, c := range classes {
		if c == class {
			return true
		}
	}
	return false
}

func (s *Server) unaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	_ = grpc.SetHeader(ctx, metadata.Pairs(mdEpoch, s.epoch))
	peer, release, err := s.admit(ctx, info.FullMethod)
	if err != nil {
		return nil, err
	}
	defer release()

	if msg, ok := req.(proto.Message); ok {
		if limit := s.maxBytes[classOf(info.FullMethod)]; limit > 0 && proto.Size(msg) > limit {
			return nil, status.Errorf(codes.ResourceExhausted, "relay message of %d bytes exceeds the %d byte limit", proto.Size(msg), limit)
		}
	}

	md, _ := metadata.FromIncomingContext(ctx)
	key := firstMD(md, mdIdempotencyKey)
	if key == "" {
		return handler(ctx, req)
	}
	if peer.Class != PeerIssued {
		return nil, status.Error(codes.PermissionDenied, "idempotent relay calls require an issued certificate")
	}
	if firstMD(md, mdEpoch) != s.epoch {
		return nil, reasonError(ctx, codes.FailedPrecondition, reasonEpochMismatch,
			"relay master restarted; start the call again under the new epoch")
	}
	return s.idempotency.do(ctx, peer.limiterKey(), info.FullMethod+"\x00"+key, func() (any, error) {
		return handler(ctx, req)
	})
}

func (s *Server) streamInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	_ = ss.SetHeader(metadata.Pairs(mdEpoch, s.epoch))
	_, release, err := s.admit(ss.Context(), info.FullMethod)
	if err != nil {
		return err
	}
	defer release()
	return handler(srv, &limitedServerStream{ServerStream: ss, limit: s.maxBytes[classOf(info.FullMethod)]})
}

// limitedServerStream 对流上收到的每条消息检查大小上限。
type limitedServerStream struct {
	grpc.ServerStream
	limit int
}

func (s *limitedServerStream) RecvMsg(m any) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err
	}
	if msg, ok := m.(proto.Message); ok && s.limit > 0 && proto.Size(msg) > s.limit {
		return status.Errorf(codes.ResourceExhausted, "relay message of %d bytes exceeds the %d byte limit", proto.Size(msg), s.limit)
	}
	return nil
}

func setTrailer(ctx context.Context, md metadata.MD) error {
	return grpc.SetTrailer(ctx, md)
}
