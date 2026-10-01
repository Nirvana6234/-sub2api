package relayselect

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type compositeRoutes struct {
	service.CompositeModelRouteRepository
	routes []service.CompositeModelRoute
}

func (r compositeRoutes) ListByGroup(_ context.Context, groupID int64, _ bool) ([]service.CompositeModelRoute, error) {
	var out []service.CompositeModelRoute
	for _, route := range r.routes {
		if route.GroupID == groupID {
			out = append(out, route)
		}
	}
	return out, nil
}

// 组合平台分组经从节点（本地 compositeTarget 中间件，设计 3.2）：主节点按公开模型选目标平台和上游模型，从节点把
// 请求体里的模型改成上游模型再转发；计量平台按选定的目标（与单机 QuotaPlatform 一样）。目标不是 OpenAI、没有匹配的
// 交给主节点。
func TestNodeServesCompositeGroups(t *testing.T) {
	e := startE2E(t)
	composite := openAIGroup(7)
	composite.Platform = service.PlatformComposite
	e.world.keys.keys["sk-c"] = testKey("sk-c", 14, composite)
	e.world.sel.deps.Composite = service.NewCompositeRouteResolver(compositeRoutes{routes: []service.CompositeModelRoute{
		{ID: 1, GroupID: 7, PublicModel: "public-model", MatchType: service.CompositeRouteMatchExact, TargetPlatform: service.PlatformOpenAI,
			UpstreamModel: "gpt-5", Endpoint: service.CompositeRouteEndpointAny, Enabled: true},
		{ID: 2, GroupID: 7, PublicModel: "claude-public", MatchType: service.CompositeRouteMatchExact, TargetPlatform: service.PlatformAnthropic,
			UpstreamModel: "claude-sonnet-4-5", Endpoint: service.CompositeRouteEndpointAny, Enabled: true},
	}})

	status, body := e.post(t, "/v1/responses", "sk-c", `{"model":"public-model","input":"hi"}`)
	require.Equal(t, http.StatusOK, status, body)
	select {
	case r := <-e.hits:
		sent, _ := io.ReadAll(r.Body)
		require.Equal(t, "gpt-5", gjson.GetBytes(sent, "model").String(), "the node forwards the upstream model the master chose")
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	voucher, err := sign.VerifyVoucher(e.settler.records()[0].GetVoucher(), e.world.pub, e.nodeID, time.Now())
	require.NoError(t, err)
	require.Equal(t, service.PlatformOpenAI, voucher.GetContext().GetQuotaPlatform(), "platform quotas count the resolved target, like a single server")
	require.Equal(t, int64(7), voucher.GetGroupId())
	require.Equal(t, "public-model", voucher.GetRequestedModel(), "the usage log's requested model is what the client wrote, like a single server")
	e.world.waitReleased(t)

	// 目标是 Anthropic、没有匹配的路由：交给主节点（测试世界没有主节点转发，按 503 写）。
	for _, model := range []string{"claude-public", "no-such-route"} {
		status, body = e.post(t, "/v1/responses", "sk-c", `{"model":"`+model+`","input":"hi"}`)
		require.Equal(t, http.StatusServiceUnavailable, status, "%s: %s", model, body)
	}
	require.Len(t, e.hits, 0)
}

// 组合平台分组选到 Anthropic 目标：从节点把请求体里的模型改成上游模型，转发由 Anthropic 的 Messages 处理函数接；
// 用量的请求模型是改写前的公开模型、配额平台按目标（与单机一样）。选到的目标不是 Anthropic（这里是 OpenAI）的交给 OpenAI 入口。
func TestNodeServesCompositeGroupsResolvingToAnthropic(t *testing.T) {
	e := startE2EWith(t, func(upstream string) []service.Account {
		a := anthropicAccount(1, "claude", service.AccountTypeAPIKey)
		a.Credentials["base_url"] = upstream
		return []service.Account{a}
	})
	composite := openAIGroup(7)
	composite.Platform = service.PlatformComposite
	e.world.keys.keys["sk-c"] = testKey("sk-c", 14, composite)
	e.world.sel.deps.Composite = service.NewCompositeRouteResolver(compositeRoutes{routes: []service.CompositeModelRoute{
		{ID: 1, GroupID: 7, PublicModel: "claude-public", MatchType: service.CompositeRouteMatchExact, TargetPlatform: service.PlatformAnthropic,
			UpstreamModel: "claude-sonnet-4-5", Endpoint: service.CompositeRouteEndpointAny, Enabled: true},
	}})

	status, body := e.post(t, "/v1/messages", "sk-c", `{"model":"claude-public","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, http.StatusOK, status, body)
	select {
	case r := <-e.hits:
		sent, _ := io.ReadAll(r.Body)
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, "claude-sonnet-4-5", gjson.GetBytes(sent, "model").String(), "the node forwards the upstream model the master chose")
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	voucher, err := sign.VerifyVoucher(e.settler.records()[0].GetVoucher(), e.world.pub, e.nodeID, time.Now())
	require.NoError(t, err)
	require.Equal(t, service.PlatformAnthropic, voucher.GetContext().GetQuotaPlatform())
	require.Equal(t, "claude-public", voucher.GetRequestedModel())
	require.Equal(t, int64(7), voucher.GetGroupId())
	e.world.waitReleased(t)
}
