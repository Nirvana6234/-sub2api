package nodegw

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SetupWebSearch 让从节点自己执行联网搜索（设计 3.3）：配置和服务 Key、代理来自配置快照里加密下发的部分，
// 用单机同一段代码建搜索管理器，配额用本机计数 + 定期汇总（node.WebSearchQuota）。换快照后调
// settings.RebuildWebSearchManager 重建。
func SetupWebSearch(ctx context.Context, settings *service.SettingService, cache *node.ConfigCache, quota websearch.QuotaStore) {
	settings.SetProxyRepository(node.NewSealedProxies(cache))
	settings.SetWebSearchManagerBuilder(ctx, func(cfg *service.WebSearchEmulationConfig, proxyURLs map[int64]string) {
		configs, ok := service.WebSearchProviderConfigs(cfg, proxyURLs)
		if !ok {
			service.SetWebSearchManager(nil)
			return
		}
		service.SetWebSearchManager(websearch.NewManagerWithQuota(configs, quota))
	})
}
