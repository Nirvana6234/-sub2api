package relayselect

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 安全审计经审核连接在主节点判定（设计 3.4）：拦截的请求从节点照单机写出、不转发；放行的照常在从节点转发；
// 主节点拿到的是预先抽好的审核输入（不带请求体），用户、Key 按主节点复查的 Key。
func TestNodeRunsTheSecurityAuditOnTheMaster(t *testing.T) {
	e := startE2E(t)
	audit := &recordingAudit{decision: securityaudit.Decision{Kind: securityaudit.DecisionBlock, HTTPStatus: http.StatusForbidden,
		ErrorCode: "content_policy_violation", ClientMessage: "blocked on the master"}}
	e.world.sel.deps.Audit = audit
	e.world.sel.deps.PromptAudit = fixedPromptMode(securityaudit.ModeAsync)

	status, body := e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"please audit me"}`)
	require.Equal(t, http.StatusForbidden, status, body)
	require.Contains(t, body, "blocked on the master")
	require.Len(t, e.hits, 0, "a blocked request is not forwarded")
	got := audit.requests()
	require.Len(t, got, 1)
	require.Equal(t, int64(3), got[0].UserID)
	require.Equal(t, int64(11), got[0].APIKeyID)
	require.Equal(t, "/v1/responses", got[0].Endpoint)
	require.Equal(t, "gpt-5", got[0].Model)
	require.Equal(t, "http", got[0].Stage)
	require.NotEmpty(t, got[0].RequestID)
	require.Empty(t, got[0].Body, "the body stays on the node")
	snapshot, err := securityaudit.ExtractPromptSnapshot(got[0])
	require.NoError(t, err)
	require.Equal(t, "please audit me", snapshot.ScanText)
	require.Equal(t, int64(0), e.world.slots.held.Load(), "nothing was selected")

	audit.set(securityaudit.AllowDecision())
	status, body = e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"hi"}`)
	require.Equal(t, http.StatusOK, status, "an audited request is served by the node: %s", body)
	select {
	case <-e.hits:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Len(t, audit.requests(), 2)
	e.world.waitReleased(t)

	// 审计都关着：准入回"不审计"，从节点不调审核连接。
	e.world.sel.deps.PromptAudit = fixedPromptMode(securityaudit.ModeOff)
	status, body = e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"hi"}`)
	require.Equal(t, http.StatusOK, status, body)
	<-e.hits
	require.Len(t, audit.requests(), 2, "no audit call when nothing would audit the request")
	e.world.waitReleased(t)
}

// promptAudit 按提示词判定：含 forbidden 的拦截，其余放行。
type promptAudit struct {
	mu     sync.Mutex
	stages []string
}

func (p *promptAudit) Check(_ context.Context, req securityaudit.Request) securityaudit.Decision {
	p.mu.Lock()
	p.stages = append(p.stages, req.Stage)
	p.mu.Unlock()
	snapshot, err := securityaudit.ExtractPromptSnapshot(req)
	if err == nil && strings.Contains(snapshot.ScanText, "forbidden") {
		return securityaudit.Decision{Kind: securityaudit.DecisionBlock, HTTPStatus: http.StatusForbidden, ErrorCode: "content_policy_violation", ClientMessage: "turn blocked"}
	}
	return securityaudit.AllowDecision()
}

func (p *promptAudit) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.stages...)
}

// WebSocket：每一轮都经主节点审计（首轮 first_turn、之后 subsequent_turn），拦截的轮不转发并关闭连接，与单机一样。
func TestNodeAuditsEveryWebSocketTurnOnTheMaster(t *testing.T) {
	up := newWSUpstream(t)
	e := startE2EWithConfig(t, wsE2EConfig, func(string) []service.Account {
		a := wsAccount(1, "one")
		a.Credentials = map[string]any{"api_key": "SECRET-one", "base_url": up.srv.URL}
		return []service.Account{a}
	})
	audit := &promptAudit{}
	e.world.sel.deps.Audit = audit
	e.world.sel.deps.PromptAudit = fixedPromptMode(securityaudit.ModeAsync)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(e.gateway.URL, "http")+"/v1/responses",
		&coderws.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer sk-a"}}})
	require.NoError(t, err, "an audited connection is served by the node")
	defer func() { _ = conn.CloseNow() }()

	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[{"role":"user","content":"hi"}]}`)))
	first := readUntilCompleted(t, ctx, conn)
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","previous_response_id":"`+
		gjson.GetBytes(first, "response.id").String()+`","input":[{"role":"user","content":"something forbidden"}]}`)))
	var closeErr error
	for closeErr == nil {
		_, msg, err := conn.Read(ctx)
		if err != nil {
			closeErr = err
			break
		}
		require.NotEqual(t, "response.completed", gjson.GetBytes(msg, "type").String(), "the blocked turn must not be forwarded: %s", msg)
	}
	require.NotEqual(t, coderws.StatusCode(-1), coderws.CloseStatus(closeErr), "the node closes the connection: %v", closeErr)
	require.Equal(t, []string{"first_turn", "subsequent_turn"}, audit.seen())
	up.mu.Lock()
	require.Equal(t, 1, up.turns, "only the allowed turn reached the upstream")
	up.mu.Unlock()
	e.world.waitReleased(t)
}

// 连接期间才打开审计：审计在每一轮 BeginTurn 之前做，所以按上一轮 BeginTurn 回的策略，晚一轮生效
// （连接可以开几个小时，不能一直按建连时的策略）。
func TestWebSocketAuditPolicyFollowsTheLatestTurn(t *testing.T) {
	up := newWSUpstream(t)
	e := startE2EWithConfig(t, wsE2EConfig, func(string) []service.Account {
		a := wsAccount(1, "one")
		a.Credentials = map[string]any{"api_key": "SECRET-one", "base_url": up.srv.URL}
		return []service.Account{a}
	})
	audit := &promptAudit{}
	e.world.sel.deps.Audit = audit

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(e.gateway.URL, "http")+"/v1/responses",
		&coderws.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer sk-a"}}})
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	turn := func(text, previous string) []byte {
		frame := `{"type":"response.create","model":"gpt-5","input":[{"role":"user","content":"` + text + `"}]`
		if previous != "" {
			frame += `,"previous_response_id":"` + previous + `"`
		}
		require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(frame+"}")))
		return readUntilCompleted(t, ctx, conn)
	}
	first := turn("one", "")
	require.Empty(t, audit.seen(), "audit off at connect: no audit call")
	e.world.sel.deps.PromptAudit = fixedPromptMode(securityaudit.ModeAsync)
	second := turn("two", gjson.GetBytes(first, "response.id").String())
	require.Empty(t, audit.seen(), "turn 2 follows the policy from turn 1's admission")
	turn("three", gjson.GetBytes(second, "response.id").String())
	require.Equal(t, []string{"subsequent_turn"}, audit.seen(), "turn 3 follows the policy from turn 2's admission")
}
