package httpserver

import (
	"strings"
	"testing"

	"transithub/backend/internal/modules/connection_health"
)

func TestFormatAutomaticDisableAlertIncludesEffectiveMultiplier(t *testing.T) {
	message := formatAutomaticDisableAlert(connection_health.AutomaticDisableEvent{
		GroupName: "plus", AccountName: "sun_mcgrox_180_plus",
		EffectiveMultiplier: 0.055, PreviousPriority: 1, CurrentPriority: 10000,
		ActiveAccountCount: 2, Reason: "upstream runtime limited",
	})
	if !strings.Contains(message, "📊 **倍率：** 0.055x") {
		t.Fatalf("notification missing effective multiplier: %s", message)
	}
}

func TestFormatAutomaticDisableAlertCombinesMultipleGroups(t *testing.T) {
	message := formatAutomaticDisableAlert(connection_health.AutomaticDisableEvent{
		AccountName: "shared-account", RecentUsageSamples: 4, Reason: "upstream runtime limited",
		Groups: []connection_health.AutomaticDisableGroup{
			{GroupName: "plus", PreviousPriority: 1, CurrentPriority: 10000, EffectiveMultiplier: 0.055, ActiveAccountCount: 2},
			{GroupName: "plus-专线", PreviousPriority: 20, CurrentPriority: 10000, EffectiveMultiplier: 0.065, ActiveAccountCount: 1},
		},
	})
	for _, want := range []string{
		"👤 **账号：** shared-account", "📦 **涉及分组：**", "plus：优先级 1 → **10000** ｜ 倍率 0.055x ｜ 当前可用账号 2 个",
		"plus-专线：优先级 20 → **10000** ｜ 倍率 0.065x ｜ 当前可用账号 1 个",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("combined notification missing %q: %s", want, message)
		}
	}
	if strings.Contains(message, "📦 **分组：**") {
		t.Fatalf("combined notification must not present a single group: %s", message)
	}
}
