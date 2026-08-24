package httpserver

import (
	"strings"
	"testing"

	"transithub/backend/internal/modules/dashboard"
	"transithub/backend/internal/modules/upstream"
)

// 营收折算只能用快照里随行存的系数。sub2api 报表本来就是人民币（系数 1），
// 拿配置里的美元汇率去重算会把整套账放大 7 倍——生产上出过这个事故。
func TestBusinessResultUsesRowRate(t *testing.T) {
	var sb strings.Builder
	writeBusinessResult(&sb, dailyReportData{
		Now: testNow(),
		Settlement: []dashboard.DailySnapshot{
			{TodayProfitUSD: 314.36, TodayPurchaseCNY: 235.86, USDToCNYRate: 1, IsFinalized: true},
		},
	})
	out := sb.String()
	if !strings.Contains(out, "¥314.36") {
		t.Fatalf("营收应按行内系数 1 折算：\n%s", out)
	}
	if strings.Contains(out, "¥2200") {
		t.Fatalf("营收被错误地乘了汇率：\n%s", out)
	}
}

func TestBusinessResultUsesTodaySnapshot(t *testing.T) {
	now := testNow()
	var sb strings.Builder
	writeBusinessResult(&sb, dailyReportData{
		Now: now,
		Settlement: []dashboard.DailySnapshot{
			{Date: now, TodayProfitUSD: 100, TodayPurchaseCNY: 40, USDToCNYRate: 1, CostStatus: dashboard.CostStatusAdminAccounted},
		},
	})
	out := sb.String()
	if !strings.Contains(out, "今日营收 ¥100.00") || !strings.Contains(out, "今日成本 ¥40.00") {
		t.Fatalf("当天快照应显示今日口径：\n%s", out)
	}
	if strings.Contains(out, "昨日营收") {
		t.Fatalf("当天快照不应继续显示昨日：\n%s", out)
	}
}

// 亏损那一行要能一眼看出来，不能和盈利长得一样。
func TestBusinessResultMarksLoss(t *testing.T) {
	var sb strings.Builder
	writeBusinessResult(&sb, dailyReportData{
		Now: testNow(),
		Settlement: []dashboard.DailySnapshot{
			{TodayProfitUSD: 64.75, TodayPurchaseCNY: 280.31, USDToCNYRate: 1, IsFinalized: true},
		},
	})
	out := sb.String()
	if !strings.Contains(out, "🔻") {
		t.Fatalf("亏损应有醒目标记：\n%s", out)
	}
	if !strings.Contains(out, "-215.56") {
		t.Fatalf("毛利算错了：\n%s", out)
	}
}

// 营收为 0 时毛利率没有意义，不能显示成 0% 或 -Inf。
func TestMarginTextZeroRevenue(t *testing.T) {
	if got := marginText(0, -50); got != "毛利率 —" {
		t.Fatalf("零营收的毛利率应留空，实际 %q", got)
	}
}

// 亏损分组必须被单独点名——全站毛利为正完全可能盖住一个在倒贴的大分组。
func TestGroupProfitNamesLosingGroups(t *testing.T) {
	var sb strings.Builder
	writeGroupProfit(&sb, dailyReportData{
		Now:       testNow(),
		Yesterday: "2026-08-21",
		GroupAccounting: []upstream.GroupAccounting{
			{GroupName: "plus", RevenueAmount: 180, CostAmount: 70, CostKnown: true},
			{GroupName: "kiro反代", RevenueAmount: 30, CostAmount: 96, CostKnown: true},
		},
	})
	out := sb.String()
	if !strings.Contains(out, "亏损分组") || !strings.Contains(out, "kiro反代") {
		t.Fatalf("亏损分组未被点名：\n%s", out)
	}
	if !strings.Contains(out, "亏 ¥66.00") {
		t.Fatalf("亏损金额算错：\n%s", out)
	}
	// 盈利分组排在亏损分组前面。
	if strings.Index(out, "plus") > strings.Index(out, "kiro反代") {
		t.Fatalf("分组未按毛利降序排列：\n%s", out)
	}
}

// 成本口径缺失时绝不能把毛利显示成等于营收，那会让分组看着全是纯利。
func TestGroupProfitUnknownCost(t *testing.T) {
	var sb strings.Builder
	writeGroupProfit(&sb, dailyReportData{
		Now:       testNow(),
		Yesterday: "2026-08-21",
		GroupAccounting: []upstream.GroupAccounting{
			{GroupName: "未归集分组", RevenueAmount: 8.36, CostKnown: false},
		},
	})
	out := sb.String()
	if !strings.Contains(out, "成本未归集") {
		t.Fatalf("成本缺失时应如实说明：\n%s", out)
	}
	if strings.Contains(out, "毛利率 100.0%") {
		t.Fatalf("成本缺失时不能把毛利算成等于营收：\n%s", out)
	}
}

// 余额告警与「余额预警」通知复用同一套阈值，两处结论不能打架。
func TestFundSafetyBalanceAlert(t *testing.T) {
	var sb strings.Builder
	writeFundSafety(&sb, sampleReportData(testNow()))
	out := sb.String()
	// 样本里 icodexs 余额 4.14 < 阈值 10，应被点名；mcgrox 折算后 ¥9.16 也低于阈值。
	if !strings.Contains(out, "余额告警") {
		t.Fatalf("低余额站点未被点名：\n%s", out)
	}
	if !strings.Contains(out, "api.icodexs.com") {
		t.Fatalf("icodexs 余额低于阈值却没报：\n%s", out)
	}
}

// 上游余额要能换算成「还能烧几天」，这是判断紧急程度的关键。
func TestRunwayEstimate(t *testing.T) {
	text := runwayText(1000, []dashboard.DailySnapshot{
		{TodayPurchaseCNY: 100},
		{TodayPurchaseCNY: 100},
	})
	// 两天各花 100，日均 100，余额 1000 → 10 天。
	if !strings.Contains(text, "10.0 天") {
		t.Fatalf("续航估算不对：%s", text)
	}
	// 日均为 0 时不能除零。
	if got := runwayText(1000, []dashboard.DailySnapshot{{TodayPurchaseCNY: 0}}); got != "" {
		t.Fatalf("零成本时不该给出续航估算，实际 %q", got)
	}
}

// 打一份完整样张出来看排版：go test -v -run TestOperationReportSample
func TestOperationReportSample(t *testing.T) {
	t.Logf("\n========== 运营日报样张 ==========\n%s==================================",
		renderDailyReport(sampleReportData(testNow())))
}
