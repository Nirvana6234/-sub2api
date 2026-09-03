package routes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// codex 真发出来的载荷长这样（截了几个字段）：instructions/tools/input 一应俱全，
// 而且里面有我们**不认识也不该认识**的字段。这条路上任何"重新拼一份 body"的写法
// 都会把它们弄丢，而且不会报错。
const codexResponsesPayload = `{"model":"gpt-5",` +
	`"instructions":"You are Codex...",` +
	`"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions instructions>"}]}],` +
	`"tools":[{"type":"function","name":"shell"}],` +
	`"reasoning":{"effort":"medium","summary":"auto"},` +
	`"include":["reasoning.encrypted_content"],` +
	`"prompt_cache_key":"01a06698","store":false,"stream":true,` +
	`"parallel_tool_calls":false,"tool_choice":"auto",` +
	`"client_metadata":{"unknown_future_field":"must survive"}}`

func newPawResponsesRouteEngine(dispatch gin.HandlerFunc, userID int64) *gin.Engine {
	return newPawResponsesRouteEngineOn(service.PlatformOpenAI, dispatch, nil, userID)
}

// pawResponsesPlatformGroups —— 平台可换的分组来源，用来验分派的另一半分支。
type pawResponsesPlatformGroups struct{ platform string }

func (g pawResponsesPlatformGroups) AvailableGroups(context.Context, int64) ([]service.Group, error) {
	return []service.Group{{ID: 7, Name: "G", Platform: g.platform, Status: service.StatusActive}}, nil
}

type pawResponsesPlatformChannels struct{ platform string }

func (c pawResponsesPlatformChannels) GetChannelForGroup(context.Context, int64) (*service.Channel, error) {
	return &service.Channel{
		Status:       service.StatusActive,
		ModelPricing: []service.ChannelModelPricing{{Platform: c.platform, Models: []string{"gpt-5"}}},
	}, nil
}

func newPawResponsesRouteEngineOn(platform string, openAIDispatch, gatewayDispatch gin.HandlerFunc, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	setting := (*service.SettingService)(nil)
	config := service.NewPawConfigService(
		pawResponsesPlatformGroups{platform: platform},
		pawChatRouteUsers{},
		pawResponsesPlatformChannels{platform: platform},
		&pawRouteStore{},
	)
	dispatch := openAIDispatch
	keySource := &pawChatRouteKeySource{apiKey: &service.APIKey{
		ID:     99,
		UserID: userID,
		Status: service.StatusActive,
		// 服务端那把内部 key 是 auto_group 的 —— 它会按请求体里的 model 自己选分组。
		// Responses 这条必须把它钉死在调用方选的分组上，所以这里刻意开着。
		AutoGroup:    true,
		AutoGroupIDs: []int64{7},
		User:         &service.User{ID: userID, Status: service.StatusActive},
	}}
	auth := func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID})
		c.Next()
	}
	RegisterPawRoutes(r.Group("/api/v1"), config, auth, setting, servermiddleware.NewPanelRateLimiter(nil, setting), PawRouteDependencies{
		ChatService:      service.NewPawChatService(config, keySource),
		OpenAIResponses:  dispatch,
		GatewayResponses: gatewayDispatch,
	})
	return r
}

func postResponses(r *gin.Engine, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/paw/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// 这条是整条路最要紧的性质：**请求体一个字节都不许动**。
func TestPawResponsesRoutePassesTheBodyThroughVerbatim(t *testing.T) {
	var seen string
	r := newPawResponsesRouteEngine(func(c *gin.Context) {
		raw, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		seen = string(raw)
		c.Status(http.StatusOK)
	}, 42)

	w := postResponses(r, codexResponsesPayload, map[string]string{PawGroupHeader: "7"})

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, codexResponsesPayload, seen, "请求体被改写了 —— codex 的行为会被悄悄改变")
}

// 分组从**请求头**来，并且必须被钉死在 key 上：auto_group 一旦还开着，
// 分组就会被请求体里的 model 反过来决定，调用方选的那个被无声地覆盖。
func TestPawResponsesRoutePinsTheHeaderGroupOntoTheKey(t *testing.T) {
	r := newPawResponsesRouteEngine(func(c *gin.Context) {
		key, ok := servermiddleware.GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.NotNil(t, key.GroupID)
		require.Equal(t, int64(7), *key.GroupID)
		require.False(t, key.AutoGroup, "auto_group 没关掉：分组会被请求体里的 model 反过来决定")
		require.Empty(t, key.AutoGroupIDs)
		c.Status(http.StatusOK)
	}, 42)

	w := postResponses(r, codexResponsesPayload, map[string]string{PawGroupHeader: "7"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// 没带分组头就得明着报错。**绝不能**退而求其次替用户挑一个 ——
// 那会让这一轮悄悄跑在用户没选的分组上，账单和限额都记到别处去。
func TestPawResponsesRouteRefusesToGuessTheGroup(t *testing.T) {
	for _, header := range []map[string]string{
		{},
		{PawGroupHeader: ""},
		{PawGroupHeader: "0"},
		{PawGroupHeader: "-1"},
		{PawGroupHeader: "abc"},
	} {
		r := newPawResponsesRouteEngine(func(c *gin.Context) {
			t.Fatal("没有分组就不该打到上游")
		}, 42)
		w := postResponses(r, codexResponsesPayload, header)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), PawGroupHeader)
	}
}

// 和 chat 那条一样：Paw 面只认账号会话，带任何 provider 凭据都直接拒。
func TestPawResponsesRouteRejectsProviderCredentialHeaders(t *testing.T) {
	r := newPawResponsesRouteEngine(func(c *gin.Context) {
		t.Fatal("responses handler must not run")
	}, 42)
	w := postResponses(r, codexResponsesPayload, map[string]string{
		PawGroupHeader: "7",
		"x-api-key":    "provider-secret",
	})
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), PawErrorCodeAuthRequired)
}

// 分组不可见 和 分组里没这个模型，是**两种不同的错**：后者告诉用户换模型，
// 前者不能泄露这个分组存在。合并成一种就等于告诉外面「这个分组 ID 是有效的」。
func TestPawResponsesRouteSeparatesUnknownGroupFromUnavailableModel(t *testing.T) {
	r := newPawResponsesRouteEngine(func(c *gin.Context) {
		t.Fatal("校验没过就不该打到上游")
	}, 42)

	w := postResponses(r, `{"model":"gpt-5"}`, map[string]string{PawGroupHeader: "999"})
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "GROUP_FORBIDDEN")

	w = postResponses(r, `{"model":"no-such-model"}`, map[string]string{PawGroupHeader: "7"})
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "MODEL_UNAVAILABLE")
}

// 这条钉的是一个**已经犯过一次的错**：照搬 chat 那条的 reasoning 校验。
//
// chat 那条的 reasoning 是用户在界面上选的，挡下来是在帮用户；这条的是 codex
// 自己每一轮都带的。把它也校一遍，结果是只要 Paw 的模型目录没列出那个档位，
// **每一轮**都被一句「reasoning level not supported」退回来 —— 而上游其实接得住。
// 这里的假模型就是一个**没有任何 reasoning 档位**的模型。
func TestPawResponsesRouteDoesNotValidateCodexOwnReasoningEffort(t *testing.T) {
	reached := false
	r := newPawResponsesRouteEngine(func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	}, 42)

	w := postResponses(r, `{"model":"gpt-5","reasoning":{"effort":"medium","summary":"auto"}}`,
		map[string]string{PawGroupHeader: "7"})

	require.True(t, reached, "codex 自带的 reasoning.effort 把整轮挡下了")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// 分派的**另一半分支**：非 OpenAI/Grok 的分组要落到通用网关那条，而不是 503。
//
// 这条和 gateway.go 里 `/responses` 用的 isOpenAIResponsesCompatibleGatewayPlatform
// 是同一套判定（OpenAI 或 Grok 走 OpenAI 网关，其余走通用网关）。两边写法不同
// 却必须给出同样的答案 —— 只测 OpenAI 一种平台的话，分歧了也看不出来。
func TestPawResponsesRouteSendsNonOpenAIPlatformsToTheGenericGateway(t *testing.T) {
	for _, platform := range []string{service.PlatformAnthropic, service.PlatformGemini} {
		var wentToGeneric bool
		r := newPawResponsesRouteEngineOn(
			platform,
			func(c *gin.Context) { t.Fatalf("%s 不该走 OpenAI 网关", platform) },
			func(c *gin.Context) { wentToGeneric = true; c.Status(http.StatusOK) },
			42,
		)
		w := postResponses(r, codexResponsesPayload, map[string]string{PawGroupHeader: "7"})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.True(t, wentToGeneric, "%s 没落到通用网关", platform)
	}
}

// Grok 和 OpenAI 一样走 OpenAI 网关 —— 这半边同样只有一个平台被覆盖过。
func TestPawResponsesRouteSendsGrokToTheOpenAIGateway(t *testing.T) {
	var wentToOpenAI bool
	r := newPawResponsesRouteEngineOn(
		service.PlatformGrok,
		func(c *gin.Context) { wentToOpenAI = true; c.Status(http.StatusOK) },
		func(c *gin.Context) { t.Fatal("Grok 不该走通用网关") },
		42,
	)
	w := postResponses(r, codexResponsesPayload, map[string]string{PawGroupHeader: "7"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.True(t, wentToOpenAI)
}

// 这两个常量在 Rust 那侧（crates/codex-host/src/proxy.rs）有一份**字面量的副本**：
// UPSTREAM_PATH = "/api/v1/paw/responses"、GROUP_HEADER = "X-Paw-Group-Id"。
// 两边靠肉眼对齐，改名的话两边测试都还是绿的、而产品 404。所以两边各钉一次字面量。
func TestPawResponsesRouteWireContractMatchesTheRustRelay(t *testing.T) {
	require.Equal(t, "X-Paw-Group-Id", PawGroupHeader,
		"改了就得同步改 crates/codex-host/src/proxy.rs 的 GROUP_HEADER")

	var seenPath string
	r := newPawResponsesRouteEngineOn(service.PlatformOpenAI, func(c *gin.Context) {
		seenPath = c.Request.URL.Path
		c.Status(http.StatusOK)
	}, nil, 42)
	postResponses(r, codexResponsesPayload, map[string]string{PawGroupHeader: "7"})
	require.Equal(t, "/api/v1/paw/responses", seenPath,
		"改了就得同步改 crates/codex-host/src/proxy.rs 的 UPSTREAM_PATH")
}
