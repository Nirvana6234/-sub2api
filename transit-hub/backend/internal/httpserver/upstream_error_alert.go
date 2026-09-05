package httpserver

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"transithub/backend/internal/modules/settings"
	"transithub/backend/internal/modules/upstream"
)

const (
	upstreamErrorAlertCooldown = 3 * time.Hour
	upstreamErrorAlertLookback = 3 * time.Hour
	upstreamErrorAlertInterval = time.Minute
)

type upstreamErrorAlertSettings interface {
	ListStrategyOwners(ctx context.Context) ([]settings.StrategyOwner, error)
	ClaimUpstreamErrorAlert(ctx context.Context, userID, adminAccountID, groupKey string, cooldown time.Duration) (bool, error)
	SendFormattedToFeishuBotsForWorkspace(ctx context.Context, userID, adminAccountID string, botIDs []string, message string, format settings.NotificationTemplateFormat)
}

type upstreamErrorAlertSessions interface {
	RequireSession(ctx context.Context, userID, adminAccountID string) (upstream.Session, error)
}

type upstreamErrorAlertFetcher interface {
	FetchSub2APIUpstreamErrorEvents(session upstream.Session, since, now time.Time) ([]upstream.UpstreamErrorEvent, error)
}

type upstreamErrorAlertScheduler struct {
	settings upstreamErrorAlertSettings
	sessions upstreamErrorAlertSessions
	fetcher  upstreamErrorAlertFetcher
}

func newUpstreamErrorAlertScheduler(settingsService upstreamErrorAlertSettings, sessions upstreamErrorAlertSessions, fetcher upstreamErrorAlertFetcher) *upstreamErrorAlertScheduler {
	return &upstreamErrorAlertScheduler{settings: settingsService, sessions: sessions, fetcher: fetcher}
}

func (s *upstreamErrorAlertScheduler) Start(ctx context.Context) {
	if s == nil || s.settings == nil || s.sessions == nil || s.fetcher == nil {
		return
	}
	go func() {
		s.tickSafely(ctx)
		ticker := time.NewTicker(upstreamErrorAlertInterval)
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

func (s *upstreamErrorAlertScheduler) tickSafely(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[upstream-error-alert] tick panic recovered: %v", recovered)
		}
	}()
	s.tick(ctx, time.Now())
}

func (s *upstreamErrorAlertScheduler) tick(ctx context.Context, now time.Time) {
	owners, err := s.settings.ListStrategyOwners(ctx)
	if err != nil {
		log.Printf("[upstream-error-alert] 读取通知设置失败 err=%v", err)
		return
	}
	for _, owner := range owners {
		strategy := owner.Settings
		if !strategy.EnableUpstreamErrorAlert || len(strategy.UpstreamErrorNotifyBotIDs) == 0 {
			continue
		}
		session, sessionErr := s.sessions.RequireSession(ctx, owner.UserID, owner.AdminAccountID)
		if sessionErr != nil || session.Platform != upstream.PlatformSub2API {
			continue
		}
		events, fetchErr := s.fetcher.FetchSub2APIUpstreamErrorEvents(session, now.Add(-upstreamErrorAlertLookback), now)
		if fetchErr != nil {
			log.Printf("[upstream-error-alert] 读取请求错误失败 user_id=%s admin_account_id=%s err=%v", owner.UserID, owner.AdminAccountID, fetchErr)
			continue
		}
		for _, group := range groupUpstreamErrorEvents(events) {
			claimed, claimErr := s.settings.ClaimUpstreamErrorAlert(ctx, owner.UserID, owner.AdminAccountID, group.key, upstreamErrorAlertCooldown)
			if claimErr != nil {
				log.Printf("[upstream-error-alert] 记录冷却失败 user_id=%s admin_account_id=%s group=%s err=%v", owner.UserID, owner.AdminAccountID, group.name, claimErr)
				continue
			}
			if !claimed {
				continue
			}
			message := formatUpstreamErrorAlert(group, upstreamErrorAlertCooldown)
			s.settings.SendFormattedToFeishuBotsForWorkspace(ctx, owner.UserID, owner.AdminAccountID,
				strategy.UpstreamErrorNotifyBotIDs, message, settings.NotificationTemplateFormatMarkdown)
		}
	}
}

type upstreamErrorAlertGroup struct {
	key        string
	name       string
	count      int
	statuses   map[int]struct{}
	latest     time.Time
	lastModel  string
	lastDetail string
}

func groupUpstreamErrorEvents(events []upstream.UpstreamErrorEvent) []upstreamErrorAlertGroup {
	grouped := make(map[string]*upstreamErrorAlertGroup)
	for _, event := range events {
		name := strings.TrimSpace(event.GroupName)
		id := strings.TrimSpace(event.GroupID)
		if name == "" && id == "" {
			continue
		}
		key := id
		if key == "" {
			key = "name:" + strings.ToLower(name)
		}
		group := grouped[key]
		if group == nil {
			group = &upstreamErrorAlertGroup{key: key, name: upstreamErrorGroupLabel(name, id), statuses: make(map[int]struct{})}
			grouped[key] = group
		}
		group.count++
		group.statuses[event.StatusCode] = struct{}{}
		if group.latest.IsZero() || event.CreatedAt.After(group.latest) {
			group.latest = event.CreatedAt
			group.lastModel = strings.TrimSpace(event.Model)
			group.lastDetail = strings.TrimSpace(event.Message)
		}
	}
	result := make([]upstreamErrorAlertGroup, 0, len(grouped))
	for _, group := range grouped {
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result
}

func formatUpstreamErrorAlert(group upstreamErrorAlertGroup, cooldown time.Duration) string {
	statusValues := make([]int, 0, len(group.statuses))
	for status := range group.statuses {
		statusValues = append(statusValues, status)
	}
	sort.Ints(statusValues)
	statusLabels := make([]string, 0, len(statusValues))
	for _, status := range statusValues {
		statusLabels = append(statusLabels, fmt.Sprintf("%d", status))
	}
	model := fallbackNonEmpty(group.lastModel, "未知")
	detail := truncateAlertText(fallbackNonEmpty(group.lastDetail, "无详情"), 180)
	return fmt.Sprintf("🔴 **请求错误告警**\n\n📦 **分组：** %s\n🔢 **状态码：** %s\n🤖 **最近模型：** %s\n🧾 **最近错误：** %s\n📈 **近 3 小时次数：** %d\n🕒 **最近发生：** %s\n\n同一分组 %s 内只提醒一次。",
		group.name, strings.Join(statusLabels, " / "), model, detail, group.count,
		formatFallbackEventTime(group.latest), formatFallbackCooldown(cooldown))
}

func upstreamErrorGroupLabel(name, id string) string {
	if strings.TrimSpace(name) == "" {
		return strings.TrimSpace(id)
	}
	if strings.TrimSpace(id) == "" {
		return strings.TrimSpace(name)
	}
	return fmt.Sprintf("%s（%s）", strings.TrimSpace(name), strings.TrimSpace(id))
}

func truncateAlertText(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}
