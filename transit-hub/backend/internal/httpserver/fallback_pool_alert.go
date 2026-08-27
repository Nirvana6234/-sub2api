package httpserver

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"transithub/backend/internal/modules/settings"
	"transithub/backend/internal/modules/upstream"
)

const (
	fallbackPoolAlertCooldown = 3 * time.Hour
	fallbackPoolUsageLookback = 3 * time.Hour
)

func checkFallbackPoolUsageAlerts(ctx context.Context, svc *settings.Service, platform *upstream.PlatformService, strategy settings.StrategySettings, userID, adminAccountID, siteID, siteName string, session upstream.Session) {
	if svc == nil || platform == nil {
		return
	}
	if session.Platform != upstream.PlatformSub2API {
		return
	}
	if !strategy.EnableMultiplierAlert || len(strategy.MultiplierNotifyBotIDs) == 0 {
		return
	}
	now := time.Now()
	events, err := platform.FetchSub2APIFallbackPoolUsageEvents(session, now.Add(-fallbackPoolUsageLookback), now)
	if err != nil {
		log.Printf("[alert] 读取兜底池使用记录失败 user_id=%s admin_account_id=%s site=%s err=%v", userID, adminAccountID, siteName, err)
		return
	}
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		sourceGroupID := strings.TrimSpace(event.SourceGroupID)
		targetGroupID := strings.TrimSpace(event.TargetGroupID)
		if sourceGroupID == "" || targetGroupID == "" {
			continue
		}
		pairKey := sourceGroupID + "\x00" + targetGroupID
		if _, ok := seen[pairKey]; ok {
			continue
		}
		seen[pairKey] = struct{}{}

		claimed, claimErr := svc.ClaimFallbackPoolAlert(ctx, userID, adminAccountID, siteID, sourceGroupID, targetGroupID, fallbackPoolAlertCooldown)
		if claimErr != nil {
			log.Printf("[alert] 记录兜底池提醒冷却失败 user_id=%s admin_account_id=%s site=%s source_group=%s target_group=%s err=%v", userID, adminAccountID, siteName, sourceGroupID, targetGroupID, claimErr)
			continue
		}
		if !claimed {
			continue
		}
		msg := formatFallbackPoolUsageAlert(siteName, event, fallbackPoolAlertCooldown)
		log.Printf("[alert] 兜底池使用触发提醒 site=%s source_group=%s target_group=%s account=%s model=%s", siteName, fallbackGroupLabel(event.SourceGroupName, event.SourceGroupID), fallbackGroupLabel(event.TargetGroupName, event.TargetGroupID), event.AccountName, event.Model)
		svc.SendFormattedToBotsForWorkspace(ctx, userID, adminAccountID, strategy.MultiplierNotifyBotIDs, msg, strategy.MultiplierTemplateFormat)
	}
}

func formatFallbackPoolUsageAlert(siteName string, event upstream.FallbackPoolUsageEvent, cooldown time.Duration) string {
	account := strings.TrimSpace(event.AccountName)
	if account == "" {
		account = strings.TrimSpace(event.AccountID)
	}
	if account == "" {
		account = "未知账号"
	}
	model := strings.TrimSpace(event.Model)
	if model == "" {
		model = "未知模型"
	}
	requestID := strings.TrimSpace(event.RequestID)
	if requestID == "" {
		requestID = "-"
	}
	return fmt.Sprintf("⚠️ 兜底分组已被使用\n🏷️ 站点：%s\n➡️ 原分组：%s\n🛟 兜底池：%s\n👤 账号：%s\n🤖 模型：%s\n🕒 时间：%s\n💵 实际扣费：%.6f\n🧾 请求：%s\n\n同一站点、同一原分组→兜底池组合在 %s 内只提醒一次。",
		fallbackNonEmpty(siteName, "未知站点"),
		fallbackGroupLabel(event.SourceGroupName, event.SourceGroupID),
		fallbackGroupLabel(event.TargetGroupName, event.TargetGroupID),
		account,
		model,
		formatFallbackEventTime(event.CreatedAt),
		event.ActualCost,
		requestID,
		formatFallbackCooldown(cooldown),
	)
}

func fallbackGroupLabel(name, id string) string {
	name = strings.TrimSpace(name)
	id = strings.TrimSpace(id)
	if name == "" {
		return fallbackNonEmpty(id, "未知分组")
	}
	if id == "" {
		return name
	}
	return fmt.Sprintf("%s（%s）", name, id)
}

func fallbackNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func formatFallbackEventTime(t time.Time) string {
	if t.IsZero() {
		return "未知"
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		t = t.In(loc)
	}
	return t.Format("2006-01-02 15:04:05")
}

func formatFallbackCooldown(cooldown time.Duration) string {
	if cooldown%time.Hour == 0 {
		return fmt.Sprintf("%d 小时", int(cooldown/time.Hour))
	}
	return cooldown.Round(time.Minute).String()
}
