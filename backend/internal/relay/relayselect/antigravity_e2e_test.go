package relayselect

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// recordingAntigravityRepo 记下主节点执行从节点 Antigravity 事件时写的账号状态。
type recordingAntigravityRepo struct {
	fakeAccounts
	mu          sync.Mutex
	modelLimits map[string]time.Time
	rateLimited map[int64]time.Time
	extra       map[int64]map[string]any
}

func newRecordingAntigravityRepo(accounts []service.Account) *recordingAntigravityRepo {
	return &recordingAntigravityRepo{fakeAccounts: fakeAccounts{accounts: accounts}, modelLimits: map[string]time.Time{},
		rateLimited: map[int64]time.Time{}, extra: map[int64]map[string]any{}}
}

func (r *recordingAntigravityRepo) SetModelRateLimit(_ context.Context, _ int64, key string, resetAt time.Time, _ ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modelLimits[key] = resetAt
	return nil
}

func (r *recordingAntigravityRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rateLimited[id] = resetAt
	return nil
}

func (r *recordingAntigravityRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extra[id] = updates
	return nil
}

// useAntigravity 给测试世界的主节点装上 Antigravity 转发服务（取 Google token、照写从节点的账号状态）。
func useAntigravity(w *world, accounts []service.Account) *recordingAntigravityRepo {
	repo := newRecordingAntigravityRepo(accounts)
	w.sel.deps.Antigravity = service.NewAntigravityGatewayService(repo, nil, nil, service.NewAntigravityTokenProvider(nil, nil, nil), nil, nil, nil, nil)
	return repo
}

func antigravityOAuthAccount(id int64, groupID int64) service.Account {
	a := antigravityMixedAccount(id)
	a.Credentials = map[string]any{"access_token": "SECRET-ag-token", "project_id": "proj-ag", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)}
	a.AccountGroups = []service.AccountGroup{{AccountID: id, GroupID: groupID}}
	return a
}

// 混合调度进 Anthropic 分组的 Antigravity 账号经从节点（开发计划 WP10）：Google token 由主节点给，从节点用与单机同一个
// 转发服务直连上游。
func TestNodeForwardsAntigravityAccounts(t *testing.T) {
	accounts := []service.Account{antigravityOAuthAccount(1, 9)}
	e := startStandardE2E(t, func(string) []service.Account { return accounts })
	useAntigravity(e.world, accounts)

	status, body := e.post(t, "/v1/messages", "sk-anthropic", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "hello")
	select {
	case r := <-e.hits:
		require.Equal(t, "Bearer SECRET-ag-token", r.Header.Get("Authorization"), "the master's Google token")
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	voucher, err := sign.VerifyVoucher(e.settler.records()[0].GetVoucher(), e.world.pub, e.nodeID, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(1), voucher.GetAccountId())
	e.world.waitReleased(t)
}

// Antigravity 回 prompt 过长：本地换到分组配置的兜底分组重试（计费资格复查在兜底分组上），从节点经主节点换、之后的选号带
// 兜底分组：用量记在兜底分组上。
func TestNodeFallsBackOnPromptTooLong(t *testing.T) {
	fallbackAccount := anthropicAccount(2, "fallback", service.AccountTypeAPIKey)
	fallbackAccount.AccountGroups = []service.AccountGroup{{AccountID: 2, GroupID: 50}}
	accounts := []service.Account{antigravityOAuthAccount(1, 9), fallbackAccount}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[1].Credentials["base_url"] = upstream
		return accounts
	})
	useAntigravity(e.world, accounts)
	fallback := openAIGroup(50)
	fallback.Platform = service.PlatformAnthropic
	fallback.ActiveAccountCount = 1
	e.world.groups.byID[50] = fallback
	original := e.world.keys.keys["sk-anthropic"].Group
	fbID := int64(50)
	original.FallbackGroupIDOnInvalidRequest = &fbID

	status, body := e.post(t, "/v1/messages", "sk-anthropic", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"too-long-trigger"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	require.Eventually(t, func() bool {
		for _, rec := range e.settler.records() {
			v, err := sign.VerifyVoucher(rec.GetVoucher(), e.world.pub, e.nodeID, time.Now())
			if err == nil && v.GetGroupId() == 50 && v.GetAccountId() == 2 {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "served by the fallback group's account and billed to that group")
	e.world.waitReleased(t)
}

// 兜底分组只认这把 Key 的分组配置的那个：节点带别的分组来时拒绝。
func TestSelectChecksTheFallbackGroup(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeStandard, anthropicAccount(1, "one", service.AccountTypeAPIKey))
	fallback := openAIGroup(50)
	fallback.Platform = service.PlatformAnthropic
	w.groups.byID[50] = fallback
	fbID := int64(50)
	w.keys.keys["sk-anthropic"].Group.FallbackGroupIDOnInvalidRequest = &fbID

	req := anthropicMessagesRequest("f1", 1, "sk-anthropic")
	req.FallbackGroupId = 77
	resp, err := w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	require.Equal(t, int32(http.StatusBadRequest), resp.GetRejection().GetStatus())
	w.waitReleased(t)

	switched, err := w.sel.SwitchFallbackGroup(ctx, testNode, &relayv1.SwitchFallbackGroupRequest{ApiKey: "sk-anthropic", Method: "POST", Path: "/v1/messages"})
	require.NoError(t, err)
	require.True(t, switched.GetSwitched())
	require.Equal(t, int64(50), switched.GetFallbackGroupId())

	// 兜底分组自己带兜底：不合规，当没有。
	other := int64(51)
	fallback.FallbackGroupIDOnInvalidRequest = &other
	switched, err = w.sel.SwitchFallbackGroup(ctx, testNode, &relayv1.SwitchFallbackGroupRequest{ApiKey: "sk-anthropic", Method: "POST", Path: "/v1/messages"})
	require.NoError(t, err)
	require.False(t, switched.GetSwitched())
}

// 从节点转发路径上写的 Antigravity 账号状态（模型级 / 账号级限流、积分耗尽标记）：主节点照写、时长不超过上限；
// INTERNAL 500 的计数和惩罚在主节点。
func TestAntigravityAccountEventsAreAppliedOnTheMaster(t *testing.T) {
	ctx := context.Background()
	accounts := []service.Account{antigravityOAuthAccount(1, 9)}
	w := newWorld(t, config.RunModeStandard, accounts...)
	repo := useAntigravity(w, accounts)
	w.sel.anthropicServed = func(*service.Account) bool { return true }

	resp, err := w.sel.Select(ctx, testNode, anthropicMessagesRequest("g1", 1, "sk-anthropic"))
	require.NoError(t, err)
	require.NotNil(t, resp.GetSelection(), "rejection: %+v", resp.GetRejection())

	event := func(kind any) *relayv1.AccountEvent {
		ev := &relayv1.AccountEvent{AccountId: 1}
		switch k := kind.(type) {
		case *relayv1.ModelRateLimitEvent:
			ev.Kind = &relayv1.AccountEvent_ModelRateLimit{ModelRateLimit: k}
		case *relayv1.RateLimitedEvent:
			ev.Kind = &relayv1.AccountEvent_RateLimited{RateLimited: k}
		case *relayv1.ModelRateLimitsExtraEvent:
			ev.Kind = &relayv1.AccountEvent_ModelRateLimitsExtra{ModelRateLimitsExtra: k}
		}
		return ev
	}
	in := time.Now().Add(30 * time.Minute)
	w.sel.applyAccountEvent(testNode, event(&relayv1.ModelRateLimitEvent{ModelKey: "claude-sonnet-4-5", ResetAtUnixMs: in.UnixMilli()}))
	w.sel.applyAccountEvent(testNode, event(&relayv1.RateLimitedEvent{ResetAtUnixMs: time.Now().Add(1000 * time.Hour).UnixMilli()}))
	w.sel.applyAccountEvent(testNode, event(&relayv1.ModelRateLimitsExtraEvent{LimitsJson: []byte(`{"gemini-3":{"rate_limit_reset_at":"2099-01-01T00:00:00Z"}}`)}))
	// 没在用的账号、不是 Antigravity 的账号不认。
	other := event(&relayv1.RateLimitedEvent{ResetAtUnixMs: in.UnixMilli()})
	other.AccountId = 99
	w.sel.applyAccountEvent(testNode, other)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.WithinDuration(t, in, repo.modelLimits["claude-sonnet-4-5"], time.Second)
	require.Contains(t, repo.modelLimits, "claude-sonnet-4-5")
	require.WithinDuration(t, time.Now().Add(7*24*time.Hour), repo.rateLimited[1], 5*time.Second, "capped")
	require.Contains(t, repo.extra[1][service.ModelRateLimitsExtraKey], "gemini-3")
	require.NotContains(t, repo.rateLimited, int64(99))

	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: resp.GetSelection().GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
}
