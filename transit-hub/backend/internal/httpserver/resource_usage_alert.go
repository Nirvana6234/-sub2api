package httpserver

import (
	"context"
	"fmt"
	"log"
	"strings"

	"transithub/backend/internal/modules/settings"
	"transithub/backend/internal/modules/upstream"
)

// checkResourceUsageAlert runs from the normal successful-sync callback, so it
// follows the workspace's configured refresh interval and does not add a second
// polling schedule. Missing metrics leave the previous alert state untouched.
func checkResourceUsageAlert(ctx context.Context, svc *settings.Service, platform *upstream.PlatformService, strategy settings.StrategySettings, userID, adminAccountID, siteID, siteName string, session upstream.Session) {
	if svc == nil || platform == nil || session.Platform != upstream.PlatformSub2API {
		return
	}
	if !strategy.EnableResourceUsageAlert || len(strategy.ResourceUsageNotifyBotIDs) == 0 {
		return
	}
	usage, err := platform.FetchSub2APIResourceUsage(session)
	if err != nil {
		log.Printf("[alert] 读取资源占用失败 user_id=%s admin_account_id=%s site=%s err=%v", userID, adminAccountID, siteName, err)
		return
	}
	if usage.CPUUsagePercent == nil || usage.MemoryUsagePercent == nil {
		log.Printf("[alert] 资源占用指标不完整 user_id=%s admin_account_id=%s site=%s", userID, adminAccountID, siteName)
		return
	}

	cpuHigh := *usage.CPUUsagePercent >= strategy.ResourceUsageCPUThreshold
	memoryHigh := *usage.MemoryUsagePercent >= strategy.ResourceUsageMemoryThreshold
	claimed, err := svc.ClaimResourceUsageAlert(ctx, userID, adminAccountID, siteID, cpuHigh, memoryHigh)
	if err != nil {
		log.Printf("[alert] 更新资源占用预警状态失败 user_id=%s admin_account_id=%s site=%s err=%v", userID, adminAccountID, siteName, err)
		return
	}
	if !claimed {
		return
	}
	message := formatResourceUsageAlert(siteName, *usage.CPUUsagePercent, strategy.ResourceUsageCPUThreshold, *usage.MemoryUsagePercent, strategy.ResourceUsageMemoryThreshold, strategy.ResourceUsageTemplate)
	log.Printf("[alert] 资源占用预警触发 site=%s cpu=%.1f%% memory=%.1f%%", siteName, *usage.CPUUsagePercent, *usage.MemoryUsagePercent)
	svc.SendFormattedToBotsForWorkspace(ctx, userID, adminAccountID, strategy.ResourceUsageNotifyBotIDs, message, strategy.ResourceUsageTemplateFormat)
}

func formatResourceUsageAlert(siteName string, cpu, cpuThreshold, memory, memoryThreshold float64, customTemplate string) string {
	template := strings.TrimSpace(customTemplate)
	if template == "" {
		template = "🔴 Sub2API 资源占用预警\n🏷️ 站点：{siteName}\n🧠 CPU：{cpu}%（阈值 {cpuThreshold}%）\n💾 内存：{memory}%（阈值 {memoryThreshold}%）"
	}
	return strings.NewReplacer(
		"{siteName}", resourceUsageLabel(siteName, "未知站点"),
		"{cpu}", fmt.Sprintf("%.1f", cpu),
		"{cpuThreshold}", fmt.Sprintf("%.1f", cpuThreshold),
		"{memory}", fmt.Sprintf("%.1f", memory),
		"{memoryThreshold}", fmt.Sprintf("%.1f", memoryThreshold),
	).Replace(template)
}

func resourceUsageLabel(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
