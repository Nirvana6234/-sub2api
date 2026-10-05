package relayselect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const geminiPath = "/v1beta/models/gemini-2.5-pro:generateContent"

func geminiAccount(id int64, name string) service.Account {
	return service.Account{ID: id, Name: name, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 2, Credentials: map[string]any{"api_key": "SECRET-" + name, "base_url": "https://up.example"}}
}

func geminiSelectRequest(requestID string, attempt uint32, key, chain string) *relayv1.SelectRequest {
	return &relayv1.SelectRequest{RequestId: requestID, Attempt: attempt, Credential: &relayv1.SelectRequest_ApiKey{ApiKey: key},
		Method: "POST", Path: geminiPath, Endpoint: relayv1.SelectEndpoint_SELECT_ENDPOINT_GEMINI_NATIVE, Model: "gemini-2.5-pro",
		ClientIp: "5.6.7.8", UserAgent: "gemini-cli", GeminiDigestChain: chain}
}

// waitHit 等到上游收到一条满足条件的请求（Gemini 转发可能还有别的探测请求）。
func waitHit(t *testing.T, e *e2e, match func(*http.Request) bool) *http.Request {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case r := <-e.hits:
			if match(r) {
				return r
			}
		case <-deadline:
			t.Fatal("upstream was not called")
		}
	}
}

// postGoogle 按 Gemini SDK 的写法调用（x-goog-api-key）。
func (e *e2e) postGoogle(t *testing.T, path, key, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.gateway.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	if key != "" {
		req.Header.Set("x-goog-api-key", key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(out)
}

const geminiBody = `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`

// Gemini 原生入口经从节点（开发计划 WP10）：从节点用单机同一个处理函数和转发服务直连上游，会话、选号在主节点，
// 用量按 Anthropic 的记录种类记账（同一个 RecordUsage）；鉴权、错误按 Google 格式。
func TestNodeServesGeminiNative(t *testing.T) {
	e := startE2EWith(t, func(upstream string) []service.Account {
		a := geminiAccount(1, "gem")
		a.Credentials["base_url"] = upstream
		return []service.Account{a}
	})

	status, body := e.postGoogle(t, geminiPath, "sk-gemini", geminiBody)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "hello")
	hit := waitHit(t, e, func(r *http.Request) bool { return r.URL.Path == geminiPath })
	require.Equal(t, "SECRET-gem", hit.Header.Get("x-goog-api-key"), "the node forwards with the account's own credentials")
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	rec := e.settler.records()[0]
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_ANTHROPIC, rec.GetKind())
	require.Equal(t, "/v1beta/models", rec.GetInboundEndpoint())
	var result service.ForwardResult
	require.NoError(t, json.Unmarshal(rec.GetResultJson(), &result))
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	voucher, err := sign.VerifyVoucher(rec.GetVoucher(), e.world.pub, e.nodeID, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(10), voucher.GetGroupId())
	require.Equal(t, int64(1), voucher.GetAccountId())
	e.world.waitReleased(t)

	// 流式、Bearer 与查询参数 key 两种鉴权写法也走从节点。
	status, body = e.postGoogle(t, "/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse", "sk-gemini", geminiBody)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "hello")
	status, body = e.postGoogle(t, geminiPath+"?key=sk-gemini", "", geminiBody)
	require.Equal(t, http.StatusOK, status, body)
	e.world.waitReleased(t)
}

// 准入的拒绝按 Google 格式写（与本地 Google 鉴权链一致）；分组模型白名单按 URL 里的模型。
func TestNodeRejectsGeminiNativeLikeTheLocalChain(t *testing.T) {
	e := startE2EWith(t, func(upstream string) []service.Account {
		a := geminiAccount(1, "gem")
		a.Credentials["base_url"] = upstream
		return []service.Account{a}
	})
	status, body := e.postGoogle(t, geminiPath, "sk-nope", geminiBody)
	require.Equal(t, http.StatusUnauthorized, status)
	require.JSONEq(t, `{"error":{"code":401,"message":"Invalid API key","status":"UNAUTHENTICATED"}}`, body)
	status, body = e.postGoogle(t, geminiPath, "", geminiBody)
	require.Equal(t, http.StatusUnauthorized, status)
	require.JSONEq(t, `{"error":{"code":401,"message":"API key is required","status":"UNAUTHENTICATED"}}`, body)

	e.world.keys.keys["sk-gemini"].Group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gemini-2.5-flash"}}
	status, body = e.postGoogle(t, geminiPath, "sk-gemini", geminiBody)
	require.Equal(t, http.StatusNotFound, status, body)
	require.Contains(t, body, `"status":"NOT_FOUND"`)
	require.Len(t, e.hits, 0)
}

// 第一次就选不出账号：Google 格式，文案带调度器的错误（本地 GeminiV1BetaModels 同一句）。
func TestNodeWritesGeminiSelectionFailuresInGoogleFormat(t *testing.T) {
	e := startE2EWith(t, func(string) []service.Account { return nil })
	status, body := e.postGoogle(t, geminiPath, "sk-gemini", geminiBody)
	require.Equal(t, http.StatusServiceUnavailable, status, body)
	require.Contains(t, body, `"message":"No available Gemini accounts: `)
	require.Contains(t, body, `"code":503`)
	e.world.waitReleased(t)
}

// 换号：第一个账号上游回 429（本地换号、账号级限流作为账号事件交主节点照写），第二个账号成功。
func TestNodeFailsOverGeminiNativeAndReportsTheRateLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("the Gemini forward path retries a 429 with its real backoff (about 15s)")
	}
	accounts := []service.Account{geminiAccount(1, "limited"), geminiAccount(2, "fine")}
	repo := newRecordingAntigravityRepo(accounts)
	e := startE2EWith(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream + "/status-429"
		accounts[1].Credentials["base_url"] = upstream
		accounts[0].Priority, accounts[1].Priority = 1, 2
		return accounts
	})
	e.world.sel.deps.Gemini = service.NewGeminiMessagesCompatService(repo, nil, nil, nil, service.NewGeminiTokenProvider(nil, seededTokens{}, nil), nil, nil, nil, &config.Config{})

	status, body := e.postGoogle(t, geminiPath, "sk-gemini", geminiBody)
	require.Equal(t, http.StatusOK, status, body)
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, int64(2), mustVoucher(t, e, e.settler.records()[0]).GetAccountId())
	require.Eventually(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return !repo.rateLimited[1].IsZero()
	}, 5*time.Second, 20*time.Millisecond, "the 429 rate limit of the first account is written by the master")
	e.world.waitReleased(t)
}

func mustVoucher(t *testing.T, e *e2e, rec *relayv1.UsageRecord) *relayv1.Voucher {
	t.Helper()
	v, err := sign.VerifyVoucher(rec.GetVoucher(), e.world.pub, e.nodeID, time.Now())
	require.NoError(t, err)
	return v
}

// 会话整段在主节点：没有绑定账号时按内容摘要链匹配；转发成功（释放时）才保存，之后更长的同一段对话匹配到同一个账号并补上粘性绑定。
func TestGeminiNativeDigestSessionLivesOnTheMaster(t *testing.T) {
	ctx := context.Background()
	cache := &countingSticky{bound: map[string]int64{}}
	useGatewayCache(t, cache)
	w := newWorld(t, config.RunModeStandard, geminiAccount(1, "one"), geminiAccount(2, "two"))

	first, err := w.sel.Select(ctx, testNode, geminiSelectRequest("g1", 1, "sk-gemini", "u:aaaa"))
	require.NoError(t, err)
	sel := first.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", first.GetRejection())
	require.Zero(t, sel.GetStickyBoundAccountId(), "nothing is bound yet")
	require.NotEmpty(t, sel.GetSessionHash(), "a digest-derived session key is returned for the node to use")
	require.Equal(t, int32(3), sel.GetMaxAccountSwitches())
	picked := sel.GetAccount().GetId()
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true, ForwardSucceeded: true})
	w.waitReleased(t)

	// 同一段对话多了一轮：摘要链匹配到上次的账号，会话键与上次一致，粘性绑定补上。
	second, err := w.sel.Select(ctx, testNode, geminiSelectRequest("g2", 1, "sk-gemini", "u:aaaa-m:bbbb-u:cccc"))
	require.NoError(t, err)
	sel2 := second.GetSelection()
	require.NotNil(t, sel2, "rejection: %+v", second.GetRejection())
	require.Equal(t, picked, sel2.GetStickyBoundAccountId())
	require.Equal(t, picked, sel2.GetAccount().GetId())
	require.NotEmpty(t, sel2.GetSessionHash())
	require.Equal(t, picked, cache.bound[sel2.GetSessionHash()], "the matched account is bound to the session key")
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel2.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)

	// 另一个用户（另一把 Key）的同样对话不会匹配（前缀哈希按用户、Key、IP、UA、模型隔离）。
	w.keys.keys["sk-gemini-b"] = testKey("sk-gemini-b", 15, w.keys.keys["sk-gemini"].Group)
	third, err := w.sel.Select(ctx, testNode, geminiSelectRequest("g3", 1, "sk-gemini-b", "u:aaaa-m:bbbb-u:cccc"))
	require.NoError(t, err)
	require.NotNil(t, third.GetSelection(), "rejection: %+v", third.GetRejection())
	require.Zero(t, third.GetSelection().GetStickyBoundAccountId())
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: third.GetSelection().GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
}

// 带了 CLI / 通用会话哈希时会话键是 "gemini:"+哈希，请求开始时查一次粘性绑定，之后的换号沿用同一个结果。
func TestGeminiNativeSessionHashAndStickyBinding(t *testing.T) {
	ctx := context.Background()
	cache := &countingSticky{bound: map[string]int64{"gemini:cli-hash": 2}}
	useGatewayCache(t, cache)
	w := newWorld(t, config.RunModeStandard, geminiAccount(1, "one"), geminiAccount(2, "two"))

	req := geminiSelectRequest("g1", 1, "sk-gemini", "")
	req.SessionHash = "cli-hash"
	resp, err := w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", resp.GetRejection())
	require.Equal(t, "gemini:cli-hash", sel.GetSessionHash())
	require.Equal(t, int64(2), sel.GetStickyBoundAccountId())

	// 这个账号失败后换号：粘性绑定仍是请求开始时查到的那个，选到另一个账号。
	retry := geminiSelectRequest("g1", 2, "sk-gemini", "")
	retry.SessionHash = "cli-hash"
	retry.ExcludedAccountIds = []int64{2}
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId()})
	resp, err = w.sel.Select(ctx, testNode, retry)
	require.NoError(t, err)
	sel2 := resp.GetSelection()
	require.NotNil(t, sel2, "rejection: %+v", resp.GetRejection())
	require.Equal(t, int64(1), sel2.GetAccount().GetId())
	require.Equal(t, int64(2), sel2.GetStickyBoundAccountId())
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel2.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
}

// Gemini 入口选不出账号、账号都被排除、准入失败时的拒绝按 Gemini 的文案（状态码、消息），格式由从节点按 Google 写。
func TestGeminiNativeRejectionsUseGeminiWording(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeStandard)
	resp, err := w.sel.Select(ctx, testNode, geminiSelectRequest("g1", 1, "sk-gemini", ""))
	require.NoError(t, err)
	rej := resp.GetRejection()
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_GATEWAY, rej.GetFormat())
	require.Equal(t, int32(http.StatusServiceUnavailable), rej.GetStatus())
	require.True(t, strings.HasPrefix(rej.GetMessage(), "No available Gemini accounts: "), rej.GetMessage())

	// 非 Gemini 分组走这个入口：交给主节点（它会回本地的 400）。
	resp, err = w.sel.Select(ctx, testNode, func() *relayv1.SelectRequest {
		r := geminiSelectRequest("g2", 1, "sk-a", "")
		return r
	}())
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED, resp.GetRejection().GetFormat())
	require.Equal(t, int64(0), w.slots.held.Load())
}

// 从节点上报的 Gemini 账号状态：账号级限流（同账号的 Gemini 转发路径）、档位冷却、清粘性绑定（只认这次请求自己的会话键）。
func TestGeminiAccountEventsAreAppliedOnTheMaster(t *testing.T) {
	ctx := context.Background()
	accounts := []service.Account{geminiAccount(1, "one")}
	repo := newRecordingAntigravityRepo(accounts)
	cache := &countingSticky{bound: map[string]int64{"gemini:cli-hash": 1}}
	useGatewayCache(t, cache)
	w := newWorld(t, config.RunModeStandard, accounts...)
	w.sel.deps.Gemini = service.NewGeminiMessagesCompatService(repo, nil, cache, nil, service.NewGeminiTokenProvider(nil, seededTokens{}, nil), nil, nil, nil, &config.Config{})

	req := geminiSelectRequest("g1", 1, "sk-gemini", "")
	req.SessionHash = "cli-hash"
	resp, err := w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", resp.GetRejection())

	until := time.Now().Add(time.Hour)
	w.sel.AccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_RateLimited{RateLimited: &relayv1.RateLimitedEvent{ResetAtUnixMs: until.UnixMilli()}}})
	require.Eventually(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return repo.rateLimited[1].After(time.Now())
	}, 2*time.Second, 5*time.Millisecond)

	// 别的会话键不认；这次请求自己的会话键才清。
	w.sel.AccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_StickyCleared{StickyCleared: &relayv1.StickySessionClearedEvent{SessionKey: "gemini:someone-else"}}})
	w.sel.AccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_StickyCleared{StickyCleared: &relayv1.StickySessionClearedEvent{SessionKey: "gemini:cli-hash"}}})
	require.Eventually(t, func() bool { return cache.deleted("gemini:cli-hash") }, 2*time.Second, 5*time.Millisecond)
	require.False(t, cache.deleted("gemini:someone-else"))

	// 节点在换号、请求结束时先释放这次选号，清绑定的事件才到：仍认这次请求自己的会话键（刚用过这个账号）。
	cache.mu.Lock()
	cache.bound["gemini:cli-hash"] = 1
	cache.mu.Unlock()
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
	w.sel.AccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_StickyCleared{StickyCleared: &relayv1.StickySessionClearedEvent{SessionKey: "gemini:someone-else"}}})
	w.sel.AccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_StickyCleared{StickyCleared: &relayv1.StickySessionClearedEvent{SessionKey: "gemini:cli-hash"}}})
	require.Eventually(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		_, bound := cache.bound["gemini:cli-hash"]
		return !bound
	}, 2*time.Second, 5*time.Millisecond, "an event that arrives after the release is still honored")
}

// 请求体读失败（超限）：Gemini 原生入口本地的链路里没有中间件读请求体，由处理函数读时按 Google 格式报错。
func TestNodeWritesGeminiBodyTooLargeInGoogleFormat(t *testing.T) {
	e := startE2EWith(t, func(upstream string) []service.Account {
		a := geminiAccount(1, "gem")
		a.Credentials["base_url"] = upstream
		return []service.Account{a}
	})
	big := `{"contents":[{"role":"user","parts":[{"text":"` + strings.Repeat("x", 11<<20) + `"}]}]}`
	status, body := e.postGoogle(t, geminiPath, "sk-gemini", big)
	require.Equal(t, http.StatusRequestEntityTooLarge, status)
	require.Contains(t, body, `"code":413`)
	require.Contains(t, body, "Request body too large, limit is")
	require.Len(t, e.hits, 0)
	e.world.waitReleased(t)
}
