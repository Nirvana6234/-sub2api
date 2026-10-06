package node_test

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// ---- 测试用的主节点 ----

type testMaster struct {
	store     *master.MemoryStore
	settings  *memSettings
	repo      service.SettingRepository // 带写入通知
	ca        *master.CA
	nodes     *master.Nodes
	events    *master.EventHub
	publisher *master.ConfigPublisher
	invalid   *master.Invalidator
	control   *master.Control
	srv       *transport.Server
	addr      string
	nodeID    int64
	nodeCert  *tls.Certificate
	// nodeEncKey：这台节点的加密私钥（加密下发的部分用它的公钥封）。
	nodeEncKey *ecdh.PrivateKey
	// settler：扣费服务背后的入账，测试替换。
	settler settlerSlot
}

// settlerSlot 让测试在服务开始之后再挂上入账实现（gRPC 服务要在开始服务前注册）。
type settlerSlot struct {
	mu sync.Mutex
	s  master.Settler
}

func (s *settlerSlot) set(v master.Settler) {
	s.mu.Lock()
	s.s = v
	s.mu.Unlock()
}

func (s *settlerSlot) Settle(ctx context.Context, nodeID int64, rec *relayv1.UsageRecord) *relayv1.UsageRecordResult {
	s.mu.Lock()
	inner := s.s
	s.mu.Unlock()
	if inner == nil {
		return &relayv1.UsageRecordResult{Seq: rec.GetSeq(), Status: relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_RETRY}
	}
	return inner.Settle(ctx, nodeID, rec)
}

func startMaster(t *testing.T) *testMaster {
	t.Helper()
	ctx := context.Background()
	kek := make([]byte, keystore.KEKLength)
	_, err := rand.Read(kek)
	require.NoError(t, err)
	keys, err := keystore.Open(t.TempDir(), kek)
	require.NoError(t, err)
	ca, err := master.NewCA(keys)
	require.NoError(t, err)

	m := &testMaster{store: master.NewMemoryStore(), ca: ca, settings: &memSettings{values: map[string]string{
		service.SettingKeyMinClaudeCodeVersion: "1.0.0",
		// 与转发无关的密钥：绝不能出现在快照里。
		service.SettingKeyAdminAPIKey: "admin-secret",
		// 转发要用的密钥：只在按节点加密的部分里。
		service.SettingKeyWebSearchEmulationConfig: `{"providers":[{"api_key":"sk-search-secret"}]}`,
	}}}
	hub := service.NewSettingChangeHub()
	m.repo = service.NewObservedSettingRepository(m.settings, hub)

	m.nodes = master.NewNodes(m.store, ca, nil, master.NodesOptions{})
	m.events = master.NewEventHub()
	m.publisher = master.NewConfigPublisher(m.repo, m.store, m.events, func() master.Trust { return master.Trust{RootFingerprints: ca.RootFingerprints()} })
	m.invalid = master.NewInvalidator(m.events)
	hub.Subscribe(m.publisher.OnSettingsChanged)
	m.events.OnConnect = func(nodeID int64) { m.publisher.NotifyNode(context.Background(), nodeID) }

	// 一台已激活的节点和它的证书。
	n, err := m.store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp-1", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	require.NoError(t, m.store.Activate(ctx, n.ID, master.Activation{Name: "tokyo-1", PublicDomain: "r1.example.com", At: time.Now()}))
	require.NoError(t, m.nodes.Load(ctx))
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	leaf, err := ca.IssueNodeCertificate(n.ID, pub, time.Hour)
	require.NoError(t, err)
	m.nodeID = n.ID
	m.nodeCert = &tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: priv, Leaf: leaf}
	m.nodeEncKey, err = sealbox.GenerateKey()
	require.NoError(t, err)
	m.publisher.SetEncryptionKeys(func(id int64) (*ecdh.PublicKey, bool) {
		if id != m.nodeID {
			return nil, false
		}
		return m.nodeEncKey.PublicKey(), true
	})

	srv, err := transport.NewServer(transport.ServerOptions{
		TLS:        transport.ServerTLSOptions{Certificate: ca.MasterCertificate, Roots: ca.RootPool},
		Authorizer: m.nodes,
		AdmitConn:  m.nodes.AdmitConn,
		Policies:   master.EnrollmentPolicies(),
	})
	require.NoError(t, err)
	m.nodes.AttachRegistry(srv.Registry())
	m.control = master.NewControl(m.publisher)
	relayv1.RegisterRelayControlServer(srv.GRPC(), m.control)
	relayv1.RegisterRelayEventsServer(srv.GRPC(), m.events)
	relayv1.RegisterRelayBillingServer(srv.GRPC(), master.NewBilling(&m.settler))
	lis, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	m.srv, m.addr = srv, lis.Addr().String()
	return m
}

// ---- 测试用的从节点 ----

type testNode struct {
	client   *transport.Client
	cache    *node.ConfigCache
	syncer   *node.ConfigSyncer
	settings *service.SettingService
}

func startNode(t *testing.T, m *testMaster) *testNode {
	t.Helper()
	fps := m.ca.RootFingerprints()
	client, err := transport.NewClient(transport.ClientOptions{
		Address: m.addr,
		TLS: transport.ClientTLSOptions{
			PinnedRootFingerprints: func() []string { return fps },
			Certificate:            func() *tls.Certificate { return m.nodeCert },
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	cache := node.NewConfigCache()
	cache.SetOpener(func(sealed, aad []byte) ([]byte, error) { return sealbox.Open(m.nodeEncKey, sealed, aad) })
	settings := service.NewSettingService(cache, &config.Config{})
	cache.OnSwap(func(*relayv1.ConfigSnapshot) { settings.InvalidateAll() })
	return &testNode{client: client, cache: cache, syncer: node.NewConfigSyncer(cache, client), settings: settings}
}

// ---- 用例 ----

func TestNodeGetsOnlyWhitelistedSettings(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	ctx := context.Background()

	_, err := n.settings.GetWebSearchEmulationConfig(ctx)
	require.Error(t, err, "no configuration yet: the node must not serve")
	require.False(t, n.cache.Ready())

	require.NoError(t, n.syncer.Sync(ctx))
	require.True(t, n.cache.Ready())
	minV, _ := n.settings.GetClaudeCodeVersionBounds(ctx)
	require.Equal(t, "1.0.0", minV)

	_, err = n.cache.GetValue(ctx, service.SettingKeyAdminAPIKey)
	require.ErrorIs(t, err, service.ErrSettingNotFound, "settings outside the whitelist read as absent")
	all, err := n.cache.GetAll(ctx)
	require.NoError(t, err)
	for k, v := range all {
		require.NotContains(t, v, "admin-secret", k)
	}
	// 转发要用的密钥经加密部分下发，节点解开后照常读到（设计 6 第二类）。
	search, err := n.cache.GetValue(ctx, service.SettingKeyWebSearchEmulationConfig)
	require.NoError(t, err)
	require.Contains(t, search, "sk-search-secret")

	nc, ok := n.cache.Node()
	require.True(t, ok)
	require.Equal(t, m.nodeID, nc.NodeID)
	require.Equal(t, "r1.example.com", nc.PublicDomain)
	require.Equal(t, 10, *nc.General.MasterRatioPercent, "defaults are filled in")
	require.Equal(t, m.ca.RootFingerprints(), n.cache.RootFingerprints())

	require.ErrorIs(t, n.cache.Set(ctx, "x", "y"), node.ErrReadOnlySettings)
}

func TestSettingChangeIsPushedAndSeenImmediately(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go node.RunEvents(ctx, n.client, n.syncer, node.EventHandlers{})
	require.Eventually(t, n.cache.Ready, 5*time.Second, 10*time.Millisecond)
	first := n.cache.Version()

	minV, _ := n.settings.GetClaudeCodeVersionBounds(ctx)
	require.Equal(t, "1.0.0", minV)

	require.NoError(t, m.repo.Set(ctx, service.SettingKeyMinClaudeCodeVersion, "2.0.0"))
	require.Eventually(t, func() bool { return n.cache.Version() != first }, 5*time.Second, 10*time.Millisecond)
	minV, _ = n.settings.GetClaudeCodeVersionBounds(ctx)
	require.Equal(t, "2.0.0", minV, "the swap invalidates the node's setting caches")

	// 加密下发的设置改了同样当场推送。
	second := n.cache.Version()
	require.NoError(t, m.repo.Set(ctx, service.SettingKeyWebSearchEmulationConfig, `{"providers":[{"api_key":"sk-rotated"}]}`))
	require.Eventually(t, func() bool { return n.cache.Version() != second }, 5*time.Second, 10*time.Millisecond)
	search, err := n.cache.GetValue(ctx, service.SettingKeyWebSearchEmulationConfig)
	require.NoError(t, err)
	require.Contains(t, search, "sk-rotated")

	// 不下发的设置改了不推送（版本不变）。
	third := n.cache.Version()
	require.NoError(t, m.repo.Set(ctx, service.SettingKeyAdminAPIKey, "admin-rotated"))
	time.Sleep(500 * time.Millisecond)
	require.Equal(t, third, n.cache.Version())
}

// 解不开加密部分（密钥不对）时不换快照：节点不带着缺了密钥的配置转发。
func TestSealedConfigNeedsTheNodesKey(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	wrong, err := sealbox.GenerateKey()
	require.NoError(t, err)
	n.cache.SetOpener(func(sealed, aad []byte) ([]byte, error) { return sealbox.Open(wrong, sealed, aad) })
	require.Error(t, n.syncer.Sync(context.Background()))
	require.False(t, n.cache.Ready())
}

func TestVersionFenceFetchesBeforeForwardingAndFailsClosed(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	ctx := context.Background()
	require.NoError(t, n.syncer.Sync(ctx))

	// 绕开设置仓储直接改库，再重新生成（定时复查会做这件事）：没有推送，但选号返回的版本变了。
	m.settings.set(service.SettingKeyMinClaudeCodeVersion, "3.0.0")
	require.NoError(t, m.publisher.Rebuild(ctx))
	want, err := m.publisher.VersionFor(ctx, m.nodeID)
	require.NoError(t, err)
	require.NotEqual(t, want, n.cache.Version())

	require.NoError(t, n.syncer.EnsureVersion(ctx, want))
	require.Equal(t, want, n.cache.Version())
	minV, _ := n.settings.GetClaudeCodeVersionBounds(ctx)
	require.Equal(t, "3.0.0", minV)

	// 已是最新：不再拉取。
	require.NoError(t, n.syncer.EnsureVersion(ctx, want))

	// 主节点不可达、版本又落后：报错（这个请求返回 503），不用旧配置转发。
	m.srv.Stop()
	require.Error(t, n.syncer.EnsureVersion(ctx, "some-newer-version"))
}

func TestUnchangedFetchReturnsOnlyTheVersion(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, n.syncer.Sync(ctx))
	resp, err := relayv1.NewRelayControlClient(n.client.Conn(transport.TierControl)).FetchConfig(ctx, &relayv1.FetchConfigRequest{KnownVersion: n.cache.Version()})
	require.NoError(t, err)
	require.True(t, resp.Unchanged)
	require.Empty(t, resp.Settings)
}

func TestInvalidationsReachTheNode(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	var mu sync.Mutex
	var got []*relayv1.Invalidation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go node.RunEvents(ctx, n.client, n.syncer, node.EventHandlers{OnInvalidation: func(inv *relayv1.Invalidation) {
		mu.Lock()
		got = append(got, inv)
		mu.Unlock()
	}})
	require.Eventually(t, func() bool { return len(m.events.ConnectedNodes()) == 1 }, 5*time.Second, 10*time.Millisecond)

	m.invalid.APIKeyHash("hash-a")
	m.invalid.APIKeyHash("hash-b")
	m.invalid.User(42)
	m.invalid.Group(7)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) > 0
	}, 5*time.Second, 10*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 1, "coalesced into one message")
	require.ElementsMatch(t, []string{"hash-a", "hash-b"}, got[0].ApiKeyHashes)
	require.Equal(t, []int64{42}, got[0].UserIds)
	require.Equal(t, []int64{7}, got[0].GroupIds)
}

func TestSecretLookingJSONIsDroppedFromTheSnapshot(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	ctx := context.Background()
	m.settings.set(service.SettingKeyGlobalBlacklist, `[{"ip":"1.2.3.4","api_key":"leak"}]`)
	require.NoError(t, m.publisher.Rebuild(ctx))
	require.NoError(t, n.syncer.Sync(ctx))
	_, err := n.cache.GetValue(ctx, service.SettingKeyGlobalBlacklist)
	require.ErrorIs(t, err, service.ErrSettingNotFound)
}

// ---- 内存设置仓储 ----

type memSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *memSettings) set(k, v string) {
	s.mu.Lock()
	s.values[k] = v
	s.mu.Unlock()
}

func (s *memSettings) Get(_ context.Context, key string) (*service.Setting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return nil, service.ErrSettingNotFound
	}
	return &service.Setting{Key: key, Value: v}, nil
}

func (s *memSettings) GetValue(ctx context.Context, key string) (string, error) {
	st, err := s.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return st.Value, nil
}

func (s *memSettings) Set(_ context.Context, key, value string) error {
	s.set(key, value)
	return nil
}

func (s *memSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (s *memSettings) SetMultiple(_ context.Context, m map[string]string) error {
	for k, v := range m {
		s.set(k, v)
	}
	return nil
}

func (s *memSettings) GetAll(context.Context) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.values {
		out[k] = v
	}
	return out, nil
}

func (s *memSettings) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.values, key)
	s.mu.Unlock()
	return nil
}

// 业务层的用户、分组、订阅、平台配额改动经 AccessChangeHub 推到从节点；
// 订阅和配额按用户作废。
func TestAccessChangesReachTheNode(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	hub := service.NewAccessChangeHub()
	hub.Subscribe(m.invalid.OnAccessChange)

	var mu sync.Mutex
	users, groups := map[int64]bool{}, map[int64]bool{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go node.RunEvents(ctx, n.client, n.syncer, node.EventHandlers{OnInvalidation: func(inv *relayv1.Invalidation) {
		mu.Lock()
		for _, u := range inv.UserIds {
			users[u] = true
		}
		for _, g := range inv.GroupIds {
			groups[g] = true
		}
		mu.Unlock()
	}})
	require.Eventually(t, func() bool { return len(m.events.ConnectedNodes()) == 1 }, 5*time.Second, 10*time.Millisecond)

	hub.Publish(service.AccessChange{Kind: service.AccessChangeUser, UserID: 1})
	hub.Publish(service.AccessChange{Kind: service.AccessChangeGroup, GroupID: 2})
	hub.Publish(service.AccessChange{Kind: service.AccessChangeSubscription, UserID: 3, GroupID: 4})
	hub.Publish(service.AccessChange{Kind: service.AccessChangePlatformQuota, UserID: 5})
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return users[1] && users[3] && users[5] && groups[2]
	}, 5*time.Second, 10*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.False(t, groups[4], "a subscription change invalidates the user, not the whole group")
}

// 票据吊销经事件流到达从节点，合进吊销表后被吊销用户的票据被拒（设计 8.1）。
func TestTicketRevocationsReachTheNode(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	list := sign.NewRevocationList()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go node.RunEvents(ctx, n.client, n.syncer, node.EventHandlers{OnTicketRevocations: func(msg *relayv1.TicketRevocations) {
		list.Apply(msg, time.Now())
	}})
	require.Eventually(t, func() bool { return len(m.events.ConnectedNodes()) == 1 }, 5*time.Second, 10*time.Millisecond)

	at := time.Now()
	m.events.Broadcast(&relayv1.MasterEnvelope{Body: &relayv1.MasterEnvelope_TicketRevocations{TicketRevocations: &relayv1.TicketRevocations{
		Users: []*relayv1.RevokedTicketUser{{UserId: 42, RevokedBeforeUnixMs: at.UnixMilli()}},
	}}})
	require.Eventually(t, func() bool { return list.Revoked(42, at.Add(-time.Minute), time.Now()) }, 5*time.Second, 10*time.Millisecond)
	require.False(t, list.Revoked(43, at.Add(-time.Minute), time.Now()))
}

// 心跳（设计 11.4）：从节点按证书认定身份发心跳，主节点记下并回主节点时间；从节点据此测时钟偏差、知道自己在排空中。
func TestHeartbeatReachesTheMasterAndMeasuresTheClock(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	clock := time.Now()
	hbs := master.NewHeartbeats(master.HeartbeatsOptions{Now: func() time.Time { return clock }})
	draining := false
	m.control.AttachHeartbeats(hbs, func(context.Context, int64) (string, bool) { return "v-test", draining })

	beater := node.NewHeartbeater(n.client, func() *relayv1.HeartbeatRequest {
		return &relayv1.HeartbeatRequest{ProgramVersion: "9.9", StartedAtUnixMs: 42, ClientConnections: 3, RxBytesPerSec: 1000}
	}, 0)
	require.True(t, beater.ClockHealthy(), "not measured yet counts as healthy")
	require.NoError(t, beater.Beat(context.Background()))
	require.True(t, hbs.Online(m.nodeID), "the master recorded the node it knows from the certificate")
	health := hbs.Health(m.nodeID, 0)
	require.Equal(t, "9.9", health.Beat.GetProgramVersion())
	require.EqualValues(t, 3, health.Beat.GetClientConnections())
	require.Less(t, beater.Skew(), 2*time.Second, "same machine: no clock difference")
	require.True(t, beater.ClockHealthy())
	require.False(t, beater.Draining())

	draining = true
	require.NoError(t, beater.Beat(context.Background()))
	require.True(t, beater.Draining())
}

// 日志和记录查询（设计 12.3）：主节点经事件流把查询转给节点，节点本机执行后经同一条流回结果；结果按不可信输入处理
// （限条数、限长度、必须是 JSON 对象）；没有事件连接的节点查询报不可用。
func TestLogQueryTravelsOverTheEventStream(t *testing.T) {
	m := startMaster(t)
	n := startNode(t, m)
	querier := master.NewLogQuerier(m.events)

	// 还没有事件连接：查不了。
	_, err := querier.Query(context.Background(), m.nodeID, &relayv1.LogQuery{Kind: "app"})
	require.ErrorIs(t, err, master.ErrLogQueryNodeUnavailable)

	outbox := node.NewEventOutbox(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	long := `{"ts":1,"message":"` + strings.Repeat("x", 70<<10) + `"}`
	go node.RunEvents(ctx, n.client, n.syncer, node.EventHandlers{Outbox: outbox, OnLogQuery: func(q *relayv1.LogQuery) {
		res := &relayv1.LogQueryResult{QueryId: q.GetQueryId(), Truncated: true}
		switch q.GetKind() {
		case "slow":
			return // 不回：主节点超时
		case "broken":
			res.Error = "log store could not be read"
		default:
			for _, rec := range []string{`{"ts":30,"message":"newest"}`, `not json`, `["array"]`, long, `{"ts":20,"message":"older"}`, `{"ts":10,"message":"oldest"}`} {
				res.Records = append(res.Records, []byte(rec))
			}
		}
		outbox.Enqueue(&relayv1.NodeEnvelope{Body: &relayv1.NodeEnvelope_LogResult{LogResult: res}})
	}})
	require.Eventually(t, func() bool { return m.events.IsConnected(m.nodeID) }, 5*time.Second, 10*time.Millisecond)

	qctx, qcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer qcancel()
	res, err := querier.Query(qctx, m.nodeID, &relayv1.LogQuery{Kind: "app", Limit: 2})
	require.NoError(t, err)
	require.Equal(t, master.LogStatusOK, res.Status)
	require.Len(t, res.Records, 2, "capped at the requested limit")
	require.Equal(t, 3, res.Skipped, "not JSON, not an object, and over-long records are dropped")
	require.True(t, res.Truncated)
	require.JSONEq(t, `{"ts":30,"message":"newest"}`, string(res.Records[0]))

	res, err = querier.Query(qctx, m.nodeID, &relayv1.LogQuery{Kind: "broken"})
	require.NoError(t, err)
	require.Equal(t, master.LogStatusError, res.Status)
	require.Equal(t, "log store could not be read", res.Error)

	short, scancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer scancel()
	_, err = querier.Query(short, m.nodeID, &relayv1.LogQuery{Kind: "slow"})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// 多台节点的结果按时间新到旧合并，每条带节点。
	merged := master.MergeLogResults([]master.NodeLogResult{
		{NodeID: 1, NodeName: "a", Records: []json.RawMessage{json.RawMessage(`{"ts":30}`), json.RawMessage(`{"ts":10}`)}},
		{NodeID: 2, NodeName: "b", Records: []json.RawMessage{json.RawMessage(`{"ts":20}`)}},
	}, 2)
	require.Len(t, merged, 2)
	require.Equal(t, int64(1), merged[0].NodeID)
	require.Equal(t, int64(2), merged[1].NodeID)
}
