package relayselect

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// localOpenAI 是单机上的 OpenAI 网关（同一个处理函数，没有从节点）：与从节点那一路对同样的请求比对结果。
type localOpenAI struct {
	server *httptest.Server
}

func startLocalOpenAI(t *testing.T, e *e2e, accounts []service.Account, routes func(r *gin.RouterGroup, h *handler.OpenAIGatewayHandler)) *localOpenAI {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.MaxBodySize = 10 << 20
	cfg.Gateway.TextMaxBodySize = 10 << 20
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	slots := &countingSlots{}
	concurrency := service.NewConcurrencyService(slots)
	billing := service.NewBillingCacheService(balanceCache{balance: 10}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	upstreamURL, err := url.Parse(e.upstream.URL)
	require.NoError(t, err)
	repo := newRecordingAntigravityRepo(accounts)
	gw := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, &countingSticky{bound: map[string]int64{}}, cfg,
		nil, concurrency, nil, nil, nil, plainUpstream{target: upstreamURL}, nil, nil, nil, nil, nil, nil, nil, nil)
	apiKeys := service.NewAPIKeyService(touchKeys{e.world.keys}, nil, nil, nil, nil, nil, cfg)
	h := handler.NewOpenAIGatewayHandler(gw, concurrency, billing, apiKeys, nil, nil, nil, nil, cfg)

	r := gin.New()
	r.Use(middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize), handler.InboundEndpointMiddleware())
	g := r.Group("/v1", gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(apiKeys, nil, cfg)))
	routes(g, h)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &localOpenAI{server: srv}
}

func (l *localOpenAI) post(t *testing.T, path, key, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, l.server.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	return doRequest(t, req)
}

const embeddingsBody = `{"model":"text-embedding-3-small","input":"hi"}`

// /v1/embeddings 经从节点：与单机同请求的响应一致（成功、没有可用账号）；用量同一个记录种类。
func TestNodeServesEmbeddingsLikeASingleServer(t *testing.T) {
	accounts := []service.Account{apiKeyAccount(1, "one")}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		accounts[0].AccountGroups = []service.AccountGroup{{AccountID: 1, GroupID: 5}}
		return accounts
	})
	local := startLocalOpenAI(t, e, accounts, func(g *gin.RouterGroup, h *handler.OpenAIGatewayHandler) {
		g.POST("/embeddings", h.Embeddings)
	})
	nodeStatus, nodeBody := e.post(t, "/v1/embeddings", "sk-a", embeddingsBody)
	e.world.waitReleased(t)
	localStatus, localBody := local.post(t, "/v1/embeddings", "sk-a", embeddingsBody)
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, localBody, nodeBody)
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5e9, 2e7)
	require.Equal(t, int64(5), mustVoucher(t, e, e.settler.records()[0]).GetGroupId())

	empty := startStandardE2E(t, func(string) []service.Account { return nil })
	emptyLocal := startLocalOpenAI(t, empty, nil, func(g *gin.RouterGroup, h *handler.OpenAIGatewayHandler) {
		g.POST("/embeddings", h.Embeddings)
	})
	nodeStatus, nodeBody = empty.post(t, "/v1/embeddings", "sk-a", embeddingsBody)
	empty.world.waitReleased(t)
	localStatus, localBody = emptyLocal.post(t, "/v1/embeddings", "sk-a", embeddingsBody)
	require.Equal(t, localStatus, nodeStatus)
	require.Equal(t, localBody, nodeBody)
}

const imagesBody = `{"model":"gpt-image-1","prompt":"a cat","n":1}`

// 同步图片入口经从节点：与单机同请求的响应一致；用量同一个记录种类；没有可用账号时的错误一致。
func TestNodeServesImagesLikeASingleServer(t *testing.T) {
	accounts := []service.Account{apiKeyAccount(1, "one")}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		accounts[0].AccountGroups = []service.AccountGroup{{AccountID: 1, GroupID: 5}}
		return accounts
	})
	e.world.keys.keys["sk-a"].Group.AllowImageGeneration = true
	local := startLocalOpenAI(t, e, accounts, func(g *gin.RouterGroup, h *handler.OpenAIGatewayHandler) {
		g.POST("/images/generations", h.Images)
	})
	nodeStatus, nodeBody := e.post(t, "/v1/images/generations", "sk-a", imagesBody)
	e.world.waitReleased(t)
	localStatus, localBody := local.post(t, "/v1/images/generations", "sk-a", imagesBody)
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, localBody, nodeBody)
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5e9, 2e7)
	require.Equal(t, int64(5), mustVoucher(t, e, e.settler.records()[0]).GetGroupId())

	empty := startStandardE2E(t, func(string) []service.Account { return nil })
	empty.world.keys.keys["sk-a"].Group.AllowImageGeneration = true
	emptyLocal := startLocalOpenAI(t, empty, nil, func(g *gin.RouterGroup, h *handler.OpenAIGatewayHandler) {
		g.POST("/images/generations", h.Images)
	})
	nodeStatus, nodeBody = empty.post(t, "/v1/images/generations", "sk-a", imagesBody)
	empty.world.waitReleased(t)
	localStatus, localBody = emptyLocal.post(t, "/v1/images/generations", "sk-a", imagesBody)
	require.Equal(t, localStatus, nodeStatus)
	require.Equal(t, localBody, nodeBody)
}

// OpenAI 分组的两个 token 计数入口经从节点：与单机同请求的响应一致；不计费（没有扣费记录）。
func TestNodeServesOpenAITokenCountingLikeASingleServer(t *testing.T) {
	accounts := []service.Account{apiKeyAccount(1, "one")}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		accounts[0].AccountGroups = []service.AccountGroup{{AccountID: 1, GroupID: 5}}
		return accounts
	})
	e.world.keys.keys["sk-a"].Group.AllowMessagesDispatch = true
	routes := func(g *gin.RouterGroup, h *handler.OpenAIGatewayHandler) {
		g.POST("/messages/count_tokens", h.CountTokens)
		g.POST("/responses/input_tokens", h.ResponsesInputTokens)
	}
	local := startLocalOpenAI(t, e, accounts, routes)
	for _, tc := range []struct{ path, body string }{
		{"/v1/messages/count_tokens", `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/responses/input_tokens", `{"model":"gpt-5","input":"hi"}`},
	} {
		nodeStatus, nodeBody := e.post(t, tc.path, "sk-a", tc.body)
		e.world.waitReleased(t)
		localStatus, localBody := local.post(t, tc.path, "sk-a", tc.body)
		require.Equal(t, http.StatusOK, localStatus, tc.path+" "+localBody)
		require.Equal(t, localStatus, nodeStatus, nodeBody)
		require.Equal(t, localBody, nodeBody, tc.path)
	}
	require.Empty(t, e.settler.records(), "token counting is not billed")

	empty := startStandardE2E(t, func(string) []service.Account { return nil })
	empty.world.keys.keys["sk-a"].Group.AllowMessagesDispatch = true
	emptyLocal := startLocalOpenAI(t, empty, nil, routes)
	for _, tc := range []struct{ path, body string }{
		{"/v1/messages/count_tokens", `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/responses/input_tokens", `{"model":"gpt-5","input":"hi"}`},
	} {
		nodeStatus, nodeBody := empty.post(t, tc.path, "sk-a", tc.body)
		empty.world.waitReleased(t)
		localStatus, localBody := emptyLocal.post(t, tc.path, "sk-a", tc.body)
		require.Equal(t, localStatus, nodeStatus)
		require.Equal(t, localBody, nodeBody, tc.path)
	}
}

// Codex alpha search 经从节点：与单机同请求的结果一致（有账号、没有可用账号）；有账号时用量同一个记录种类。
func TestNodeServesAlphaSearchLikeASingleServer(t *testing.T) {
	routes := func(g *gin.RouterGroup, h *handler.OpenAIGatewayHandler) {
		g.POST("/alpha/search", h.AlphaSearch)
	}
	const body = `{"model":"gpt-5","id":"search-1","query":"hi"}`
	accounts := []service.Account{apiKeyAccount(1, "one")}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		accounts[0].AccountGroups = []service.AccountGroup{{AccountID: 1, GroupID: 5}}
		return accounts
	})
	local := startLocalOpenAI(t, e, accounts, routes)
	nodeStatus, nodeBody := e.post(t, "/v1/alpha/search", "sk-a", body)
	e.world.waitReleased(t)
	localStatus, localBody := local.post(t, "/v1/alpha/search", "sk-a", body)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, localBody, nodeBody)

	empty := startStandardE2E(t, func(string) []service.Account { return nil })
	emptyLocal := startLocalOpenAI(t, empty, nil, routes)
	nodeStatus, nodeBody = empty.post(t, "/v1/alpha/search", "sk-a", body)
	empty.world.waitReleased(t)
	localStatus, localBody = emptyLocal.post(t, "/v1/alpha/search", "sk-a", body)
	require.Equal(t, localStatus, nodeStatus)
	require.Equal(t, localBody, nodeBody)
}

// Grok、国产兼容平台（Kimi 等）分组的 Responses / Chat / count_tokens 入口也经从节点（API Key 账号）：与单机同请求的响应一致。
func TestNodeServesOpenAICompatiblePlatformsLikeASingleServer(t *testing.T) {
	kimi := openAIGroup(21)
	kimi.Platform = service.PlatformKimi
	account := apiKeyAccount(1, "kimi")
	account.Platform = service.PlatformKimi
	account.AccountGroups = []service.AccountGroup{{AccountID: 1, GroupID: 21}}
	accounts := []service.Account{account}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		return accounts
	})
	e.world.keys.keys["sk-kimi"] = testKey("sk-kimi", 25, kimi)
	local := startLocalOpenAI(t, e, accounts, func(g *gin.RouterGroup, h *handler.OpenAIGatewayHandler) {
		g.POST("/chat/completions", h.ChatCompletions)
		g.POST("/responses", h.Responses)
	})
	for _, tc := range []struct{ path, body string }{
		{"/v1/chat/completions", `{"model":"kimi-k2","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/responses", `{"model":"kimi-k2","input":"hi"}`},
	} {
		nodeStatus, nodeBody := e.post(t, tc.path, "sk-kimi", tc.body)
		e.world.waitReleased(t)
		localStatus, localBody := local.post(t, tc.path, "sk-kimi", tc.body)
		require.Equal(t, localStatus, nodeStatus, tc.path+" "+nodeBody)
		if tc.path == "/v1/responses" {
			// 响应里的 item id 是随机的，比对其余字段。
			for _, field := range []string{"status", "model", "output.0.content.0.text", "usage"} {
				require.Equal(t, gjson.Get(localBody, field).Raw, gjson.Get(nodeBody, field).Raw, field)
			}
			continue
		}
		require.Equal(t, localBody, nodeBody, tc.path)
	}
	require.Eventually(t, func() bool { return len(e.settler.records()) >= 1 }, 5e9, 2e7, "served by the node and billed")
	require.Equal(t, int64(21), mustVoucher(t, e, e.settler.records()[0]).GetGroupId())
}
