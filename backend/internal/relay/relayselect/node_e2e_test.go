package relayselect

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/relay/accountcodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodegw"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
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

// plainUpstream 是测试用的上游 HTTP（不走代理、不做 TLS 指纹）。
type plainUpstream struct{}

func (plainUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return http.DefaultTransport.RoundTrip(req)
}

func (plainUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return http.DefaultTransport.RoundTrip(req)
}

type e2e struct {
	world    *world
	settler  *recordingSettler
	gateway  *httptest.Server
	upstream *httptest.Server
	hits     chan *http.Request
}

func startE2E(t *testing.T) *e2e {
	t.Helper()
	ctx := context.Background()
	e := &e2e{settler: &recordingSettler{}, hits: make(chan *http.Request, 16)}

	e.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		e.hits <- r
		w.Header().Set("Content-Type", "application/json")
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
	settings := memSettings{values: map[string]string{}}
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

	account := service.Account{ID: 1, Name: "one", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 2,
		Credentials: map[string]any{"api_key": "SECRET-one", "base_url": e.upstream.URL}}
	e.world = newWorldOn(t, &config.Config{RunMode: config.RunModeSimple}, 10, n.ID, account)

	srv, err := transport.NewServer(transport.ServerOptions{
		TLS:        transport.ServerTLSOptions{Certificate: ca.MasterCertificate, Roots: ca.RootPool},
		Authorizer: nodes,
		AdmitConn:  nodes.AdmitConn,
		Policies:   master.EnrollmentPolicies(),
	})
	require.NoError(t, err)
	nodes.AttachRegistry(srv.Registry())
	control := master.NewControl(publisher)
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
	nodeCfg := &config.Config{}
	nodeCfg.Gateway.MaxBodySize = 10 << 20
	nodeCfg.Security.URLAllowlist.AllowInsecureHTTP = true // 假上游是 http://127.0.0.1
	cache := node.NewConfigCache()
	nodeSettings := service.NewSettingService(cache, nodeCfg)
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
	go node.RunEvents(runCtx, client, syncer, node.EventHandlers{Outbox: outbox})
	go sender.Run(runCtx)

	h := nodegw.NewOpenAIHandler(nodegw.GatewayDeps{
		Config: nodeCfg, Settings: nodeSettings, HTTPUpstream: plainUpstream{}, Dispatcher: d,
		Decider:  node.NewRemoteUpstreamErrorDecider(client),
		Reporter: node.NewRemoteAccountReporter(outbox),
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
