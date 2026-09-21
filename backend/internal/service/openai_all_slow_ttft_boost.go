package service

import "context"

const (
	// openAIAllSlowTTFTBoostFactor 是整池都慢时给延迟权重的放大倍数。
	openAIAllSlowTTFTBoostFactor = 3.0
	// openAIAllSlowTTFTMinOverPriority 保证提权后的延迟权重相对优先级有足够优势。
	// 只按倍数放大不够稳：运营可能把 ttft 配得很低而把 priority 配得很高，那样
	// 放大 3 倍仍然压不过优先级，提权就等于没做。
	openAIAllSlowTTFTMinOverPriority = 1.5
)

// openAIBoostTTFTWeightWhenAllSlow 返回本轮评分应使用的延迟权重。
//
// allSlow 为假时原样返回，主力池不受任何影响；为真时取「放大 3 倍」与
// 「优先级权重的 1.5 倍」中的较大者，确保延迟真的成为主导因素而不只是被调高。
//
// 刻意只动这一个权重：负载、队列、错误率仍照常参与，否则会把全部流量焊死在
// 单个"最快"的号上，反而制造热点。
func openAIBoostTTFTWeightWhenAllSlow(ttftWeight, priorityWeight float64, allSlow bool) float64 {
	if !allSlow || ttftWeight <= 0 {
		return ttftWeight
	}
	boosted := ttftWeight * openAIAllSlowTTFTBoostFactor
	if floor := priorityWeight * openAIAllSlowTTFTMinOverPriority; boosted < floor {
		boosted = floor
	}
	return boosted
}

// allOpenAICandidatesSlow 报告候选池是否整体都慢——也就是那道「健康/慢」硬隔离
// 是否已经塌缩成单一档位。
//
// 判定刻意复用分池同一个数据源（延迟追踪器的账号级 p90）和同一个阈值，保证提权
// 恰好在分池失效的那一刻生效，两个机制不会各说各话。
//
// 没有任何账号有延迟读数时返回 false：那种情况下延迟无从比较，提权只会放大噪声。
func (s *OpenAIGatewayService) allOpenAICandidatesSlow(ctx context.Context, candidates []openAIAccountCandidateScore) bool {
	if s == nil || len(candidates) == 0 {
		return false
	}
	threshold, _, enabled := s.openAILatencyAwareFallbackSettings(ctx)
	if !enabled || threshold <= 0 {
		return false
	}
	tracker := s.getOpenAILatencyTracker()
	if tracker == nil {
		return false
	}
	sawReading := false
	for i := range candidates {
		account := candidates[i].account
		if account == nil {
			continue
		}
		tail, ok := tracker.AccountTail(account.ID)
		if !ok {
			// 没读数的账号不能判定为慢，否则它会被当成"全慢"的一员，
			// 把一个其实还没被观测过的池子误判成需要提权。
			return false
		}
		sawReading = true
		if tail <= threshold {
			return false
		}
	}
	return sawReading
}
