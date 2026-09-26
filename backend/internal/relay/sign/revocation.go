package sign

import (
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
)

// RevocationList 是票据吊销表：某个用户在某个时间点及之前签发的票据全部作废（设计 8.1）。
//
// 条目保留到"吊销时间 + 票据有效期 + 时钟偏差"：那之后被它挡住的票据都已过期。
// 只在内存：从节点用它提前拒绝，以主节点选号时的复查（用户状态、token_version）为准，
// 所以主节点重启、从节点清表都不会让被吊销的用户继续用上游。
// 主节点也用同一个类型记下最近的吊销，事件流连上时整份发给节点。
type RevocationList struct {
	mu    sync.Mutex
	users map[int64]time.Time
}

// NewRevocationList 创建空表。
func NewRevocationList() *RevocationList {
	return &RevocationList{users: map[int64]time.Time{}}
}

// retention 是条目的保留时长。
const revocationRetention = TicketLifetime + ClockSkew

// Revoke 作废 userID 在 before 及之前签发的票据。同一用户取更晚的时间。
func (r *RevocationList) Revoke(userID int64, before, now time.Time) {
	if r == nil || userID <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.users[userID]; !ok || before.After(cur) {
		r.users[userID] = before
	}
	r.purgeLocked(now)
}

// Revoked 报告 userID 在 issuedAt 签发的票据是否已被吊销。恰好在吊销时刻签发的也算吊销
// （毫秒精度下分不清先后，宁可让客户端换一张新票据）。
func (r *RevocationList) Revoked(userID int64, issuedAt, now time.Time) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	before, ok := r.users[userID]
	if !ok {
		return false
	}
	if now.Sub(before) > revocationRetention {
		delete(r.users, userID)
		return false
	}
	return !issuedAt.After(before)
}

// Apply 合并主节点推来的吊销。
func (r *RevocationList) Apply(msg *relayv1.TicketRevocations, now time.Time) {
	if msg == nil {
		return
	}
	for _, u := range msg.Users {
		if u != nil {
			r.Revoke(u.UserId, time.UnixMilli(u.RevokedBeforeUnixMs), now)
		}
	}
}

// Snapshot 返回当前全部有效条目（主节点在事件流连上时整份发给节点）。
func (r *RevocationList) Snapshot(now time.Time) *relayv1.TicketRevocations {
	msg := &relayv1.TicketRevocations{}
	if r == nil {
		return msg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.purgeLocked(now)
	for id, before := range r.users {
		msg.Users = append(msg.Users, &relayv1.RevokedTicketUser{UserId: id, RevokedBeforeUnixMs: before.UnixMilli()})
	}
	return msg
}

// Reset 清空（从节点发现主节点纪元变化时，设计 7.4）。
func (r *RevocationList) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.users = map[int64]time.Time{}
	r.mu.Unlock()
}

// Len 返回条目数（诊断用）。
func (r *RevocationList) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.users)
}

func (r *RevocationList) purgeLocked(now time.Time) {
	for id, before := range r.users {
		if now.Sub(before) > revocationRetention {
			delete(r.users, id)
		}
	}
}
