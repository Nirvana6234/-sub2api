package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 回归测试对应 openai_gateway_handler.go 里 IsOpenAIWSSessionPreemptedError 分支：
// 同账号并发开第二个任务会取消第一个任务正在使用的 preemptCtx，之前直接 return
// 后交给 defer wsConn.CloseNow() 硬断连接，客户端收不到任何终止事件，Codex CLI
// 报 "websocket closed by server before response.completed"。
// 修复后应先写一帧合法的 response.failed 终止事件。这里对 writeResponsesFailedWS
// 做单测，验证它产出的 WS 帧是客户端能识别的合法 response.failed 终止事件。
//
// 注意：调用方必须传 context.Background()（调用点已经这么做），不能传已经被
// 抢占取消的 preemptCtx——context.WithTimeout 在一个已取消的 parent 上派生出的
// 子 context 会立即处于 Done 状态，conn.Write 会随之随机失败/不发送，这不是
// writeResponsesFailedWS 自身能兜底的事，是调用点的责任。
func TestWriteResponsesFailedWS_SendsRecognizableTerminalEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	acceptErrCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			acceptErrCh <- err
			return
		}
		acceptErrCh <- nil
		defer conn.CloseNow()

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r

		// 镜像真实调用点：session_preempted 分支传的是 context.Background()，不是
		// 已经被取消的 preemptCtx。
		writeResponsesFailedWS(context.Background(), conn, c, "server_error",
			"session preempted by a newer concurrent request on the same account/API key")

		// 保持连接存活，直到客户端读完消息再由测试主动关闭。
		<-r.Context().Done()
	}))
	defer server.Close()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer clientConn.CloseNow()

	require.NoError(t, <-acceptErrCh, "server failed to accept the websocket upgrade")

	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	msgType, data, err := clientConn.Read(readCtx)
	require.NoError(t, err, "client should receive a response.failed frame instead of a bare close")
	require.Equal(t, coderws.MessageText, msgType)

	body := gjson.ParseBytes(data)
	require.Equal(t, "response.failed", body.Get("type").String())
	require.Equal(t, "failed", body.Get("response.status").String())
	require.Equal(t, "server_error", body.Get("response.error.code").String())
	require.Contains(t, body.Get("response.error.message").String(), "preempted")
}
