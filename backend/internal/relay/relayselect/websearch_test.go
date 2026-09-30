package relayselect

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodegw"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 主节点汇总各节点的联网搜索用量（与单机同一个 Redis 计数），剩余按在线节点分摊成份额；不限额的服务不回份额。
func TestWebSearchUsageSharesOnTheMaster(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))
	mr := miniredis.RunT(t)
	mgr := websearch.NewManager([]websearch.ProviderConfig{{Type: "brave", APIKey: "k", QuotaLimit: 10}, {Type: "tavily", APIKey: "k"}},
		redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	w.sel.webSearchManager = func() *websearch.Manager { return mgr }
	w.sel.env.OnlineNodes = func() int { return 3 }

	out, err := w.sel.ReportWebSearchUsage(ctx, testNode, &relayv1.WebSearchUsageReport{Used: map[string]int64{"brave": 4, "tavily": 9}})
	require.NoError(t, err)
	require.Len(t, out.GetShares(), 1, "unlimited providers need no share")
	require.Equal(t, int64(4), out.GetShares()[0].GetUsed())
	require.Equal(t, int64(2), out.GetShares()[0].GetAllowance(), "(10-4)/3")
	used, _ := mgr.GetUsage(ctx, "brave")
	require.Equal(t, int64(4), used, "the single-server counter")

	out, _ = w.sel.ReportWebSearchUsage(ctx, testNode, &relayv1.WebSearchUsageReport{Used: map[string]int64{"brave": 5}})
	require.Equal(t, int64(1), out.GetShares()[0].GetAllowance(), "one left: every node may still try")
	out, _ = w.sel.ReportWebSearchUsage(ctx, testNode, &relayv1.WebSearchUsageReport{Used: map[string]int64{"brave": 1}})
	require.Equal(t, int64(0), out.GetShares()[0].GetAllowance(), "exhausted")

	w.sel.webSearchManager = func() *websearch.Manager { return nil }
	out, err = w.sel.ReportWebSearchUsage(ctx, testNode, &relayv1.WebSearchUsageReport{})
	require.NoError(t, err)
	require.Empty(t, out.GetShares(), "web search off on the master")
}

// 端到端：从节点经主从连接上报用量，拿回份额。
func TestNodeReportsWebSearchUsage(t *testing.T) {
	e := startE2E(t)
	ctx := context.Background()
	mr := miniredis.RunT(t)
	mgr := websearch.NewManager([]websearch.ProviderConfig{{Type: "brave", APIKey: "k", QuotaLimit: 10}}, redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	e.world.sel.webSearchManager = func() *websearch.Manager { return mgr }

	q := node.NewWebSearchQuota(e.client)
	cfg := websearch.ProviderConfig{Type: "brave", QuotaLimit: 10}
	for i := 0; i < 3; i++ {
		ok, _ := q.Reserve(ctx, cfg)
		require.True(t, ok)
	}
	require.NoError(t, q.Report(ctx))
	used, _ := mgr.GetUsage(ctx, "brave")
	require.Equal(t, int64(3), used)
	nodeUsed, _ := q.Usage(ctx, "brave")
	require.Equal(t, int64(3), nodeUsed)
}

// 从节点用加密下发的联网搜索配置（含服务 Key 和代理）建搜索管理器，与单机同一段代码。
func TestNodeBuildsWebSearchFromSealedConfig(t *testing.T) {
	useMasterSettings(t, map[string]string{
		service.SettingKeyWebSearchEmulationConfig: `{"enabled":true,"providers":[{"type":"brave","api_key":"sk-brave-secret","quota_limit":50}]}`,
	})
	e := startE2E(t)
	t.Cleanup(func() { service.SetWebSearchManager(nil) })
	service.SetWebSearchManager(nil)

	nodegw.SetupWebSearch(context.Background(), e.nodeSettings, e.nodeCache, node.NewWebSearchQuota(e.client))
	mgr := service.CurrentWebSearchManager()
	require.NotNil(t, mgr, "the node has the web search config, including the provider key")
	configs := mgr.Configs()
	require.Len(t, configs, 1)
	require.Equal(t, "sk-brave-secret", configs[0].APIKey)
	require.Equal(t, int64(50), configs[0].QuotaLimit)
	_, isNodeQuota := mgr.Quota().(*node.WebSearchQuota)
	require.True(t, isNodeQuota, "quota is counted on the node and reported to the master")
}
