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

func TestFormatAutomaticDisableAlertIncludesConcreteUpstreamCause(t *testing.T) {
	message := formatAutomaticDisableAlert(connection_health.AutomaticDisableEvent{
		GroupName: "plus", AccountName: "balance-limited", EffectiveMultiplier: 0.055,
		PreviousPriority: 1, CurrentPriority: 10000, ActiveAccountCount: 2,
		CauseKey: "balance_exhausted", CauseModelName: "gpt-5.6-terra",
		CauseDetail: `{"code":"INSUFFICIENT_BALANCE","message":"账户余额不足"}`,
	})
	for _, want := range []string{"余额或额度耗尽", "gpt-5.6-terra", "INSUFFICIENT_BALANCE", "上游响应"} {
		if !strings.Contains(message, want) {
			t.Fatalf("concrete cause missing %q: %s", want, message)
		}
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

func TestFormatAutomaticRecoveryAlert(t *testing.T) {
	message := formatAutomaticRecoveryAlert(connection_health.AutomaticRecoveryEvent{
		GroupName: "plus", AccountName: "sun_mcgrox_180_plus", ModelName: "gpt-5.6-sol",
	})
	for _, want := range []string{"🟢 **上游账号已自动恢复**", "plus", "sun_mcgrox_180_plus", "gpt-5.6-sol", "真实模型请求成功"} {
		if !strings.Contains(message, want) {
			t.Fatalf("recovery notification missing %q: %s", want, message)
		}
	}
}

func TestFormatAutomaticRecoveryAlertForPriorityRestore(t *testing.T) {
	message := formatAutomaticRecoveryAlert(connection_health.AutomaticRecoveryEvent{
		GroupName: "plus", AccountName: "sun_mcgrox_180_plus", EffectiveMultiplier: 0.055,
		PreviousPriority: 10000, CurrentPriority: 200, ActiveAccountCount: 2, ModelName: "倍率调度恢复",
	})
	for _, want := range []string{"倍率调度已恢复", "0.055x", "10000 → **200**", "**分组当前可用账号：** 2 个"} {
		if !strings.Contains(message, want) {
			t.Fatalf("priority recovery notification missing %q: %s", want, message)
		}
	}
}

func TestFormatAutomaticRecoveryObservationAlert(t *testing.T) {
	message := formatAutomaticRecoveryAlert(connection_health.AutomaticRecoveryEvent{
		GroupName: "plus", AccountName: "sun_mcgrox_180_plus", EffectiveMultiplier: 0.055,
		ModelName: "gpt-5.6-sol", Stage: connection_health.AutomaticRecoveryStageObserving,
	})
	for _, want := range []string{"🟡 **上游账号恢复观察中**", "0.055x", "gpt-5.6-sol", "当前开始恢复观察"} {
		if !strings.Contains(message, want) {
			t.Fatalf("observation notification missing %q: %s", want, message)
		}
	}
}
