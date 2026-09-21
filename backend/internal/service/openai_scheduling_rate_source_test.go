package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 调度的成本因子必须和记账同源：手工上游倍率优先于探测。
//
// 2026-09-20 生产实测的分叉：plus(2) 的 7 个候选里，6 个自营号都填了手工倍率
// （120/182/184=0.075，155/183/175=0.04），而"填了手工倍率就不再探测"是
// upstreamBillingProbeShouldRun 的既定规则，于是它们的 extra 里根本没有探测快照。
// openAISchedulingRate 当时只读新鲜探测，结果 7 个号里只有中转号 221 能给出样本，
// openAIUpstreamCostFactors 的 `len(samples) < 2` 直接让全池中性——管理员标注的便宜
// 号一分加成都拿不到。更糟的是只要再多一个中转号探测成功，成本权重 1.5 就变成只在
// 中转号之间分配。
func TestOpenAISchedulingRateUsesManualRateFirst(t *testing.T) {
	now := time.Now()

	t.Run("手工倍率优先于新鲜探测", func(t *testing.T) {
		account := upstreamCostTestAccount(221, UpstreamBillingProbeStatusOK, 0.9,
			now.Add(-time.Minute), 30*time.Minute)
		account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.04

		rate, ok := openAISchedulingRate(account, now, 1.0)
		require.True(t, ok)
		require.InDelta(t, 0.04, rate, 1e-9,
			"管理员的手工声明是权威值，不得被探测覆盖——记账也是这个口径")
	})

	t.Run("只有手工倍率、没有探测时仍然出样本", func(t *testing.T) {
		// 这正是 120/182/184 的生产形态：填了倍率所以从不探测。
		account := &Account{ID: 120, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Extra: map[string]any{UpstreamBillingManualRateMultiplierExtraKey: 0.075}}

		rate, ok := openAISchedulingRate(account, now, 1.0)
		require.True(t, ok,
			"填了手工倍率的自营号必须能参与成本比较，否则成本权重只在中转号之间分配")
		require.InDelta(t, 0.075, rate, 1e-9)
	})

	t.Run("没有手工倍率时回落到新鲜探测", func(t *testing.T) {
		account := upstreamCostTestAccount(221, UpstreamBillingProbeStatusOK, 0.06,
			now.Add(-time.Minute), 30*time.Minute)

		rate, ok := openAISchedulingRate(account, now, 1.0)
		require.True(t, ok)
		require.InDelta(t, 0.06, rate, 1e-9)
	})

	t.Run("既无手工倍率也无新鲜探测就没有样本", func(t *testing.T) {
		account := &Account{ID: 999, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Extra: map[string]any{}}
		_, ok := openAISchedulingRate(account, now, 1.0)
		require.False(t, ok, "建表默认的列值不是成本声明，不能拿来排序")
	})

	t.Run("非法手工倍率不算声明", func(t *testing.T) {
		account := &Account{ID: 998, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Extra: map[string]any{UpstreamBillingManualRateMultiplierExtraKey: -1.0}}
		_, ok := openAISchedulingRate(account, now, 1.0)
		require.False(t, ok)
	})

	t.Run("OAuth 账号仍走统一倍率", func(t *testing.T) {
		account := &Account{ID: 97, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Extra: map[string]any{UpstreamBillingManualRateMultiplierExtraKey: 0.04}}
		rate, ok := openAISchedulingRate(account, now, 1.0)
		require.True(t, ok)
		require.InDelta(t, 1.0, rate, 1e-9, "OAuth 分支的既有语义不变")
	})
}

// 端到端：生产 plus(2) 那 7 个候选，修好之后成本因子必须真的把便宜号排在前面。
func TestUpstreamCostFactorsCoverManualRateAccounts(t *testing.T) {
	now := time.Now()

	manualAccount := func(id int64, rate float64) *Account {
		return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Extra: map[string]any{UpstreamBillingManualRateMultiplierExtraKey: rate}}
	}
	accounts := []*Account{
		manualAccount(120, 0.075), manualAccount(182, 0.075), manualAccount(184, 0.075),
		manualAccount(155, 0.04), manualAccount(183, 0.04), manualAccount(175, 0.04),
		upstreamCostTestAccount(221, UpstreamBillingProbeStatusOK, 0.9,
			now.Add(-time.Minute), 30*time.Minute),
	}

	factors := openAIUpstreamCostFactors(accounts, now, 1.0)

	require.Greater(t, factors[155], factors[120],
		"0.04 的号必须比 0.075 的号拿到更高的成本因子")
	require.Greater(t, factors[120], factors[221],
		"0.075 的自营号必须优于上游自报 0.9 的中转号")

	// 修好之前 7 个号里只有 221 能给出样本，`len(samples) < 2` 让全池拿中性值 0.5。
	// 这两条钉住"手工倍率确实进入了比较"：便宜的自营号越过中性线拿到加成，
	// 贵的中转号被压到中性线以下。回退 openAISchedulingRate 必炸。
	for _, id := range []int64{155, 183, 175} {
		require.Greaterf(t, factors[id], openAIUpstreamCostNeutralFactor,
			"账号 %d 手工标注 0.04，是池内最便宜的一档，必须拿到高于中性的加成", id)
	}
	require.Less(t, factors[221], openAIUpstreamCostNeutralFactor,
		"上游自报 0.9 的中转号必须被压到中性线以下")
}
