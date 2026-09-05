package httpserver

import (
	"strings"
	"testing"
	"time"

	"transithub/backend/internal/modules/connection_health"
	"transithub/backend/internal/modules/dashboard"
	"transithub/backend/internal/modules/my_sites"
	"transithub/backend/internal/modules/settings"
	"transithub/backend/internal/modules/upstream"
)

func floatPtr(v float64) *float64 { return &v }
func strPtr(v string) *string     { return &v }
func int64Ptr(v int64) *int64     { return &v }

// metric 按真实数据的形态构造：Value 是 USD 原值，
// Display 是后端格式化好的 USD 数字字符串（不带币种符号）。
func metric(value float64, display string) upstream.MetricValue {
	return upstream.MetricValue{Value: &value, Display: display}
}

func testNow() time.Time {
	return time.Date(2026, time.August, 21, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
}

// sampleReportData 直接取自生产库 upstream_sites 的真实数值，
// 特别保留了 www.mcgrox.top —— 它的充值倍率是 0.1428 而非 1，
// 是检验「USD 额度 ≠ 人民币实付」这条口径的关键样本。
func sampleReportData(now time.Time) dailyReportData {
	return dailyReportData{
		Now: now,
		Strategy: settings.StrategySettings{
			DefaultBalanceThreshold: 10,
		},
		Sites: []upstream.Response{
			{
				ID: "site-mcgrox", Name: "www.mcgrox.top", Status: upstream.StatusConnected,
				RechargeRate: 0.1428,
				Metrics: upstream.Metrics{
					Balance:      metric(64.1726916, "64.1727"),
					TodayConsume: metric(42.909222, "42.9092"),
				},
				LastSyncedAt: int64Ptr(now.Add(-2 * time.Minute).UnixMilli()),
			},
			{
				ID: "site-mhapi", Name: "https://api.mhapi.cn", Status: upstream.StatusConnected,
				RechargeRate: 1,
				Metrics: upstream.Metrics{
					Balance:      metric(47.80384302, "47.8038"),
					TodayConsume: metric(1.41289098, "1.4129"),
				},
				LastSyncedAt: int64Ptr(now.Add(-3 * time.Minute).UnixMilli()),
			},
			{
				ID: "site-tntapi", Name: "https://tntapi.com", Status: upstream.StatusConnected,
				RechargeRate: 1,
				Metrics: upstream.Metrics{
					Balance:      metric(40.28400321, "40.284"),
					TodayConsume: metric(0.52942045, "0.5294"),
				},
				LastSyncedAt: int64Ptr(now.Add(-3 * time.Minute).UnixMilli()),
			},
			{
				// 今天没消费、没成本、没变动、同步正常、余额高于阈值 —— 应被折叠
				ID: "site-keiko", Name: "https://keiko.lol", Status: upstream.StatusConnected,
				RechargeRate: 1,
				Metrics: upstream.Metrics{
					Balance:      metric(20.31949741, "20.3195"),
					TodayConsume: metric(0, "0"),
				},
				LastSyncedAt: int64Ptr(now.Add(-4 * time.Minute).UnixMilli()),
			},
			{
				ID: "site-icodexs", Name: "https://api.icodexs.com", Status: upstream.StatusConnected,
				RechargeRate: 1,
				Metrics: upstream.Metrics{
					Balance:      metric(4.13779048, "4.1378"),
					TodayConsume: metric(0.003045, "0.003"),
				},
				LastSyncedAt: int64Ptr(now.Add(-9 * time.Hour).UnixMilli()),
			},
			{
				// 登录失败的站点：余额指标为空，Display 是占位的 "-"
				ID: "site-youc", Name: "https://ai.youc.online", Status: upstream.StatusError,
				ErrorKey:     strPtr("admin.upstream.errors.auth"),
				RechargeRate: 1,
				Metrics: upstream.Metrics{
					Balance:      upstream.MetricValue{Display: "-"},
					TodayConsume: upstream.MetricValue{Display: "-"},
				},
				LastSyncedAt: nil,
			},
		},
		// USDToCNYRate 必须显式给 1：生产上 admin 站点是 sub2api，报表本来就是
		// 人民币，快照里存的就是 1。留空会走 EffectiveRate() 的默认回退（7），
		// 把营收凭空放大 7 倍——生产上真出过这个事故。
		Settlement: []dashboard.DailySnapshot{
			{Date: now.AddDate(0, 0, -2), TodayPurchaseCNY: 210.00, TodayProfitUSD: 280.00, SiteBalanceUSD: 1200, USDToCNYRate: 1, UpstreamBalanceCNY: 5400, CostStatus: dashboard.CostStatusAdminAccounted, IsFinalized: true},
			{Date: now.AddDate(0, 0, -1), TodayPurchaseCNY: 235.86, TodayProfitUSD: 314.36, SiteBalanceUSD: 1240, USDToCNYRate: 1, UpstreamBalanceCNY: 5180, CostStatus: dashboard.CostStatusAdminAccounted, IsFinalized: true},
		},
		Yesterday: "2026-08-21",
		GroupAccounting: []upstream.GroupAccounting{
			{GroupName: "plus", RevenueAmount: 180.00, CostAmount: 70.00, CostKnown: true},
			{GroupName: "gpt-pro", RevenueAmount: 96.00, CostAmount: 40.00, CostKnown: true},
			// 亏损分组：营收盖不住采购成本，必须被单独点名。
			{GroupName: "claude-kiro反代", RevenueAmount: 30.00, CostAmount: 96.00, CostKnown: true},
			// 成本口径缺失：不能把毛利显示成等于营收。
			{GroupName: "未归集分组", RevenueAmount: 8.36, CostAmount: 0, CostKnown: false},
		},
		Changes: []connection_health.MultiplierChange{
			{SiteID: "site-tntapi", GroupName: "openAI plus", Previous: 0.1, Current: 0.12, ObservedAt: now.Add(-4 * time.Hour)},
		},
		UnmappedChanges: []upstream.MultiplierEvent{
			{SiteID: "site-mhapi", SiteName: "https://api.mhapi.cn", GroupID: "g-unmapped", GroupName: "未映射通道", PreviousMultiplier: 0.05, CurrentMultiplier: 0.06, ObservedAt: now.Add(-3 * time.Hour)},
			{SiteID: "site-mhapi", SiteName: "https://api.mhapi.cn", GroupID: "g-unmapped", GroupName: "未映射通道", PreviousMultiplier: 0.06, CurrentMultiplier: 0.055, ObservedAt: now.Add(-2 * time.Hour)},
		},
		TodayCosts: []my_sites.TargetAccountCost{
			// 关键对照：mcgrox 的 metrics.todayConsume 是 42.9092，那是上游对
			// 用户的扣费；本方 Sub2API 记录的账号采购成本只有 ¥0.57。
			// 简报必须显示后者。这两个数差着两个数量级，一旦搞混就会
			// 把「用户花了多少」当成「我们花了多少」。
			//
			// 注意 CostCNY 是原样取自 account_cost 的：sub2api 的金额本身
			// 就是人民币口径，不存在也不需要任何折算系数。
			{SiteID: "site-mcgrox", CostCNY: 0.5712},
			{SiteID: "site-mhapi", CostCNY: 0.7},
			{SiteID: "site-tntapi", CostCNY: 0.08},
			{SiteID: "site-icodexs", CostCNY: 0.001},
		},
		CostFrom: now.AddDate(0, 0, -7).Format("2006-01-02"),
		CostTo:   now.AddDate(0, 0, -1).Format("2006-01-02"),
		GroupCosts: []my_sites.TargetAccountCost{
			// 同样是账号采购成本，按「自有分组 + 绑定账号」查出来后挂在对应的
			// 上游目标上。这里的 GroupName 是上游分组名，成本却是按自有分组算的。
			{SiteID: "site-mcgrox", GroupName: "claude-sonnet", CostCNY: 38.31},
			{SiteID: "site-mcgrox", GroupName: "claude-opus", CostCNY: 8.59},
			{SiteID: "site-mhapi", GroupName: "gpt-4o", CostCNY: 21.44},
			{SiteID: "site-tntapi", GroupName: "openAI plus", CostCNY: 8.92},
			{SiteID: "site-icodexs", GroupName: "gemini-pro", CostCNY: 1.07},
		},
		// 算不出成本的上游。取自生产实际情况：多数是没绑成本账号，
		// 还有映射指向了已删除的站点。
		Unresolved: []my_sites.UnresolvedTarget{
			{OwnGroup: "plus", SiteID: "site-mhapi", GroupName: "激励gpt", Reason: my_sites.ReasonUnbound},
			{OwnGroup: "plus", SiteID: "site-tntapi", GroupName: "GPT特价", Reason: my_sites.ReasonUnbound},
			{OwnGroup: "生图分组image-2", SiteID: "288e8336-c5f4-47fb-9b7c-75bbd5b673a6",
				GroupName: "生图专用分组", Reason: my_sites.ReasonGroupMissing},
		},
	}
}

// 算不出成本的上游必须单独列出来。硬凑一个账号会把成本算错，
// 而悄悄省略会让总额偏低且无人知道少了谁——所以只能如实报缺口。
func TestUnresolvedSectionListsGaps(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))

	if !strings.Contains(report, "未归集成本的上游（3 个）") {
		t.Error("缺少未归集成本段落或数量不对")
	}
	if !strings.Contains(report, "未绑成本账号") || !strings.Contains(report, "激励gpt") {
		t.Error("未绑定的上游没有被列出来")
	}
	// 映射指向已删除的站点时，用短 ID 标出来，至少能定位
	if !strings.Contains(report, "站点已删除") || !strings.Contains(report, "288e8336") {
		t.Errorf("已删除站点未被标注：\n%s", report)
	}
}

// 没有缺口时不该出现这一段，免得每天都挂个空标题。
func TestUnresolvedSectionOmittedWhenEmpty(t *testing.T) {
	data := sampleReportData(testNow())
	data.Unresolved = nil

	if strings.Contains(renderDailyReport(data), "未归集成本") {
		t.Error("没有缺口时不应输出该段落")
	}
}

// 主要用途是把简报样张打出来（go test -v -run TestDailyReportSample），
// 顺带断言关键段落，防止排版被改坏。
func TestDailyReportSample(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))

	t.Logf("\n========== 简报样张 ==========\n%s==============================", report)

	for _, want := range []string{
		"共飞后台运营日报",
		"━━ 一、经营结果 ━━",
		"━━ 二、资金安全 ━━",
		"━━ 三、分组经营",
		"━━ 四、通道异常 ━━",
		"━━ 六、站点明细",
		"━━ www.mcgrox.top ━━",
		"━━ https://tntapi.com ━━",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("简报缺少段落 %q", want)
		}
	}
}

func TestUnmappedMultiplierChangesAreAggregated(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))
	if !strings.Contains(report, "未对接分组倍率变动（近 24 小时，未即时通知）") {
		t.Fatal("日报缺少未映射倍率变动段落")
	}
	if !strings.Contains(report, "0.05x → **0.055x**") || !strings.Contains(report, "期间变动 2 次") {
		t.Fatalf("未映射倍率变动没有聚合首尾值和次数：\n%s", report)
	}
}

func TestUnmappedMultiplierChangesSectionOmittedWhenEmpty(t *testing.T) {
	data := sampleReportData(testNow())
	data.UnmappedChanges = nil
	if strings.Contains(renderDailyReport(data), "未对接分组倍率变动") {
		t.Fatal("没有未映射事件时不应输出该段")
	}
}

// 每个站点的消费、成本、倍率变动、健康状况必须集中在它自己那一块里，
// 而不是散落在几个横切段落中。
func TestSiteBlockKeepsSiteDataTogether(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))

	blockStart := strings.Index(report, "━━ https://tntapi.com ━━")
	if blockStart < 0 {
		t.Fatal("找不到 tntapi 的站点块")
	}
	// 取该块到下一个站点块之间的内容
	rest := report[blockStart+len("━━ https://tntapi.com ━━"):]
	if next := strings.Index(rest, "\n**━━ "); next >= 0 {
		rest = rest[:next]
	}

	for _, want := range []string{
		"今日 ¥0.08",              // 账号真实成本
		"余额 ¥40.28（40.284 USD）", // 余额
		"openAI plus ¥8.92",     // 该站点的分组成本
		"今日倍率变动 1 次",            // 该站点的倍率变动
		"0.1x → **0.12x**",
	} {
		if !strings.Contains(rest, want) {
			t.Errorf("tntapi 的站点块里缺少 %q，实际内容：\n%s", want, rest)
		}
	}
}

// 一个站点的分组成本不能跑到别的站点块里去。
func TestSiteBlockDoesNotLeakOtherSitesCosts(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))

	blockStart := strings.Index(report, "━━ https://tntapi.com ━━")
	rest := report[blockStart:]
	if next := strings.Index(rest[10:], "\n**━━ "); next >= 0 {
		rest = rest[:next+10]
	}
	if strings.Contains(rest, "claude-sonnet") {
		t.Error("mcgrox 的分组成本泄漏到了 tntapi 的块里")
	}
}

// 余额要按站点充值倍率折成人民币。RechargeRate 不是汇率，而是
// 「这个站点每 1 USD 额度实付多少人民币」，各站点差异极大：
// mcgrox 是 0.1428，其余多为 1。当成统一汇率会把余额算错一个数量级。
func TestSiteBlockBalanceUsesRechargeRate(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))

	// 64.1726916 × 0.1428 = 9.1638...
	if !strings.Contains(report, "余额 ¥9.16（64.1727 USD）") {
		t.Error("mcgrox 余额未按站点充值倍率 0.1428 折算")
	}
	// 倍率为 1 的站点，人民币与 USD 数值相同
	if !strings.Contains(report, "余额 ¥47.80（47.8038 USD）") {
		t.Error("倍率为 1 的站点折算结果不正确")
	}
}

// 今日金额必须是账号采购成本，不是上游对用户的扣费。
//
// 这两个数在 mcgrox 上差了两个数量级：用户扣费 42.9092，采购成本 ¥0.57。
// 另外账号成本取自本方 Sub2API，本身就是人民币口径，
// 不该再乘站点充值倍率（那是折算上游额度用的，与本方账号成本无关）。
func TestSiteBlockTodayUsesAccountCostNotUserCharge(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))
	block := siteBlockOf(t, report, "www.mcgrox.top")

	if !strings.Contains(block, "今日 ¥0.57") {
		t.Errorf("今日金额未采用账号采购成本，实际块内容：\n%s", block)
	}
	// 6.13 = 42.9092 × 0.1428，即「把用户扣费当成采购成本」时会算出的数
	if strings.Contains(block, "¥6.13") {
		t.Error("简报错误地把上游对用户的扣费当成了采购成本")
	}
	// 0.08 = 0.5712 × 0.1428，即「对账号成本又多乘一次充值倍率」时会算出的数
	if strings.Contains(block, "今日 ¥0.08") {
		t.Error("账号成本被多乘了一次充值倍率")
	}
}

// siteBlockOf 截取某个站点在简报里的那一块。
// 跨站点断言容易误判——比如两个站点的金额恰好都是 ¥0.08，
// 在全文里 Contains 就分不清是谁的。
func siteBlockOf(t *testing.T, report, siteName string) string {
	t.Helper()
	marker := "**━━ " + siteName + " ━━**"
	start := strings.Index(report, marker)
	if start < 0 {
		t.Fatalf("简报里找不到站点块 %q", siteName)
	}
	rest := report[start+len(marker):]
	if next := strings.Index(rest, "\n**━━ "); next >= 0 {
		rest = rest[:next]
	}
	return rest
}

// 余额跌破阈值要报警，未跌破要显示还差多少。阈值是人民币口径。
func TestSiteBlockBalanceThreshold(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))

	if !strings.Contains(report, "⚠️ 已跌破预警线 ¥10.00") {
		t.Error("跌破阈值的站点未告警")
	}
	if !strings.Contains(report, "距预警线 ¥37.80") {
		t.Error("距预警线的余量计算不正确")
	}
}

// 站点块按人民币账号成本降序：USD 额度跨站点不可比。
func TestSiteBlocksSortByCNYSpend(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))

	mcgrox := strings.Index(report, "━━ www.mcgrox.top ━━")
	mhapi := strings.Index(report, "━━ https://api.mhapi.cn ━━")
	if mcgrox < 0 || mhapi < 0 {
		t.Fatal("站点块缺失")
	}
	// mhapi 今日成本 ¥0.70 > mcgrox ¥0.57
	if mhapi > mcgrox {
		t.Error("站点块未按人民币实付降序排列")
	}
}

// 完全没动静的站点折叠成一行，不单独占一块。
func TestQuietSitesAreCollapsed(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))

	if strings.Contains(report, "━━ https://keiko.lol ━━") {
		t.Error("无活动的站点不应单独成块")
	}
	if !strings.Contains(report, "其余 1 个站点无活动") {
		t.Error("缺少无活动站点的折叠行")
	}
	if !strings.Contains(report, "https://keiko.lol") {
		t.Error("折叠行里应当列出站点名")
	}
}

// 余额已跌破预警线的站点必须露面，哪怕今天一分钱没花。
func TestLowBalanceSiteIsNotCollapsed(t *testing.T) {
	now := testNow()
	site := upstream.Response{
		ID: "site-poor", Name: "poor-site", Status: upstream.StatusConnected,
		RechargeRate: 1,
		Metrics: upstream.Metrics{
			Balance:      metric(2, "2"),
			TodayConsume: metric(0, "0"),
		},
		LastSyncedAt: int64Ptr(now.Add(-time.Minute).UnixMilli()),
	}
	strategy := settings.StrategySettings{DefaultBalanceThreshold: 10}

	if isQuietSiteForReport(site, my_sites.TargetAccountCost{}, nil, nil, strategy, now) {
		t.Error("余额跌破预警线的站点不应被折叠")
	}
}

// 有账号采购成本的站点不能被折叠，哪怕它没有分组明细和倍率变动。
func TestSiteWithTodayCostIsNotCollapsed(t *testing.T) {
	now := testNow()
	site := upstream.Response{
		ID: "site-busy", Name: "busy-site", Status: upstream.StatusConnected,
		RechargeRate: 1,
		Metrics: upstream.Metrics{
			Balance:      metric(500, "500"),
			TodayConsume: metric(0, "0"),
		},
		LastSyncedAt: int64Ptr(now.Add(-time.Minute).UnixMilli()),
	}
	strategy := settings.StrategySettings{DefaultBalanceThreshold: 10}

	todayCost := my_sites.TargetAccountCost{SiteID: "site-busy", CostCNY: 3.2}
	if isQuietSiteForReport(site, todayCost, nil, nil, strategy, now) {
		t.Error("今日有采购成本的站点不应被折叠")
	}
}

// 指标为空的站点不能渲染成 ¥0.00，那会被误读成「今天没花钱」。
func TestMissingMetricRendersAsDash(t *testing.T) {
	if got := amountText(upstream.MetricValue{Display: "-"}, 1); got != "—" {
		t.Errorf("无数据的指标应显示为破折号，实际 %q", got)
	}
}

// 环比基于同源的前后两天：235.86 相对 210.00 是 +12.3%。
func TestOverviewComputesDelta(t *testing.T) {
	report := renderDailyReport(sampleReportData(testNow()))

	if !strings.Contains(report, "环比 ↑ +12.3%") {
		t.Errorf("环比计算或格式不正确：\n%s", report)
	}
}

// 前一日没有采购时不能除零，要显式说明环比略过。
func TestOverviewHandlesZeroBaseline(t *testing.T) {
	var sb strings.Builder
	writeBusinessResult(&sb, dailyReportData{
		Now: testNow(),
		Settlement: []dashboard.DailySnapshot{
			{TodayPurchaseCNY: 0, USDToCNYRate: 1},
			{TodayPurchaseCNY: 120, USDToCNYRate: 1},
		},
	})
	if !strings.Contains(sb.String(), "前日无数据，环比略") {
		t.Errorf("零基数环比未妥善处理：%s", sb.String())
	}
}

// 认证失败和久未同步都要在各自的站点块里点名。
func TestSiteHealthIssues(t *testing.T) {
	now := testNow()

	authFailed := upstream.Response{
		Status: upstream.StatusError, ErrorKey: strPtr("admin.upstream.errors.auth"),
	}
	if issue := siteHealthIssue(authFailed, now); !strings.Contains(issue, "认证失败") {
		t.Errorf("错误码未翻译成中文说明：%q", issue)
	}

	stale := upstream.Response{
		Status: upstream.StatusConnected, LastSyncedAt: int64Ptr(now.Add(-9 * time.Hour).UnixMilli()),
	}
	if issue := siteHealthIssue(stale, now); !strings.Contains(issue, "已 9 小时未同步") {
		t.Errorf("久未同步未被识别：%q", issue)
	}

	healthy := upstream.Response{
		Status: upstream.StatusConnected, LastSyncedAt: int64Ptr(now.Add(-time.Minute).UnixMilli()),
	}
	if issue := siteHealthIssue(healthy, now); issue != "" {
		t.Errorf("正常站点不应有健康问题：%q", issue)
	}
}

func TestNeverAuthenticatedSitesAreExcludedFromReport(t *testing.T) {
	now := testNow()
	data := dailyReportData{
		Now: now,
		Sites: []upstream.Response{
			{ID: "never", Name: "never-auth", Status: upstream.StatusError, LastSyncedAt: nil},
			{ID: "stale", Name: "stale-site", Status: upstream.StatusError, LastSyncedAt: int64Ptr(now.Add(-9 * time.Hour).UnixMilli())},
		},
		Changes: []connection_health.MultiplierChange{{SiteID: "never", GroupName: "hidden", Previous: 1, Current: 2}},
	}
	report := renderDailyReport(data)
	if strings.Contains(report, "never-auth") || strings.Contains(report, "hidden") {
		t.Fatalf("never-authenticated site leaked into report:\n%s", report)
	}
	if !strings.Contains(report, "stale-site") || !strings.Contains(report, "最后一次同步") {
		t.Fatalf("previously synced stale site should remain reportable:\n%s", report)
	}
}

// 没有任何数据时要有兜底文案，不能出现空简报。
func TestRenderHandlesEmptyData(t *testing.T) {
	report := renderDailyReport(dailyReportData{Now: testNow(), CostFrom: "2026-08-14", CostTo: "2026-08-20"})

	for _, want := range []string{"一、经营结果", "暂无上游站点", "暂无结算数据"} {
		if !strings.Contains(report, want) {
			t.Errorf("空数据兜底缺少 %q", want)
		}
	}
}

// 充值倍率缺失或为 0 时按 1 处理，绝不能把余额算成 0。
func TestEffectiveRechargeRateFallback(t *testing.T) {
	if got := effectiveRechargeRate(upstream.Response{RechargeRate: 0}); got != 1 {
		t.Errorf("倍率为 0 时应回退到 1，实际 %v", got)
	}
	if got := effectiveRechargeRate(upstream.Response{RechargeRate: -3}); got != 1 {
		t.Errorf("倍率为负时应回退到 1，实际 %v", got)
	}
	if got := effectiveRechargeRate(upstream.Response{RechargeRate: 0.1428}); got != 0.1428 {
		t.Errorf("正常倍率被改动：%v", got)
	}
}
