// Package node 是主从分流的从节点侧（docs/MASTER_RELAY_NODES.md）。
//
// WP4 起包含：配置快照（替代从节点上的 SettingRepository）、版本栅栏、
// 事件流（配置变更、缓存作废）。从节点装配（WP9）在这之上复用现有转发服务。
package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"golang.org/x/sync/singleflight"
)

// ErrReadOnlySettings：从节点不能写系统设置。转发路径上需要写的（如 Ollama Cloud 用量设置）
// 改成事件发给主节点执行（开发计划 2.2）。
var ErrReadOnlySettings = errors.New("relay node settings are read-only; writes go to the master as events")

// ErrNoConfig：还没拿到配置快照。从节点没拿到配置之前不接请求（设计第 6 节）。
var ErrNoConfig = errors.New("relay node has no configuration from the master yet")

// NodeConfig 与 master.NodeConfig 的 JSON 相同（这里不依赖 master 包）。
type NodeConfig struct {
	NodeID             int64  `json:"node_id"`
	Name               string `json:"name"`
	Region             string `json:"region"`
	PublicDomain       string `json:"public_domain"`
	Status             string `json:"status"`
	BandwidthLimitMbps int    `json:"bandwidth_limit_mbps"`
	General            struct {
		MasterRatioPercent       *int   `json:"master_ratio_percent"`
		APIKeyNodeRule           string `json:"api_key_node_rule"`
		HeartbeatIntervalSeconds int    `json:"heartbeat_interval_seconds"`
		OfflineAfterSeconds      int    `json:"offline_after_seconds"`
		DrainMaxWaitMinutes      int    `json:"drain_max_wait_minutes"`
		LoadThresholdPercent     int    `json:"load_threshold_percent"`
		AssignmentRefreshSeconds int    `json:"assignment_refresh_seconds"`
	} `json:"general"`
}

// ConfigCache 保存主节点下发的配置快照，并实现 service.SettingRepository：
// 从节点上的 SettingService 用它代替数据库，读到的只有白名单内的设置。
type ConfigCache struct {
	snap   atomic.Pointer[relayv1.ConfigSnapshot]
	node   atomic.Pointer[NodeConfig]
	mu     sync.Mutex
	onSwap []func(*relayv1.ConfigSnapshot)
}

var _ service.SettingRepository = (*ConfigCache)(nil)

// NewConfigCache 创建空的配置缓存。
func NewConfigCache() *ConfigCache { return &ConfigCache{} }

// OnSwap 注册换快照后的回调（例如 SettingService.InvalidateAll、更新固定的根证书指纹）。
// 回调在放行新请求之前同步执行。
func (c *ConfigCache) OnSwap(fn func(*relayv1.ConfigSnapshot)) {
	c.mu.Lock()
	c.onSwap = append(c.onSwap, fn)
	c.mu.Unlock()
}

// Version 返回当前快照版本；还没有快照时为空。
func (c *ConfigCache) Version() string {
	if s := c.snap.Load(); s != nil {
		return s.Version
	}
	return ""
}

// Ready 报告是否已经拿到配置。
func (c *ConfigCache) Ready() bool { return c.snap.Load() != nil }

// Node 返回本节点配置。
func (c *ConfigCache) Node() (*NodeConfig, bool) {
	n := c.node.Load()
	return n, n != nil
}

// Section 返回一个配置分段（JSON）。
func (c *ConfigCache) Section(name string) ([]byte, bool) {
	s := c.snap.Load()
	if s == nil {
		return nil, false
	}
	b, ok := s.Sections[name]
	return b, ok
}

// RootFingerprints 返回快照里的根证书指纹。
func (c *ConfigCache) RootFingerprints() []string {
	if s := c.snap.Load(); s != nil {
		return append([]string(nil), s.RootFingerprints...)
	}
	return nil
}

// Apply 换上新快照，然后同步执行回调。unchanged 的回复不换。
func (c *ConfigCache) Apply(snap *relayv1.ConfigSnapshot) error {
	if snap == nil || snap.Unchanged {
		return nil
	}
	var nc NodeConfig
	if len(snap.NodeConfig) > 0 {
		if err := json.Unmarshal(snap.NodeConfig, &nc); err != nil {
			return fmt.Errorf("relay node config is malformed: %w", err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.node.Store(&nc)
	c.snap.Store(snap)
	for _, fn := range c.onSwap {
		fn(snap)
	}
	return nil
}

// ---- service.SettingRepository ----

func (c *ConfigCache) settings() (map[string]string, error) {
	s := c.snap.Load()
	if s == nil {
		return nil, ErrNoConfig
	}
	return s.Settings, nil
}

func (c *ConfigCache) Get(_ context.Context, key string) (*service.Setting, error) {
	m, err := c.settings()
	if err != nil {
		return nil, err
	}
	v, ok := m[key]
	if !ok {
		return nil, service.ErrSettingNotFound
	}
	return &service.Setting{Key: key, Value: v}, nil
}

func (c *ConfigCache) GetValue(_ context.Context, key string) (string, error) {
	m, err := c.settings()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return v, nil
}

func (c *ConfigCache) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	m, err := c.settings()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (c *ConfigCache) GetAll(context.Context) (map[string]string, error) {
	m, err := c.settings()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out, nil
}

func (c *ConfigCache) Set(context.Context, string, string) error { return ErrReadOnlySettings }

func (c *ConfigCache) SetMultiple(context.Context, map[string]string) error {
	return ErrReadOnlySettings
}

func (c *ConfigCache) Delete(context.Context, string) error { return ErrReadOnlySettings }

// ---- 拉取与版本栅栏 ----

// ConfigSyncer 从主节点拉取配置并换上。
type ConfigSyncer struct {
	cache   *ConfigCache
	control relayv1.RelayControlClient
	group   singleflight.Group
	// Timeout 是一次拉取的截止时间（默认 3 秒）。
	Timeout time.Duration
}

// NewConfigSyncer 创建拉取器。client 用控制连接。
func NewConfigSyncer(cache *ConfigCache, client *transport.Client) *ConfigSyncer {
	return &ConfigSyncer{cache: cache, control: relayv1.NewRelayControlClient(client.Conn(transport.TierControl)), Timeout: 3 * time.Second}
}

// Sync 拉取一次（并发调用合并成一次）。
func (s *ConfigSyncer) Sync(ctx context.Context) error {
	_, err, _ := s.group.Do("sync", func() (any, error) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.Timeout)
		defer cancel()
		snap, err := s.control.FetchConfig(cctx, &relayv1.FetchConfigRequest{KnownVersion: s.cache.Version()})
		if err != nil {
			return nil, err
		}
		return nil, s.cache.Apply(snap)
	})
	return err
}

// EnsureVersion 是版本栅栏（设计 6.2）：选号返回的版本和本地不同时，先同步拉取再转发。
// 拉取失败返回错误，这个请求应返回 503，不能用旧配置转发。
func (s *ConfigSyncer) EnsureVersion(ctx context.Context, want string) error {
	if want != "" && s.cache.Version() == want {
		return nil
	}
	if err := s.Sync(ctx); err != nil {
		return fmt.Errorf("relay config is behind and could not be refreshed: %w", err)
	}
	return nil
}
