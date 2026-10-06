package nodegw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/relay/accountcodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/identity"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodestore"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 从节点后台循环的间隔（开发计划 WP9：续期远小于"10 分钟 − 30 秒"）。
const (
	renewInterval  = time.Minute
	idleReturnTime = 10 * time.Minute
	shutdownGrace  = 30 * time.Second
)

// RunOptions 是从节点运行时从进程入口拿到的东西。
type RunOptions struct {
	ProgramVersion string
	// HTTPUpstream 是转发用的上游 HTTP（repository.NewHTTPUpstream：连接池、代理、TLS 指纹）。
	HTTPUpstream service.HTTPUpstream
}

// Run 运行从节点直到 ctx 结束（NODE_ROLE=relay）：不连数据库和 Redis，只经 TLS 双向认证连主节点。
// 启动顺序：身份与注册（等管理员激活）→ 配置快照 → 核对租约 → 事件流、扣费发送、额度续期 → 对外服务。
func Run(ctx context.Context, cfg *config.Config, opts RunOptions) error {
	rc := cfg.Relay
	if strings.TrimSpace(rc.NodeMasterAddr) == "" || len(rc.NodeRootFingerprints) == 0 {
		return errors.New("relay node: relay.node_master_addr and relay.node_root_fingerprints are required")
	}
	dataDir := nodeDataDir(rc)
	idDir := filepath.Join(dataDir, "identity")
	id, err := identity.Load(idDir)
	if err != nil {
		return fmt.Errorf("relay node identity: %w", err)
	}
	pins, err := identity.LoadRootPins(idDir, rc.NodeRootFingerprints)
	if err != nil {
		return err
	}
	client, err := transport.NewClient(transport.ClientOptions{
		Address: rc.NodeMasterAddr,
		TLS:     transport.ClientTLSOptions{PinnedRootFingerprints: pins.Pinned, Certificate: id.TLSCertificate},
	})
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	slog.Info("relay node starting", "fingerprint", id.Fingerprint(), "master", rc.NodeMasterAddr)
	host, _ := os.Hostname()
	enroller := identity.NewEnroller(id, client, identity.EnrollerOptions{
		Hostname: host, ProgramVersion: opts.ProgramVersion, DisplayName: rc.NodeDisplayName, Pins: pins,
		OnStatus: func(s relayv1.NodeStatus) {
			slog.Info("relay node status", "status", s.String(), "fingerprint", id.Fingerprint())
		},
	})
	if err := enroller.EnsureCertificate(ctx); err != nil {
		return fmt.Errorf("relay node enrollment: %w", err)
	}
	go enroller.RunRenewal(ctx, func(err error) { slog.Warn("relay certificate renewal failed", "error", err) })
	slog.Info("relay node certificate ready", "node_id", id.NodeID())

	cache := node.NewConfigCache()
	// 加密下发的部分（审核、联网搜索的配置和代理）用本节点的加密私钥解开，只在内存（设计 6 第二类）。
	cache.SetOpener(id.OpenSealed)
	settings := service.NewSettingService(cache, cfg)
	errorPassthrough := service.NewStaticErrorPassthroughService(nil)
	tlsProfiles := service.NewStaticTLSFingerprintProfileService(nil)
	// moderation 在下面组装（要用主从连接）；换快照时它还没有就跳过。
	var moderation *Moderation
	var webSearchReady bool
	cache.OnSwap(func(*relayv1.ConfigSnapshot) {
		settings.InvalidateAll()
		if webSearchReady {
			settings.RebuildWebSearchManager(context.Background())
		}
		if moderation != nil {
			moderation.Service.InvalidateRuntimeSnapshot()
		}
		applyErrorPassthroughRules(cache, errorPassthrough)
		applyTLSFingerprintProfiles(cache, tlsProfiles)
		if changed, err := pins.Update(cache.RootFingerprints()); err != nil {
			slog.Warn("relay root fingerprints could not be saved", "error", err)
		} else if changed {
			slog.Info("relay root fingerprints updated", "fingerprints", pins.Pinned())
		}
	})
	syncer := node.NewConfigSyncer(cache, client)
	if err := retry(ctx, "config sync", syncer.Sync); err != nil {
		return err
	}
	// 联网搜索在从节点执行（设计 3.3）；配额本机计数、定期汇总（下面 Run）。
	webSearchQuota := node.NewWebSearchQuota(client)
	SetupWebSearch(ctx, settings, cache, webSearchQuota)
	webSearchReady = true

	outbox := node.NewEventOutbox(0)
	selectClient := node.NewSelectClient(client, outbox)
	// 心跳测得的主从时钟偏差：额度租约的停用时间点按它再提前；超过上限时拒绝服务（设计第 19 节）。
	var heartbeater *node.Heartbeater
	clockSkew := func() time.Duration {
		if heartbeater == nil {
			return 0
		}
		return heartbeater.Skew()
	}
	quota := node.NewLocalQuota(node.SelectionRefiller{Client: selectClient}, time.Now, clockSkew)
	quotaSync := node.NewQuotaSync(quota, client)
	// 重启后本机没有租约：上报空列表，主节点关掉这台之前的租约（设计 4.2）。之后才发选号。
	if err := retry(ctx, "lease report", quotaSync.Report); err != nil {
		return err
	}

	wal, err := node.OpenUsageWAL(filepath.Join(dataDir, "usage"), node.UsageWALOptions{})
	if err != nil {
		return fmt.Errorf("relay usage queue: %w", err)
	}
	defer func() { _ = wal.Close() }()
	// 本机的日志和记录（审核记录等，设计第 12 节），与扣费队列同在数据目录下。
	records, err := nodestore.Open(filepath.Join(dataDir, "records"), nodestore.Options{})
	if err != nil {
		return fmt.Errorf("relay node records: %w", err)
	}
	defer func() { _ = records.Close() }()
	// 本机日志和记录（设计第 12 节）：程序日志、请求错误日志写进本机存储，后台查询由主节点转来执行。
	stats := NewStats()
	logSink := NewLogSink(records, stats)
	logger.SetSink(logSink)
	defer func() {
		logger.SetSink(nil)
		logSink.Close()
	}()
	var d *Dispatcher
	sender := node.NewUsageSender(wal, node.NewBillingClient(client), node.UsageSenderOptions{
		OnResult: func(rec *relayv1.UsageRecord, res *relayv1.UsageRecordResult) { d.OnUsageResult(rec, res) },
	})
	deps := Deps{
		NodeID: id.NodeID, Select: selectClient, Quota: quota, Secrets: accountcodec.NewSecretCache(),
		Open: id.OpenSealed, WAL: wal, Kick: sender.Kick, EnsureConfig: syncer.EnsureVersion,
		// 主节点重启（纪元变化）：作废推送可能漏了，Key 负缓存清空，再核对租约。
		AfterEpochChange: func(ctx context.Context) error { d.ClearKeyCache(); return quotaSync.Report(ctx) },
		InvalidAuth:      service.NewInvalidAuthAbuseGuard(cfg),
	}
	if u := strings.TrimSpace(rc.NodeMasterURL); u != "" {
		masterURL, err := url.Parse(u)
		if err != nil {
			return fmt.Errorf("relay.node_master_url: %w", err)
		}
		deps.HandOff = NewHandOff(masterURL, nil, cache.HandoffSigner(id.NodeID, time.Now))
	}
	d = NewDispatcher(deps)

	revocations := sign.NewRevocationList()
	runCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()
	moderation = NewModeration(runCtx, cache, records, client)
	go webSearchQuota.Run(runCtx)
	logQueries := NewLogQueryExecutor(records, moderation.Records)
	go node.RunEvents(runCtx, client, syncer, node.EventHandlers{
		Outbox: outbox,
		// 后台按节点查日志和记录（设计 12.3）：本机执行，结果经事件流回主节点。
		OnLogQuery: func(q *relayv1.LogQuery) {
			outbox.Enqueue(&relayv1.NodeEnvelope{Body: &relayv1.NodeEnvelope_LogResult{LogResult: logQueries.Execute(runCtx, q)}})
		},
		OnFlaggedHashes: moderation.Hashes.Apply,
		// 主节点的缓存作废推送（新建 Key、Key 改动）：清 Key 负缓存。
		OnInvalidation: d.OnInvalidation,
		// 中转票据的吊销表（设计 8.1）：只用来提前拒绝，以主节点复查为准。
		OnTicketRevocations: func(m *relayv1.TicketRevocations) { revocations.Apply(m, time.Now()) },
		OnConnected: func(ctx context.Context) {
			// 事件流（重新）连上：断开期间的作废推送收不到，Key 负缓存清空。
			d.ClearKeyCache()
			moderation.Hashes.RunResyncOnConnect(ctx)
		},
		OnQuotaRecall: func(rc *relayv1.QuotaRecall) {
			if err := quotaSync.HandleRecall(runCtx, rc); err != nil {
				slog.Warn("relay quota recall failed", "error", err)
			}
		},
	})
	go sender.Run(runCtx)
	// 心跳（设计 11.4）：每 5 秒一次，带本机负载与计数。
	heartbeater = node.NewHeartbeater(client, func() *relayv1.HeartbeatRequest {
		req := stats.Snapshot(runCtx, dataDir)
		req.ProgramVersion, req.ConfigVersion = opts.ProgramVersion, cache.Version()
		req.ReservedTotalMicros, _ = quota.ReservedTotal()
		appended, pending, oldest := wal.Stats()
		req.UsageRecordsEnqueuedTotal, req.BillingBacklog = appended, int32(pending)
		if !oldest.IsZero() {
			req.BillingOldestAtUnixMs = oldest.UnixMilli()
		}
		req.InflightSelectionIds = d.InflightSelections()
		req.LogBacklog, req.LogDropped = int32(logSink.Backlog()), logSink.Dropped()
		return req
	}, time.Duration(cache.GeneralHeartbeatSeconds())*time.Second)
	go heartbeater.Run(runCtx)
	go every(runCtx, renewInterval, func() {
		if err := quotaSync.Renew(runCtx); err != nil {
			slog.Warn("relay lease renewal failed", "error", err)
		}
		if err := quotaSync.ReleaseIdle(runCtx, idleReturnTime); err != nil {
			slog.Warn("relay idle quota return failed", "error", err)
		}
	})

	if strings.EqualFold(cfg.Server.Mode, gin.ReleaseMode) {
		gin.SetMode(gin.ReleaseMode)
	}
	decider, reporter := node.NewRemoteUpstreamErrorDecider(client), node.NewRemoteAccountReporter(outbox)
	gatewayDeps := GatewayDeps{
		Config: cfg, Settings: settings, HTTPUpstream: opts.HTTPUpstream, Dispatcher: d,
		Decider:          decider,
		Reporter:         reporter,
		ErrorPassthrough: errorPassthrough,
		TLSProfiles:      tlsProfiles,
		Moderation:       moderation,
		Ops:              service.NewOpsService(NewNodeOpsRepository(records, stats), cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil),
	}
	h := NewOpenAIHandler(gatewayDeps)
	gh := NewAnthropicHandler(gatewayDeps, AnthropicDeps{
		AccountState: node.NewRemoteAccountState(decider, reporter), TempUnschedulable: reporter.TempUnschedulable, MaskedSession: reporter.MaskedSession, Reporter: reporter,
	})
	r := NewEngine()
	r.GET("/health", func(c *gin.Context) {
		if err := wal.Healthy(); err != nil || !cache.Ready() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "node_id": id.NodeID()})
	})
	r.Use(stats.Middleware())
	// 请求错误日志写本机（设计 12.2）；成功的请求不产生任何日志流量。
	r.Use(handler.OpsErrorLoggerMiddleware(gatewayDeps.Ops))
	r.Use(func(c *gin.Context) {
		// 主从时钟偏差超过上限时不接新请求（设计第 19 节）：扣费凭证和额度到期都按时间判断。
		if !heartbeater.ClockHealthy() {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "Service temporarily unavailable"}})
			return
		}
		c.Next()
	})
	r.Use(func(c *gin.Context) {
		// 扣费队列写不进去时不接新请求（设计 3.1）：否则转发了记不上账。
		if err := wal.Healthy(); err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "Service temporarily unavailable"}})
			return
		}
		c.Next()
	})
	RegisterRoutes(r, h, d, cfg, gh, WithAsyncImages(handler.NewAsyncImageHandlerWithTasks(NewRemoteImageTasks(client), h)),
		WithPaw(NewPawNode(&sign.TicketVerifier{Keys: cache.TicketKeys, NodeID: id.NodeID, Revocations: revocations})))

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ConnState:         stats.ConnState,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("relay node serving", "addr", srv.Addr, "node_id", id.NodeID())

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	// 关闭：不接新请求、等进行中的请求，再把扣费队列发完（有超时，没发完的留在队列里下次启动重发）。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("relay node http shutdown", "error", err)
	}
	for shutdownCtx.Err() == nil && wal.Len() > 0 {
		if n, err := sender.Flush(shutdownCtx); err != nil || n == 0 {
			break
		}
	}
	slog.Info("relay node stopped", "pending_usage", wal.Len())
	return nil
}

// nodeDataDir 返回从节点数据目录：显式配置的，否则 <DATA_DIR 或 ./data>/relay-node。
func nodeDataDir(c config.RelayConfig) string {
	if dir := strings.TrimSpace(c.NodeDataDir); dir != "" {
		return dir
	}
	base := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if base == "" {
		base = "./data"
	}
	return filepath.Join(base, "relay-node")
}

// retry 按退避重试直到成功或 ctx 结束。
func retry(ctx context.Context, what string, fn func(context.Context) error) error {
	backoff := transport.DefaultBackoff()
	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		wait := backoff.Next()
		slog.Warn("relay node "+what+" failed, retrying", "error", err, "wait", wait)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func every(ctx context.Context, d time.Duration, fn func()) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// applyTLSFingerprintProfiles 用配置快照里的 TLS 指纹模板替换本地模板；分段缺失时清空，解不开时保留原来的并报警。
func applyTLSFingerprintProfiles(cache *node.ConfigCache, svc *service.TLSFingerprintProfileService) {
	raw, ok := cache.Section(master.SectionTLSFingerprintProfiles)
	if !ok {
		svc.ReplaceProfiles(nil)
		return
	}
	var profiles []*model.TLSFingerprintProfile
	if err := json.Unmarshal(raw, &profiles); err != nil {
		slog.Error("relay TLS fingerprint profiles in the config snapshot are malformed; keeping the previous profiles", "error", err)
		return
	}
	svc.ReplaceProfiles(profiles)
}

// applyErrorPassthroughRules 用配置快照里的错误透传规则替换本地规则；分段缺失或解不开时保留原来的并报警。
func applyErrorPassthroughRules(cache *node.ConfigCache, svc *service.ErrorPassthroughService) {
	raw, ok := cache.Section(master.SectionErrorPassthroughRules)
	if !ok {
		svc.ReplaceRules(nil)
		return
	}
	var rules []*model.ErrorPassthroughRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		slog.Error("relay error passthrough rules in the config snapshot are malformed; keeping the previous rules", "error", err)
		return
	}
	svc.ReplaceRules(rules)
}
