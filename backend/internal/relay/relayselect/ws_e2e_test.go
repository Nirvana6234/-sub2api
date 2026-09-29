package relayselect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// wsUpstream 是 Responses WebSocket 的假上游：每收到一个 response.create 回 response.created 和带用量的
// response.completed。
type wsUpstream struct {
	srv   *httptest.Server
	mu    sync.Mutex
	auths []string
	turns int
}

func newWSUpstream(t *testing.T) *wsUpstream {
	u := &wsUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		u.mu.Lock()
		u.auths = append(u.auths, r.Header.Get("Authorization"))
		u.mu.Unlock()
		for {
			_, msg, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if gjson.GetBytes(msg, "type").String() != "response.create" {
				continue
			}
			u.mu.Lock()
			u.turns++
			id := "resp_ws_" + string(rune('0'+u.turns))
			u.mu.Unlock()
			model := gjson.GetBytes(msg, "model").String()
			_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.created","response":{"id":"`+id+`","model":"`+model+`","status":"in_progress"}}`))
			_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"`+id+`","model":"`+model+`","status":"completed","usage":{"input_tokens":21,"output_tokens":4,"total_tokens":25}}}`))
		}
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func wsE2EConfig(c *config.Config) {
	c.Gateway.OpenAIWS.Enabled = true
	c.Gateway.OpenAIWS.OAuthEnabled = true
	c.Gateway.OpenAIWS.APIKeyEnabled = true
	c.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	c.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	c.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	c.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	c.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey = 4
}

// readUntilCompleted 读到这一轮的 response.completed（或出错）。
func readUntilCompleted(t *testing.T, ctx context.Context, conn *coderws.Conn) []byte {
	t.Helper()
	for {
		_, msg, err := conn.Read(ctx)
		require.NoError(t, err)
		switch gjson.GetBytes(msg, "type").String() {
		case "response.completed":
			return msg
		case "error", "response.failed":
			t.Fatalf("unexpected event: %s", msg)
		}
	}
}

// 端到端：Codex 经从节点的 Responses WebSocket。连接选号和每一轮准入在主节点，帧在从节点和上游之间转发；
// 每一轮一张凭证、一条用量；连接关闭后主节点上的槽、租约都放掉。
func TestNodeServesResponsesWebSocketEndToEnd(t *testing.T) {
	logger.InitBootstrap()
	up := newWSUpstream(t)
	e := startE2EWithConfig(t, wsE2EConfig, func(string) []service.Account {
		a := wsAccount(1, "one")
		a.Credentials = map[string]any{"api_key": "SECRET-one", "base_url": up.srv.URL}
		return []service.Account{a}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(e.gateway.URL, "http") + "/v1/responses"
	conn, resp, err := coderws.Dial(ctx, wsURL, &coderws.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer sk-a"}}})
	require.NoError(t, err, "upgrade through the node")
	defer func() { _ = conn.CloseNow() }()
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[{"role":"user","content":"hi"}]}`)))
	first := readUntilCompleted(t, ctx, conn)
	require.Equal(t, int64(21), gjson.GetBytes(first, "response.usage.input_tokens").Int())
	// 两轮之间不占槽（本地 AfterTurn 放掉这一轮的用户槽和账号槽）。
	e.world.waitReleased(t)
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","previous_response_id":"`+gjson.GetBytes(first, "response.id").String()+`","input":[{"role":"user","content":"again"}]}`)))
	readUntilCompleted(t, ctx, conn)

	up.mu.Lock()
	require.NotEmpty(t, up.auths)
	require.Equal(t, "Bearer SECRET-one", up.auths[0], "the node decrypted the upstream key")
	up.mu.Unlock()

	// 每一轮一条扣费记录、各自一张凭证。
	require.Eventually(t, func() bool { return len(e.settler.records()) == 2 }, 5*time.Second, 20*time.Millisecond)
	recs := e.settler.records()
	for _, rec := range recs {
		require.NotEmpty(t, rec.GetVoucher())
		require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI, rec.GetKind())
		require.Equal(t, "/v1/responses", rec.GetInboundEndpoint())
	}
	require.NotEqual(t, recs[0].GetVoucher(), recs[1].GetVoucher(), "one voucher per turn")

	// 两轮之间槽已经放了；关闭连接后主节点上的一切都放掉。
	require.NoError(t, conn.Close(coderws.StatusNormalClosure, ""))
	e.world.waitReleased(t)
	require.Eventually(t, func() bool {
		e.world.slots.leaseMu.Lock()
		defer e.world.slots.leaseMu.Unlock()
		return len(e.world.slots.leases) == 0
	}, 5*time.Second, 20*time.Millisecond, "the connection lease is released")
}
