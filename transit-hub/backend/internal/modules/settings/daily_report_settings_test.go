package settings

import "testing"

// 推送时刻非法时必须退回默认值。若放任非法值留着，调度器每分钟比对
// HH:MM 永远匹配不上，表现为「开了简报但一条都收不到」——最难查的那类故障。
func TestNormalizeDailyReportTime(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", defaultDailyReportTime},
		{"   ", defaultDailyReportTime},
		{"25:00", defaultDailyReportTime},
		{"9:00:00", defaultDailyReportTime},
		{"abc", defaultDailyReportTime},
		{"7:30", "07:30"},
		{"21:45", "21:45"},
		{"00:00", "00:00"},
	}
	for _, tc := range cases {
		if got := normalizeDailyReportTime(tc.in); got != tc.want {
			t.Errorf("normalizeDailyReportTime(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// 新字段缺失的历史记录必须能安全落到「未开启 + 默认时刻」，
// 不能因为反序列化出零值就让调度器在 00:00 意外推送。
func TestNormalizeStrategyFillsDailyReportDefaults(t *testing.T) {
	normalized := normalizeStrategySettings(StrategySettings{})

	if normalized.EnableDailyReport {
		t.Error("缺省应为未开启简报")
	}
	if normalized.DailyReportTime != defaultDailyReportTime {
		t.Errorf("默认推送时刻 = %q，期望 %q", normalized.DailyReportTime, defaultDailyReportTime)
	}
	if normalized.DailyReportFormat != NotificationTemplateFormatMarkdown {
		t.Errorf("默认格式 = %q，期望 markdown", normalized.DailyReportFormat)
	}
}

// 已经显式选过格式的配置不该被默认值覆盖。
func TestNormalizeStrategyKeepsExplicitReportFormat(t *testing.T) {
	normalized := normalizeStrategySettings(StrategySettings{
		DailyReportFormat: NotificationTemplateFormatText,
	})
	if normalized.DailyReportFormat != NotificationTemplateFormatText {
		t.Errorf("显式选择的格式被覆盖为 %q", normalized.DailyReportFormat)
	}
}
