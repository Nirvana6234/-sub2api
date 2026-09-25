package service

import "time"

// expiredNano 是"已经过期"的时间戳。不用 0：有的读取把 0 当成"永不过期"（测试钩子）。
const expiredNano int64 = 1

// InvalidateAll 让所有由系统设置派生的进程内缓存立即失效，下一次读取重新从 SettingRepository 取。
//
// 主从分流的从节点用它实现版本栅栏（docs/MASTER_RELAY_NODES.md 第 6 节）：从节点的
// SettingRepository 是主节点推送的配置快照，换快照后必须先清掉这些缓存再放行请求，
// 否则最多按缓存 TTL（几秒）用旧配置转发。
//
// 新增设置缓存时要在这里一起清掉；setting_cache_invalidate_test.go 会检查。
func (s *SettingService) InvalidateAll() {
	if s != nil {
		s.invalidateHotSettings()
		s.antigravityUAVersionCache.Store(&cachedAntigravityUserAgentVersion{expiresAt: expiredNano})
		s.openAICodexUACache.Store(&cachedOpenAICodexUserAgent{expiresAt: expiredNano})
		s.openAICodexVersionCache.Store(&cachedOpenAICodexClientVersion{expiresAt: expiredNano})
		s.codexRestrictionPolicyCache.Store(&cachedCodexRestrictionPolicy{expiresAt: expiredNano})
		s.cyberSessionBlockRuntimeCache.Store(&cachedCyberSessionBlockRuntime{expiresAt: expiredNano})
		s.panelRateLimitCache.Store(&cachedPanelRateLimitSettings{expiresAt: expiredNano})
		s.openAIQuotaAutoPauseSettingsCache.Store(&cachedOpenAIQuotaAutoPauseSettings{expiresAt: expiredNano})
		s.openAIAPIKeyHealthBreakerCache.Store(&cachedOpenAIAPIKeyHealthBreakerSettings{expiresAt: time.Unix(0, expiredNano)})
		s.globalBlacklistCache.Store(&cachedGlobalBlacklist{ExpiresAt: time.Unix(0, expiredNano)})
	}
	versionBoundsCache.Store(&cachedVersionBounds{expiresAt: expiredNano})
	backendModeCache.Store(&cachedBackendMode{expiresAt: expiredNano})
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{expiresAt: expiredNano})
	accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{expiresAt: expiredNano})
	guestTrialConfigCache.Store(&cachedGuestTrialConfig{expiresAt: expiredNano})
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{expiresAt: expiredNano})
	webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{expiresAt: expiredNano})
}
