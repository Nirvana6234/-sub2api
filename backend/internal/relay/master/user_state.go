package master

import (
	"context"
	"errors"
	"sort"
	"time"
)

// UserAssignmentView 是管理页上一个用户当前的分配。
type UserAssignmentView struct {
	NodeID      int64      `json:"node_id"` // 0 = 主节点
	Reason      string     `json:"reason,omitempty"`
	PinnedUntil *time.Time `json:"pinned_until,omitempty"`
	AssignedAt  time.Time  `json:"assigned_at"`
}

// UserLeaseView 是一个用户在某台节点上锁着的一份额度。
type UserLeaseView struct {
	NodeID    int64     `json:"node_id"`
	NodeName  string    `json:"node_name,omitempty"`
	Dimension string    `json:"dimension"`
	Granted   string    `json:"granted"` // 微单位换回的金额，按十进制字符串
	ExpiresAt time.Time `json:"expires_at"`
	// NodeOnline 为 false 时这份额度回不来，要等到期或管理员立即回收（设计 4.4）。
	NodeOnline bool `json:"node_online"`
}

// UserRelayState 是管理页上一个用户的主从状态：分配到哪里、各节点上锁着多少额度。
type UserRelayState struct {
	UserID     int64               `json:"user_id"`
	Assignment *UserAssignmentView `json:"assignment,omitempty"`
	Leases     []UserLeaseView     `json:"leases"`
}

// UserRelayState 返回一个用户的分配和各节点上锁着的额度（管理员处理"这个用户的钱锁在掉线节点上"时用）。
func (r *Runtime) UserRelayState(ctx context.Context, userID int64) (*UserRelayState, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return nil, err
	}
	out := &UserRelayState{UserID: userID, Leases: []UserLeaseView{}}
	if rr.users != nil {
		a, err := rr.users.store.Get(ctx, userID)
		switch {
		case err == nil && a != nil:
			view := &UserAssignmentView{NodeID: a.NodeID, Reason: a.Reason, AssignedAt: a.AssignedAt}
			if a.Pinned(r.now()) {
				view.PinnedUntil = a.PinnedUntil
			}
			out.Assignment = view
		case err != nil && !errors.Is(err, ErrAssignmentNotFound):
			return nil, err
		}
	}
	if rr.quotas == nil {
		return out, nil
	}
	leases, err := rr.quotas.activeForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	online := map[int64]bool{}
	for _, id := range rr.events.ConnectedNodes() {
		online[id] = true
	}
	names := map[int64]string{}
	if nodes, err := r.deps.Store.List(ctx); err == nil {
		for _, n := range nodes {
			names[n.ID] = n.Name
		}
	}
	for _, l := range leases {
		out.Leases = append(out.Leases, UserLeaseView{
			NodeID: l.NodeID, NodeName: names[l.NodeID], Dimension: l.Dimension, Granted: formatMicros(l.Granted),
			ExpiresAt: l.ExpiresAt, NodeOnline: online[l.NodeID],
		})
	}
	sort.SliceStable(out.Leases, func(i, j int) bool {
		if out.Leases[i].NodeID != out.Leases[j].NodeID {
			return out.Leases[i].NodeID < out.Leases[j].NodeID
		}
		return out.Leases[i].Dimension < out.Leases[j].Dimension
	})
	return out, nil
}
