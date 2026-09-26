package master

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ConfigPublisher 生成配置快照并推给从节点（设计第 6 节第二类）：
//   - 设置改动后等 200 毫秒合并，重新生成；内容变了就通知所有在线节点拉取；
//   - 每 30 秒定时重新生成一次，兜住绕开设置仓储直接改库的情况；
//   - 版本是内容哈希，选号返回里带着它，从节点版本对不上时先同步拉取（版本栅栏）。
type ConfigPublisher struct {
	settings SettingsReader
	nodes    NodeStore
	events   *EventHub
	trust    func() Trust

	sectionsMu sync.Mutex
	sections   map[string]SectionProvider

	mu      sync.RWMutex
	current *globalSnapshot
	// delivered：每台节点最近一次拉到的快照里的信任材料标识（根指纹、票据公钥，只在内存；
	// 主节点重启后节点重连会先拉配置，随即补齐）。密钥轮换据此判断新版本是否已送达所有节点。
	delivered map[int64]map[string]struct{}

	triggerMu sync.Mutex
	timer     *time.Timer
	now       func() time.Time
}

// NewConfigPublisher 创建发布器。trust 返回当前要下发的信任材料（所有未停用的根证书指纹、票据公钥）。
func NewConfigPublisher(settings SettingsReader, nodes NodeStore, events *EventHub, trust func() Trust) *ConfigPublisher {
	return &ConfigPublisher{settings: settings, nodes: nodes, events: events, trust: trust, sections: map[string]SectionProvider{}, delivered: map[int64]map[string]struct{}{}, now: time.Now}
}

// RegisterSection 登记一个配置分段（错误透传规则、TLS 指纹等）。登记后下一次生成生效。
func (p *ConfigPublisher) RegisterSection(name string, provide SectionProvider) {
	p.sectionsMu.Lock()
	p.sections[name] = provide
	p.sectionsMu.Unlock()
}

// Rebuild 立即重新生成；内容变了就通知在线节点。
func (p *ConfigPublisher) Rebuild(ctx context.Context) error {
	p.sectionsMu.Lock()
	sections := make(map[string]SectionProvider, len(p.sections))
	for k, v := range p.sections {
		sections[k] = v
	}
	p.sectionsMu.Unlock()

	next, err := buildGlobal(ctx, p.settings, sections, p.trust(), func(key string) {
		slog.Error("relay config snapshot dropped a value that looks like it contains a secret", "key", key)
	})
	if err != nil {
		return err
	}
	p.mu.Lock()
	changed := p.current == nil || p.current.hash != next.hash
	p.current = next
	p.mu.Unlock()
	if changed {
		p.notifyAll(ctx)
	}
	return nil
}

// Trigger 安排一次合并后的重新生成（改动后 200 毫秒）。设置写入、节点配置修改、
// 分段内容修改都调用它；多次调用合并成一次。
func (p *ConfigPublisher) Trigger() {
	p.triggerMu.Lock()
	defer p.triggerMu.Unlock()
	if p.timer != nil {
		return
	}
	p.timer = time.AfterFunc(configRebuildDelay, func() {
		p.triggerMu.Lock()
		p.timer = nil
		p.triggerMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.Rebuild(ctx); err != nil {
			slog.Error("relay config rebuild failed", "error", err)
		}
	})
}

// OnSettingsChanged 是 SettingChangeHub 的订阅回调：只有下发的设置和主从分流设置改了才重新生成。
func (p *ConfigPublisher) OnSettingsChanged(keys []string) {
	for _, k := range keys {
		if isPublishedSetting(k) {
			p.Trigger()
			return
		}
	}
}

func isPublishedSetting(key string) bool {
	if strings.HasPrefix(key, "relay_") {
		return true
	}
	for _, k := range ForwardingSettingKeys {
		if k == key {
			return true
		}
	}
	return false
}

// RunRecheck 定时重新生成，直到 ctx 结束。
func (p *ConfigPublisher) RunRecheck(ctx context.Context) {
	t := time.NewTicker(configRecheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := p.Rebuild(rctx); err != nil {
				slog.Error("relay config recheck failed", "error", err)
			}
			cancel()
		}
	}
}

// SnapshotFor 返回某台节点当前的完整快照。
func (p *ConfigPublisher) SnapshotFor(ctx context.Context, nodeID int64) (*relayv1.ConfigSnapshot, error) {
	p.mu.RLock()
	cur := p.current
	p.mu.RUnlock()
	if cur == nil {
		if err := p.Rebuild(ctx); err != nil {
			return nil, err
		}
		p.mu.RLock()
		cur = p.current
		p.mu.RUnlock()
	}
	node, err := p.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return cur.snapshotFor(node), nil
}

// VersionFor 返回某台节点当前的配置版本，选号返回里带着它（WP7）。
func (p *ConfigPublisher) VersionFor(ctx context.Context, nodeID int64) (string, error) {
	snap, err := p.SnapshotFor(ctx, nodeID)
	if err != nil {
		return "", err
	}
	return snap.Version, nil
}

// NotifyNode 告诉某台节点它的当前版本（事件流建立时、这台的节点配置改了时）。
func (p *ConfigPublisher) NotifyNode(ctx context.Context, nodeID int64) {
	version, err := p.VersionFor(ctx, nodeID)
	if err != nil {
		return
	}
	p.events.SendTo(nodeID, &relayv1.MasterEnvelope{Body: &relayv1.MasterEnvelope_ConfigChanged{ConfigChanged: &relayv1.ConfigChanged{Version: version}}})
}

func (p *ConfigPublisher) notifyAll(ctx context.Context) {
	for _, nodeID := range p.events.ConnectedNodes() {
		p.NotifyNode(ctx, nodeID)
	}
}

// FetchConfig 是 RelayControl.FetchConfig 的实现，由 Control 转调。
func (p *ConfigPublisher) FetchConfig(ctx context.Context, nodeID int64, known string) (*relayv1.ConfigSnapshot, error) {
	snap, err := p.SnapshotFor(ctx, nodeID)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "relay config is not available")
	}
	// 版本哈希包含信任材料，所以"没变"也说明节点手里就是这一份。
	ids := Trust{RootFingerprints: snap.RootFingerprints, TicketPublicKeys: snap.TicketPublicKeys}.deliveryIDs()
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	p.mu.Lock()
	p.delivered[nodeID] = set
	p.mu.Unlock()
	if known != "" && known == snap.Version {
		return &relayv1.ConfigSnapshot{Version: snap.Version, Unchanged: true}, nil
	}
	return snap, nil
}

// nodesMissing 返回 nodeIDs 里还没拉到含某项信任材料（deliveryID）的配置的节点。
func (p *ConfigPublisher) nodesMissing(nodeIDs []int64, deliveryID string) []int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var missing []int64
	for _, id := range nodeIDs {
		if _, ok := p.delivered[id][deliveryID]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}
