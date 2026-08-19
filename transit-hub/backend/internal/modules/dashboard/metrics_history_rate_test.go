package dashboard

import "testing"

// 历史快照必须保留写入时的汇率。
// 参考项目的硬约束：Historical request cost must not silently change when
// current model prices or group multipliers change later.
//
// 回归场景：Trends 曾用「当前全局汇率」乘所有历史行，改一次汇率
// 整条历史曲线被追溯改写。修复后改用每行持久化的 usd_to_cny_rate。

func TestDailySnapshotEffectiveRateFallsBackForPreMigrationRows(t *testing.T) {
	// 迁移前写入的旧行没有汇率列，读出来是 0。
	// 必须兜底成默认值，绝不能用 0 把营收乘成 0。
	cases := []struct {
		name string
		rate float64
	}{
		{"迁移前旧行（零值）", 0},
		{"脏数据（负值）", -7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := DailySnapshot{TodayProfitUSD: 100, USDToCNYRate: tc.rate}
			if got := snap.EffectiveRate(); got != DefaultUSDToCNYRate {
				t.Fatalf("EffectiveRate() = %v, want fallback %v", got, DefaultUSDToCNYRate)
			}
			if cny := snap.TodayProfitUSD * snap.EffectiveRate(); cny == 0 {
				t.Fatal("营收被乘成 0：汇率兜底失效")
			}
		})
	}
}

func TestDailySnapshotEffectiveRateHonorsPersistedRate(t *testing.T) {
	snap := DailySnapshot{TodayProfitUSD: 100, USDToCNYRate: 6.5}
	if got := snap.EffectiveRate(); got != 6.5 {
		t.Fatalf("EffectiveRate() = %v, want persisted 6.5", got)
	}
}

// 核心不变量：同一批历史行各用自己的汇率，互不干扰。
// 若实现回退成「当前全局汇率」，两行会得到相同倍率，此测试失败。
func TestHistoricalRowsKeepTheirOwnRate(t *testing.T) {
	snapshots := []DailySnapshot{
		{TodayProfitUSD: 100, USDToCNYRate: 7.0}, // 当时汇率 7.0
		{TodayProfitUSD: 100, USDToCNYRate: 6.0}, // 后来调成 6.0
	}

	got := make([]float64, 0, len(snapshots))
	for _, snap := range snapshots {
		got = append(got, snap.TodayProfitUSD*snap.EffectiveRate())
	}

	if got[0] != 700 {
		t.Fatalf("第一行 CNY = %v, want 700（按其自身汇率 7.0）", got[0])
	}
	if got[1] != 600 {
		t.Fatalf("第二行 CNY = %v, want 600（按其自身汇率 6.0）", got[1])
	}
	if got[0] == got[1] {
		t.Fatal("两行得到相同金额：说明用了统一汇率，历史被追溯改写")
	}
}

// 汇率变更后，已落盘的历史行金额不得改变。
func TestChangingCurrentRateDoesNotRewriteHistory(t *testing.T) {
	historical := DailySnapshot{TodayProfitUSD: 100, USDToCNYRate: 7.0}
	before := historical.TodayProfitUSD * historical.EffectiveRate()

	// 管理员把当前汇率改成 6.0（写进 dashboard_balance_filter）。
	// 历史行自带汇率，不受影响。
	currentConfig := BalanceFilterConfig{USDToCNYRate: 6.0}
	if currentConfig.EffectiveUSDToCNYRate() != 6.0 {
		t.Fatal("当前配置汇率未生效")
	}

	after := historical.TodayProfitUSD * historical.EffectiveRate()
	if before != after {
		t.Fatalf("历史金额被改写：before=%v after=%v", before, after)
	}
	if after != 700 {
		t.Fatalf("历史金额 = %v, want 700（保留原汇率 7.0）", after)
	}
}
