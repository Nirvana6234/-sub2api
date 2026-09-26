package master

import (
	"context"
	"encoding/json"
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

	// rootMu 串行化根证书轮换操作（预备、启用、停用）。
	rootMu sync.Mutex
	now    func() time.Time
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
	keys        *keystore.Store
}

// NewRuntime 创建运行时（不启动任何东西）。
func NewRuntime(deps RuntimeDeps) *Runtime {
	return &Runtime{deps: deps, status: RuntimeStatus{State: StateOff, Since: time.Now()}, now: time.Now}
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
func (r *Runtime) SetEnabled(ctx context.Context, actor int64, enabled bool) (RuntimeStatus, error) {
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
	st := r.Status()
	r.audit(ctx, actor, AuditRelaySwitched, map[string]any{"enabled": enabled, "state": st.State, "reason": st.Reason})
	return st, nil
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
	running := &runningRelay{server: server, listener: lis, nodes: nodes, publisher: publisher, events: events, invalidator: invalidator, cancel: cancel, ca: ca, keys: keys}
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

// ---- 管理页操作（handler/admin 的主从分流管理接口调用；权限与二次验证由路由负责，
// 来源 IP 由 handler 用 WithSourceIP 放进 ctx）----

var (
	// ErrRelayNotRunning：主从分流没在运行，节点和根证书操作做不了。
	ErrRelayNotRunning = errors.New("relay is not running; turn it on first")
	// ErrInvalidGeneralConfig：通用配置不合法。
	ErrInvalidGeneralConfig = errors.New("invalid relay general config")
	// ErrRootNotFound：没有这个版本的根证书（或已停用）。
	ErrRootNotFound = errors.New("relay root version not found")
	// ErrRootNotStaged：只有预备中的根证书可以启用。
	ErrRootNotStaged = errors.New("only a staged relay root can be activated")
	// ErrRootInUse：正在签发的根证书不能停用，先启用新的根证书。
	ErrRootInUse = errors.New("the signing relay root cannot be retired; activate a new root first")
	// ErrRootRetireTooEarly：新根证书启用未满 rootRetireAfterActivation，旧根签的节点证书可能还在用。
	ErrRootRetireTooEarly = errors.New("the new relay root has not signed long enough for every node certificate to be reissued")
	// ErrRootNotDelivered：还有在服务的节点没拿到预备根证书的指纹，这时切换签发会让它们连不上。
	ErrRootNotDelivered = errors.New("some serving relay nodes have not received the staged root fingerprint yet")
)

// RootNotDeliveredError 列出还没拿到预备根证书指纹的节点。errors.Is(err, ErrRootNotDelivered) 成立。
type RootNotDeliveredError struct{ NodeIDs []int64 }

func (e *RootNotDeliveredError) Error() string {
	return fmt.Sprintf("%s (nodes %v)", ErrRootNotDelivered.Error(), e.NodeIDs)
}

func (e *RootNotDeliveredError) Unwrap() error { return ErrRootNotDelivered }

// rootRetireAfterActivation：新根证书启用满这么久才能停用旧根。节点证书 24 小时有效，
// 到这时用旧根签的节点证书都已过期，停用旧根不会让在线节点断开。
const rootRetireAfterActivation = 25 * time.Hour

func (r *Runtime) audit(ctx context.Context, actor int64, action string, detail map[string]any) {
	if err := r.deps.Store.Audit(ctx, AuditEntry{ActorUserID: actor, Action: action, SourceIP: sourceIPFrom(ctx), Detail: detail}); err != nil {
		slog.Warn("relay audit write failed", "action", action, "error", err)
	}
}

// ListNodes 列出所有节点（开关关闭时也能看）。
func (r *Runtime) ListNodes(ctx context.Context) ([]*Node, error) { return r.deps.Store.List(ctx) }

// GeneralConfig 返回主从分流通用配置（含默认值）。
func (r *Runtime) GeneralConfig(ctx context.Context) (GeneralConfig, error) {
	return LoadGeneralConfig(ctx, r.deps.Settings)
}

// SetGeneralConfig 保存通用配置。写入经过设置仓储，运行中会自动推给从节点。
func (r *Runtime) SetGeneralConfig(ctx context.Context, actor int64, g GeneralConfig) (GeneralConfig, error) {
	if err := g.Validate(); err != nil {
		return GeneralConfig{}, fmt.Errorf("%w: %v", ErrInvalidGeneralConfig, err)
	}
	before, err := r.GeneralConfig(ctx)
	if err != nil {
		return GeneralConfig{}, err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return GeneralConfig{}, err
	}
	if err := r.deps.Settings.Set(ctx, SettingKeyRelayGeneralConfig, string(raw)); err != nil {
		return GeneralConfig{}, err
	}
	after := g.WithDefaults()
	r.audit(ctx, actor, AuditGeneralConfigChanged, map[string]any{"before": before, "after": after})
	return after, nil
}

func (r *Runtime) runningRelay() (*runningRelay, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		return nil, ErrRelayNotRunning
	}
	return r.running, nil
}

// nodeOp 在运行中的节点管理上做一个操作，成功后让相关节点重新拉配置。
func (r *Runtime) nodeOp(fn func(*Nodes) error) error {
	rr, err := r.runningRelay()
	if err != nil {
		return err
	}
	if err := fn(rr.nodes); err != nil {
		return err
	}
	rr.publisher.Trigger()
	return nil
}

// ActivateNode 核对指纹后激活待激活节点（设计 11.2）。
func (r *Runtime) ActivateNode(ctx context.Context, nodeID int64, fingerprint string, a Activation) error {
	return r.nodeOp(func(n *Nodes) error { return n.Activate(ctx, nodeID, fingerprint, a) })
}

// RejectNode 拒绝待激活节点（这把长期密钥被拉黑）。
func (r *Runtime) RejectNode(ctx context.Context, nodeID, actor int64) error {
	return r.nodeOp(func(n *Nodes) error { return n.Reject(ctx, nodeID, actor) })
}

// RejectAllPendingNodes 一键拒绝所有待激活节点。
func (r *Runtime) RejectAllPendingNodes(ctx context.Context, actor int64) (int, error) {
	count := 0
	err := r.nodeOp(func(n *Nodes) error {
		var err error
		count, err = n.RejectAllPending(ctx, actor)
		return err
	})
	return count, err
}

// DisableNode 停用节点（吊销证书、断开连接）。
func (r *Runtime) DisableNode(ctx context.Context, nodeID, actor int64) error {
	return r.nodeOp(func(n *Nodes) error { return n.Disable(ctx, nodeID, actor) })
}

// EnableNode 让停用的节点回到待激活。
func (r *Runtime) EnableNode(ctx context.Context, nodeID, actor int64) error {
	return r.nodeOp(func(n *Nodes) error { return n.Enable(ctx, nodeID, actor) })
}

// RevokeNode 因怀疑被攻破吊销节点证书（退回待激活）。
func (r *Runtime) RevokeNode(ctx context.Context, nodeID, actor int64, reason string) error {
	return r.nodeOp(func(n *Nodes) error { return n.RevokeCertificates(ctx, nodeID, actor, reason) })
}

// SetNodeAllowMultiIP 对这台单独关闭或打开"同时两个 IP"的判断。
func (r *Runtime) SetNodeAllowMultiIP(ctx context.Context, nodeID, actor int64, allow bool) error {
	return r.nodeOp(func(n *Nodes) error { return n.SetAllowMultiIP(ctx, nodeID, actor, allow) })
}

// RootInfo 描述一个根证书版本（管理页展示）。
type RootInfo struct {
	Version     int        `json:"version"`
	Fingerprint string     `json:"fingerprint"`
	Staged      bool       `json:"staged"`
	Signing     bool       `json:"signing"`
	CreatedAt   time.Time  `json:"created_at"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	// PendingNodeIDs：预备版本还没送达的在服务节点（为空才能启用）。
	PendingNodeIDs []int64 `json:"pending_node_ids,omitempty"`
}

// Roots 列出未停用的根证书版本。
func (r *Runtime) Roots(ctx context.Context) ([]RootInfo, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return nil, err
	}
	ring, err := rr.keys.Ring(keystore.PurposeRootCA)
	if err != nil {
		return nil, err
	}
	out := make([]RootInfo, 0, len(ring.Keys))
	for _, k := range ring.Keys {
		info := RootInfo{
			Version:     k.Version,
			Fingerprint: transport.CertificateFingerprint(k.Certificate),
			Staged:      k.Staged,
			Signing:     ring.Active != nil && ring.Active.Version == k.Version,
			CreatedAt:   k.CreatedAt,
			ActivatedAt: k.ActivatedAt,
		}
		if k.Staged {
			if info.PendingNodeIDs, err = r.nodesMissingRoot(ctx, rr, info.Fingerprint); err != nil {
				return nil, err
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// nodesMissingRoot 返回在服务（已激活、排空中）但还没拉到这个根证书指纹的节点。
// 离线的在服务节点也算：它回来时要能认出新根证书签的主节点证书。
func (r *Runtime) nodesMissingRoot(ctx context.Context, rr *runningRelay, fingerprint string) ([]int64, error) {
	nodes, err := r.deps.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	var serving []int64
	for _, n := range nodes {
		if n.Status.Serving() {
			serving = append(serving, n.ID)
		}
	}
	return rr.publisher.NodesMissingRoot(serving, fingerprint), nil
}

// reloadRoots 根证书变动后重新加载 CA 并立即重新生成配置（新指纹随配置推给节点）。
func (r *Runtime) reloadRoots(ctx context.Context, rr *runningRelay) error {
	if err := rr.ca.Reload(); err != nil {
		return err
	}
	return rr.publisher.Rebuild(ctx)
}

func findRoot(ring *keystore.Ring, version int) *keystore.Key {
	for _, k := range ring.Keys {
		if k.Version == version {
			return k
		}
	}
	return nil
}

// StageRoot 轮换第一步：生成预备根证书。它的指纹随配置推给所有从节点，
// 等在服务的节点都拿到之后才能 ActivateRoot（开发计划 WP4：新指纹先下发，再切换签发，最后停用旧根）。
func (r *Runtime) StageRoot(ctx context.Context, actor int64) (RootInfo, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return RootInfo{}, err
	}
	r.rootMu.Lock()
	defer r.rootMu.Unlock()
	k, err := rr.keys.Stage(keystore.PurposeRootCA)
	if err != nil {
		return RootInfo{}, err
	}
	fp := transport.CertificateFingerprint(k.Certificate)
	if err := r.reloadRoots(ctx, rr); err != nil {
		return RootInfo{}, err
	}
	r.audit(ctx, actor, AuditRootStaged, map[string]any{"version": k.Version, "fingerprint": fp})
	return RootInfo{Version: k.Version, Fingerprint: fp, Staged: true, CreatedAt: k.CreatedAt}, nil
}

// ActivateRoot 轮换第二步：用预备根证书签发（主节点证书、之后的节点证书）。
// 还有在服务的节点没拿到它的指纹时拒绝（RootNotDeliveredError）：切换后它们认不出主节点证书。
func (r *Runtime) ActivateRoot(ctx context.Context, actor int64, version int) error {
	rr, err := r.runningRelay()
	if err != nil {
		return err
	}
	r.rootMu.Lock()
	defer r.rootMu.Unlock()
	ring, err := rr.keys.Ring(keystore.PurposeRootCA)
	if err != nil {
		return err
	}
	target := findRoot(ring, version)
	if target == nil {
		return ErrRootNotFound
	}
	if !target.Staged {
		return ErrRootNotStaged
	}
	fp := transport.CertificateFingerprint(target.Certificate)
	missing, err := r.nodesMissingRoot(ctx, rr, fp)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return &RootNotDeliveredError{NodeIDs: missing}
	}
	if err := rr.keys.Activate(keystore.PurposeRootCA, version); err != nil {
		return err
	}
	if err := r.reloadRoots(ctx, rr); err != nil {
		return err
	}
	r.audit(ctx, actor, AuditRootActivated, map[string]any{"version": version, "fingerprint": fp})
	return nil
}

// RetireRoot 轮换第三步：停用旧根证书。签发用的根证书启用满 25 小时后才允许
// （这时旧根签的节点证书都已过期）。预备中的根证书随时可以放弃。
func (r *Runtime) RetireRoot(ctx context.Context, actor int64, version int) error {
	rr, err := r.runningRelay()
	if err != nil {
		return err
	}
	r.rootMu.Lock()
	defer r.rootMu.Unlock()
	ring, err := rr.keys.Ring(keystore.PurposeRootCA)
	if err != nil {
		return err
	}
	target := findRoot(ring, version)
	if target == nil {
		return ErrRootNotFound
	}
	if !target.Staged {
		if ring.Active == nil || ring.Active.Version == version {
			return ErrRootInUse
		}
		if ring.Active.ActivatedAt == nil || r.now().Sub(*ring.Active.ActivatedAt) < rootRetireAfterActivation {
			return ErrRootRetireTooEarly
		}
	}
	if err := rr.keys.Retire(keystore.PurposeRootCA, version); err != nil {
		return err
	}
	if err := r.reloadRoots(ctx, rr); err != nil {
		return err
	}
	r.audit(ctx, actor, AuditRootRetired, map[string]any{"version": version, "fingerprint": transport.CertificateFingerprint(target.Certificate), "staged": target.Staged})
	return nil
}
