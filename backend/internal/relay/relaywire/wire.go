// Package relaywire 把主从分流接进主程序的依赖注入（cmd/server/wire.go）。
//
// 它单独成包：同时依赖 master 与 repository，而 repository 又实现 master 的存储接口，
// 放进任何一方都会循环导入。
package relaywire

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/repository"
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
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rt.Init(ctx)
	return rt
}
