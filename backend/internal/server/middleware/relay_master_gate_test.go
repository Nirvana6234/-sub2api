package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestIsRelayForwardingRoute(t *testing.T) {
	get, post := http.MethodGet, http.MethodPost
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		// 转发入口：各前缀、根路径别名、子路径。
		{post, "/v1/responses", true},
		{post, "/v1/responses/*subpath", true},
		{get, "/v1/responses", true}, // Responses WebSocket
		{post, "/v1/chat/completions", true},
		{post, "/v1/messages", true},
		{post, "/v1/messages/count_tokens", true},
		{post, "/chat/completions", true},
		{post, "/responses", true},
		{post, "/v1/images/generations", true},
		{post, "/v1/images/edits/async", true},
		{post, "/v1/videos/generations", true},
		{get, "/v1/videos/:request_id/content", true},
		{post, "/v1/embeddings", true},
		{post, "/v1/alpha/search", true},
		{post, "/v1/systemone", true},
		{post, "/v1/web_search", true},
		{post, "/v1/live", true},
		{get, "/v1/live/:call_id", true},
		{get, "/v1/realtime", true},
		{post, "/v1/tts", true},
		{post, "/v1/custom-voices", true},
		{post, "/backend-api/codex/responses", true},
		{post, "/api/v3/contents/generations/tasks", true},
		{get, "/v3/contents/generations/tasks/:task_id", true},
		{post, "/v1beta/models/*modelAction", true},
		{post, "/antigravity/v1/messages", true},
		{post, "/antigravity/v1beta/models/*modelAction", true},
		// 不是转发的接口（设计 8.4）和别的东西：不拦。
		{get, "/v1/models", false},
		{get, "/v1/models/:model", false},
		{get, "/v1beta/models", false},
		{get, "/v1beta/models/*modelAction", false},
		{get, "/v1/usage", false},
		{get, "/v1/sub2api/billing", false},
		{get, "/v1/relay/assignment", false},
		{post, "/v1/images/batches", false},
		{get, "/v1/images/batches/:batch_id", false},
		{get, "/v1/images/tasks/:task_id", false},
		{get, "/antigravity/v1/usage", false},
		{get, "/backend-api/codex/models", false},
		{post, "/api/v1/paw/chat/completions", false},
		{post, "/api/v1/auth/login", false},
		{post, "/models/foo", false},
		{get, "/health", false},
		{get, "", false},
	} {
		require.Equal(t, tc.want, IsRelayForwardingRoute(tc.method, tc.path), "%s %s", tc.method, tc.path)
	}
}

type gateStub struct {
	block   bool
	key     []byte
	address string
}

func (g gateStub) BlockMasterForwarding(context.Context) bool { return g.block }
func (g gateStub) VerifyHandoff(header, method, path string) bool {
	_, err := sign.VerifyHandoff(g.key, header, method, path, time.Now())
	return err == nil
}
func (g gateStub) AssignedAddress(context.Context, string) string { return g.address }

// 设计 10.5：主节点分配比例为 0 时，API Key 的转发请求打到主节点回 403（OpenAI / Anthropic / Google 格式）并提示分配的地址；
// 从节点交来的请求（带标记）、不是转发的接口、没开分流时都照常。
func TestRelayMasterGateBlocksDirectForwardingAtRatioZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := []byte("0123456789abcdef0123456789abcdef")
	r := gin.New()
	r.Use(RelayMasterGateMiddleware())
	ok := func(c *gin.Context) { c.String(http.StatusOK, "served") }
	r.POST("/v1/responses", ok)
	r.POST("/v1/messages", ok)
	r.POST("/v1beta/models/*modelAction", ok)
	r.GET("/v1/models", ok)
	r.GET("/v1/relay/assignment", ok)

	do := func(method, path string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	auth := map[string]string{"Authorization": "Bearer sk-a"}

	// 没有闸门（主从分流没开）：照常。
	require.Equal(t, http.StatusOK, do(http.MethodPost, "/v1/responses", auth).Code)

	SetRelayMasterGate(gateStub{block: true, key: key, address: "https://r1.example.com"})
	t.Cleanup(func() { SetRelayMasterGate(nil) })

	w := do(http.MethodPost, "/v1/responses", auth)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, "master_relay_disabled", gjson.Get(w.Body.String(), "error.code").String(), w.Body.String())
	require.Contains(t, gjson.Get(w.Body.String(), "error.message").String(), "https://r1.example.com")

	w = do(http.MethodPost, "/v1/messages", auth)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, "error", gjson.Get(w.Body.String(), "type").String(), "Anthropic shape: "+w.Body.String())

	w = do(http.MethodPost, "/v1beta/models/gemini:generateContent", map[string]string{"x-goog-api-key": "sk-a"})
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, "PERMISSION_DENIED", gjson.Get(w.Body.String(), "error.status").String(), "Google shape: "+w.Body.String())

	// 不是转发的接口照常（模型列表、分配查询）。
	require.Equal(t, http.StatusOK, do(http.MethodGet, "/v1/models", auth).Code)
	require.Equal(t, http.StatusOK, do(http.MethodGet, "/v1/relay/assignment", auth).Code)

	// 从节点交来的请求：标记要对得上方法和路径；客户端自己编的标记不行。
	marker := sign.SignHandoff(key, 7, http.MethodPost, "/v1/responses", time.Now())
	withMarker := map[string]string{"Authorization": "Bearer sk-a", sign.HandoffHeader: marker}
	require.Equal(t, http.StatusOK, do(http.MethodPost, "/v1/responses", withMarker).Code)
	require.Equal(t, http.StatusForbidden, do(http.MethodPost, "/v1/messages", withMarker).Code, "the marker is bound to the path")
	forgedKey := []byte("some-other-key-some-other-key-123")
	forged := map[string]string{"Authorization": "Bearer sk-a", sign.HandoffHeader: sign.SignHandoff(forgedKey, 7, http.MethodPost, "/v1/responses", time.Now())}
	require.Equal(t, http.StatusForbidden, do(http.MethodPost, "/v1/responses", forged).Code)

	// 分配比例大于 0：照常。
	SetRelayMasterGate(gateStub{block: false})
	require.Equal(t, http.StatusOK, do(http.MethodPost, "/v1/responses", auth).Code)
}
