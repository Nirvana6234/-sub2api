// Package relaywire 把主从分流接进主程序的依赖注入（cmd/server/wire.go）。
//
// 它单独成包：同时依赖 master 与 repository，而 repository 又实现 master 的存储接口，
// 放进任何一方都会循环导入。
package relaywire

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/relayselect"
	"github.com/Wei-Shaw/sub2api/internal/relay/relaysettle"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/wire"
)

// ProviderSet 提供主节点的主从分流运行时。
var ProviderSet = wire.NewSet(ProvideMasterRuntime)

// ProvideMasterRuntime 创建主从分流运行时并按开关对齐一次。
//
// 开关关闭（默认）时只订阅开关变化：不开端口、不起后台任务、不写库（开发计划第 1 节门槛 3）。
// 开关打开时当场启动；之后开关的改动也当场生效（动态启停）。
func ProvideMasterRuntime(
	cfg *config.Config,
	db *sql.DB,
	settings service.SettingRepository,
	hub *service.SettingChangeHub,
	apiKeys *service.APIKeyService,
	billing *service.BillingCacheService,
	accessChanges *service.AccessChangeHub,
	users service.UserRepository,
	subscriptions *service.SubscriptionService,
	settingService *service.SettingService,
	gateway *service.OpenAIGatewayService,
	concurrency *service.ConcurrencyService,
	moderation *service.ContentModerationService,
	promptAudit *securityaudit.PromptService,
	accounts service.AccountRepository,
	groups service.GroupRepository,
	errorPassthrough *service.ErrorPassthroughService,
	ops *service.OpsService,
	proxies service.ProxyRepository,
) *master.Runtime {
	// 用户、分组、订阅作废时发布改动（平台配额在仓储层已接好，见 repository/wire.go）。
	service.AttachAccessChangeHub(accessChanges, apiKeys, billing)
	rt := master.NewRuntime(master.RuntimeDeps{
		Config:        cfg,
		Store:         repository.NewRelayNodeRepository(db),
		Settings:      settings,
		Hub:           hub,
		APIKeys:       apiKeys,
		AccessChanges: accessChanges,
		Users:         users,
		Leases:        repository.NewRelayLeaseRepository(db),
		ReservedSink:  billing,
		NewSelector: relayselect.NewFactory(relayselect.Deps{
			Config: cfg, APIKeys: apiKeys, Subscriptions: subscriptions, Settings: settingService,
			Billing: billing, Gateway: gateway, Concurrency: concurrency,
			Moderation: moderation, PromptAudit: promptAuditMode(promptAudit), Ops: ops, Users: users,
		}),
		VoucherPartitions: repository.NewRelayVoucherPartitions(db),
		Sections:          forwardingSections(errorPassthrough),
		SealedSections:    sealedSections(settings, proxies),
		NewSettler: relaysettle.NewFactory(relaysettle.Deps{
			Gateway: gateway, APIKeys: apiKeys, Accounts: accounts, Groups: groups, Subscriptions: subscriptions,
			Vouchers: relayVoucherRecorder(db),
		}),
	})
	if moderation != nil {
		// 命中过的输入名单变了（审核命中、后台删除或清空、从节点上报）当场推给各从节点的副本（设计 3.4）。
		moderation.SetHashChangeListener(func(ch service.ContentModerationHashChange) {
			rt.BroadcastFlaggedHashes(&relayv1.FlaggedHashes{Added: ch.Added, Removed: ch.Removed, Cleared: ch.Cleared})
		})
	}
	if errorPassthrough != nil {
		// 规则改了当场重新生成快照（发布器另有 30 秒一次的定时重算兜底）。
		errorPassthrough.SetChangeNotifier(func() { hub.Notify([]string{master.SectionChangedKey(master.SectionErrorPassthroughRules)}) })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rt.Init(ctx)
	return rt
}

// relayVoucherRecorder 是扣费仓储的凭证记录（入账没走到扣费事务时把凭证记成零消耗）。
func relayVoucherRecorder(db *sql.DB) service.RelayVoucherRecorder {
	r, ok := repository.NewUsageBillingRepository(nil, db).(service.RelayVoucherRecorder)
	if !ok {
		panic("relaywire: the usage billing repository does not record relay vouchers")
	}
	return r
}

// promptAuditMode 把可能为 nil 的提示词审计服务转成接口（nil 指针不能直接放进接口）。
func promptAuditMode(p *securityaudit.PromptService) interface{ EffectiveMode() securityaudit.Mode } {
	if p == nil {
		return nil
	}
	return p
}

// sealedSections 是按节点加密下发的分段（设计 6 第二类）：加密下发的配置（内容审核、联网搜索）引用的代理，
// 含代理密码。代理改了由主节点定时重新生成快照带上（30 秒）。
func sealedSections(settings service.SettingRepository, proxies service.ProxyRepository) map[string]master.SectionProvider {
	if proxies == nil {
		return nil
	}
	return map[string]master.SectionProvider{
		master.SealedSectionProxies: func(ctx context.Context) ([]byte, error) {
			values, err := settings.GetMultiple(ctx, []string{service.SettingKeyContentModerationConfig, service.SettingKeyWebSearchEmulationConfig})
			if err != nil {
				return nil, err
			}
			ids := service.SealedConfigProxyIDs(values[service.SettingKeyContentModerationConfig], values[service.SettingKeyWebSearchEmulationConfig])
			if len(ids) == 0 {
				return nil, nil
			}
			list, err := proxies.ListByIDs(ctx, ids)
			if err != nil {
				return nil, err
			}
			return json.Marshal(list)
		},
	}
}

// forwardingSections 是配置快照里 settings 表之外的转发配置分段（设计 6）。
func forwardingSections(errorPassthrough *service.ErrorPassthroughService) map[string]master.SectionProvider {
	sections := map[string]master.SectionProvider{}
	if errorPassthrough != nil {
		sections[master.SectionErrorPassthroughRules] = func(ctx context.Context) ([]byte, error) {
			rules, err := errorPassthrough.List(ctx)
			if err != nil {
				return nil, err
			}
			return json.Marshal(rules)
		}
	}
	return sections
}
