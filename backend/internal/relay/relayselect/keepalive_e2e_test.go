package relayselect

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 选号在主节点排队时给客户端保活：选号是一次阻塞的调用，从节点按本地排队时同一个回调每个间隔发一次 SSE ping
// （OpenAI 是注释行，Anthropic 是 ping 事件），排完队才开始转发；响应不被 ping 破坏。
func TestNodeSendsKeepaliveWhileTheMasterQueuesTheSelection(t *testing.T) {
	accounts := []service.Account{apiKeyAccount(1, "one"), anthropicAccount(2, "claude", service.AccountTypeAPIKey)}
	accounts[1].AccountGroups = []service.AccountGroup{{AccountID: 2, GroupID: 9}}
	e := startE2EWithConfig(t, func(cfg *config.Config) {
		cfg.RunMode = config.RunModeStandard
		cfg.Concurrency.PingInterval = 1
	}, func(upstream string) []service.Account {
		for i := range accounts {
			accounts[i].Credentials["base_url"] = upstream
		}
		accounts[0].AccountGroups = []service.AccountGroup{{AccountID: 1, GroupID: 5}}
		return accounts
	})
	e.world.sel.onSelect = func(*relayv1.SelectRequest) { time.Sleep(2500 * time.Millisecond) }

	for _, tc := range []struct{ name, path, key, body, ping string }{
		{"openai responses", "/v1/responses", "sk-a", `{"model":"gpt-5","input":"hi","stream":true}`, ":\n\n"},
		{"openai chat", "/v1/chat/completions", "sk-a", `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":true}`, ":\n\n"},
		{"anthropic messages", "/v1/messages", "sk-anthropic", `{"model":"claude-sonnet-4-5","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, `data: {"type": "ping"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, e.gateway.URL+tc.path, strings.NewReader(tc.body))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+tc.key)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			out, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode, string(out))
			require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
			require.True(t, strings.HasPrefix(string(out), tc.ping), "the stream starts with keepalive pings: %q", string(out))
			require.Contains(t, string(out), "hello", "the real response follows the pings")
			e.world.waitReleased(t)
		})
	}
}
