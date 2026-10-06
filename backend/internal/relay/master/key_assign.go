package master

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 新 Key 的节点分配（设计 10.2、10.8）。
//
// 先按"主节点分配比例"掷骰子决定给不给主节点；分给从节点时，只在"已激活、没排空、没停用、在线、负载没超阈值"的节点里
// 按权重随机选。权重 = 带宽上限 ÷ (1 + 这台上近 7 天用过的 Key 数)：带宽大的多分，已经挂着很多活跃 Key 的少分，
// 不是每次挑最空的（避免同一时刻建的一批 Key 全挤到同一台）。带宽没填的按 DefaultNodeBandwidthMbps 算。
//
// 负载（带宽实测）由心跳上报，那部分属于 WP13/WP14；这里通过 Load 取，没接入时按 0。

const (
	// DefaultNodeBandwidthMbps 是没填带宽上限的节点用的权重基数。
	DefaultNodeBandwidthMbps = 100
	// keyStatsTTL：每台节点上 Key 数统计的缓存时间（建 Key 不是热路径，短缓存够用）。
	keyStatsTTL = 30 * time.Second
	// activeKeyWindow：统计"近多久用过"的窗口。
	activeKeyWindow = 7 * 24 * time.Hour
)

// KeyStatsFunc 返回每台节点上分配的 Key 数。
type KeyStatsFunc func(ctx context.Context, activeSince time.Time) (map[int64]service.RelayKeyStat, error)

// KeyAssignerDeps 是分配器的输入。
type KeyAssignerDeps struct {
	// Nodes 列出全部节点（NodeStore.List）。
	Nodes func(ctx context.Context) ([]*Node, error)
	// Online 返回当前连着事件流的节点 ID；nil 时都按在线。
	Online func() []int64
	// Config 返回通用配置（主节点分配比例、负载阈值）。
	Config func(ctx context.Context) GeneralConfig
	// Stats 返回每台节点上的 Key 数；nil 时按没有。
	Stats KeyStatsFunc
	// Load 返回节点的负载（0~1，超过 1 也行）；节点 0 是主节点（带宽上限为 0）；nil 时都是 0。
	Load func(nodeID int64, bandwidthMbps int) float64
	// Rand 返回 [0,1) 的随机数；nil 用 math/rand。
	Rand func() float64
	Now  func() time.Time
}

// KeyAssigner 给新 Key 选节点（实现 service.RelayKeyAssigner）。
type KeyAssigner struct {
	deps KeyAssignerDeps

	mu      sync.Mutex
	stats   map[int64]service.RelayKeyStat
	statsAt time.Time
}

// NewKeyAssigner 创建分配器。
func NewKeyAssigner(deps KeyAssignerDeps) *KeyAssigner {
	if deps.Rand == nil {
		deps.Rand = rand.Float64
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &KeyAssigner{deps: deps}
}

var _ service.RelayKeyAssigner = (*KeyAssigner)(nil)

// AssignNewKeyNode 实现 service.RelayKeyAssigner：没有可分配的节点且主节点比例为 0 时返回 nil（保持未分配）。
func (a *KeyAssigner) AssignNewKeyNode(ctx context.Context) (*int64, error) {
	id, ok, err := a.Pick(ctx, nil)
	if err != nil || !ok {
		return nil, err
	}
	a.noteAssigned(id)
	return &id, nil
}

// eligible 报告一台节点现在能不能被分配新 Key。
func (a *KeyAssigner) eligible(n *Node, online map[int64]bool) bool {
	if n.Status != NodeActive || n.PublicDomain == "" {
		return false
	}
	return online == nil || online[n.ID]
}

// Pick 选一个节点：返回（节点 ID，0 为主节点；是否选到了）。exclude 里的节点不选（重新分配时排除原节点）。
func (a *KeyAssigner) Pick(ctx context.Context, exclude map[int64]bool) (int64, bool, error) {
	cfg := a.deps.Config(ctx).WithDefaults()
	ratio := *cfg.MasterRatioPercent
	nodes, err := a.deps.Nodes(ctx)
	if err != nil {
		return 0, false, err
	}
	var online map[int64]bool
	if a.deps.Online != nil {
		online = map[int64]bool{}
		for _, id := range a.deps.Online() {
			online[id] = true
		}
	}
	threshold := float64(cfg.LoadThresholdPercent) / 100
	bandwidth := map[int64]int{}
	for _, n := range nodes {
		bandwidth[n.ID] = n.BandwidthLimitMbps
	}
	load := func(id int64) float64 {
		if a.deps.Load == nil {
			return 0
		}
		return a.deps.Load(id, bandwidth[id])
	}
	masterOK := ratio > 0 && !exclude[0]
	masterUnderLoad := masterOK && load(0) <= threshold

	var pool, overloaded []*Node
	for _, n := range nodes {
		if exclude[n.ID] || !a.eligible(n, online) {
			continue
		}
		if load(n.ID) > threshold {
			overloaded = append(overloaded, n)
			continue
		}
		pool = append(pool, n)
	}

	// 按比例先决定给不给主节点（主节点也受负载阈值约束，超了这次改给从节点）。
	if masterUnderLoad && a.deps.Rand()*100 < float64(ratio) {
		return 0, true, nil
	}
	if len(pool) > 0 {
		stats := a.keyStats(ctx)
		return a.weighted(pool, stats).ID, true, nil
	}
	// 没有负载合格的从节点：比例大于 0 时给主节点（10.5：从节点全部不可用就分给主节点）；否则给负载最低的从节点。
	if masterOK {
		return 0, true, nil
	}
	if len(overloaded) > 0 {
		best := overloaded[0]
		for _, n := range overloaded[1:] {
			if load(n.ID) < load(best.ID) {
				best = n
			}
		}
		return best.ID, true, nil
	}
	return 0, false, nil
}

// weighted 按权重随机选一台。
func (a *KeyAssigner) weighted(pool []*Node, stats map[int64]service.RelayKeyStat) *Node {
	weights := make([]float64, len(pool))
	var sum float64
	for i, n := range pool {
		bw := float64(n.BandwidthLimitMbps)
		if bw <= 0 {
			bw = DefaultNodeBandwidthMbps
		}
		w := bw / float64(1+stats[n.ID].Active)
		weights[i] = w
		sum += w
	}
	r := a.deps.Rand() * sum
	for i, w := range weights {
		if r < w {
			return pool[i]
		}
		r -= w
	}
	return pool[len(pool)-1]
}

// keyStats 返回每台节点上的 Key 数（短缓存；读不到时沿用上一次的，没有就当没有）。
func (a *KeyAssigner) keyStats(ctx context.Context) map[int64]service.RelayKeyStat {
	if a.deps.Stats == nil {
		return nil
	}
	now := a.deps.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.statsAt.IsZero() && now.Sub(a.statsAt) < keyStatsTTL {
		return a.stats
	}
	stats, err := a.deps.Stats(ctx, now.Add(-activeKeyWindow))
	if err != nil {
		return a.stats
	}
	a.stats, a.statsAt = stats, now
	return stats
}

// noteAssigned 把刚分配出去的 Key 计入缓存（缓存期内连续建 Key 时，权重随分配的数量即时变化）。
func (a *KeyAssigner) noteAssigned(nodeID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stats == nil {
		return
	}
	s := a.stats[nodeID]
	s.Total++
	s.Active++
	a.stats[nodeID] = s
}
