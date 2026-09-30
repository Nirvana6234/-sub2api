package relayselect

import (
	"context"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// usageAdder 是能加上各节点报上来的用量的配额存储（主节点的 Redis 实现）。
type usageAdder interface {
	Add(ctx context.Context, cfg websearch.ProviderConfig, delta int64) (int64, error)
}

// ReportWebSearchUsage 联网搜索用量汇总（设计 3.3）：搜索在从节点执行，配额是全局的。把节点报上来的用量加进
// 全局计数（与单机同一个 Redis 计数、同样的按订阅日重置），回每个有上限的服务的已用和这台的份额：剩余按在线
// 节点分摊，还有剩余时至少 1。主节点自己没开联网搜索时回空（节点那边按"没有份额"放行，与单机 Redis 不可用时一样）。
func (s *selector) ReportWebSearchUsage(ctx context.Context, nodeID int64, req *relayv1.WebSearchUsageReport) (*relayv1.WebSearchUsageShares, error) {
	mgr := s.webSearch()
	if mgr == nil {
		return &relayv1.WebSearchUsageShares{}, nil
	}
	adder, _ := mgr.Quota().(usageAdder)
	online := 1
	if s.env.OnlineNodes != nil {
		if n := s.env.OnlineNodes(); n > 1 {
			online = n
		}
	}
	out := &relayv1.WebSearchUsageShares{}
	for _, cfg := range mgr.Configs() {
		if cfg.QuotaLimit <= 0 {
			continue
		}
		used, err := mgr.GetUsage(ctx, cfg.Type)
		// 负数是节点上报之后又失败退回的（与单机失败时回退一致）。
		if delta := req.GetUsed()[cfg.Type]; delta != 0 && adder != nil {
			if used, err = adder.Add(ctx, cfg, delta); err != nil {
				return nil, err
			}
		}
		if err != nil {
			slog.Warn("relay: web search usage unavailable", "node_id", nodeID, "provider", cfg.Type, "error", err)
			continue
		}
		allowance := int64(0)
		if remaining := cfg.QuotaLimit - used; remaining > 0 {
			allowance = remaining / int64(online)
			if allowance < 1 {
				allowance = 1
			}
		}
		out.Shares = append(out.Shares, &relayv1.WebSearchShare{Provider: cfg.Type, Limit: cfg.QuotaLimit, Used: used, Allowance: allowance})
	}
	return out, nil
}

// webSearch 返回主节点当前的联网搜索管理器（测试可替换）。
func (s *selector) webSearch() *websearch.Manager {
	if s.webSearchManager != nil {
		return s.webSearchManager()
	}
	return service.CurrentWebSearchManager()
}
