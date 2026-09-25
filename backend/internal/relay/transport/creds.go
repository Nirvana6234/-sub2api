package transport

import (
	"context"
	"errors"
	"net"
	"time"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// PeerAuthInfo 是主节点端握手后挂在 gRPC peer 上的认证信息。
type PeerAuthInfo struct {
	credentials.TLSInfo
	Identity PeerIdentity
}

// PeerFromContext 取当前 RPC 对端的身份。只有经过本包服务端的连接才有。
func PeerFromContext(ctx context.Context) (PeerIdentity, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil {
		return PeerIdentity{}, false
	}
	info, ok := p.AuthInfo.(PeerAuthInfo)
	if !ok {
		return PeerIdentity{}, false
	}
	return info.Identity, true
}

// serverCreds 在标准 TLS 凭据外面包一层：握手后给对端分类、登记连接。
// 主节点端口只通过它提供服务，没有明文的退路（设计 7.1）。
type serverCreds struct {
	credentials.TransportCredentials
	opts     ServerTLSOptions
	registry *ConnRegistry
	admit    func(PeerIdentity) error
}

func newServerCreds(opts ServerTLSOptions, registry *ConnRegistry, admit func(PeerIdentity) error) (*serverCreds, error) {
	cfg, err := ServerTLSConfig(opts)
	if err != nil {
		return nil, err
	}
	return &serverCreds{TransportCredentials: credentials.NewTLS(cfg), opts: opts, registry: registry, admit: admit}, nil
}

func (c *serverCreds) ServerHandshake(raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, auth, err := c.TransportCredentials.ServerHandshake(raw)
	if err != nil {
		return nil, nil, err
	}
	tlsInfo, ok := auth.(credentials.TLSInfo)
	if !ok {
		_ = conn.Close()
		return nil, nil, errors.New("relay server handshake produced non-TLS auth info")
	}
	// VerifyConnection 已经让不合格的握手失败；这里再算一次拿到身份（只在握手时发生）。
	identity, err := classifyPeer(tlsInfo.State.PeerCertificates, c.opts.Roots(), c.opts.now())
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	identity.RemoteAddr = raw.RemoteAddr()
	if c.admit != nil {
		if err := c.admit(identity); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
	}
	return c.registry.track(conn, identity), PeerAuthInfo{TLSInfo: tlsInfo, Identity: identity}, nil
}

func (c *serverCreds) Clone() credentials.TransportCredentials {
	return &serverCreds{TransportCredentials: c.TransportCredentials.Clone(), opts: c.opts, registry: c.registry, admit: c.admit}
}

// handshakeTimeout 限制 TLS 握手时间，慢速握手不能长期占着主节点的连接。
const handshakeTimeout = 10 * time.Second
