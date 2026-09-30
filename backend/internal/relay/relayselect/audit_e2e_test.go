package relayselect

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// moderationConfig 是测试用的审核配置：关键词直接拦截（不调外部接口）、两次违规封号。
const moderationConfig = `{"enabled":true,"mode":"pre_block","all_groups":true,"keyword_blocking_mode":"keyword_only",` +
	`"blocked_keywords":["forbidden-word"],"auto_ban_enabled":true,"ban_threshold":2,"violation_window_hours":24,` +
	`"api_key":"sk-moderation-secret"}`

// 安全审计在从节点本地判定（设计 3.4）：审核配置（含审核接口 Key）经加密下发到达从节点；命中的请求在
// 从节点当场拦截、不转发；审核记录留在从节点本机；违规次数报给主节点累计，到阈值封号。
func TestNodeAuditsLocallyAndReportsViolations(t *testing.T) {
	useMasterSettings(t, map[string]string{
		service.SettingKeyRiskControlEnabled:      "true",
		service.SettingKeyContentModerationConfig: moderationConfig,
	})
	e := startE2E(t)
	ctx := context.Background()
	logs := &moderationLogs{}
	users := &banUsers{users: map[int64]*service.User{3: {ID: 3, Email: "u3@example.com", Status: service.StatusActive, Role: service.RoleUser}}}
	e.world.sel.deps.Users = users
	e.world.sel.deps.Moderation = service.NewContentModerationService(memSettings{values: map[string]string{
		service.SettingKeyRiskControlEnabled:      "true",
		service.SettingKeyContentModerationConfig: moderationConfig,
	}}, logs, nil, nil, users, nil, nil, nil)

	status, body := e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"hello there"}`)
	require.Equal(t, http.StatusOK, status, "a clean request is served by the node: %s", body)
	<-e.hits
	e.world.waitReleased(t)

	status, body = e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"this has a forbidden-word in it"}`)
	require.Equal(t, http.StatusForbidden, status, body)
	require.Len(t, e.hits, 0, "a blocked request is not forwarded")
	require.Equal(t, int64(0), e.world.slots.held.Load(), "blocked before selection")

	// 记录在从节点本机（审核服务后台写入），主节点收到一条不含输入内容的违规。
	var local []service.ContentModerationLog
	require.Eventually(t, func() bool {
		local, _, _ = e.moderation.Service.ListLogs(ctx, service.ContentModerationLogFilter{Pagination: pagination.PaginationParams{Page: 1, PageSize: 10}})
		return len(local) == 1
	}, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, "keyword_block", local[0].Action)
	require.Contains(t, local[0].InputExcerpt, "forbidden-word", "the node keeps the full record")
	require.Equal(t, 1, local[0].ViolationCount, "the count comes from the master")
	require.Eventually(t, func() bool {
		logs.mu.Lock()
		defer logs.mu.Unlock()
		return len(logs.logs) == 1
	}, 5*time.Second, 20*time.Millisecond)
	require.Empty(t, logs.logs[0].InputExcerpt, "input content never reaches the master")
	require.Equal(t, e.nodeID, *logs.logs[0].NodeID)

	// 第二次违规：主节点累计到阈值封号。
	status, _ = e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"again forbidden-word"}`)
	require.Equal(t, http.StatusForbidden, status)
	require.Eventually(t, func() bool {
		users.mu.Lock()
		defer users.mu.Unlock()
		return len(users.banned) == 1
	}, 5*time.Second, 20*time.Millisecond)
}

// 命中过的输入名单同步到从节点后，从节点不调外部审核接口也能直接拦截（与单机的哈希拦截一样）。
func TestNodeBlocksKnownFlaggedInputLocally(t *testing.T) {
	// 关键词之外还要过哈希和外部接口（keyword_and_api）：哈希命中时不再调外部接口。
	cfg := strings.Replace(moderationConfig, `"keyword_blocking_mode":"keyword_only",`, `"keyword_blocking_mode":"keyword_and_api","pre_hash_check_enabled":true,`, 1)
	useMasterSettings(t, map[string]string{
		service.SettingKeyRiskControlEnabled:      "true",
		service.SettingKeyContentModerationConfig: cfg,
	})
	e := startE2E(t)
	ctx := context.Background()
	body := `{"model":"gpt-5","input":"a previously flagged prompt"}`
	hash := service.ExtractContentModerationInput(service.ContentModerationProtocolOpenAIResponses, []byte(body)).Hash()
	cache := &scanHashes{set: []string{hash}}
	e.world.sel.deps.Moderation = service.NewContentModerationService(memSettings{values: map[string]string{service.SettingKeyRiskControlEnabled: "true"}},
		&moderationLogs{}, cache, nil, nil, nil, nil, nil)
	require.NoError(t, e.moderation.Hashes.Resync(ctx))

	status, out := e.post(t, "/v1/responses", "sk-a", body)
	require.Equal(t, http.StatusForbidden, status, out)
	require.Contains(t, out, hash, "blocked by the flagged-input list, like a single server")
	require.Len(t, e.hits, 0)
}

// WebSocket：每一轮在从节点本地审计，命中的轮不转发并关闭连接，与单机一样。
func TestNodeAuditsEveryWebSocketTurnLocally(t *testing.T) {
	useMasterSettings(t, map[string]string{
		service.SettingKeyRiskControlEnabled:      "true",
		service.SettingKeyContentModerationConfig: moderationConfig,
	})
	up := newWSUpstream(t)
	e := startE2EWithConfig(t, wsE2EConfig, func(string) []service.Account {
		a := wsAccount(1, "one")
		a.Credentials = map[string]any{"api_key": "SECRET-one", "base_url": up.srv.URL}
		return []service.Account{a}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(e.gateway.URL, "http")+"/v1/responses",
		&coderws.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer sk-a"}}})
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[{"role":"user","content":"hi"}]}`)))
	first := readUntilCompleted(t, ctx, conn)
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","previous_response_id":"`+
		gjson.GetBytes(first, "response.id").String()+`","input":[{"role":"user","content":"a forbidden-word here"}]}`)))
	var closeErr error
	for {
		_, msg, err := conn.Read(ctx)
		if err != nil {
			closeErr = err
			break
		}
		require.NotEqual(t, "response.completed", gjson.GetBytes(msg, "type").String(), "the blocked turn must not be forwarded: %s", msg)
	}
	require.NotEqual(t, coderws.StatusCode(-1), coderws.CloseStatus(closeErr), "the node closes the connection: %v", closeErr)
	up.mu.Lock()
	require.Equal(t, 1, up.turns)
	up.mu.Unlock()
	e.world.waitReleased(t)
}

// cyber 策略命中：会话屏蔽标记由主节点写（授权），风控记录留在从节点本机，计入封号的违规报给主节点累计。
func TestNodeKeepsCyberRecordsLocally(t *testing.T) {
	useMasterSettings(t, map[string]string{
		service.SettingKeyRiskControlEnabled:      "true",
		service.SettingKeyContentModerationConfig: moderationConfig,
	})
	e := startE2E(t)
	ctx := context.Background()
	marked := make(chan recordedCyber, 2)
	e.world.sel.recordCyber = func(hit handler.CyberPolicyHit, subj handler.CyberPolicySubject, scope string, keys []string) {
		marked <- recordedCyber{hit: hit, subj: subj, blockScope: scope, blockKeys: keys}
	}
	logs := &moderationLogs{}
	users := &banUsers{users: map[int64]*service.User{3: {ID: 3, Status: service.StatusActive, Role: service.RoleUser}}}
	e.world.sel.deps.Users = users
	e.world.sel.deps.Moderation = service.NewContentModerationService(memSettings{values: map[string]string{
		service.SettingKeyRiskControlEnabled:      "true",
		service.SettingKeyContentModerationConfig: moderationConfig,
	}}, logs, nil, nil, users, nil, nil, nil)

	status, _ := e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"cyber-trigger"}`)
	require.Equal(t, http.StatusBadRequest, status, "the client gets the upstream error, like a single server")
	select {
	case <-marked:
	case <-time.After(5 * time.Second):
		t.Fatal("the master was not asked to mark the session")
	}
	var local []service.ContentModerationLog
	require.Eventually(t, func() bool {
		local, _, _ = e.moderation.Service.ListLogs(ctx, service.ContentModerationLogFilter{Result: "hit"})
		return len(local) == 1
	}, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, service.ContentModerationActionCyberPolicy, local[0].Action)
	require.Contains(t, local[0].Error, "blocked by policy", "the upstream message stays on the node")
	require.Eventually(t, func() bool {
		logs.mu.Lock()
		defer logs.mu.Unlock()
		return len(logs.logs) == 1
	}, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, service.ContentModerationActionCyberPolicy, logs.logs[0].Action)
	require.Empty(t, logs.logs[0].Error)
}
