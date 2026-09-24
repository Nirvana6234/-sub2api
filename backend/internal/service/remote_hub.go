package service

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// RemoteHub keeps the live connection of every desktop assistant that is
// online for phone sync, and routes phone requests to it.
//
// It only relays. Frames from the phone are passed to the assistant as opaque
// JSON and the assistant's answers go back the same way; nothing is parsed
// beyond the envelope and nothing is stored. The assistant — not this server —
// decides whether a request is allowed, so a compromised server can do no more
// than a paired phone could.
//
// State is per process. With more than one instance behind a load balancer, a
// phone request can land on an instance that does not hold the assistant's
// connection; routing across instances is not implemented yet.
type RemoteHub struct {
	mu     sync.Mutex
	agents map[remoteAgentKey]*RemoteAgentSession
	nextID atomic.Uint64

	// RequestTimeout bounds a phone request waiting for the assistant's answer.
	RequestTimeout time.Duration
}

// RemoteAgentConn is the transport to one assistant; the WebSocket handler
// provides it. Send is called from several goroutines and must serialise.
type RemoteAgentConn interface {
	Send(frame []byte) error
	Close() error
}

type remoteAgentKey struct {
	userID   int64
	deviceID string
}

// Frames exchanged with the assistant.
type remoteFrame struct {
	ID           string          `json:"id,omitempty"`
	Kind         string          `json:"kind"`
	PairingID    int64           `json:"pairing_id,omitempty"`
	Subscription string          `json:"subscription,omitempty"`
	Type         string          `json:"type,omitempty"`
	Body         json.RawMessage `json:"body,omitempty"`
}

const (
	remoteSubscriptionBuffer = 64
	defaultRemoteTimeout     = 15 * time.Second
)

var (
	ErrRemoteDeviceOffline = infraerrors.ServiceUnavailable("REMOTE_DEVICE_OFFLINE", "the computer is not connected")
	ErrRemoteDeviceTimeout = infraerrors.GatewayTimeout("REMOTE_DEVICE_TIMEOUT", "the computer did not answer in time")
	ErrRemoteDeviceGone    = infraerrors.ServiceUnavailable("REMOTE_DEVICE_GONE", "the computer disconnected")
)

func NewRemoteHub() *RemoteHub {
	return &RemoteHub{agents: make(map[remoteAgentKey]*RemoteAgentSession), RequestTimeout: defaultRemoteTimeout}
}

// RemoteAgentSession is one assistant connection.
type RemoteAgentSession struct {
	hub     *RemoteHub
	key     remoteAgentKey
	conn    RemoteAgentConn
	closed  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	pending map[string]chan json.RawMessage
	subs    map[string]chan json.RawMessage
	hello   json.RawMessage
}

// Attach registers a connection for (userID, deviceID). An earlier connection
// for the same computer is closed: the newest one wins, as after a reconnect.
func (h *RemoteHub) Attach(userID int64, deviceID string, conn RemoteAgentConn) *RemoteAgentSession {
	session := &RemoteAgentSession{
		hub:     h,
		key:     remoteAgentKey{userID, deviceID},
		conn:    conn,
		closed:  make(chan struct{}),
		pending: make(map[string]chan json.RawMessage),
		subs:    make(map[string]chan json.RawMessage),
	}

	h.mu.Lock()
	previous := h.agents[session.key]
	h.agents[session.key] = session
	h.mu.Unlock()

	if previous != nil {
		previous.Close()
	}
	return session
}

// Online reports whether the computer is connected.
func (h *RemoteHub) Online(userID int64, deviceID string) bool {
	return h.session(userID, deviceID) != nil
}

// Hello is the last status the assistant reported (desktop app running, etc.).
func (h *RemoteHub) Hello(userID int64, deviceID string) json.RawMessage {
	session := h.session(userID, deviceID)
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.hello
}

// Request sends a phone request to the assistant and waits for its answer.
func (h *RemoteHub) Request(ctx context.Context, userID int64, deviceID string, pairingID int64, body json.RawMessage) (json.RawMessage, error) {
	session := h.session(userID, deviceID)
	if session == nil {
		return nil, ErrRemoteDeviceOffline
	}

	id := "r" + strconv.FormatUint(h.nextID.Add(1), 10)
	answer := make(chan json.RawMessage, 1)
	if !session.register(session.pending, id, answer) {
		return nil, ErrRemoteDeviceGone
	}
	defer session.unregisterPending(id)

	if err := session.send(remoteFrame{ID: id, Kind: "cmd", PairingID: pairingID, Body: body}); err != nil {
		return nil, ErrRemoteDeviceGone
	}

	timeout := h.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRemoteTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result, ok := <-answer:
		if !ok {
			return nil, ErrRemoteDeviceGone
		}
		return result, nil
	case <-timer.C:
		return nil, ErrRemoteDeviceTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-session.closed:
		return nil, ErrRemoteDeviceGone
	}
}

// Subscribe asks the assistant to stream events for body until the returned
// stop function is called or the channel closes. The channel closes when the
// assistant disconnects, or when the subscriber falls behind — the phone then
// resumes from its last cursor, which costs nothing because rollouts are
// append-only.
func (h *RemoteHub) Subscribe(userID int64, deviceID string, pairingID int64, body json.RawMessage) (<-chan json.RawMessage, func(), error) {
	session := h.session(userID, deviceID)
	if session == nil {
		return nil, nil, ErrRemoteDeviceOffline
	}

	id := "s" + strconv.FormatUint(h.nextID.Add(1), 10)
	events := make(chan json.RawMessage, remoteSubscriptionBuffer)
	if !session.register(session.subs, id, events) {
		return nil, nil, ErrRemoteDeviceGone
	}
	if err := session.send(remoteFrame{ID: id, Kind: "subscribe", Subscription: id, PairingID: pairingID, Body: body}); err != nil {
		session.unregisterSub(id)
		return nil, nil, ErrRemoteDeviceGone
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			if session.unregisterSub(id) {
				_ = session.send(remoteFrame{Kind: "unsubscribe", Subscription: id})
			}
		})
	}
	return events, stop, nil
}

// Notify sends an unsolicited event, e.g. a pairing request, to the assistant.
// It reports whether the assistant was connected.
func (h *RemoteHub) Notify(userID int64, deviceID string, eventType string, body any) bool {
	session := h.session(userID, deviceID)
	if session == nil {
		return false
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return false
	}
	return session.send(remoteFrame{Kind: "event", Type: eventType, Body: raw}) == nil
}

// Disconnect drops the computer's connection, e.g. after its pairings are all revoked.
func (h *RemoteHub) Disconnect(userID int64, deviceID string) {
	if session := h.session(userID, deviceID); session != nil {
		session.Close()
	}
}

func (h *RemoteHub) session(userID int64, deviceID string) *RemoteAgentSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.agents[remoteAgentKey{userID, deviceID}]
}

// RemoteAgentFrame is a frame from the assistant that the hub does not
// consume itself, for the caller to act on (pairing confirmations).
type RemoteAgentFrame struct {
	Kind      string
	PairingID int64
	Body      json.RawMessage
}

// HandleFrame routes one frame received from the assistant. Answers and
// subscription events are delivered here; anything else is returned.
func (s *RemoteAgentSession) HandleFrame(data []byte) (*RemoteAgentFrame, error) {
	var frame remoteFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return nil, infraerrors.BadRequest("REMOTE_BAD_FRAME", "frame is not JSON")
	}

	switch frame.Kind {
	case "result":
		s.mu.Lock()
		answer := s.pending[frame.ID]
		delete(s.pending, frame.ID)
		s.mu.Unlock()
		if answer != nil {
			answer <- frame.Body
		}
		return nil, nil

	case "event":
		s.mu.Lock()
		events, ok := s.subs[frame.Subscription]
		delivered := true
		if ok {
			select {
			case events <- frame.Body:
			default:
				// The phone is not keeping up. Dropping one event would leave a gap it
				// cannot see; ending the stream makes it resume from its cursor.
				delete(s.subs, frame.Subscription)
				close(events)
				delivered = false
			}
		}
		s.mu.Unlock()
		if !delivered {
			_ = s.send(remoteFrame{Kind: "unsubscribe", Subscription: frame.Subscription})
		}
		return nil, nil

	case "hello":
		s.mu.Lock()
		s.hello = frame.Body
		s.mu.Unlock()
		return nil, nil

	case "ping":
		return nil, s.send(remoteFrame{Kind: "pong"})

	default:
		return &RemoteAgentFrame{Kind: frame.Kind, PairingID: frame.PairingID, Body: frame.Body}, nil
	}
}

// Close ends the session: waiting requests fail and subscriptions end.
func (s *RemoteAgentSession) Close() {
	s.once.Do(func() {
		close(s.closed)
		s.hub.mu.Lock()
		if s.hub.agents[s.key] == s {
			delete(s.hub.agents, s.key)
		}
		s.hub.mu.Unlock()

		s.mu.Lock()
		for id, answer := range s.pending {
			close(answer)
			delete(s.pending, id)
		}
		for id, events := range s.subs {
			close(events)
			delete(s.subs, id)
		}
		s.mu.Unlock()
		_ = s.conn.Close()
	})
}

// Done is closed when the session ends.
func (s *RemoteAgentSession) Done() <-chan struct{} { return s.closed }

func (s *RemoteAgentSession) send(frame remoteFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	select {
	case <-s.closed:
		return ErrRemoteDeviceGone
	default:
	}
	return s.conn.Send(data)
}

func (s *RemoteAgentSession) register(table map[string]chan json.RawMessage, id string, ch chan json.RawMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.closed:
		return false
	default:
	}
	table[id] = ch
	return true
}

func (s *RemoteAgentSession) unregisterPending(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
}

// unregisterSub removes and closes a subscription; it reports whether it was still open.
// Closing happens under the lock that event delivery also holds, so an event can
// never be sent into a closed channel.
func (s *RemoteAgentSession) unregisterSub(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	events, ok := s.subs[id]
	if !ok {
		return false
	}
	delete(s.subs, id)
	close(events)
	return true
}
