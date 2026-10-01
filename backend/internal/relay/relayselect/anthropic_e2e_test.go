package relayselect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodegw"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Anthropic 分组的 /v1/messages 经从节点（开发计划 WP10）：主节点选号，从节点用单机同一个处理函数和转发服务
// 直连上游，用量按 Anthropic 的记录种类记账（主节点用 GatewayService.RecordUsage 入账）。
func TestNodeServesAnthropicMessages(t *testing.T) {
	e := startE2EWith(t, func(upstream string) []service.Account {
		a := anthropicAccount(1, "claude", service.AccountTypeAPIKey)
		a.Credentials["base_url"] = upstream
		return []service.Account{a}
	})

	status, body := e.post(t, "/v1/messages", "sk-anthropic", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "hello")
	select {
	case r := <-e.hits:
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, "SECRET-claude", r.Header.Get("x-api-key"), "the node forwards with the account's own credentials")
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	rec := e.settler.records()[0]
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_ANTHROPIC, rec.GetKind())
	require.Equal(t, "/v1/messages", rec.GetInboundEndpoint())
	require.NotEmpty(t, rec.GetRequestPayloadHash())
	var result service.ForwardResult
	require.NoError(t, json.Unmarshal(rec.GetResultJson(), &result))
	require.Equal(t, 5, result.Usage.InputTokens)
	voucher, err := sign.VerifyVoucher(rec.GetVoucher(), e.world.pub, e.nodeID, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(9), voucher.GetGroupId())
	require.Equal(t, int64(1), voucher.GetAccountId())
	e.world.waitReleased(t)
}

// 从节点不接的账号：主节点没装 Antigravity 转发服务时，混合调度进来的 Antigravity 账号交给主节点：交给主节点（测试世界没有主节点转发，按 503 写），上游不被调用。
func TestNodeHandsOffAnthropicAccountTypesNotServedYet(t *testing.T) {
	e := startE2EWith(t, func(upstream string) []service.Account {
		a := antigravityMixedAccount(1)
		a.Credentials["base_url"] = upstream
		return []service.Account{a}
	})
	status, body := e.post(t, "/v1/messages", "sk-anthropic", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, http.StatusServiceUnavailable, status, body)
	require.Len(t, e.hits, 0)
	e.world.waitReleased(t)
}

// countingSticky 记下每次粘性会话绑定。
type countingSticky struct {
	nodegw.NoopGatewayCache
	mu    sync.Mutex
	bound map[string]int64
	sets  int
}

func (c *countingSticky) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.bound[key]; ok {
		return id, nil
	}
	return 0, service.ErrStickySessionNotFound
}

func (c *countingSticky) SetSessionAccountID(_ context.Context, _ int64, key string, id int64, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bound[key] = id
	c.sets++
	return nil
}

// 转发成功后的粘性会话绑定（本地同一条件）：请求开始时没有绑定或者绑定的就是这个账号时刷新；
// 绑定的账号因负载被跳过、换了别的账号时不覆盖。
func TestReleaseRebindsStickySessionLikeTheLocalHandler(t *testing.T) {
	ctx := context.Background()
	cache := &countingSticky{bound: map[string]int64{}}
	useGatewayCache(t, cache)
	w := newWorld(t, config.RunModeStandard, anthropicAccount(1, "one", service.AccountTypeAPIKey))
	sets := func() int {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return cache.sets
	}

	resp, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("s1", 1, "sk-anthropic"))
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", resp.GetRejection())
	before := sets()
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true, ForwardSucceeded: true})
	require.Eventually(t, func() bool { return sets() == before+1 }, 2*time.Second, 5*time.Millisecond, "a successful forward refreshes the binding")
	w.waitReleased(t)

	// 请求开始时绑定的是别的账号（被跳过了）：不覆盖。
	cache.mu.Lock()
	cache.bound["session-1"] = 99
	cache.mu.Unlock()
	resp, err = w.sel.Select(ctx, testNode, anthropicMessagesRequest("s2", 1, "sk-anthropic"))
	require.NoError(t, err)
	sel = resp.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", resp.GetRejection())
	require.Equal(t, int64(99), sel.GetStickyBoundAccountId())
	before = sets()
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true, ForwardSucceeded: true})
	w.waitReleased(t)
	require.Equal(t, before, sets(), "the release does not rebind (the scheduler already handled the missing account during selection, like a single server)")
}

// tempUnschedAccounts 记下临时不可调度的写入。
type tempUnschedAccounts struct {
	fakeAccounts
	mu    sync.Mutex
	until map[int64]time.Time
}

func (r *tempUnschedAccounts) SetTempUnschedulable(_ context.Context, id int64, until time.Time, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.until[id] = until
	return nil
}

// 从节点转发路径上的临时不可调度（本地直接写账号仓储）：主节点照写，时长不超过本地用的最长时长；
// 这台节点没在用的账号不认。
func TestTempUnschedulableEventFromNode(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeStandard, anthropicAccount(1, "one", service.AccountTypeAPIKey))
	repo := &tempUnschedAccounts{fakeAccounts: fakeAccounts{accounts: []service.Account{anthropicAccount(1, "one", service.AccountTypeAPIKey)}}, until: map[int64]time.Time{}}
	w.sel.deps.AnthropicGateway = service.NewGatewayService(repo, nil, nil, nil, nil, nil, nil, nil, w.sel.deps.Config,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	event := func(until time.Time) *relayv1.AccountEvent {
		return &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_TempUnschedulable{
			TempUnschedulable: &relayv1.TempUnschedulableEvent{UntilUnixMs: until.UnixMilli(), Reason: "empty stream response"},
		}}
	}

	w.sel.applyAccountEvent(testNode, event(time.Now().Add(time.Minute)))
	require.Empty(t, repo.until, "an account the node is not using is ignored")

	resp, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("t1", 1, "sk-anthropic"))
	require.NoError(t, err)
	require.NotNil(t, resp.GetSelection(), "rejection: %+v", resp.GetRejection())
	w.sel.applyAccountEvent(testNode, event(time.Now().Add(time.Hour)))
	repo.mu.Lock()
	until := repo.until[1]
	repo.mu.Unlock()
	require.WithinDuration(t, time.Now().Add(maxRelayTempUnschedulable), until, 5*time.Second, "capped at the longest local duration")
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: resp.GetSelection().GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
}

// 从节点上照本地的换号循环：第一个账号 429，换号（带上已失败的账号）后第二个账号成功；没有绑定的会话，不强制按缓存计费。
func TestNodeAnthropicFailsOverToTheNextAccount(t *testing.T) {
	e := startE2EWith(t, func(upstream string) []service.Account {
		limited := anthropicAccount(1, "limited", service.AccountTypeAPIKey)
		limited.Credentials["base_url"] = upstream + "/status-429"
		limited.Priority = 1
		fine := anthropicAccount(2, "fine", service.AccountTypeAPIKey)
		fine.Credentials["base_url"] = upstream
		fine.Priority = 5
		return []service.Account{limited, fine}
	})

	status, body := e.post(t, "/v1/messages", "sk-anthropic", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	require.Eventually(t, func() bool {
		for _, rec := range e.settler.records() {
			v, err := sign.VerifyVoucher(rec.GetVoucher(), e.world.pub, e.nodeID, time.Now())
			if err == nil && v.GetAccountId() == 2 {
				require.False(t, rec.GetForceCacheBilling())
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "served by the second account")
	require.GreaterOrEqual(t, len(e.hits), 2, "the rate-limited account was tried first")
	e.world.waitReleased(t)
}

// OAuth 账号经从节点（开发计划 WP10，身份信息在主节点）：主节点按客户端的指纹头做与本地同一段 GetOrCreateFingerprint、
// 取 access token，从节点照本地的伪装转发；用到的伪装会话 ID 报回主节点写入。
func TestNodeForwardsAnthropicOAuthWithTheMastersIdentity(t *testing.T) {
	e := startE2EWith(t, func(upstream string) []service.Account {
		a := anthropicAccount(1, "oauth", service.AccountTypeOAuth)
		a.Credentials = map[string]any{"access_token": "SECRET-at"}
		a.Extra = map[string]any{"account_uuid": "11111111-2222-3333-4444-555555555555", "session_id_masking_enabled": true}
		return []service.Account{a}
	})

	req, err := http.NewRequest(http.MethodPost, e.gateway.URL+"/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer sk-anthropic")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Stainless-OS", "Linux")
	req.Header.Set("X-Stainless-Arch", "arm64")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))

	var hit *http.Request
	select {
	case hit = <-e.hits:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Equal(t, "Bearer SECRET-at", hit.Header.Get("Authorization"), "the master's access token")

	e.world.identity.mu.Lock()
	fp := e.world.identity.fingerprints[1]
	e.world.identity.mu.Unlock()
	require.NotNil(t, fp, "the fingerprint is created on the master")
	require.Equal(t, "Linux", fp.StainlessOS)
	require.Equal(t, fp.StainlessArch, hit.Header.Get("X-Stainless-Arch"), "the node applies the master's fingerprint")
	sent, _ := io.ReadAll(hit.Body)
	userID := gjson.GetBytes(sent, "metadata.user_id").String()
	require.Contains(t, userID, fp.ClientID, "metadata.user_id carries the master's client id")

	require.Eventually(t, func() bool {
		e.world.identity.mu.Lock()
		defer e.world.identity.mu.Unlock()
		return e.world.identity.masked[1] != ""
	}, 5*time.Second, 20*time.Millisecond, "the masked session id the node used is written on the master")
	e.world.identity.mu.Lock()
	masked := e.world.identity.masked[1]
	e.world.identity.mu.Unlock()
	require.Contains(t, userID, masked)
	e.world.waitReleased(t)
}

// Vertex 服务账号经从节点：服务账号文件不下发，主节点换好的 token 和只写在文件里的项目随凭据下发，从节点照本地转发。
func TestNodeForwardsAnthropicVertexServiceAccounts(t *testing.T) {
	e := startE2EWith(t, func(string) []service.Account {
		a := anthropicAccount(1, "vertex", service.AccountTypeServiceAccount)
		a.Credentials = map[string]any{
			"service_account_json": `{"type":"service_account","client_email":"relay@proj.iam.gserviceaccount.com",` +
				`"private_key":"SECRET-PRIVATE-KEY","private_key_id":"k1","project_id":"proj-from-file"}`,
			"location": "us-east5",
		}
		return []service.Account{a}
	})

	status, body := e.post(t, "/v1/messages", "sk-anthropic", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	select {
	case r := <-e.hits:
		require.Equal(t, "Bearer SECRET-vertex-token", r.Header.Get("Authorization"), "the master's exchanged token")
		require.Contains(t, r.URL.Path, "/projects/proj-from-file/locations/us-east5/publishers/anthropic/models/")
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	e.world.waitReleased(t)
}

// count_tokens 经从节点：主节点查计费资格、按模型选账号（不占槽），从节点直连上游；不计费，没有扣费记录。
func TestNodeServesAnthropicCountTokens(t *testing.T) {
	e := startE2EWith(t, func(upstream string) []service.Account {
		a := anthropicAccount(1, "claude", service.AccountTypeAPIKey)
		a.Credentials["base_url"] = upstream
		return []service.Account{a}
	})
	for _, path := range []string{"/v1/messages/count_tokens", "/messages/count_tokens"} {
		status, body := e.post(t, path, "sk-anthropic", `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`)
		require.Equal(t, http.StatusOK, status, "%s: %s", path, body)
		require.JSONEq(t, `{"input_tokens":7}`, body)
		select {
		case r := <-e.hits:
			require.Equal(t, "/v1/messages/count_tokens", r.URL.Path)
			require.Equal(t, "SECRET-claude", r.Header.Get("x-api-key"))
		case <-time.After(5 * time.Second):
			t.Fatal("upstream was not called")
		}
	}
	e.world.waitReleased(t)
	require.Empty(t, e.settler.records(), "count_tokens is not billed")
	require.Zero(t, e.world.slots.userAcquires.Load(), "count_tokens takes no user slot")
}

// Bedrock（API Key 模式）经从节点：凭据随选号加密下发，从节点照本地转发到 Bedrock。
func TestNodeForwardsAnthropicBedrockAccounts(t *testing.T) {
	e := startE2EWith(t, func(string) []service.Account {
		a := anthropicAccount(1, "bedrock", service.AccountTypeBedrock)
		a.Credentials = map[string]any{"auth_mode": "apikey", "api_key": "SECRET-bedrock-key", "aws_region": "us-east-1"}
		return []service.Account{a}
	})
	status, body := e.post(t, "/v1/messages", "sk-anthropic", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	select {
	case r := <-e.hits:
		require.True(t, strings.HasPrefix(r.URL.Path, "/model/"), r.URL.Path)
		require.Equal(t, "Bearer SECRET-bedrock-key", r.Header.Get("Authorization"))
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	e.world.waitReleased(t)
}

// 开了用户消息串行队列的 OAuth 账号经从节点：从节点照本地的排队代码拿锁、转发、放锁，锁在主节点。
func TestNodeSerializesUserMessagesThroughTheMaster(t *testing.T) {
	e := startE2EWith(t, func(string) []service.Account {
		a := anthropicAccount(1, "oauth", service.AccountTypeOAuth)
		a.Credentials = map[string]any{"access_token": "SECRET-at"}
		a.Extra = map[string]any{"user_msg_queue_mode": "serialize"}
		return []service.Account{a}
	})
	queue := &memUserMsgQueue{}
	e.world.sel.deps.UserMsgQueue = queue

	status, body := e.post(t, "/v1/messages", "sk-anthropic", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	select {
	case <-e.hits:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Eventually(t, func() bool {
		acquired, released, held := queue.counts()
		return acquired == 1 && released == 1 && held == 0
	}, 5*time.Second, 20*time.Millisecond, "the node took the master's lock for the account and gave it back")
	e.world.waitReleased(t)
}

// 没有分组的 Key（后台允许未分组 Key 调度时，本地走 Anthropic 网关按"未分组账号"选号）经从节点：只有 Anthropic 的
// 入口接，其他入口交给主节点；用量没有分组。
func TestNodeServesUngroupedKeys(t *testing.T) {
	e := startStandardE2E(t, func(upstream string) []service.Account {
		a := anthropicAccount(1, "claude", service.AccountTypeAPIKey)
		a.Credentials["base_url"] = upstream
		return []service.Account{a}
	})
	e.world.sel.deps.Settings = service.NewSettingService(memSettings{values: map[string]string{service.SettingKeyAllowUngroupedKeyScheduling: "true"}}, e.world.sel.deps.Config)
	e.world.keys.keys["sk-ungrouped"] = testKey("sk-ungrouped", 18, nil)

	status, body := e.post(t, "/v1/messages", "sk-ungrouped", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	select {
	case <-e.hits:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	voucher, err := sign.VerifyVoucher(e.settler.records()[0].GetVoucher(), e.world.pub, e.nodeID, time.Now())
	require.NoError(t, err)
	require.Zero(t, voucher.GetGroupId())
	e.world.waitReleased(t)

	// OpenAI 的入口不接未分组 Key：交给主节点（测试世界没有主节点转发，按 503 写）。
	status, body = e.post(t, "/v1/responses", "sk-ungrouped", `{"model":"gpt-5","input":"hi"}`)
	require.Equal(t, http.StatusServiceUnavailable, status, body)
}
