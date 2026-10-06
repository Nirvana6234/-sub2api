package relayselect

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 节点规则（设计 10.2）：默认"仅分配的从节点"——分配给别的节点（含主节点）的 Key 在这台节点上回 403 并提示改用分配的地址；分配给
// 这台的、还没分配的 Key 照常；规则改成"全部从节点"后都接。拒绝按入口写成 OpenAI / Anthropic / Google 的错误格式。
func TestNodeRuleRejectsKeysAssignedElsewhere(t *testing.T) {
	accounts := []service.Account{apiKeyAccount(1, "one"), anthropicAccount(2, "claude", service.AccountTypeAPIKey)}
	accounts[1].AccountGroups = []service.AccountGroup{{AccountID: 2, GroupID: 9}}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		for i := range accounts {
			accounts[i].Credentials["base_url"] = upstream
		}
		accounts[0].AccountGroups = []service.AccountGroup{{AccountID: 1, GroupID: 5}}
		return accounts
	})
	rule := master.APIKeyNodeRuleAssigned
	e.world.sel.env.GeneralConfig = func(context.Context) master.GeneralConfig {
		return master.GeneralConfig{APIKeyNodeRule: rule}.WithDefaults()
	}
	keyA, keyAnthropic := e.world.keys.keys["sk-a"], e.world.keys.keys["sk-anthropic"]
	other, mine, masterNode := e.nodeID+7, e.nodeID, int64(0)
	const responses = `{"model":"gpt-5","input":"hi"}`
	const messages = `{"model":"claude-sonnet-4-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`

	// 改分配后清掉 Key 的鉴权缓存（线上由 APIKeyService 的作废做）。
	assign := func(k *service.APIKey, id *int64) {
		k.RelayNodeID = id
		e.world.sel.deps.APIKeys.InvalidateAuthCacheByKey(context.Background(), k.Key)
	}

	// 没分配：哪台都接。
	assign(keyA, nil)
	status, body := e.post(t, "/v1/responses", "sk-a", responses)
	require.Equal(t, http.StatusOK, status, body)
	e.world.waitReleased(t)

	// 分配给这台：照常。
	assign(keyA, &mine)
	status, body = e.post(t, "/v1/responses", "sk-a", responses)
	require.Equal(t, http.StatusOK, status, body)
	e.world.waitReleased(t)

	// 分配给别的节点或主节点：403，OpenAI 格式。
	for _, id := range []int64{other, masterNode} {
		id := id
		assign(keyA, &id)
		status, body = e.post(t, "/v1/responses", "sk-a", responses)
		require.Equal(t, http.StatusForbidden, status, body)
		require.Equal(t, "api_key_node_mismatch", gjson.Get(body, "error.code").String(), body)
		require.Contains(t, gjson.Get(body, "error.message").String(), "assigned")
	}

	// Anthropic 入口：Anthropic 格式。
	assign(keyAnthropic, &other)
	status, body = e.post(t, "/v1/messages", "sk-anthropic", messages)
	require.Equal(t, http.StatusForbidden, status, body)
	require.Equal(t, "error", gjson.Get(body, "type").String(), body)
	require.Equal(t, "permission_error", gjson.Get(body, "error.type").String(), body)

	// 规则改成"全部从节点"：都接。
	rule = master.APIKeyNodeRuleAny
	status, body = e.post(t, "/v1/responses", "sk-a", responses)
	require.Equal(t, http.StatusOK, status, body)
	e.world.waitReleased(t)
}

// 重新分配（设计 10.2）：管理员把 Key 从 A 移到 B 后，下一个请求就在 A 上被拒、在 B 上放行，不用等缓存过期——
// 节点规则读的是鉴权缓存里的快照，重新分配作废了这把 Key 的缓存。
func TestReassignedKeyMovesBetweenNodesImmediately(t *testing.T) {
	e := startE2E(t)
	e.world.sel.env.GeneralConfig = func(context.Context) master.GeneralConfig {
		return master.GeneralConfig{APIKeyNodeRule: master.APIKeyNodeRuleAssigned}.WithDefaults()
	}
	key := e.world.keys.keys["sk-a"]
	const responses = `{"model":"gpt-5","input":"hi"}`
	apiKeys := e.world.sel.deps.APIKeys

	moved, err := apiKeys.AssignRelayNode(context.Background(), []int64{key.ID}, e.nodeID, false)
	require.NoError(t, err)
	require.Equal(t, 1, moved)
	require.Nil(t, key.RelayNodeChangedAt, "the first assignment does not raise the address-changed notice")
	status, body := e.post(t, "/v1/responses", "sk-a", responses)
	require.Equal(t, http.StatusOK, status, body)
	e.world.waitReleased(t)

	// 移到别的节点：这台上马上拒绝，不用等缓存过期。
	_, err = apiKeys.AssignRelayNode(context.Background(), []int64{key.ID}, e.nodeID+1, true)
	require.NoError(t, err)
	require.NotNil(t, key.RelayNodeChangedAt, "a reassignment raises it")
	status, body = e.post(t, "/v1/responses", "sk-a", responses)
	require.Equal(t, http.StatusForbidden, status, body)
	require.Equal(t, "api_key_node_mismatch", gjson.Get(body, "error.code").String(), body)

	// 移回来：马上又接。
	_, err = apiKeys.AssignRelayNode(context.Background(), []int64{key.ID}, e.nodeID, true)
	require.NoError(t, err)
	status, body = e.post(t, "/v1/responses", "sk-a", responses)
	require.Equal(t, http.StatusOK, status, body)
	e.world.waitReleased(t)
}
