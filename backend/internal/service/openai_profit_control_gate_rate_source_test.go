package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 利润门的 U 必须和记账的 U 同源。
//
// 2026-07-26 引入利润门时，否决点直接读 accounts.rate_multiplier 列；
// 2026-08-27/09-10 重建的 profitControlAccountUpstreamRate 把"手工上游倍率 →
// 新鲜探测 → 列值"确立为唯一解析口径，AccountCostRateMultiplier（扣费）和
// fallbackPoolRejectReasonWhenSourcing（跨分组借号）都改用了它，唯独否决点
// 留在老路径上。结果是准入和扣费按两套成本判断，两个方向都能出错。
//
// 这些用例各自钉住一个方向；把否决点改回读列值，它们必须失败。
func TestProfitControlGateUsesSameRateSourceAsBilling(t *testing.T) {
	now := time.Now()
	gateCtx := func(threshold float64) context.Context {
		return context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
			groupID:   77,
			platform:  PlatformOpenAI,
			threshold: threshold,
			pricingAt: now,
		})
	}

	t.Run("手工上游倍率优先于未维护的列值", func(t *testing.T) {
		// 生产形态：管理员在后台手填了上游倍率（存在 extra 里），列还是建表
		// 默认 1.0。扣费按 0.045 走，是笔赚钱的单；老否决点按 1.0 判，把这种
		// 账号整池否决掉。
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(301, UpstreamBillingProbeStatusFailed, 0, now.Add(-3*time.Hour), 30*time.Minute),
			accountRateMultiplierSchemaDefault)
		account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.045

		vetoed, reason := openAIProfitControlVetoReason(gateCtx(0.13), account)
		require.False(t, vetoed,
			"手工声明的 0.045 远低于阈值 0.13，不得因为列上留着默认 1.0 被否决，否决原因=%s", reason)

		rate := AccountCostRateMultiplier(account, now)
		require.NotNil(t, rate)
		require.InDelta(t, 0.045, *rate, 1e-9, "构造前提：扣费确实按手工倍率走")
	})

	t.Run("新鲜探测高于列值时必须否决", func(t *testing.T) {
		// 反方向、也是更贵的一侧：列上写着 0.12 看着能赚，新鲜探测回报上游
		// 实际 0.9。扣费按 0.9 记，老否决点却按 0.12 放行，每一单都亏。
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(302, UpstreamBillingProbeStatusOK, 0.9, now.Add(-time.Minute), 30*time.Minute),
			0.12)

		vetoed, reason := openAIProfitControlVetoReason(gateCtx(0.13), account)
		require.True(t, vetoed, "新鲜探测 0.9 已越过阈值 0.13，不得按陈旧列值 0.12 放行")
		require.Equal(t, openAIProfitFilterReasonThreshold, reason)

		rate := AccountCostRateMultiplier(account, now)
		require.NotNil(t, rate)
		require.InDelta(t, 0.9, *rate, 1e-9, "构造前提：扣费确实按探测值走")
	})

	t.Run("无人声明成本时按可用性优先放行", func(t *testing.T) {
		// 三态契约里的 undeclared：这个账号的价格本该由探测提供，探测拿不到
		// 值，列上是没人动过的建表默认 1.0。默认值不是成本声明，门不替运营
		// 假设成本——放行并计数，而不是按 1.0 判为越线。
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(303, UpstreamBillingProbeStatusFailed, 0, now.Add(-3*time.Hour), 30*time.Minute),
			accountRateMultiplierSchemaDefault)
		_, _, state := profitControlAccountUpstreamRate(account, now)
		require.Equal(t, profitControlRateUndeclared, state, "构造前提：这个账号确实是未声明态")

		vetoed, _ := openAIProfitControlVetoReason(gateCtx(0.13), account)
		require.False(t, vetoed, "未声明不等于越线，门应放行并由 undeclared_rate_admit_total 暴露")
	})

	t.Run("被放行的账号扣费倍率不得高于阈值", func(t *testing.T) {
		// 同源的可证伪表述：凡是门放行且成本可判定的账号，按记账口径算出的
		// 成本都必须在阈值内。任何一方被改出分叉都会在这里炸。
		threshold := 0.13
		accounts := []*Account{
			profitControlTestAccountWithRate(
				upstreamCostTestAccount(311, UpstreamBillingProbeStatusOK, 0.05, now.Add(-time.Minute), 30*time.Minute), 1.0),
			profitControlTestAccountWithRate(
				upstreamCostTestAccount(312, UpstreamBillingProbeStatusOK, 0.9, now.Add(-time.Minute), 30*time.Minute), 0.12),
			profitControlTestAccountWithRate(
				&Account{ID: 313, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 0.1),
			profitControlTestAccountWithRate(
				&Account{ID: 314, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 0.5),
		}
		accounts[0].Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.02

		for _, account := range accounts {
			vetoed, _ := openAIProfitControlVetoReason(gateCtx(threshold), account)
			if vetoed {
				continue
			}
			_, _, state := profitControlAccountUpstreamRate(account, now)
			if state == profitControlRateUndeclared {
				continue
			}
			rate := AccountCostRateMultiplier(account, now)
			require.NotNil(t, rate, "账号 %d 被放行就必须能算出扣费倍率", account.ID)
			require.LessOrEqualf(t, *rate, threshold+1e-9,
				"账号 %d 被利润门放行，但扣费倍率 %v 超过阈值 %v——准入与扣费口径分叉了",
				account.ID, *rate, threshold)
		}
	})
}
