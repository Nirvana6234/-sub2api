package service

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// 主从分流的 API Key 节点分配（docs/MASTER_RELAY_NODES.md 10.2）：Key 上记分配的节点 ID（0 = 主节点，空 = 还没分配）。
// 业务层只做三件事：新建 Key 时问分配器要一个节点、管理员改分配、改完作废鉴权缓存（节点规则读的是缓存里的快照）。
// 选哪台节点的逻辑在 relay/master（它知道节点状态、比例和负载）。

// ErrRelayKeyAssignmentUnsupported 在存储不支持节点分配时返回（测试替身）。
var ErrRelayKeyAssignmentUnsupported = errors.New("api key relay node assignment is not supported by the repository")

// RelayKeyAssigner 给新 Key 选节点。主从分流开关关闭时没有分配器，Key 保持未分配。
type RelayKeyAssigner interface {
	// AssignNewKeyNode 返回新 Key 的节点 ID；返回 nil 表示不分配（没有可用节点且主节点分配比例为 0 等）。
	AssignNewKeyNode(ctx context.Context) (*int64, error)
}

type relayKeyAssignerHolder struct{ a RelayKeyAssigner }

// RelayKeyFilter 选一批 Key：未分配的，或分配给某个节点（含主节点 0）的。
type RelayKeyFilter struct {
	Unassigned bool
	NodeID     int64
}

// RelayKeyStat 是一个节点上分配的 Key 数：总数和 activeSince 之后用过的数。
type RelayKeyStat struct {
	Total  int64
	Active int64
}

// APIKeyRelayRepository 是 Key 仓储里和节点分配有关的部分（单独的接口，不改 APIKeyRepository，测试替身不受影响）。
type APIKeyRelayRepository interface {
	// AssignRelayNode 把这些 Key 分配给节点；changedAt 非空时同时记"分配改变时间"（重新分配；第一次分配不记）。
	// 返回实际更新的 Key 原文（作废鉴权缓存用）。已软删除的 Key 不动。
	AssignRelayNode(ctx context.Context, ids []int64, nodeID int64, changedAt *time.Time) ([]string, error)
	// ListRelayKeyIDs 按 ID 升序列出符合条件、ID 大于 afterID 的 Key，最多 limit 个。
	ListRelayKeyIDs(ctx context.Context, filter RelayKeyFilter, afterID int64, limit int) ([]int64, error)
	// CountRelayKeys 统计符合条件的 Key 数。
	CountRelayKeys(ctx context.Context, filter RelayKeyFilter) (int64, error)
	// RelayKeyStats 按节点统计分配的 Key 数（不含未分配的）。
	RelayKeyStats(ctx context.Context, activeSince time.Time) (map[int64]RelayKeyStat, error)
}

// SetRelayKeyAssigner 设置（nil 取消）新 Key 的节点分配器（主从分流运行时在开关打开时设置）。
func (s *APIKeyService) SetRelayKeyAssigner(a RelayKeyAssigner) {
	if a == nil {
		s.relayAssigner.Store(nil)
		return
	}
	s.relayAssigner.Store(&relayKeyAssignerHolder{a: a})
}

// assignNewKeyRelayNode 新建 Key 时给它定节点；分配失败不影响建 Key（保持未分配，哪台都接）。
func (s *APIKeyService) assignNewKeyRelayNode(ctx context.Context, key *APIKey) {
	h := s.relayAssigner.Load()
	if h == nil {
		return
	}
	id, err := h.a.AssignNewKeyNode(ctx)
	if err != nil {
		slog.Warn("relay: assigning a node to the new api key failed; leaving it unassigned", "error", err)
		return
	}
	key.RelayNodeID = id
}

func (s *APIKeyService) relayRepo() (APIKeyRelayRepository, error) {
	r, ok := s.apiKeyRepo.(APIKeyRelayRepository)
	if !ok {
		return nil, ErrRelayKeyAssignmentUnsupported
	}
	return r, nil
}

// AssignRelayNode 把这些 Key 分配给节点（管理员重新分配、上线时给存量 Key 分配）。
// markChanged 为 true 时记"分配改变时间"（Key 页面据此提示地址已变更）。每把 Key 的鉴权缓存都作废：
// 节点规则读缓存里的快照，作废也会推给从节点。返回实际更新的数量。
func (s *APIKeyService) AssignRelayNode(ctx context.Context, ids []int64, nodeID int64, markChanged bool) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	repo, err := s.relayRepo()
	if err != nil {
		return 0, err
	}
	var at *time.Time
	if markChanged {
		now := time.Now()
		at = &now
	}
	keys, err := repo.AssignRelayNode(ctx, ids, nodeID, at)
	if err != nil {
		return 0, err
	}
	for _, k := range keys {
		s.InvalidateAuthCacheByKey(ctx, k)
	}
	return len(keys), nil
}

// ListRelayKeyIDs 见 APIKeyRelayRepository.ListRelayKeyIDs。
func (s *APIKeyService) ListRelayKeyIDs(ctx context.Context, filter RelayKeyFilter, afterID int64, limit int) ([]int64, error) {
	repo, err := s.relayRepo()
	if err != nil {
		return nil, err
	}
	return repo.ListRelayKeyIDs(ctx, filter, afterID, limit)
}

// CountRelayKeys 见 APIKeyRelayRepository.CountRelayKeys。
func (s *APIKeyService) CountRelayKeys(ctx context.Context, filter RelayKeyFilter) (int64, error) {
	repo, err := s.relayRepo()
	if err != nil {
		return 0, err
	}
	return repo.CountRelayKeys(ctx, filter)
}

// RelayKeyStats 见 APIKeyRelayRepository.RelayKeyStats。
func (s *APIKeyService) RelayKeyStats(ctx context.Context, activeSince time.Time) (map[int64]RelayKeyStat, error) {
	repo, err := s.relayRepo()
	if err != nil {
		return nil, err
	}
	return repo.RelayKeyStats(ctx, activeSince)
}
