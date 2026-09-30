package relayselect

import (
	"context"
	"net/http"
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
		w := newWorld(t, config.RunModeStandard, anthropicAccount(1, "oauth", service.AccountTypeOAuth))
		resp, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("o1", 1, "sk-anthropic"))
		require.NoError(t, err)
		require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED, resp.GetRejection().GetFormat())
		w.waitReleased(t)
	})
}
