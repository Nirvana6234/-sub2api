package nodegw

import (
	"context"
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
	RegisterRoutes(r, nil, d, cfg)
	node := httptest.NewServer(r)
	t.Cleanup(node.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(node.URL, "http") + "/v1/responses"
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
	require.Equal(t, "/v1/responses", gotPath)
	require.Equal(t, "127.0.0.1", gotXFF, "the client IP rides in X-Forwarded-For")
	require.NoError(t, conn.Close(coderws.StatusNormalClosure, ""))
}
