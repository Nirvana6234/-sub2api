package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 记账用的上游成本倍率必须按运营口径解析：
// 手工倍率 → 探测值（新鲜优先，过期次之）→ 列值 → 1.0。
//
// 三个记账入口此前调的是 Account.BillingRateMultiplier()，只读
// accounts.rate_multiplier 列。生产上这些账号的列值全是从没人维护过的建表默认
// 1.0，于是管理员填好的手工倍率和探测到的真实倍率被完全无视：2026-09-14 近 24
// 小时里 15 个账号按 1.0 记出 $341.91 的成本，真实成本只有 $19.85，实收 $22.76
// ——账面巨亏 $319，实际是盈利的。同一个数字还喂给 IncrementQuotaUsed，账号额度
// 按同样倍数虚耗。
//
// 用例按生产账号的真实形态构造。把任一入口改回只读列值，这里必须失败。
func TestAccountBillingRateMultiplierFollowsOperatorRateSource(t *testing.T) {
	now := time.Now()

	t.Run("手工填写的倍率优先于一切", func(t *testing.T) {
		// 生产账号 155/175/183（手工 0.04）与 225（手工 0.15）：只有手工值，
		// 没有探测快照，列值是建表默认 1.0。
		account := profitControlTestAccountWithRate(
			&Account{ID: 155, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Extra: map[string]any{UpstreamBillingManualRateMultiplierExtraKey: 0.04}},
			accountRateMultiplierSchemaDefault)

		require.InDelta(t, 0.04, AccountBillingRateMultiplier(account, now), 1e-9,
			"管理员填了 0.04，记账就必须按 0.04，不能退回列上的默认 1.0")
	})

	t.Run("手工倍率不被探测值覆盖", func(t *testing.T) {
		// 生产账号 227：手工填 0.15，而探测回报 5。采信探测会把成本记成 33 倍。
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(227, UpstreamBillingProbeStatusOK, 5,
				now.Add(-time.Minute), 30*time.Minute),
			accountRateMultiplierSchemaDefault)
		account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.15

		require.InDelta(t, 0.15, AccountBillingRateMultiplier(account, now), 1e-9,
			"手工值是管理员的明确声明，新鲜探测也不得覆盖它")
	})

	t.Run("没填手工倍率时采用探测值", func(t *testing.T) {
		// 生产账号 198：未填手工，探测回报 0.06。
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(198, UpstreamBillingProbeStatusOK, 0.06,
				now.Add(-time.Minute), 30*time.Minute),
			accountRateMultiplierSchemaDefault)

		require.InDelta(t, 0.06, AccountBillingRateMultiplier(account, now), 1e-9)
	})

	t.Run("探测过期仍优于从没维护过的列值", func(t *testing.T) {
		// 生产账号 195：探测 0.1，fresh_until 早已过期（探测开关是关的，不会再刷新）。
		// 过期的探测值仍然比建表默认 1.0 更接近真实成本。
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(195, UpstreamBillingProbeStatusOK, 0.1,
				now.Add(-2*time.Hour), 30*time.Minute),
			accountRateMultiplierSchemaDefault)

		_, ok := openAIFreshUpstreamBillingRate(account, now)
		require.False(t, ok, "构造前提：这份快照确实已经过期")

		require.InDelta(t, 0.1, AccountBillingRateMultiplier(account, now), 1e-9)
	})

	t.Run("列值有维护时按列值", func(t *testing.T) {
		account := profitControlTestAccountWithRate(
			&Account{ID: 76, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 0.12)

		require.InDelta(t, 0.12, AccountBillingRateMultiplier(account, now), 1e-9)
	})

	t.Run("什么都拿不到时按1算", func(t *testing.T) {
		// 生产账号 221：无手工、无探测快照、列值是建表默认。这种情况本来就该按 1。
		account := profitControlTestAccountWithRate(
			&Account{ID: 221, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			accountRateMultiplierSchemaDefault)
		require.InDelta(t, 1.0, AccountBillingRateMultiplier(account, now), 1e-9)

		// 探测本该是这个账号的成本来源（开关开着），但从没成功拿到过倍率，
		// 列值也没人动过。这是三态里真正的"未声明"：同样按 1 计，误差方向是
		// 高估成本，而不是把亏损的账号显示成盈利。
		undeclared := profitControlTestAccountWithRate(
			&Account{ID: 137, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Extra: map[string]any{UpstreamBillingProbeEnabledExtraKey: true}},
			accountRateMultiplierSchemaDefault)
		_, _, state := accountCostUpstreamRate(undeclared, now)
		require.Equal(t, profitControlRateUndeclared, state, "构造前提：这个账号确实无从判定")
		require.InDelta(t, 1.0, AccountBillingRateMultiplier(undeclared, now), 1e-9)
	})

	t.Run("记账倍率与利润门准入同源", func(t *testing.T) {
		// 两者共用 accountCostUpstreamRate / profitControlAccountUpstreamRate，
		// 差别只在利润门不采信过期快照。凡是利润门认定为 declared 的账号，
		// 记账必须得到同一个数字。
		accounts := []*Account{
			profitControlTestAccountWithRate(
				upstreamCostTestAccount(311, UpstreamBillingProbeStatusOK, 0.05,
					now.Add(-time.Minute), 30*time.Minute), accountRateMultiplierSchemaDefault),
			profitControlTestAccountWithRate(
				&Account{ID: 312, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 0.12),
		}
		accounts = append(accounts, profitControlTestAccountWithRate(
			&Account{ID: 313, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Extra: map[string]any{UpstreamBillingManualRateMultiplierExtraKey: 0.045}},
			accountRateMultiplierSchemaDefault))

		for _, account := range accounts {
			gateRate, _, state := profitControlAccountUpstreamRate(account, now)
			require.Equal(t, profitControlRateDeclared, state)
			require.InDeltaf(t, gateRate, AccountBillingRateMultiplier(account, now), 1e-9,
				"账号 %d：利润门按 %v 判准入，记账就必须按 %v 计", account.ID, gateRate, gateRate)
		}
	})
}
