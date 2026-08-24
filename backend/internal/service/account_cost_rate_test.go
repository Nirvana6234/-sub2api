package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 记账倍率必须与利润门准入同源。此前记账只读 accounts.rate_multiplier，
// 而准入已经会读手工/探测倍率，于是同一账号在两处得到不同成本：准入按探测到的
// 0.045 判它合格，usage_logs 却按列上从没维护过的 1.0 记账，account_cost 虚高
// 约 22 倍，把实际盈利的一天显示成巨额亏损。
func TestAccountCostRateMultiplierMatchesProfitGate(t *testing.T) {
	now := time.Now()

	t.Run("采用新鲜探测值而不是未维护的列值", func(t *testing.T) {
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute),
			1.0,
		)
		got := AccountCostRateMultiplier(account, now)
		require.NotNil(t, got)
		require.InDelta(t, 0.045, *got, 1e-9)
	})

	t.Run("手工倍率优先于探测值", func(t *testing.T) {
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(2, UpstreamBillingProbeStatusOK, 0.001, now.Add(-time.Minute), 30*time.Minute),
			1.0,
		)
		account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.05
		got := AccountCostRateMultiplier(account, now)
		require.NotNil(t, got)
		require.InDelta(t, 0.05, *got, 1e-9,
			"上游自报便宜不得压过运营钉死的成本，否则记账会系统性低估")
	})

	t.Run("探测过期时回退列值", func(t *testing.T) {
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(3, UpstreamBillingProbeStatusOK, 0.045, now.Add(-3*time.Hour), 30*time.Minute),
			0.9,
		)
		got := AccountCostRateMultiplier(account, now)
		require.NotNil(t, got)
		require.InDelta(t, 0.9, *got, 1e-9)
	})

	t.Run("未声明时返回nil而不是回退1.0", func(t *testing.T) {
		// 没人声明过成本时按标准原价记账，等于凭空替上游编一个最贵的价格：
		// 生产上正是这样把三个未标注倍率的账号算出 ¥129/¥46/¥43 的假成本，
		// 而它们真实营收只有 ¥13/¥4.6/¥3.1。nil 让该行写进 NULL，成本聚合
		// 直接跳过——宁可少算，也不虚报。
		account := profitControlTestAccountWithRate(
			&Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 1.0)
		account.RateMultiplierUndeclared = true
		require.Nil(t, AccountCostRateMultiplier(account, now))
	})

	t.Run("坏数据与空账号同样不参与成本", func(t *testing.T) {
		bad := profitControlTestAccountWithRate(
			&Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, -1)
		require.Nil(t, AccountCostRateMultiplier(bad, now))
		require.Nil(t, AccountCostRateMultiplier(nil, now))
	})

	t.Run("零倍率是合法的免费上游", func(t *testing.T) {
		account := profitControlTestAccountWithRate(
			&Account{ID: 6, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 0)
		got := AccountCostRateMultiplier(account, now)
		require.NotNil(t, got, "0 是明确声明的免费上游，与未声明不同，必须参与成本计算")
		require.Zero(t, *got)
	})
}

// 成本倍率对外披露时必须连来源一起给：transithub 的调价映射要据此判断
// 这个数字能不能用来算毛利。来源判断一旦与利润门分叉，就会重演"上游标称 0.8、
// 实际手工成本 0.04"那种 20 倍偏差（生产账号 119/120）。
func TestAccountCostRateMultiplierWithSource(t *testing.T) {
	now := time.Now()

	t.Run("手工倍率优先且来源标记为manual", func(t *testing.T) {
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(101, UpstreamBillingProbeStatusOK, 0.8, now.Add(-time.Minute), 30*time.Minute),
			1.0,
		)
		account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.04
		rate, source := AccountCostRateMultiplierWithSource(account, now)
		require.NotNil(t, rate)
		require.InDelta(t, 0.04, *rate, 1e-9,
			"运营钉死的 0.04 必须压过上游自报的 0.8")
		require.Equal(t, AccountCostRateSourceManual, source)
	})

	t.Run("无手工值时用新鲜探测值并标记probe", func(t *testing.T) {
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(102, UpstreamBillingProbeStatusOK, 0.16, now.Add(-time.Minute), 30*time.Minute),
			1.0,
		)
		rate, source := AccountCostRateMultiplierWithSource(account, now)
		require.NotNil(t, rate)
		require.InDelta(t, 0.16, *rate, 1e-9)
		require.Equal(t, AccountCostRateSourceProbe, source)
	})

	t.Run("探测失败且列值是建表默认时判为无声明", func(t *testing.T) {
		// 对应生产账号 137：探测连续 403 失败，列上只有建表默认 1.0。
		// 若此时把 1.0 当成本，等于把中转账号按原价结算，曾虚高约 100 倍。
		// 按生产库里 failed 快照的真实形态构造：只有失败计数与时间戳，
		// 没有 data、没有 fresh_until——探测从没成功过，自然拿不出倍率。
		// 不能用 upstreamCostTestAccount，它总会塞进 data 和 fresh_until，
		// 而 failed 快照只要带着新鲜倍率仍会被采信，那就测不到本用例要守的行为。
		account := profitControlTestAccountWithRate(
			&Account{
				ID:       103,
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Extra: map[string]any{
					UpstreamBillingProbeEnabledExtraKey: true,
					UpstreamBillingProbeExtraKey: map[string]any{
						"status":          UpstreamBillingProbeStatusFailed,
						"last_error":      "invalid_response",
						"http_status":     float64(200),
						"failure_count":   float64(12),
						"last_attempt_at": now.Add(-time.Minute).UTC().Format(time.RFC3339Nano),
						"next_probe_at":   now.Add(29 * time.Minute).UTC().Format(time.RFC3339Nano),
					},
				},
			},
			1.0,
		)
		rate, source := AccountCostRateMultiplierWithSource(account, now)
		require.Nil(t, rate, "没探测到就是不知道，不能拿建表默认值冒充声明")
		require.Equal(t, AccountCostRateSourceNone, source)
	})

	t.Run("显式未声明时返回none", func(t *testing.T) {
		// 对应生产账号 138/135/127/125：rate_multiplier_undeclared = true 且无探测。
		account := profitControlTestAccountWithRate(
			&Account{ID: 104, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 1.0)
		account.RateMultiplierUndeclared = true
		rate, source := AccountCostRateMultiplierWithSource(account, now)
		require.Nil(t, rate)
		require.Equal(t, AccountCostRateSourceNone, source)
	})

	t.Run("列值声明标记为column", func(t *testing.T) {
		account := profitControlTestAccountWithRate(
			&Account{ID: 105, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 0.65)
		rate, source := AccountCostRateMultiplierWithSource(account, now)
		require.NotNil(t, rate)
		require.InDelta(t, 0.65, *rate, 1e-9)
		require.Equal(t, AccountCostRateSourceColumn, source)
	})

	t.Run("空账号返回none", func(t *testing.T) {
		rate, source := AccountCostRateMultiplierWithSource(nil, now)
		require.Nil(t, rate)
		require.Equal(t, AccountCostRateSourceNone, source)
	})

	t.Run("与AccountCostRateMultiplier数值始终一致", func(t *testing.T) {
		// 两个函数共用 profitControlAccountUpstreamRate，任何一方被改出分叉都应在此暴露。
		accounts := []*Account{
			profitControlTestAccountWithRate(
				upstreamCostTestAccount(106, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute), 1.0),
			profitControlTestAccountWithRate(
				&Account{ID: 107, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 0),
			profitControlTestAccountWithRate(
				&Account{ID: 108, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, -1),
		}
		for _, account := range accounts {
			legacy := AccountCostRateMultiplier(account, now)
			rate, _ := AccountCostRateMultiplierWithSource(account, now)
			if legacy == nil {
				require.Nil(t, rate)
				continue
			}
			require.NotNil(t, rate)
			require.InDelta(t, *legacy, *rate, 1e-9)
		}
	})
}
