package master

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type userStatusStub map[int64]*service.User

func (s userStatusStub) GetByID(_ context.Context, id int64) (*service.User, error) {
	if id == 99 {
		return nil, errors.New("database is down")
	}
	u, ok := s[id]
	if !ok {
		return nil, service.ErrUserNotFound
	}
	return u, nil
}

// fakeSession 登记一条假的事件流，返回它收到的消息。
func fakeSession(h *EventHub, nodeID int64) chan *relayv1.MasterEnvelope {
	sess := &eventSession{out: make(chan *relayv1.MasterEnvelope, 16), done: make(chan struct{})}
	h.mu.Lock()
	if h.sessions[nodeID] == nil {
		h.sessions[nodeID] = map[*eventSession]struct{}{}
	}
	h.sessions[nodeID][sess] = struct{}{}
	h.mu.Unlock()
	return sess.out
}

func revokedUsers(env *relayv1.MasterEnvelope) []int64 {
	var ids []int64
	for _, u := range env.GetTicketRevocations().GetUsers() {
		ids = append(ids, u.UserId)
	}
	return ids
}

// 只有被停用、被删除的用户才吊销；充值之类的普通改动、查询出错都不吊销（以选号复查为准）。
func TestTicketRevokerRevokesOnlyInactiveOrDeletedUsers(t *testing.T) {
	events := NewEventHub()
	out := fakeSession(events, 7)
	now := time.Now()
	users := userStatusStub{
		1: {ID: 1, Status: service.StatusActive},
		2: {ID: 2, Status: service.StatusDisabled},
	}
	rv := newTicketRevoker(users, events, func() time.Time { return now })
	ctx := context.Background()

	for _, id := range []int64{1, 2, 3, 99} {
		rv.check(ctx, id)
	}
	var got []int64
	for len(out) > 0 {
		got = append(got, revokedUsers(<-out)...)
	}
	require.ElementsMatch(t, []int64{2, 3}, got, "disabled and deleted users only")
	require.True(t, rv.list.Revoked(2, now, now))
	require.False(t, rv.list.Revoked(1, now, now))

	// 新连上的节点收到整份。
	late := fakeSession(events, 8)
	rv.sendAll(8)
	require.ElementsMatch(t, []int64{2, 3}, revokedUsers(<-late))
}

// 只排队用户改动；队列满时丢弃而不阻塞改动方。
func TestTicketRevokerQueueNeverBlocks(t *testing.T) {
	rv := newTicketRevoker(userStatusStub{}, NewEventHub(), time.Now)
	rv.OnAccessChange(service.AccessChange{Kind: service.AccessChangeGroup, GroupID: 1})
	rv.OnAccessChange(service.AccessChange{Kind: service.AccessChangeSubscription, UserID: 1, GroupID: 1})
	require.Empty(t, rv.queue)
	done := make(chan struct{})
	go func() {
		for i := 0; i < revocationQueueSize+10; i++ {
			rv.OnAccessChange(service.AccessChange{Kind: service.AccessChangeUser, UserID: int64(i + 1)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing a user change blocked on a full queue")
	}
	require.Len(t, rv.queue, revocationQueueSize)
}

// run 把排队的用户逐个查完。
func TestTicketRevokerRunDrainsTheQueue(t *testing.T) {
	events := NewEventHub()
	out := fakeSession(events, 7)
	rv := newTicketRevoker(userStatusStub{}, events, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rv.run(ctx)
	rv.OnAccessChange(service.AccessChange{Kind: service.AccessChangeUser, UserID: 5})
	select {
	case env := <-out:
		require.Equal(t, []int64{5}, revokedUsers(env))
	case <-time.After(2 * time.Second):
		t.Fatal("the deleted user was never revoked")
	}
}
