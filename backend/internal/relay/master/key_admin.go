package master

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 管理员对 API Key 节点分配的操作（设计 10.2、10.7、10.8）：单个 / 按节点批量重新分配、上线时给存量 Key 分配、
// 各节点上的 Key 数（改比例、停用节点前提示影响）。改完都作废这些 Key 的鉴权缓存（节点规则读缓存里的快照，作废也推给从节点）。

var (
	// ErrKeyTargetUnavailable：目标节点现在不能接收 Key（没激活、没填域名、排空或停用）。
	ErrKeyTargetUnavailable = errors.New("the target node cannot take api keys right now")
	// ErrMasterRatioZero：主节点分配比例为 0，不能把 Key 分配给主节点（设计 10.8）。
	ErrMasterRatioZero = errors.New("the master node does not take api keys while the master ratio is 0")
	// ErrKeyAssignmentUnavailable：没有运行中的主从分流，或存储不支持节点分配。
	ErrKeyAssignmentUnavailable = errors.New("api key node assignment is not available")
)

// keyBatchSize 是批量操作每批处理的 Key 数。
const keyBatchSize = 500

// KeyAssignmentSummary 是各节点上分配的 Key 数。
type KeyAssignmentSummary struct {
	// Unassigned：还没分配的 Key（开关打开前建的）。
	Unassigned int64 `json:"unassigned"`
	// Master：分配给主节点的。
	Master int64 `json:"master"`
	// Nodes：每台从节点上的数量和近 7 天用过的数量。
	Nodes map[int64]service.RelayKeyStat `json:"nodes"`
}

func (r *Runtime) keyAssigner() (*KeyAssigner, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return nil, err
	}
	if rr.keyAssigner == nil || r.deps.APIKeys == nil {
		return nil, ErrKeyAssignmentUnavailable
	}
	return rr.keyAssigner, nil
}

// KeyAssignmentSummary 返回各节点上分配的 Key 数。
func (r *Runtime) KeyAssignmentSummary(ctx context.Context) (KeyAssignmentSummary, error) {
	if r.deps.APIKeys == nil {
		return KeyAssignmentSummary{}, ErrKeyAssignmentUnavailable
	}
	unassigned, err := r.deps.APIKeys.CountRelayKeys(ctx, service.RelayKeyFilter{Unassigned: true})
	if err != nil {
		return KeyAssignmentSummary{}, err
	}
	stats, err := r.deps.APIKeys.RelayKeyStats(ctx, r.now().Add(-activeKeyWindow))
	if err != nil {
		return KeyAssignmentSummary{}, err
	}
	out := KeyAssignmentSummary{Unassigned: unassigned, Nodes: map[int64]service.RelayKeyStat{}}
	for id, s := range stats {
		if id == 0 {
			out.Master = s.Total
			continue
		}
		out.Nodes[id] = s
	}
	return out, nil
}

// validateKeyTarget 核对目标节点能接收 Key：主节点要求分配比例大于 0；从节点要已激活、填了域名。
func (r *Runtime) validateKeyTarget(ctx context.Context, nodeID int64) error {
	if nodeID == 0 {
		if *r.cachedGeneralConfig(ctx).WithDefaults().MasterRatioPercent <= 0 {
			return ErrMasterRatioZero
		}
		return nil
	}
	n, err := r.deps.Store.GetByID(ctx, nodeID)
	if err != nil {
		return err
	}
	if n.Status != NodeActive || n.PublicDomain == "" {
		return ErrKeyTargetUnavailable
	}
	return nil
}

// MoveKeys 把这些 Key 重新分配给指定节点（0 为主节点）。Key 页面之后显示"地址已变更"。返回实际移动的数量。
func (r *Runtime) MoveKeys(ctx context.Context, actor int64, ids []int64, nodeID int64) (int, error) {
	if _, err := r.keyAssigner(); err != nil {
		return 0, err
	}
	if err := r.validateKeyTarget(ctx, nodeID); err != nil {
		return 0, err
	}
	n, err := r.deps.APIKeys.AssignRelayNode(ctx, ids, nodeID, true)
	if err != nil {
		return 0, err
	}
	r.audit(ctx, actor, AuditKeysReassigned, map[string]any{"key_ids": ids, "to_node": nodeID, "moved": n})
	return n, nil
}

// MoveNodeKeys 把分配给 from 节点（0 为主节点）的全部 Key 重新分配：to 非空时全给它，否则每把 Key 按分配规则
// 重新选一台（不选 from）。没有可选节点的 Key 保持原样。返回移动的数量和没能移走的数量。
func (r *Runtime) MoveNodeKeys(ctx context.Context, actor, from int64, to *int64) (moved, left int, err error) {
	assigner, err := r.keyAssigner()
	if err != nil {
		return 0, 0, err
	}
	if to != nil {
		if *to == from {
			return 0, 0, nil
		}
		if err := r.validateKeyTarget(ctx, *to); err != nil {
			return 0, 0, err
		}
	}
	filter := service.RelayKeyFilter{NodeID: from}
	exclude := map[int64]bool{from: true}
	var after int64
	for {
		ids, err := r.deps.APIKeys.ListRelayKeyIDs(ctx, filter, after, keyBatchSize)
		if err != nil {
			return moved, left, err
		}
		if len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
		groups := map[int64][]int64{}
		for _, id := range ids {
			target := int64(0)
			if to != nil {
				target = *to
			} else {
				picked, ok, err := assigner.Pick(ctx, exclude)
				if err != nil {
					return moved, left, err
				}
				if !ok {
					left++
					continue
				}
				target = picked
				assigner.noteAssigned(picked)
			}
			groups[target] = append(groups[target], id)
		}
		for node, group := range groups {
			n, err := r.deps.APIKeys.AssignRelayNode(ctx, group, node, true)
			if err != nil {
				return moved, left, err
			}
			moved += n
		}
	}
	r.audit(ctx, actor, AuditKeysReassigned, map[string]any{"from_node": from, "to_node": to, "moved": moved, "left": left})
	return moved, left, nil
}

// ReplaceNode 换机器（设计 11.6）：旧节点停用（还在服务就先停，额度作废放回）、域名和名称等转给新节点并激活、
// 分到旧节点的 Key 全部转给新节点——域名不变，用户不用改地址，所以这些 Key 不记"地址已变更"。返回转移的 Key 数。
func (r *Runtime) ReplaceNode(ctx context.Context, actor, fromID, toID int64, fingerprint string) (int, error) {
	if _, err := r.keyAssigner(); err != nil {
		return 0, err
	}
	if err := r.nodeOp(func(n *Nodes) error { return n.CheckReplace(ctx, fromID, toID, fingerprint) }); err != nil {
		return 0, err
	}
	from, err := r.deps.Store.GetByID(ctx, fromID)
	if err != nil {
		return 0, err
	}
	if from.Status.Serving() {
		if err := r.DisableNode(ctx, fromID, actor); err != nil {
			return 0, err
		}
	}
	if err := r.nodeOp(func(n *Nodes) error { return n.Replace(ctx, fromID, toID, fingerprint, actor) }); err != nil {
		return 0, err
	}
	moved := 0
	filter := service.RelayKeyFilter{NodeID: fromID}
	var after int64
	for {
		ids, err := r.deps.APIKeys.ListRelayKeyIDs(ctx, filter, after, keyBatchSize)
		if err != nil {
			return moved, err
		}
		if len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
		n, err := r.deps.APIKeys.AssignRelayNode(ctx, ids, toID, false)
		if err != nil {
			return moved, err
		}
		moved += n
	}
	r.audit(ctx, actor, AuditKeysReassigned, map[string]any{"from_node": fromID, "to_node": toID, "moved": moved, "reason": "node_replaced"})
	return moved, nil
}

// AssignUnassignedKeys 给所有还没分配节点的 Key 分配（设计 10.7 第 1 步：上线时批量分配）。
// 第一次分配不记"分配改变时间"（Key 页面不提示地址变更）。返回分配数和没能分配的数量。
func (r *Runtime) AssignUnassignedKeys(ctx context.Context, actor int64) (assigned, left int, err error) {
	assigner, err := r.keyAssigner()
	if err != nil {
		return 0, 0, err
	}
	filter := service.RelayKeyFilter{Unassigned: true}
	var after int64
	for {
		ids, err := r.deps.APIKeys.ListRelayKeyIDs(ctx, filter, after, keyBatchSize)
		if err != nil {
			return assigned, left, err
		}
		if len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
		groups := map[int64][]int64{}
		for _, id := range ids {
			picked, ok, err := assigner.Pick(ctx, nil)
			if err != nil {
				return assigned, left, err
			}
			if !ok {
				left++
				continue
			}
			assigner.noteAssigned(picked)
			groups[picked] = append(groups[picked], id)
		}
		for node, group := range groups {
			n, err := r.deps.APIKeys.AssignRelayNode(ctx, group, node, false)
			if err != nil {
				return assigned, left, err
			}
			assigned += n
		}
	}
	r.audit(ctx, actor, AuditKeysAssigned, map[string]any{"assigned": assigned, "left": left})
	return assigned, left, nil
}
