package dashboard

import (
	"testing"

	"transithub/backend/internal/modules/upstream"
)

// 成本口径优先取 admin 站点自身的账号成本（sub2api total_account_cost）。
//
// 回归的是一个真实的核算错误：成本原本绕道各上游站点的日汇总接口，再乘每个站点
// 配置的 rechargeRate。那条路有两个固有缺陷——上游只能给出「当天累计用量」，
// 必须搭配一个事后探测到的当前倍率，等于把倍率变更追溯应用到变更前的流量；
// 而且任何一个站点的 rechargeRate 填错（生产上出现过 0.143，即把 1/7 填进了
// 「USD×倍率=CNY」的字段）都会让总成本失真。
//
// admin 站点的 total_account_cost 由 SUM(account_stats_cost × account_rate_multiplier)
// 得出，倍率是每条 usage log 上的快照，天然按请求发生时刻分段计价，且与营收
// 来自同一次查询、同一时间范围、同一时区。
func TestResolvePurchasePrefersAdminAccountCost(t *testing.T) {
	accounting := upstream.AdminUsageAccounting{
		RevenueUSD:     33.02,
		AccountCostUSD: 16.5,
		HasAccountCost: true,
	}

	got := resolvePurchase(accounting, 7)
	if got.TotalCNY != 16.5*7 {
		t.Fatalf("expected admin account cost converted at the reporting rate, got %v", got.TotalCNY)
	}
	if got.Status != CostStatusAdminAccounted {
		t.Fatalf("expected admin_accounted status, got %s", got.Status)
	}
}

func TestResolvePurchaseRejectsMissingAdminCost(t *testing.T) {
	// 旧版 sub2api 与 new-api 不返回 total_account_cost。此时必须沿用上游站点
	// 采集的结果，绝不能把缺失当成 0 让净利润凭空变好看。
	got := resolvePurchase(upstream.AdminUsageAccounting{RevenueUSD: 10}, 7)

	if got.TotalCNY != 0 {
		t.Fatalf("missing admin cost must not be reconstructed, got %v", got.TotalCNY)
	}
	if got.Status != CostStatusMissing {
		t.Fatalf("missing admin cost status = %s, want %s", got.Status, CostStatusMissing)
	}
}

func TestResolvePurchaseKeepsZeroAdminCostDistinctFromMissing(t *testing.T) {
	// 上游成本真的是 0（当天没有转发任何请求）与「这个平台不提供该口径」是两回
	// 事：前者应该覆盖掉上游采集值，后者必须回退。
	got := resolvePurchase(upstream.AdminUsageAccounting{HasAccountCost: true}, 7)

	if got.TotalCNY != 0 {
		t.Fatalf("a real zero cost must remain zero, got %v", got.TotalCNY)
	}
	if got.Status != CostStatusAdminAccounted {
		t.Fatalf("expected admin_accounted status, got %s", got.Status)
	}
}
