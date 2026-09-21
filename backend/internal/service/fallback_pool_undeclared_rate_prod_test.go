package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 复现 2026-09-14 生产形态：plus(2) 刚配上兜底 → puls-兜底(29)，但兜底依然取不到号。
//
// 池 29 里 4 个可调度账号（195/198/221/223）的共同形态：
//   - accounts.rate_multiplier = 1.0000，正好是建表默认值（没人维护过）
//   - extra.upstream_billing_probe 存在且 status=ok，但 received_at=08:19Z、
//     fresh_until=09:19Z，而当时已是 10:24Z —— 探测早已过期且 worker 停摆
//     （next_probe_at=08:49Z 过去近两小时都没再跑）
//   - 没有手工上游倍率
//
// 这个组合会被 profitControlAccountUpstreamRate 判为"未声明"，而兜底取号比常规
// 调度多一条硬规则：成本未声明的账号不参与兜底。于是整池被拒，兜底配了等于没配。
func TestFallbackSourcingRejectsProductionPool29Shape(t *testing.T) {
	now := time.Now()

	// interval=30min ⇒ fresh_until = received_at + 1h，配合 received_at = now-2h
	// 得到"过期一小时"的快照，与生产快照的时间关系一致。
	newPoolAccount := func(id int64, probeRate float64) *Account {
		return profitControlTestAccountWithRate(
			upstreamCostTestAccount(id, UpstreamBillingProbeStatusOK, probeRate,
				now.Add(-2*time.Hour), 30*time.Minute),
			accountRateMultiplierSchemaDefault)
	}

	pool := map[int64]float64{195: 0.1, 198: 0.06, 221: 0.045, 223: 0.06}

	t.Run("现状：整池被兜底门拒绝", func(t *testing.T) {
		for id, probeRate := range pool {
			account := newPoolAccount(id, probeRate)

			rate, source, state := profitControlAccountUpstreamRate(account, now)
			require.Equalf(t, profitControlRateUndeclared, state,
				"账号 %d：列上是建表默认 1.0、探测已过期，应判为未声明（实得 rate=%v source=%s）",
				id, rate, source)

			reason := fallbackPoolRejectReasonWhenSourcing(context.Background(), account, true)
			require.Equalf(t, fallbackFilterReasonUndeclaredRate, reason,
				"账号 %d 必须被兜底门拒绝——这就是配了兜底仍然取不到号的原因", id)
		}
	})

	t.Run("同样的账号走常规调度不会被拒", func(t *testing.T) {
		// 对照组：证明拦截确实来自兜底专属规则，而不是账号本身不可用。
		// 常规利润门对"未声明"是放行的（可用性优先）。
		account := newPoolAccount(221, 0.045)
		gate := &openAIProfitControlGate{
			groupID: 2, platform: PlatformOpenAI, threshold: 0.5, pricingAt: now,
		}
		ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, gate)

		vetoed, _ := openAIProfitControlVetoReason(ctx, account)
		require.False(t, vetoed, "常规调度对未声明成本是放行的")

		require.Equal(t, fallbackFilterReasonUndeclaredRate,
			fallbackPoolRejectReasonWhenSourcing(ctx, account, true),
			"同一个账号，只有在兜底取号时才被拒")
	})

	t.Run("填上手工上游倍率后整池放行", func(t *testing.T) {
		// 手工倍率没有新鲜度窗口，一直有效直到管理员清除，因此不受探测停摆影响。
		for id, probeRate := range pool {
			account := newPoolAccount(id, probeRate)
			account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = probeRate

			rate, source, state := profitControlAccountUpstreamRate(account, now)
			require.Equal(t, profitControlRateDeclared, state)
			require.Equal(t, profitControlRateSourceManualUpstream, source)
			require.InDeltaf(t, probeRate, rate, 1e-9, "账号 %d 的成本应按手工倍率算", id)

			require.Emptyf(t, fallbackPoolRejectReasonWhenSourcing(context.Background(), account, true),
				"账号 %d 填上手工倍率后必须能参与兜底", id)
		}
	})
}
