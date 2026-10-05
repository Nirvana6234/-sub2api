package relayselect

import (
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Grok 账号在从节点上收到上游 429：从节点没有仓储，把错误响应作为账号事件交给主节点，主节点用单机同一段代码写限流状态
// （限流重置时间、额度快照），调度状态在主节点。
func TestGrokUpstreamErrorOnTheNodeIsAppliedByTheMaster(t *testing.T) {
	writes := useAccountWrites(t)
	account := grokMediaAccount(1)
	accounts := []service.Account{account}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream + "/status-429"
		return accounts
	})
	t.Setenv("XAI_ALLOW_UNSAFE_URL_OVERRIDES", "1")

	status, body := e.post(t, "/v1/chat/completions", "sk-grokgroup", `{"model":"grok-4.6","messages":[{"role":"user","content":"hi"}]}`)
	require.GreaterOrEqual(t, status, 400, body)
	require.Eventually(t, func() bool {
		limited, _, extra := writes.snapshot()
		_, hasLimit := limited[1]
		return hasLimit || len(extra[1]) > 0
	}, 5*time.Second, 20*time.Millisecond, "the master wrote the rate-limit state for the Grok account")
	e.world.waitReleased(t)
}

// 流空闲让 Grok 账号临时不可调度：事件带冷却时长，主节点照写；超过上限按上限。只认 Grok 平台账号。
func TestGrokTempUnscheduleEventIsAppliedAndCapped(t *testing.T) {
	writes := useAccountWrites(t)
	account := grokMediaAccount(1)
	w := newWorld(t, config.RunModeStandard, account)
	w.sel.recent[nodeAccount{nodeID: testNode, accountID: 1}] = recentUse{account: &account, until: time.Now().Add(time.Minute)}
	other := apiKeyAccount(2, "openai")
	w.sel.recent[nodeAccount{nodeID: testNode, accountID: 2}] = recentUse{account: &other, until: time.Now().Add(time.Minute)}

	before := time.Now()
	w.sel.applyAccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_GrokTempUnschedule{
		GrokTempUnschedule: &relayv1.GrokTempUnscheduleEvent{CooldownMs: (2 * time.Minute).Milliseconds(), Reason: "grok stream idle timeout"},
	}})
	_, unsched, _ := writes.snapshot()
	require.WithinDuration(t, before.Add(2*time.Minute), unsched[1], 5*time.Second)

	w.sel.applyAccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_GrokTempUnschedule{
		GrokTempUnschedule: &relayv1.GrokTempUnscheduleEvent{CooldownMs: (6 * time.Hour).Milliseconds(), Reason: "too long"},
	}})
	_, unsched, _ = writes.snapshot()
	require.WithinDuration(t, before.Add(maxRelayGrokTempUnschedulable), unsched[1], 5*time.Second, "capped")

	// 不是 Grok 账号：不写。
	w.sel.applyAccountEvent(testNode, &relayv1.AccountEvent{AccountId: 2, Kind: &relayv1.AccountEvent_GrokTempUnschedule{
		GrokTempUnschedule: &relayv1.GrokTempUnscheduleEvent{CooldownMs: 1000, Reason: "x"},
	}})
	_, unsched, _ = writes.snapshot()
	require.NotContains(t, unsched, int64(2))

	// 错误响应事件：限流状态主节点写。
	w.sel.applyAccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_GrokUpstreamError{
		GrokUpstreamError: &relayv1.GrokUpstreamErrorEvent{StatusCode: http.StatusTooManyRequests, Body: []byte(`{"error":"rate limited"}`), Model: "grok-4"},
	}})
	limited, _, extra := writes.snapshot()
	_, hasLimit := limited[1]
	require.True(t, hasLimit || len(extra[1]) > 0, "rate limit / quota snapshot written")
}
