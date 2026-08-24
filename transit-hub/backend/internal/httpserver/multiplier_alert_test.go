package httpserver

import (
	"context"
	"testing"

	"transithub/backend/internal/modules/my_sites"
	"transithub/backend/internal/modules/settings"
	"transithub/backend/internal/modules/upstream"
)

type multiplierEventCapture struct {
	events []upstream.MultiplierEvent
}

func (c *multiplierEventCapture) InsertMultiplierEvent(_ context.Context, event upstream.MultiplierEvent) error {
	c.events = append(c.events, event)
	return nil
}

// 这组测试用 nil 的 *settings.Service 当探针：真要发通知就会走进
// sendNotificationToBots 并解引用 nil 上的字段而 panic。
// 于是「没 panic」== 没发送，「panic 了」== 尝试发送了。
// 比起为一个纯过滤逻辑去搭一整套 mock，这样更直接，也不必改动生产代码的结构。

func multiplierAlertStrategy() settings.StrategySettings {
	return settings.StrategySettings{
		EnableMultiplierAlert:  true,
		MultiplierNotifyBotIDs: []string{"bot-1"},
	}
}

func groupsWithMultiplier(id, name string, multiplier float64) upstream.Metrics {
	return upstream.Metrics{
		Groups: []upstream.GroupInfo{
			{ID: id, Name: name, Multiplier: &multiplier},
		},
	}
}

// 线上真实误报：tntapi 的 openAI plus 分组 0.1 → 0.12 触发了预警，
// 但这个上游从未被任何分组映射对接过。
func TestCheckMultiplierChangesSkipsUnmappedGroup(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("未对接的分组不应触发发送，却走到了发送逻辑：%v", recovered)
		}
	}()

	oldMetrics := groupsWithMultiplier("g-1", "openAI plus", 0.1)
	newMetrics := groupsWithMultiplier("g-1", "openAI plus", 0.12)

	checkMultiplierChanges(
		context.Background(), nil, multiplierAlertStrategy(),
		"user-1", "workspace-1", "site-tntapi", "tntapi",
		oldMetrics, newMetrics,
		map[string]struct{}{}, // 空集合 = 没有任何分组被对接
		nil,
	)
}

// 映射集合读取失败时传的是 nil，此时同样必须一条都不发 ——
// 绝不能退化回「全部上报」的旧行为。
func TestCheckMultiplierChangesSkipsWhenMappingUnavailable(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("映射不可用时应当静默跳过，却走到了发送逻辑：%v", recovered)
		}
	}()

	checkMultiplierChanges(
		context.Background(), nil, multiplierAlertStrategy(),
		"user-1", "workspace-1", "site-tntapi", "tntapi",
		groupsWithMultiplier("g-1", "openAI plus", 0.1),
		groupsWithMultiplier("g-1", "openAI plus", 0.12),
		nil, nil,
	)
}

// 反向确认过滤没有把正常预警一起挡掉：对接过的分组仍然要报。
func TestCheckMultiplierChangesAlertsMappedGroup(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("已对接的分组应当触发发送，但没有走到发送逻辑")
		}
	}()

	mapped := map[string]struct{}{
		my_sites.UpstreamGroupKey("site-tntapi", "openAI plus"): {},
	}

	checkMultiplierChanges(
		context.Background(), nil, multiplierAlertStrategy(),
		"user-1", "workspace-1", "site-tntapi", "tntapi",
		groupsWithMultiplier("g-1", "openAI plus", 0.1),
		groupsWithMultiplier("g-1", "openAI plus", 0.12),
		mapped, nil,
	)
}

// 分组名两端的空白不该导致对接关系匹配不上。
func TestUpstreamGroupKeyTrimsGroupName(t *testing.T) {
	if got, want := my_sites.UpstreamGroupKey("site-1", "  plus  "), "site-1|plus"; got != want {
		t.Fatalf("UpstreamGroupKey = %q，期望 %q", got, want)
	}
}

// 倍率没变时不发预警，这条老行为不能被过滤逻辑带坏。
func TestCheckMultiplierChangesIgnoresUnchangedMultiplier(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("倍率未变化不应触发发送：%v", recovered)
		}
	}()

	mapped := map[string]struct{}{
		my_sites.UpstreamGroupKey("site-tntapi", "openAI plus"): {},
	}

	checkMultiplierChanges(
		context.Background(), nil, multiplierAlertStrategy(),
		"user-1", "workspace-1", "site-tntapi", "tntapi",
		groupsWithMultiplier("g-1", "openAI plus", 0.1),
		groupsWithMultiplier("g-1", "openAI plus", 0.1),
		mapped, nil,
	)
}

func TestCheckMultiplierChangesStoresMappedEventAndAttemptsNotification(t *testing.T) {
	capture := new(multiplierEventCapture)
	defer func() {
		if recover() == nil {
			t.Fatal("已映射且开启预警时应尝试发送")
		}
		if len(capture.events) != 1 || !capture.events[0].Mapped || !capture.events[0].Notified {
			t.Fatalf("事件没有按已映射/已通知记录：%+v", capture.events)
		}
	}()

	checkMultiplierChangesWithEvents(
		context.Background(), nil, multiplierAlertStrategy(),
		"user-1", "workspace-1", "site-tntapi", "tntapi",
		groupsWithMultiplier("g-1", "openAI plus", 0.1),
		groupsWithMultiplier("g-1", "openAI plus", 0.12),
		map[string]struct{}{my_sites.UpstreamGroupKey("site-tntapi", "openAI plus"): {}}, true, capture, nil,
	)
}

func TestCheckMultiplierChangesStoresUnmappedEventWithoutNotification(t *testing.T) {
	capture := new(multiplierEventCapture)
	checkMultiplierChangesWithEvents(
		context.Background(), nil, multiplierAlertStrategy(),
		"user-1", "workspace-1", "site-tntapi", "tntapi",
		groupsWithMultiplier("g-1", "openAI plus", 0.1),
		groupsWithMultiplier("g-1", "openAI plus", 0.12),
		map[string]struct{}{}, true, capture, nil,
	)
	if len(capture.events) != 1 || capture.events[0].Mapped || capture.events[0].Notified {
		t.Fatalf("未映射事件记录错误：%+v", capture.events)
	}
}

func TestCheckMultiplierChangesStoresEventsWhenAlertDisabled(t *testing.T) {
	capture := new(multiplierEventCapture)
	strategy := multiplierAlertStrategy()
	strategy.EnableMultiplierAlert = false
	checkMultiplierChangesWithEvents(
		context.Background(), nil, strategy,
		"user-1", "workspace-1", "site-tntapi", "tntapi",
		groupsWithMultiplier("g-1", "mapped", 0.1),
		groupsWithMultiplier("g-1", "mapped", 0.12),
		map[string]struct{}{my_sites.UpstreamGroupKey("site-tntapi", "mapped"): {}}, true, capture, nil,
	)
	checkMultiplierChangesWithEvents(
		context.Background(), nil, strategy,
		"user-1", "workspace-1", "site-tntapi", "tntapi",
		groupsWithMultiplier("g-2", "unmapped", 0.2),
		groupsWithMultiplier("g-2", "unmapped", 0.24),
		map[string]struct{}{}, true, capture, nil,
	)
	if len(capture.events) != 2 || capture.events[0].Notified || capture.events[1].Notified {
		t.Fatalf("关闭预警时仍应记录但不标记通知：%+v", capture.events)
	}
}
