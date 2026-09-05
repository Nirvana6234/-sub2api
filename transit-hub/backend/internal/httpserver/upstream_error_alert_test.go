package httpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"transithub/backend/internal/modules/settings"
	"transithub/backend/internal/modules/upstream"
)

type upstreamErrorAlertSettingsFake struct {
	owners   []settings.StrategyOwner
	claimed  bool
	claimKey string
	messages []string
}

func (f *upstreamErrorAlertSettingsFake) ListStrategyOwners(context.Context) ([]settings.StrategyOwner, error) {
	return f.owners, nil
}
func (f *upstreamErrorAlertSettingsFake) ClaimUpstreamErrorAlert(_ context.Context, _, _, groupKey string, cooldown time.Duration) (bool, error) {
	f.claimKey = groupKey
	if cooldown != 3*time.Hour {
		panic("unexpected cooldown")
	}
	return f.claimed, nil
}
func (f *upstreamErrorAlertSettingsFake) SendFormattedToFeishuBotsForWorkspace(_ context.Context, _, _ string, _ []string, message string, format settings.NotificationTemplateFormat) {
	if format != settings.NotificationTemplateFormatMarkdown {
		panic("unexpected format")
	}
	f.messages = append(f.messages, message)
}

type upstreamErrorAlertSessionsFake struct{ calls int }

func (f *upstreamErrorAlertSessionsFake) RequireSession(context.Context, string, string) (upstream.Session, error) {
	f.calls++
	return upstream.Session{Platform: upstream.PlatformSub2API, AdminAPIKey: "key"}, nil
}

type upstreamErrorAlertFetcherFake struct {
	calls  int
	events []upstream.UpstreamErrorEvent
}

func (f *upstreamErrorAlertFetcherFake) FetchSub2APIUpstreamErrorEvents(upstream.Session, time.Time, time.Time) ([]upstream.UpstreamErrorEvent, error) {
	f.calls++
	return f.events, nil
}

func TestUpstreamErrorAlertTickGroupsAndHonorsCooldown(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	settingsFake := &upstreamErrorAlertSettingsFake{claimed: true, owners: []settings.StrategyOwner{{
		UserID: "user", AdminAccountID: "workspace", Settings: settings.StrategySettings{
			EnableUpstreamErrorAlert: true, UpstreamErrorNotifyBotIDs: []string{"feishu-1"},
		},
	}}}
	sessions := &upstreamErrorAlertSessionsFake{}
	fetcher := &upstreamErrorAlertFetcherFake{events: []upstream.UpstreamErrorEvent{
		{GroupID: "9", GroupName: "plus-free", StatusCode: 503, Message: "Service temporarily unavailable", Model: "gpt-5.5", CreatedAt: now.Add(-2 * time.Minute)},
		{GroupID: "9", GroupName: "plus-free", StatusCode: 502, Message: "Upstream request failed", Model: "gpt-5.6-sol", CreatedAt: now.Add(-time.Minute)},
	}}
	scheduler := newUpstreamErrorAlertScheduler(settingsFake, sessions, fetcher)
	scheduler.tick(context.Background(), now)

	if settingsFake.claimKey != "9" || len(settingsFake.messages) != 1 {
		t.Fatalf("expected one group notification, key=%q messages=%d", settingsFake.claimKey, len(settingsFake.messages))
	}
	message := settingsFake.messages[0]
	for _, want := range []string{"plus-free（9）", "502 / 503", "近 3 小时次数：** 2", "gpt-5.6-sol"} {
		if !strings.Contains(message, want) {
			t.Fatalf("notification missing %q: %s", want, message)
		}
	}

	settingsFake.claimed = false
	scheduler.tick(context.Background(), now.Add(time.Minute))
	if len(settingsFake.messages) != 1 {
		t.Fatalf("cooldown should suppress duplicate, got %d messages", len(settingsFake.messages))
	}
}

func TestUpstreamErrorAlertTickDisabledDoesNotFetch(t *testing.T) {
	settingsFake := &upstreamErrorAlertSettingsFake{owners: []settings.StrategyOwner{{Settings: settings.StrategySettings{EnableUpstreamErrorAlert: false}}}}
	fetcher := &upstreamErrorAlertFetcherFake{}
	newUpstreamErrorAlertScheduler(settingsFake, &upstreamErrorAlertSessionsFake{}, fetcher).tick(context.Background(), time.Now())
	if fetcher.calls != 0 {
		t.Fatalf("disabled alert fetched upstream %d times", fetcher.calls)
	}
}
