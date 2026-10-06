package master

import (
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// eventSendBuffer 是每条事件流的发送缓冲。满了说明这台节点收得太慢：断开它的事件流，
// 节点重连后会重新拉配置（设计 7.4），不在主节点无限堆积。
const eventSendBuffer = 256

// EventHub 管理从节点的事件流（RelayEvents.Stream），用于主节点向从节点推送
// 配置变更、缓存作废和指令。每台节点可能有多条流（重连、证书轮换期间），都会收到。
type EventHub struct {
	relayv1.UnimplementedRelayEventsServer

	mu       sync.Mutex
	sessions map[int64]map[*eventSession]struct{}
	// OnNodeEvent 处理从节点发来的事件（心跳等，后续工作包接入）。
	OnNodeEvent func(nodeID int64, env *relayv1.NodeEnvelope)
	// OnLogResult 处理从节点回的日志查询结果（LogQuerier）。
	OnLogResult func(nodeID int64, r *relayv1.LogQueryResult)
	// OnConnect 在一条事件流建立时调用（例如立刻告诉它当前配置版本）。
	OnConnect func(nodeID int64)
}

type eventSession struct {
	out  chan *relayv1.MasterEnvelope
	done chan struct{}
	once sync.Once
}

func (s *eventSession) close() { s.once.Do(func() { close(s.done) }) }

// NewEventHub 创建事件中心。
func NewEventHub() *EventHub {
	return &EventHub{sessions: map[int64]map[*eventSession]struct{}{}}
}

// Stream 实现 RelayEvents.Stream。
func (h *EventHub) Stream(stream relayv1.RelayEvents_StreamServer) error {
	peer, ok := transport.PeerFromContext(stream.Context())
	if !ok || peer.Class != transport.PeerIssued {
		return status.Error(codes.PermissionDenied, "event streams require an issued certificate")
	}
	sess := &eventSession{out: make(chan *relayv1.MasterEnvelope, eventSendBuffer), done: make(chan struct{})}
	h.register(peer.NodeID, sess)
	defer h.unregister(peer.NodeID, sess)
	if h.OnConnect != nil {
		h.OnConnect(peer.NodeID)
	}

	recvErr := make(chan error, 1)
	go func() {
		for {
			env, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			if ping := env.GetPing(); ping != nil {
				h.trySend(sess, &relayv1.MasterEnvelope{Seq: env.Seq, Body: &relayv1.MasterEnvelope_Ping{Ping: ping}})
				continue
			}
			if lr := env.GetLogResult(); lr != nil {
				if h.OnLogResult != nil {
					h.OnLogResult(peer.NodeID, lr)
				}
				continue
			}
			if h.OnNodeEvent != nil {
				h.OnNodeEvent(peer.NodeID, env)
			}
		}
	}()

	for {
		select {
		case env := <-sess.out:
			if err := stream.Send(env); err != nil {
				return err
			}
		case err := <-recvErr:
			return err
		case <-sess.done:
			return status.Error(codes.ResourceExhausted, "event stream fell behind; reconnect and resync")
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// Broadcast 发给所有节点的所有事件流。
func (h *EventHub) Broadcast(env *relayv1.MasterEnvelope) {
	for _, sess := range h.allSessions() {
		h.trySend(sess, env)
	}
}

// SendTo 发给某台节点的所有事件流。
func (h *EventHub) SendTo(nodeID int64, env *relayv1.MasterEnvelope) {
	h.mu.Lock()
	var targets []*eventSession
	for sess := range h.sessions[nodeID] {
		targets = append(targets, sess)
	}
	h.mu.Unlock()
	for _, sess := range targets {
		h.trySend(sess, env)
	}
}

// IsConnected 报告节点现在有没有事件流。
func (h *EventHub) IsConnected(nodeID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions[nodeID]) > 0
}

// ConnectedNodes 返回当前有事件流的节点。
func (h *EventHub) ConnectedNodes() []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]int64, 0, len(h.sessions))
	for id := range h.sessions {
		out = append(out, id)
	}
	return out
}

func (h *EventHub) trySend(sess *eventSession, env *relayv1.MasterEnvelope) {
	select {
	case sess.out <- env:
	default:
		sess.close()
	}
}

func (h *EventHub) allSessions() []*eventSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*eventSession
	for _, set := range h.sessions {
		for sess := range set {
			out = append(out, sess)
		}
	}
	return out
}

func (h *EventHub) register(nodeID int64, sess *eventSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[nodeID] == nil {
		h.sessions[nodeID] = map[*eventSession]struct{}{}
	}
	h.sessions[nodeID][sess] = struct{}{}
}

func (h *EventHub) unregister(nodeID int64, sess *eventSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions[nodeID], sess)
	if len(h.sessions[nodeID]) == 0 {
		delete(h.sessions, nodeID)
	}
}
