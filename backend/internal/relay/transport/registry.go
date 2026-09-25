package transport

import (
	"net"
	"sync"
	"time"
)

// ConnInfo 描述一条已握手的主从连接。
type ConnInfo struct {
	Peer        PeerIdentity
	ConnectedAt time.Time
}

// ConnRegistry 记录主节点上所有已握手的连接，按节点和证书序列号断开（WP3 用）：
//   - 证书续签后，旧证书的连接最多保留 60 秒，由主节点关闭（设计 7.2）；
//   - 吊销证书、停用节点；
//   - 同一身份出现两份时断开这台的所有连接。
//
// gRPC 没有按连接关闭的接口，所以在传输凭据的握手处把连接包一层登记在这里，
// 关闭底层连接后 gRPC 的传输层会自行清理。
type ConnRegistry struct {
	mu        sync.Mutex
	conns     map[*trackedConn]struct{}
	onConnect func(ConnInfo)
	now       func() time.Time
}

// NewConnRegistry 创建连接登记表。onConnect 在每条连接握手成功后调用（可为 nil），
// WP3 用它检测"同一张证书同时从两个 IP 连接"。
func NewConnRegistry(onConnect func(ConnInfo)) *ConnRegistry {
	return &ConnRegistry{conns: make(map[*trackedConn]struct{}), onConnect: onConnect, now: time.Now}
}

type trackedConn struct {
	net.Conn
	info     ConnInfo
	registry *ConnRegistry
	once     sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		c.registry.mu.Lock()
		delete(c.registry.conns, c)
		c.registry.mu.Unlock()
	})
	return c.Conn.Close()
}

func (r *ConnRegistry) track(conn net.Conn, peer PeerIdentity) net.Conn {
	tc := &trackedConn{Conn: conn, info: ConnInfo{Peer: peer, ConnectedAt: r.now()}, registry: r}
	r.mu.Lock()
	r.conns[tc] = struct{}{}
	r.mu.Unlock()
	if r.onConnect != nil {
		r.onConnect(tc.info)
	}
	return tc
}

// Connections 返回某个节点当前的连接（节点 ID 为签发证书里的 ID）。
func (r *ConnRegistry) Connections(nodeID int64) []ConnInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []ConnInfo
	for c := range r.conns {
		if c.info.Peer.Class == PeerIssued && c.info.Peer.NodeID == nodeID {
			out = append(out, c.info)
		}
	}
	return out
}

// CloseNode 断开这个节点用签发证书建立的所有连接，返回断开的条数。
func (r *ConnRegistry) CloseNode(nodeID int64) int {
	return r.closeWhere(func(p PeerIdentity) bool { return p.Class == PeerIssued && p.NodeID == nodeID })
}

// CloseSerial 断开用这张证书建立的所有连接。
func (r *ConnRegistry) CloseSerial(serial string) int {
	return r.closeWhere(func(p PeerIdentity) bool { return p.Class == PeerIssued && p.CertSerial == serial })
}

// CloseSerialAfter 在 delay 之后断开用这张证书建立的连接（续签后给旧连接的宽限期）。
func (r *ConnRegistry) CloseSerialAfter(serial string, delay time.Duration) *time.Timer {
	return time.AfterFunc(delay, func() { r.CloseSerial(serial) })
}

// CloseKey 断开用这把长期密钥建立的连接（节点被拒绝、密钥拉黑时）。
func (r *ConnRegistry) CloseKey(fingerprint string) int {
	return r.closeWhere(func(p PeerIdentity) bool { return p.Class == PeerLongTerm && p.KeyFingerprint == fingerprint })
}

func (r *ConnRegistry) closeWhere(match func(PeerIdentity) bool) int {
	r.mu.Lock()
	var victims []*trackedConn
	for c := range r.conns {
		if match(c.info.Peer) {
			victims = append(victims, c)
		}
	}
	r.mu.Unlock()
	for _, c := range victims {
		_ = c.Close()
	}
	return len(victims)
}
