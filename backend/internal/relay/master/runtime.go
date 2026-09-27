package master

import (
	"context"
	"crypto/ed25519"
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
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
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
	// AccessChanges：用户、分组、订阅、平台配额的改动，运行时转成作废推给从节点。
	AccessChanges *service.AccessChangeHub
	// Users：用户被停用、删除时推票据吊销（设计 8.1）；nil 时不推（以选号复查为准）。
	Users UserStatusReader
	// Leases：额度租约存储（设计第 4 节）；nil 时不提供额度服务（测试）。
	Leases LeaseStore
	// ReservedSink：主从分流运行时把冻结额读取挂到余额预检上（service.BillingCacheService）。
	ReservedSink ReservedBalanceSink
	Notifier     Notifier
	// NewSelector 创建选号实现（relayselect，WP7）；nil 时不提供选号（测试、还没接入的部署）。
	NewSelector func(SelectEnv) Selector
	// NewSettler 创建扣费入账（relaysettle，WP8）；nil 时不提供扣费服务。
	NewSettler func(SettleEnv) Settler
	// VoucherPartitions 维护已入账凭证表的按月分区（预建、过期删除，设计 5.4）；nil 时不维护。
	VoucherPartitions VoucherPartitionMaintainer
	// Sections 是配置快照里 settings 表之外的转发配置分段（错误透传规则等，设计 6），按名字登记。
	Sections map[string]SectionProvider
}

// VoucherPartitionMaintainer 维护已入账凭证表的分区（repository.RelayVoucherPartitions）。
type VoucherPartitionMaintainer interface {
	Maintain(ctx context.Context, now time.Time) error
}

// ReservedBalanceSink 接收冻结额读取（service.BillingCacheService 满足）。
type ReservedBalanceSink interface {
	SetRelayReservedBalanceReader(service.RelayReservedBalanceReader)
}

// Runtime 按主从分流总开关动态启停主节点的主从通信：开关打开当场开端口、起后台任务，
// 关掉当场停止。开关关闭时除了订阅开关变化之外什么都不做（开发计划第 1 节门槛 3）。
type Runtime struct {
	deps RuntimeDeps

	mu      sync.Mutex
	status  RuntimeStatus
	running *runningRelay
	unsub   func()

	// keyMu 串行化密钥轮换操作（预备、启用、停用）。
	keyMu sync.Mutex
	now   func() time.Time
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
	signing     *signingKeys
	revoker     *ticketRevoker
	quotas      *Quotas
	recaller    *EventRecaller
	quotaEvents *quotaEvents
	selector    Selector
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
		var q *Quotas
		if r.running != nil {
			q = r.running.quotas
		}
		r.voidAllLeases(ctx, q)
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

// voidAllLeases 主从分流被关闭：作废所有节点的生效租约，钱全部放回（设计 4.3）。
// 开关可能被直接写库或在主节点停着时改掉，绕过了 SetEnabled 的"先停用所有节点"检查；
// 关着的时候没有到期回收，不放回的话这部分余额会一直不能花、不能退。
// 只在"因为开关关闭而停止"时做：进程退出（Close）不作废，重启后按纪元核对继续用。
func (r *Runtime) voidAllLeases(ctx context.Context, q *Quotas) {
	if r.deps.Leases == nil || r.deps.Store == nil {
		return
	}
	nodes, err := r.deps.Store.List(ctx)
	if err != nil {
		slog.Warn("relay off: list nodes to void leases failed", "error", err)
		return
	}
	for _, n := range nodes {
		leases, err := r.deps.Leases.ListActiveByNode(ctx, n.ID)
		if err != nil {
			slog.Warn("relay off: list node leases failed", "node_id", n.ID, "error", err)
			continue
		}
		if len(leases) == 0 {
			continue
		}
		if q == nil {
			if q, err = NewQuotas(ctx, r.deps.Leases, "", r.now); err != nil {
				slog.Warn("relay off: open quota store failed", "error", err)
				return
			}
		}
		if returned, err := q.closeLeases(ctx, leases, LeaseVoided, "relay_disabled"); err != nil {
			slog.Warn("relay off: void leases failed", "node_id", n.ID, "error", err)
		} else {
			slog.Info("relay off: leases voided", "node_id", n.ID, "count", len(leases), "returned", FromMicros(returned))
		}
	}
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
	signing, err := loadSigningKeys(keys)
	if err != nil {
		return nil, err
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
	publisher := NewConfigPublisher(r.deps.Settings, r.deps.Store, events, func() Trust {
		return Trust{RootFingerprints: ca.RootFingerprints(), TicketPublicKeys: signing.ticketPublicKeys()}
	})
	for name, provide := range r.deps.Sections {
		publisher.RegisterSection(name, provide)
	}
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
	var quotas *Quotas
	if r.deps.Leases != nil {
		if quotas, err = NewQuotas(ctx, r.deps.Leases, server.Epoch(), r.now); err != nil {
			return nil, err
		}
	}
	var recaller *EventRecaller
	var qEvents *quotaEvents
	if quotas != nil {
		recaller = NewEventRecaller(quotas, events)
		qEvents = newQuotaEvents(quotas, recaller)
	}
	var revoker *ticketRevoker
	if r.deps.Users != nil {
		revoker = newTicketRevoker(r.deps.Users, events, r.now)
		if qEvents != nil {
			revoker.onInactive = qEvents.OnUserInactive
		}
	}
	events.OnConnect = func(nodeID int64) {
		publisher.NotifyNode(context.Background(), nodeID)
		if revoker != nil {
			revoker.sendAll(nodeID)
		}
	}
	relayv1.RegisterRelayEnrollmentServer(server.GRPC(), NewEnrollment(nodes, server))
	control := NewControl(publisher)
	if quotas != nil {
		control.AttachQuotas(quotas, recaller, server.Epoch())
	}
	var selector Selector
	if r.deps.NewSelector != nil {
		selector = r.deps.NewSelector(SelectEnv{
			Epoch:  server.Epoch(),
			Quotas: quotas,
			IssueVoucher: func(v *relayv1.Voucher) ([]byte, *relayv1.Voucher, error) {
				return sign.IssueVoucher(signing.currentVoucherSigner(), v, r.now())
			},
			NodeEncryptionKey: nodes.EncryptionKey,
			ConfigVersion:     publisher.VersionFor,
			VerifyVoucher:     r.VerifyVoucher,
		})
		control.AttachSelector(selector, server.Epoch())
		RouteNodeEvents(events, selector)
	}
	relayv1.RegisterRelayControlServer(server.GRPC(), control)
	if r.deps.NewSettler != nil {
		env := SettleEnv{VerifyVoucher: r.VerifyVoucher, LastSuspectRevocation: r.deps.Store.LastSuspectRevocation}
		if quotas != nil {
			env.RefreshUser = quotas.RefreshUser
		}
		relayv1.RegisterRelayBillingServer(server.GRPC(), NewBilling(r.deps.NewSettler(env)))
	}
	relayv1.RegisterRelayEventsServer(server.GRPC(), events)

	if err := publisher.Rebuild(ctx); err != nil {
		return nil, fmt.Errorf("build relay config snapshot: %w", err)
	}
	lis, err := net.Listen("tcp", cfg.Relay.MasterListenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on relay.master_listen_addr: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	running := &runningRelay{server: server, listener: lis, nodes: nodes, publisher: publisher, events: events, invalidator: invalidator, cancel: cancel, ca: ca, keys: keys, signing: signing, revoker: revoker, quotas: quotas, recaller: recaller, quotaEvents: qEvents, selector: selector}
	var unsubs []func()
	if r.deps.Hub != nil {
		unsubs = append(unsubs, r.deps.Hub.Subscribe(publisher.OnSettingsChanged))
	}
	if r.deps.APIKeys != nil {
		r.deps.APIKeys.SetAuthCacheInvalidationListener(invalidator.APIKeyHash)
		unsubs = append(unsubs, func() { r.deps.APIKeys.SetAuthCacheInvalidationListener(nil) })
	}
	if r.deps.AccessChanges != nil {
		unsubs = append(unsubs, r.deps.AccessChanges.Subscribe(invalidator.OnAccessChange))
		if revoker != nil {
			unsubs = append(unsubs, r.deps.AccessChanges.Subscribe(revoker.OnAccessChange))
		}
		if qEvents != nil {
			unsubs = append(unsubs, r.deps.AccessChanges.Subscribe(qEvents.OnAccessChange))
		}
	}
	if quotas != nil {
		// 管理员减余额、退款遇到锁着的余额时先从在线节点收回（设计 4.4）。
		service.SetRelayBalanceReclaimer(&balanceReclaimer{quotas: quotas, recaller: recaller})
		unsubs = append(unsubs, func() { service.SetRelayBalanceReclaimer(nil) })
	}
	if quotas != nil && r.deps.ReservedSink != nil {
		// 余额预检从此减去锁在从节点上的部分（设计 4.3）；停止时摘下，回到只看余额。
		r.deps.ReservedSink.SetRelayReservedBalanceReader(quotas)
		unsubs = append(unsubs, func() { r.deps.ReservedSink.SetRelayReservedBalanceReader(nil) })
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
	if quotas != nil {
		running.goRun(func() { quotas.RunExpiry(runCtx) })
		running.goRun(func() { qEvents.run(runCtx) })
	}
	if revoker != nil {
		running.goRun(func() { revoker.run(runCtx) })
	}
	running.goRun(func() { runPendingPurge(runCtx, nodes) })
	if p := r.deps.VoucherPartitions; p != nil {
		running.goRun(func() { runVoucherPartitions(runCtx, p, r.now) })
	}
	return running, nil
}

// runVoucherPartitions 启动时和之后每天维护一次已入账凭证表的分区。
func runVoucherPartitions(ctx context.Context, p VoucherPartitionMaintainer, now func() time.Time) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if err := p.Maintain(ctx, now()); err != nil {
			slog.Warn("relay voucher partition maintenance failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
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
	if rr.selector != nil {
		rr.selector.Close()
	}
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
	// ErrRelayNotRunning：主从分流没在运行，节点和密钥操作做不了。
	ErrRelayNotRunning = errors.New("relay is not running; turn it on first")
	// ErrInvalidGeneralConfig：通用配置不合法。
	ErrInvalidGeneralConfig = errors.New("invalid relay general config")
	// ErrUnknownKeyPurpose：密钥用途不是 root_ca / ticket / voucher。
	ErrUnknownKeyPurpose = errors.New("unknown relay key purpose")
	// ErrKeyNotFound：没有这个版本（或已停用）。
	ErrKeyNotFound = errors.New("relay key version not found")
	// ErrKeyNotStaged：只有预备中的版本可以启用。
	ErrKeyNotStaged = errors.New("only a staged relay key can be activated")
	// ErrKeyInUse：正在签发的版本不能停用，先启用新版本。
	ErrKeyInUse = errors.New("the signing relay key cannot be retired; activate a new version first")
	// ErrKeyRetireTooEarly：新版本签发还不够久，旧版本签的证书、票据或凭证可能还在用。
	ErrKeyRetireTooEarly = errors.New("the new relay key has not signed long enough for everything signed by the old one to expire")
	// ErrKeyNotDelivered：还有在服务的节点没拿到预备版本，这时切换签发会让它们认不出新签名。
	ErrKeyNotDelivered = errors.New("some serving relay nodes have not received the staged key yet")
)

// KeyNotDeliveredError 列出还没拿到预备版本的节点。errors.Is(err, ErrKeyNotDelivered) 成立。
type KeyNotDeliveredError struct{ NodeIDs []int64 }

func (e *KeyNotDeliveredError) Error() string {
	return fmt.Sprintf("%s (nodes %v)", ErrKeyNotDelivered.Error(), e.NodeIDs)
}

func (e *KeyNotDeliveredError) Unwrap() error { return ErrKeyNotDelivered }

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

// DisableNode 停用节点（吊销证书、断开连接）；它锁着的额度全部作废放回（设计 4.4、5.4：之后补报的已发生用量照常扣）。
func (r *Runtime) DisableNode(ctx context.Context, nodeID, actor int64) error {
	if err := r.nodeOp(func(n *Nodes) error { return n.Disable(ctx, nodeID, actor) }); err != nil {
		return err
	}
	return r.voidNodeLeases(ctx, nodeID, "node_disabled")
}

// voidNodeLeases 作废一台节点的全部租约。
func (r *Runtime) voidNodeLeases(ctx context.Context, nodeID int64, reason string) error {
	rr, err := r.runningRelay()
	if err != nil || rr.quotas == nil {
		return err
	}
	returned, err := rr.quotas.VoidNode(ctx, nodeID, reason)
	if err != nil {
		return fmt.Errorf("void relay node leases: %w", err)
	}
	if returned > 0 {
		slog.Info("relay node leases voided", "node_id", nodeID, "reason", reason, "returned", FromMicros(returned))
	}
	return nil
}

// Quotas 返回运行中的额度服务（WP7 选号时申请额度用）；没在运行时为 nil。
func (r *Runtime) Quotas() *Quotas {
	rr, err := r.runningRelay()
	if err != nil {
		return nil
	}
	return rr.quotas
}

// EnableNode 让停用的节点回到待激活。
func (r *Runtime) EnableNode(ctx context.Context, nodeID, actor int64) error {
	return r.nodeOp(func(n *Nodes) error { return n.Enable(ctx, nodeID, actor) })
}

// RevokeNode 因怀疑被攻破吊销节点证书（退回待激活）。
func (r *Runtime) RevokeNode(ctx context.Context, nodeID, actor int64, reason string) error {
	if err := r.nodeOp(func(n *Nodes) error { return n.RevokeCertificates(ctx, nodeID, actor, reason) }); err != nil {
		return err
	}
	return r.voidNodeLeases(ctx, nodeID, "node_revoked")
}

// SetNodeAllowMultiIP 对这台单独关闭或打开"同时两个 IP"的判断。
func (r *Runtime) SetNodeAllowMultiIP(ctx context.Context, nodeID, actor int64, allow bool) error {
	return r.nodeOp(func(n *Nodes) error { return n.SetAllowMultiIP(ctx, nodeID, actor, allow) })
}

// KeyInfo 描述一个密钥版本（管理页展示）。
type KeyInfo struct {
	Purpose keystore.Purpose `json:"purpose"`
	Version int              `json:"version"`
	// Fingerprint：根证书是证书指纹，票据和凭证是公钥的 SHA-256。
	Fingerprint string     `json:"fingerprint"`
	Staged      bool       `json:"staged"`
	Signing     bool       `json:"signing"`
	CreatedAt   time.Time  `json:"created_at"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	// PendingNodeIDs：预备版本还没送达的在服务节点（为空才能启用；凭证不下发，恒为空）。
	PendingNodeIDs []int64 `json:"pending_node_ids,omitempty"`
}

// ParseKeyPurpose 校验管理接口里的用途名。
func ParseKeyPurpose(s string) (keystore.Purpose, error) {
	switch p := keystore.Purpose(s); p {
	case keystore.PurposeRootCA, keystore.PurposeTicket, keystore.PurposeVoucher:
		return p, nil
	}
	return "", ErrUnknownKeyPurpose
}

func keyFingerprint(k *keystore.Key) string {
	if k.Purpose == keystore.PurposeRootCA {
		return transport.CertificateFingerprint(k.Certificate)
	}
	pub, _ := k.Signer.Public().(ed25519.PublicKey)
	return publicKeyFingerprint(pub)
}

// keyDeliveryID 是这个版本下发给节点时的标识（与 Trust.deliveryIDs 一致）。
func keyDeliveryID(k *keystore.Key) string {
	if k.Purpose == keystore.PurposeRootCA {
		return rootDeliveryID(transport.CertificateFingerprint(k.Certificate))
	}
	pub, _ := k.Signer.Public().(ed25519.PublicKey)
	return ticketDeliveryID(uint32(k.Version), pub)
}

// Keys 列出某种用途未停用的版本。
func (r *Runtime) Keys(ctx context.Context, purpose keystore.Purpose) ([]KeyInfo, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return nil, err
	}
	ring, err := rr.keys.Ring(purpose)
	if err != nil {
		return nil, err
	}
	out := make([]KeyInfo, 0, len(ring.Keys))
	for _, k := range ring.Keys {
		info := KeyInfo{
			Purpose:     purpose,
			Version:     k.Version,
			Fingerprint: keyFingerprint(k),
			Staged:      k.Staged,
			Signing:     ring.Active != nil && ring.Active.Version == k.Version,
			CreatedAt:   k.CreatedAt,
			ActivatedAt: k.ActivatedAt,
		}
		if k.Staged && keyNeedsDelivery(purpose) {
			if info.PendingNodeIDs, err = r.nodesMissingKey(ctx, rr, k); err != nil {
				return nil, err
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// nodesMissingKey 返回在服务（已激活、排空中）但还没拉到这个版本的节点。
// 离线的在服务节点也算：它回来时要能认出新版本签的东西。
func (r *Runtime) nodesMissingKey(ctx context.Context, rr *runningRelay, k *keystore.Key) ([]int64, error) {
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
	return rr.publisher.nodesMissing(serving, keyDeliveryID(k)), nil
}

// reloadKeys 密钥变动后重新加载：根证书重载 CA，票据和凭证重载签名器；
// 然后立即重新生成配置（新根指纹、新票据公钥随配置推给节点）。
func (r *Runtime) reloadKeys(ctx context.Context, rr *runningRelay, purpose keystore.Purpose) error {
	var err error
	if purpose == keystore.PurposeRootCA {
		err = rr.ca.Reload()
	} else {
		err = rr.signing.reload()
	}
	if err != nil {
		return err
	}
	return rr.publisher.Rebuild(ctx)
}

func findKey(ring *keystore.Ring, version int) *keystore.Key {
	for _, k := range ring.Keys {
		if k.Version == version {
			return k
		}
	}
	return nil
}

// StageKey 轮换第一步：生成预备版本。根证书指纹、票据公钥随配置推给所有从节点，
// 等在服务的节点都拿到之后才能 ActivateKey（开发计划 WP4、WP5：先下发，再切换签发，最后停用旧版本）。
func (r *Runtime) StageKey(ctx context.Context, actor int64, purpose keystore.Purpose) (KeyInfo, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return KeyInfo{}, err
	}
	r.keyMu.Lock()
	defer r.keyMu.Unlock()
	k, err := rr.keys.Stage(purpose)
	if err != nil {
		return KeyInfo{}, err
	}
	if err := r.reloadKeys(ctx, rr, purpose); err != nil {
		return KeyInfo{}, err
	}
	fp := keyFingerprint(k)
	r.audit(ctx, actor, AuditKeyStaged, map[string]any{"purpose": purpose, "version": k.Version, "fingerprint": fp})
	return KeyInfo{Purpose: purpose, Version: k.Version, Fingerprint: fp, Staged: true, CreatedAt: k.CreatedAt}, nil
}

// ActivateKey 轮换第二步：切换到预备版本签发。根证书和票据要求所有在服务的节点
// 都已拿到它（KeyNotDeliveredError），否则切换后它们认不出新签名。
func (r *Runtime) ActivateKey(ctx context.Context, actor int64, purpose keystore.Purpose, version int) error {
	rr, err := r.runningRelay()
	if err != nil {
		return err
	}
	r.keyMu.Lock()
	defer r.keyMu.Unlock()
	ring, err := rr.keys.Ring(purpose)
	if err != nil {
		return err
	}
	target := findKey(ring, version)
	if target == nil {
		return ErrKeyNotFound
	}
	if !target.Staged {
		return ErrKeyNotStaged
	}
	if keyNeedsDelivery(purpose) {
		missing, err := r.nodesMissingKey(ctx, rr, target)
		if err != nil {
			return err
		}
		if len(missing) > 0 {
			return &KeyNotDeliveredError{NodeIDs: missing}
		}
	}
	if err := rr.keys.Activate(purpose, version); err != nil {
		return err
	}
	if err := r.reloadKeys(ctx, rr, purpose); err != nil {
		return err
	}
	r.audit(ctx, actor, AuditKeyActivated, map[string]any{"purpose": purpose, "version": version, "fingerprint": keyFingerprint(target)})
	return nil
}

// RetireKey 轮换第三步：停用旧版本。新版本签发满 keyRetireWait 后才允许（这时旧版本签的
// 节点证书 / 票据都已过期、凭证都已超过入账期限）。预备中的版本随时可以放弃。
func (r *Runtime) RetireKey(ctx context.Context, actor int64, purpose keystore.Purpose, version int) error {
	rr, err := r.runningRelay()
	if err != nil {
		return err
	}
	r.keyMu.Lock()
	defer r.keyMu.Unlock()
	ring, err := rr.keys.Ring(purpose)
	if err != nil {
		return err
	}
	target := findKey(ring, version)
	if target == nil {
		return ErrKeyNotFound
	}
	if !target.Staged {
		if ring.Active == nil || ring.Active.Version == version {
			return ErrKeyInUse
		}
		if ring.Active.ActivatedAt == nil || r.now().Sub(*ring.Active.ActivatedAt) < keyRetireWait(purpose) {
			return ErrKeyRetireTooEarly
		}
	}
	if err := rr.keys.Retire(purpose, version); err != nil {
		return err
	}
	if err := r.reloadKeys(ctx, rr, purpose); err != nil {
		return err
	}
	r.audit(ctx, actor, AuditKeyRetired, map[string]any{"purpose": purpose, "version": version, "fingerprint": keyFingerprint(target), "staged": target.Staged})
	return nil
}
