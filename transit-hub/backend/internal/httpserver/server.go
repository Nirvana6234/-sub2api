package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"transithub/backend/internal/config"
	"transithub/backend/internal/modules/admin_accounts"
	"transithub/backend/internal/modules/auth"
	"transithub/backend/internal/modules/connection_health"
	"transithub/backend/internal/modules/dashboard"
	"transithub/backend/internal/modules/group_rate_campaigns"
	"transithub/backend/internal/modules/group_rates"
	"transithub/backend/internal/modules/health"
	"transithub/backend/internal/modules/leaderboard"
	"transithub/backend/internal/modules/lottery"
	"transithub/backend/internal/modules/mass_email"
	"transithub/backend/internal/modules/my_sites"
	"transithub/backend/internal/modules/purity_check"
	"transithub/backend/internal/modules/settings"
	"transithub/backend/internal/modules/system"
	"transithub/backend/internal/modules/tickets"
	"transithub/backend/internal/modules/upstream"
	"transithub/backend/internal/modules/users"
	"transithub/backend/internal/shared/authctx"
	"transithub/backend/internal/shared/httpjson"
)

const (
	apiPrefix              = "/api"
	upstreamRequestTimeout = 60 * time.Second
)

type Server struct {
	cfg                            config.Config
	mux                            *http.ServeMux
	allowed                        map[string]struct{}
	authService                    *auth.Service
	leaderboardFrameAncestorOrigin func(ctx context.Context, embedToken string) (string, bool)
	lotteryFrameAncestorOrigin     func(ctx context.Context, embedToken string) (string, bool)
	lotteryCancel                  context.CancelFunc
	lotteryWorker                  *lottery.Worker
	purityCheckCancel              context.CancelFunc
	purityCheck                    *purity_check.Service
}

func New(cfg config.Config, db *pgxpool.Pool, redisClient *redis.Client) *Server {
	server := &Server{
		cfg:     cfg,
		mux:     http.NewServeMux(),
		allowed: makeAllowedOrigins(cfg.CORSOrigins),
	}

	health.RegisterRoutes(server.mux)
	authService := auth.NewService(auth.NewRepository(db))
	server.authService = authService
	if err := authService.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}

	// 管理员初始化：数据库就绪后、注册路由前执行
	if err := authService.BootstrapAdmin(context.Background(), cfg.AdminEmail, cfg.AdminPassword); err != nil {
		panic(err)
	}

	auth.RegisterRoutes(server.mux, authService, cfg.AllowPublicRegister)
	users.RegisterRoutes(server.mux, users.NewService(users.NewRepository(db)))
	adminAccountsService := admin_accounts.NewService(admin_accounts.NewRepository(db))
	upstreamRepository := upstream.NewRepository(db)
	if err := upstreamRepository.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	groupRatesService := group_rates.NewService(group_rates.NewRepository(db))
	if err := groupRatesService.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	group_rates.RegisterRoutes(server.mux, groupRatesService, adminAccountsService)
	upstreamHTTPClient := &http.Client{Timeout: upstreamRequestTimeout}
	platformService := upstream.NewPlatformService(upstream.NewHTTPClient(upstreamHTTPClient))
	upstreamCache := upstream.NewRedisSiteCache(redisClient)
	upstreamService := upstream.NewService(platformService, upstreamRepository, groupRateSnapshotWriter{service: groupRatesService}, upstreamCache)
	groupRatesService.SetSnapshotRefresher(upstreamService)
	upstreamService.SetAdminAccountResolver(adminAccountsService)
	upstream.RegisterRoutes(server.mux, upstreamService, adminAccountsService)
	mySitesService := my_sites.NewService(my_sites.NewRepository(db), platformService, upstreamService)
	if err := mySitesService.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	my_sites.RegisterRoutes(server.mux, mySitesService)
	mySitesService.SetAdminAccountResolver(adminAccountsService)
	upstreamService.SetAfterRemove(func(ctx context.Context, userID, adminAccountID, siteID string) error {
		return mySitesService.CleanupDeletedUpstreamSites(ctx, userID, adminAccountID, []string{siteID})
	})
	// 上游 Key 连通性测试：复用 connection_health 的探活实现，见 upstream_key_tester.go。
	mySitesService.SetUpstreamKeyTester(newUpstreamKeyTester())

	// 工单模块：iframe 嵌入配置 + 工单/回复。公开 iframe 接口鉴权完全依赖 embedToken/Sub2API
	// token 换取的 embed session，与 TransitHub 登录态无关，因此不加入 protectedPath（见下方）。
	ticketsRepository := tickets.NewRepository(db)
	if err := ticketsRepository.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	ticketsSub2APIClient := tickets.NewSub2APIClient(&http.Client{Timeout: upstreamRequestTimeout})
	ticketsSessions := tickets.NewEmbedSessionStore(redisClient)
	ticketsStorage, err := tickets.NewAttachmentStorage(cfg.TicketUploadDir)
	if err != nil {
		panic(err)
	}
	ticketsService := tickets.NewService(ticketsRepository, ticketsSessions, ticketsSub2APIClient, ticketsStorage)
	ticketsService.SetAdminAccountResolver(adminAccountsService)
	// Sub2API 用户资料弹窗按当前 workspace 的 admin 会话（mySitesService）实时查询用户详情/余额
	// 历史（platformService），复用已有的会话存储和刷新逻辑，不新增第二套 admin token 存储。
	ticketsService.SetAdminSessionProvider(mySitesService)
	ticketsService.SetSub2APIAdminClient(platformService)
	tickets.RegisterRoutes(server.mux, ticketsService)

	// 排行榜模块：后台接口复用当前 workspace 的 dashboard admin session；公开 embed 接口
	// 不进入 TransitHub 登录态，由独立 embed token + Sub2API viewer token 换取短期 Redis session。
	leaderboardRepository := leaderboard.NewRepository(db)
	leaderboardSessions := leaderboard.NewEmbedSessionStore(redisClient)
	leaderboardSub2APIClient := leaderboard.NewSub2APIClient(&http.Client{Timeout: upstreamRequestTimeout})
	leaderboardService := leaderboard.NewService(leaderboardRepository, leaderboardSessions, leaderboardSub2APIClient, platformService, mySitesService)
	leaderboardService.SetAdminAccountResolver(adminAccountsService)
	if err := leaderboardService.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	server.leaderboardFrameAncestorOrigin = leaderboardService.FrameAncestorOrigin
	leaderboard.RegisterRoutes(server.mux, leaderboardService)

	lotteryRepository := lottery.NewRepository(db)
	lotterySessions := lottery.NewEmbedSessionStore(redisClient)
	if cfg.LotteryAllowPrivateSub2APITargets {
		log.Printf("[lottery] WARNING: private Sub2API targets are enabled for local debugging; do not enable this in production")
	}
	lotteryViewerClient := lottery.NewSub2APIViewerClientWithPrivateTargets(&http.Client{Timeout: upstreamRequestTimeout}, cfg.LotteryAllowPrivateSub2APITargets)
	lotteryRewardClient := lottery.NewRewardClientWithPrivateTargets(&http.Client{Timeout: upstreamRequestTimeout}, cfg.LotteryAllowPrivateSub2APITargets)
	lotteryService := lottery.NewService(lotteryRepository, lotterySessions, lotteryViewerClient, lotteryRewardClient, mySitesService)
	lotteryService.SetAdminAccountResolver(adminAccountsService)
	lotteryService.SetSubscriptionGroupProvider(platformService)
	lotteryService.SetAllowPrivateTargets(cfg.LotteryAllowPrivateSub2APITargets)
	if err := lotteryService.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	server.lotteryFrameAncestorOrigin = lotteryService.FrameAncestorOrigin
	lottery.RegisterRoutes(server.mux, lotteryService)

	settingsService := settings.NewService(http.DefaultClient, settings.NewRepository(db))
	settingsService.SetAdminAccountResolver(adminAccountsService)
	if err := settingsService.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	// SMTP_ENCRYPTION_KEY 是可选项：空值不影响启动；显式配置了非法值（非 base64 或非 32 字节）
	// 必须尽早启动失败，避免运行时才发现加密能力不可用。抽成 configureSMTPEncryptionKey
	// 这个窄 seam，便于在不启动真实 DB/Redis 依赖的情况下单元测试这条组装路径。
	if _, err := configureSMTPEncryptionKey(settingsService, cfg.SMTPEncryptionKey); err != nil {
		panic(err)
	}

	// dashboard 指标表必须在 admin_accounts 之前完成 schema，
	// 因为 admin_accounts.EnsureSchema 的 legacy 迁移会 UPDATE dashboard 表。
	metricsRepo := dashboard.NewMetricsRepository(db)
	if err := metricsRepo.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}

	// admin_accounts 最后执行 schema：此时所有业务表和 workspace 字段已存在，
	// legacy 迁移可以安全地 UPDATE 所有业务表的 admin_account_id。
	if err := adminAccountsService.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	admin_accounts.RegisterRoutes(server.mux, adminAccountsService)

	// 注入机器人通知能力，供自动调价成功后发送通知。
	mySitesService.SetBotNotifier(settingsService)

	// 批量邮件模块只复用已保存的模板/SMTP 配置和当前 workspace 的 Sub2API admin 会话；
	// 创建批次时只解析收件人，真正 SMTP 发送由 Postgres-backed worker 异步执行。
	massEmailService := mass_email.NewService(
		mass_email.NewRepository(db),
		mySitesService,
		platformService,
		settingsService,
	)
	if err := massEmailService.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	mass_email.RegisterRoutes(server.mux, massEmailService, adminAccountsService)
	massEmailWorker := mass_email.NewWorker(massEmailService)

	// 活动调价中心：批量修改 admin 自有分组倍率的独立模块，不复用/不污染 my_sites 的自动调价逻辑。
	// mySitesService 提供 admin 会话与分组倍率读写能力，groupRatesService 提供分组类型标签查询，
	// settingsService 提供机器人通知发送能力。
	campaignsService := group_rate_campaigns.NewService(
		group_rate_campaigns.NewRepository(db),
		mySitesService,
		settingsService,
		groupRatesService,
		group_rate_campaigns.Config{
			NotifyEnabledDefault: cfg.GroupRateCampaignNotifyEnabled,
			DefaultNotifyBotIDs:  cfg.GroupRateCampaignDefaultNotifyBots,
			StartTemplateDefault: cfg.GroupRateCampaignStartTemplate,
			EndTemplateDefault:   cfg.GroupRateCampaignEndTemplate,
			SchedulerInterval:    cfg.GroupRateCampaignSchedulerInterval,
		},
	)
	if err := campaignsService.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	campaignsService.SetAdminAccountResolver(adminAccountsService)
	group_rate_campaigns.RegisterRoutes(server.mux, campaignsService, adminAccountsService)

	// 分组健康探活模块：数据源为 real_connections（通过 mySitesService 只读接口），
	// upstreamService 提供站点 base_url/平台类型查询，platformService 提供 new-api 远端降级/恢复能力。
	// 不新增手动配置的探活目标，也不改变 my_sites/upstream 现有数据语义。
	// 仓储单独留一个引用：每日简报要直接查倍率变动历史，不经过 service 层。
	connHealthRepo := connection_health.NewRepository(db)
	connHealthService := connection_health.NewService(
		connHealthRepo,
		mySitesService,
		upstreamService,
		platformService,
	)
	if err := connHealthService.EnsureSchema(context.Background()); err != nil {
		panic(err)
	}
	connHealthService.SetAdminAccountResolver(adminAccountsService)
	// 注入平台中性的分组/账号读取能力：admin 分组健康主列表用它拉取 admin 全量分组及
	// 分组下账号/渠道，叠加 real_connections 探活状态。platformService 已实现所需方法。
	connHealthService.SetPlatformGroupReader(platformService)
	// Reuse the already configured multiplier-alert bots for scheduling alerts.
	// These alerts originate from the background scheduler, so load settings by
	// the event workspace instead of relying on a browser-selected workspace.
	connHealthService.SetAutomaticDisableNotifier(connection_health.AutomaticDisableNotifyFunc(func(ctx context.Context, event connection_health.AutomaticDisableEvent) {
		strategy, err := settingsService.GetStrategyForWorkspace(ctx, event.UserID, event.AdminAccountID)
		if err != nil {
			log.Printf("[alert] load automatic-disable notification settings failed user_id=%s admin_account_id=%s err=%v", event.UserID, event.AdminAccountID, err)
			return
		}
		if !strategy.EnableMultiplierAlert || len(strategy.MultiplierNotifyBotIDs) == 0 {
			return
		}
		message := formatAutomaticDisableAlert(event)
		log.Printf("[alert] upstream target automatically deprioritized platform=%s group=%s account=%s old=%d new=%d recent_usage_samples=%d reason=%s",
			event.Platform, event.GroupName, event.AccountName, event.PreviousPriority, event.CurrentPriority, event.RecentUsageSamples, event.Reason)
		settingsService.SendFormattedToBots(ctx, event.UserID, strategy.MultiplierNotifyBotIDs, message, strategy.MultiplierTemplateFormat)
	}))
	connHealthService.SetAutomaticRecoveryNotifier(connection_health.AutomaticRecoveryNotifyFunc(func(ctx context.Context, event connection_health.AutomaticRecoveryEvent) {
		strategy, err := settingsService.GetStrategyForWorkspace(ctx, event.UserID, event.AdminAccountID)
		if err != nil {
			log.Printf("[alert] load automatic-recovery notification settings failed user_id=%s admin_account_id=%s err=%v", event.UserID, event.AdminAccountID, err)
			return
		}
		if !strategy.EnableMultiplierAlert || len(strategy.MultiplierNotifyBotIDs) == 0 {
			return
		}
		message := formatAutomaticRecoveryAlert(event)
		log.Printf("[alert] upstream target recovery notification stage=%s platform=%s group=%s account=%s model=%s", event.Stage, event.Platform, event.GroupName, event.AccountName, event.ModelName)
		settingsService.SendFormattedToBots(ctx, event.UserID, strategy.MultiplierNotifyBotIDs, message, strategy.MultiplierTemplateFormat)
	}))
	connection_health.RegisterRoutes(server.mux, connHealthService)

	// GPT-5.6 纯度检测：驱动一个旁路 Python 检测器，判断上游给的到底是不是申报的型号。
	// mySitesService 提供 admin 会话，platformService 负责列账号并在检测前临时解析明文凭据
	// （与 connection_health 手动探活复用同一条 ResolveProbeCredential 链路）。
	// 未配置 PURITY_CHECK_DETECTOR_URL 时接口返回「检测器不可用」，不启动 worker。
	purityCheckService := purity_check.NewService(
		purity_check.NewRepository(db),
		mySitesService,
		platformService,
		purity_check.NewDetectorClient(cfg.PurityCheckDetectorURL),
		cfg.PurityCheckDetectorRunsDir,
	)
	purityCheckService.SetAdminAccountResolver(adminAccountsService)
	// 分组倍率对接按已绑定的本方账号显示最近一次可行动的纯度问题；状态只在
	// mapping-options 的响应中派生，不会写回用户的调价配置。
	mySitesService.SetPurityIssueReader(purityCheckService)
	groupRatesService.SetPurityIssueReader(purityCheckService)
	// 账号绑的是 xray 的 socks5 端口，检测器只认 http:// 代理，这里给它同出口的 HTTP 入口。
	purityCheckService.SetHTTPProxyURL(cfg.PurityCheckHTTPProxyURL)
	purity_check.RegisterRoutes(server.mux, purityCheckService)

	// 所有 workspace 表 schema 完成后再补 legacy 归属；随后才启动 restore、worker 和 scheduler，
	// 避免后台任务在旧行尚未补齐 workspace 时读取或写回数据。
	if err := adminAccountsService.AssignLegacyRows(context.Background()); err != nil {
		panic(err)
	}
	if err := upstreamService.RestoreSavedSites(context.Background()); err != nil {
		panic(err)
	}
	massEmailWorker.Start(context.Background())
	campaignsService.StartScheduler(context.Background())
	lotteryCtx, lotteryCancel := context.WithCancel(context.Background())
	lotteryWorker := lottery.NewWorker(lotteryService)
	lotteryWorker.Start(lotteryCtx)
	lotteryService.StartScheduler(lotteryCtx)
	server.lotteryCancel = lotteryCancel
	server.lotteryWorker = lotteryWorker
	connHealthService.StartScheduler(context.Background())
	connHealthService.StartMultiplierSnapshotScheduler(context.Background())
	// 串行 worker：检测器同一时刻只能跑一个会话，队列必须一个一个来。
	purityCheckCtx, purityCheckCancel := context.WithCancel(context.Background())
	purityCheckService.Start(purityCheckCtx)
	server.purityCheckCancel = purityCheckCancel
	server.purityCheck = purityCheckService

	// 策略设置变更时通知上游服务更新定时同步配置。
	applyRefreshConfig := func(s settings.StrategySettings) {
		upstreamService.SetRefreshConfig(upstream.RefreshConfig{
			Enabled:  s.EnableRefreshInterval,
			Interval: time.Duration(s.RefreshInterval) * time.Second,
		})
	}
	settingsService.OnStrategyChanged = applyRefreshConfig

	// 启动时读取已保存的策略设置，按配置决定是否开启定时同步。
	if strategy, err := settingsService.GetFirstStrategy(context.Background()); err == nil {
		applyRefreshConfig(strategy)
	}

	// 站点同步成功后检查余额预警和倍率变更，按配置发送通知。
	upstreamService.AfterSync = func(ctx context.Context, userID, adminAccountID, siteID, siteName string, oldMetrics, newMetrics upstream.Metrics) {
		strategy, err := settingsService.GetFirstStrategy(ctx)
		if err != nil {
			return
		}
		checkBalanceWarning(ctx, settingsService, upstreamService, strategy, userID, adminAccountID, siteID, siteName, oldMetrics, newMetrics)

		// 未映射分组也要记录事件，所以映射关系读取不能被即时预警开关短路。
		mappedGroups, mapErr := mySitesService.ListMappedUpstreamGroups(ctx, userID, adminAccountID)
		mappingAvailable := mapErr == nil
		if mapErr != nil {
			// 读不到映射时全部按 mapped=true 降级：宁可多发一条即时通知，
			// 也不能把不确定的事件静默塞进日报，造成真正的即时预警漏报。
			log.Printf("[alert] 读取分组映射失败，本轮倍率事件按已映射处理 user_id=%s site=%s err=%v", userID, siteName, mapErr)
			mappedGroups = map[string]struct{}{}
		}
		checkMultiplierChangesWithEvents(ctx, settingsService, strategy, userID, adminAccountID, siteID, siteName, oldMetrics, newMetrics, mappedGroups, mappingAvailable, upstreamRepository, mySitesService)
		// 自动调价：分组级 enableAutoPricing 是唯一开关，Service 内部逐 mapping 判断。
		mySitesService.ApplyAutoPricingAfterSync(ctx, userID, adminAccountID, siteID, siteName, oldMetrics, newMetrics)
	}

	// 仪表盘指标服务：实时计算核心指标，并持续维护当天快照。
	// 日报预览/推送复用同一个实时指标入口，确保报告反映当天截至当前时刻的数据。
	dashboardSessionStore := dashboard.NewRepository(redisClient)
	metricsService := dashboard.NewMetricsService(dashboardSessionStore, platformService, upstreamService, metricsRepo, adminAccountsService)
	metricsService.SetMySiteSync(mySitesService)
	metricsService.StartScheduler(context.Background())

	// 每日简报：按各工作区自己配置的时刻汇总站点消费、余额、倍率变动与同步健康。
	// 未开启时调度器每分钟只做一次读配置的空转，开销可忽略。
	dailyReportSvc := newDailyReportScheduler(dailyReportDeps{
		settings:    settingsService,
		mySites:     mySitesService,
		upstream:    upstreamService,
		metrics:     metricsRepo,
		liveMetrics: metricsService,
		connHealth:  connHealthRepo,
		events:      upstreamRepository,
	})
	dailyReportSvc.Start(context.Background())
	// 手动触发（网页按钮 / 机器人指令）与定时推送共用同一套取数和排版。
	dailyReportSvc.RegisterRoutes(server.mux)

	settings.RegisterRoutes(server.mux, settingsService)

	// 仪表盘 admin 登录门禁：复用 sub2api 平台客户端（platformService），会话存于 Redis，
	// 并启动后台协程对临期令牌做自动刷新。
	dashboardService := dashboard.NewService(dashboardSessionStore, platformService)
	dashboardService.SetAdminAccountService(adminAccountsService)
	dashboardService.SetMySiteSync(mySitesService)
	adminAccountsService.SetWorkspaceCleanup(workspaceCleanup{
		dashboardSessions:   dashboardSessionStore,
		ticketSessions:      ticketsSessions,
		leaderboardSessions: leaderboardSessions,
		lotterySessions:     lotterySessions,
		attachments:         ticketsStorage,
		upstreamSites:       upstreamService,
		mySiteMappings:      mySitesService,
	})
	adminAccountsService.StartCleanupWorker(context.Background(), time.Minute)
	dashboardService.StartRefresher(context.Background())

	dashboard.RegisterRoutes(server.mux, dashboardService, metricsService)

	// 系统信息 API：开源版仅保留版本号展示
	systemService := system.NewService(cfg)
	system.RegisterRoutes(server.mux, systemService)

	return server
}

type groupRateSnapshotWriter struct {
	service *group_rates.Service
}

type dashboardSessionCleaner interface {
	Delete(ctx context.Context, userID string, adminAccountID string) error
}

type ticketEmbedSessionCleaner interface {
	DeleteWorkspace(ctx context.Context, userID string, adminAccountID string) error
}

type leaderboardEmbedSessionCleaner interface {
	DeleteWorkspace(ctx context.Context, userID string, adminAccountID string) error
}

type lotteryEmbedSessionCleaner interface {
	DeleteWorkspace(ctx context.Context, userID string, adminAccountID string) error
}

type attachmentCleaner interface {
	Delete(storagePath string) error
}

type upstreamSiteCleaner interface {
	CleanupDeletedWorkspaceSites(ctx context.Context, userID string, siteIDs []string) error
}

type mySiteMappingCleaner interface {
	CleanupDeletedUpstreamSites(ctx context.Context, userID, adminAccountID string, siteIDs []string) error
}

type workspaceCleanup struct {
	dashboardSessions   dashboardSessionCleaner
	ticketSessions      ticketEmbedSessionCleaner
	leaderboardSessions leaderboardEmbedSessionCleaner
	lotterySessions     lotteryEmbedSessionCleaner
	attachments         attachmentCleaner
	upstreamSites       upstreamSiteCleaner
	mySiteMappings      mySiteMappingCleaner
}

func (c workspaceCleanup) CleanupDeletedWorkspace(ctx context.Context, payload admin_accounts.WorkspaceCleanupPayload) error {
	var errs []error
	if c.dashboardSessions != nil {
		if err := c.dashboardSessions.Delete(ctx, payload.UserID, payload.AdminAccountID); err != nil {
			errs = append(errs, fmt.Errorf("dashboard session cleanup: %w", err))
		}
	}
	if c.ticketSessions != nil {
		if err := c.ticketSessions.DeleteWorkspace(ctx, payload.UserID, payload.AdminAccountID); err != nil {
			errs = append(errs, fmt.Errorf("ticket embed session cleanup: %w", err))
		}
	}
	if c.leaderboardSessions != nil {
		if err := c.leaderboardSessions.DeleteWorkspace(ctx, payload.UserID, payload.AdminAccountID); err != nil {
			errs = append(errs, fmt.Errorf("leaderboard embed session cleanup: %w", err))
		}
	}
	if c.lotterySessions != nil {
		if err := c.lotterySessions.DeleteWorkspace(ctx, payload.UserID, payload.AdminAccountID); err != nil {
			errs = append(errs, fmt.Errorf("lottery embed session cleanup: %w", err))
		}
	}
	if c.upstreamSites != nil {
		if err := c.upstreamSites.CleanupDeletedWorkspaceSites(ctx, payload.UserID, payload.UpstreamSiteIDs); err != nil {
			errs = append(errs, fmt.Errorf("upstream site cleanup: %w", err))
		}
	}
	if c.mySiteMappings != nil {
		if err := c.mySiteMappings.CleanupDeletedUpstreamSites(ctx, payload.UserID, payload.AdminAccountID, payload.UpstreamSiteIDs); err != nil {
			errs = append(errs, fmt.Errorf("my-site mapping cleanup: %w", err))
		}
	}
	if c.attachments != nil {
		for _, path := range payload.AttachmentStoragePaths {
			if strings.TrimSpace(path) == "" {
				continue
			}
			if err := c.attachments.Delete(path); err != nil {
				errs = append(errs, fmt.Errorf("ticket attachment cleanup %q: %w", path, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (w groupRateSnapshotWriter) SaveSiteSnapshot(ctx context.Context, userID string, adminAccountID string, siteID string, siteName string, sitePlatform upstream.Platform, groups []upstream.SnapshotGroup) error {
	snapshots := make([]group_rates.SnapshotGroup, 0, len(groups))
	for _, group := range groups {
		snapshots = append(snapshots, group_rates.SnapshotGroup{
			ID:         group.ID,
			Name:       group.Name,
			Platform:   group.Platform,
			Multiplier: group.Multiplier,
		})
	}
	return w.service.SaveSiteSnapshot(ctx, userID, adminAccountID, siteID, siteName, string(sitePlatform), snapshots)
}

func (s *Server) Handler() http.Handler {
	// 非 /api 路径交给静态文件服务，支持 Vue history 路由回退
	static := staticHandler(s.cfg.PublicDir)

	return s.logRequests(s.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.setSecurityHeaders(w, r)
		if !strings.HasPrefix(r.URL.Path, apiPrefix) {
			static.ServeHTTP(w, r)
			return
		}
		if s.protectedPath(r.URL.Path) {
			user, err := s.authService.CurrentUser(r.Context(), bearerToken(r.Header.Get("Authorization")))
			if err != nil {
				httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
				return
			}
			r = r.WithContext(authctx.WithUserID(r.Context(), user.ID))
		}
		s.mux.ServeHTTP(w, r)
	})))
}

func (s *Server) Shutdown(ctx context.Context) error {
	// 先停纯度检测：它可能正在驱动一个检测器会话，停之前要让它把 stop 发出去，
	// 否则旁路服务会留着一个孤儿会话，下次启动的任务全被判 busy。
	if s.purityCheckCancel != nil {
		s.purityCheckCancel()
	}
	if s.purityCheck != nil {
		s.purityCheck.Stop()
	}
	if s.lotteryCancel != nil {
		s.lotteryCancel()
	}
	if s.lotteryWorker == nil {
		return nil
	}
	s.lotteryWorker.Stop()
	done := make(chan struct{})
	go func() {
		s.lotteryWorker.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (s *Server) protectedPath(path string) bool {
	return strings.HasPrefix(path, "/api/admin-accounts") || strings.HasPrefix(path, "/api/upstream-sites") || strings.HasPrefix(path, "/api/group-rates") || strings.HasPrefix(path, "/api/group-rate-campaigns") || strings.HasPrefix(path, "/api/my-sites") || strings.HasPrefix(path, "/api/settings") || strings.HasPrefix(path, "/api/dashboard") || strings.HasPrefix(path, "/api/system") || strings.HasPrefix(path, "/api/connection-health") || strings.HasPrefix(path, "/api/purity-check") || strings.HasPrefix(path, "/api/daily-report") || strings.HasPrefix(path, "/api/tickets") || strings.HasPrefix(path, "/api/leaderboard") || strings.HasPrefix(path, "/api/lottery") || strings.HasPrefix(path, "/api/mass-email")
}

func (s *Server) setSecurityHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if r.Method == http.MethodGet && r.URL.Path == "/embed/leaderboard" {
		w.Header().Set("Referrer-Policy", "no-referrer")
		origin := ""
		if s.leaderboardFrameAncestorOrigin != nil {
			origin, _ = s.leaderboardFrameAncestorOrigin(r.Context(), r.URL.Query().Get("embed_token"))
		}
		if origin == "" {
			w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
			return
		}
		w.Header().Set("Content-Security-Policy", "frame-ancestors "+origin)
	}
	if r.Method == http.MethodGet && r.URL.Path == "/embed/lottery" {
		w.Header().Set("Referrer-Policy", "no-referrer")
		origin := ""
		if s.lotteryFrameAncestorOrigin != nil {
			origin, _ = s.lotteryFrameAncestorOrigin(r.Context(), r.URL.Query().Get("embed_token"))
		}
		if origin == "" {
			w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
			return
		}
		w.Header().Set("Content-Security-Policy", "frame-ancestors "+origin)
	}
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		writer := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(writer, r)
		log.Printf("request method=%s path=%s status=%d duration=%s", r.Method, r.URL.Path, writer.status, time.Since(startedAt))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Flush 透传底层 ResponseWriter 的 Flusher 能力，
// 确保 SSE 等流式响应在经过 logRequests 中间件包装后仍能正常刷新。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if len(s.cfg.CORSOrigins) == 0 {
			if origin == "" {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		} else if _, ok := s.allowed[origin]; ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func makeAllowedOrigins(origins []string) map[string]struct{} {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	return allowed
}

// checkBalanceWarning 检测余额是否低于阈值并发送通知。
// 同一工作区的同一上游站点只发送一次；状态持久化在数据库中，服务重启也不重复。
// 优先使用站点级 BalanceThreshold 覆盖全局 DefaultBalanceThreshold；站点设置为 nil 时降级到全局。
func checkBalanceWarning(ctx context.Context, svc *settings.Service, uSvc *upstream.Service, strategy settings.StrategySettings, userID, adminAccountID, siteID, siteName string, _ /* oldMetrics */, newMetrics upstream.Metrics) {
	if !strategy.EnableBalanceWarning || len(strategy.BalanceNotifyBotIDs) == 0 {
		return
	}
	if newMetrics.Balance.Value == nil {
		return
	}

	// 获取站点充值倍率，用于将 USD 余额转换为 CNY 后与阈值比较。
	site, err := uSvc.GetSite(ctx, siteID)
	if err != nil {
		return
	}
	rechargeRate := site.RechargeRate
	if rechargeRate <= 0 {
		rechargeRate = 1
	}

	newBalCNY := *newMetrics.Balance.Value * rechargeRate

	// 站点级阈值覆盖：有值则用站点配置，否则使用全局默认（均为 CNY）。
	threshold := strategy.DefaultBalanceThreshold
	if site.Settings.BalanceThreshold != nil {
		threshold = *site.Settings.BalanceThreshold
	}

	if newBalCNY >= threshold {
		return
	}

	if !claimBalanceAlert(ctx, svc, userID, adminAccountID, siteID, siteName) {
		return
	}

	msg := formatBalanceWarning(siteName, newBalCNY, threshold, strategy.BalanceTemplate)
	log.Printf("[alert] 余额预警触发 site=%s balanceCNY=%.2f threshold=%.2f rechargeRate=%.2f", siteName, newBalCNY, threshold, rechargeRate)
	svc.SendFormattedToBots(ctx, userID, strategy.BalanceNotifyBotIDs, msg, strategy.BalanceTemplateFormat)
}

type balanceAlertClaimer interface {
	ClaimBalanceAlert(ctx context.Context, userID, adminAccountID, siteID string) (bool, error)
}

func claimBalanceAlert(ctx context.Context, claimer balanceAlertClaimer, userID, adminAccountID, siteID, siteName string) bool {
	claimed, err := claimer.ClaimBalanceAlert(ctx, userID, adminAccountID, siteID)
	if err != nil {
		log.Printf("[alert] 记录余额预警状态失败 user_id=%s admin_account_id=%s site=%s err=%v", userID, adminAccountID, siteName, err)
		return false
	}
	return claimed
}

// checkMultiplierChanges 对比同步前后的分组倍率，任何变化都发送通知。
// 只受系统设置全局开关 strategy.EnableMultiplierAlert 控制。
// checkMultiplierChanges 只对「已被分组映射对接」的上游分组发预警。
// mappedGroups 由 my_sites.ListMappedUpstreamGroups 提供，key 是 siteID|groupName。
//
// 之前这里拿不到映射关系（siteID 参数被 `_ = siteID` 丢弃），于是把上游站点上
// 的每一个分组变动都报了出来——包括那些从未对接、与本方定价毫无关系的分组。
// 界面上写的是「当监控的对接分组倍率发生任何变动时」，实现必须与之对齐。
func checkMultiplierChanges(ctx context.Context, svc *settings.Service, strategy settings.StrategySettings, userID, adminAccountID, siteID, siteName string, oldMetrics, newMetrics upstream.Metrics, mappedGroups map[string]struct{}, impacts groupImpactReader) {
	checkMultiplierChangesWithEvents(ctx, svc, strategy, userID, adminAccountID, siteID, siteName,
		oldMetrics, newMetrics, mappedGroups, true, nil, impacts)
}

type multiplierEventWriter interface {
	InsertMultiplierEvent(ctx context.Context, event upstream.MultiplierEvent) error
}

func checkMultiplierChangesWithEvents(ctx context.Context, svc *settings.Service, strategy settings.StrategySettings, userID, adminAccountID, siteID, siteName string, oldMetrics, newMetrics upstream.Metrics, mappedGroups map[string]struct{}, mappingAvailable bool, events multiplierEventWriter, impacts groupImpactReader) {
	if len(oldMetrics.Groups) == 0 {
		return
	}
	oldMap := make(map[string]float64, len(oldMetrics.Groups))
	for _, g := range oldMetrics.Groups {
		if g.Multiplier != nil {
			oldMap[g.ID+"|"+g.Name] = *g.Multiplier
		}
	}
	for _, g := range newMetrics.Groups {
		if g.Multiplier == nil {
			continue
		}
		key := g.ID + "|" + g.Name
		oldVal, existed := oldMap[key]
		if !existed || oldVal == *g.Multiplier {
			continue
		}
		mapped := true
		if mappingAvailable {
			_, mapped = mappedGroups[my_sites.UpstreamGroupKey(siteID, g.Name)]
		}
		notify := mapped && strategy.EnableMultiplierAlert && len(strategy.MultiplierNotifyBotIDs) > 0
		if events != nil {
			event := upstream.MultiplierEvent{
				UserID: userID, AdminAccountID: adminAccountID, SiteID: siteID, SiteName: siteName,
				GroupID: g.ID, GroupName: g.Name, PreviousMultiplier: oldVal, CurrentMultiplier: *g.Multiplier,
				Mapped: mapped, Notified: notify, ObservedAt: time.Now().UTC(),
			}
			if err := events.InsertMultiplierEvent(ctx, event); err != nil {
				log.Printf("[alert] 记录倍率事件失败 user_id=%s site=%s group=%s err=%v", userID, siteName, g.Name, err)
			}
		}
		if !mapped {
			log.Printf("[alert] 跳过未对接分组的倍率变动 site=%s group=%s old=%.4f new=%.4f",
				siteName, g.Name, oldVal, *g.Multiplier)
			continue
		}
		if !notify {
			continue
		}
		// 影响面（对接了我方哪个分组、那个分组卖多少、这条通道最近花了多少）
		// 是判断"要不要现在处理"的依据。查不到就退化成原来的干巴巴几行，
		// 绝不因为补充信息取不到就把预警本身吞掉。
		var impact my_sites.GroupImpact
		if impacts != nil {
			impact = impacts.UpstreamGroupImpact(ctx, userID, adminAccountID, siteID, g.Name, multiplierImpactDays)
		}
		msg := formatMultiplierChange(siteName, g.Name, oldVal, *g.Multiplier, strategy.MultiplierTemplate, impact)
		log.Printf("[alert] 倍率变更触发 site=%s group=%s old=%.4f new=%.4f own_groups=%d cost_resolved=%v",
			siteName, g.Name, oldVal, *g.Multiplier, len(impact.OwnGroups), impact.CostResolved)
		svc.SendFormattedToBots(ctx, userID, strategy.MultiplierNotifyBotIDs, msg, strategy.MultiplierTemplateFormat)
	}
}

const defaultBalanceTemplate = "🔴 余额预警\n🏷️ 站点：{siteName}\n💰 当前余额：¥{balance}\n⚠️ 预警阈值：¥{threshold}\n请及时检查并充值，避免服务中断。"

func formatAutomaticDisableAlert(event connection_health.AutomaticDisableEvent) string {
	accountName := strings.TrimSpace(event.AccountName)
	if accountName == "" {
		accountName = event.AccountID
	}
	groupName := strings.TrimSpace(event.GroupName)
	if groupName == "" {
		groupName = event.GroupID
	}
	recentUse := "无近期请求样本"
	if event.RecentUsageSamples > 0 {
		recentUse = fmt.Sprintf("近 1 小时有 %d 条真实请求样本", event.RecentUsageSamples)
	}
	cause := formatAutomaticDisableCause(event)
	detail := formatAutomaticDisableDetail(event)
	if len(event.Groups) > 1 {
		groups := make([]string, 0, len(event.Groups))
		for _, group := range event.Groups {
			groupName := strings.TrimSpace(group.GroupName)
			if groupName == "" {
				groupName = group.GroupID
			}
			groups = append(groups, fmt.Sprintf("  · %s：优先级 %d → **%d** ｜ 倍率 %sx ｜ 当前可用账号 %d 个",
				groupName, group.PreviousPriority, group.CurrentPriority, trimFloat(group.EffectiveMultiplier), group.ActiveAccountCount))
		}
		return fmt.Sprintf("🔴 **上游账号已自动置底**\n\n👤 **账号：** %s\n📦 **涉及分组：**\n%s\n⏱️ **使用情况：** %s\n⚠️ **原因：** %s%s\n\n该账号已被自动调度排到最后，请及时检查上游状态与健康策略。",
			accountName, strings.Join(groups, "\n"), recentUse, cause, detail)
	}
	return fmt.Sprintf("🔴 **上游账号已自动置底**\n\n📦 **分组：** %s\n👤 **账号：** %s\n📊 **倍率：** %sx\n📉 **优先级：** %d → **%d**\n✅ **分组当前可用账号：** %d 个\n⏱️ **使用情况：** %s\n⚠️ **原因：** %s%s\n\n该账号已被自动调度排到最后，请及时检查上游状态与健康策略。",
		groupName, accountName, trimFloat(event.EffectiveMultiplier), event.PreviousPriority, event.CurrentPriority, event.ActiveAccountCount, recentUse, cause, detail)
}

func formatAutomaticDisableCause(event connection_health.AutomaticDisableEvent) string {
	labels := map[string]string{
		"balance_exhausted":         "余额或额度耗尽",
		"invalid_credential":        "无效 Key 或凭据",
		"rate_limited":              "上游限流（429）",
		"network_failure":           "上游网络连接失败或超时",
		"upstream_server_error":     "上游服务错误（5xx）",
		"model_unavailable":         "模型不可用",
		"authentication_failed":     "上游认证失败",
		"invalid_response":          "上游返回异常响应",
		"upstream_runtime_limited":  "上游临时限制该账号",
		"upstream_unschedulable":    "上游标记账号不可调度",
		"health_policy_unavailable": "健康策略判定不可用",
		"health_probe_failed":       "健康探测失败",
	}
	if label := labels[event.CauseKey]; label != "" {
		return label
	}
	return event.Reason
}

func formatAutomaticDisableDetail(event connection_health.AutomaticDisableEvent) string {
	detail := strings.TrimSpace(event.CauseDetail)
	if detail == "" {
		return ""
	}
	model := strings.TrimSpace(event.CauseModelName)
	modelLine := ""
	if model != "" {
		modelLine = fmt.Sprintf("\n🤖 **触发模型：** %s", model)
	}
	return fmt.Sprintf("%s\n📨 **上游响应：** %s", modelLine, detail)
}

func formatAutomaticRecoveryAlert(event connection_health.AutomaticRecoveryEvent) string {
	accountName := strings.TrimSpace(event.AccountName)
	if accountName == "" {
		accountName = event.AccountID
	}
	groupName := strings.TrimSpace(event.GroupName)
	if groupName == "" {
		groupName = event.GroupID
	}
	modelName := strings.TrimSpace(event.ModelName)
	if modelName == "" {
		modelName = "真实模型请求"
	}
	if event.Stage == connection_health.AutomaticRecoveryStageObserving {
		return fmt.Sprintf("🟡 **上游账号恢复观察中**\n\n📦 **分组：** %s\n👤 **账号：** %s\n📊 **倍率：** %sx\n🤖 **验证模型：** %s\n✅ **探测结果：** 真实模型请求成功\n\n账号此前已自动置底，当前开始恢复观察；后续探测稳定且重新加入自动调度后，会再发送“已自动恢复”通知。", groupName, accountName, trimFloat(event.EffectiveMultiplier), modelName)
	}
	if modelName == "倍率调度恢复" {
		return fmt.Sprintf("🟢 **上游账号已自动恢复**\n\n📦 **分组：** %s\n👤 **账号：** %s\n📊 **倍率：** %sx\n📉 **优先级：** %d → **%d**\n✅ **分组当前可用账号：** %d 个\n✅ **恢复原因：** 倍率调度已恢复该账号的正常优先级，账号重新加入自动调度", groupName, accountName, trimFloat(event.EffectiveMultiplier), event.PreviousPriority, event.CurrentPriority, event.ActiveAccountCount)
	}
	return fmt.Sprintf("🟢 **上游账号已自动恢复**\n\n📦 **分组：** %s\n👤 **账号：** %s\n🤖 **验证模型：** %s\n✅ **恢复原因：** 真实模型请求成功，账号已重新加入自动调度\n\n该账号已通过健康验证并恢复可用。", groupName, accountName, modelName)
}

// defaultMultiplierTemplate 带上影响面。只报「0.055x → 0.065x」没法判断要不要动手：
// 同样涨 18%，一周跑 3 块和一周跑 3000 块的通道，紧急程度差三个数量级。
const defaultMultiplierTemplate = "🟠 倍率变更预警\n🏷️ 站点：{siteName}\n📦 上游分组：{groupName}\n📊 倍率：{oldRate}x → {newRate}x（{changeDirection} {changePercent}）\n\n🔗 我方分组：{ownGroups}\n📈 近 {days} 天该通道成本：{weeklyCost}（日均 {dailyAvgCost}）\n💸 按同等用量估算，每周成本变化：{costImpact}\n\n🔎 请确认成本变化，并检查下游定价策略。"

// defaultMultiplierMarkdownTemplate 是 markdown 渠道下的新版默认模板。
// 内容与 defaultMultiplierTemplate 一致，只是把关键数字加粗。
const defaultMultiplierMarkdownTemplate = "🟠 **倍率变更预警**\n\n🏷️ **站点：** {siteName}\n📦 **上游分组：** {groupName}\n📊 **倍率：** {oldRate}x → **{newRate}x**（{changeDirection} {changePercent}）\n\n🔗 **我方分组：** {ownGroups}\n📈 **近 {days} 天该通道成本：** {weeklyCost}（日均 {dailyAvgCost}）\n💸 **按同等用量估算，每周成本变化：** {costImpact}\n\n🔎 请确认成本变化，并检查下游定价策略。"

// multiplierImpactDays 是预警里回看的天数。7 天能覆盖一个完整的周内波动，
// 又不会把早已下线的通道算进来。
const multiplierImpactDays = 7

// builtInMultiplierTemplates 是历代内置默认模板。命中说明用户从没手改过模板，
// 可以安全升级到新版；只要用户动过一个字就不在这个集合里，会被原样保留。
var builtInMultiplierTemplates = func() map[string]struct{} {
	templates := []string{
		// 当前现役的 markdown 默认值（zh / en），也是线上库里存着的那份。
		"🟠 **倍率变更预警**\n\n🏷️ **站点：** {siteName}\n📦 **分组：** {groupName}\n📊 **倍率：** {oldRate}x → **{newRate}x**（{changeDirection}）\n\n🔎 请确认成本变化，并检查下游定价策略。",
		"🟠 **Multiplier change warning**\n\n🏷️ **Site:** {siteName}\n📦 **Group:** {groupName}\n📊 **Rate:** {oldRate}x → **{newRate}x** ({changeDirection})\n\n🔎 Review the cost change and confirm whether downstream pricing needs adjustment.",
		// 更早的纯文本版本。
		"【倍率变更】{siteName} 的 {groupName} 分组倍率已{changeDirection}：{oldRate}x -> {newRate}x。",
		"[Multiplier change] {siteName} / {groupName} {changeDirection}: {oldRate}x -> {newRate}x.",
		defaultMultiplierTemplate,
	}
	set := make(map[string]struct{}, len(templates))
	for _, template := range templates {
		set[strings.TrimSpace(template)] = struct{}{}
	}
	return set
}()

func isBuiltInMultiplierTemplate(template string) bool {
	_, ok := builtInMultiplierTemplates[strings.TrimSpace(template)]
	return ok
}

// groupImpactReader 是预警对 my_sites 的窄依赖，方便单测注入假数据。
type groupImpactReader interface {
	UpstreamGroupImpact(ctx context.Context, userID, adminAccountID, siteID, groupName string, days int) my_sites.GroupImpact
}

func formatBalanceWarning(siteName string, balance, threshold float64, customTemplate string) string {
	tpl := customTemplate
	if tpl == "" {
		tpl = defaultBalanceTemplate
	}
	r := strings.NewReplacer(
		"{siteName}", siteName,
		"{balance}", fmt.Sprintf("%.2f", balance),
		"{threshold}", fmt.Sprintf("%.2f", threshold),
	)
	return r.Replace(tpl)
}

func formatMultiplierChange(siteName, groupName string, oldRate, newRate float64, customTemplate string, impact my_sites.GroupImpact) string {
	tpl := customTemplate
	if tpl == "" || isBuiltInMultiplierTemplate(tpl) {
		// 用户从没改过模板（库里存的就是历代内置默认值）时直接升级到新版，
		// 否则他们要先进设置页手动保存一次才能看到影响面——那等于白做。
		// 真正手写过模板的人不受影响：只要有一个字不同就走自己的模板。
		tpl = defaultMultiplierMarkdownTemplate
		if customTemplate == "" {
			tpl = defaultMultiplierTemplate
		}
	}
	changeDirection := "上升"
	if newRate < oldRate {
		changeDirection = "下降"
	}
	days := impact.Days
	if days <= 0 {
		days = multiplierImpactDays
	}
	r := strings.NewReplacer(
		"{siteName}", siteName,
		"{groupName}", groupName,
		"{oldRate}", fmt.Sprintf("%.4f", oldRate),
		"{newRate}", fmt.Sprintf("%.4f", newRate),
		"{changeDirection}", changeDirection,
		"{changePercent}", formatChangePercent(oldRate, newRate),
		"{ownGroups}", formatOwnGroups(impact),
		"{days}", strconv.Itoa(days),
		"{weeklyCost}", formatImpactCost(impact),
		"{dailyAvgCost}", formatImpactDailyAvg(impact, days),
		"{costImpact}", formatCostImpact(impact, oldRate, newRate),
	)
	return r.Replace(tpl)
}

// formatOwnGroups 列出对接了这个上游分组的自有分组及其当前倍率。
func formatOwnGroups(impact my_sites.GroupImpact) string {
	if !impact.HasOwnGroups() {
		return "未找到对接的自有分组"
	}
	parts := make([]string, 0, len(impact.OwnGroups))
	for _, group := range impact.OwnGroups {
		if group.Multiplier > 0 {
			parts = append(parts, fmt.Sprintf("%s（当前 %sx）", group.Name, trimFloat(group.Multiplier)))
			continue
		}
		// 倍率取不到时如实说，不要显示成 0x——那会被读成"免费卖"。
		parts = append(parts, fmt.Sprintf("%s（倍率未知）", group.Name))
	}
	return strings.Join(parts, "、")
}

// formatImpactCost 报最近这段时间的采购成本。
// 【口径】account_cost 本身就是人民币，这里绝不做任何折算。
func formatImpactCost(impact my_sites.GroupImpact) string {
	if !impact.CostResolved {
		return unresolvedCostHint(impact)
	}
	return "¥" + fmt.Sprintf("%.2f", impact.CostCNY)
}

func formatImpactDailyAvg(impact my_sites.GroupImpact, days int) string {
	if !impact.CostResolved || days <= 0 {
		return "—"
	}
	return "¥" + fmt.Sprintf("%.2f", impact.CostCNY/float64(days))
}

// formatCostImpact 按「用量不变」估算这次变价带来的周成本变化。
//
// 这是个粗估而不是预测：用量本来就会波动，涨价之后还可能因为切换路由而变少。
// 但它回答了运营真正想知道的那个量级问题——这次变价值不值得现在处理。
func formatCostImpact(impact my_sites.GroupImpact, oldRate, newRate float64) string {
	if !impact.CostResolved {
		return unresolvedCostHint(impact)
	}
	if oldRate <= 0 {
		return "—"
	}
	delta := impact.CostCNY * (newRate/oldRate - 1)
	sign := "+"
	if delta < 0 {
		sign = "-"
		delta = -delta
	}
	return fmt.Sprintf("%s¥%.2f", sign, delta)
}

// unresolvedCostHint 把「查不到成本」的原因说清楚。
// 直接显示 ¥0.00 是最坏的选择：没绑账号和真没跑量会被读成同一件事。
func unresolvedCostHint(impact my_sites.GroupImpact) string {
	switch impact.CostUnresolvedReason {
	case my_sites.ReasonUnbound:
		return "未绑成本账号（去「调价映射」补绑后才有数）"
	case my_sites.ReasonGroupMissing:
		return "自有分组在 Sub2API 上找不到"
	case my_sites.ReasonAmbiguous:
		return "绑定归属冲突"
	case my_sites.ReasonQueryFailed:
		return "成本查询失败"
	default:
		return "无数据"
	}
}

func formatChangePercent(oldRate, newRate float64) string {
	if oldRate <= 0 {
		return "—"
	}
	percent := (newRate/oldRate - 1) * 100
	sign := "+"
	if percent < 0 {
		sign = "-"
		percent = -percent
	}
	return fmt.Sprintf("%s%.1f%%", sign, percent)
}

// trimFloat 去掉倍率末尾没用的零：0.0550 显示成 0.055。
func trimFloat(value float64) string {
	text := strconv.FormatFloat(value, 'f', 4, 64)
	text = strings.TrimRight(text, "0")
	return strings.TrimSuffix(text, ".")
}
