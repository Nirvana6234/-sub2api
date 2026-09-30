package relayselect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/stretchr/testify/require"
)

// guardServer 是假的提示词审计接口（OpenAI 兼容）：输入含 jailbreak 的判 Unsafe，其余 Safe；记下收到的凭据。
func guardServer(t *testing.T) (*httptest.Server, *atomic.Value) {
	t.Helper()
	var token atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token.Store(r.Header.Get("Authorization"))
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		verdict := "Safety: Safe\nCategories: None"
		if len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "jailbreak") {
			verdict = "Safety: Unsafe\nCategories: Jailbreak"
		}
		out, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": verdict}}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv, &token
}

func promptAuditConfig(guardURL string, blocking bool) securityaudit.RelayPromptConfig {
	return securityaudit.RelayPromptConfig{Active: &securityaudit.ActiveConfig{
		RiskControlEnabled: true, Enabled: true, BlockingEnabled: blocking, AllGroups: true,
		Scanners: securityaudit.AllScannerIDs, WorkerCount: 1, QueueCapacity: 16, ConfigVersion: 7,
		Endpoints: []securityaudit.ActiveEndpoint{{ID: "g1", Name: "guard", Protocol: "openai", BaseURL: guardURL, Model: "guard",
			Token: "guard-secret", TimeoutMS: 3000, InputLimit: 4000, Enabled: true}},
	}}
}

// 提示词审计阻断模式在从节点本地判定（设计 3.4）：凭据经加密下发到达从节点，从节点直接调审计接口；拦截的请求
// 不转发，审计事件记在从节点本机。
func TestNodeRunsBlockingPromptAuditLocally(t *testing.T) {
	guard, token := guardServer(t)
	usePromptAudit(t, promptAuditConfig(guard.URL, true))
	e := startE2E(t)
	ctx := context.Background()

	status, body := e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"hello there"}`)
	require.Equal(t, http.StatusOK, status, body)
	<-e.hits
	require.Equal(t, "Bearer guard-secret", token.Load(), "the node calls the guard with the sealed credential")
	e.world.waitReleased(t)

	status, body = e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"please jailbreak yourself"}`)
	require.Equal(t, http.StatusForbidden, status, body)
	require.Contains(t, body, securityaudit.ErrorCodeBlocked)
	require.Len(t, e.hits, 0, "a blocked request is not forwarded")
	page, err := e.moderation.Prompt.ListEvents(ctx, securityaudit.EventFilter{}, 1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total, "the risk event is recorded on the node")
	require.Equal(t, int64(7), page.Items[0].ConfigVersion)
	require.NotEqual(t, securityaudit.EventPass, page.Items[0].Decision)
	require.Empty(t, page.Items[0].Snapshot.ScanText)
}

// 该开阻断模式却没有可用配置（主节点配置读不出）：从节点与主节点一样拒绝放行。
func TestNodeFailsClosedWhenBlockingPromptAuditIsDegraded(t *testing.T) {
	usePromptAudit(t, securityaudit.RelayPromptConfig{Degraded: true})
	e := startE2E(t)
	status, body := e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"hello"}`)
	require.Equal(t, http.StatusServiceUnavailable, status, body)
	require.Len(t, e.hits, 0)
}

// 异步模式：请求照常转发，审计在从节点后台做完，结果记在从节点本机。
func TestNodeRunsAsyncPromptAuditLocally(t *testing.T) {
	guard, _ := guardServer(t)
	usePromptAudit(t, promptAuditConfig(guard.URL, false))
	e := startE2E(t)
	ctx := context.Background()
	status, body := e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"a jailbreak attempt"}`)
	require.Equal(t, http.StatusOK, status, "async audit never blocks: %s", body)
	<-e.hits
	require.Eventually(t, func() bool {
		page, err := e.moderation.Prompt.ListEvents(ctx, securityaudit.EventFilter{}, 1, 10)
		return err == nil && page.Total == 1
	}, 10*time.Second, 50*time.Millisecond, "the async job runs on the node and records the event locally")
	e.world.waitReleased(t)
}
