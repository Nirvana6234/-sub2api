package master

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// RuntimeState 是主节点主从通信的运行状态，管理页展示。
type RuntimeState string

const (
	// StateNotMaster：本机 NODE_ROLE 不是 master。
	StateNotMaster RuntimeState = "not_master"
	// StateNotConfigured：没配监听地址或私钥加密密钥，开关打开也不会启动。
	StateNotConfigured RuntimeState = "not_configured"
	// StateOff：总开关关闭（默认），没有端口、没有后台任务。
	StateOff RuntimeState = "off"
	// StateRunning：主从通信端口已开，后台任务在跑。
	StateRunning RuntimeState = "running"
	// StateFailed：开关打开但启动失败，见 Reason。
	StateFailed RuntimeState = "failed"
)

// RuntimeStatus 是运行状态的快照。
type RuntimeStatus struct {
	State            RuntimeState `json:"state"`
	Reason           string       `json:"reason,omitempty"`
	ListenAddr       string       `json:"listen_addr,omitempty"`
	Epoch            string       `json:"epoch,omitempty"`
	RootFingerprints []string     `json:"root_fingerprints,omitempty"`
	Since            time.Time    `json:"since"`
}

// ErrNodesStillServing：还有从节点处于已激活或排空中，不能关闭主从分流。
// 先排空并停用所有从节点：否则关掉后它们手里锁着的额度、没回报的扣费都悬在那里。
var ErrNodesStillServing = errors.New("relay nodes are still active or draining; drain and disable them before turning relay off")

// RuntimeDeps 是运行时的依赖（由 relaywire 注入）。
type RuntimeDeps struct {
	Config   *config.Config
	Store    NodeStore
	Settings service.SettingRepository
	Hub      *service.SettingChangeHub
	APIKeys  *service.APIKeyService
	Notifier Notifier
}

// Runtime 按主从分流总开关动态启停主节点的主从通信：开关打开当场开端口、起后台任务，
// 关掉当场停止。开关关闭时除了订阅开关变化之外什么都不做（开发计划第 1 节门槛 3）。
type Runtime struct {
	deps RuntimeDeps

	mu      sync.Mutex
	status  RuntimeStatus
	running *runningRelay
	unsub   func()
}

type runningRelay struct {
	server      *transport.Server
	listener    net.Listener
	nodes       *Nodes
	publisher   *ConfigPublisher
	events      *EventHub
	invalidator *Invalidator
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	unsubscribe func()
	ca          *CA
}

// NewRuntime 创建运行时（不启动任何东西）。
func NewRuntime(deps RuntimeDeps) *Runtime {
	return &Runtime{deps: deps, status: RuntimeStatus{State: StateOff, Since: time.Now()}}
}

// Init 订阅开关变化并按当前开关状态对齐一次。只在主节点角色下订阅。
func (r *Runtime) Init(ctx context.Context) {
	if r.deps.Config == nil || r.deps.Config.Relay.NodeRole != config.RelayNodeRoleMaster {
		r.setStatus(RuntimeStatus{State: StateNotMaster})
		return
	}
	if r.deps.Hub != nil {
		r.unsub = r.deps.Hub.Subscribe(func(keys []string) {
			for _, k := range keys {
				if k == SettingKeyRelayEnabled {
					go func() {
						cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						r.Reconcile(cctx)
					}()
					return
				}
			}
		})
	}
	r.Reconcile(ctx)
}

// Status 返回当前运行状态。
func (r *Runtime) Status() RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.status
	if r.running != nil {
		st.RootFingerprints = r.running.ca.RootFingerprints()
	}
	return st
}

// Enabled 读取总开关。
func (r *Runtime) Enabled(ctx context.Context) (bool, error) {
	v, err := r.deps.Settings.GetValue(ctx, SettingKeyRelayEnabled)
	if errors.Is(err, service.ErrSettingNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(v), "true"), nil
}

// SetEnabled 打开或关闭主从分流。关闭前要求没有已激活或排空中的从节点。
// 写入设置后当场对齐（开端口或停止）。
func (r *Runtime) SetEnabled(ctx context.Context, enabled bool) (RuntimeStatus, error) {
	if !enabled {
		nodes, err := r.deps.Store.List(ctx)
		if err != nil {
			return r.Status(), err
		}
		for _, n := range nodes {
			if n.Status.Serving() {
				return r.Status(), ErrNodesStillServing
			}
		}
	}
	value := "false"
	if enabled {
		value = "true"
	}
	if err := r.deps.Settings.Set(ctx, SettingKeyRelayEnabled, value); err != nil {
		return r.Status(), err
	}
	r.Reconcile(ctx)
	return r.Status(), nil
}

// Reconcile 让运行状态与开关、配置一致。
func (r *Runtime) Reconcile(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg := r.deps.Config
	if cfg == nil || cfg.Relay.NodeRole != config.RelayNodeRoleMaster {
		r.status = RuntimeStatus{State: StateNotMaster, Since: time.Now()}
		return
	}
	enabled, err := r.Enabled(ctx)
	if err != nil {
		r.status = RuntimeStatus{State: StateFailed, Reason: "cannot read the relay switch: " + err.Error(), Since: time.Now()}
		return
	}
	if !enabled {
		r.stopLocked()
		r.status = RuntimeStatus{State: StateOff, Since: time.Now()}
		return
	}
	if r.running != nil {
		return
	}
	if strings.TrimSpace(cfg.Relay.MasterListenAddr) == "" {
		r.status = RuntimeStatus{State: StateNotConfigured, Reason: "relay.master_listen_addr is not set", Since: time.Now()}
		return
	}
	kek, err := loadKEK(cfg.Relay)
	if err != nil {
		r.status = RuntimeStatus{State: StateNotConfigured, Reason: err.Error(), Since: time.Now()}
		return
	}
	running, err := r.start(ctx, kek)
	if err != nil {
		slog.Error("relay master failed to start", "error", err)
		r.status = RuntimeStatus{State: StateFailed, Reason: err.Error(), Since: time.Now()}
		return
	}
	r.running = running
	r.status = RuntimeStatus{State: StateRunning, ListenAddr: running.listener.Addr().String(), Epoch: running.server.Epoch(), Since: time.Now()}
	slog.Info("relay master started", "listen", running.listener.Addr().String(), "root_fingerprints", running.ca.RootFingerprints())
}

// Close 在进程退出时停止（不改开关）。
func (r *Runtime) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.unsub != nil {
		r.unsub()
		r.unsub = nil
	}
	r.stopLocked()
}

// Nodes 返回运行中的节点管理（管理页用）；没在运行时为 nil。
func (r *Runtime) Nodes() *Nodes {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		return nil
	}
	return r.running.nodes
}

// Publisher 返回运行中的配置发布器；没在运行时为 nil。
func (r *Runtime) Publisher() *ConfigPublisher {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		return nil
	}
	return r.running.publisher
}

func (r *Runtime) setStatus(st RuntimeStatus) {
	r.mu.Lock()
	st.Since = time.Now()
	r.status = st
	r.mu.Unlock()
}

// defaultRelayLimits 是按节点、按类别的限流默认值（设计 7.4）。
var defaultRelayLimits = map[transport.CallClass]transport.Limit{
	transport.ClassEnrollment: {RatePerSecond: 5, Burst: 20, MaxInflight: 20},
	transport.ClassControl:    {RatePerSecond: 3000, Burst: 6000, MaxInflight: 2000},
	transport.ClassEvents:     {MaxInflight: 8},
	transport.ClassBilling:    {RatePerSecond: 50, Burst: 100, MaxInflight: 16},
	transport.ClassModeration: {RatePerSecond: 500, Burst: 1000, MaxInflight: 200},
	transport.ClassLogs:       {RatePerSecond: 20, Burst: 40, MaxInflight: 4},
}

func (r *Runtime) start(ctx context.Context, kek []byte) (*runningRelay, error) {
	cfg := r.deps.Config
	keys, err := keystore.Open(relayKeyDir(cfg.Relay), kek)
	if err != nil {
		return nil, fmt.Errorf("open relay key directory: %w", err)
	}
	ca, err := NewCA(keys)
	if err != nil {
		return nil, fmt.Errorf("load relay root certificate: %w", err)
	}
	general, err := LoadGeneralConfig(ctx, r.deps.Settings)
	if err != nil {
		return nil, err
	}
	nodes := NewNodes(r.deps.Store, ca, r.deps.Notifier, NodesOptions{
		HeartbeatInterval: time.Duration(general.HeartbeatIntervalSeconds) * time.Second,
	})
	if err := nodes.Load(ctx); err != nil {
		return nil, fmt.Errorf("load relay nodes: %w", err)
	}
	events := NewEventHub()
	publisher := NewConfigPublisher(r.deps.Settings, r.deps.Store, events, ca.RootFingerprints)
	invalidator := NewInvalidator(events)

	server, err := transport.NewServer(transport.ServerOptions{
		TLS:        transport.ServerTLSOptions{Certificate: ca.MasterCertificate, Roots: ca.RootPool},
		Authorizer: nodes,
		AdmitConn:  nodes.AdmitConn,
		OnConnect:  nodes.OnConnect,
		Policies:   EnrollmentPolicies(),
		Limits:     defaultRelayLimits,
	})
	if err != nil {
		return nil, err
	}
	nodes.AttachRegistry(server.Registry())
	events.OnConnect = func(nodeID int64) { publisher.NotifyNode(context.Background(), nodeID) }
	relayv1.RegisterRelayEnrollmentServer(server.GRPC(), NewEnrollment(nodes, server))
	relayv1.RegisterRelayControlServer(server.GRPC(), NewControl(publisher))
	relayv1.RegisterRelayEventsServer(server.GRPC(), events)

	if err := publisher.Rebuild(ctx); err != nil {
		return nil, fmt.Errorf("build relay config snapshot: %w", err)
	}
	lis, err := net.Listen("tcp", cfg.Relay.MasterListenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on relay.master_listen_addr: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	running := &runningRelay{server: server, listener: lis, nodes: nodes, publisher: publisher, events: events, invalidator: invalidator, cancel: cancel, ca: ca}
	var unsubs []func()
	if r.deps.Hub != nil {
		unsubs = append(unsubs, r.deps.Hub.Subscribe(publisher.OnSettingsChanged))
	}
	if r.deps.APIKeys != nil {
		r.deps.APIKeys.SetAuthCacheInvalidationListener(invalidator.APIKeyHash)
		unsubs = append(unsubs, func() { r.deps.APIKeys.SetAuthCacheInvalidationListener(nil) })
	}
	running.unsubscribe = func() {
		for _, u := range unsubs {
			u()
		}
	}

	running.goRun(func() {
		if err := server.Serve(lis); err != nil {
			slog.Warn("relay master server stopped", "error", err)
		}
	})
	running.goRun(func() {
		nodes.RunRefresh(runCtx, func(err error) { slog.Warn("relay node state refresh failed", "error", err) })
	})
	running.goRun(func() { publisher.RunRecheck(runCtx) })
	running.goRun(func() { runPendingPurge(runCtx, nodes) })
	return running, nil
}

func (rr *runningRelay) goRun(fn func()) {
	rr.wg.Add(1)
	go func() {
		defer rr.wg.Done()
		fn()
	}()
}

// stopLocked 停止主从通信：先摘掉钩子，再优雅停止服务（最多 10 秒），最后停后台任务。
func (r *Runtime) stopLocked() {
	rr := r.running
	if rr == nil {
		return
	}
	r.running = nil
	rr.unsubscribe()
	done := make(chan struct{})
	go func() {
		rr.server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		rr.server.Stop()
	}
	rr.cancel()
	rr.wg.Wait()
	slog.Info("relay master stopped")
}

// runPendingPurge 每小时清理一次超过 24 小时未激活的节点（设计 11.1）。
func runPendingPurge(ctx context.Context, nodes *Nodes) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := nodes.PurgeStalePending(ctx); err != nil {
				slog.Warn("relay pending purge failed", "error", err)
			}
		}
	}
}

// loadKEK 读取私钥加密密钥：必须显式配置，不自动生成（换了它已有私钥就解不开）。
func loadKEK(c config.RelayConfig) ([]byte, error) {
	raw := strings.TrimSpace(c.KeyEncryptionKey)
	if raw == "" && strings.TrimSpace(c.KeyEncryptionKeyFile) != "" {
		b, err := os.ReadFile(c.KeyEncryptionKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read relay.key_encryption_key_file: %w", err)
		}
		raw = strings.TrimSpace(string(b))
	}
	if raw == "" {
		return nil, errors.New("relay.key_encryption_key (or relay.key_encryption_key_file) is not set")
	}
	return keystore.ParseKEK(raw)
}

// relayKeyDir 返回私钥目录：显式配置的，否则 <DATA_DIR 或 ./data>/relay-keys。
func relayKeyDir(c config.RelayConfig) string {
	if dir := strings.TrimSpace(c.KeyDir); dir != "" {
		return dir
	}
	base := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if base == "" {
		base = "./data"
	}
	return filepath.Join(base, "relay-keys")
}
