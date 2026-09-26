package master

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// UserStatusReader 读用户当前状态（service.UserRepository 满足）。
type UserStatusReader interface {
	GetByID(ctx context.Context, id int64) (*service.User, error)
}

// revocationQueueSize：待查的用户改动。满了就丢（并记日志）：吊销表只用来提前拒绝，
// 以选号时的复查为准（设计 8.1）。
const revocationQueueSize = 1024

// ticketRevoker 把"用户被停用、被删除"推给从节点的票据吊销表（设计 8.1）。
//
// 用户改动（service.AccessChangeUser）很多与吊销无关（充值、兑换、改并发），所以不直接吊销，
// 而是在后台查一次当前状态：不存在或未启用才吊销。改密码、改邮箱不经过这里——
// 那些靠选号时比对 token_version 拒绝，从节点被拒后自己记进吊销表。
type ticketRevoker struct {
	users  UserStatusReader
	list   *sign.RevocationList
	events *EventHub
	now    func() time.Time
	queue  chan int64
}

func newTicketRevoker(users UserStatusReader, events *EventHub, now func() time.Time) *ticketRevoker {
	return &ticketRevoker{users: users, list: sign.NewRevocationList(), events: events, now: now, queue: make(chan int64, revocationQueueSize)}
}

// OnAccessChange 订阅业务层改动：用户改动排队待查。在改动方的协程里执行，不阻塞。
func (t *ticketRevoker) OnAccessChange(c service.AccessChange) {
	if c.Kind != service.AccessChangeUser || c.UserID <= 0 {
		return
	}
	select {
	case t.queue <- c.UserID:
	default:
		slog.Warn("relay ticket revocation queue is full; relying on the selection re-check", "user_id", c.UserID)
	}
}

// run 逐个查排队的用户，直到 ctx 结束。
func (t *ticketRevoker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-t.queue:
			t.check(ctx, id)
		}
	}
}

func (t *ticketRevoker) check(ctx context.Context, userID int64) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u, err := t.users.GetByID(cctx, userID)
	switch {
	case errors.Is(err, service.ErrUserNotFound):
		t.revoke(userID)
	case err != nil:
		slog.Warn("relay ticket revocation check failed; relying on the selection re-check", "user_id", userID, "error", err)
	case !u.IsActive():
		t.revoke(userID)
	}
}

// revoke 作废这个用户到现在为止签发的票据，并推给所有在线节点。
func (t *ticketRevoker) revoke(userID int64) {
	now := t.now()
	t.list.Revoke(userID, now, now)
	t.events.Broadcast(&relayv1.MasterEnvelope{Body: &relayv1.MasterEnvelope_TicketRevocations{
		TicketRevocations: &relayv1.TicketRevocations{Users: []*relayv1.RevokedTicketUser{{UserId: userID, RevokedBeforeUnixMs: now.UnixMilli()}}},
	}})
}

// sendAll 事件流连上时把当前全部吊销发给这台节点（它可能刚清过表）。
func (t *ticketRevoker) sendAll(nodeID int64) {
	snap := t.list.Snapshot(t.now())
	if len(snap.Users) == 0 {
		return
	}
	t.events.SendTo(nodeID, &relayv1.MasterEnvelope{Body: &relayv1.MasterEnvelope_TicketRevocations{TicketRevocations: snap}})
}

// RevokeTickets 立即作废某个用户到现在为止签发的票据并推给节点（WP7 选号复查发现票据
// 已失效时也可以调用，让其他节点提前拒绝）。
func (r *Runtime) RevokeTickets(userID int64) error {
	rr, err := r.runningRelay()
	if err != nil {
		return err
	}
	if rr.revoker == nil {
		return nil
	}
	rr.revoker.revoke(userID)
	return nil
}
