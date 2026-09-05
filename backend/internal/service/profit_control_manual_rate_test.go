package service

// 利润门 U 的三态解析：已声明 / 未声明 / 非法。
//
// 回归的是一次真实生产故障：某 anthropic 分组售价倍率 0.1、margin 与 buffer 均
// 为 0（阈值 0.1），组内三个中转账号从建号起 accounts.rate_multiplier 就是当时
// 的 DDL 默认值 1.0、从没有人维护过，运营只在账号名上标了 "0.04x"。利润门把这
// 个默认值当成"运营者声明成本为 1.0"，判定越线后把整池账号排除，6 小时内 1153
// 次请求返回 no available accounts，而候选排序侧读 extra 的成本信号一切正常。
//
// 根治有两条：其一，用显式字段 rate_multiplier_undeclared（migration 202）承载
// "从没人声明过成本"，与"声明为 1.0"彻底分开，未声明时利润门放行并告警而不是
// 替运营假设成本；其二，准入与候选排序读同一份运营声明（含 extra 上的手工上游
// 倍率），不再一个读 extra、一个读列。
//
// 刻意不把 rate_multiplier 改成可空：nil 当前的确切含义是"调度缓存漏字段"
// （DB 列非空且有默认值），利润门对它 fail-closed。让该列可空会使"未声明"与
// "漏字段"同形，并把漏字段的后果从保守拒绝翻转成静默放行。

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func profitRateTestGate(threshold float64) context.Context {
	return context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		groupID:   1,
		platform:  PlatformAnthropic,
		threshold: threshold,
		pricingAt: time.Now(),
	})
}

func profitRateTestAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
}

// profitRateTestUndeclaredAccount 复刻线上从没人填过倍率的账号形态：列上是
// 建表默认值 1.0，同时带着显式的未声明标记。
func profitRateTestUndeclaredAccount(id int64) *Account {
	account := profitControlTestAccountWithRate(profitRateTestAccount(id), 1.0)
	account.RateMultiplierUndeclared = true
	return account
}

func TestProfitControlAccountUpstreamRateSources(t *testing.T) {
	t.Run("手工上游倍率优先于列值", func(t *testing.T) {
		account := profitControlTestAccountWithRate(profitRateTestAccount(121), 1.0)
		account.Extra = map[string]any{UpstreamBillingManualRateMultiplierExtraKey: 0.04}

		rate, source, state := profitControlAccountUpstreamRate(account, time.Now())
		require.Equal(t, profitControlRateDeclared, state)
		require.InDelta(t, 0.04, rate, 1e-9)
		require.Equal(t, profitControlRateSourceManualUpstream, source)
	})

	t.Run("没有手工倍率时用列值", func(t *testing.T) {
		account := profitControlTestAccountWithRate(profitRateTestAccount(110), 0.07)

		rate, source, state := profitControlAccountUpstreamRate(account, time.Now())
		require.Equal(t, profitControlRateDeclared, state)
		require.InDelta(t, 0.07, rate, 1e-9)
		require.Equal(t, profitControlRateSourceAccountColumn, source)
	})

	t.Run("显式未声明标记优先于列上的默认值", func(t *testing.T) {
		// 这是本次故障的正解：没人填过倍率是"缺数据"，不是"坏数据"，更不是
		// "声明成本为 1.0"——哪怕列上确实躺着 1.0。
		_, source, state := profitControlAccountUpstreamRate(profitRateTestUndeclaredAccount(1), time.Now())
		require.Equal(t, profitControlRateUndeclared, state)
		require.Equal(t, profitControlRateSourceUndeclared, source)
	})

	t.Run("列值为nil是缓存漏字段按坏数据保守拒绝", func(t *testing.T) {
		// DB 列非空且有默认值，nil 只可能来自调度缓存反序列化缺字段。它必须
		// 继续 fail-closed，不能被当成"未声明"而放行——否则快照一旦漏列，
		// 利润门会静默失效。
		_, _, state := profitControlAccountUpstreamRate(profitRateTestAccount(2), time.Now())
		require.Equal(t, profitControlRateInvalid, state)
	})

	t.Run("列值明确为1.0是声明不是未声明", func(t *testing.T) {
		account := profitControlTestAccountWithRate(profitRateTestAccount(8), 1.0)

		rate, source, state := profitControlAccountUpstreamRate(account, time.Now())
		require.Equal(t, profitControlRateDeclared, state,
			"运营明确填了 1.0（官方直连原价）时必须按声明严格判定，不能当成未声明放行")
		require.InDelta(t, 1.0, rate, 1e-9)
		require.Equal(t, profitControlRateSourceAccountColumn, source)
	})

	t.Run("未声明但设了手工倍率时按手工倍率声明", func(t *testing.T) {
		account := profitRateTestUndeclaredAccount(3)
		account.Extra = map[string]any{UpstreamBillingManualRateMultiplierExtraKey: 0.04}

		rate, _, state := profitControlAccountUpstreamRate(account, time.Now())
		require.Equal(t, profitControlRateDeclared, state)
		require.InDelta(t, 0.04, rate, 1e-9)
	})

	t.Run("零倍率是合法的免费上游声明", func(t *testing.T) {
		account := profitControlTestAccountWithRate(profitRateTestAccount(4), 1.0)
		account.Extra = map[string]any{UpstreamBillingManualRateMultiplierExtraKey: 0.0}

		rate, source, state := profitControlAccountUpstreamRate(account, time.Now())
		require.Equal(t, profitControlRateDeclared, state)
		require.Zero(t, rate)
		require.Equal(t, profitControlRateSourceManualUpstream, source)
	})

	t.Run("负数列值是坏数据按非法处理", func(t *testing.T) {
		account := profitControlTestAccountWithRate(profitRateTestAccount(5), -1)

		_, _, state := profitControlAccountUpstreamRate(account, time.Now())
		require.Equal(t, profitControlRateInvalid, state,
			"坏数据必须与缺数据分开：前者保守拒绝，后者放行告警")
	})

	t.Run("非apikey账号同样采信手工倍率", func(t *testing.T) {
		// 契约变更：手工倍率不再被账号形态挡住。旧行为是"OAuth 账号忽略残留的
		// 手工值"，用身份判断兜底残留；但同一个判断也会把管理员当下填进去的
		// 止血倍率一并丢掉，而止血正是这个字段唯一的用途。
		// 残留改在源头清理——UpdateAccount 的身份变更分支会连同探测状态一起
		// 删除该键，由 TestUpdateAccountResetsProbeStateOnIdentityChange 锁定。
		account := profitControlTestAccountWithRate(&Account{
			ID:       6,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				UpstreamBillingManualRateMultiplierExtraKey: 0.04,
			},
		}, 1.0)

		rate, source, state := profitControlAccountUpstreamRate(account, time.Now())
		require.Equal(t, profitControlRateDeclared, state)
		require.InDelta(t, 0.04, rate, 1e-9)
		require.Equal(t, profitControlRateSourceManualUpstream, source)
	})

	t.Run("非法手工倍率回退列值而不是直接拒绝", func(t *testing.T) {
		account := profitControlTestAccountWithRate(profitRateTestAccount(7), 0.05)
		account.Extra = map[string]any{UpstreamBillingManualRateMultiplierExtraKey: -1.0}

		rate, source, state := profitControlAccountUpstreamRate(account, time.Now())
		require.Equal(t, profitControlRateDeclared, state)
		require.InDelta(t, 0.05, rate, 1e-9)
		require.Equal(t, profitControlRateSourceAccountColumn, source)
	})
}

func TestOpenAIProfitControlVetoRateStates(t *testing.T) {
	// 生产配置复刻：D=0.1、margin=buffer=0 → threshold=0.1。
	const threshold = 0.1

	t.Run("生产故障回归：手工倍率优先于探测值2", func(t *testing.T) {
		// group 12 在生产中的实际参数是 D=0.11、margin=0.15，门槛为
		// 0.11*(1-0.15)=0.0935。账号 119/120 的自动探测曾返回 2，
		// 但管理员已经明确设置手工成本 0.07；利润门必须使用手工值放行。
		const productionThreshold = 0.0935
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(120, UpstreamBillingProbeStatusOK, 2, time.Now().Add(-time.Minute), 30*time.Minute),
			1.0,
		)
		account.Platform = PlatformOpenAI
		account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.07

		vetoed, reason := openAIProfitControlVetoReason(profitRateTestGate(productionThreshold), account)
		require.False(t, vetoed, "手工成本 0.07 <= 生产门槛 0.0935，不得被自动探测值 2 过滤")
		require.Empty(t, reason)
	})

	t.Run("后台标了0.04x的账号不再被列值否决", func(t *testing.T) {
		account := profitControlTestAccountWithRate(&Account{
			ID:       121,
			Name:     "B-【https://api.mhapi.cn】-0.04x",
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				UpstreamBillingManualRateMultiplierExtraKey: 0.04,
			},
		}, 1.0)

		vetoed, reason := openAIProfitControlVetoReason(profitRateTestGate(threshold), account)
		require.False(t, vetoed, "手工上游倍率 0.04 <= 阈值 0.1，准入与候选排序必须读同一份声明")
		require.Empty(t, reason)
	})

	t.Run("未声明成本的账号越线放行并可观测", func(t *testing.T) {
		vetoed, reason := openAIProfitControlVetoReason(profitRateTestGate(threshold), profitRateTestUndeclaredAccount(110))
		require.False(t, vetoed, "没有任何成本声明时利润门无判定依据，不得替运营假设成本再据此否决")
		require.Empty(t, reason)
	})

	t.Run("明确声明1.0的账号照常否决", func(t *testing.T) {
		account := profitControlTestAccountWithRate(profitRateTestAccount(111), 1.0)

		vetoed, reason := openAIProfitControlVetoReason(profitRateTestGate(threshold), account)
		require.True(t, vetoed, "1.0 一旦是运营的明确声明就必须严格判定，否则利润门形同虚设")
		require.Equal(t, openAIProfitFilterReasonThreshold, reason)
	})

	t.Run("手工倍率越线时照常否决", func(t *testing.T) {
		account := profitControlTestAccountWithRate(profitRateTestAccount(122), 0.01)
		account.Extra = map[string]any{UpstreamBillingManualRateMultiplierExtraKey: 0.5}

		vetoed, reason := openAIProfitControlVetoReason(profitRateTestGate(threshold), account)
		require.True(t, vetoed, "手工倍率是运营者的成本声明，越线必须否决，不能被更低的列值救回")
		require.Equal(t, openAIProfitFilterReasonThreshold, reason)
	})

	t.Run("新鲜探测值参与准入并优先于列值", func(t *testing.T) {
		// 契约变更（运营决策）：探测值现在参与准入。生产上 4 个账号列值都是没人
		// 维护过的 1.0，但探测拿到了 0.04~0.045，正是它们让分组重新可用。
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(123, UpstreamBillingProbeStatusOK, 0.04, time.Now().Add(-time.Minute), 30*time.Minute),
			1.0,
		)
		account.Platform = PlatformAnthropic

		vetoed, reason := openAIProfitControlVetoReason(profitRateTestGate(threshold), account)
		require.False(t, vetoed, "新鲜探测值 0.04 <= 阈值 0.1，不该再按列上的 1.0 否决")
		require.Empty(t, reason)
	})

	t.Run("过期探测值不采信且列值仍是默认时判为未声明", func(t *testing.T) {
		// 快照 3 小时前、窗口 30 分钟：早已过期，过期值不能继续替上游背书。
		// 但列上的 1.0 是建表默认值而非声明——探测本就是这个账号的成本来源。
		// 旧行为在这里回退 1.0 并按阈值否决，正是"三个从没维护过倍率的中转账号
		// 被按 1.0 判定越线、整组排除、6 小时 1153 次 no available accounts"
		// 那次故障的形状。现在改为放行并告警，把问题暴露给运营而不是打死流量。
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(124, UpstreamBillingProbeStatusOK, 0.04, time.Now().Add(-3*time.Hour), 30*time.Minute),
			accountRateMultiplierSchemaDefault,
		)
		account.Platform = PlatformAnthropic

		rate, _, state := profitControlAccountUpstreamRate(account, time.Now())
		require.Equal(t, profitControlRateUndeclared, state)
		require.Zero(t, rate, "过期的 0.04 不得被当成有效声明")

		vetoed, reason := openAIProfitControlVetoReason(profitRateTestGate(threshold), account)
		require.False(t, vetoed, "无判定依据时放行并告警，而不是替运营假设一个成本")
		require.Empty(t, reason)
	})

	t.Run("过期探测值遇上维护过的列值仍按列值否决", func(t *testing.T) {
		// 上一条放宽的只是"列值还停在默认值"这一种情形。列值被真的维护过时，
		// 它就是有人负责的声明，过期探测值绝不能借"未声明"绕过阈值判定。
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(125, UpstreamBillingProbeStatusOK, 0.04, time.Now().Add(-3*time.Hour), 30*time.Minute),
			0.5,
		)
		account.Platform = PlatformAnthropic

		vetoed, reason := openAIProfitControlVetoReason(profitRateTestGate(threshold), account)
		require.True(t, vetoed, "维护过的列值 0.5 > 阈值 0.1，必须否决")
		require.Equal(t, openAIProfitFilterReasonThreshold, reason)
	})

	t.Run("坏数据保守拒绝", func(t *testing.T) {
		account := profitControlTestAccountWithRate(profitRateTestAccount(124), -1)

		vetoed, reason := openAIProfitControlVetoReason(profitRateTestGate(threshold), account)
		require.True(t, vetoed)
		require.Equal(t, openAIProfitFilterReasonInvalidAccountRate, reason)
	})

	t.Run("阈值容得下声明值时不否决", func(t *testing.T) {
		account := profitControlTestAccountWithRate(profitRateTestAccount(125), 1.0)

		vetoed, _ := openAIProfitControlVetoReason(profitRateTestGate(1.5), account)
		require.False(t, vetoed)
	})
}

func TestPreviewProfitAdmissionMatchesGateRateStates(t *testing.T) {
	now := time.Now()
	group := profitControlTestGroup(1, 0, 0)
	group.Platform = PlatformAnthropic
	group.RateMultiplier = 0.1
	group.ProfitControlEnabled = true

	manual := profitControlTestAccountWithRate(profitRateTestAccount(121), 1.0)
	manual.Extra = map[string]any{UpstreamBillingManualRateMultiplierExtraKey: 0.04}

	undeclared := profitRateTestUndeclaredAccount(110)

	declaredExpensive := profitControlTestAccountWithRate(profitRateTestAccount(111), 1.0)

	report := PreviewProfitAdmission([]ProfitPreviewGroupInput{{
		Group:    group,
		Accounts: []*Account{manual, undeclared, declaredExpensive},
	}}, now)[0]

	byID := map[int64]ProfitPreviewAccountVerdict{}
	for _, verdict := range report.Verdicts {
		byID[verdict.AccountID] = verdict
	}

	require.Equal(t, ProfitPreviewClassAdmitted, byID[121].Class)
	require.Equal(t, profitControlRateSourceManualUpstream, byID[121].EffectiveRateSource)
	require.NotContains(t, byID[121].Warnings, ProfitPreviewWarningRateUndeclared)

	require.Equal(t, ProfitPreviewClassAdmitted, byID[110].Class,
		"未声明成本的账号在预览里也必须与线上一致地放行")
	require.Equal(t, profitControlRateSourceUndeclared, byID[110].EffectiveRateSource)
	require.Nil(t, byID[110].AccountRate, "没有声明就不该在预览里编造一个倍率")
	require.Contains(t, byID[110].Warnings, ProfitPreviewWarningRateUndeclared,
		"放行必须显式可见，否则运营会以为利润保证在这些账号上成立")

	require.Equal(t, ProfitPreviewClassRejectedThreshold, byID[111].Class,
		"明确声明 1.0 越过阈值 0.1，预览必须与线上一样否决")
	require.NotContains(t, byID[111].Warnings, ProfitPreviewWarningRateUndeclared)
}

// TestProfitControlManualRateBeatsProbe 钉死本次契约变更后唯一的安全边界。
//
// 探测值由上游自报，手工倍率是运营方自己的判断。生产上真实出现过同一账号
// 手工填 0.05、探测自报 0.001（差 50 倍）的情况；另有两个账号手工填 0.04、
// 而探测与账号名都指向 0.8（差 20 倍）。一旦让探测覆盖手工，上游只要自报足够
// 便宜就能永久通过利润门而实际按高价结算——手工优先是运营对付不可信上游的
// 唯一手段，不能因为"探测更新鲜"就反转。
func TestProfitControlManualRateBeatsProbe(t *testing.T) {
	const threshold = 0.1

	t.Run("手工高于阈值时压过便宜的探测值", func(t *testing.T) {
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(200, UpstreamBillingProbeStatusOK, 0.001, time.Now().Add(-time.Minute), 30*time.Minute),
			1.0,
		)
		account.Platform = PlatformAnthropic
		account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.5

		rate, source, state := profitControlAccountUpstreamRate(account, time.Now())
		require.Equal(t, profitControlRateDeclared, state)
		require.InDelta(t, 0.5, rate, 1e-9, "必须取手工 0.5，而不是上游自报的 0.001")
		require.Equal(t, profitControlRateSourceManualUpstream, source)

		vetoed, _ := openAIProfitControlVetoReason(profitRateTestGate(threshold), account)
		require.True(t, vetoed, "上游自报便宜不能把运营钉死的成本覆盖掉")
	})

	t.Run("手工低于阈值时压过昂贵的探测值", func(t *testing.T) {
		// 生产上 mcgrox 两个账号就是这个形态：手工 0.04、探测自报 0.8。
		account := profitControlTestAccountWithRate(
			upstreamCostTestAccount(201, UpstreamBillingProbeStatusOK, 0.8, time.Now().Add(-time.Minute), 30*time.Minute),
			1.0,
		)
		account.Platform = PlatformAnthropic
		account.Extra[UpstreamBillingManualRateMultiplierExtraKey] = 0.04

		rate, source, _ := profitControlAccountUpstreamRate(account, time.Now())
		require.InDelta(t, 0.04, rate, 1e-9)
		require.Equal(t, profitControlRateSourceManualUpstream, source)

		vetoed, _ := openAIProfitControlVetoReason(profitRateTestGate(threshold), account)
		require.False(t, vetoed)
	})
}
