package relayselect

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// localGemini 是单机上的 Gemini 网关（同一个处理函数，没有从节点）：与从节点那一路对同样的请求逐项比对，守住
// "GeminiV1BetaModels / Messages 的 Gemini 分支改成可经从节点之后，单机行为不变"。
type localGemini struct {
	server *httptest.Server
	cache  *countingSticky
}

func startLocalGemini(t *testing.T, e *e2e, accounts []service.Account) *localGemini {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.MaxBodySize = 10 << 20
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	slots := &countingSlots{}
	concurrency := service.NewConcurrencyService(slots)
	billing := service.NewBillingCacheService(balanceCache{balance: 10}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	cache := &countingSticky{bound: map[string]int64{}}
	identity := &memIdentity{fingerprints: map[int64]*service.Fingerprint{}, masked: map[int64]string{}}
	upstreamURL, err := url.Parse(e.upstream.URL)
	require.NoError(t, err)
	upstream := plainUpstream{target: upstreamURL}
	repo := newRecordingAntigravityRepo(accounts) // 单机转发路径上写账号状态（429 限流）的仓储
	gw := service.NewGatewayService(repo, e.world.groups, nil, nil, nil, nil, nil, cache, cfg,
		nil, concurrency, nil, nil, billing, service.NewIdentityService(identity), upstream, nil,
		service.NewClaudeTokenProvider(nil, seededTokens{}, nil), nil, nil, service.NewDigestSessionStore(), nil, nil, nil, nil, nil, nil, nil)
	compat := service.NewGeminiMessagesCompatService(repo, e.world.groups, cache, nil, service.NewGeminiTokenProvider(nil, seededTokens{}, nil), nil, upstream, nil, cfg)
	apiKeys := service.NewAPIKeyService(touchKeys{e.world.keys}, nil, nil, nil, nil, nil, cfg)
	settings := service.NewSettingService(memSettings{values: map[string]string{}}, cfg)
	h := handler.NewGatewayHandler(gw, nil, compat, nil, nil, concurrency, billing, nil, apiKeys, nil, nil, nil, nil, cfg, settings)

	r := gin.New()
	r.Use(middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize), handler.InboundEndpointMiddleware())
	gemini := r.Group("/v1beta", middleware.APIKeyAuthWithSubscriptionGoogle(apiKeys, nil, cfg))
	gemini.POST("/models/*modelAction", h.GeminiV1BetaModels)
	messages := r.Group("/v1", gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(apiKeys, nil, cfg)))
	messages.POST("/messages", h.Messages)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &localGemini{server: srv, cache: cache}
}

// touchKeys 是单机这一路用的 Key 仓储：鉴权成功后记最后使用时间（测试世界的仓储没有实现）。
type touchKeys struct{ fakeKeys }

func (touchKeys) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }

func (l *localGemini) postGoogle(t *testing.T, path, key, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, l.server.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("x-goog-api-key", key)
	req.Header.Set("Content-Type", "application/json")
	return doRequest(t, req)
}

func (l *localGemini) post(t *testing.T, path, key, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, l.server.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	return doRequest(t, req)
}

func doRequest(t *testing.T, req *http.Request) (int, string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(out)
}

// usedAccount 取上游收到的下一条 Gemini 请求用的账号（按它带的 API Key 认）。
func usedAccount(t *testing.T, e *e2e) string {
	t.Helper()
	hit := waitHit(t, e, func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/v1beta/models/") })
	return hit.Header.Get("x-goog-api-key")
}

func drainHits(e *e2e) {
	for {
		select {
		case <-e.hits:
		default:
			return
		}
	}
}

const (
	geminiTurn1 = `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	geminiTurn2 = `{"contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"text":"hello"}]},{"role":"user","parts":[{"text":"more"}]}]}`
)

// 同样的请求在单机和从节点上得到同样的结果：状态码、响应体、用到的账号；多轮对话没有会话哈希时由内容摘要会话把第二轮
// 绑到第一轮的账号上（两边都是）。
func TestGeminiNativeLocalAndNodeAgree(t *testing.T) {
	useGatewayCache(t, &countingSticky{bound: map[string]int64{}}) // 主节点的粘性会话缓存
	accounts := []service.Account{geminiAccount(1, "first"), geminiAccount(2, "second")}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		for i := range accounts {
			accounts[i].Credentials["base_url"] = upstream
		}
		accounts[0].Priority, accounts[1].Priority = 1, 2
		return accounts
	})
	local := startLocalGemini(t, e, accounts)

	type result struct {
		status          int
		body            string
		turn1, turn2    string
		sessionAccounts []string
	}
	run := func(post func(path, key, body string) (int, string)) result {
		accounts[0].Priority, accounts[1].Priority = 1, 2
		drainHits(e)
		var r result
		r.status, r.body = post(geminiPath, "sk-gemini", geminiTurn1)
		r.turn1 = usedAccount(t, e)
		// 从节点那一路：第一轮的释放消息（带"转发成功"，主节点这时保存摘要会话）是异步送到的；同一段对话的下一轮通常隔几秒才来。
		time.Sleep(300 * time.Millisecond)
		// 第一轮落在 first 上；把优先级对调，没有会话的话第二轮会去 second。
		accounts[0].Priority, accounts[1].Priority = 2, 1
		status, _ := post(geminiPath, "sk-gemini", geminiTurn2)
		require.Equal(t, http.StatusOK, status)
		r.turn2 = usedAccount(t, e)
		return r
	}
	nodeRun := run(func(path, key, body string) (int, string) { return e.postGoogle(t, path, key, body) })
	e.world.waitReleased(t)
	localRun := run(func(path, key, body string) (int, string) { return local.postGoogle(t, path, key, body) })

	require.Equal(t, http.StatusOK, localRun.status, localRun.body)
	require.Equal(t, localRun.status, nodeRun.status)
	require.Equal(t, localRun.body, nodeRun.body, "the same response body")
	require.Equal(t, "SECRET-first", localRun.turn1)
	require.Equal(t, localRun.turn1, nodeRun.turn1)
	require.Equal(t, "SECRET-first", localRun.turn2, "the digest session binds the second turn to the first turn's account")
	require.Equal(t, localRun.turn2, nodeRun.turn2)
}

// 单机和从节点上 Gemini 分组的 /v1/messages（Messages 的 Gemini 分支）结果一致。
func TestGeminiMessagesLocalAndNodeAgree(t *testing.T) {
	accounts := []service.Account{geminiAccount(1, "gem")}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		return accounts
	})
	local := startLocalGemini(t, e, accounts)
	msg := `{"model":"gemini-2.5-pro","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`

	nodeStatus, nodeBody := e.post(t, "/v1/messages", "sk-gemini", msg)
	e.world.waitReleased(t)
	localStatus, localBody := local.post(t, "/v1/messages", "sk-gemini", msg)
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus)
	for _, path := range []string{"content.0.text", "role", "stop_reason", "usage.input_tokens", "usage.output_tokens", "model"} {
		require.Equal(t, gjson.Get(localBody, path).Raw, gjson.Get(nodeBody, path).Raw, path)
	}
}

// 没有可用账号：单机和从节点的状态码、Google 格式的错误体一致。
func TestGeminiNoAccountLocalAndNodeAgree(t *testing.T) {
	e := startStandardE2E(t, func(string) []service.Account { return nil })
	local := startLocalGemini(t, e, nil)
	nodeStatus, nodeBody := e.postGoogle(t, geminiPath, "sk-gemini", geminiTurn1)
	localStatus, localBody := local.postGoogle(t, geminiPath, "sk-gemini", geminiTurn1)
	require.Equal(t, http.StatusServiceUnavailable, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus)
	require.Equal(t, localBody, nodeBody)

	// 白名单不允许、Key 不对也一样。
	nodeStatus, nodeBody = e.postGoogle(t, geminiPath, "sk-nope", geminiTurn1)
	localStatus, localBody = local.postGoogle(t, geminiPath, "sk-nope", geminiTurn1)
	require.Equal(t, localStatus, nodeStatus)
	require.Equal(t, localBody, nodeBody)
}

// 单机的 429 换号与从节点一致（真实退避约 15 秒）。
func TestGeminiFailoverLocalAndNodeAgree(t *testing.T) {
	if testing.Short() {
		t.Skip("the Gemini forward path retries a 429 with its real backoff (about 15s per run)")
	}
	accounts := []service.Account{geminiAccount(1, "limited"), geminiAccount(2, "fine")}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream + "/status-429"
		accounts[1].Credentials["base_url"] = upstream
		accounts[0].Priority, accounts[1].Priority = 1, 2
		return accounts
	})
	e.world.sel.deps.Gemini = service.NewGeminiMessagesCompatService(newRecordingAntigravityRepo(accounts), nil, nil, nil, service.NewGeminiTokenProvider(nil, seededTokens{}, nil), nil, nil, nil, &config.Config{})
	local := startLocalGemini(t, e, accounts)

	nodeStatus, nodeBody := e.postGoogle(t, geminiPath, "sk-gemini", geminiTurn1)
	e.world.waitReleased(t)
	time.Sleep(50 * time.Millisecond)
	// 单机这一路没有限流写入的仓储，第一个账号仍可选，同样 429 后换到第二个。
	localStatus, localBody := local.postGoogle(t, geminiPath, "sk-gemini", geminiTurn1)
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus)
	require.Equal(t, localBody, nodeBody)
}
