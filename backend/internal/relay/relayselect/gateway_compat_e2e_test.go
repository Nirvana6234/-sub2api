package relayselect

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	chatBody      = `{"model":"gemini-2.5-pro","messages":[{"role":"user","content":"hi"}]}`
	responsesBody = `{"model":"claude-sonnet-4-5","input":"hi"}`
)

// Gemini 分组的 /v1/chat/completions、Anthropic 分组的 /v1/responses（GatewayHandler，把请求转成 Messages / Gemini 请求再转发）经从节点：
// 用量同一个记录种类，上游由从节点直连。
func TestNodeServesGatewayChatAndResponses(t *testing.T) {
	gem := geminiAccount(1, "gem")
	anth := anthropicAccount(2, "claude", service.AccountTypeAPIKey)
	anth.AccountGroups = []service.AccountGroup{{AccountID: 2, GroupID: 9}}
	accounts := []service.Account{gem, anth}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		for i := range accounts {
			accounts[i].Credentials["base_url"] = upstream
		}
		return accounts
	})

	status, body := e.post(t, "/v1/chat/completions", "sk-gemini", chatBody)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, "hello")
	waitHit(t, e, func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/v1beta/models/gemini-2.5-pro:") })
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	rec := e.settler.records()[0]
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_ANTHROPIC, rec.GetKind())
	require.Equal(t, int64(10), mustVoucher(t, e, rec).GetGroupId())
	e.world.waitReleased(t)

	status, body = e.post(t, "/v1/responses", "sk-anthropic", responsesBody)
	require.Equal(t, http.StatusOK, status, body)
	waitHit(t, e, func(r *http.Request) bool { return r.URL.Path == "/v1/messages" })
	require.Eventually(t, func() bool { return len(e.settler.records()) == 2 }, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, int64(9), mustVoucher(t, e, e.settler.records()[1]).GetGroupId())
	e.world.waitReleased(t)
}

// 单机和从节点上同样的请求结果一致：成功的响应、没有可用账号时的错误体（Responses 写 code、Chat 写 type）。
func TestGatewayChatAndResponsesLocalAndNodeAgree(t *testing.T) {
	accounts := []service.Account{geminiAccount(1, "gem")}
	anth := anthropicAccount(2, "claude", service.AccountTypeAPIKey)
	anth.AccountGroups = []service.AccountGroup{{AccountID: 2, GroupID: 9}}
	accounts = append(accounts, anth)
	e := startStandardE2E(t, func(upstream string) []service.Account {
		for i := range accounts {
			accounts[i].Credentials["base_url"] = upstream
		}
		return accounts
	})
	local := startLocalGemini(t, e, accounts)

	for _, tc := range []struct{ path, key, body, field string }{
		{"/v1/chat/completions", "sk-gemini", chatBody, "choices.0.message.content"},
		{"/v1/responses", "sk-anthropic", responsesBody, "status"},
	} {
		nodeStatus, nodeBody := e.post(t, tc.path, tc.key, tc.body)
		e.world.waitReleased(t)
		localStatus, localBody := local.post(t, tc.path, tc.key, tc.body)
		require.Equal(t, http.StatusOK, localStatus, localBody)
		require.Equal(t, localStatus, nodeStatus, nodeBody)
		require.Equal(t, gjson.Get(localBody, tc.field).Raw, gjson.Get(nodeBody, tc.field).Raw, tc.path)
	}

	// 没有可用账号。
	empty := startStandardE2E(t, func(string) []service.Account { return nil })
	emptyLocal := startLocalGemini(t, empty, nil)
	for _, tc := range []struct{ path, key, body string }{
		{"/v1/chat/completions", "sk-gemini", chatBody},
		{"/v1/responses", "sk-anthropic", responsesBody},
	} {
		nodeStatus, nodeBody := empty.post(t, tc.path, tc.key, tc.body)
		empty.world.waitReleased(t)
		localStatus, localBody := emptyLocal.post(t, tc.path, tc.key, tc.body)
		require.Equal(t, http.StatusServiceUnavailable, localStatus, localBody)
		require.Equal(t, localStatus, nodeStatus)
		require.Equal(t, localBody, nodeBody, tc.path)
	}
}

func typeSafeAccount(id int64, base string) service.Account {
	return service.Account{ID: id, Name: "ts", Platform: service.PlatformTypeSafe, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 2,
		Credentials: map[string]any{"api_key": "SECRET-ts", "base_url": base}, AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: 31}}}
}

// TypeSafe 的 Jev 判断请求（/v1/systemone）经从节点：与单机同请求的响应一致，用量同一个记录种类；没有可用账号时的错误一致。
func TestNodeServesTypeSafeSystemOneLikeASingleServer(t *testing.T) {
	const body = `{"model":"jev-latest","state":{"text":"hi"},"questions":{"q":{"type":"noul"}}}`
	accounts := []service.Account{typeSafeAccount(1, "")}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		return accounts
	})
	local := startLocalGemini(t, e, accounts)
	nodeStatus, nodeBody := e.post(t, "/v1/systemone", "sk-typesafe", body)
	e.world.waitReleased(t)
	localStatus, localBody := local.post(t, "/v1/systemone", "sk-typesafe", body)
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, localBody, nodeBody)
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_ANTHROPIC, e.settler.records()[0].GetKind())
	require.Equal(t, int64(31), mustVoucher(t, e, e.settler.records()[0]).GetGroupId())

	empty := startStandardE2E(t, func(string) []service.Account { return nil })
	emptyLocal := startLocalGemini(t, empty, nil)
	nodeStatus, nodeBody = empty.post(t, "/v1/systemone", "sk-typesafe", body)
	empty.world.waitReleased(t)
	localStatus, localBody = emptyLocal.post(t, "/v1/systemone", "sk-typesafe", body)
	require.Equal(t, http.StatusServiceUnavailable, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus)
	require.Equal(t, localBody, nodeBody)
}
