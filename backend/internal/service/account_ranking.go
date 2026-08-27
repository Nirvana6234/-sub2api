package service

import (
	mathrand "math/rand"
)

// 网关侧候选账号排序的唯一实现。
//
// 在此之前，「同一批候选里谁更该被选中」这件事在 gateway_scheduling.go 里有 7 处
// 各自独立的实现：selectAccountForModelWithPlatform 与 selectAccountWithMixedScheduling
// 各有两条内联比较链、两个 sortAccountsBy* 排序函数、以及分层链末端的 selectByLRU。
//
// 拷贝已经漂移出两种语义：混合调度那两条要求「双方都是 Gemini」才按账号类型偏好
// OAuth，其余四处没有这个限定。这类差异没有任何注释说明，只能靠逐行比对才能发现。
//
// 更实际的代价是新增考量必须手工同步 7 遍，漏一处就是静默的行为不一致——
// PreferSoonestReset（use-it-or-lose-it）至今只在分层链里生效，另外两条选号路径
// 完全不认这个开关；稀缺能力保护第一次接入时也只覆盖了 4 条内联链中的 2 条。
//
// 所以这里收敛成一个比较函数加一份策略：新增维度只在 accountRankBetter 里加一次，
// 各路径通过 accountRankPolicy 表达自己的差异，差异本身也因此变成显式的。

// accountRankPolicy 描述一次候选比较要考虑哪些维度。
type accountRankPolicy struct {
	// preferOAuth 在其余条件持平时偏好 OAuth 账号。
	preferOAuth bool
	// oauthPreferenceGeminiOnly 限定 preferOAuth 的适用范围：仅当两侧都是 Gemini
	// 时才比较账号类型。混合调度分组里会混入 antigravity 账号，跨平台比较账号类型
	// 没有意义，这是混合调度路径原有的行为。
	oauthPreferenceGeminiOnly bool
	// preferSpecialized 启用稀缺能力保护：支持模型集合更窄的账号优先。
	// 放在最久未使用之前——否则空闲的多能力账号会持续抢走本该由专精账号承担的流量。
	preferSpecialized bool
	// ignoreLastUsed 跳过「最久未使用」维度。用于「仅按优先级排序、随后在同优先级内
	// 随机打乱」的调度模式，此时再比较使用时间会让随机化失去意义。
	ignoreLastUsed bool
}

// accountRankBetter 报告 a 是否应当排在 b 之前。
//
// 维度顺序：优先级 → 专精度（可选）→ 从未使用 → 最久未使用 → OAuth 偏好。
func accountRankBetter(a, b *Account, p accountRankPolicy) bool {
	if a == nil || b == nil {
		return a != nil
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if p.preferSpecialized {
		ab, bb := accountModelBreadth(a), accountModelBreadth(b)
		if ab != bb {
			return ab < bb
		}
	}
	if p.ignoreLastUsed {
		return accountOAuthPreferred(a, b, p)
	}
	switch {
	case a.LastUsedAt == nil && b.LastUsedAt != nil:
		return true
	case a.LastUsedAt != nil && b.LastUsedAt == nil:
		return false
	case a.LastUsedAt == nil && b.LastUsedAt == nil:
		return accountOAuthPreferred(a, b, p)
	case a.LastUsedAt.Equal(*b.LastUsedAt):
		// 使用时间持平也要看 OAuth 偏好。这是收敛后的统一口径：selectByLRU 一直是这么
		// 做的（它先取最小时间的并列组再比类型），而四条内联比较链只在两侧都从未使用时
		// 才比类型，时间相等就直接判平。后者看不出是有意为之，更像是漏了这一种情况。
		return accountOAuthPreferred(a, b, p)
	default:
		return a.LastUsedAt.Before(*b.LastUsedAt)
	}
}

func accountOAuthPreferred(a, b *Account, p accountRankPolicy) bool {
	if !p.preferOAuth || a.Type == b.Type {
		return false
	}
	if p.oauthPreferenceGeminiOnly &&
		(a.Platform != PlatformGemini || b.Platform != PlatformGemini) {
		return false
	}
	return a.Type == AccountTypeOAuth
}

// accountRankTied 报告两个候选在当前策略下不分先后。
func accountRankTied(a, b *Account, p accountRankPolicy) bool {
	return !accountRankBetter(a, b, p) && !accountRankBetter(b, a, p)
}

// pickBestAccountWithRandomTie 选出最优候选；并列时随机取一个以分散负载。
//
// 分层链末端原本用 selectByLRU 承担这件事，它自己又实现了一遍「未用过优先、其次最久
// 未用、再看 OAuth」。改为复用同一个比较函数后，分层链与其余选号路径的口径才真正一致。
func pickBestAccountWithRandomTie(accounts []accountWithLoad, p accountRankPolicy) *accountWithLoad {
	if len(accounts) == 0 {
		return nil
	}
	if len(accounts) == 1 {
		return &accounts[0]
	}
	bestIdx := 0
	for i := 1; i < len(accounts); i++ {
		if accountRankBetter(accounts[i].account, accounts[bestIdx].account, p) {
			bestIdx = i
		}
	}
	tied := make([]int, 0, len(accounts))
	for i := range accounts {
		if accountRankTied(accounts[i].account, accounts[bestIdx].account, p) {
			tied = append(tied, i)
		}
	}
	if len(tied) <= 1 {
		return &accounts[bestIdx]
	}
	return &accounts[tied[mathrand.Intn(len(tied))]]
}

// gatewayRankPolicy 组装网关侧的排序策略。
//
// 稀缺能力保护在这里统一注入：调用方不需要（也不应该）各自记得判断开关，否则又会
// 回到「新增维度要同步 N 处、漏一处就静默不一致」的老路。
func (s *GatewayService) gatewayRankPolicy(preferOAuth bool, opts ...func(*accountRankPolicy)) accountRankPolicy {
	p := accountRankPolicy{
		preferOAuth:       preferOAuth,
		preferSpecialized: s.preferSpecializedAccountsEnabled(),
	}
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

// withGeminiOnlyOAuthPreference 用于混合调度：仅当两侧都是 Gemini 时才按账号类型偏好。
func withGeminiOnlyOAuthPreference(p *accountRankPolicy) { p.oauthPreferenceGeminiOnly = true }

// withoutLastUsed 用于「仅按优先级排序 + 同优先级内随机」的调度模式。
func withoutLastUsed(p *accountRankPolicy) { p.ignoreLastUsed = true }
