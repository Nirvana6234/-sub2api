package httpserver

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"transithub/backend/internal/modules/connection_health"
	"transithub/backend/internal/modules/dashboard"
	"transithub/backend/internal/modules/my_sites"
	"transithub/backend/internal/modules/settings"
	"transithub/backend/internal/modules/upstream"
	"transithub/backend/internal/shared/authctx"
	"transithub/backend/internal/shared/httpjson"
)

const (
	// dailyReportTimezone 与仪表盘业务日、reportingDate 共用同一套报表时钟，
	// 否则简报里的「今天」会和站点页面上的「今天」错开一天。
	dailyReportTimezone = "Asia/Shanghai"

	// staleSyncThreshold 超过这个时长没同步就在简报里点名。
	// 刷新间隔最低才 60 秒，正常情况下远不会拖到 6 小时，
	// 一旦超过基本可以断定同步链路已经停摆。
	staleSyncThreshold = 6 * time.Hour

	// groupCostDays 分组成本的回溯天数（不含今天，今天还没结束）。
	groupCostDays = 7

	// maxGroupsPerSite 单个站点最多列几个分组，超出的并成一行。
	// 站点多的时候全列会把消息撑成一堵墙，反而没人看。
	maxGroupsPerSite = 5

	// maxUnresolvedPerReason 每种「算不出成本」的原因最多列几条。
	maxUnresolvedPerReason = 8

	// maxGroupsInProfitSection 分组经营段最多列几个分组。
	// 亏损分组会另行全部点名，不受这个上限影响。
	maxGroupsInProfitSection = 6

	// maxChannelIssues 异常段最多列几条，超出的折成一行计数。
	maxChannelIssues = 10
	// maxUnmappedMultiplierChanges 未映射倍率事件最多列几条，超出的折成一行计数。
	maxUnmappedMultiplierChanges = 8
)

// dailyReportLocation 返回报表时钟。容器镜像未必带 tzdata，
// LoadLocation 失败时回退固定 +8——中国不使用夏令时，两者完全等价。
func dailyReportLocation() *time.Location {
	loc, err := time.LoadLocation(dailyReportTimezone)
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return loc
}

// dailyReportDeps 汇集简报要用到的各方数据源，都是 server 里已构造好的实例，只读不写。
type dailyReportDeps struct {
	settings    *settings.Service
	mySites     *my_sites.Service
	upstream    *upstream.Service
	metrics     *dashboard.MetricsRepository
	liveMetrics dailyMetricsReader
	connHealth  *connection_health.Repository
	events      multiplierEventStore
}

type dailyMetricsReader interface {
	LiveMetrics(ctx context.Context, userID string) (dashboard.MetricsResponse, error)
}

type multiplierEventStore interface {
	ListMultiplierEventsSince(ctx context.Context, userID, adminAccountID string, since time.Time, mappedOnly *bool) ([]upstream.MultiplierEvent, error)
	PruneMultiplierEventsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// dailyReportData 是渲染一份简报所需的全部数据。
// 取数与排版刻意分开：排版是纯函数，可以完全脱离数据库和上游接口做测试。
type dailyReportData struct {
	Now             time.Time
	Strategy        settings.StrategySettings
	Sites           []upstream.Response
	Settlement      []dashboard.DailySnapshot
	Changes         []connection_health.MultiplierChange
	TodayCosts      []my_sites.TargetAccountCost
	GroupCosts      []my_sites.TargetAccountCost
	Unresolved      []my_sites.UnresolvedTarget
	UnmappedChanges []upstream.MultiplierEvent
	CostFrom        string
	CostTo          string

	// GroupAccounting 是昨日各自有分组的营收与采购成本，用来算分组毛利。
	GroupAccounting []upstream.GroupAccounting
	// WeekAccounting 是近 7 日同口径的分组账，用于总览里的周毛利率。
	WeekAccounting []upstream.GroupAccounting
	// Yesterday 是昨天的日期字符串，分组毛利那段的口径就是它。
	Yesterday string
}

// dailyReportScheduler 每分钟检查一次是否到了某个工作区配置的推送时刻。
//
// 为什么用「分钟轮询 + 精确匹配 HH:MM」而不是算出下次触发时间再 sleep：
// 推送时刻可以随时在设置页改，sleep 到半路配置变了就得处理唤醒和重算，
// 而每分钟醒一次的开销可以忽略不计。
type dailyReportScheduler struct {
	deps dailyReportDeps

	mu            sync.Mutex
	sent          map[string]string // 工作区 -> 最近一次已推送的业务日
	lastPrunedDay string
}

func newDailyReportScheduler(deps dailyReportDeps) *dailyReportScheduler {
	return &dailyReportScheduler{deps: deps, sent: make(map[string]string)}
}

// Start 起后台协程，随 ctx 一起结束。
func (s *dailyReportScheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tickSafely(ctx)
			}
		}
	}()
}

func (s *dailyReportScheduler) tickSafely(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[daily-report] 调度 panic 已恢复: %v", recovered)
		}
	}()
	s.tick(ctx)
}

func (s *dailyReportScheduler) tick(ctx context.Context) {
	owners, err := s.deps.settings.ListStrategyOwners(ctx)
	if err != nil {
		log.Printf("[daily-report] 读取策略设置失败: %v", err)
		return
	}

	now := time.Now().In(dailyReportLocation())
	currentDay := now.Format("2006-01-02")
	currentTime := now.Format("15:04")
	if s.deps.events != nil && s.lastPrunedDay != currentDay {
		if _, err := s.deps.events.PruneMultiplierEventsBefore(ctx, now.AddDate(0, 0, -30)); err != nil {
			log.Printf("[daily-report] 清理倍率事件失败 cutoff=%s err=%v", now.AddDate(0, 0, -30).Format(time.RFC3339), err)
		} else {
			s.lastPrunedDay = currentDay
		}
	}

	for _, owner := range owners {
		if !owner.Settings.EnableDailyReport || len(owner.Settings.DailyReportBotIDs) == 0 {
			continue
		}
		// 只在配置时刻那一分钟发。错过就跳过当天，不做补发：
		// 补发意味着进程每重启一次就可能多推一条，比漏一条更烦人。
		if owner.Settings.DailyReportTime != currentTime {
			continue
		}
		if !s.markSent(owner.UserID, owner.AdminAccountID, currentDay) {
			continue
		}

		data := collectDailyReportData(ctx, s.deps, owner, now)
		report := renderDailyReport(data)
		// 正文一并落日志：简报一天只发一条，多这几行不占地方，
		// 但事后追查「那天的数字为什么不对」时，有没有原文差别很大。
		log.Printf("[daily-report] 推送简报 user_id=%s admin_account_id=%s date=%s 站点=%d 今日成本条数=%d 周成本条数=%d\n%s",
			owner.UserID, owner.AdminAccountID, currentDay,
			len(data.Sites), len(data.TodayCosts), len(data.GroupCosts), report)
		s.deps.settings.SendFormattedToBotsForWorkspace(ctx, owner.UserID, owner.AdminAccountID,
			owner.Settings.DailyReportBotIDs, report, owner.Settings.DailyReportFormat)
	}
}

// RegisterRoutes 挂上手动触发接口。
//
// 它不属于任何一个业务模块：一份运营日报要同时用到 settings / my_sites /
// upstream / dashboard / connection_health 五处数据，放进其中任何一个模块都会
// 让那个模块反向依赖其余四个。
func (s *dailyReportScheduler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/daily-report/send-now", s.handleSendNow)
	mux.HandleFunc("GET /api/daily-report/preview", s.handlePreview)
}

// handleSendNow 立即生成一份运营日报并推给配置好的机器人。
//
// 刻意不复用定时推送的「今天已发过」去重：手动触发就是要「现在再给我一份」，
// 被去重挡掉才是意外。也不更新那个去重标记——手动发一次不该顶掉当天的定时推送。
func (s *dailyReportScheduler) handleSendNow(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	owner, err := s.deps.settings.CurrentStrategyOwner(r.Context(), userID)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "admin.settings.errors.request")
		return
	}
	if len(owner.Settings.DailyReportBotIDs) == 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "admin.settings.errors.dailyReportNoBots")
		return
	}

	report := renderDailyReport(collectDailyReportData(r.Context(), s.deps, owner, time.Now().In(dailyReportLocation())))
	result, err := s.deps.settings.SendFormattedToBotsForWorkspaceWithResult(r.Context(), owner.UserID, owner.AdminAccountID,
		owner.Settings.DailyReportBotIDs, report, owner.Settings.DailyReportFormat)
	if err != nil {
		log.Printf("[daily-report] 手动发送加载渠道失败 user_id=%s admin_account_id=%s err=%v", owner.UserID, owner.AdminAccountID, err)
		httpjson.WriteError(w, http.StatusBadGateway, "admin.settings.errors.dailyReportSendFailed")
		return
	}
	if result.Matched < result.Requested {
		log.Printf("[daily-report] 手动发送未匹配机器人 user_id=%s admin_account_id=%s requested=%d", owner.UserID, owner.AdminAccountID, result.Requested)
		httpjson.WriteError(w, http.StatusBadRequest, "admin.settings.errors.dailyReportBotsNotSaved")
		return
	}
	if result.Failed > 0 {
		log.Printf("[daily-report] 手动发送存在投递失败 user_id=%s admin_account_id=%s requested=%d matched=%d delivered=%d failed=%d", owner.UserID, owner.AdminAccountID, result.Requested, result.Matched, result.Delivered, result.Failed)
		httpjson.WriteError(w, http.StatusBadGateway, "admin.settings.errors.dailyReportSendFailed")
		return
	}

	httpjson.Write(w, http.StatusOK, map[string]any{
		"sent":     true,
		"botCount": result.Delivered,
		"report":   report,
	})
}

// handlePreview 只生成不发送，用来在页面上先看一眼内容对不对。
func (s *dailyReportScheduler) handlePreview(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	owner, err := s.deps.settings.CurrentStrategyOwner(r.Context(), userID)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "admin.settings.errors.request")
		return
	}
	httpjson.Write(w, http.StatusOK, map[string]any{
		"report": renderDailyReport(collectDailyReportData(r.Context(), s.deps, owner, time.Now().In(dailyReportLocation()))),
		"format": owner.Settings.DailyReportFormat,
	})
}

// markSent 记录「今天已发过」，返回 false 表示本轮无需再发。
func (s *dailyReportScheduler) markSent(userID, adminAccountID, day string) bool {
	key := userID + "|" + adminAccountID
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent[key] == day {
		return false
	}
	s.sent[key] = day
	return true
}

// collectDailyReportData 取数。任何一段失败都只让那一段留空，
// 不影响其余内容——简报少一块总好过整条发不出去。
func collectDailyReportData(ctx context.Context, deps dailyReportDeps, owner settings.StrategyOwner, now time.Time) dailyReportData {
	data := dailyReportData{
		Now:      now,
		Strategy: owner.Settings,
		Sites:    deps.upstream.ListForAccount(ctx, owner.UserID, owner.AdminAccountID),
	}
	if liveSiteIDs, siteErr := deps.upstream.ListSiteIDsForAccount(ctx, owner.UserID, owner.AdminAccountID); siteErr != nil {
		log.Printf("[daily-report] 读取权威上游站点清单失败，跳过失效映射清理 user_id=%s err=%v", owner.UserID, siteErr)
	} else if deps.mySites != nil {
		if cleanupErr := deps.mySites.CleanupMissingUpstreamSites(ctx, owner.UserID, owner.AdminAccountID, liveSiteIDs); cleanupErr != nil {
			log.Printf("[daily-report] 自动清理失效上游映射失败 user_id=%s err=%v", owner.UserID, cleanupErr)
		}
	}

	// ListRange 只返回历史日期，当天由 LiveMetrics 实时计算后追加。
	// 取 7 天历史 + 今天，最后两条用于今日环比，整段用于近 8 日汇总。
	snapshots, err := deps.metrics.ListRange(ctx, owner.UserID, owner.AdminAccountID, 7, now.Format("2006-01-02"))
	if err != nil {
		log.Printf("[daily-report] 读取每日结算失败 user_id=%s err=%v", owner.UserID, err)
	} else {
		data.Settlement = snapshots
	}
	if deps.liveMetrics != nil {
		metrics, metricsErr := deps.liveMetrics.LiveMetrics(ctx, owner.UserID)
		if metricsErr != nil {
			log.Printf("[daily-report] 读取今日实时指标失败 user_id=%s err=%v", owner.UserID, metricsErr)
		} else {
			data.Settlement = append(data.Settlement, dashboard.DailySnapshot{
				UserID: owner.UserID, AdminAccountID: owner.AdminAccountID,
				Date: now, TodayProfitUSD: metrics.TodayProfitUSD.Amount,
				SiteBalanceUSD: metrics.SiteBalanceUSD.Amount, TodayPurchaseCNY: metrics.TodayPurchaseCNY.Amount,
				UpstreamBalanceCNY: metrics.UpstreamBalanceCNY.Amount, CostStatus: metrics.CostStatus,
				USDToCNYRate: metrics.USDToCNYRate, IsFinalized: false,
			})
		}
	}

	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	changes, err := deps.connHealth.ListMultiplierChangesBetween(ctx, owner.UserID, owner.AdminAccountID, dayStart, now)
	if err != nil {
		log.Printf("[daily-report] 读取倍率变动失败 user_id=%s err=%v", owner.UserID, err)
	} else {
		data.Changes = changes
	}
	if deps.events != nil {
		mappedOnly := false
		events, eventErr := deps.events.ListMultiplierEventsSince(ctx, owner.UserID, owner.AdminAccountID, now.Add(-24*time.Hour), &mappedOnly)
		if eventErr != nil {
			log.Printf("[daily-report] 读取未映射倍率事件失败 user_id=%s err=%v", owner.UserID, eventErr)
		} else {
			data.UnmappedChanges = events
		}
	}

	// 今日成本也走账号成本口径。site.metrics.todayConsume 是上游用户扣费，
	// 不能拿它冒充我们实际支付给上游账号的采购成本。
	today := now.Format("2006-01-02")
	data.TodayCosts = deps.mySites.TargetAccountCostRange(ctx, owner.UserID, owner.AdminAccountID, today, today).Costs

	// 日报按今天口径统计，成本和分组经营均包含截至当前时刻的实时数据。
	data.CostTo = today
	data.CostFrom = now.AddDate(0, 0, -(groupCostDays - 1)).Format("2006-01-02")
	weekly := deps.mySites.TargetAccountCostRange(ctx, owner.UserID, owner.AdminAccountID, data.CostFrom, data.CostTo)
	data.GroupCosts = weekly.Costs
	// 归集不到成本的目标取周期这次的即可：两次查的是同一批映射，
	// 差别只在日期范围，诊断结果一致。
	data.Unresolved = weekly.Unresolved

	// 分组毛利按今天截至当前时刻计算。
	data.Yesterday = data.CostTo
	data.GroupAccounting = deps.mySites.GroupAccountingRange(ctx, owner.UserID, owner.AdminAccountID, data.Yesterday, data.Yesterday)
	data.WeekAccounting = deps.mySites.GroupAccountingRange(ctx, owner.UserID, owner.AdminAccountID, data.CostFrom, data.CostTo)

	return data
}

// renderDailyReport 把数据排版成 Markdown。纯函数，不碰任何外部依赖。
//
// 组织方式是「以站点为主线」：一个站点的消费、余额、分组成本、倍率变动和
// 健康状况集中在一块里，看完一块就掌握这个站点的全部情况，
// 不用在几个横切段落之间来回找同一个名字。
func renderDailyReport(data dailyReportData) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "📊 **共飞后台运营日报**\n🗓 %s（%s）· %s\n",
		data.Now.Format("2006-01-02"), weekdayCN(data.Now), data.Now.Format("15:04"))

	writeBusinessResult(&sb, data)
	writeFundSafety(&sb, data)
	writeGroupProfit(&sb, data)
	writeChannelIssues(&sb, data)
	writeUnresolvedSection(&sb, data.Unresolved, data.Sites)
	// 站点明细放最后：前五段回答「要不要动手」，这一段回答「具体去哪个站点动手」。
	// 没有动静的站点会被自动折叠，不会把消息撑成一堵墙。
	writeSiteBlocks(&sb, data)

	return sb.String()
}

// changePercentSuffix 给倍率变动补一个涨跌幅。0.055 → 0.065 看着差一点点，
// 说成 +18.2% 才知道该不该紧张。
func changePercentSuffix(before, now float64) string {
	if before <= 0 {
		return ""
	}
	return fmt.Sprintf("（%s）", formatDelta((now-before)/before*100))
}

func weekdayCN(t time.Time) string {
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[int(t.Weekday())]
}

// snapshotRevenueCNY 把快照里的营收折成人民币。
//
// 字段名叫 TodayProfitUSD 是历史遗留：它存的是**上游平台报表的原始币种值**，
// 折算系数随行存在 USDToCNYRate 里（sub2api 平台就是 1，因为它本来就报人民币）。
// 用当前配置的美元汇率去重算会把整套账放大 7 倍——生产上真出过这个事故。
func snapshotRevenueCNY(snap dashboard.DailySnapshot) float64 {
	return snap.TodayProfitUSD * snap.EffectiveRate()
}

func snapshotBalanceCNY(snap dashboard.DailySnapshot) float64 {
	return snap.SiteBalanceUSD * snap.EffectiveRate()
}

// writeBusinessResult 第一段：今天截至当前时刻赚了多少、花了多少、剩多少。
//
// 放在最前面是因为它是唯一一个「不看就不知道公司在赚钱还是赔钱」的数字。
// 之前的简报只报成本，成本涨了看得见，但涨的成本有没有被营收覆盖看不见。
func writeBusinessResult(sb *strings.Builder, data dailyReportData) {
	sb.WriteString("\n**━━ 一、经营结果 ━━**\n")

	if len(data.Settlement) == 0 {
		sb.WriteString("暂无结算数据。\n")
		return
	}

	// 历史快照按日期升序返回；生产日报会把今天的实时快照追加到末尾。
	latest := data.Settlement[len(data.Settlement)-1]
	period := "昨日"
	if !latest.Date.IsZero() && latest.Date.In(data.Now.Location()).Format("2006-01-02") == data.Now.Format("2006-01-02") {
		period = "今日"
	}
	revenue := snapshotRevenueCNY(latest)
	cost := latest.TodayPurchaseCNY
	profit := revenue - cost

	fmt.Fprintf(sb, "💰 %s营收 ¥%.2f", period, revenue)
	if len(data.Settlement) >= 2 {
		before := data.Settlement[len(data.Settlement)-2]
		fmt.Fprintf(sb, "%s", ratioSuffix(snapshotRevenueCNY(before), revenue))
	}
	sb.WriteString("\n")

	fmt.Fprintf(sb, "💸 %s成本 ¥%.2f", period, cost)
	if len(data.Settlement) >= 2 {
		fmt.Fprintf(sb, "%s", ratioSuffix(data.Settlement[len(data.Settlement)-2].TodayPurchaseCNY, cost))
	}
	sb.WriteString("\n")

	// 亏损时用醒目的标记：这是整份报告里最需要立刻反应的一行。
	marker := "📈"
	if profit < 0 {
		marker = "🔻"
	}
	fmt.Fprintf(sb, "%s %s毛利 ¥%.2f　%s\n", marker, period, profit, marginText(revenue, profit))

	// 近 7 日汇总走同一批快照，与上面三行完全同口径。
	weekRevenue, weekCost := 0.0, 0.0
	for _, snap := range data.Settlement {
		weekRevenue += snapshotRevenueCNY(snap)
		weekCost += snap.TodayPurchaseCNY
	}
	fmt.Fprintf(sb, "📉 近 %d 日 营收 ¥%.2f ｜ 成本 ¥%.2f ｜ %s\n",
		len(data.Settlement), weekRevenue, weekCost, marginText(weekRevenue, weekRevenue-weekCost))

	if !latest.IsFinalized {
		sb.WriteString(fmt.Sprintf("_注：%s数据尚未结算完毕，数值可能还会调整。_\n", period))
	}
	if latest.CostStatus != dashboard.CostStatusAdminAccounted {
		sb.WriteString(fmt.Sprintf("_注：%s成本口径不完整，毛利仅供参考。_\n", period))
	}
}

// marginText 输出毛利率。营收为 0 时不硬算——除零的结果不是「0%」而是「没有意义」。
func marginText(revenue, profit float64) string {
	if revenue <= 0 {
		return "毛利率 —"
	}
	return fmt.Sprintf("毛利率 %.1f%%", profit/revenue*100)
}

// ratioSuffix 生成「（环比 +12.3%）」。基数为 0 时不编百分比。
func ratioSuffix(before, now float64) string {
	if before <= 0 {
		return "（前日无数据，环比略）"
	}
	return fmt.Sprintf("（环比 %s）", formatDelta((now-before)/before*100))
}

// writeFundSafety 第二段：钱还够烧多久，以及谁快没钱了。
func writeFundSafety(sb *strings.Builder, data dailyReportData) {
	sb.WriteString("\n**━━ 二、资金安全 ━━**\n")

	if len(data.Settlement) > 0 {
		latest := data.Settlement[len(data.Settlement)-1]
		fmt.Fprintf(sb, "🏦 上游余额 ¥%.2f%s\n", latest.UpstreamBalanceCNY, runwayText(latest.UpstreamBalanceCNY, data.Settlement))
		fmt.Fprintf(sb, "👥 用户余额 ¥%.2f（我方待兑付）\n", snapshotBalanceCNY(latest))
	}

	// 余额告警与「余额预警」通知复用同一套阈值判定，避免两处结论打架。
	alerts := make([]string, 0, len(data.Sites))
	for _, site := range data.Sites {
		balanceCNY, threshold, ok := balanceAgainstThreshold(site, data.Strategy)
		if !ok || balanceCNY >= threshold {
			continue
		}
		alerts = append(alerts, fmt.Sprintf("  · %s ¥%.2f（阈值 ¥%.2f）", site.Name, balanceCNY, threshold))
	}
	if len(alerts) == 0 {
		sb.WriteString("✅ 无余额告警\n")
		return
	}
	fmt.Fprintf(sb, "⚠️ 余额告警（%d 个）\n", len(alerts))
	sb.WriteString(strings.Join(alerts, "\n"))
	sb.WriteString("\n")
}

// runwayText 用近期日均成本估算上游余额还能撑几天。
// 这是个粗估：用量会变，但「还剩 3 天」和「还剩 3 个月」是完全不同的紧急程度。
func runwayText(balanceCNY float64, snapshots []dashboard.DailySnapshot) string {
	if balanceCNY <= 0 || len(snapshots) == 0 {
		return ""
	}
	total := 0.0
	for _, snap := range snapshots {
		total += snap.TodayPurchaseCNY
	}
	avg := total / float64(len(snapshots))
	if avg <= 0 {
		return ""
	}
	return fmt.Sprintf("（按近 %d 日均成本可支撑 ≈ %.1f 天）", len(snapshots), balanceCNY/avg)
}

// writeGroupProfit 第三段：哪个分组在赚钱，哪个在倒贴。
//
// 全站毛利是正的不代表每个分组都健康——一个跑量大的亏损分组完全可能被
// 其他分组的利润盖住，只看总数永远发现不了。
func writeGroupProfit(sb *strings.Builder, data dailyReportData) {
	fmt.Fprintf(sb, "\n**━━ 三、分组经营（%s）━━**\n", data.Yesterday)

	priced := make([]upstream.GroupAccounting, 0, len(data.GroupAccounting))
	for _, group := range data.GroupAccounting {
		// 一分钱没跑的分组不占版面。
		if group.RevenueAmount <= 0 && group.CostAmount <= 0 {
			continue
		}
		priced = append(priced, group)
	}
	if len(priced) == 0 {
		sb.WriteString("今日截至当前时刻没有分组产生消费。\n")
		return
	}

	// 按毛利额降序：贡献最大的排最前，亏得最多的排最后，两头都是要看的。
	sort.SliceStable(priced, func(i, j int) bool {
		return priced[i].RevenueAmount-priced[i].CostAmount > priced[j].RevenueAmount-priced[j].CostAmount
	})

	listed := priced
	if len(listed) > maxGroupsInProfitSection {
		listed = listed[:maxGroupsInProfitSection]
	}
	for index, group := range listed {
		profit := group.RevenueAmount - group.CostAmount
		if !group.CostKnown {
			// 成本口径缺失时绝不把毛利显示成等于营收——那会让分组看着全是纯利。
			fmt.Fprintf(sb, "%d. %s 营收 ¥%.2f ｜ 成本未归集\n", index+1, group.GroupName, group.RevenueAmount)
			continue
		}
		fmt.Fprintf(sb, "%d. %s 营收 ¥%.2f ｜ 成本 ¥%.2f ｜ %s\n",
			index+1, group.GroupName, group.RevenueAmount, group.CostAmount, marginText(group.RevenueAmount, profit))
	}
	if len(priced) > len(listed) {
		fmt.Fprintf(sb, "  · _另有 %d 个分组_\n", len(priced)-len(listed))
	}

	// 亏损分组单独点名，哪怕它排在列表外——这是这一段真正的价值。
	losing := make([]string, 0, len(priced))
	for _, group := range priced {
		if !group.CostKnown {
			continue
		}
		if profit := group.RevenueAmount - group.CostAmount; profit < 0 {
			losing = append(losing, fmt.Sprintf("  · %s %s（亏 ¥%.2f）",
				group.GroupName, marginText(group.RevenueAmount, profit), -profit))
		}
	}
	if len(losing) > 0 {
		fmt.Fprintf(sb, "⚠️ 亏损分组（%d 个）\n", len(losing))
		sb.WriteString(strings.Join(losing, "\n"))
		sb.WriteString("\n")
	}
}

// writeChannelIssues 第四段：今天有哪些通道出了问题。
func writeChannelIssues(sb *strings.Builder, data dailyReportData) {
	sb.WriteString("\n**━━ 四、通道异常 ━━**\n")

	lines := make([]string, 0, len(data.Sites)+len(data.Changes))
	for _, site := range data.Sites {
		if excludeSiteFromReport(site) {
			continue
		}
		if issue := siteHealthIssue(site, data.Now); issue != "" {
			lines = append(lines, fmt.Sprintf("🔴 %s：%s", site.Name, issue))
		}
	}

	siteNames := make(map[string]string, len(data.Sites))
	for _, site := range data.Sites {
		siteNames[site.ID] = site.Name
	}
	for _, change := range data.Changes {
		if site := siteByID(data.Sites, change.SiteID); site != nil && excludeSiteFromReport(*site) {
			continue
		}
		name := siteNames[change.SiteID]
		if name == "" {
			name = shortID(change.SiteID)
		}
		lines = append(lines, fmt.Sprintf("🟠 %s「%s」%sx → **%sx**%s",
			name, change.GroupName, trimFloat(change.Previous), trimFloat(change.Current),
			changePercentSuffix(change.Previous, change.Current)))
	}

	if len(lines) == 0 && len(data.UnmappedChanges) == 0 {
		sb.WriteString("✅ 今日无异常\n")
		return
	}
	if len(lines) > maxChannelIssues {
		remaining := len(lines) - maxChannelIssues
		lines = lines[:maxChannelIssues]
		lines = append(lines, fmt.Sprintf("  · _另有 %d 条_", remaining))
	}
	if len(lines) > 0 {
		sb.WriteString(strings.Join(lines, "\n"))
		sb.WriteString("\n")
	}
	writeUnmappedMultiplierChanges(sb, data.UnmappedChanges)
}

func writeUnmappedMultiplierChanges(sb *strings.Builder, events []upstream.MultiplierEvent) {
	if len(events) == 0 {
		return
	}
	type aggregate struct {
		SiteName  string
		GroupName string
		Previous  float64
		Current   float64
		Count     int
	}
	aggregates := make(map[string]*aggregate, len(events))
	order := make([]string, 0, len(events))
	for _, event := range events {
		key := event.SiteID + "|" + event.GroupID + "|" + event.GroupName
		item, ok := aggregates[key]
		if !ok {
			item = &aggregate{SiteName: event.SiteName, GroupName: event.GroupName, Previous: event.PreviousMultiplier}
			aggregates[key] = item
			order = append(order, key)
		}
		item.Current = event.CurrentMultiplier
		item.Count++
	}

	lines := make([]string, 0, len(order))
	for _, key := range order {
		item := aggregates[key]
		name := item.SiteName
		if name == "" {
			name = "未知站点"
		}
		extra := changePercentSuffix(item.Previous, item.Current)
		if item.Count > 1 {
			if extra == "" {
				extra = fmt.Sprintf("（期间变动 %d 次）", item.Count)
			} else {
				extra = strings.TrimSuffix(extra, "）") + fmt.Sprintf("，期间变动 %d 次）", item.Count)
			}
		}
		lines = append(lines, fmt.Sprintf("  · %s「%s」%sx → **%sx**%s",
			name, item.GroupName, trimFloat(item.Previous), trimFloat(item.Current), extra))
	}
	if len(lines) > maxUnmappedMultiplierChanges {
		remaining := len(lines) - maxUnmappedMultiplierChanges
		lines = lines[:maxUnmappedMultiplierChanges]
		lines = append(lines, fmt.Sprintf("  · _另有 %d 条未映射变动_", remaining))
	}
	sb.WriteString("\n**🔕 未对接分组倍率变动（近 24 小时，未即时通知）**\n")
	sb.WriteString(strings.Join(lines, "\n"))
	sb.WriteString("\n")
}

// writeUnresolvedSection 单独列出算不出采购成本的上游。
//
// 这些上游的消费不在上面的成本统计里。与其硬凑一个账号把成本算错，
// 不如把缺口摆出来——总额偏低是可以接受的，偏低而不知道少了谁不行。
func writeUnresolvedSection(sb *strings.Builder, unresolved []my_sites.UnresolvedTarget, sites []upstream.Response) {
	if len(unresolved) == 0 {
		return
	}

	siteNames := make(map[string]string, len(sites))
	for _, site := range sites {
		siteNames[site.ID] = site.Name
	}

	// 按原因归拢，同类问题的处理方式相同，放一起看更省事。
	byReason := make(map[my_sites.UnresolvedReason][]string)
	order := make([]my_sites.UnresolvedReason, 0, 5)
	for _, item := range unresolved {
		name, known := siteNames[item.SiteID]
		reason := item.Reason
		if !known {
			// 取数层只看映射，不知道站点还在不在。指向已删除站点的映射要去清理，
			// 而不是补绑账号——混在「未绑定」里会让人白跑一趟。
			reason = reasonSiteGone
			name = shortID(item.SiteID)
		}
		if _, seen := byReason[reason]; !seen {
			order = append(order, reason)
		}
		byReason[reason] = append(byReason[reason],
			fmt.Sprintf("  · 我方分组「%s」← 上游 %s 的「%s」分组", item.OwnGroup, name, item.GroupName))
	}

	fmt.Fprintf(sb, "\n**━━ 五、未归集成本的上游（%d 个）━━**\n", len(unresolved))
	sb.WriteString("_下列上游的消费未计入上面的成本统计。这里列的是上游分组与我方分组的映射关系，不是 Sub2API 账号名。_\n")
	for _, reason := range order {
		lines := byReason[reason]
		fmt.Fprintf(sb, "**%s**（%d）\n", describeUnresolvedReason(reason), len(lines))
		listed := lines
		if len(listed) > maxUnresolvedPerReason {
			listed = listed[:maxUnresolvedPerReason]
		}
		sb.WriteString(strings.Join(listed, "\n"))
		sb.WriteString("\n")
		if len(lines) > maxUnresolvedPerReason {
			fmt.Fprintf(sb, "  · _另有 %d 个同类_\n", len(lines)-maxUnresolvedPerReason)
		}
	}
}

// reasonSiteGone 是渲染层追加的分类：映射里的站点已经从上游列表中删除。
// 取数层拿不到站点列表，判断不了这一点，所以放在这里补。
const reasonSiteGone my_sites.UnresolvedReason = "site_gone"

func describeUnresolvedReason(reason my_sites.UnresolvedReason) string {
	switch reason {
	case reasonSiteGone:
		return "映射指向的站点已删除（建议清理该调价数据源）"
	case my_sites.ReasonUnbound:
		return "未绑成本账号（去 分组管理 → 调价映射 → 该分组的「调价数据源」补绑）"
	case my_sites.ReasonGroupMissing:
		return "自有分组在 Sub2API 上不存在"
	case my_sites.ReasonAmbiguous:
		return "账号绑定归属冲突（同一账号被多个上游共用）"
	case my_sites.ReasonQueryFailed:
		return "成本查询失败"
	default:
		return string(reason)
	}
}

// shortID 截短 UUID，简报里只需要够定位即可。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// writeSiteBlocks 逐站点成块输出，按今日人民币实付降序。
// 完全没有动静的站点会被折叠成一行，免得十几个零消费站点把简报撑满。
func writeSiteBlocks(sb *strings.Builder, data dailyReportData) {
	fmt.Fprintf(sb, "\n**━━ 六、站点明细（近 %d 天）━━**\n", groupCostDays)
	reportableSites := make([]upstream.Response, 0, len(data.Sites))
	for _, site := range data.Sites {
		if !excludeSiteFromReport(site) {
			reportableSites = append(reportableSites, site)
		}
	}
	if len(reportableSites) == 0 {
		sb.WriteString("\n暂无上游站点。\n")
		return
	}

	costsBySite := groupCostsBySite(data.GroupCosts)
	todayCostsBySite := siteCostTotalsBySite(data.TodayCosts)
	changesBySite := multiplierChangesBySite(data.Changes)
	reportableSiteIDs := make(map[string]struct{}, len(reportableSites))
	for _, site := range reportableSites {
		reportableSiteIDs[site.ID] = struct{}{}
	}
	totalCost := 0.0
	for _, cost := range data.GroupCosts {
		if _, ok := reportableSiteIDs[cost.SiteID]; ok {
			totalCost += cost.CostCNY
		}
	}

	sorted := make([]upstream.Response, len(reportableSites))
	copy(sorted, reportableSites)
	// 按今日账号采购成本降序：最花钱的排最前。
	// 不能改用 site.Metrics.TodayConsume 排序——那是上游对用户的扣费，
	// 排出来的顺序反映的是「谁的用户花得多」，不是「我们在谁身上花得多」。
	sort.SliceStable(sorted, func(i, j int) bool {
		return todayCostsBySite[sorted[i].ID].CostCNY > todayCostsBySite[sorted[j].ID].CostCNY
	})

	quiet := make([]string, 0)
	for _, site := range sorted {
		costs := costsBySite[site.ID]
		changes := changesBySite[site.ID]
		if isQuietSiteForReport(site, todayCostsBySite[site.ID], costs, changes, data.Strategy, data.Now) {
			quiet = append(quiet, site.Name)
			continue
		}
		writeSiteBlock(sb, site, todayCostsBySite[site.ID], costs, changes, data.Strategy, data.Now, totalCost)
	}

	if len(quiet) > 0 {
		fmt.Fprintf(sb, "\n**━━ 其余 %d 个站点无活动 ━━**\n%s\n",
			len(quiet), strings.Join(quiet, "、"))
	}
}

// isQuietSiteForReport 判断一个站点是否完全没动静：没成本、没倍率变动、
// 状态正常且同步没停摆。这类站点只报个名字就够了。
//
// 判定用的是账号采购成本，绝不能换成 site.Metrics.TodayConsume——
// 那是上游对用户的扣费，两者可以差一个数量级。
func isQuietSiteForReport(site upstream.Response, todayCost my_sites.TargetAccountCost, costs []my_sites.TargetAccountCost, changes []connection_health.MultiplierChange, strategy settings.StrategySettings, now time.Time) bool {
	if todayCost.CostCNY > 0 || len(costs) > 0 || len(changes) > 0 {
		return false
	}
	if siteHealthIssue(site, now) != "" {
		return false
	}
	if balanceCNY, threshold, ok := balanceAgainstThreshold(site, strategy); ok && balanceCNY < threshold {
		return false
	}
	return true
}

// writeSiteBlock 输出单个站点的完整情况。
func writeSiteBlock(sb *strings.Builder, site upstream.Response, todayCost my_sites.TargetAccountCost, costs []my_sites.TargetAccountCost, changes []connection_health.MultiplierChange, strategy settings.StrategySettings, now time.Time, totalCost float64) {
	rate := effectiveRechargeRate(site)

	fmt.Fprintf(sb, "\n**━━ %s ━━**\n", site.Name)
	fmt.Fprintf(sb, "今日 %s ｜ 余额 %s",
		siteCostText(todayCost),
		amountText(site.Metrics.Balance, rate))

	if balanceCNY, threshold, ok := balanceAgainstThreshold(site, strategy); ok {
		if balanceCNY < threshold {
			fmt.Fprintf(sb, " ⚠️ 已跌破预警线 ¥%.2f", threshold)
		} else {
			fmt.Fprintf(sb, "（距预警线 ¥%.2f）", balanceCNY-threshold)
		}
	}
	sb.WriteString("\n")

	writeSiteCosts(sb, costs, totalCost)
	writeSiteChanges(sb, changes)

	if issue := siteHealthIssue(site, now); issue != "" {
		fmt.Fprintf(sb, "⚠️ %s\n", issue)
	}
}

// writeSiteCosts 该站点近 N 天各分组的成本，降序。
func writeSiteCosts(sb *strings.Builder, costs []my_sites.TargetAccountCost, totalCost float64) {
	if len(costs) == 0 {
		return
	}

	sorted := make([]my_sites.TargetAccountCost, len(costs))
	copy(sorted, costs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CostCNY > sorted[j].CostCNY })

	siteTotal := 0.0
	for _, cost := range sorted {
		siteTotal += cost.CostCNY
	}

	share := ""
	if totalCost > 0 {
		share = fmt.Sprintf("，占全部成本 %.1f%%", siteTotal/totalCost*100)
	}
	fmt.Fprintf(sb, "近 %d 天成本 ¥%.2f%s\n", groupCostDays, siteTotal, share)

	listed := sorted
	if len(listed) > maxGroupsPerSite {
		listed = listed[:maxGroupsPerSite]
	}
	for _, cost := range listed {
		fmt.Fprintf(sb, "  · %s ¥%.2f\n", cost.GroupName, cost.CostCNY)
	}
	if len(sorted) > maxGroupsPerSite {
		fmt.Fprintf(sb, "  · _其余 %d 个分组合计 ¥%.2f_\n",
			len(sorted)-maxGroupsPerSite, siteTotal-sumCosts(listed))
	}
}

func sumCosts(costs []my_sites.TargetAccountCost) float64 {
	total := 0.0
	for _, cost := range costs {
		total += cost.CostCNY
	}
	return total
}

// writeSiteChanges 该站点今天发生的倍率变动。
func writeSiteChanges(sb *strings.Builder, changes []connection_health.MultiplierChange) {
	if len(changes) == 0 {
		return
	}
	fmt.Fprintf(sb, "今日倍率变动 %d 次\n", len(changes))
	for _, change := range changes {
		fmt.Fprintf(sb, "  · %s %.4gx → **%.4gx**\n", change.GroupName, change.Previous, change.Current)
	}
}

// siteHealthIssue 返回该站点的健康问题描述，没问题时返回空串。
// 针对的是最危险的故障模式：会话过期后同步静默停摆，
// 余额预警和自动调价跟着一起失效，而表面上什么动静都没有。
func siteHealthIssue(site upstream.Response, now time.Time) string {
	if site.Status == upstream.StatusError {
		return describeSiteError(site.ErrorKey) + lastSyncedSuffix(site, now)
	}
	if site.LastSyncedAt == nil {
		return "从未成功同步"
	}
	lastSynced := time.UnixMilli(*site.LastSyncedAt).In(now.Location())
	if now.Sub(lastSynced) > staleSyncThreshold {
		return fmt.Sprintf("已 %s未同步（最后一次 %s）",
			humanizeDuration(now.Sub(lastSynced)), lastSynced.Format("01-02 15:04"))
	}
	return ""
}

// excludeSiteFromReport omits sites that have never completed authentication.
// A previously healthy site with a stale sync remains reportable and is handled
// by siteHealthIssue instead.
func excludeSiteFromReport(site upstream.Response) bool {
	return site.Status == upstream.StatusError && site.LastSyncedAt == nil
}

func siteByID(sites []upstream.Response, id string) *upstream.Response {
	for index := range sites {
		if sites[index].ID == id {
			return &sites[index]
		}
	}
	return nil
}

func groupCostsBySite(costs []my_sites.TargetAccountCost) map[string][]my_sites.TargetAccountCost {
	grouped := make(map[string][]my_sites.TargetAccountCost)
	for _, cost := range costs {
		grouped[cost.SiteID] = append(grouped[cost.SiteID], cost)
	}
	return grouped
}

func siteCostTotalsBySite(costs []my_sites.TargetAccountCost) map[string]my_sites.TargetAccountCost {
	totals := make(map[string]my_sites.TargetAccountCost)
	for _, cost := range costs {
		total := totals[cost.SiteID]
		total.SiteID = cost.SiteID
		total.CostCNY += cost.CostCNY
		totals[cost.SiteID] = total
	}
	return totals
}

func siteCostText(cost my_sites.TargetAccountCost) string {
	if cost.SiteID == "" {
		return "—"
	}
	return fmt.Sprintf("¥%.2f", cost.CostCNY)
}

func multiplierChangesBySite(changes []connection_health.MultiplierChange) map[string][]connection_health.MultiplierChange {
	grouped := make(map[string][]connection_health.MultiplierChange)
	for _, change := range changes {
		grouped[change.SiteID] = append(grouped[change.SiteID], change)
	}
	return grouped
}

// balanceAgainstThreshold 复用余额预警的换算口径：USD 余额乘充值倍率折成 CNY，
// 站点级阈值优先于全局默认。两处算法必须一致，否则会出现简报说「安全」
// 而余额预警已经在报的矛盾。
func balanceAgainstThreshold(site upstream.Response, strategy settings.StrategySettings) (balanceCNY, threshold float64, ok bool) {
	if site.Metrics.Balance.Value == nil {
		return 0, 0, false
	}
	balanceCNY = *site.Metrics.Balance.Value * effectiveRechargeRate(site)

	threshold = strategy.DefaultBalanceThreshold
	if site.Settings.BalanceThreshold != nil {
		threshold = *site.Settings.BalanceThreshold
	}
	if threshold <= 0 {
		return 0, 0, false
	}
	return balanceCNY, threshold, true
}

// effectiveRechargeRate 取站点的充值倍率，缺失或非法时按 1 处理。
// 它不是汇率，而是「1 USD 额度实际花了多少人民币」，由充值时的实付金额决定，
// 各站点差异极大——把它当成统一汇率会把余额算错一个数量级。
func effectiveRechargeRate(site upstream.Response) float64 {
	if site.RechargeRate <= 0 {
		return 1
	}
	return site.RechargeRate
}

// amountText 与上游管理页面同口径：人民币实付在前，USD 额度括号里跟着。
func amountText(value upstream.MetricValue, rechargeRate float64) string {
	if value.Value == nil {
		return "—"
	}
	// Display 是后端已按 4 位小数格式化好的 USD 数值（不带币种符号），
	// 直接复用可以保证简报和页面上的小字完全一致。
	usd := strings.TrimSpace(value.Display)
	if usd == "" || usd == "-" {
		usd = fmt.Sprintf("%.4f", *value.Value)
	}
	return fmt.Sprintf("¥%.2f（%s USD）", *value.Value*rechargeRate, usd)
}

// consumeCNY 今日人民币实付，取不到值时返回 -1 让它排在最后。
func consumeCNY(site upstream.Response) float64 {
	if site.Metrics.TodayConsume.Value == nil {
		return -1
	}
	return *site.Metrics.TodayConsume.Value * effectiveRechargeRate(site)
}

// describeSiteError 把 i18n key 翻成简报里能直接读的中文。
// 简报是发到聊天软件的，直接甩 admin.upstream.errors.auth 这种键名没人看得懂；
// 未收录的键原样保留，至少还能拿去搜。
func describeSiteError(errorKey *string) string {
	if errorKey == nil || strings.TrimSpace(*errorKey) == "" {
		return "同步异常"
	}
	switch strings.TrimSpace(*errorKey) {
	case upstream.ErrorAuth:
		return "认证失败（会话可能已过期，需重新登录该站点）"
	case upstream.ErrorAdminRequired:
		return "缺少管理员权限"
	case upstream.ErrorInteractiveLoginRequired:
		return "需要手动登录"
	case upstream.ErrorNotFound:
		return "站点或接口不存在"
	case upstream.ErrorInvalidURL:
		return "站点地址无效"
	case upstream.ErrorRequest:
		return "请求上游失败（网络或对方接口异常）"
	default:
		return *errorKey
	}
}

func lastSyncedSuffix(site upstream.Response, now time.Time) string {
	if site.LastSyncedAt == nil {
		return "，从未成功同步"
	}
	lastSynced := time.UnixMilli(*site.LastSyncedAt).In(now.Location())
	return fmt.Sprintf("，最后一次同步 %s", lastSynced.Format("01-02 15:04"))
}

func formatDelta(delta float64) string {
	switch {
	case delta > 0:
		return fmt.Sprintf("↑ +%.1f%%", delta)
	case delta < 0:
		return fmt.Sprintf("↓ %.1f%%", delta)
	default:
		return "持平"
	}
}

func humanizeDuration(d time.Duration) string {
	if d >= 24*time.Hour {
		return fmt.Sprintf("%d 天", int(d.Hours()/24))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	}
	return fmt.Sprintf("%d 分钟", int(d.Minutes()))
}
