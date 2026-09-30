package relayselect

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/relay/accountcodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodegw"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodestore"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 端到端：客户端请求打到从节点，经真实 TLS 连接向主节点准入、选号，转发到假上游，响应原样回给客户端；
// 扣费记录经扣费连接送到主节点，释放带回 response id，主节点记下归属后续链能通过。

type recordingSettler struct {
	mu   sync.Mutex
	recs []*relayv1.UsageRecord
}

func (s *recordingSettler) Settle(_ context.Context, _ int64, rec *relayv1.UsageRecord) *relayv1.UsageRecordResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, rec)
	return &relayv1.UsageRecordResult{Seq: rec.GetSeq(), Status: relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED}
}

func (s *recordingSettler) records() []*relayv1.UsageRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*relayv1.UsageRecord(nil), s.recs...)
}

// plainUpstream 是测试用的上游 HTTP（不走代理、不做 TLS 指纹）。发往固定地址（如 OAuth 的 chatgpt.com）的请求
// 改写到测试上游。
type plainUpstream struct{ target *url.URL }

func (u plainUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if u.target != nil && req.URL.Host != u.target.Host {
		req.URL.Scheme, req.URL.Host = u.target.Scheme, u.target.Host
	}
	return http.DefaultTransport.RoundTrip(req)
}

func (u plainUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

type e2e struct {
	world    *world
	settler  *recordingSettler
	gateway  *httptest.Server
	upstream *httptest.Server
	hits     chan *http.Request
	// client 是从节点到主节点的连接（端到端测试直接用它调主节点）。
	client *transport.Client
	nodeID int64
	// moderation 是从节点上的安全审计；records 是从节点本机的记录存储。
	moderation *nodegw.Moderation
	// nodeSettings 是从节点的设置服务（配置快照 + 加密下发的部分）；nodeCache 是它背后的快照。
	nodeSettings *service.SettingService
	nodeCache    *node.ConfigCache
	records      *nodestore.Store
}

// testPromptAudit 是端到端测试世界里主节点下发的提示词审计配置（nil 时不下发）。用例用 usePromptAudit 设置。
var testPromptAudit *securityaudit.RelayPromptConfig

func usePromptAudit(t *testing.T, cfg securityaudit.RelayPromptConfig) {
	t.Helper()
	testPromptAudit = &cfg
	t.Cleanup(func() { testPromptAudit = nil })
}

// testMasterSettings 是端到端测试世界里主节点额外的系统设置（在开始之前设；nil 时不加）。用例用 useMasterSettings 设置。
var testMasterSettings map[string]string

func useMasterSettings(t *testing.T, values map[string]string) {
	t.Helper()
	testMasterSettings = values
	t.Cleanup(func() { testMasterSettings = nil })
}

func startE2E(t *testing.T) *e2e {
	t.Helper()
	return startE2EWith(t, func(upstreamURL string) []service.Account {
		return []service.Account{{ID: 1, Name: "one", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 2,
			Credentials: map[string]any{"api_key": "SECRET-one", "base_url": upstreamURL}}}
	})
}

// startE2EWith 同 startE2E，账号由 accounts 给出（参数是假上游地址）。
func startE2EWith(t *testing.T, accounts func(upstreamURL string) []service.Account) *e2e {
	t.Helper()
	return startE2EWithConfig(t, nil, accounts)
}

// startE2EWithConfig 同 startE2EWith；configure 非 nil 时同样改主节点和从节点的配置（如打开 WebSocket）。
func startE2EWithConfig(t *testing.T, configure func(*config.Config), accounts func(upstreamURL string) []service.Account) *e2e {
	t.Helper()
	ctx := context.Background()
	e := &e2e{settler: &recordingSettler{}, hits: make(chan *http.Request, 16)}

	e.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		e.hits <- r
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/status-429") {
			// 账号的 base_url 带这个前缀时上游回 429（换号、自动分组换组的用例）。
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
			return
		}
		if bytes.Contains(body, []byte("cyber-trigger")) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":"cyber_policy","message":"blocked by policy","type":"invalid_request_error"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			_, _ = io.WriteString(w, `{"id":"chatcmpl-e2e","object":"chat.completion","model":"gpt-5",`+
				`"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],`+
				`"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`)
			return
		}
		if bytes.Contains(body, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: response.output_text.delta\n"+
				`data: {"type":"response.output_text.delta","delta":"hello"}`+"\n\n"+
				"event: response.completed\n"+
				`data: {"type":"response.completed","response":{"id":"resp_e2e2","object":"response","status":"completed","model":"gpt-5",`+
				`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],`+
				`"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`+"\n\n")
			return
		}
		_, _ = io.WriteString(w, `{"id":"resp_e2e1","object":"response","status":"completed","model":"gpt-5",`+
			`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],`+
			`"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`)
	}))
	t.Cleanup(e.upstream.Close)

	// ---- 主节点 ----
	kek := make([]byte, keystore.KEKLength)
	_, err := rand.Read(kek)
	require.NoError(t, err)
	keys, err := keystore.Open(t.TempDir(), kek)
	require.NoError(t, err)
	ca, err := master.NewCA(keys)
	require.NoError(t, err)
	store := master.NewMemoryStore()
	// cyber 会话屏蔽打开：从节点把查询键随选号上送（主节点的网关服务没有设置，查屏蔽表时按关闭处理）。
	settings := memSettings{values: map[string]string{service.SettingKeyCyberSessionBlockEnabled: "true"}}
	for k, v := range testMasterSettings {
		settings.values[k] = v
	}
	nodes := master.NewNodes(store, ca, nil, master.NodesOptions{})
	events := master.NewEventHub()
	publisher := master.NewConfigPublisher(settings, store, events, func() master.Trust { return master.Trust{RootFingerprints: ca.RootFingerprints()} })
	n, err := store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp-1", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	require.NoError(t, store.Activate(ctx, n.ID, master.Activation{Name: "tokyo-1", PublicDomain: "r1.example.com", At: time.Now()}))
	require.NoError(t, nodes.Load(ctx))
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	leaf, err := ca.IssueNodeCertificate(n.ID, pub, time.Hour)
	require.NoError(t, err)
	nodeCert := &tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: priv, Leaf: leaf}

	masterCfg := &config.Config{RunMode: config.RunModeSimple}
	if configure != nil {
		configure(masterCfg)
	}
	e.world = newWorldOn(t, masterCfg, 10, n.ID, accounts(e.upstream.URL)...)

	srv, err := transport.NewServer(transport.ServerOptions{
		TLS:        transport.ServerTLSOptions{Certificate: ca.MasterCertificate, Roots: ca.RootPool},
		Authorizer: nodes,
		AdmitConn:  nodes.AdmitConn,
		Policies:   master.EnrollmentPolicies(),
	})
	require.NoError(t, err)
	nodes.AttachRegistry(srv.Registry())
	control := master.NewControl(publisher)
	if testPromptAudit != nil {
		cfg := *testPromptAudit
		publisher.RegisterSealedSection(master.SealedSectionPromptAudit, func(context.Context) ([]byte, error) { return json.Marshal(cfg) })
	}
	// 加密下发的部分（审核配置等）用这台节点的加密公钥封（与上游凭据同一把）。
	publisher.SetEncryptionKeys(func(id int64) (*ecdh.PublicKey, bool) {
		if id != n.ID {
			return nil, false
		}
		return e.world.nodeKey.PublicKey(), true
	})
	control.AttachSelector(e.world.sel, srv.Epoch())
	master.RouteNodeEvents(events, e.world.sel)
	relayv1.RegisterRelayControlServer(srv.GRPC(), control)
	relayv1.RegisterRelayEventsServer(srv.GRPC(), events)
	relayv1.RegisterRelayBillingServer(srv.GRPC(), master.NewBilling(e.settler))
	lis, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	// ---- 从节点 ----
	upstreamURL, err := url.Parse(e.upstream.URL)
	require.NoError(t, err)
	fps := ca.RootFingerprints()
	client, err := transport.NewClient(transport.ClientOptions{
		Address: lis.Addr().String(),
		TLS: transport.ClientTLSOptions{
			PinnedRootFingerprints: func() []string { return fps },
			Certificate:            func() *tls.Certificate { return nodeCert },
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	e.client, e.nodeID = client, n.ID
	nodeCfg := &config.Config{}
	nodeCfg.Gateway.MaxBodySize = 10 << 20
	nodeCfg.Security.URLAllowlist.AllowInsecureHTTP = true // 假上游是 http://127.0.0.1
	if configure != nil {
		configure(nodeCfg)
	}
	cache := node.NewConfigCache()
	cache.SetOpener(func(sealed, aad []byte) ([]byte, error) { return sealbox.Open(e.world.nodeKey, sealed, aad) })
	nodeSettings := service.NewSettingService(cache, nodeCfg)
	e.nodeSettings, e.nodeCache = nodeSettings, cache
	cache.OnSwap(func(*relayv1.ConfigSnapshot) { nodeSettings.InvalidateAll() })
	syncer := node.NewConfigSyncer(cache, client)
	require.NoError(t, syncer.Sync(ctx))

	outbox := node.NewEventOutbox(0)
	selectClient := node.NewSelectClient(client, outbox)
	quota := node.NewLocalQuota(node.SelectionRefiller{Client: selectClient}, time.Now, func() time.Duration { return 0 })
	quotaSync := node.NewQuotaSync(quota, client)
	wal, err := node.OpenUsageWAL(t.TempDir(), node.UsageWALOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = wal.Close() })

	var d *nodegw.Dispatcher
	sender := node.NewUsageSender(wal, node.NewBillingClient(client), node.UsageSenderOptions{
		Interval: 20 * time.Millisecond,
		OnResult: func(rec *relayv1.UsageRecord, res *relayv1.UsageRecordResult) { d.OnUsageResult(rec, res) },
	})
	d = nodegw.NewDispatcher(nodegw.Deps{
		NodeID:  func() int64 { return n.ID },
		Select:  selectClient,
		Quota:   quota,
		Secrets: accountcodec.NewSecretCache(),
		Open:    func(sealed, aad []byte) ([]byte, error) { return sealbox.Open(e.world.nodeKey, sealed, aad) },
		WAL:     wal,
		Kick:    sender.Kick,
		EnsureConfig: func(ctx context.Context, v string) error {
			return syncer.EnsureVersion(ctx, v)
		},
		AfterEpochChange: quotaSync.Report,
	})
	runCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	records, err := nodestore.Open(t.TempDir(), nodestore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = records.Close() })
	e.moderation = nodegw.NewModeration(runCtx, cache, records, client)
	cache.OnSwap(func(*relayv1.ConfigSnapshot) { e.moderation.Service.InvalidateRuntimeSnapshot() })
	e.records = records
	go node.RunEvents(runCtx, client, syncer, node.EventHandlers{
		Outbox: outbox, OnFlaggedHashes: e.moderation.Hashes.Apply, OnConnected: e.moderation.Hashes.RunResyncOnConnect,
	})
	go sender.Run(runCtx)

	h := nodegw.NewOpenAIHandler(nodegw.GatewayDeps{
		Config: nodeCfg, Settings: nodeSettings, HTTPUpstream: plainUpstream{target: upstreamURL}, Dispatcher: d,
		Decider:    node.NewRemoteUpstreamErrorDecider(client),
		Reporter:   node.NewRemoteAccountReporter(outbox),
		Moderation: e.moderation,
	})
	r := nodegw.NewEngine()
	nodegw.RegisterRoutes(r, h, d, nodeCfg)
	e.gateway = httptest.NewServer(r)
	t.Cleanup(e.gateway.Close)
	return e
}

func (e *e2e) post(t *testing.T, path, key, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.gateway.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(out)
}

func TestNodeServesResponsesEndToEnd(t *testing.T) {
	logger.InitBootstrap()
	e := startE2E(t)

	status, body := e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"hi"}`)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "resp_e2e1")
	select {
	case r := <-e.hits:
		require.Equal(t, "Bearer SECRET-one", r.Header.Get("Authorization"), "the node decrypted the upstream key")
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}

	// 扣费记录送到主节点；释放带回 response id，主节点放槽并记下归属。
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	rec := e.settler.records()[0]
	require.NotEmpty(t, rec.GetVoucher())
	var result service.OpenAIForwardResult
	require.NoError(t, json.Unmarshal(rec.GetResultJson(), &result))
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, "/v1/responses", rec.GetInboundEndpoint())
	require.NotEmpty(t, rec.GetRequestPayloadHash())
	e.world.waitReleased(t)

	status, body = e.post(t, "/v1/responses", "sk-b", `{"model":"gpt-5","input":"again","previous_response_id":"resp_e2e1"}`)
	require.Equal(t, http.StatusOK, status, "the same user can continue: the owner was recorded from the release: %s", body)
	status, body = e.post(t, "/v1/responses", "sk-nope", `{"model":"gpt-5","input":"hi"}`)
	require.Equal(t, http.StatusUnauthorized, status)
	require.JSONEq(t, `{"code":"INVALID_API_KEY","message":"Invalid API key"}`, body)
}

func TestNodeServesChatCompletionsEndToEnd(t *testing.T) {
	e := startE2E(t)
	status, body := e.post(t, "/v1/chat/completions", "sk-a", `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "hello")
	<-e.hits
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	rec := e.settler.records()[0]
	require.Equal(t, "/v1/chat/completions", rec.GetInboundEndpoint())
	require.Empty(t, rec.GetRequestPayloadHash(), "chat usage has no payload hash, like a single server")
	var result service.OpenAIForwardResult
	require.NoError(t, json.Unmarshal(rec.GetResultJson(), &result))
	require.Positive(t, result.Usage.InputTokens)
	e.world.waitReleased(t)
}

func TestNodeServesOpenAIMessagesEndToEnd(t *testing.T) {
	e := startE2E(t)
	body := `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`

	status, out := e.post(t, "/v1/messages", "sk-a", body)
	require.Equal(t, http.StatusForbidden, status, out)
	require.Contains(t, out, `"type":"error"`, "anthropic error shape")

	e.world.keys.keys["sk-a"].Group.AllowMessagesDispatch = true
	status, out = e.post(t, "/v1/messages", "sk-a", body)
	require.Equal(t, http.StatusOK, status, out)
	require.Contains(t, out, "hello")
	r := <-e.hits
	require.Equal(t, "Bearer SECRET-one", r.Header.Get("Authorization"))
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	rec := e.settler.records()[0]
	require.Equal(t, "/v1/messages", rec.GetInboundEndpoint())
	require.NotEmpty(t, rec.GetRequestPayloadHash())
	var result service.OpenAIForwardResult
	require.NoError(t, json.Unmarshal(rec.GetResultJson(), &result))
	require.Positive(t, result.Usage.InputTokens)
	e.world.waitReleased(t)
}

// 影子 OAuth 账号：凭据取自母账号，母账号随选号下发（从节点没有账号仓储）。
func TestNodeServesAShadowOAuthAccount(t *testing.T) {
	parentID := int64(2)
	e := startE2EWith(t, func(string) []service.Account {
		return []service.Account{
			{ID: 1, Name: "shadow", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive,
				Schedulable: true, Concurrency: 2, ParentAccountID: &parentID, Credentials: map[string]any{}},
			{ID: 2, Name: "parent", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive,
				Schedulable: false, Concurrency: 2, Credentials: map[string]any{"access_token": "PARENT-TOKEN", "chatgpt_account_id": "acct-parent"}},
		}
	})
	status, out := e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"hi"}`)
	require.Equal(t, http.StatusOK, status, out)
	r := <-e.hits
	require.Equal(t, "Bearer PARENT-TOKEN", r.Header.Get("Authorization"), "the shadow forwards with its parent's token")
	require.Equal(t, "acct-parent", r.Header.Get("chatgpt-account-id"))
	e.world.waitReleased(t)
}

// 上游判定 cyber 策略：客户端照单机收到上游的错误；主节点按这次选号记下风控记录（归属取自选号记录、
// 屏蔽键由选号时的查询键推导），用量行经扣费队列按 cyber 口径送到主节点；报告发完才释放。
func TestNodeReportsCyberPolicyHitsToTheMaster(t *testing.T) {
	e := startE2E(t)
	hits := make(chan recordedCyber, 4)
	e.world.sel.recordCyber = func(hit handler.CyberPolicyHit, subj handler.CyberPolicySubject, scope string, keys []string) {
		hits <- recordedCyber{hit: hit, subj: subj, blockScope: scope, blockKeys: keys}
	}

	req, err := http.NewRequest(http.MethodPost, e.gateway.URL+"/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"cyber-trigger"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer sk-a")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("session_id", "sess-cyber")
	req.Header.Set("User-Agent", "codex/1.0")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	out, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, string(out))
	require.Contains(t, string(out), "cyber_policy", "the upstream error is passed through like a single server")
	<-e.hits

	var got recordedCyber
	select {
	case got = <-hits:
	case <-time.After(5 * time.Second):
		t.Fatal("the master did not record the cyber hit")
	}
	require.Equal(t, "sk-a", got.subj.APIKey.Key)
	require.Equal(t, int64(1), got.subj.Account.ID)
	require.NotNil(t, got.subj.NodeID)
	require.Equal(t, "blocked by policy", got.hit.Mark.Message)
	require.Equal(t, http.StatusBadRequest, got.hit.Mark.UpstreamStatus)
	require.Equal(t, "gpt-5", got.hit.Model)
	require.Equal(t, "/v1/responses", got.hit.InboundEndpoint)
	require.Equal(t, "codex/1.0", got.hit.UserAgent)
	require.NotEmpty(t, got.blockKeys, "block keys derived from the lookup sent at selection")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session_id", "sess-cyber")
	require.Equal(t, service.CyberSessionExplicitBlockKey(11, c, []byte(`{"model":"gpt-5","input":"cyber-trigger"}`)), got.blockKeys[0])

	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	rec := e.settler.records()[0]
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI_CYBER_POLICY, rec.GetKind())
	require.True(t, rec.GetCyberBlocked())
	var result service.OpenAIForwardResult
	require.NoError(t, json.Unmarshal(rec.GetResultJson(), &result))
	require.Equal(t, "gpt-5", result.Model)
	require.Zero(t, result.Usage.InputTokens)
	e.world.waitReleased(t)

	// Chat Completions 与 Messages 入口同样上报（各自按入口格式把错误写给客户端）。
	e.world.keys.keys["sk-a"].Group.AllowMessagesDispatch = true
	for i, tc := range []struct{ path, body, endpoint string }{
		{"/v1/chat/completions", `{"model":"gpt-5","messages":[{"role":"user","content":"cyber-trigger"}]}`, "/v1/chat/completions"},
		{"/v1/messages", `{"model":"gpt-5","max_tokens":64,"messages":[{"role":"user","content":"cyber-trigger"}]}`, "/v1/messages"},
	} {
		status, out := e.post(t, tc.path, "sk-a", tc.body)
		require.NotEqual(t, http.StatusOK, status, "%s: %s", tc.path, out)
		<-e.hits
		select {
		case got = <-hits:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the master did not record the cyber hit", tc.path)
		}
		require.Equal(t, tc.endpoint, got.hit.InboundEndpoint)
		require.Equal(t, "blocked by policy", got.hit.Mark.Message)
		require.Equal(t, int64(1), got.subj.Account.ID)
		require.Eventually(t, func() bool { return len(e.settler.records()) == i+2 }, 5*time.Second, 20*time.Millisecond, tc.path)
		rec := e.settler.records()[i+1]
		require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI_CYBER_POLICY, rec.GetKind(), tc.path)
		require.Equal(t, tc.endpoint, rec.GetInboundEndpoint())
		e.world.waitReleased(t)
	}
}
