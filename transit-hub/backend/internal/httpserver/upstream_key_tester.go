package httpserver

import (
	"context"

	"transithub/backend/internal/modules/connection_health"
	"transithub/backend/internal/modules/my_sites"
)

// upstreamKeyTester 把 connection_health 的探活能力适配成 my_sites 需要的窄接口。
//
// 【为什么要这层适配】connection_health 已经 import 了 my_sites（分组健康要读
// RealConnection 和倍率快照），my_sites 反过来 import 它会形成循环。所以 my_sites
// 只用基础类型声明能力，由这里——一个已经同时依赖两边的地方——把两者接起来。
//
// 探活逻辑本身一行不重写：/v1/models 用 ModelDiscoveryRunner，真实请求用
// RealProbeRunner，包括它的 responses → chat/completions 回退和脱敏。
type upstreamKeyTester struct {
	discovery *connection_health.ModelDiscoveryRunner
	probe     *connection_health.RealProbeRunner
}

func newUpstreamKeyTester() *upstreamKeyTester {
	return &upstreamKeyTester{
		discovery: connection_health.NewModelDiscoveryRunner(),
		probe:     connection_health.NewRealProbeRunner(),
	}
}

func (t *upstreamKeyTester) ListModels(ctx context.Context, baseURL string, key string) ([]string, error) {
	discovered, err := t.discovery.ListModels(ctx, baseURL, key)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(discovered))
	for _, model := range discovered {
		models = append(models, model.ID)
	}
	return models, nil
}

func (t *upstreamKeyTester) ProbeChat(ctx context.Context, baseURL string, key string, model string) my_sites.UpstreamProbeResult {
	outcome := t.probe.Probe(ctx, connection_health.ProbeRequest{
		BaseURL:     baseURL,
		UpstreamKey: key,
		ModelName:   model,
		// 上游站点（sub2api / new-api）都是 OpenAI 兼容中转，走 chat/completions
		// 这条最通用的路径；RealProbeRunner 内部还会按需回退，不用在这里判平台。
		ProviderFamily: "openai",
		// 只要证明链路能出词，1 个 token 就够。测试是运维随手点的，
		// 默认值必须便宜到可以随便点。
		MaxTokens: 1,
	})
	return my_sites.UpstreamProbeResult{
		OK:        outcome.Result == connection_health.ResultOK,
		Result:    string(outcome.Result),
		LatencyMs: outcome.LatencyMs,
		Detail:    outcome.Detail,
	}
}
