package master

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryLeaseStore 是 LeaseStore 的内存实现（测试用），语义与 SQL 实现一致（mastertest 契约测试）。
// WithUser 期间持有全局锁：fn 出错时整个事务的改动都丢弃。
type MemoryLeaseStore struct {
	mu       sync.Mutex
	nextID   int64
	leases   map[int64]*Lease
	reserved map[int64]Micros
	users    map[int64]bool // 已知用户；未登记的用户视为存在（测试方便），Delete 后不存在
}

// NewMemoryLeaseStore 创建空的内存存储。
func NewMemoryLeaseStore() *MemoryLeaseStore {
	return &MemoryLeaseStore{leases: map[int64]*Lease{}, reserved: map[int64]Micros{}, users: map[int64]bool{}}
}

// DeleteUser 模拟用户被删除（测试用）。
func (s *MemoryLeaseStore) DeleteUser(userID int64) {
	s.mu.Lock()
	s.users[userID] = false
	s.mu.Unlock()
}

// ReservedBalance 返回某个用户的冻结额（测试用，相当于读 users.relay_reserved_balance）。
func (s *MemoryLeaseStore) ReservedBalance(userID int64) Micros {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reserved[userID]
}

// All 返回全部租约（含已关闭）的副本（测试用）。
func (s *MemoryLeaseStore) All() []*Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Lease, 0, len(s.leases))
	for _, l := range s.leases {
		c := *l
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *MemoryLeaseStore) WithUser(ctx context.Context, userID int64, fn func(tx LeaseTx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if exists, known := s.users[userID]; known && !exists {
		return ErrLeaseUserNotFound
	}
	// 在副本上改，成功后再换回去，模拟事务回滚。
	tx := &memoryLeaseTx{s: s, userID: userID, leases: map[int64]*Lease{}, reserved: s.reserved[userID], nextID: s.nextID}
	for id, l := range s.leases {
		if l.UserID == userID {
			c := *l
			tx.leases[id] = &c
		}
	}
	if err := fn(tx); err != nil {
		return err
	}
	for id, l := range tx.leases {
		s.leases[id] = l
	}
	s.nextID = tx.nextID
	if tx.reserved == 0 {
		delete(s.reserved, userID)
	} else {
		s.reserved[userID] = tx.reserved
	}
	return nil
}

func (s *MemoryLeaseStore) ListActiveByNode(_ context.Context, nodeID int64) ([]*Lease, error) {
	return s.list(func(l *Lease) bool { return l.Status == LeaseActive && l.NodeID == nodeID }, 0), nil
}

func (s *MemoryLeaseStore) ListActiveByScope(_ context.Context, dimensions []string, scopeID int64, userID int64) ([]*Lease, error) {
	dims := map[string]bool{}
	for _, d := range dimensions {
		dims[d] = true
	}
	return s.list(func(l *Lease) bool {
		return l.Status == LeaseActive && dims[l.Dimension] && l.ScopeID == scopeID && (userID <= 0 || l.UserID == userID)
	}, 0), nil
}

func (s *MemoryLeaseStore) ListExpired(_ context.Context, before time.Time, limit int) ([]*Lease, error) {
	return s.list(func(l *Lease) bool { return l.Status == LeaseActive && l.ExpiresAt.Before(before) }, limit), nil
}

func (s *MemoryLeaseStore) ReservedBalances(context.Context) (map[int64]Micros, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int64]Micros, len(s.reserved))
	for id, v := range s.reserved {
		if v > 0 {
			out[id] = v
		}
	}
	return out, nil
}

func (s *MemoryLeaseStore) list(match func(*Lease) bool, limit int) []*Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Lease
	for _, l := range s.leases {
		if match(l) {
			c := *l
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

type memoryLeaseTx struct {
	s        *MemoryLeaseStore
	userID   int64
	leases   map[int64]*Lease
	reserved Micros
	nextID   int64
}

func (t *memoryLeaseTx) Active(context.Context) ([]*Lease, error) {
	var out []*Lease
	for _, l := range t.leases {
		if l.Status == LeaseActive {
			c := *l
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (t *memoryLeaseTx) active(id int64) (*Lease, error) {
	l, ok := t.leases[id]
	if !ok || l.Status != LeaseActive {
		return nil, ErrLeaseNotFound
	}
	return l, nil
}

func (t *memoryLeaseTx) Grant(_ context.Context, key LeaseKey, amount Micros, expiresAt time.Time, epoch string, now time.Time) (*Lease, error) {
	if amount <= 0 || key.UserID != t.userID {
		return nil, ErrLeaseNotFound
	}
	var cur *Lease
	for _, l := range t.leases {
		if l.Status == LeaseActive && l.LeaseKey == key {
			cur = l
			break
		}
	}
	if cur == nil {
		t.nextID++
		cur = &Lease{ID: t.nextID, LeaseKey: key, Status: LeaseActive, CreatedAt: now}
		t.leases[cur.ID] = cur
	}
	cur.Granted += amount
	cur.ExpiresAt, cur.MasterEpoch, cur.UpdatedAt = expiresAt, epoch, now
	if isBalanceDimension(key.Dimension) {
		t.reserved += amount
	}
	c := *cur
	return &c, nil
}

func (t *memoryLeaseTx) Reduce(_ context.Context, leaseID int64, amount Micros, now time.Time) (*Lease, error) {
	l, err := t.active(leaseID)
	if err != nil {
		return nil, err
	}
	if amount < 0 || amount > l.Granted {
		return nil, ErrLeaseAmount
	}
	l.Granted -= amount
	l.UpdatedAt = now
	if isBalanceDimension(l.Dimension) {
		t.reserved -= amount
	}
	c := *l
	return &c, nil
}

func (t *memoryLeaseTx) ApplyReturned(_ context.Context, leaseID int64, returnedTotal Micros, now time.Time) (Micros, error) {
	l, err := t.active(leaseID)
	if err != nil {
		return 0, err
	}
	delta := returnedTotal - l.ReturnedTotal
	if delta < 0 {
		delta = 0
	}
	if delta > l.Granted {
		delta = l.Granted
	}
	l.Granted -= delta
	if returnedTotal > l.ReturnedTotal {
		l.ReturnedTotal = returnedTotal
	}
	l.UpdatedAt = now
	if isBalanceDimension(l.Dimension) {
		t.reserved -= delta
	}
	return delta, nil
}

func (t *memoryLeaseTx) Close(_ context.Context, leaseID int64, status LeaseStatus, reason string, now time.Time) (Micros, error) {
	l, err := t.active(leaseID)
	if err != nil {
		return 0, err
	}
	returned := l.Granted
	if isBalanceDimension(l.Dimension) {
		t.reserved -= returned
	}
	l.Granted, l.Status, l.CloseReason, l.UpdatedAt = 0, status, reason, now
	closed := now
	l.ClosedAt = &closed
	return returned, nil
}

func (t *memoryLeaseTx) Renew(_ context.Context, leaseID int64, expiresAt time.Time, lastUsedAt *time.Time, now time.Time) (*Lease, error) {
	l, err := t.active(leaseID)
	if err != nil {
		return nil, err
	}
	l.ExpiresAt, l.UpdatedAt = expiresAt, now
	if lastUsedAt != nil && (l.LastUsedAt == nil || lastUsedAt.After(*l.LastUsedAt)) {
		u := *lastUsedAt
		l.LastUsedAt = &u
	}
	c := *l
	return &c, nil
}
