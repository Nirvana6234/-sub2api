package transport

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

// Tier 是连接的优先级类别（开发计划 2.3）。不同类别用不同的 TCP 连接，
// 避免 HTTP/2 队头阻塞和流控互相拖累：日志风暴不能拖慢选号。
type Tier string

const (
	// TierControl：选号、上游错误决策、联网搜索、额度、Key 查询、查询接口转交，以及接入握手。
	TierControl Tier = "control"
	// TierEvents：一条长期双向流，释放、账号事件、心跳、主节点指令。
	TierEvents Tier = "events"
	// TierBilling：扣费批次。
	TierBilling Tier = "billing"
	// TierModeration：内容审核输入（文字 + 最多 1 张图），不和选号挤在一起。
	TierModeration Tier = "moderation"
	// TierTasks：异步任务的结果（带图片，单条消息可能很大）。
	TierTasks Tier = "tasks"
	// TierLogs：日志摘要、计数、指标，优先级最低。
	TierLogs Tier = "logs"
)

var allTiers = []Tier{TierControl, TierEvents, TierBilling, TierModeration, TierLogs, TierTasks}

// 各类连接单条消息的默认上限，与服务端 defaultMaxMessageBytes 对应。
var defaultTierMaxBytes = map[Tier]int{
	TierControl:    4 << 20,
	TierEvents:     32 << 20,
	TierBilling:    16 << 20,
	TierModeration: 16 << 20,
	TierLogs:       4 << 20,
	TierTasks:      64 << 20,
}

// 调用没有自带截止时间时用的默认值。流（事件连接）不设。
var defaultTierTimeout = map[Tier]time.Duration{
	TierControl:    5 * time.Second,
	TierBilling:    30 * time.Second,
	TierModeration: 10 * time.Second,
	TierLogs:       10 * time.Second,
	TierTasks:      60 * time.Second,
}

// ClientOptions 配置从节点到主节点的连接。
type ClientOptions struct {
	// Address 是主节点主从通信端口，host:port。
	Address string
	TLS     ClientTLSOptions
	// Network 固定出口的地址族，默认 "tcp4"。多出口会被主节点当成"同一证书从两个 IP 连接"
	// 而断开（设计 7.2），所以所有连接都走同一个地址族。
	Network string
	// LocalIP 可选：固定本机出口地址。
	LocalIP string
	// ControlConns 是控制连接条数（2~4，默认 2），轮流使用。
	ControlConns   int
	ProgramVersion string
	// OnEpochChange 在发现主节点纪元变化时调用（old 为空表示第一次得知纪元）。
	// 从节点据此清缓存、重拉配置、上报租约（设计 7.4）。
	OnEpochChange func(old, new string)
	// RotateGrace 是换证书后旧连接保留多久（默认 60 秒，与主节点关闭旧证书连接的宽限一致）。
	RotateGrace     time.Duration
	MaxMessageBytes map[Tier]int
	DefaultTimeout  map[Tier]time.Duration
	// Backoff 是 gRPC 连接重连的退避参数（默认 0.5~30 秒，带抖动）。
	Backoff backoff.Config
}

// Client 是从节点到主节点的一组连接。
type Client struct {
	opts   ClientOptions
	tlsCfg credentials.TransportCredentials
	dialer func(ctx context.Context, addr string) (net.Conn, error)

	current atomic.Pointer[connSet]
	epoch   atomic.Value // string
	epochMu sync.Mutex

	mu     sync.Mutex
	closed bool
	timers []*time.Timer
}

type connSet struct {
	byTier map[Tier][]*grpc.ClientConn
	next   atomic.Uint64
}

func (s *connSet) pick(t Tier) *grpc.ClientConn {
	conns := s.byTier[t]
	if len(conns) == 1 {
		return conns[0]
	}
	return conns[int(s.next.Add(1)%uint64(len(conns)))]
}

func (s *connSet) close() {
	for _, conns := range s.byTier {
		for _, c := range conns {
			_ = c.Close()
		}
	}
}

// NewClient 创建连接（连接在第一次调用时建立，断开后自动重连）。
func NewClient(opts ClientOptions) (*Client, error) {
	if opts.Address == "" {
		return nil, errors.New("relay client needs the master address")
	}
	if opts.Network == "" {
		opts.Network = "tcp4"
	}
	if opts.ControlConns <= 0 {
		opts.ControlConns = 2
	}
	if opts.ControlConns > 4 {
		opts.ControlConns = 4
	}
	if opts.RotateGrace <= 0 {
		opts.RotateGrace = 60 * time.Second
	}
	if opts.Backoff == (backoff.Config{}) {
		opts.Backoff = backoff.Config{BaseDelay: 500 * time.Millisecond, Multiplier: 1.6, Jitter: 0.2, MaxDelay: 30 * time.Second}
	}
	tlsCfg, err := ClientTLSConfig(opts.TLS)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if opts.LocalIP != "" {
		ip := net.ParseIP(opts.LocalIP)
		if ip == nil {
			return nil, errors.New("relay client local IP is invalid")
		}
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	c := &Client{
		opts:   opts,
		tlsCfg: credentials.NewTLS(tlsCfg),
		dialer: func(ctx context.Context, addr string) (net.Conn, error) {
			return d.DialContext(ctx, opts.Network, addr)
		},
	}
	c.epoch.Store("")
	set, err := c.dialSet()
	if err != nil {
		return nil, err
	}
	c.current.Store(set)
	return c, nil
}

func (c *Client) dialSet() (*connSet, error) {
	set := &connSet{byTier: make(map[Tier][]*grpc.ClientConn, len(allTiers))}
	for _, t := range allTiers {
		n := 1
		if t == TierControl {
			n = c.opts.ControlConns
		}
		maxBytes := defaultTierMaxBytes[t]
		if v, ok := c.opts.MaxMessageBytes[t]; ok && v > 0 {
			maxBytes = v
		}
		for i := 0; i < n; i++ {
			conn, err := grpc.NewClient("passthrough:///"+c.opts.Address,
				grpc.WithTransportCredentials(c.tlsCfg),
				grpc.WithContextDialer(c.dialer),
				grpc.WithConnectParams(grpc.ConnectParams{Backoff: c.opts.Backoff, MinConnectTimeout: 5 * time.Second}),
				grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
				grpc.WithDisableRetry(),
				grpc.WithUserAgent("sub2api-relay/"+c.opts.ProgramVersion),
				grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxBytes), grpc.MaxCallSendMsgSize(maxBytes)),
			)
			if err != nil {
				set.close()
				return nil, err
			}
			conn.Connect()
			set.byTier[t] = append(set.byTier[t], conn)
		}
	}
	return set, nil
}

// Conn 返回某类连接。每次调用按当前连接组选连接，所以 Rotate 之后自动用上新连接。
func (c *Client) Conn(t Tier) grpc.ClientConnInterface { return &tierConn{client: c, tier: t} }

// Epoch 返回最近一次得知的主节点纪元（还没和主节点通信过时为空）。
func (c *Client) Epoch() string {
	e, _ := c.epoch.Load().(string)
	return e
}

// Rotate 用当前证书重建所有连接（证书续签后调用，设计 7.2）。新调用立即走新连接，
// 旧连接保留 RotateGrace 让进行中的调用结束，之后关闭。
func (c *Client) Rotate() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("relay client is closed")
	}
	set, err := c.dialSet()
	if err != nil {
		return err
	}
	old := c.current.Swap(set)
	c.timers = append(c.timers, time.AfterFunc(c.opts.RotateGrace, old.close))
	return nil
}

// Close 关闭所有连接。
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	for _, t := range c.timers {
		t.Stop()
	}
	c.current.Load().close()
	return nil
}

func (c *Client) observeEpoch(md metadata.MD) {
	e := firstMD(md, mdEpoch)
	if e == "" {
		return
	}
	c.epochMu.Lock()
	old, _ := c.epoch.Load().(string)
	if old == e {
		c.epochMu.Unlock()
		return
	}
	c.epoch.Store(e)
	c.epochMu.Unlock()
	if c.opts.OnEpochChange != nil {
		c.opts.OnEpochChange(old, e)
	}
}

func (c *Client) outgoing(ctx context.Context) context.Context {
	pairs := []string{mdProtocolVersion, ProtocolCurrent.String()}
	if key := idempotencyKeyFrom(ctx); key != "" {
		pairs = append(pairs, mdIdempotencyKey, key, mdEpoch, c.Epoch())
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}

// tierConn 在调用上附加协议版本、幂等键和纪元，读回纪元，并把主节点的错误转成类型化错误。
type tierConn struct {
	client *Client
	tier   Tier
}

func (t *tierConn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	if _, has := ctx.Deadline(); !has {
		timeout := defaultTierTimeout[t.tier]
		if v, ok := t.client.opts.DefaultTimeout[t.tier]; ok && v > 0 {
			timeout = v
		}
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}
	var header, trailer metadata.MD
	opts = append(opts, grpc.Header(&header), grpc.Trailer(&trailer))
	err := t.client.current.Load().pick(t.tier).Invoke(t.client.outgoing(ctx), method, args, reply, opts...)
	t.client.observeEpoch(header)
	t.client.observeEpoch(trailer)
	return translateError(err, trailer)
}

func (t *tierConn) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	cs, err := t.client.current.Load().pick(t.tier).NewStream(t.client.outgoing(ctx), desc, method, opts...)
	if err != nil {
		return nil, translateError(err, nil)
	}
	return &observedClientStream{ClientStream: cs, client: t.client}, nil
}

type observedClientStream struct {
	grpc.ClientStream
	client   *Client
	observed atomic.Bool
}

func (s *observedClientStream) observeHeader() {
	if s.observed.CompareAndSwap(false, true) {
		if md, err := s.Header(); err == nil {
			s.client.observeEpoch(md)
		}
	}
}

func (s *observedClientStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	s.observeHeader()
	if err != nil {
		trailer := s.Trailer()
		s.client.observeEpoch(trailer)
		return translateError(err, trailer)
	}
	return nil
}

func (s *observedClientStream) SendMsg(m any) error {
	err := s.ClientStream.SendMsg(m)
	if err != nil {
		return translateError(err, nil)
	}
	return nil
}
