package master

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
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
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"google.golang.org/protobuf/proto"
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
	// Probe、Lookup：外部探测和域名解析（nil 用真实实现；测试里替换）。
	Probe  ProbeFunc
	Lookup LookupFunc
	// UserAssignments 保存小白端用户的分配（relay_user_assignments）；nil 时用内存（测试）。
	UserAssignments UserAssignmentStore
	// Metrics 保存心跳的分钟汇总（relay_node_minute_metrics）；nil 时不落库。
	Metrics MetricsSink
	// NewSelector 创建选号实现（relayselect，WP7）；nil 时不提供选号（测试、还没接入的部署）。
	NewSelector func(SelectEnv) Selector
	// NewSettler 创建扣费入账（relaysettle，WP8）；nil 时不提供扣费服务。
	NewSettler func(SettleEnv) Settler
	// VoucherPartitions 维护已入账凭证表的按月分区（预建、过期删除，设计 5.4）；nil 时不维护。
	VoucherPartitions VoucherPartitionMaintainer
	// Sections 是配置快照里 settings 表之外的转发配置分段（错误透传规则等，设计 6），按名字登记。
	Sections map[string]SectionProvider
	// SealedSections 是按节点加密下发的分段（内容可含密钥，如审核、联网搜索引用的代理），按名字登记。
	SealedSections map[string]SectionProvider
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

	// addresses 是节点接入地址的短缓存（address.go）。
	addresses addressCache
	// domains 是已登记的从节点域名（激活或排空中的）的短缓存。
	domains struct {
		mu  sync.Mutex
		set map[string]struct{}
		at  time.Time
	}
	// masterIPCache 是主节点自己的 IP（域名解析检查用）。
	masterIPCache struct {
		mu  sync.Mutex
		ips []string
		at  time.Time
	}
	// host 是主节点自己的网卡速率和转发并发（主节点转发上限，设计 10.5）。
	host hostLoad

	// generalCache 是选号热路径上通用配置的短缓存。
	generalCache struct {
		mu    sync.Mutex
		value GeneralConfig
		at    time.Time
	}
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
	// keyAssigner 给新建的 API Key 选节点（设计 10.2）。
	keyAssigner *KeyAssigner
	// heartbeats 记各节点的心跳、在线状态、负载（设计 11.4）。
	heartbeats *Heartbeats
	// logs 把后台的日志和记录查询转给从节点（设计第 12 节）。
	logs *LogQuerier
	// users 给小白端用户分配节点（设计 10.1）。
	users *userAssigner
	// health 是外部探测、域名解析检查、错误率降级（设计 10.3）。
	health *HealthMonitor
	// handoffKey 是"交给主节点转发"的标记密钥（handoff.go）。
	handoffKey []byte
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

// BroadcastFlaggedHashes 把命中过的输入名单的变化推给所有在线从节点（设计 3.4）；没在运行时不做。
// 离线的节点重连后会整份重新拉取。
func (r *Runtime) BroadcastFlaggedHashes(change *relayv1.FlaggedHashes) {
	r.mu.Lock()
	running := r.running
	r.mu.Unlock()
	if running == nil || change == nil {
		return
	}
	running.events.Broadcast(&relayv1.MasterEnvelope{Body: &relayv1.MasterEnvelope_FlaggedHashes{FlaggedHashes: change}})
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
	transport.ClassTasks:      {RatePerSecond: 200, Burst: 400, MaxInflight: 32},
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
	for name, provide := range r.deps.SealedSections {
		publisher.RegisterSealedSection(name, provide)
	}
	handoffKey, err := newHandoffKey()
	if err != nil {
		return nil, fmt.Errorf("generate relay hand-off key: %w", err)
	}
	publisher.RegisterSealedSection(SealedSectionHandoff, handoffSection(handoffKey))
	publisher.SetEncryptionKeys(nodes.EncryptionKey)
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
	heartbeats := NewHeartbeats(HeartbeatsOptions{
		Bandwidths: func() map[int64]int {
			out := map[int64]int{}
			if list, err := r.deps.Store.List(context.Background()); err == nil {
				for _, n := range list {
					out[n.ID] = n.BandwidthLimitMbps
				}
			}
			return out
		},
		LoadThreshold: func() float64 {
			return float64(r.cachedGeneralConfig(context.Background()).WithDefaults().LoadThresholdPercent) / 100
		},
		Now: func() time.Time { return r.now() }, Notifier: r.deps.Notifier, Sink: r.deps.Metrics,
		OfflineAfter: func() time.Duration {
			return time.Duration(r.cachedGeneralConfig(context.Background()).WithDefaults().OfflineAfterSeconds) * time.Second
		},
	})
	control.AttachHeartbeats(heartbeats, func(ctx context.Context, nodeID int64) (string, bool) {
		version, _ := publisher.VersionFor(ctx, nodeID)
		return version, nodes.Status(nodeID) == NodeDraining
	})
	health := NewHealthMonitor(HealthDeps{
		Nodes: r.deps.Store.List, Config: r.cachedGeneralConfig, Heartbeats: heartbeats, Notifier: r.deps.Notifier,
		Now: func() time.Time { return r.now() }, Probe: r.deps.Probe, Lookup: r.deps.Lookup, MasterIPs: r.masterIPs,
	})
	if quotas != nil {
		control.AttachQuotas(quotas, recaller, server.Epoch())
	}
	var selector Selector
	if r.deps.NewSelector != nil {
		selector = r.deps.NewSelector(SelectEnv{
			Epoch:       server.Epoch(),
			OnlineNodes: func() int { return len(events.ConnectedNodes()) },
			Quotas:      quotas,
			IssueVoucher: func(v *relayv1.Voucher) ([]byte, *relayv1.Voucher, error) {
				return sign.IssueVoucher(signing.currentVoucherSigner(), v, r.now())
			},
			NodeEncryptionKey: nodes.EncryptionKey,
			ConfigVersion:     publisher.VersionFor,
			VerifyVoucher:     r.VerifyVoucher,
			VerifyTicket:      r.VerifyTicket,
			GeneralConfig:     r.cachedGeneralConfig,
			NodeAddress:       r.nodeAddress,
			NoteStale:         heartbeats.NoteStale,
		})
		control.AttachSelector(selector, server.Epoch())
		RouteNodeEvents(events, selector)
	}
	relayv1.RegisterRelayControlServer(server.GRPC(), control)
	relayv1.RegisterRelayTasksServer(server.GRPC(), NewTasksServer(control))
	if r.deps.NewSettler != nil {
		env := SettleEnv{VerifyVoucher: r.VerifyVoucher, LastSuspectRevocation: r.deps.Store.LastSuspectRevocation}
		if quotas != nil {
			env.RefreshUser = quotas.RefreshUser
		}
		billing := NewBilling(r.deps.NewSettler(env))
		billing.OnSettled = heartbeats.NoteSettled
		relayv1.RegisterRelayBillingServer(server.GRPC(), billing)
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
	running := &runningRelay{server: server, listener: lis, nodes: nodes, publisher: publisher, events: events, invalidator: invalidator, cancel: cancel, ca: ca, keys: keys, signing: signing, revoker: revoker, quotas: quotas, recaller: recaller, quotaEvents: qEvents, selector: selector, handoffKey: handoffKey, heartbeats: heartbeats, health: health, logs: NewLogQuerier(events)}
	var unsubs []func()
	if r.deps.Hub != nil {
		unsubs = append(unsubs, r.deps.Hub.Subscribe(publisher.OnSettingsChanged))
	}
	if r.deps.APIKeys != nil {
		r.deps.APIKeys.SetAuthCacheInvalidationListener(invalidator.APIKeyHash)
		unsubs = append(unsubs, func() { r.deps.APIKeys.SetAuthCacheInvalidationListener(nil) })
		// 新建的 Key 从此按分配规则定节点；开关关闭时摘下，Key 保持未分配。
		running.keyAssigner = NewKeyAssigner(KeyAssignerDeps{
			Nodes: r.deps.Store.List, Online: heartbeats.OnlineNodes, Config: r.cachedGeneralConfig,
			Stats: r.deps.APIKeys.RelayKeyStats, Now: func() time.Time { return r.now() }, Load: heartbeats.Load, Assignable: health.Assignable,
		})
		r.deps.APIKeys.SetRelayKeyAssigner(running.keyAssigner)
		unsubs = append(unsubs, func() { r.deps.APIKeys.SetRelayKeyAssigner(nil) })
	}
	// Key 的接入地址按分配的节点现取（导出配置、地址查询接口）。
	r.invalidateAddresses()
	service.SetRelayAddressResolver(r)
	unsubs = append(unsubs, func() { service.SetRelayAddressResolver(nil) })
	// 小白端用户分配（登录后和定期询问，设计 10.9）。
	assignments := r.deps.UserAssignments
	if assignments == nil {
		assignments = NewMemoryUserAssignments()
	}
	running.users = newUserAssigner(r, assignments)
	service.SetRelayUserAssigner(r)
	unsubs = append(unsubs, func() { service.SetRelayUserAssigner(nil) })
	running.goRun(func() { r.host.run(runCtx) })
	// 主节点分配比例为 0 时主节点不转发 API Key 请求（设计 10.5）；从节点交来的请求带标记，照常处理。
	middleware.SetRelayMasterGate(r)
	unsubs = append(unsubs, func() { middleware.SetRelayMasterGate(nil) })
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
	running.goRun(func() { heartbeats.Run(runCtx) })
	if addr := strings.TrimSpace(cfg.Relay.DomainAskAddr); addr != "" {
		ask, err := r.startDomainAsk(addr)
		if err != nil {
			slog.Warn("relay domain ask endpoint did not start", "addr", addr, "error", err)
		} else {
			unsubs = append(unsubs, func() { _ = ask.Close() })
		}
	}
	running.goRun(func() { health.Run(runCtx) })
	if runner, ok := r.deps.Notifier.(interface{ Run(context.Context) }); ok {
		// 通知按合并窗口定时发出（同时发生的合成一条）。
		running.goRun(func() { runner.Run(runCtx) })
	}
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

// Notifier 返回通知器（管理页配置飞书渠道用；没有时为 nil）。
func (r *Runtime) Notifier() Notifier { return r.deps.Notifier }

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
	defer func() {
		// 主节点分配比例被修改（设计 13）：影响到哪些分配、哪些 Key，管理页的影响清单另有接口。
		if after, err := r.GeneralConfig(ctx); err == nil && *before.MasterRatioPercent != *after.MasterRatioPercent && r.deps.Notifier != nil {
			r.deps.Notifier.Notify(ctx, Event{Kind: EventMasterRatioChanged, Severity: SeverityInfo, Detail: map[string]any{
				"from_percent": *before.MasterRatioPercent, "to_percent": *after.MasterRatioPercent}})
		}
	}()
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
	r.invalidateAddresses()
	r.domains.mu.Lock()
	r.domains.set = nil
	r.domains.mu.Unlock()
	rr.publisher.Trigger()
	return nil
}

// ErrDomainMismatch：填写的对外域名当前没有解析到这台的注册 IP（设计 11.1 第 5 步）。管理员确认后可以带 ignoreDNS 再激活。
var ErrDomainMismatch = errors.New("the domain does not currently resolve to the IP this node registered from")

// ActivateNode 核对指纹后激活待激活节点（设计 11.2）。激活前检查域名当前解析到这台的注册 IP：不一致时返回 ErrDomainMismatch，
// 管理员确认继续（ignoreDNS）才激活（比如解析还没生效、先激活再改解析）。
func (r *Runtime) ActivateNode(ctx context.Context, nodeID int64, fingerprint string, a Activation, ignoreDNS bool) error {
	if !ignoreDNS {
		check, err := r.CheckNodeDomain(ctx, nodeID, a.PublicDomain)
		if err != nil {
			return err
		}
		if check.State != DomainNode {
			return fmt.Errorf("%w (resolves to: %s)", ErrDomainMismatch, strings.Join(check.Resolved, ", "))
		}
	}
	return r.nodeOp(func(n *Nodes) error { return n.Activate(ctx, nodeID, fingerprint, a) })
}

// CheckNodeDomain 检查一个域名当前是否解析到这台节点的注册 IP（管理页填域名时和激活前用）。
func (r *Runtime) CheckNodeDomain(ctx context.Context, nodeID int64, domain string) (DomainCheck, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return DomainCheck{}, err
	}
	n, err := r.deps.Store.GetByID(ctx, nodeID)
	if err != nil {
		return DomainCheck{}, err
	}
	return rr.health.CheckDomain(ctx, strings.ToLower(strings.TrimSpace(domain)), nodeIP(n)), nil
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
	r.forgetHeartbeats(nodeID)
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
	r.forgetHeartbeats(nodeID)
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

// generalConfigCacheTTL：选号热路径上读通用配置的缓存时间（改配置后最多这么久生效；节点规则还随配置快照下发给从节点）。
const generalConfigCacheTTL = 5 * time.Second

// cachedGeneralConfig 是选号用的通用配置读取：短缓存，读不到时沿用上一次的（从没读到过是默认值）。
func (r *Runtime) cachedGeneralConfig(ctx context.Context) GeneralConfig {
	r.generalCache.mu.Lock()
	defer r.generalCache.mu.Unlock()
	if !r.generalCache.at.IsZero() && r.now().Sub(r.generalCache.at) < generalConfigCacheTTL {
		return r.generalCache.value
	}
	g, err := LoadGeneralConfig(ctx, r.deps.Settings)
	if err != nil {
		if r.generalCache.at.IsZero() {
			return GeneralConfig{}.WithDefaults()
		}
		return r.generalCache.value
	}
	r.generalCache.value, r.generalCache.at = g, r.now()
	return g
}

// IsNodeDomain 实现 middleware.RelayMasterGate：这个 Host 是不是已登记的从节点域名（Caddy 的 ask 接口和 Host 限制都用它）。
func (r *Runtime) IsNodeDomain(host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	if _, err := r.runningRelay(); err != nil {
		return false
	}
	r.domains.mu.Lock()
	defer r.domains.mu.Unlock()
	if r.domains.set == nil || r.now().Sub(r.domains.at) > 10*time.Second {
		set := map[string]struct{}{}
		if nodes, err := r.deps.Store.List(context.Background()); err == nil {
			for _, n := range nodes {
				if n.Status.Serving() && n.PublicDomain != "" {
					set[strings.ToLower(n.PublicDomain)] = struct{}{}
				}
			}
			r.domains.set, r.domains.at = set, r.now()
		} else if r.domains.set == nil {
			return false
		}
	}
	_, ok := r.domains.set[host]
	return ok
}

// normalizeHost 去掉端口和末尾的点并转小写。
func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

// masterIPs 是主节点自己的地址（api_base_url 的主机解析出来的 IP，缓存 1 分钟）：域名解析到它说明管理员手动切到了主节点（设计 10.3）。
func (r *Runtime) masterIPs(ctx context.Context) []string {
	r.masterIPCache.mu.Lock()
	defer r.masterIPCache.mu.Unlock()
	if r.now().Sub(r.masterIPCache.at) < time.Minute && r.masterIPCache.ips != nil {
		return r.masterIPCache.ips
	}
	v, err := r.deps.Settings.GetValue(ctx, service.SettingKeyAPIBaseURL)
	if err != nil || strings.TrimSpace(v) == "" {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || u.Hostname() == "" {
		return nil
	}
	lookup := r.deps.Lookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, host)
		}
	}
	rctx, cancel := context.WithTimeout(ctx, domainResolveTimeout)
	defer cancel()
	ips, err := lookup(rctx, u.Hostname())
	if err != nil {
		return r.masterIPCache.ips
	}
	r.masterIPCache.ips, r.masterIPCache.at = ips, r.now()
	return ips
}

// forgetHeartbeats 停用或吊销之后忘掉这台的心跳状态（它不是掉线，不发离线通知）。
func (r *Runtime) forgetHeartbeats(nodeID int64) {
	if rr, err := r.runningRelay(); err == nil {
		if rr.heartbeats != nil {
			rr.heartbeats.Forget(nodeID)
		}
		if rr.health != nil {
			rr.health.Forget(nodeID)
		}
	}
}

// NodeHealths 返回各节点的实时状态（心跳、在线、负载、时钟偏差、对账差额，管理页）。
func (r *Runtime) NodeHealths(ctx context.Context) ([]NodeHealth, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return nil, err
	}
	nodes, err := r.deps.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NodeHealth, 0, len(nodes))
	for _, n := range nodes {
		if !n.Status.Serving() {
			continue
		}
		health := rr.heartbeats.Health(n.ID, n.BandwidthLimitMbps)
		health.External = rr.health.State(n.ID)
		out = append(out, health)
	}
	return out, nil
}

// QueryNodeLogs 把日志和记录查询转给节点并收集结果（设计 12.3）：nodeIDs 为空时问全部在服务的节点，并发问、每台有超时；
// 离线、超时、出错的节点在结果里标出，不影响别的节点。结果只返回、不入库。
func (r *Runtime) QueryNodeLogs(ctx context.Context, nodeIDs []int64, template *relayv1.LogQuery) ([]NodeLogResult, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return nil, err
	}
	nodes, err := r.deps.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*Node{}
	var targets []*Node
	for _, n := range nodes {
		byID[n.ID] = n
		if n.Status.Serving() {
			targets = append(targets, n)
		}
	}
	if len(nodeIDs) > 0 {
		targets = targets[:0]
		for _, id := range nodeIDs {
			n, ok := byID[id]
			if !ok {
				return nil, ErrNodeNotFound
			}
			targets = append(targets, n)
		}
	}
	results := make([]NodeLogResult, len(targets))
	var wg sync.WaitGroup
	for i, n := range targets {
		i, n := i, n
		wg.Add(1)
		go func() {
			defer wg.Done()
			qctx, cancel := context.WithTimeout(ctx, LogQueryTimeout)
			defer cancel()
			res, err := rr.logs.Query(qctx, n.ID, proto.Clone(template).(*relayv1.LogQuery))
			switch {
			case err == nil:
				results[i] = *res
			case errors.Is(err, ErrLogQueryNodeUnavailable):
				results[i] = NodeLogResult{NodeID: n.ID, Status: LogStatusOffline, Records: []json.RawMessage{}}
			case errors.Is(err, context.DeadlineExceeded):
				results[i] = NodeLogResult{NodeID: n.ID, Status: LogStatusTimeout, Records: []json.RawMessage{}}
			default:
				results[i] = NodeLogResult{NodeID: n.ID, Status: LogStatusError, Error: "query failed", Records: []json.RawMessage{}}
			}
			results[i].NodeName = n.Name
		}()
	}
	wg.Wait()
	return results, nil
}
