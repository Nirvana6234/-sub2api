package relayselect

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func wsConfig() *config.Config {
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	return cfg
}

func wsAccount(id int64, name string) service.Account {
	a := apiKeyAccount(id, name)
	a.Extra = map[string]any{"openai_apikey_responses_websockets_v2_enabled": true}
	return a
}

func wsRequest(requestID string, attempt uint32, key string) *relayv1.SelectRequest {
	return &relayv1.SelectRequest{RequestId: requestID, Attempt: attempt, Credential: &relayv1.SelectRequest_ApiKey{ApiKey: key},
		Method: "GET", Path: "/v1/responses", Endpoint: relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_RESPONSES_WS, Model: "gpt-5",
		ClientIp: "5.6.7.8", SessionHash: "ws-session"}
}

func wsCloseOf(t *testing.T, resp *relayv1.SelectResponse) (coderws.StatusCode, string) {
	t.Helper()
	r := resp.GetRejection()
	require.NotNil(t, r, "expected a rejection, got %+v", resp)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_WS_CLOSE, r.GetFormat())
	return coderws.StatusCode(r.GetStatus()), r.GetMessage()
}

// 一条 WebSocket 连接：建连选号占用户槽和账号槽、不签凭证；每一轮签一张凭证（计价时间按这一轮）；
// 一轮结束放掉两个槽、选号留着；下一轮重新占槽。
func TestWebSocketConnectionTurns(t *testing.T) {
	ctx := context.Background()
	w := newWorldOn(t, wsConfig(), 10, testNode, wsAccount(1, "one"))

	resp, err := w.sel.Select(ctx, testNode, wsRequest("c1", 1, "sk-a"))
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", resp.GetRejection())
	require.Equal(t, int64(1), sel.GetAccount().GetId())
	require.Empty(t, sel.GetVoucher(), "vouchers are issued per turn")
	require.Equal(t, int64(1), w.slots.held.Load())
	require.Equal(t, int64(1), w.slots.accounts.Load())

	turn1, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: sel.GetSelectionId(), Turn: 1, Model: "gpt-5"})
	require.NoError(t, err)
	require.Zero(t, turn1.GetCloseStatus(), turn1.GetCloseReason())
	require.Equal(t, int64(1), w.slots.accounts.Load(), "turn 1 uses the slots taken at connect")
	v1, err := sign.VerifyVoucher(turn1.GetVoucher(), w.pub, testNode, time.Now())
	require.NoError(t, err)
	require.Equal(t, sel.GetSelectionId(), v1.GetSelectionId())
	require.Equal(t, "gpt-5", v1.GetRequestedModel())
	require.Equal(t, turn1.GetPricingAtUnixMs(), v1.GetContext().GetPricingAtUnixMs())

	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), TurnEnd: true, TurnId: turn1.GetTurnId(), ResponseIds: []string{"resp_ws1"}})
	require.Equal(t, int64(0), w.slots.held.Load(), "the turn's user slot is given back")
	require.Equal(t, int64(0), w.slots.accounts.Load(), "the turn's account slot is given back")

	time.Sleep(2 * time.Millisecond)
	turn2, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: sel.GetSelectionId(), Turn: 2, Model: "gpt-5-mini"})
	require.NoError(t, err)
	require.Zero(t, turn2.GetCloseStatus(), turn2.GetCloseReason())
	require.Equal(t, int64(1), w.slots.held.Load())
	require.Equal(t, int64(1), w.slots.accounts.Load())
	v2, err := sign.VerifyVoucher(turn2.GetVoucher(), w.pub, testNode, time.Now())
	require.NoError(t, err)
	require.NotEqual(t, v1.GetVoucherId(), v2.GetVoucherId(), "one voucher per turn")
	require.Equal(t, "gpt-5-mini", v2.GetRequestedModel())
	require.Greater(t, v2.GetContext().GetPricingAtUnixMs(), v1.GetContext().GetPricingAtUnixMs(), "each turn is priced when it starts")

	mapping, err := w.sel.TurnMapping(ctx, testNode, &relayv1.TurnMappingRequest{SelectionId: sel.GetSelectionId(), Model: "gpt-5-mini"})
	require.NoError(t, err)
	require.False(t, mapping.GetChannelMapped())

	// 连接结束：放掉一切。
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
	gone, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: sel.GetSelectionId(), Turn: 3, Model: "gpt-5"})
	require.NoError(t, err)
	require.Equal(t, int32(coderws.StatusTryAgainLater), gone.GetCloseStatus(), "a lost connection record asks the client to reconnect")
	_, err = w.sel.TurnMapping(ctx, testNode, &relayv1.TurnMappingRequest{SelectionId: sel.GetSelectionId(), Model: "gpt-5"})
	require.Error(t, err)
}

// 连接内换号：放掉这次选号（不结束请求），再以同一请求 ID 选号（排除失败的账号），用户槽没占着时先补占。
func TestWebSocketFailoverWithinAConnection(t *testing.T) {
	ctx := context.Background()
	w := newWorldOn(t, wsConfig(), 10, testNode, wsAccount(1, "one"), wsAccount(2, "two"))

	resp, err := w.sel.Select(ctx, testNode, wsRequest("c1", 1, "sk-a"))
	require.NoError(t, err)
	first := resp.GetSelection()
	turn, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: first.GetSelectionId(), Turn: 1, Model: "gpt-5"})
	require.NoError(t, err)
	// 一轮失败：钩子先结束这一轮（两个槽都放掉），再换号。
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: first.GetSelectionId(), TurnEnd: true, TurnId: turn.GetTurnId()})
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: first.GetSelectionId()})
	require.Equal(t, int64(0), w.slots.held.Load())

	retry := wsRequest("c1", 2, "sk-a")
	retry.ExcludedAccountIds = []int64{first.GetAccount().GetId()}
	resp, err = w.sel.Select(ctx, testNode, retry)
	require.NoError(t, err)
	second := resp.GetSelection()
	require.NotNil(t, second, "rejection: %+v", resp.GetRejection())
	require.NotEqual(t, first.GetAccount().GetId(), second.GetAccount().GetId())
	require.Equal(t, int64(1), w.slots.held.Load(), "the user slot is taken again before reselecting")

	// 再换号时没有账号了：按换号耗尽（从节点按它最近的上游错误关闭）。
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: second.GetSelectionId()})
	last := wsRequest("c1", 3, "sk-a")
	last.ExcludedAccountIds = []int64{1, 2}
	resp, err = w.sel.Select(ctx, testNode, last)
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED, resp.GetRejection().GetFormat())
	w.waitReleased(t)
}

// 占不到槽：账号忙时这一轮关闭连接，刚占的用户槽放掉；建连时账号忙同样按单机的关闭码。
func TestWebSocketBusyAccount(t *testing.T) {
	ctx := context.Background()
	w := newWorldOn(t, wsConfig(), 10, testNode, wsAccount(1, "one"))
	resp, err := w.sel.Select(ctx, testNode, wsRequest("c1", 1, "sk-a"))
	require.NoError(t, err)
	sel := resp.GetSelection()
	turn1, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: sel.GetSelectionId(), Turn: 1, Model: "gpt-5"})
	require.NoError(t, err)
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), TurnEnd: true, TurnId: turn1.GetTurnId()})

	w.slots.accountLimit.Store(1)
	w.slots.accounts.Store(1) // 别的请求占着这个账号唯一的槽
	busy, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: sel.GetSelectionId(), Turn: 2, Model: "gpt-5"})
	require.NoError(t, err)
	require.Equal(t, int32(coderws.StatusTryAgainLater), busy.GetCloseStatus())
	require.Equal(t, "account is busy, please retry later", busy.GetCloseReason())
	require.Empty(t, busy.GetVoucher())
	require.Equal(t, int64(0), w.slots.held.Load(), "the user slot taken for this turn is given back")
}

// 两轮之间不占槽的连接不按 15 分钟的占槽上限清理；空闲太久（或占着槽超过上限）才清理，之后下一轮要求重连。
func TestWebSocketConnectionsSurviveIdleGaps(t *testing.T) {
	ctx := context.Background()
	w := newWorldOn(t, wsConfig(), 10, testNode, wsAccount(1, "one"))
	resp, err := w.sel.Select(ctx, testNode, wsRequest("c1", 1, "sk-a"))
	require.NoError(t, err)
	sel := resp.GetSelection()
	turn1, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: sel.GetSelectionId(), Turn: 1, Model: "gpt-5"})
	require.NoError(t, err)
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), TurnEnd: true, TurnId: turn1.GetTurnId()})

	base := time.Now()
	w.sel.now = func() time.Time { return base.Add(holdLimit + time.Minute) }
	w.sel.reap()
	next, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: sel.GetSelectionId(), Turn: 2, Model: "gpt-5"})
	require.NoError(t, err)
	require.Zero(t, next.GetCloseStatus(), "an idle connection outlives the slot hold limit")
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), TurnEnd: true, TurnId: next.GetTurnId()})

	w.sel.now = func() time.Time { return base.Add(holdLimit + wsIdleLimit + 2*time.Minute) }
	w.sel.reap()
	gone, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: sel.GetSelectionId(), Turn: 3, Model: "gpt-5"})
	require.NoError(t, err)
	require.Equal(t, int32(coderws.StatusTryAgainLater), gone.GetCloseStatus())
	w.sel.now = time.Now
}

// 建连时的检查按单机顺序和关闭码：cyber 会话屏蔽、计费资格；升级之前分组可能被审计时整条连接交给主节点。
func TestWebSocketConnectRejections(t *testing.T) {
	ctx := context.Background()
	w := newWorldOn(t, wsConfig(), 10, testNode, wsAccount(1, "one"))

	w.sel.findCyberBlocked = func(context.Context, service.CyberSessionLookup) string { return "blocked" }
	req := wsRequest("c1", 1, "sk-a")
	req.Cyber = &relayv1.CyberSessionLookup{ExplicitKey: "k"}
	resp, err := w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	code, reason := wsCloseOf(t, resp)
	require.Equal(t, coderws.StatusPolicyViolation, code)
	require.Equal(t, "session blocked by cyber-security policy", reason)
	require.Equal(t, "blocked", resp.GetRejection().GetCyberBlockKey())
	require.Equal(t, int64(0), w.slots.held.Load(), "cyber is checked before the user slot")
	w.sel.findCyberBlocked = func(context.Context, service.CyberSessionLookup) string { return "" }

	standard := wsConfig()
	standard.RunMode = config.RunModeStandard
	poor := newWorldOn(t, standard, 0, testNode, wsAccount(1, "one"))
	resp, err = poor.sel.Select(ctx, testNode, wsRequest("c2", 1, "sk-a"))
	require.NoError(t, err)
	code, reason = wsCloseOf(t, resp)
	require.Equal(t, coderws.StatusPolicyViolation, code)
	require.Equal(t, "billing check failed", reason)
	poor.waitReleased(t)

	admit := func(sel *selector) *relayv1.AdmitResponse {
		out, err := sel.Admit(ctx, testNode, &relayv1.AdmitRequest{Credential: &relayv1.AdmitRequest_ApiKey{ApiKey: "sk-a"}, ClientIp: "5.6.7.8", Method: "GET", Path: "/v1/responses"})
		require.NoError(t, err)
		return out
	}
	require.NotNil(t, admit(w.sel).GetAdmission(), "the node serves the connection; auditing happens on the node")
}

// 每个 Key 的 WebSocket 连接数租约：上限用主节点的配置，按节点记归属，别的节点不能续期或释放。
func TestWebSocketLeases(t *testing.T) {
	ctx := context.Background()
	cfg := wsConfig()
	cfg.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey = 1
	w := newWorldOn(t, cfg, 10, testNode, wsAccount(1, "one"))
	lease := func(nodeID int64, op relayv1.WebSocketLeaseOp, id string) bool {
		out, err := w.sel.WebSocketLease(ctx, nodeID, &relayv1.WebSocketLeaseRequest{Op: op, ApiKey: "sk-a", LeaseId: id})
		require.NoError(t, err)
		return out.GetOk()
	}
	require.True(t, lease(testNode, relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_ACQUIRE, "l1"))
	require.False(t, lease(testNode, relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_ACQUIRE, "l2"), "the master's per-key limit applies")
	require.True(t, lease(testNode, relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_REFRESH, "l1"))
	require.False(t, lease(testNode+1, relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_REFRESH, "l1"), "another node cannot refresh it")
	require.False(t, lease(testNode+1, relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_RELEASE, "l1"), "or release it")
	require.True(t, lease(testNode, relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_RELEASE, "l1"))
	require.True(t, lease(testNode, relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_ACQUIRE, "l2"), "a released lease frees the slot")
	_, err := w.sel.WebSocketLease(ctx, testNode, &relayv1.WebSocketLeaseRequest{Op: relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_ACQUIRE, ApiKey: "sk-nope", LeaseId: "l3"})
	require.Error(t, err, "an unknown key gets nothing")

	unlimited := newWorldOn(t, wsConfig(), 10, testNode, wsAccount(1, "one"))
	for _, id := range []string{"a", "b", "c"} {
		out, err := unlimited.sel.WebSocketLease(ctx, testNode, &relayv1.WebSocketLeaseRequest{Op: relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_ACQUIRE, ApiKey: "sk-a", LeaseId: id})
		require.NoError(t, err)
		require.True(t, out.GetOk())
	}
	out, err := unlimited.sel.WebSocketLease(ctx, testNode, &relayv1.WebSocketLeaseRequest{Op: relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_REFRESH, LeaseId: "a"})
	require.NoError(t, err)
	require.True(t, out.GetOk(), "without a limit a lease stays valid")
}

// 下一轮的 BeginTurn 比上一轮的结束消息先到（事件连接上的释放是异步的）：上一轮当作已结束，下一轮占着自己的槽；
// 迟到的结束消息不放下一轮的槽。
func TestWebSocketLateTurnEndDoesNotReleaseTheNextTurn(t *testing.T) {
	ctx := context.Background()
	w := newWorldOn(t, wsConfig(), 10, testNode, wsAccount(1, "one"))
	resp, err := w.sel.Select(ctx, testNode, wsRequest("c1", 1, "sk-a"))
	require.NoError(t, err)
	sel := resp.GetSelection()
	turn1, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: sel.GetSelectionId(), Turn: 1, Model: "gpt-5"})
	require.NoError(t, err)

	turn2, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: sel.GetSelectionId(), Turn: 2, Model: "gpt-5"})
	require.NoError(t, err)
	require.Zero(t, turn2.GetCloseStatus(), turn2.GetCloseReason())
	require.Equal(t, int64(1), w.slots.held.Load(), "turn 2 holds a user slot (turn 1's was given back first)")
	require.Equal(t, int64(1), w.slots.accounts.Load())

	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), TurnEnd: true, TurnId: turn1.GetTurnId()})
	require.Equal(t, int64(1), w.slots.held.Load(), "a late end of turn 1 leaves turn 2's slots alone")
	require.Equal(t, int64(1), w.slots.accounts.Load())

	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), TurnEnd: true, TurnId: turn2.GetTurnId()})
	require.Equal(t, int64(0), w.slots.held.Load())
	require.Equal(t, int64(0), w.slots.accounts.Load())
}

// 连接内换号后，旧选号那一轮迟到的结束消息不放新选号的用户槽。
func TestWebSocketLateTurnEndAfterFailover(t *testing.T) {
	ctx := context.Background()
	w := newWorldOn(t, wsConfig(), 10, testNode, wsAccount(1, "one"), wsAccount(2, "two"))
	resp, err := w.sel.Select(ctx, testNode, wsRequest("c1", 1, "sk-a"))
	require.NoError(t, err)
	first := resp.GetSelection()
	turn, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: first.GetSelectionId(), Turn: 1, Model: "gpt-5"})
	require.NoError(t, err)

	// 换号：同步的选号比旧选号那一轮的结束消息和释放消息（事件连接，异步）先到主节点。
	retry := wsRequest("c1", 2, "sk-a")
	retry.ExcludedAccountIds = []int64{first.GetAccount().GetId()}
	resp, err = w.sel.Select(ctx, testNode, retry)
	require.NoError(t, err)
	second := resp.GetSelection()
	require.NotNil(t, second)
	require.Equal(t, int64(1), w.slots.held.Load(), "the connection keeps its one user slot")

	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: first.GetSelectionId(), TurnEnd: true, TurnId: turn.GetTurnId()})
	require.Equal(t, int64(1), w.slots.held.Load(), "the old selection's late turn end leaves the connection's user slot alone")
	require.Equal(t, int64(1), w.slots.accounts.Load(), "it gives back only the old account's slot")
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: first.GetSelectionId()})

	next, err := w.sel.BeginTurn(ctx, testNode, &relayv1.BeginTurnRequest{SelectionId: second.GetSelectionId(), Turn: 1, Model: "gpt-5"})
	require.NoError(t, err)
	require.Zero(t, next.GetCloseStatus())
	require.Equal(t, int64(1), w.slots.held.Load())
	require.Equal(t, int64(1), w.slots.accounts.Load())

	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: second.GetSelectionId(), TurnEnd: true, TurnId: next.GetTurnId()})
	require.Equal(t, int64(0), w.slots.held.Load())
	require.Equal(t, int64(0), w.slots.accounts.Load())
}
