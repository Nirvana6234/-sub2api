package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeAgentConn records what the hub sends to an assistant.
type fakeAgentConn struct {
	mu     sync.Mutex
	frames []remoteFrame
	sent   chan remoteFrame
	closed bool
}

func newFakeAgentConn() *fakeAgentConn { return &fakeAgentConn{sent: make(chan remoteFrame, 64)} }

func (c *fakeAgentConn) Send(data []byte) error {
	var frame remoteFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return err
	}
	c.mu.Lock()
	c.frames = append(c.frames, frame)
	c.mu.Unlock()
	c.sent <- frame
	return nil
}

func (c *fakeAgentConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func (c *fakeAgentConn) next(t *testing.T) remoteFrame {
	t.Helper()
	select {
	case frame := <-c.sent:
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("no frame sent to the assistant")
		return remoteFrame{}
	}
}

func frameJSON(t *testing.T, frame remoteFrame) []byte {
	t.Helper()
	data, err := json.Marshal(frame)
	require.NoError(t, err)
	return data
}

func TestRemoteHub_RequestIsAnsweredByTheAssistant(t *testing.T) {
	hub := NewRemoteHub()
	conn := newFakeAgentConn()
	session := hub.Attach(7, "device-0001", conn)

	done := make(chan json.RawMessage, 1)
	go func() {
		result, err := hub.Request(context.Background(), 7, "device-0001", 42, json.RawMessage(`{"type":"sessions.list"}`))
		require.NoError(t, err)
		done <- result
	}()

	cmd := conn.next(t)
	require.Equal(t, "cmd", cmd.Kind)
	require.Equal(t, int64(42), cmd.PairingID, "the assistant must know which pairing asked")
	require.JSONEq(t, `{"type":"sessions.list"}`, string(cmd.Body))

	_, err := session.HandleFrame(frameJSON(t, remoteFrame{Kind: "result", ID: cmd.ID, Body: json.RawMessage(`{"threads":[]}`)}))
	require.NoError(t, err)
	require.JSONEq(t, `{"threads":[]}`, string(<-done))
}

func TestRemoteHub_OfflineComputerIsReportedAtOnce(t *testing.T) {
	_, err := NewRemoteHub().Request(context.Background(), 7, "device-0001", 1, json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrRemoteDeviceOffline)
}

func TestRemoteHub_AnUnansweredRequestTimesOut(t *testing.T) {
	hub := NewRemoteHub()
	hub.RequestTimeout = 50 * time.Millisecond
	hub.Attach(7, "device-0001", newFakeAgentConn())

	_, err := hub.Request(context.Background(), 7, "device-0001", 1, json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrRemoteDeviceTimeout)
}

// A reconnect replaces the connection; requests waiting on the old one fail
// rather than hang until their timeout.
func TestRemoteHub_AReconnectEndsTheOldSession(t *testing.T) {
	hub := NewRemoteHub()
	old := newFakeAgentConn()
	hub.Attach(7, "device-0001", old)

	errs := make(chan error, 1)
	go func() {
		_, err := hub.Request(context.Background(), 7, "device-0001", 1, json.RawMessage(`{}`))
		errs <- err
	}()
	old.next(t)

	hub.Attach(7, "device-0001", newFakeAgentConn())

	require.ErrorIs(t, <-errs, ErrRemoteDeviceGone)
	require.True(t, old.closed)
	require.True(t, hub.Online(7, "device-0001"))
}

// Users are isolated even when their assistants report the same device id.
func TestRemoteHub_ComputersAreKeyedByUserToo(t *testing.T) {
	hub := NewRemoteHub()
	hub.Attach(7, "device-0001", newFakeAgentConn())

	require.False(t, hub.Online(8, "device-0001"))
	_, err := hub.Request(context.Background(), 8, "device-0001", 1, json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrRemoteDeviceOffline)
}

func TestRemoteHub_SubscriptionStreamsUntilStopped(t *testing.T) {
	hub := NewRemoteHub()
	conn := newFakeAgentConn()
	session := hub.Attach(7, "device-0001", conn)

	events, stop, err := hub.Subscribe(7, "device-0001", 3, json.RawMessage(`{"type":"session.subscribe"}`))
	require.NoError(t, err)
	subscribe := conn.next(t)
	require.Equal(t, "subscribe", subscribe.Kind)

	_, err = session.HandleFrame(frameJSON(t, remoteFrame{Kind: "event", Subscription: subscribe.Subscription, Body: json.RawMessage(`{"seq":1}`)}))
	require.NoError(t, err)
	require.JSONEq(t, `{"seq":1}`, string(<-events))

	stop()
	require.Equal(t, "unsubscribe", conn.next(t).Kind)
	_, open := <-events
	require.False(t, open)

	// An event arriving after the stop must not panic on the closed channel.
	_, err = session.HandleFrame(frameJSON(t, remoteFrame{Kind: "event", Subscription: subscribe.Subscription, Body: json.RawMessage(`{}`)}))
	require.NoError(t, err)
}

// A phone that stops reading must not stall the assistant's connection. Its
// stream ends instead, and it resumes from its cursor.
func TestRemoteHub_ASlowSubscriberIsCutOffNotBuffered(t *testing.T) {
	hub := NewRemoteHub()
	conn := newFakeAgentConn()
	session := hub.Attach(7, "device-0001", conn)
	events, _, err := hub.Subscribe(7, "device-0001", 3, json.RawMessage(`{}`))
	require.NoError(t, err)
	id := conn.next(t).Subscription

	for i := 0; i <= remoteSubscriptionBuffer; i++ {
		_, err := session.HandleFrame(frameJSON(t, remoteFrame{Kind: "event", Subscription: id, Body: json.RawMessage(`{}`)}))
		require.NoError(t, err)
	}

	require.Equal(t, "unsubscribe", conn.next(t).Kind)
	received := 0
	for range events {
		received++
	}
	require.Equal(t, remoteSubscriptionBuffer, received)
}

func TestRemoteHub_UnknownFramesAreHandedBack(t *testing.T) {
	hub := NewRemoteHub()
	session := hub.Attach(7, "device-0001", newFakeAgentConn())

	frame, err := session.HandleFrame([]byte(`{"kind":"pair.confirm","pairing_id":9}`))

	require.NoError(t, err)
	require.Equal(t, &RemoteAgentFrame{Kind: "pair.confirm", PairingID: 9}, frame)
}
