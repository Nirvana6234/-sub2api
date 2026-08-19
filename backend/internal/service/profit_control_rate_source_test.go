package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func probeFailedAccount(id int64, rateMultiplier float64) *Account {
	rate := rateMultiplier
	return &Account{
		ID:             id,
		Platform:       PlatformOpenAI,
		Type:           AccountTypeAPIKey,
		RateMultiplier: &rate,
		Extra: map[string]any{
			UpstreamBillingProbeEnabledExtraKey: true,
			UpstreamBillingProbeExtraKey: map[string]any{
				"status":        string(UpstreamBillingProbeStatusFailed),
				"last_error":    "http_error",
				"http_status":   float64(403),
				"failure_count": float64(44),
			},
		},
	}
}

// 生产账号 137：探测连续 403 失败 44 次，列值停在建表默认 1.0，于是 5 次请求
// 把 ¥0.5321 原价全额记成账号成本，而同批营收只有 ¥0.0053——虚高约 100 倍。
// 探测拿不到值时的真相是"不知道"，不是"按原价"。
func TestProfitControlRate_ProbeFailedWithDefaultColumnIsUndeclared(t *testing.T) {
	account := probeFailedAccount(137, accountRateMultiplierSchemaDefault)

	rate, source, state := profitControlAccountUpstreamRate(account, time.Now())
	require.Equal(t, profitControlRateUndeclared, state,
		"探测是该账号的成本来源且失败，列上的默认值不构成声明")
	require.Zero(t, rate)
	require.Equal(t, profitControlRateSourceUndeclared, source)
	require.Nil(t, AccountCostRateMultiplier(account, time.Now()),
		"未声明必须写 NULL，让成本聚合跳过该行")
}

// 探测同样失败，但管理员真的维护过列值（生产账号 76 = 0.12，失败 330 次）。
// 这个值是有人负责的声明，不能被一并丢弃，否则修一个洞会挖出另一个洞。
func TestProfitControlRate_ProbeFailedKeepsMaintainedColumn(t *testing.T) {
	account := probeFailedAccount(76, 0.12)

	rate, _, state := profitControlAccountUpstreamRate(account, time.Now())
	require.Equal(t, profitControlRateDeclared, state)
	require.InDelta(t, 0.12, rate, 1e-9)
}

// 探测开关已关但快照还在（生产账号 3/4/5/35/93 的 404 unsupported 形态）：
// 快照本身就说明这个账号的价格是问上游要的，列上的默认值同样不算声明。
func TestProfitControlRate_DisabledProbeWithLeftoverSnapshotIsUndeclared(t *testing.T) {
	account := probeFailedAccount(3, accountRateMultiplierSchemaDefault)
	account.Extra[UpstreamBillingProbeEnabledExtraKey] = false

	_, _, state := profitControlAccountUpstreamRate(account, time.Now())
	require.Equal(t, profitControlRateUndeclared, state)
}

// 从没配过探测的账号（生产账号 34 是官方 Anthropic 直连，无 base_url、无探测键）
// 列值 1.0 就是真实倍率，必须继续参与成本核算，不能被误判成未声明。
func TestProfitControlRate_NoProbeSourceKeepsDefaultColumn(t *testing.T) {
	rate := accountRateMultiplierSchemaDefault
	account := &Account{
		ID: 34, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		RateMultiplier: &rate,
	}

	got, source, state := profitControlAccountUpstreamRate(account, time.Now())
	require.Equal(t, profitControlRateDeclared, state,
		"没有指定成本来源时列值仍是唯一声明，官方直连的 1.0 是真值")
	require.InDelta(t, 1.0, got, 1e-9)
	require.Equal(t, profitControlRateSourceAccountColumn, source)
}

// 探测彻底关闭且无快照、列值被维护过（生产账号 113 = 0.09）：原样采信。
func TestProfitControlRate_ProbeDisabledUsesMaintainedColumn(t *testing.T) {
	rate := 0.09
	account := &Account{
		ID: 113, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		RateMultiplier: &rate,
		Extra:          map[string]any{UpstreamBillingProbeEnabledExtraKey: false},
	}

	got, _, state := profitControlAccountUpstreamRate(account, time.Now())
	require.Equal(t, profitControlRateDeclared, state)
	require.InDelta(t, 0.09, got, 1e-9)
}

// 管理员填的手工倍率是止血手段：探测正在失败时它必须立刻生效，
// 这正是"等管理员手动标记了倍率以后按标记的倍率计算"那条规则的落点。
func TestProfitControlRate_ManualRateStopsTheBleeding(t *testing.T) {
	account := probeFailedAccount(137, accountRateMultiplierSchemaDefault)
	account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.05

	got, source, state := profitControlAccountUpstreamRate(account, time.Now())
	require.Equal(t, profitControlRateDeclared, state)
	require.InDelta(t, 0.05, got, 1e-9)
	require.Equal(t, profitControlRateSourceManualUpstream, source)

	cost := AccountCostRateMultiplier(account, time.Now())
	require.NotNil(t, cost)
	require.InDelta(t, 0.05, *cost, 1e-9, "记账与准入必须读同一个声明")
}

// 手工倍率不再被账号形态挡住。写入侧只允许探测型账号设置，改类型时
// UpdateAccount 会清掉残留，所以读取侧无条件采信是安全的——而把它关在身份
// 判断里，只会让管理员填的止血值被静默丢弃。
func TestProfitControlRate_ManualRateHonoredOnNonProbeIdentity(t *testing.T) {
	rate := accountRateMultiplierSchemaDefault
	account := &Account{
		ID: 900, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		RateMultiplier: &rate,
		Extra:          map[string]any{UpstreamBillingManualRateMultiplierExtraKey: 0.07},
	}

	got, source, state := profitControlAccountUpstreamRate(account, time.Now())
	require.Equal(t, profitControlRateDeclared, state)
	require.InDelta(t, 0.07, got, 1e-9)
	require.Equal(t, profitControlRateSourceManualUpstream, source)
}

// 手工倍率 0 是"上游免费"的明确声明，与未声明不同，必须照常采信。
func TestProfitControlRate_ManualZeroIsADeclaration(t *testing.T) {
	account := probeFailedAccount(901, accountRateMultiplierSchemaDefault)
	account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.0

	got, _, state := profitControlAccountUpstreamRate(account, time.Now())
	require.Equal(t, profitControlRateDeclared, state)
	require.Zero(t, got)
}

// 新鲜探测值仍然压过列值，且不受本次改动影响。
func TestProfitControlRate_FreshProbeStillWins(t *testing.T) {
	now := time.Now()
	account := profitControlTestAccountWithRate(
		upstreamCostTestAccount(902, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute),
		accountRateMultiplierSchemaDefault,
	)

	got, source, state := profitControlAccountUpstreamRate(account, now)
	require.Equal(t, profitControlRateDeclared, state)
	require.InDelta(t, 0.045, got, 1e-9)
	require.Equal(t, profitControlRateSourceUpstreamProbe, source)
}

// 探测成功但快照已过期，列值又停在默认值：同样是"不知道"。
// 修复前这里会静默回退 1.0，把中转账号按原价记账。
func TestProfitControlRate_StaleProbeWithDefaultColumnIsUndeclared(t *testing.T) {
	now := time.Now()
	account := profitControlTestAccountWithRate(
		upstreamCostTestAccount(903, UpstreamBillingProbeStatusOK, 0.045, now.Add(-3*time.Hour), 30*time.Minute),
		accountRateMultiplierSchemaDefault,
	)

	_, _, state := profitControlAccountUpstreamRate(account, now)
	require.Equal(t, profitControlRateUndeclared, state)
}
