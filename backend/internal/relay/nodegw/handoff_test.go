package nodegw

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
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 从节点还没接的 WebSocket（Codex 的 Responses WS）经"交给主节点"原样反向代理：升级握手、双向帧都要通。
func TestHandOffProxiesWebSocketUpgrades(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotAuth, gotPath, gotXFF string
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotXFF = r.Header.Get("Authorization"), r.URL.Path, r.Header.Get("X-Forwarded-For")
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			typ, msg, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), typ, append([]byte("echo:"), msg...)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(master.Close)
	masterURL, err := url.Parse(master.URL)
	require.NoError(t, err)

	d := NewDispatcher(Deps{HandOff: NewHandOff(masterURL, http.DefaultTransport)})
	cfg := &config.Config{}
	cfg.Gateway.MaxBodySize = 1 << 20
	r := NewEngine()
	RegisterRoutes(r, nil, d, cfg, nil)
	node := httptest.NewServer(r)
	t.Cleanup(node.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 实时会话旁路（/:call_id）按 ID 选号，还没接入，仍整条连接交给主节点。
	wsURL := "ws" + strings.TrimPrefix(node.URL, "http") + "/backend-api/codex/call_sideband1"
	conn, resp, err := coderws.Dial(ctx, wsURL, &coderws.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer sk-a"}}})
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	for _, frame := range []string{`{"type":"response.create","model":"gpt-5"}`, `{"type":"response.create","model":"gpt-5","input":"again"}`} {
		require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(frame)))
		_, got, err := conn.Read(ctx)
		require.NoError(t, err)
		require.Equal(t, "echo:"+frame, string(got))
	}
	require.Equal(t, "Bearer sk-a", gotAuth, "the credential reaches the master unchanged")
	require.Equal(t, "/backend-api/codex/call_sideband1", gotPath)
	require.Equal(t, "127.0.0.1", gotXFF, "the client IP rides in X-Forwarded-For")
	require.NoError(t, conn.Close(coderws.StatusNormalClosure, ""))
}

// 不是转发的 API Key 接口（设计 8.4：用量、余额、模型清单、批量图片任务）从节点原样交给主节点执行：方法、路径、查询串、
// 请求体、凭据、响应状态和响应体都不变，客户端 IP 放在 X-Forwarded-For；从节点不替它们鉴权也不缓存。
func TestNonForwardingEndpointsAreHandedOffUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type seen struct{ method, path, query, auth, xff, body string }
	var got seen
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("X-Forwarded-For"), string(body)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"from":"master","path":"`+r.URL.Path+`"}`)
	}))
	t.Cleanup(master.Close)
	masterURL, err := url.Parse(master.URL)
	require.NoError(t, err)
	d := NewDispatcher(Deps{HandOff: NewHandOff(masterURL, http.DefaultTransport)})
	cfg := &config.Config{}
	cfg.Gateway.MaxBodySize = 1 << 20
	r := NewEngine()
	RegisterRoutes(r, nil, d, cfg, nil)
	node := httptest.NewServer(r)
	t.Cleanup(node.Close)

	for _, tc := range []struct{ method, path, query, body string }{
		{http.MethodGet, "/v1/models", "client_version=0.1", ""},
		{http.MethodGet, "/v1/models/gpt-5", "", ""},
		{http.MethodGet, "/v1beta/models", "", ""},
		{http.MethodGet, "/v1/usage", "", ""},
		{http.MethodGet, "/v1/sub2api/billing", "", ""},
		{http.MethodPost, "/v1/images/batches", "", `{"model":"gpt-image-1","requests":[]}`},
		{http.MethodGet, "/v1/images/batches/batch_1", "", ""},
		{http.MethodGet, "/v1/relay/assignment", "", ""},
	} {
		target := node.URL + tc.path
		if tc.query != "" {
			target += "?" + tc.query
		}
		req, err := http.NewRequest(tc.method, target, strings.NewReader(tc.body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer sk-a")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, tc.path)
		out, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusAccepted, resp.StatusCode, tc.path)
		require.JSONEq(t, `{"from":"master","path":"`+tc.path+`"}`, string(out), tc.path)
		require.Equal(t, seen{tc.method, tc.path, tc.query, "Bearer sk-a", "127.0.0.1", tc.body}, got, tc.path)
	}
}

// 交给主节点的请求带从节点自己签的标记（主节点分配比例为 0 时只接带标记的转发请求，设计 10.5）；客户端自己带的标记一律去掉。
func TestHandOffCarriesTheNodesOwnMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotMarker, gotMethod, gotPath string
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMarker, gotMethod, gotPath = r.Header.Get("X-Sub2api-Relay-Handoff"), r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(master.Close)
	masterURL, err := url.Parse(master.URL)
	require.NoError(t, err)
	signed := true
	signer := func(method, path string) string {
		if !signed {
			return ""
		}
		return "node-marker:" + method + ":" + path
	}
	d := NewDispatcher(Deps{HandOff: NewHandOff(masterURL, http.DefaultTransport, signer)})
	cfg := &config.Config{}
	cfg.Gateway.MaxBodySize = 1 << 20
	r := NewEngine()
	RegisterRoutes(r, nil, d, cfg, nil)
	node := httptest.NewServer(r)
	t.Cleanup(node.Close)

	do := func(clientMarker string) {
		req, err := http.NewRequest(http.MethodPost, node.URL+"/v1/live", strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer sk-a")
		if clientMarker != "" {
			req.Header.Set("X-Sub2api-Relay-Handoff", clientMarker)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
	}

	do("")
	require.Equal(t, "node-marker:POST:/v1/live", gotMarker)
	require.Equal(t, "POST", gotMethod)
	require.Equal(t, "/v1/live", gotPath)

	do("forged-by-client")
	require.Equal(t, "node-marker:POST:/v1/live", gotMarker, "the client's own marker is replaced")

	signed = false // 还没收到密钥：不带标记，客户端自己带的也去掉。
	do("forged-by-client")
	require.Empty(t, gotMarker)
}

// 设计 8.3：从节点只开放网关接口；登录态接口、管理后台、支付、网页、小白端配置接口一律 404，不转给主节点。
func TestHandOffOnlyServesTheGatewaySurface(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reached := map[string]bool{}
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached[r.URL.Path] = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(master.Close)
	masterURL, err := url.Parse(master.URL)
	require.NoError(t, err)
	d := NewDispatcher(Deps{HandOff: NewHandOff(masterURL, http.DefaultTransport)})
	cfg := &config.Config{}
	cfg.Gateway.MaxBodySize = 1 << 20
	r := NewEngine()
	RegisterRoutes(r, nil, d, cfg, nil)
	node := httptest.NewServer(r)
	t.Cleanup(node.Close)

	status := func(method, path string) int {
		req, err := http.NewRequest(method, node.URL+path, strings.NewReader("{}"))
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for _, path := range []string{"/v1/usage", "/v1/models", "/v1beta/models", "/v1/live", "/backend-api/codex/models", "/antigravity/v1/usage",
		"/models", "/usage", "/api/v3/contents/generations/tasks/x", "/v1/relay/assignment", "/v1/images/batches"} {
		require.Equal(t, http.StatusNoContent, status(http.MethodPost, path), path)
		require.True(t, reached[path], path)
	}
	for _, path := range []string{"/", "/api/v1/admin/users", "/api/v1/auth/login", "/api/v1/paw/config", "/api/v1/paw/auto-group", "/api/v1/playground/chat",
		"/api/v1/trial/x", "/api/v1/remote/ws", "/admin", "/setup", "/assets/app.js", "/v10/x", "/responsesx", "/payment/notify", "/api/v1/user/profile"} {
		require.Equal(t, http.StatusNotFound, status(http.MethodPost, path), path)
		require.False(t, reached[path], "%s must never reach the master", path)
	}
}
