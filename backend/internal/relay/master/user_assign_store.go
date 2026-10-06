package master

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// 小白端用户分到哪台节点（relay_user_assignments，设计 10.1、10.4）。没有行 = 还没分配。

// 分配原因（relay_user_assignments.reason）。
const (
	AssignReasonNew       = "new"       // 第一次分配
	AssignReasonFailover  = "failover"  // 原节点不可用或客户端报告连不上
	AssignReasonFallback  = "fallback"  // 没有可用的从节点，临时分给主节点（从节点恢复后下次询问分回去）
	AssignReasonManual    = "manual"    // 管理员移动
	AssignReasonPinned    = "pinned"    // 管理员固定
	AssignReasonRebalance = "rebalance" // 管理员重新平衡之后重新分配
)

// ErrAssignmentNotFound：这个用户还没有分配。
var ErrAssignmentNotFound = errors.New("relay user assignment not found")

// UserAssignment 是一行分配。
type UserAssignment struct {
	UserID      int64
	NodeID      int64 // 0 = 主节点
	Reason      string
	PinnedUntil *time.Time
	AssignedAt  time.Time
	UpdatedAt   time.Time
}

// Pinned 报告固定是否还有效。
func (a *UserAssignment) Pinned(now time.Time) bool {
	return a != nil && a.PinnedUntil != nil && a.PinnedUntil.After(now)
}

// UserAssignmentStore 是分配的持久化。
type UserAssignmentStore interface {
	Get(ctx context.Context, userID int64) (*UserAssignment, error)
	// Put 写入（新建或覆盖）；AssignedAt 在节点变了时更新。
	Put(ctx context.Context, a *UserAssignment) error
	Delete(ctx context.Context, userID int64) error
	// CountByNode 返回每个节点（含 0）上的用户数。
	CountByNode(ctx context.Context) (map[int64]int64, error)
	// ListByNode 按用户 ID 升序列出分到这个节点的用户（ID 大于 afterUserID，最多 limit 个）。
	ListByNode(ctx context.Context, nodeID, afterUserID int64, limit int) ([]int64, error)
	// DeleteUnpinned 删掉没有固定的分配（重新平衡：下次询问时重新分配）；返回删除的行数。
	DeleteUnpinned(ctx context.Context, now time.Time) (int64, error)
}

// MemoryUserAssignments 是内存实现（测试）。
type MemoryUserAssignments struct {
	mu   sync.Mutex
	rows map[int64]UserAssignment
}

// NewMemoryUserAssignments 创建内存存储。
func NewMemoryUserAssignments() *MemoryUserAssignments {
	return &MemoryUserAssignments{rows: map[int64]UserAssignment{}}
}

func (m *MemoryUserAssignments) Get(_ context.Context, userID int64) (*UserAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.rows[userID]; ok {
		cp := a
		return &cp, nil
	}
	return nil, ErrAssignmentNotFound
}

func (m *MemoryUserAssignments) Put(_ context.Context, a *UserAssignment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	row := *a
	if old, ok := m.rows[a.UserID]; ok && old.NodeID == a.NodeID {
		row.AssignedAt = old.AssignedAt
	} else {
		row.AssignedAt = now
	}
	row.UpdatedAt = now
	m.rows[a.UserID] = row
	return nil
}

func (m *MemoryUserAssignments) Delete(_ context.Context, userID int64) error {
	m.mu.Lock()
	delete(m.rows, userID)
	m.mu.Unlock()
	return nil
}

func (m *MemoryUserAssignments) CountByNode(context.Context) (map[int64]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[int64]int64{}
	for _, a := range m.rows {
		out[a.NodeID]++
	}
	return out, nil
}

func (m *MemoryUserAssignments) ListByNode(_ context.Context, nodeID, afterUserID int64, limit int) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []int64
	for id, a := range m.rows {
		if a.NodeID == nodeID && id > afterUserID {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

func (m *MemoryUserAssignments) DeleteUnpinned(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, a := range m.rows {
		if !a.Pinned(now) {
			delete(m.rows, id)
			n++
		}
	}
	return n, nil
}
