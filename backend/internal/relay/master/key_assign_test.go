package master_test

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func activeNode(id int64, domain string, bandwidth int) *master.Node {
	return &master.Node{ID: id, Status: master.NodeActive, PublicDomain: domain, BandwidthLimitMbps: bandwidth}
}

type pickEnv struct {
	nodes  []*master.Node
	online []int64
	ratio  int
	stats  map[int64]service.RelayKeyStat
	load   map[int64]float64
	rnd    float64
}

func (p *pickEnv) assigner() *master.KeyAssigner {
	d := master.KeyAssignerDeps{
		Nodes: func(context.Context) ([]*master.Node, error) { return p.nodes, nil },
		Config: func(context.Context) master.GeneralConfig {
			return master.GeneralConfig{MasterRatioPercent: &p.ratio}.WithDefaults()
		},
		Stats: func(context.Context, time.Time) (map[int64]service.RelayKeyStat, error) { return p.stats, nil },
		Load:  func(id int64) float64 { return p.load[id] },
		Rand:  func() float64 { return p.rnd },
	}
	if p.online != nil {
		d.Online = func() []int64 { return p.online }
	}
	return master.NewKeyAssigner(d)
}

func (p *pickEnv) pick(t *testing.T, exclude map[int64]bool) (int64, bool) {
	t.Helper()
	id, ok, err := p.assigner().Pick(context.Background(), exclude)
	require.NoError(t, err)
	return id, ok
}

// 设计 10.2/10.8：先按主节点分配比例决定给不给主节点，其余只在已激活、有域名、在线、负载没超阈值的从节点里选。
func TestKeyAssignerPicksByRatioAndEligibility(t *testing.T) {
	p := &pickEnv{
		nodes: []*master.Node{
			activeNode(1, "a.example.com", 100),
			{ID: 2, Status: master.NodeDraining, PublicDomain: "b.example.com", BandwidthLimitMbps: 100},
			{ID: 3, Status: master.NodeDisabled, PublicDomain: "c.example.com", BandwidthLimitMbps: 100},
			{ID: 4, Status: master.NodePending},
			activeNode(5, "", 100),
			activeNode(6, "f.example.com", 100), // 不在线
		},
		online: []int64{1, 2, 3, 5},
		ratio:  10,
	}

	p.rnd = 0.05 // 掷到 5%（< 10%）：主节点。
	id, ok := p.pick(t, nil)
	require.True(t, ok)
	require.Equal(t, int64(0), id)

	p.rnd = 0.5 // 否则只有节点 1 合格：排空、停用、待激活、没填域名、不在线的都不分。
	id, ok = p.pick(t, nil)
	require.True(t, ok)
	require.Equal(t, int64(1), id)

	// 重新分配时排除原节点：只剩主节点（比例大于 0 时）。
	id, ok = p.pick(t, map[int64]bool{1: true})
	require.True(t, ok)
	require.Equal(t, int64(0), id, "no relay node is left, so the master takes it (10.5)")
}

// 主节点分配比例为 0：主节点不接新 Key；从节点都不可用时不分配（保持未分配）。
func TestKeyAssignerNeverPicksMasterAtRatioZero(t *testing.T) {
	p := &pickEnv{nodes: []*master.Node{activeNode(1, "a.example.com", 100)}, ratio: 0, rnd: 0}
	for i := 0; i < 5; i++ {
		id, ok := p.pick(t, nil)
		require.True(t, ok)
		require.Equal(t, int64(1), id)
	}
	_, ok := p.pick(t, map[int64]bool{1: true})
	require.False(t, ok, "nothing to pick: the key stays unassigned")

	p.nodes = nil
	_, ok = p.pick(t, nil)
	require.False(t, ok)
}

// 权重 = 带宽上限 ÷ (1 + 近 7 天用过的 Key 数)；带宽没填按默认 100。
func TestKeyAssignerWeightsByBandwidthAndActiveKeys(t *testing.T) {
	p := &pickEnv{
		nodes: []*master.Node{activeNode(1, "a.example.com", 1000), activeNode(2, "b.example.com", 1000), activeNode(3, "c.example.com", 0)},
		ratio: 0,
		stats: map[int64]service.RelayKeyStat{1: {Total: 50, Active: 9}, 2: {Total: 1, Active: 0}},
	}
	// 权重：1 → 1000/10 = 100；2 → 1000/1 = 1000；3 → 100/1 = 100。总 1200：[0,100) 节点 1，[100,1100) 节点 2，其余节点 3。
	for rnd, want := range map[float64]int64{0.0: 1, 0.05: 1, 0.08: 1, 0.09: 2, 0.5: 2, 0.9: 2, 0.93: 3, 0.99: 3} {
		p.rnd = rnd
		id, ok := p.pick(t, nil)
		require.True(t, ok)
		require.Equal(t, want, id, "rand %v", rnd)
	}
}

// 负载超阈值（默认 85%）的节点不再分新 Key；都超了时比例大于 0 给主节点，否则给负载最低的。
func TestKeyAssignerRespectsLoadThreshold(t *testing.T) {
	p := &pickEnv{
		nodes: []*master.Node{activeNode(1, "a.example.com", 100), activeNode(2, "b.example.com", 100)},
		ratio: 0, rnd: 0.0,
		load: map[int64]float64{1: 0.9, 2: 0.5},
	}
	id, _ := p.pick(t, nil)
	require.Equal(t, int64(2), id, "node 1 is over the 85% threshold")

	p.load = map[int64]float64{1: 0.95, 2: 0.9}
	id, ok := p.pick(t, nil)
	require.True(t, ok)
	require.Equal(t, int64(2), id, "all over: the least loaded one")

	p.ratio, p.rnd = 10, 0.5
	id, _ = p.pick(t, nil)
	require.Equal(t, int64(0), id, "all relay nodes over the threshold and the master ratio > 0: the master")
}

// ---- 运行时的批量操作 ----

// memKeyRepo 是内存里的 Key 仓储：只实现节点分配用到的部分。
type memKeyRepo struct {
	service.APIKeyRepository
	mu   sync.Mutex
	keys map[int64]*service.APIKey
}

func newMemKeyRepo(keys ...*service.APIKey) *memKeyRepo {
	r := &memKeyRepo{keys: map[int64]*service.APIKey{}}
	for _, k := range keys {
		r.keys[k.ID] = k
	}
	return r
}

func (r *memKeyRepo) AssignRelayNode(_ context.Context, ids []int64, nodeID int64, changedAt *time.Time) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, id := range ids {
		k, ok := r.keys[id]
		if !ok {
			continue
		}
		n := nodeID
		k.RelayNodeID = &n
		if changedAt != nil {
			at := *changedAt
			k.RelayNodeChangedAt = &at
		}
		out = append(out, k.Key)
	}
	return out, nil
}

func (r *memKeyRepo) matches(k *service.APIKey, f service.RelayKeyFilter) bool {
	if f.Unassigned {
		return k.RelayNodeID == nil
	}
	return k.RelayNodeID != nil && *k.RelayNodeID == f.NodeID
}

func (r *memKeyRepo) ListRelayKeyIDs(_ context.Context, f service.RelayKeyFilter, after int64, limit int) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []int64
	for id, k := range r.keys {
		if id > after && r.matches(k, f) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

func (r *memKeyRepo) CountRelayKeys(ctx context.Context, f service.RelayKeyFilter) (int64, error) {
	ids, _ := r.ListRelayKeyIDs(ctx, f, 0, 1<<30)
	return int64(len(ids)), nil
}

func (r *memKeyRepo) RelayKeyStats(context.Context, time.Time) (map[int64]service.RelayKeyStat, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[int64]service.RelayKeyStat{}
	for _, k := range r.keys {
		if k.RelayNodeID != nil {
			s := out[*k.RelayNodeID]
			s.Total++
			out[*k.RelayNodeID] = s
		}
	}
	return out, nil
}

func (r *memKeyRepo) node(id int64) *int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.keys[id].RelayNodeID
}

type keyAdminHarness struct {
	*runtimeHarness
	repo  *memKeyRepo
	nodes []*master.Node
}

func newKeyAdminHarness(t *testing.T, ratio int, keys ...*service.APIKey) *keyAdminHarness {
	t.Helper()
	ctx := context.Background()
	repo := newMemKeyRepo(keys...)
	apiKeys := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, nil)
	h := newRuntimeWith(t, nil, func(d *master.RuntimeDeps) { d.APIKeys = apiKeys })
	g := master.GeneralConfig{MasterRatioPercent: &ratio}
	_, err := h.runtime.SetGeneralConfig(ctx, 1, g)
	require.NoError(t, err)
	st, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)
	out := &keyAdminHarness{runtimeHarness: h, repo: repo}
	for _, d := range []string{"a.example.com", "b.example.com"} {
		n, err := h.store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp-" + d, IdentityPublicKey: []byte{1}}, 20)
		require.NoError(t, err)
		require.NoError(t, h.store.Activate(ctx, n.ID, master.Activation{PublicDomain: d, BandwidthLimitMbps: 100, At: time.Now()}))
		out.nodes = append(out.nodes, n)
	}
	return out
}

func apiKey(id int64, node *int64) *service.APIKey {
	return &service.APIKey{ID: id, Key: "sk-" + string(rune('a'+id)), RelayNodeID: node}
}

func ptr(v int64) *int64 { return &v }

// 设计 10.2 重新分配：单个移动记"分配改变时间"；目标必须能接收。
func TestRuntimeMovesKeysBetweenNodes(t *testing.T) {
	ctx := context.Background()
	h := newKeyAdminHarness(t, 10, apiKey(1, ptr(0)), apiKey(2, nil))
	a, b := h.nodes[0].ID, h.nodes[1].ID

	n, err := h.runtime.MoveKeys(ctx, 1, []int64{1, 2}, a)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, a, *h.repo.node(1))
	require.Equal(t, a, *h.repo.node(2))
	require.NotNil(t, h.repo.keys[1].RelayNodeChangedAt, "a reassignment drives the address-changed notice")

	// 不能分给不存在的、没激活的节点。
	_, err = h.runtime.MoveKeys(ctx, 1, []int64{1}, 999)
	require.Error(t, err)
	require.NoError(t, h.runtime.Nodes().Disable(ctx, b, 1))
	_, err = h.runtime.MoveKeys(ctx, 1, []int64{1}, b)
	require.ErrorIs(t, err, master.ErrKeyTargetUnavailable)
}

// 主节点分配比例为 0：Key 不能分给主节点（设计 10.8）。
func TestRuntimeRefusesMasterTargetAtRatioZero(t *testing.T) {
	ctx := context.Background()
	h := newKeyAdminHarness(t, 0, apiKey(1, nil))
	_, err := h.runtime.MoveKeys(ctx, 1, []int64{1}, 0)
	require.ErrorIs(t, err, master.ErrMasterRatioZero)
	_, _, err = h.runtime.MoveNodeKeys(ctx, 1, h.nodes[0].ID, ptr(0))
	require.ErrorIs(t, err, master.ErrMasterRatioZero)
}

// 按节点批量移动：指定目标时全给它；不指定时每把按分配规则重选（不选原节点）；选不出的留在原节点并计数。
func TestRuntimeMovesAllKeysOfANode(t *testing.T) {
	ctx := context.Background()
	h := newKeyAdminHarness(t, 0, apiKey(1, nil), apiKey(2, nil), apiKey(3, nil))
	a, b := h.nodes[0].ID, h.nodes[1].ID
	_, err := h.runtime.MoveKeys(ctx, 1, []int64{1, 2, 3}, a)
	require.NoError(t, err)

	// 在线才能被分配：测试里节点没连事件流，所以自动重选找不到目标，Key 留在原节点。
	moved, left, err := h.runtime.MoveNodeKeys(ctx, 1, a, nil)
	require.NoError(t, err)
	require.Equal(t, 0, moved)
	require.Equal(t, 3, left)
	require.Equal(t, a, *h.repo.node(1))

	// 指定目标：全部移过去。
	moved, left, err = h.runtime.MoveNodeKeys(ctx, 1, a, &b)
	require.NoError(t, err)
	require.Equal(t, 3, moved)
	require.Equal(t, 0, left)
	for _, id := range []int64{1, 2, 3} {
		require.Equal(t, b, *h.repo.node(id))
	}
	summary, err := h.runtime.KeyAssignmentSummary(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(3), summary.Nodes[b].Total)
	require.Zero(t, summary.Nodes[a].Total)
}

// 上线时给存量 Key 批量分配（10.7 第 1 步）：第一次分配不记"分配改变时间"，已经分配的不动。
func TestRuntimeAssignsUnassignedKeysWithoutMarkingChange(t *testing.T) {
	ctx := context.Background()
	h := newKeyAdminHarness(t, 100, apiKey(1, nil), apiKey(2, nil), apiKey(3, ptr(77)))
	assigned, left, err := h.runtime.AssignUnassignedKeys(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 2, assigned)
	require.Zero(t, left)
	require.Equal(t, int64(0), *h.repo.node(1), "ratio 100: everything to the master")
	require.Equal(t, int64(0), *h.repo.node(2))
	require.Equal(t, int64(77), *h.repo.node(3), "assigned keys are left alone")
	require.Nil(t, h.repo.keys[1].RelayNodeChangedAt)
	summary, err := h.runtime.KeyAssignmentSummary(ctx)
	require.NoError(t, err)
	require.Zero(t, summary.Unassigned)
	require.Equal(t, int64(2), summary.Master)
}
