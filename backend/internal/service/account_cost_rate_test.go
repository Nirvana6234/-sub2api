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
