package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// upstreamBillingProbeShouldRun 是探测调度的唯一准则，
// ListDueUpstreamBillingProbeAccounts 的 SQL 候选条件必须镜像它
// （见 repository 侧的 TestListDueUpstreamBillingProbeAccountsMirrorsShouldRun）。
func TestUpstreamBillingProbeShouldRunRules(t *testing.T) {
	probeAccount := func(id int64) *Account {
		return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Extra: map[string]any{}}
	}

	t.Run("填了手工倍率就不探测", func(t *testing.T) {
		// 生产账号 155/175/183/225 的形态：管理员已给出权威声明，联网探测没有意义。
		account := probeAccount(155)
		account.Extra[UpstreamBillingProbeEnabledExtraKey] = true
		account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.04

		require.False(t, upstreamBillingProbeShouldRun(account),
			"手工倍率优先，开关开着也不该再去探测——这类账号占满候选名额会饿死其他号")
	})

	t.Run("非法手工倍率不算声明仍要探测", func(t *testing.T) {
		// 负数不是合法声明，upstreamBillingManualRateMultiplier 返回 ok=false，
		// 因此 SQL 的排除条件必须比这更窄（只排除合法数字）。
		account := probeAccount(156)
		account.Extra[UpstreamBillingProbeEnabledExtraKey] = true
		account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = -1.0

		require.True(t, upstreamBillingProbeShouldRun(account))
	})

	t.Run("开关关着但标记未声明也要探测", func(t *testing.T) {
		// 这条是 SQL 此前漏掉的：只认 probe_enabled 会让未声明的账号永远拿不到成本，
		// 记账按 1.0 计，兜底取号时又被当成未声明直接拒绝。
		account := probeAccount(137)
		account.RateMultiplierUndeclared = true

		require.True(t, upstreamBillingProbeShouldRun(account))
	})

	t.Run("开关开着就探测", func(t *testing.T) {
		account := probeAccount(142)
		account.Extra[UpstreamBillingProbeEnabledExtraKey] = true

		require.True(t, upstreamBillingProbeShouldRun(account))
	})

	t.Run("两个条件都不满足就不探测", func(t *testing.T) {
		require.False(t, upstreamBillingProbeShouldRun(probeAccount(221)))
	})
}
