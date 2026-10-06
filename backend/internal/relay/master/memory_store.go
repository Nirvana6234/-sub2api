package master

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore 是 NodeStore 的内存实现，测试用。语义与 internal/repository 的
// SQL 实现一致（条件更新、待激活上限、续签唯一性），两者共用同一套契约测试。
type MemoryStore struct {
	mu     sync.Mutex
	nextID int64
	nodes  map[int64]*Node
	certs  map[string]*Certificate
	audits []AuditEntry
}

// NewMemoryStore 创建空的内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nodes: map[int64]*Node{}, certs: map[string]*Certificate{}}
}

func cloneNode(n *Node) *Node {
	c := *n
	if n.SystemInfo != nil {
		c.SystemInfo = make(map[string]string, len(n.SystemInfo))
		for k, v := range n.SystemInfo {
			c.SystemInfo[k] = v
		}
	}
	return &c
}

func (s *MemoryStore) CreatePending(_ context.Context, n *Node, maxPending int) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := 0
	for _, existing := range s.nodes {
		if existing.IdentityFingerprint == n.IdentityFingerprint {
			return cloneNode(existing), nil
		}
		if existing.Status == NodePending {
			pending++
		}
	}
	if pending >= maxPending {
		return nil, ErrPendingLimit
	}
	s.nextID++
	c := cloneNode(n)
	c.ID = s.nextID
	c.Status = NodePending
	c.CreatedAt = time.Now()
	c.UpdatedAt = c.CreatedAt
	s.nodes[c.ID] = c
	return cloneNode(c), nil
}

func (s *MemoryStore) UpdateRegistration(_ context.Context, id int64, hostname, programVersion, displayName string, systemInfo map[string]string, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	n.Hostname, n.ProgramVersion, n.RegisteredIP = hostname, programVersion, ip
	if displayName != "" {
		n.Name = displayName
	}
	n.SystemInfo = systemInfo
	return nil
}

func (s *MemoryStore) GetByID(_ context.Context, id int64) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return nil, ErrNodeNotFound
	}
	return cloneNode(n), nil
}

func (s *MemoryStore) GetByFingerprint(_ context.Context, fp string) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.nodes {
		if n.IdentityFingerprint == fp {
			return cloneNode(n), nil
		}
	}
	return nil, ErrNodeNotFound
}

func (s *MemoryStore) List(context.Context) ([]*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, cloneNode(n))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) SetStatus(_ context.Context, id int64, from []NodeStatus, to NodeStatus) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return false, ErrNodeNotFound
	}
	for _, f := range from {
		if n.Status == f {
			n.Status = to
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) Activate(_ context.Context, id int64, a Activation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	if n.Status != NodePending {
		return ErrStatusConflict
	}
	for _, other := range s.nodes {
		if other.ID != id && strings.EqualFold(other.PublicDomain, a.PublicDomain) && a.PublicDomain != "" {
			return ErrDomainTaken
		}
	}
	n.Status = NodeActive
	if a.Name != "" {
		n.Name = a.Name
	}
	n.PublicDomain, n.BandwidthLimitMbps, n.Region = a.PublicDomain, a.BandwidthLimitMbps, a.Region
	at, actor := a.At, a.ActorUserID
	n.ActivatedAt, n.ActivatedBy = &at, &actor
	return nil
}

func (s *MemoryStore) ReplaceNode(_ context.Context, fromID, toID int64, a Activation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	from, ok := s.nodes[fromID]
	if !ok {
		return ErrNodeNotFound
	}
	to, ok := s.nodes[toID]
	if !ok {
		return ErrNodeNotFound
	}
	if from.Status != NodeDisabled || to.Status != NodePending {
		return ErrStatusConflict
	}
	from.PublicDomain = ""
	to.Status = NodeActive
	if a.Name != "" {
		to.Name = a.Name
	}
	to.PublicDomain, to.BandwidthLimitMbps, to.Region = a.PublicDomain, a.BandwidthLimitMbps, a.Region
	at, actor := a.At, a.ActorUserID
	to.ActivatedAt, to.ActivatedBy = &at, &actor
	return nil
}

func (s *MemoryStore) SetAllowMultiIP(_ context.Context, id int64, allow bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	n.AllowMultiIP = allow
	return nil
}

func (s *MemoryStore) SetEncryptionKey(_ context.Context, id int64, key []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	n.EncryptionPublicKey = append([]byte(nil), key...)
	return nil
}

func (s *MemoryStore) TouchSeen(_ context.Context, id int64, ip string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return false, ErrNodeNotFound
	}
	changed := n.LastSeenIP != "" && n.LastSeenIP != ip
	n.LastSeenIP, n.LastSeenAt = ip, &at
	return changed, nil
}

func (s *MemoryStore) PurgeStalePending(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int64
	for id, n := range s.nodes {
		if n.Status == NodePending && n.ActivatedAt == nil && n.CreatedAt.Before(before) {
			delete(s.nodes, id)
			count++
		}
	}
	return count, nil
}

func (s *MemoryStore) InsertCertificate(_ context.Context, c *Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.RenewedFromSerial != "" {
		for _, existing := range s.certs {
			if existing.RenewedFromSerial == c.RenewedFromSerial {
				return ErrDuplicateRenewal
			}
		}
	}
	cp := *c
	cp.CreatedAt = time.Now()
	s.certs[c.Serial] = &cp
	return nil
}

func (s *MemoryStore) GetCertificate(_ context.Context, serial string) (*Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.certs[serial]
	if !ok {
		return nil, ErrNodeNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *MemoryStore) GetRenewalOf(_ context.Context, serial string) (*Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.certs {
		if c.RenewedFromSerial == serial {
			cp := *c
			return &cp, nil
		}
	}
	return nil, ErrNodeNotFound
}

func (s *MemoryStore) ListRenewedSerials(_ context.Context, notAfter time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.certs {
		if c.RenewedFromSerial == "" {
			continue
		}
		if old, ok := s.certs[c.RenewedFromSerial]; ok && old.NotAfter.After(notAfter) {
			out = append(out, old.Serial)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *MemoryStore) CountCertificates(_ context.Context, nodeID int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, c := range s.certs {
		if c.NodeID == nodeID {
			count++
		}
	}
	return count, nil
}

func (s *MemoryStore) RevokeCertificates(_ context.Context, nodeID int64, reason string, at time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for serial, c := range s.certs {
		if c.NodeID == nodeID && c.RevokedAt == nil && c.NotAfter.After(at) {
			t := at
			c.RevokedAt, c.RevokeReason = &t, reason
			out = append(out, serial)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *MemoryStore) LastSuspectRevocation(_ context.Context, nodeID int64) (time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var last time.Time
	found := false
	for _, c := range s.certs {
		if c.NodeID == nodeID && c.RevokedAt != nil && IsSuspectRevokeReason(c.RevokeReason) && (!found || c.RevokedAt.After(last)) {
			last, found = *c.RevokedAt, true
		}
	}
	return last, found, nil
}

func (s *MemoryStore) ListRevokedSerials(_ context.Context, notAfter time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for serial, c := range s.certs {
		if c.RevokedAt != nil && c.NotAfter.After(notAfter) {
			out = append(out, serial)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *MemoryStore) Audit(_ context.Context, e AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, e)
	return nil
}

// Audits 返回记录过的审计动作（测试用）。
func (s *MemoryStore) Audits() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AuditEntry(nil), s.audits...)
}

// SetCreatedAt 改节点的注册时间（测试待激活超时用）。
func (s *MemoryStore) SetCreatedAt(id int64, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.nodes[id]; ok {
		n.CreatedAt = at
	}
}
