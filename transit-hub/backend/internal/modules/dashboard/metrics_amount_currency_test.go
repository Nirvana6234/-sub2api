package dashboard

import (
	"math"
	"testing"

	"transithub/backend/internal/modules/upstream"
)

// sub2api 的营收、余额、账号成本本身就是人民币，折算系数必须是 1。
// 生产事故复现：8/9 的成本 ¥409.57 被乘成 ¥2866.96，周期营收 ¥310.49 被乘成
// ¥2174.96，仪表盘因此显示净利润 -¥876.52，而实际每天都是盈利的。
func TestAmountToCNYRate_Sub2APIAmountsAreAlreadyCNY(t *testing.T) {
	if got := amountToCNYRate(upstream.PlatformSub2API, 7); got != 1 {
		t.Fatalf("sub2api 折算系数 = %v, want 1（再乘一次汇率会把整套账放大 7 倍）", got)
	}
}

// new-api 只有 quota，已折成美元，必须继续按配置汇率换算，不能被一并改成 1。
func TestAmountToCNYRate_NewAPIStillConverts(t *testing.T) {
	if got := amountToCNYRate(upstream.PlatformNewAPI, 7); got != 7 {
		t.Fatalf("new-api 折算系数 = %v, want 7（其金额是美元原值）", got)
	}
}

// 未知平台在取数时走的是 sub2api 实现，折算也必须跟着走 sub2api 口径，
// 否则新增平台会静默按美元多乘一次汇率。
func TestAmountToCNYRate_UnknownPlatformFollowsFetchDefault(t *testing.T) {
	for _, platform := range []upstream.Platform{upstream.PlatformAuto, upstream.Platform(""), upstream.Platform("future")} {
		if got := amountToCNYRate(platform, 7); got != 1 {
			t.Fatalf("平台 %q 折算系数 = %v, want 1（FetchAdminUsageAccounting 的 default 分支走 sub2api）", platform, got)
		}
	}
}

// 账号成本与营收必须用同一个系数，否则净利润又变成混币种相减。
func TestResolvePurchase_UsesSameRateAsRevenue(t *testing.T) {
	accounting := upstream.AdminUsageAccounting{RevenueUSD: 33.58, AccountCostUSD: 19.85, HasAccountCost: true}
	rate := amountToCNYRate(upstream.PlatformSub2API, 7)

	got := resolvePurchase(accounting, rate)
	if math.Abs(got.TotalCNY-19.85) > 1e-9 {
		t.Fatalf("成本 = %v, want 19.85", got.TotalCNY)
	}
	if got.Status != CostStatusAdminAccounted {
		t.Fatalf("成本状态 = %q, want %q", got.Status, CostStatusAdminAccounted)
	}
	if netProfit := accounting.RevenueUSD*rate - got.TotalCNY; netProfit <= 0 {
		t.Fatalf("净利润 = %v, 该日营收 33.58 成本 19.85 必须为正", netProfit)
	}
}

// 没有账号成本口径时，成本必须保持不可用，不能走 TransitHub 本地兜底。
func TestResolvePurchase_MissingAdminCostIsUnavailable(t *testing.T) {
	got := resolvePurchase(upstream.AdminUsageAccounting{RevenueUSD: 84.96}, 7)
	if got.TotalCNY != 0 || got.Status != CostStatusMissing {
		t.Fatalf("missing admin cost = %+v, want zero amount with missing status", got)
	}
}

// 账号成本为 0 与"没有这个口径"是两回事：前者是真的没花钱，必须采信。
func TestResolvePurchase_ZeroAccountCostIsStillAuthoritative(t *testing.T) {
	got := resolvePurchase(upstream.AdminUsageAccounting{AccountCostUSD: 0, HasAccountCost: true}, 1)
	if got.TotalCNY != 0 || got.Status != CostStatusAdminAccounted {
		t.Fatalf("零成本被误判为缺失: %+v", got)
	}
}
