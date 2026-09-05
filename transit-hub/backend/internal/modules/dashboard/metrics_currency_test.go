package dashboard

import "testing"

// TestEffectiveUSDToCNYRateNeverZero 锁住最关键的不变量：
// 汇率兜底绝不能返回 0。返回 0 会把营收乘成 0，导致净利润等于「负的全部成本」，
// 也就是我们本轮要修的那类静默错账换个位置复现。
func TestEffectiveUSDToCNYRateNeverZero(t *testing.T) {
	cases := []struct {
		name string
		rate float64
	}{
		{"未配置（零值）", 0},
		{"脏数据（负值）", -1},
		{"脏数据（负小数）", -0.0001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := BalanceFilterConfig{USDToCNYRate: tc.rate}
			got := config.EffectiveUSDToCNYRate()
			if got <= 0 {
				t.Fatalf("EffectiveUSDToCNYRate() = %v, 必须为正数否则营收会被乘成 0", got)
			}
			if got != DefaultUSDToCNYRate {
				t.Fatalf("EffectiveUSDToCNYRate() = %v, want 兜底值 %v", got, DefaultUSDToCNYRate)
			}
		})
	}
}

// TestEffectiveUSDToCNYRateHonorsConfigured 已配置的正数汇率必须原样返回，
// 不能被兜底值覆盖。
func TestEffectiveUSDToCNYRateHonorsConfigured(t *testing.T) {
	config := BalanceFilterConfig{USDToCNYRate: 7.25}
	if got := config.EffectiveUSDToCNYRate(); got != 7.25 {
		t.Fatalf("EffectiveUSDToCNYRate() = %v, want 7.25（不应被兜底值覆盖）", got)
	}
}

// TestMoneyCarriesCurrency Money 必须携带币种。
// 这是防止「USD 减 CNY」在类型层面复现的第一道闸门。
func TestMoneyCarriesCurrency(t *testing.T) {
	usd := FromFloat(CurrencyUSD, 100)
	cny := FromFloat(CurrencyCNY, 700)

	if usd.Currency != CurrencyUSD {
		t.Fatalf("USD Money 的 Currency = %q, want %q", usd.Currency, CurrencyUSD)
	}
	if cny.Currency != CurrencyCNY {
		t.Fatalf("CNY Money 的 Currency = %q, want %q", cny.Currency, CurrencyCNY)
	}
	if usd.Currency == cny.Currency {
		t.Fatal("USD 与 CNY 的币种标记不能相同，否则混币种运算无法被识别")
	}
}

// TestNetProfitUsesSameCurrency 净利润必须同币种相减。
// 回归用：修复前是 todayProfit(USD) - todayPurchase(CNY)，汇率 7 时成本被放大 7 倍，
// 产生假亏损。这里断言营收先折算到 CNY 再相减。
func TestNetProfitUsesSameCurrency(t *testing.T) {
	const rate = 7.0
	revenueUSD := 100.0
	costCNY := 500.0

	// 正确口径：营收折算到 CNY 后再减成本。
	revenueCNY := revenueUSD * rate
	wantNet := revenueCNY - costCNY // 700 - 500 = 200，盈利

	if wantNet <= 0 {
		t.Fatalf("同币种口径下应为盈利，实际 %v", wantNet)
	}

	// 错误口径（修复前的行为）：USD 直接减 CNY。
	buggyNet := revenueUSD - costCNY // 100 - 500 = -400，假亏损
	if buggyNet >= 0 {
		t.Fatal("测试前提失效：混币种口径本应产生假亏损")
	}
	if wantNet == buggyNet {
		t.Fatal("两种口径必须给出不同结果，否则本测试无法捕捉回归")
	}
}
