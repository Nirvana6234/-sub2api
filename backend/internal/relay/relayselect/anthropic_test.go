package relayselect

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func anthropicAccount(id int64, name, typ string) service.Account {
	return service.Account{ID: id, Name: name, Platform: service.PlatformAnthropic, Type: typ,
		Status: service.StatusActive, Schedulable: true, Concurrency: 2, Credentials: map[string]any{"api_key": "SECRET-" + name, "base_url": "https://up.example"}}
}

func anthropicMessagesRequest(requestID string, attempt uint32, key string) *relayv1.SelectRequest {
	return &relayv1.SelectRequest{RequestId: requestID, Attempt: attempt, Credential: &relayv1.SelectRequest_ApiKey{ApiKey: key},
		Method: "POST", Path: "/v1/messages", Endpoint: relayv1.SelectEndpoint_SELECT_ENDPOINT_ANTHROPIC_MESSAGES, Model: "claude-sonnet-4-5",
		ClientIp: "5.6.7.8", SessionHash: "session-1"}
}

// Anthropic Messages 的一轮选号：选中时带账号快照和凭证；选号失败、预热拦截、准入失败按本地的写法回给从节点，
// 换号状态留在从节点。
func TestSelectAnthropicMessages(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeStandard, anthropicAccount(1, "one", service.AccountTypeAPIKey))

	resp, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("m1", 1, "sk-anthropic"))
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", resp.GetRejection())
	require.Equal(t, int64(1), sel.GetAccount().GetId())
	require.Equal(t, int64(9), sel.GetGroupId())
	require.EqualValues(t, anthropicDefaultMaxAccountSwitches, sel.GetMaxAccountSwitches())
	require.Equal(t, "claude-sonnet-4-5", sel.GetForwardModel())
	voucher, err := sign.VerifyVoucher(sel.GetVoucher(), w.pub, testNode, time.Now())
	require.NoError(t, err)
	require.Equal(t, service.PlatformAnthropic, voucher.GetContext().GetQuotaPlatform())
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)

	// 已排除了所有账号：选号耗尽，从节点按本地的 HandleSelectionExhausted 处理。
	exhausted := anthropicMessagesRequest("m2", 1, "sk-anthropic")
	exhausted.ExcludedAccountIds = []int64{1}
	resp, err = w.sel.Select(ctx, testNode, exhausted)
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED, resp.GetRejection().GetFormat())
	// 从节点照本地可能退避后接着选：请求记录（用户槽）留着，直到请求结束消息。
	require.EqualValues(t, 1, w.slots.held.Load())
	w.sel.Release(testNode, &relayv1.SelectionRelease{RequestDone: true, RequestId: exhausted.GetRequestId()})
	w.waitReleased(t)

	// OpenAI 分组的 Key 不走这个入口。
	resp, err = w.sel.Select(ctx, testNode, anthropicMessagesRequest("m3", 1, "sk-a"))
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED, resp.GetRejection().GetFormat())
}

func TestSelectAnthropicMessagesRejections(t *testing.T) {
	ctx := context.Background()

	t.Run("no account at all: the local first-selection error", func(t *testing.T) {
		w := newWorld(t, config.RunModeStandard)
		resp, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("n1", 1, "sk-anthropic"))
		require.NoError(t, err)
		rej := resp.GetRejection()
		require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_GATEWAY, rej.GetFormat())
		require.Equal(t, int32(http.StatusServiceUnavailable), rej.GetStatus())
		require.Contains(t, rej.GetMessage(), "No available accounts")
		w.waitReleased(t)
	})

	t.Run("warmup interception", func(t *testing.T) {
		a := anthropicAccount(1, "one", service.AccountTypeAPIKey)
		a.Credentials["intercept_warmup_requests"] = true
		w := newWorld(t, config.RunModeStandard, a)
		req := anthropicMessagesRequest("i1", 1, "sk-anthropic")
		req.InterceptType = int32(handler.InterceptTypeWarmup)
		resp, err := w.sel.Select(ctx, testNode, req)
		require.NoError(t, err)
		require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_INTERCEPTED, resp.GetRejection().GetFormat())
		require.Equal(t, int32(handler.InterceptTypeWarmup), resp.GetRejection().GetInterceptType())
		w.waitReleased(t)
	})

	t.Run("account types the node cannot forward yet go to the master", func(t *testing.T) {
		w := newWorld(t, config.RunModeStandard, anthropicAccount(1, "bedrock", service.AccountTypeBedrock))
		resp, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("o1", 1, "sk-anthropic"))
		require.NoError(t, err)
		require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED, resp.GetRejection().GetFormat())
		w.waitReleased(t)
	})
}

// 同一次请求里选号耗尽后接着选（从节点照本地 HandleSelectionExhausted 退避后重选）：请求记录留着，用户槽只占一次，
// 计价时间和粘性会话起点不变（与单机在整个请求里只算一次一致）。
func TestSelectAnthropicKeepsTheRequestAcrossRetries(t *testing.T) {
	ctx := context.Background()
	cache := &countingSticky{bound: map[string]int64{}}
	useGatewayCache(t, cache)
	w := newWorld(t, config.RunModeStandard, anthropicAccount(1, "one", service.AccountTypeAPIKey))

	first, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("k1", 1, "sk-anthropic"))
	require.NoError(t, err)
	sel := first.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", first.GetRejection())
	require.Zero(t, sel.GetStickyBoundAccountId())
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId()})

	// 选号时已经按本地的做法绑了粘性会话；请求开始时没有绑定，这一点不因为重选而变。
	exhausted := anthropicMessagesRequest("k1", 2, "sk-anthropic")
	exhausted.ExcludedAccountIds = []int64{1}
	resp, err := w.sel.Select(ctx, testNode, exhausted)
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED, resp.GetRejection().GetFormat())

	again, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("k1", 3, "sk-anthropic"))
	require.NoError(t, err)
	sel2 := again.GetSelection()
	require.NotNil(t, sel2, "rejection: %+v", again.GetRejection())
	require.Zero(t, sel2.GetStickyBoundAccountId(), "the sticky starting point is taken once per request")
	require.Equal(t, sel.GetPricingAtUnixMs(), sel2.GetPricingAtUnixMs(), "the pricing time is fixed at the start of the request")
	require.EqualValues(t, 1, w.slots.userAcquires.Load(), "the user slot is taken once for the whole request")
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel2.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
}

type recordingSessions struct {
	service.SessionLimitCache
	mu           sync.Mutex
	unregistered []int64
}

func (r *recordingSessions) UnregisterSession(_ context.Context, accountID int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unregistered = append(r.unregistered, accountID)
	return nil
}

type recordingRPM struct {
	service.RPMCache
	increments atomic.Int64
}

func (r *recordingRPM) IncrementRPM(context.Context, int64) (int, error) {
	return int(r.increments.Add(1)), nil
}

// OAuth 账号一次尝试结束时本地做的主节点那几步：转发成功计一次 RPM；上游没服务这次尝试时放掉会话数注册。
func TestAnthropicAttemptReleaseFollowsTheLocalHandler(t *testing.T) {
	ctx := context.Background()
	oauth := anthropicAccount(1, "oauth", service.AccountTypeOAuth)
	oauth.Credentials = map[string]any{"access_token": "SECRET-at"}
	oauth.Extra = map[string]any{"max_sessions": float64(2), "base_rpm": float64(10)}
	w := newWorld(t, config.RunModeStandard, oauth)
	sessions, rpm := &recordingSessions{}, &recordingRPM{}
	w.sel.deps.AnthropicGateway = service.NewGatewayService(fakeAccounts{accounts: []service.Account{oauth}}, nil, nil, nil, nil, nil, nil, nil, w.sel.deps.Config,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, sessions, rpm, nil, nil, nil, nil, nil, nil, nil, nil)
	unregistered := func() int {
		sessions.mu.Lock()
		defer sessions.mu.Unlock()
		return len(sessions.unregistered)
	}

	resp, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("o1", 1, "sk-anthropic"))
	require.NoError(t, err)
	require.NotNil(t, resp.GetSelection(), "rejection: %+v", resp.GetRejection())
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: resp.GetSelection().GetSelectionId(), RequestDone: true, ForwardSucceeded: true, UpstreamServed: true})
	require.EqualValues(t, 1, rpm.increments.Load())
	require.Zero(t, unregistered(), "a served session keeps its registration")

	resp, err = w.sel.Select(ctx, testNode, anthropicMessagesRequest("o2", 1, "sk-anthropic"))
	require.NoError(t, err)
	require.NotNil(t, resp.GetSelection(), "rejection: %+v", resp.GetRejection())
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: resp.GetSelection().GetSelectionId(), RequestDone: true})
	require.EqualValues(t, 1, rpm.increments.Load())
	require.Equal(t, 1, unregistered(), "an attempt the upstream never served gives its session registration back")
	w.waitReleased(t)
}

// 用户消息串行队列还没接到从节点：开了队列的 OAuth 账号交给主节点。
func TestAnthropicOAuthWithMessageQueueStaysOnTheMaster(t *testing.T) {
	ctx := context.Background()
	oauth := anthropicAccount(1, "oauth", service.AccountTypeOAuth)
	oauth.Credentials = map[string]any{"access_token": "SECRET-at"}
	oauth.Extra = map[string]any{"user_msg_queue_mode": "serialize"}
	w := newWorld(t, config.RunModeStandard, oauth)
	resp, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("q1", 1, "sk-anthropic"))
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED, resp.GetRejection().GetFormat())
	w.waitReleased(t)
}
