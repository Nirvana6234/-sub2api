package relayselect

import (
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// forcedAntigravityAccount 是在 Anthropic 分组（9）和 Gemini 分组（10）里、但没开混合调度的 Antigravity 账号：只有
// /antigravity/* 入口（强制 Antigravity 平台，分组平台不限）才选得到它。
func forcedAntigravityAccount(id int64) service.Account {
	a := antigravityOAuthAccount(id, 9)
	a.AccountGroups = append(a.AccountGroups, service.AccountGroup{AccountID: id, GroupID: 10})
	a.Extra = nil
	return a
}

// /antigravity/v1/messages：强制 Antigravity 平台，主节点按路径定（不信从节点的说法），分组平台不限（Anthropic 分组的 Key 也能用）。
func TestNodeServesAntigravityMessagesRoute(t *testing.T) {
	accounts := []service.Account{forcedAntigravityAccount(1)}
	e := startStandardE2E(t, func(string) []service.Account { return accounts })
	useAntigravity(e.world, accounts)
	msg := `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`

	// 同一把 Key 走 /v1/messages 选不到这个账号（没开混合调度、不在分组里）。
	status, body := e.post(t, "/v1/messages", "sk-anthropic", msg)
	require.NotEqual(t, http.StatusOK, status, body)
	e.world.waitReleased(t)

	status, body = e.post(t, "/antigravity/v1/messages", "sk-anthropic", msg)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "hello")
	hit := waitHit(t, e, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer SECRET-ag-token" })
	require.NotNil(t, hit)
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	voucher, err := sign.VerifyVoucher(e.settler.records()[0].GetVoucher(), e.world.pub, e.nodeID, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(1), voucher.GetAccountId())
	e.world.waitReleased(t)

	// count_tokens：强制 Antigravity 平台，本地处理函数对这个平台回"不支持"（从节点上同一段代码，不用问主节点）。
	status, body = e.post(t, "/antigravity/v1/messages/count_tokens", "sk-anthropic", msg)
	require.Equal(t, http.StatusNotFound, status, body)
	require.Contains(t, body, "count_tokens endpoint is not supported for this platform")
	e.world.waitReleased(t)

	// 不接的分组平台（OpenAI）交给主节点。
	status, _ = e.post(t, "/antigravity/v1/messages", "sk-a", msg)
	require.Equal(t, http.StatusServiceUnavailable, status, "handed off to the master (the test world has no master forwarding)")
	e.world.waitReleased(t)
}

// /antigravity/v1beta：Gemini 原生格式，同样强制 Antigravity 平台；Google 格式的鉴权与错误。
func TestNodeServesAntigravityNativeRoute(t *testing.T) {
	accounts := []service.Account{forcedAntigravityAccount(1)}
	e := startStandardE2E(t, func(string) []service.Account { return accounts })
	useAntigravity(e.world, accounts)
	path := "/antigravity/v1beta/models/gemini-2.5-pro:generateContent"

	status, body := e.postGoogle(t, path, "sk-nope", geminiBody)
	require.Equal(t, http.StatusUnauthorized, status)
	require.JSONEq(t, `{"error":{"code":401,"message":"Invalid API key","status":"UNAUTHENTICATED"}}`, body)

	// Gemini 分组和 Anthropic 分组的 Key 都行（强制平台，分组平台不限）。
	for _, key := range []string{"sk-gemini", "sk-anthropic"} {
		status, body = e.postGoogle(t, path, key, geminiBody)
		require.Equal(t, http.StatusOK, status, body)
		require.Contains(t, body, "hello")
		e.world.waitReleased(t)
	}
	hit := waitHit(t, e, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer SECRET-ag-token" })
	require.NotNil(t, hit)
	require.Eventually(t, func() bool { return len(e.settler.records()) == 2 }, 5*time.Second, 20*time.Millisecond)
	for _, rec := range e.settler.records() {
		voucher, err := sign.VerifyVoucher(rec.GetVoucher(), e.world.pub, e.nodeID, time.Now())
		require.NoError(t, err)
		require.Equal(t, int64(1), voucher.GetAccountId())
	}
}

// /antigravity/v1/messages 回 prompt 过长换到兜底分组：本地清掉强制平台，按兜底分组的平台（Anthropic）调度；主节点在带兜底分组的
// 选号上同样不再强制（强制平台由路径定，不是从节点说的）。
func TestNodeClearsTheForcedPlatformOnTheFallbackGroup(t *testing.T) {
	fallbackAccount := anthropicAccount(2, "fallback", service.AccountTypeAPIKey)
	fallbackAccount.AccountGroups = []service.AccountGroup{{AccountID: 2, GroupID: 50}}
	accounts := []service.Account{forcedAntigravityAccount(1), fallbackAccount}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[1].Credentials["base_url"] = upstream
		return accounts
	})
	useAntigravity(e.world, accounts)
	fallback := openAIGroup(50)
	fallback.Platform = service.PlatformAnthropic
	fallback.ActiveAccountCount = 1
	e.world.groups.byID[50] = fallback
	fbID := int64(50)
	e.world.keys.keys["sk-anthropic"].Group.FallbackGroupIDOnInvalidRequest = &fbID

	status, body := e.post(t, "/antigravity/v1/messages", "sk-anthropic", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"too-long-trigger"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	require.Eventually(t, func() bool {
		for _, rec := range e.settler.records() {
			v, err := sign.VerifyVoucher(rec.GetVoucher(), e.world.pub, e.nodeID, time.Now())
			if err == nil && v.GetGroupId() == 50 && v.GetAccountId() == 2 {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "served by the fallback group's Anthropic account")
	e.world.waitReleased(t)
}
