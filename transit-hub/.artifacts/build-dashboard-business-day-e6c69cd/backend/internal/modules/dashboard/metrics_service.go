package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"transithub/backend/internal/modules/upstream"
)

// UpstreamLister 抽象上游站点列表读取，由 upstream.Service 实现。
// 仪表盘只需要读取已同步的站点数据，不需要修改或触发同步。
// List 用于用户请求路径（自动使用当前工作区），
// ListForAccount 用于后台调度等需要显式指定工作区的内部流程。
type UpstreamLister interface {
	List(ctx context.Context, userID string) []upstream.Response
	ListForAccount(ctx context.Context, userID, adminAccountID string) []upstream.Response
	// KeyUsageToday 和 BalanceBreakdown 是「今日成本」「上游总余额」下钻弹窗的数据源，
	// 由 upstream.Service 实现（持有 session/cache，能校验站点归属和当前工作区）。
	KeyUsageToday(ctx context.Context, userID string) ([]upstream.KeyUsageTodayItem, error)
	KeyUsageTodayForAccount(ctx context.Context, userID, adminAccountID string) ([]upstream.KeyUsageTodayItem, error)
	KeyUsageForAccountDate(ctx context.Context, userID, adminAccountID, date string) ([]upstream.KeyUsageTodayItem, error)
	BalanceBreakdown(ctx context.Context, userID string) ([]upstream.BalanceBreakdownItem, error)
}

// MetricsService 负责仪表盘指标的实时计算、历史快照存储与午夜调度。
// 与同包的 Service（admin 会话管理）职责分离，共享 SessionStore 和 PlatformClient。
type MetricsService struct {
	store       SessionStore
	platform    PlatformClient
	upstreams   UpstreamLister
	metricsRepo *MetricsRepository
	accounts    AdminAccountService
	sessionSync MySiteStateSync
}

func (s *MetricsService) SetMySiteSync(sync MySiteStateSync) {
	s.sessionSync = sync
}

func (s *MetricsService) freshAdminSession(ctx context.Context, userID string, adminAccountID string, record *AdminSession) (upstream.Session, error) {
	if s.sessionSync != nil {
		stored, exists, err := s.sessionSync.StoredSession(ctx, userID, adminAccountID)
		if err != nil {
			return upstream.Session{}, err
		}
		if !exists || sessionAppearsNewer(record.Session, stored) {
			if err := s.sessionSync.SyncAdminSession(ctx, userID, adminAccountID, record.Session, record.Identity); err != nil {
				return upstream.Session{}, err
			}
		}
		canonical, err := s.sessionSync.RequireSession(ctx, userID, adminAccountID)
		if err != nil {
			return upstream.Session{}, err
		}
		if !sessionEqual(canonical, record.Session) {
			record.Session = canonical
			record.LastRefreshedAt = nowMillis()
			if err := s.store.Save(ctx, userID, adminAccountID, *record); err != nil {
				return upstream.Session{}, err
			}
		}
		return canonical, nil
	}

	refreshed, err := s.platform.RefreshSession(record.Session)
	if err != nil {
		return upstream.Session{}, err
	}
	if !sessionEqual(refreshed, record.Session) {
		record.Session = refreshed
		record.LastRefreshedAt = nowMillis()
		if err := s.store.Save(ctx, userID, adminAccountID, *record); err != nil {
			return upstream.Session{}, err
		}
	}
	return refreshed, nil
}

func NewMetricsService(store SessionStore, platform PlatformClient, upstreams UpstreamLister, metricsRepo *MetricsRepository, accounts AdminAccountService) *MetricsService {
	return &MetricsService{store: store, platform: platform, upstreams: upstreams, metricsRepo: metricsRepo, accounts: accounts}
}

// LiveMetrics calculates and returns the dashboard's current core metrics.
// It also upserts today's snapshot so trend data can keep accumulating.
//
// Calculation sources:
//   - todayProfit: admin site's total actual usage for today.
//   - siteBalance: filtered sum of admin-site user balances.
//   - todayPurchase: per-key upstream usage sum, shared with the cost drill-down.
//   - upstreamBalance: cached upstream balance sum multiplied by site recharge rates.
//   - netProfit: todayProfit - todayPurchase.
func sumKeyUsageToday(items []upstream.KeyUsageTodayItem) float64 {
	var total float64
	for _, item := range items {
		if item.TodayAmount > 0 {
			total += item.TodayAmount
		}
	}
	return total
}

func (s *MetricsService) purchaseForAccountDate(ctx context.Context, userID, adminAccountID, date string, allowCachedFallback bool) (float64, error) {
	items, err := s.upstreams.KeyUsageForAccountDate(ctx, userID, adminAccountID, date)
	total := sumKeyUsageToday(items)
	if err == nil {
		return total, nil
	}

	var collectionErr *upstream.KeyUsageCollectionError
	if errors.As(err, &collectionErr) && collectionErr.FailedSites < collectionErr.TotalSites {
		return total, err
	}

	if !allowCachedFallback {
		return total, err
	}
	log.Printf("dashboard metrics: current-day key usage unavailable user_id=%s admin_account_id=%s err=%v, using cached current metrics", userID, adminAccountID, err)
	var cachedTotal float64
	for _, site := range s.upstreams.ListForAccount(ctx, userID, adminAccountID) {
		if site.RechargeRate <= 0 || site.Metrics.TodayConsume.Value == nil {
			continue
		}
		cachedTotal += *site.Metrics.TodayConsume.Value * site.RechargeRate
	}
	return cachedTotal, nil
}

func (s *MetricsService) todayPurchaseForAccount(ctx context.Context, userID, adminAccountID string) float64 {
	total, _ := s.purchaseForAccountDate(ctx, userID, adminAccountID, dashboardDate(time.Now()), true)
	return total
}

func (s *MetricsService) LiveMetrics(ctx context.Context, userID string) (MetricsResponse, error) {
	// 获取并校验 admin 会话（平台感知：sub2api 检查 AccessToken，new-api 检查 Cookie+UserID）。
	adminAccountID, err := s.requireCurrentAdminAccount(ctx, userID)
	if err != nil {
		return MetricsResponse{}, err
	}
	record, err := s.store.Get(ctx, userID, adminAccountID)
	if err != nil {
		return MetricsResponse{}, err
	}
	if record == nil || !record.Session.IsAuthenticated() {
		return MetricsResponse{}, requestError(ErrorAdminOnly)
	}

	// 如有必要先刷新令牌（new-api 不使用 refresh token，RefreshSession 会直接返回原会话）。
	session, err := s.freshAdminSession(ctx, userID, adminAccountID, record)
	if err != nil {
		return MetricsResponse{}, requestError(ErrorAdminOnly)
	}

	// 校验 admin 角色（平台中性）。
	if err := s.platform.VerifyAdmin(session); err != nil {
		return MetricsResponse{}, requestError(ErrorAdminOnly)
	}

	// 并行获取四项独立数据：今日盈利、站点余额、分组数量、上游指标。
	// 各 goroutine 出错只记日志、降级为零值，不阻塞整体返回。
	today := dashboardDate(time.Now())
	var (
		todayProfit     float64
		siteBalance     float64
		groupCount      int
		todayPurchase   float64
		upstreamBalance float64
		wg              sync.WaitGroup
	)

	// goroutine 1: 今日盈利额度（平台中性）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		profit, err := s.platform.FetchAdminUsageStats(session, today, today)
		if err != nil {
			log.Printf("dashboard metrics: fetch usage stats failed user_id=%s err=%v", userID, err)
			return
		}
		todayProfit = profit
	}()

	// goroutine 2: 站点用户总余额（平台中性）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		filterConfig, err := s.metricsRepo.GetBalanceFilter(ctx, userID, adminAccountID)
		if err != nil {
			log.Printf("dashboard metrics: load balance filter failed user_id=%s err=%v, using defaults", userID, err)
			filterConfig = BalanceFilterConfig{ExcludeAdmin: true, ExcludeBalances: []float64{}}
		}
		balanceResult, err := s.platform.FetchAdminSiteBalanceFiltered(session, upstream.BalanceFilter{
			ExcludeAdmin:    filterConfig.ExcludeAdmin,
			ExcludeBalances: filterConfig.ExcludeBalances,
		})
		if err != nil {
			log.Printf("dashboard metrics: fetch site balance failed user_id=%s err=%v", userID, err)
			return
		}
		siteBalance = balanceResult.Balance
	}()

	// goroutine 3: 管理员站点分组数量（平台中性）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		groups, err := s.platform.FetchAdminAllGroups(session)
		if err != nil {
			log.Printf("dashboard metrics: fetch admin groups failed user_id=%s err=%v", userID, err)
			return
		}
		groupCount = len(groups)
	}()

	// goroutine 4: 今日进货额度与上游总余额（读取 Redis 缓存，无外部 API 调用）。
	// 使用 List（用户请求路径，自动过滤当前工作区站点）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		todayPurchase = s.todayPurchaseForAccount(ctx, userID, adminAccountID)
		for _, site := range s.upstreams.ListForAccount(ctx, userID, adminAccountID) {
			if site.RechargeRate <= 0 {
				continue
			}
			if site.Metrics.Balance.Value != nil {
				upstreamBalance += *site.Metrics.Balance.Value * site.RechargeRate
			}
		}
	}()

	wg.Wait()

	netProfit := todayProfit - todayPurchase

	result := MetricsResponse{
		TodayProfit:     todayProfit,
		SiteBalance:     siteBalance,
		TodayPurchase:   todayPurchase,
		NetProfit:       netProfit,
		UpstreamBalance: upstreamBalance,
		GroupCount:      groupCount,
	}

	// 将当天指标 upsert 到数据库，即使部分指标获取失败也保存已有数据，
	// 后续调用会用更完整的数据覆盖。
	if err := s.upsertSnapshot(ctx, userID, adminAccountID, today, result, false); err != nil {
		log.Printf("dashboard metrics: save current draft failed user_id=%s date=%s err=%v", userID, today, err)
	}

	return result, nil
}

// Trends 查询历史趋势数据，返回最近 days 天的每日快照（不含当天）。
// 当天的数据由前端通过 LiveMetrics 获取后追加到序列末尾。
func (s *MetricsService) Trends(ctx context.Context, userID string, days int) (TrendResponse, error) {
	if days != 7 && days != 30 {
		days = 7
	}
	// 按当前工作区过滤趋势数据。
	adminAccountID, err := s.requireCurrentAdminAccount(ctx, userID)
	if err != nil {
		return TrendResponse{}, err
	}
	snapshots, err := s.metricsRepo.ListRange(ctx, userID, adminAccountID, days, dashboardDate(time.Now()))
	if err != nil {
		return TrendResponse{}, err
	}
	points := make([]TrendPoint, 0, len(snapshots))
	for _, snap := range snapshots {
		points = append(points, TrendPoint{
			Date:            snap.Date.Format("2006-01-02"),
			TodayProfit:     snap.TodayProfit,
			SiteBalance:     snap.SiteBalance,
			TodayPurchase:   snap.TodayPurchase,
			NetProfit:       snap.NetProfit,
			UpstreamBalance: snap.UpstreamBalance,
		})
	}
	return TrendResponse{Points: points}, nil
}

// StartScheduler 启动午夜快照调度协程。
// 每天午夜（Asia/Shanghai 时区）为所有活跃 admin 用户保存当天的指标快照，
// 确保即使用户当天未访问仪表盘，趋势图也不会出现空缺。
func (s *MetricsService) StartScheduler(ctx context.Context) {
	go func() {
		s.reconcileSnapshots(ctx, time.Now())
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.reconcileSnapshots(ctx, now)
			}
		}
	}()
}

// reconcileSnapshots retries a bounded business-day history. Historical rows
// are finalized only when same-day revenue and upstream cost are both present.
func (s *MetricsService) reconcileSnapshots(ctx context.Context, now time.Time) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("dashboard scheduler panic recovered: %v", recovered)
		}
	}()
	refs, err := s.store.ActiveSessions(ctx)
	if err != nil {
		log.Printf("dashboard scheduler: list active users failed: %v", err)
		return
	}
	for _, ref := range refs {
		for offset := 1; offset <= 3; offset++ {
			date := dashboardBusinessDay(now).AddDate(0, 0, -offset).Format("2006-01-02")
			finalized, err := s.metricsRepo.IsFinalized(ctx, ref.UserID, ref.AdminAccountID, date)
			if err != nil {
				log.Printf("dashboard scheduler: read snapshot state failed user_id=%s admin_account_id=%s date=%s err=%v", ref.UserID, ref.AdminAccountID, date, err)
				continue
			}
			if finalized {
				continue
			}
			if err := s.finalizeSnapshot(ctx, ref.UserID, ref.AdminAccountID, date); err != nil {
				log.Printf("dashboard scheduler: reconciliation deferred user_id=%s admin_account_id=%s date=%s err=%v", ref.UserID, ref.AdminAccountID, date, err)
			}
		}
	}
}

func (s *MetricsService) finalizeSnapshot(ctx context.Context, userID, adminAccountID, date string) error {
	record, err := s.store.Get(ctx, userID, adminAccountID)
	if err != nil {
		return err
	}
	if record == nil || !record.Session.IsAuthenticated() {
		return errors.New("admin session unavailable")
	}
	session, err := s.freshAdminSession(ctx, userID, adminAccountID, record)
	if err != nil {
		return err
	}
	profit, err := s.platform.FetchAdminUsageStats(session, date, date)
	if err != nil {
		return err
	}
	purchase, err := s.purchaseForAccountDate(ctx, userID, adminAccountID, date, false)
	if err != nil {
		return err
	}
	filterCfg, err := s.metricsRepo.GetBalanceFilter(ctx, userID, adminAccountID)
	if err != nil {
		return err
	}
	// Balances are point-in-time values. Delayed reconciliation preserves
	// accurate usage and profit while recording the currently observed balance.
	balance, err := s.platform.FetchAdminSiteBalanceFiltered(session, upstream.BalanceFilter{ExcludeAdmin: filterCfg.ExcludeAdmin, ExcludeBalances: filterCfg.ExcludeBalances})
	if err != nil {
		return err
	}
	var upstreamBalance float64
	for _, site := range s.upstreams.ListForAccount(ctx, userID, adminAccountID) {
		if site.RechargeRate > 0 && site.Metrics.Balance.Value != nil {
			upstreamBalance += *site.Metrics.Balance.Value * site.RechargeRate
		}
	}
	return s.upsertSnapshot(ctx, userID, adminAccountID, date, MetricsResponse{TodayProfit: profit, SiteBalance: balance.Balance, TodayPurchase: purchase, NetProfit: profit - purchase, UpstreamBalance: upstreamBalance}, true)
}

func (s *MetricsService) upsertSnapshot(ctx context.Context, userID, adminAccountID, date string, metrics MetricsResponse, finalized bool) error {
	parsedDate, err := dashboardDateValue(date)
	if err != nil {
		return err
	}
	id, err := metricsRandomID()
	if err != nil {
		return err
	}
	snapshot := DailySnapshot{ID: id, UserID: userID, AdminAccountID: adminAccountID, Date: parsedDate, TodayProfit: metrics.TodayProfit, SiteBalance: metrics.SiteBalance, TodayPurchase: metrics.TodayPurchase, NetProfit: metrics.NetProfit, UpstreamBalance: metrics.UpstreamBalance, CreatedAt: time.Now(), IsFinalized: finalized}
	if finalized {
		finalizedAt := time.Now()
		snapshot.FinalizedAt = &finalizedAt
	}
	return s.metricsRepo.Upsert(ctx, snapshot)
}

// AdminGroups 获取管理员站点的所有分组列表（平台中性）。
func (s *MetricsService) AdminGroups(ctx context.Context, userID string) (AdminGroupsResponse, error) {
	adminAccountID, err := s.requireCurrentAdminAccount(ctx, userID)
	if err != nil {
		return AdminGroupsResponse{}, err
	}
	record, err := s.store.Get(ctx, userID, adminAccountID)
	if err != nil {
		return AdminGroupsResponse{}, err
	}
	if record == nil || !record.Session.IsAuthenticated() {
		return AdminGroupsResponse{}, requestError(ErrorAdminOnly)
	}

	session, err := s.freshAdminSession(ctx, userID, adminAccountID, record)
	if err != nil {
		return AdminGroupsResponse{}, requestError(ErrorAdminOnly)
	}

	groups, err := s.platform.FetchAdminGroups(session)
	if err != nil {
		return AdminGroupsResponse{}, err
	}

	items := make([]AdminGroupItem, 0, len(groups))
	for _, g := range groups {
		platform := ""
		if g.Platform != nil {
			platform = *g.Platform
		}
		items = append(items, AdminGroupItem{
			ID:         g.ID,
			Name:       g.Name,
			Platform:   platform,
			Multiplier: g.MultiplierDisplay,
		})
	}
	return AdminGroupsResponse{Count: len(items), Groups: items}, nil
}

// GroupUsageToday 获取当前工作区「我的站点」所有分组今日的使用额度（平台中性）。
// 数据只在弹窗打开时按需请求，不参与 LiveMetrics 的批量指标计算。
func (s *MetricsService) GroupUsageToday(ctx context.Context, userID string) (GroupUsageTodayResponse, error) {
	adminAccountID, err := s.requireCurrentAdminAccount(ctx, userID)
	if err != nil {
		return GroupUsageTodayResponse{}, err
	}
	record, err := s.store.Get(ctx, userID, adminAccountID)
	if err != nil {
		return GroupUsageTodayResponse{}, err
	}
	if record == nil || !record.Session.IsAuthenticated() {
		return GroupUsageTodayResponse{}, requestError(ErrorAdminOnly)
	}

	session, err := s.freshAdminSession(ctx, userID, adminAccountID, record)
	if err != nil {
		return GroupUsageTodayResponse{}, requestError(ErrorAdminOnly)
	}

	if err := s.platform.VerifyAdmin(session); err != nil {
		return GroupUsageTodayResponse{}, requestError(ErrorAdminOnly)
	}

	groups, err := s.platform.FetchAdminGroups(session)
	if err != nil {
		return GroupUsageTodayResponse{}, err
	}

	stats, err := s.platform.FetchAdminGroupDailyStats(session, groups)
	if err != nil {
		return GroupUsageTodayResponse{}, err
	}

	// 归一化：分组名去空格、空名跳过、重名分组合并求和；顺序按首次出现排列。
	order := make([]string, 0, len(stats))
	totals := make(map[string]float64, len(stats))
	for _, stat := range stats {
		name := strings.TrimSpace(stat.GroupName)
		if name == "" {
			continue
		}
		if _, exists := totals[name]; !exists {
			order = append(order, name)
		}
		totals[name] += stat.TodayActualCost
	}

	items := make([]GroupUsageTodayItem, 0, len(order))
	var total float64
	for _, name := range order {
		amount := totals[name]
		items = append(items, GroupUsageTodayItem{GroupName: name, TodayAmount: amount})
		total += amount
	}

	return GroupUsageTodayResponse{
		Date:   dashboardDate(time.Now()),
		Total:  total,
		Groups: items,
	}, nil
}

// UpstreamKeyUsageToday 获取当前工作区所有上游站点中，今天有消费的 key 明细（仪表盘「今日成本」下钻）。
// 数据只在弹窗打开时按需请求，不参与 LiveMetrics 的批量指标计算。
// 排序、总额与筛选逻辑全部由 upstream.Service.KeyUsageToday 保证与 todayPurchase 口径一致，
// 这里只负责排序展示和响应封装。
func (s *MetricsService) UpstreamKeyUsageToday(ctx context.Context, userID string) (UpstreamKeyUsageTodayResponse, error) {
	items, err := s.upstreams.KeyUsageToday(ctx, userID)
	failedSites := 0
	totalSites := 0
	if err != nil {
		var collectionErr *upstream.KeyUsageCollectionError
		if !errors.As(err, &collectionErr) || collectionErr.TotalSites <= 0 || collectionErr.FailedSites >= collectionErr.TotalSites {
			return UpstreamKeyUsageTodayResponse{}, requestError(ErrorUpstreamKeyUsageUnavailable)
		}
		failedSites = collectionErr.FailedSites
		totalSites = collectionErr.TotalSites
		log.Printf("dashboard key usage: partial upstream failure user_id=%s failed_sites=%d total_sites=%d", userID, failedSites, totalSites)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].TodayAmount > items[j].TodayAmount
	})

	responseItems := make([]UpstreamKeyUsageTodayItem, 0, len(items))
	var total float64
	for _, item := range items {
		responseItems = append(responseItems, UpstreamKeyUsageTodayItem{
			SiteID:       item.SiteID,
			SiteName:     item.SiteName,
			Platform:     string(item.Platform),
			KeyID:        item.KeyID,
			KeyName:      item.KeyName,
			GroupName:    item.GroupName,
			TodayAmount:  item.TodayAmount,
			RawAmount:    item.RawAmount,
			RechargeRate: item.RechargeRate,
		})
		total += item.TodayAmount
	}

	return UpstreamKeyUsageTodayResponse{
		Date:        dashboardDate(time.Now()),
		Total:       total,
		Keys:        responseItems,
		FailedSites: failedSites,
		TotalSites:  totalSites,
	}, nil
}

// UpstreamBalanceBreakdown 获取当前工作区所有上游站点的余额明细（仪表盘「上游总余额」下钻）。
// 直接复用已同步缓存数据，不触发外部平台请求；未知余额（rechargeRate 未配置或尚未同步成功）的站点排在列表最后，
// total 只对已知余额求和，与 LiveMetrics 中 upstreamBalance 的计算口径一致。
func (s *MetricsService) UpstreamBalanceBreakdown(ctx context.Context, userID string) (UpstreamBalanceBreakdownResponse, error) {
	items, err := s.upstreams.BalanceBreakdown(ctx, userID)
	if err != nil {
		return UpstreamBalanceBreakdownResponse{}, err
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Balance == nil || items[j].Balance == nil {
			return items[i].Balance != nil
		}
		return *items[i].Balance > *items[j].Balance
	})

	responseItems := make([]UpstreamBalanceBreakdownItem, 0, len(items))
	var total float64
	for _, item := range items {
		responseItems = append(responseItems, UpstreamBalanceBreakdownItem{
			SiteID:       item.SiteID,
			SiteName:     item.SiteName,
			Platform:     string(item.Platform),
			Balance:      item.Balance,
			RawBalance:   item.RawBalance,
			RechargeRate: item.RechargeRate,
			LastSyncedAt: item.LastSyncedAt,
			Status:       string(item.Status),
		})
		if item.Balance != nil {
			total += *item.Balance
		}
	}

	return UpstreamBalanceBreakdownResponse{
		Total: total,
		Sites: responseItems,
	}, nil
}

// GetBalanceFilter 读取当前用户当前工作区的余额筛选配置。
func (s *MetricsService) GetBalanceFilter(ctx context.Context, userID string) (BalanceFilterConfig, error) {
	// 按当前工作区隔离筛选配置。
	adminAccountID, err := s.requireCurrentAdminAccount(ctx, userID)
	if err != nil {
		return BalanceFilterConfig{}, err
	}
	return s.metricsRepo.GetBalanceFilter(ctx, userID, adminAccountID)
}

// SaveBalanceFilter 保存用户当前工作区的余额筛选配置。
func (s *MetricsService) SaveBalanceFilter(ctx context.Context, userID string, config BalanceFilterConfig) error {
	// 按当前工作区隔离筛选配置。
	adminAccountID, err := s.requireCurrentAdminAccount(ctx, userID)
	if err != nil {
		return err
	}
	config.UserID = userID
	config.AdminAccountID = adminAccountID
	return s.metricsRepo.SaveBalanceFilter(ctx, config)
}

func (s *MetricsService) requireCurrentAdminAccount(ctx context.Context, userID string) (string, error) {
	if s.accounts == nil {
		return "", requestError(ErrorAdminOnly)
	}
	return s.accounts.RequireCurrentID(ctx, userID)
}

func metricsRandomID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
