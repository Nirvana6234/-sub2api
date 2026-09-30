package relayselect

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodegw"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
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

// 从节点还不能转发的账号类型（这里是 OAuth）：交给主节点（测试世界没有主节点转发，按 503 写），上游不被调用。
func TestNodeHandsOffAnthropicAccountTypesNotServedYet(t *testing.T) {
	e := startE2EWith(t, func(upstream string) []service.Account {
		a := anthropicAccount(1, "oauth", service.AccountTypeOAuth)
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
