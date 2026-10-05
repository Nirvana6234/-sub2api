package relayselect

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// ---- 小白端 /paw/* 的测试替身（与 routes 包的路由测试同一类）----

const (
	pawUserID        = int64(3)
	pawInternalKey   = "SECRET-INTERNAL-PAW-KEY"
	pawOpenAIGroupID = int64(5)
	pawTypeSafeGroup = int64(31)
)

type pawGroups struct{}

func (pawGroups) AvailableGroups(context.Context, int64) ([]service.Group, error) {
	return []service.Group{
		{ID: pawOpenAIGroupID, Name: "OpenAI", Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1},
		{ID: pawTypeSafeGroup, Name: "TypeSafe", Platform: service.PlatformTypeSafe, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1},
	}, nil
}

// pawUsers 是用户仓储：配置服务和主节点的复查都用它；password 变了 token_version 跟着变。
type pawUsers struct {
	service.UserRepository
	mu       sync.Mutex
	password string
	status   string
}

func (u *pawUsers) GetByID(context.Context, int64) (*service.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return &service.User{ID: pawUserID, Username: "u", Email: "u@example.com", PasswordHash: u.password, Role: "user", Status: u.status, Balance: 10, Concurrency: 5}, nil
}

func (u *pawUsers) changePassword(hash string) {
	u.mu.Lock()
	u.password = hash
	u.mu.Unlock()
}

func (u *pawUsers) tokenVersion() int64 {
	user, _ := u.GetByID(context.Background(), pawUserID)
	return service.ResolvedTokenVersion(user)
}

type pawChannels struct{}

func (pawChannels) GetChannelForGroup(_ context.Context, groupID int64) (*service.Channel, error) {
	platform := service.PlatformOpenAI
	if groupID == pawTypeSafeGroup {
		platform = service.PlatformTypeSafe
	}
	return &service.Channel{Status: service.StatusActive, ModelPricing: []service.ChannelModelPricing{{Platform: platform, Models: []string{"gpt-5"}}}}, nil
}

type pawStore struct{}

func (*pawStore) GetPawDefaults(context.Context, int64) (service.PawDefaults, error) {
	return service.PawDefaults{}, nil
}
func (*pawStore) SavePawDefaults(context.Context, int64, service.PawDefaults) error { return nil }

// pawLookup 是内部 key 的查找（真实现是 APIKeyService）：用户只有一把内部 key，分组按 pawGroups。
type pawLookup struct{}

func (pawLookup) internalKey() *service.APIKey {
	return &service.APIKey{ID: 99, Key: pawInternalKey, UserID: pawUserID, Name: service.PlaygroundChatAPIKeyName, Status: service.StatusActive, AutoGroup: true,
		AutoGroupIDs: []int64{pawOpenAIGroupID}, User: &service.User{ID: pawUserID, Status: service.StatusActive, Balance: 10, Concurrency: 5}}
}

func (l pawLookup) SearchAPIKeys(context.Context, int64, string, int) ([]service.APIKey, error) {
	return []service.APIKey{*l.internalKey()}, nil
}
func (pawLookup) EnsurePlaygroundAPIKeys(context.Context, int64) error { return nil }
func (l pawLookup) Create(context.Context, int64, service.CreateAPIKeyRequest) (*service.APIKey, error) {
	return l.internalKey(), nil
}
func (l pawLookup) GetByID(context.Context, int64) (*service.APIKey, error) {
	return l.internalKey(), nil
}
func (pawLookup) GetAvailableGroups(ctx context.Context, userID int64) ([]service.Group, error) {
	return pawGroups{}.AvailableGroups(ctx, userID)
}
func (pawLookup) GetActiveSubscriptionForGroup(context.Context, int64, int64) (*service.UserSubscription, error) {
	return nil, nil
}

var pawKeySource = service.APIKeyPawChatKeySource{Service: pawLookup{}}

func newPawServices() (*service.PawConfigService, *service.PawChatService, *service.PawImageService) {
	cfg := service.NewPawConfigService(pawGroups{}, &pawUsers{status: service.StatusActive}, pawChannels{}, &pawStore{})
	return cfg, service.NewPawChatService(cfg, pawKeySource), service.NewPawImageService(cfg, pawKeySource)
}

// usePaw 给主节点装上 /paw 的校验服务和用户仓储。
func (e *e2e) usePaw(t *testing.T) *pawUsers {
	t.Helper()
	users := &pawUsers{status: service.StatusActive}
	cfg := service.NewPawConfigService(pawGroups{}, users, pawChannels{}, &pawStore{})
	e.world.sel.deps.Users = users
	e.world.sel.deps.PawChat = service.NewPawChatService(cfg, pawKeySource)
	e.world.sel.deps.PawImages = service.NewPawImageService(cfg, pawKeySource)
	return users
}

// ticket 签一张给这台节点的中转票据（WP10 里直接签；分配接口在 WP12）。
func (e *e2e) ticket(t *testing.T, tokenVersion int64) string {
	t.Helper()
	return e.ticketFor(t, e.nodeID, tokenVersion, time.Now())
}

func (e *e2e) ticketFor(t *testing.T, nodeID, tokenVersion int64, at time.Time) string {
	t.Helper()
	token, _, err := sign.IssueTicket(e.world.ticketSigner, pawUserID, nodeID, tokenVersion, at)
	require.NoError(t, err)
	return token
}

func (e *e2e) pawPost(t *testing.T, path, token, body string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.gateway.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return doRequest(t, req)
}

// startLocalPaw 是单机上的 /paw 路由（同一份 routes.RegisterPawRoutes，JWT 换成固定用户），分派到本地 OpenAI 网关。
func startLocalPaw(t *testing.T, e *e2e, accounts []service.Account) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.MaxBodySize = 10 << 20
	cfg.Gateway.TextMaxBodySize = 10 << 20
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	concurrency := service.NewConcurrencyService(&countingSlots{})
	billing := service.NewBillingCacheService(balanceCache{balance: 10}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	upstreamURL, err := url.Parse(e.upstream.URL)
	require.NoError(t, err)
	gw := service.NewOpenAIGatewayService(newRecordingAntigravityRepo(accounts), nil, nil, nil, nil, nil, &countingSticky{bound: map[string]int64{}}, cfg,
		nil, concurrency, nil, nil, nil, plainUpstream{target: upstreamURL}, nil, nil, nil, nil, nil, nil, nil, nil)
	apiKeys := service.NewAPIKeyService(touchKeys{e.world.keys}, nil, nil, nil, nil, nil, cfg)
	h := handler.NewOpenAIGatewayHandler(gw, concurrency, billing, apiKeys, nil, nil, nil, nil, cfg)
	pawCfg, chat, _ := newPawServices()
	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: pawUserID})
		c.Next()
	})
	r := gin.New()
	r.Use(handler.InboundEndpointMiddleware())
	routes.RegisterPawRoutes(r.Group("/api/v1"), pawCfg, jwt, nil, middleware.NewPanelRateLimiter(nil, nil), routes.PawRouteDependencies{
		ChatService: chat, OpenAIGateway: h, OpenAIChat: h.ChatCompletions, OpenAIResponses: h.Responses, Config: cfg,
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func localPawPost(t *testing.T, srv *httptest.Server, path, body string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return doRequest(t, req)
}

func pawE2E(t *testing.T) (*e2e, *pawUsers, *httptest.Server) {
	t.Helper()
	accounts := []service.Account{apiKeyAccount(1, "one")}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		accounts[0].AccountGroups = []service.AccountGroup{{AccountID: 1, GroupID: pawOpenAIGroupID}}
		return accounts
	})
	users := e.usePaw(t)
	return e, users, startLocalPaw(t, e, accounts)
}

// 小白端 /paw 请求经从节点：与单机同请求的响应一致；扣费记录的凭证记在内部 key 钉死的分组上；内部 key 的原文不离开主节点
// （从节点发给主节点的凭据是会话句柄）。
func TestNodeServesPawRequestsLikeASingleServer(t *testing.T) {
	e, users, local := pawE2E(t)
	var credentials []string
	var mu sync.Mutex
	e.world.sel.onSelect = func(req *relayv1.SelectRequest) {
		mu.Lock()
		credentials = append(credentials, req.GetApiKey())
		mu.Unlock()
	}
	ticket := e.ticket(t, users.tokenVersion())
	group := map[string]string{"X-Paw-Group-Id": "5"}

	// Responses：请求体原样透传，分组走请求头。
	const responses = `{"model":"gpt-5","input":"hi","client_metadata":{"future":"kept"}}`
	nodeStatus, nodeBody := e.pawPost(t, "/api/v1/paw/responses", ticket, responses, group)
	e.world.waitReleased(t)
	localStatus, localBody := localPawPost(t, local, "/api/v1/paw/responses", responses, group)
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	for _, field := range []string{"status", "model", "output.0.content.0.text", "usage"} {
		require.Equal(t, gjson.Get(localBody, field).Raw, gjson.Get(nodeBody, field).Raw, field)
	}
	rec := e.lastRecord(t, 1)
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI, rec.GetKind())
	require.Equal(t, pawOpenAIGroupID, mustVoucher(t, e, rec).GetGroupId())

	// 聊天：请求体由从节点拼（带附件时用本机的附件服务）。
	const chat = `{"group_id":5,"model_id":"gpt-5","messages":[{"role":"user","content":"hello"}]}`
	nodeStatus, nodeBody = e.pawPost(t, "/api/v1/paw/chat/completions", ticket, chat, nil)
	e.world.waitReleased(t)
	localStatus, localBody = localPawPost(t, local, "/api/v1/paw/chat/completions", chat, nil)
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, localBody, nodeBody)
	e.lastRecord(t, 2)

	// 从节点发给主节点的选号凭据全是会话句柄，内部 key 的原文没有出现过。
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, credentials)
	for _, c := range credentials {
		require.True(t, strings.HasPrefix(c, pawSessionPrefix), c)
		require.NotContains(t, c, pawInternalKey)
	}
}

// 错误：分组看不到、分组里没有这个模型、TypeSafe 分组走聊天 / Responses、System One 不是 TypeSafe 分组；与单机的状态码和响应体一致。
func TestNodePawErrorsMatchASingleServer(t *testing.T) {
	e, users, local := pawE2E(t)
	ticket := e.ticket(t, users.tokenVersion())
	for _, tc := range []struct {
		name, path, body string
		headers          map[string]string
		status           int
	}{
		{"group the user cannot see (chat)", "/api/v1/paw/chat/completions", `{"group_id":99,"model_id":"gpt-5","messages":[{"role":"user","content":"hi"}]}`, nil, http.StatusForbidden},
		{"model not in the catalog (chat)", "/api/v1/paw/chat/completions", `{"group_id":5,"model_id":"nope","messages":[{"role":"user","content":"hi"}]}`, nil, http.StatusBadRequest},
		{"TypeSafe group on chat", "/api/v1/paw/chat/completions", `{"group_id":31,"model_id":"gpt-5","messages":[{"role":"user","content":"hi"}]}`, nil, http.StatusForbidden},
		{"TypeSafe group on responses", "/api/v1/paw/responses", `{"model":"gpt-5","input":"hi"}`, map[string]string{"X-Paw-Group-Id": "31"}, http.StatusForbidden},
		{"group the user cannot see (responses)", "/api/v1/paw/responses", `{"model":"gpt-5","input":"hi"}`, map[string]string{"X-Paw-Group-Id": "99"}, http.StatusForbidden},
		{"bad group header (responses)", "/api/v1/paw/responses", `{"model":"gpt-5","input":"hi"}`, map[string]string{"X-Paw-Group-Id": "abc"}, http.StatusBadRequest},
		{"group the user cannot see (messages)", "/api/v1/paw/messages", `{"model":"claude-sonnet-4-5","max_tokens":8,"messages":[]}`, map[string]string{"X-Paw-Group-Id": "99"}, http.StatusForbidden},
		{"TypeSafe group on messages", "/api/v1/paw/messages", `{"model":"claude-sonnet-4-5","max_tokens":8,"messages":[]}`, map[string]string{"X-Paw-Group-Id": "31"}, http.StatusForbidden},
		{"missing group (messages)", "/api/v1/paw/messages", `{"model":"claude-sonnet-4-5"}`, nil, http.StatusBadRequest},
		{"System One on a non-TypeSafe group", "/api/v1/paw/systemone", `{"model":"jev-latest"}`, map[string]string{"X-Paw-Group-Id": "5"}, http.StatusForbidden},
		{"provider credential header", "/api/v1/paw/chat/completions", `{"group_id":5,"model_id":"gpt-5","messages":[{"role":"user","content":"hi"}]}`, map[string]string{"x-api-key": "provider-secret"}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodeStatus, nodeBody := e.pawPost(t, tc.path, ticket, tc.body, tc.headers)
			localStatus, localBody := localPawPost(t, local, tc.path, tc.body, tc.headers)
			require.Equal(t, tc.status, localStatus, localBody)
			require.Equal(t, localStatus, nodeStatus, nodeBody)
			require.JSONEq(t, localBody, nodeBody)
		})
	}
}

// 票据：没带、格式错、过期、签给别的节点、被吊销都在从节点本地拒绝；改密码（token_version 变了）、用户停用在主节点复查时拒绝。
func TestNodePawTicketChecks(t *testing.T) {
	e, users, _ := pawE2E(t)
	body := `{"group_id":5,"model_id":"gpt-5","messages":[{"role":"user","content":"hi"}]}`
	post := func(token string) (int, string) {
		return e.pawPost(t, "/api/v1/paw/chat/completions", token, body, nil)
	}

	status, out := post("")
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "UNAUTHORIZED", gjson.Get(out, "code").String())

	status, out = post("not-a-ticket")
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "INVALID_TOKEN", gjson.Get(out, "code").String())

	status, out = post(e.ticketFor(t, e.nodeID+1, users.tokenVersion(), time.Now()))
	require.Equal(t, http.StatusUnauthorized, status, "a ticket for another node")
	require.Equal(t, "INVALID_TOKEN", gjson.Get(out, "code").String())

	status, out = post(e.ticketFor(t, e.nodeID, users.tokenVersion(), time.Now().Add(-sign.TicketLifetime-time.Hour)))
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "TOKEN_EXPIRED", gjson.Get(out, "code").String())

	good := e.ticket(t, users.tokenVersion())
	status, out = post(good)
	require.Equal(t, http.StatusOK, status, out)
	e.world.waitReleased(t)

	// 吊销表（主节点推送）：提前在从节点拒绝。
	e.revocations.Revoke(pawUserID, time.Now().Add(time.Second), time.Now())
	status, out = post(good)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "TOKEN_REVOKED", gjson.Get(out, "code").String())
	e.revocations.Reset()

	// 改密码：旧票据（token_version 变了）下一个请求在主节点被拒，不论从节点有没有收到吊销。
	users.changePassword("new-hash")
	status, out = post(good)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "TOKEN_REVOKED", gjson.Get(out, "code").String())
	// 重新签的票据可用。
	status, out = post(e.ticket(t, users.tokenVersion()))
	require.Equal(t, http.StatusOK, status, out)
	e.world.waitReleased(t)

	// 用户停用。
	users.mu.Lock()
	users.status = service.StatusDisabled
	users.mu.Unlock()
	status, out = post(e.ticket(t, users.tokenVersion()))
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "USER_INACTIVE", gjson.Get(out, "code").String())
}

// 会话句柄只在签给的节点上用、过期作废、用户复查不过时作废；句柄不能当 API Key 用到别的入口。
func TestPawSessionHandleIsBoundToTheNodeAndTheUser(t *testing.T) {
	e, users, _ := pawE2E(t)
	ctx := context.Background()
	resp, err := e.world.sel.PawResolve(ctx, e.nodeID, &relayv1.PawResolveRequest{
		Op: relayv1.PawResolveRequest_OP_RESPONSES, Ticket: e.ticket(t, users.tokenVersion()), GroupId: pawOpenAIGroupID, ModelId: "gpt-5", Method: "POST", Path: "/api/v1/paw/responses",
	})
	require.NoError(t, err)
	handle := resp.GetResolution().GetSession()
	require.True(t, strings.HasPrefix(handle, pawSessionPrefix), "%+v", resp.GetRejection())

	selectReq := func(nodeID int64) *relayv1.SelectResponse {
		out, err := e.world.sel.Select(ctx, nodeID, &relayv1.SelectRequest{
			RequestId: "paw-1", Attempt: 1, Credential: &relayv1.SelectRequest_ApiKey{ApiKey: handle}, Method: "POST", Path: "/api/v1/paw/responses",
			Endpoint: relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_RESPONSES, Model: "gpt-5", ClientIp: "5.6.7.8",
		})
		require.NoError(t, err)
		return out
	}
	require.NotNil(t, selectReq(e.nodeID).GetSelection())
	other := selectReq(e.nodeID + 1)
	require.Equal(t, int32(http.StatusUnauthorized), other.GetRejection().GetStatus(), "another node cannot use the handle")

	users.changePassword("changed")
	require.Equal(t, int32(http.StatusUnauthorized), selectReq(e.nodeID).GetRejection().GetStatus(), "the user is rechecked on every use")
}

// 聊天带附件：附件传到本机的附件服务，请求体由从节点拼（附件内容不经主节点）。
func TestNodePawChatWithAttachments(t *testing.T) {
	e, users, _ := pawE2E(t)
	ticket := e.ticket(t, users.tokenVersion())

	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="pixel.png"`)
	header.Set("Content-Type", "image/png")
	part, err := w.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(tinyPNG)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	req, err := http.NewRequest(http.MethodPost, e.gateway.URL+"/api/v1/paw/files", &form)
	require.NoError(t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+ticket)
	status, body := doRequest(t, req)
	require.Equal(t, http.StatusOK, status, body)
	id := gjson.Get(body, "data.id").String()
	require.NotEmpty(t, id)

	chat := `{"group_id":5,"model_id":"gpt-5","messages":[{"role":"user","content":"read it"}],"attachments":[{"id":"` + id + `"}]}`
	status, body = e.pawPost(t, "/api/v1/paw/chat/completions", ticket, chat, nil)
	require.Equal(t, http.StatusOK, status, body)
	e.world.waitReleased(t)
	require.Eventually(t, func() bool {
		for {
			select {
			case hit := <-e.hits:
				_ = hit
			default:
				return true
			}
		}
	}, time.Second, 10*time.Millisecond)

	// 引用一个不存在的附件：与单机同样的错误。
	status, body = e.pawPost(t, "/api/v1/paw/chat/completions", ticket,
		`{"group_id":5,"model_id":"gpt-5","messages":[{"role":"user","content":"x"}],"attachments":[{"id":"att_missing"}]}`, nil)
	require.GreaterOrEqual(t, status, 400, body)
	require.Equal(t, "ATTACHMENT_INVALID", gjson.Get(body, "error.code").String(), body)
}

// tinyPNG 是 1x1 的 PNG（附件按内容识别类型）。
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R', 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
}
