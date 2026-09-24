package service

import "sort"

// splitOpenAICandidatesByPriorityTier 按组内优先级把候选切成有序档位，数值小的档位在前。
//
// 档位内部保持入参顺序，交由上层既有的排序规则（稀缺能力保护 → 延迟分池 → topK
// 加权随机）各自处理，本函数只负责"档与档之间谁先谁后"。
//
// 为什么需要它：打分里的 priorityFactor 是对候选池做 min-max 归一化
// （1 - (p-min)/(max-min)），量纲完全取决于池内的离群值。2026-09-20 生产实测，
// plus(2) 的优先级集合是 {1,1,1,2,2,3,50}，分母被那个 50 撑到 49，于是优先级 1 的
// 120 和优先级 3 的 221 之间只差 0.041 分，而 TTFT 因子满量程能给 0.75 分——管理员
// 手工排的优先级只贡献了两者总差距的约 5%，实际等于没排。叠加
// buildOpenAIWeightedSelectionOrder 的 +1.0 保底（见该函数注释），池内最差的账号
// 仍能拿到约 9% 的会话链头；而粘性权重会把一次链头放大成整条会话，所以运营看到的
// 就是"流量总往最差的号上跑"。
//
// 分档而不是调权重，是因为管理员填的优先级本来就是档位语义（"优先级 1 的没压满就
// 别碰 2"），把它塞进同一个加权和里去和延迟、负载互相抵消，怎么调系数都只是在换一
// 个抵消比例。
func splitOpenAICandidatesByPriorityTier(pool []openAIAccountCandidateScore) [][]openAIAccountCandidateScore {
	if len(pool) == 0 {
		return nil
	}
	buckets := make(map[int][]openAIAccountCandidateScore, len(pool))
	priorities := make([]int, 0, len(pool))
	for _, candidate := range pool {
		if _, seen := buckets[candidate.priority]; !seen {
			priorities = append(priorities, candidate.priority)
		}
		buckets[candidate.priority] = append(buckets[candidate.priority], candidate)
	}
	sort.Ints(priorities)

	tiers := make([][]openAIAccountCandidateScore, 0, len(priorities))
	for _, priority := range priorities {
		tiers = append(tiers, buckets[priority])
	}
	return tiers
}

// globalTopKOpenAICandidates 返回不分档时本轮真正会进入候选的那一批账号。
//
// 分档之后每一档各留 topK 个名额，合起来比原先的全局 topK 宽。判断"绑定账号该不该
// 跨档置顶"时必须拿这个全局口径，否则 topK 之外的账号会借分档复活。
func globalTopKOpenAICandidates(pool []openAIAccountCandidateScore, topK int) []openAIAccountCandidateScore {
	if topK <= 0 {
		return nil
	}
	if topK >= len(pool) {
		return pool
	}
	return selectTopKOpenAICandidates(pool, topK)
}

// openAIStickyBoundAccountID 返回本次请求已经绑定、且确实在候选池里的账号 ID，
// 没有绑定时返回 0。
//
// 取值顺序与 buildOpenAISelectionOrder 里的档内置顶逻辑保持一致：先看
// previous_response 绑定，再看会话粘性绑定，且都必须命中候选池才算数。
func openAIStickyBoundAccountID(req OpenAIAccountScheduleRequest, pool []openAIAccountCandidateScore) int64 {
	if !req.StickyWeighted {
		return 0
	}
	for _, stickyID := range []int64{req.StickyPreviousAccountID, req.StickyAccountID} {
		if stickyID <= 0 {
			continue
		}
		for _, candidate := range pool {
			if candidate.account != nil && candidate.account.ID == stickyID {
				return stickyID
			}
		}
	}
	return 0
}

// demoteOpenAICandidate 把指定账号压到选号序列最后，其余顺序不变。
//
// 用于粘性逃逸：逃逸已经判定这个号当前不可用（TTFT 差 / 错误率高 / 并发满），
// 优先级分档不能再按优先级把它提回最前。只降级不剔除——后面的号全都拿不到槽位时，
// 它仍然是最后一条出路，这和本包"排序而不过滤"的一贯取向一致。
func demoteOpenAICandidate(order []openAIAccountCandidateScore, accountID int64) []openAIAccountCandidateScore {
	if accountID <= 0 || len(order) < 2 {
		return order
	}
	for i, candidate := range order {
		if candidate.account == nil || candidate.account.ID != accountID {
			continue
		}
		if i == len(order)-1 {
			return order
		}
		demoted := make([]openAIAccountCandidateScore, 0, len(order))
		demoted = append(demoted, order[:i]...)
		demoted = append(demoted, order[i+1:]...)
		return append(demoted, candidate)
	}
	return order
}

// hoistOpenAICandidate 把指定账号提到选号序列最前面，其余顺序不变。
//
// 分档必须让位于粘性：previous_response 链一旦换号就断了，用户会直接收到错误。
// 档内置顶是 buildOpenAISelectionOrder 原本就有的行为，分档之后还要再跨档提一次，
// 否则一个绑在低优先级档上的会话会被前面整档的账号挤到后面去。
func hoistOpenAICandidate(order []openAIAccountCandidateScore, accountID int64) []openAIAccountCandidateScore {
	if accountID <= 0 || len(order) < 2 {
		return order
	}
	for i, candidate := range order {
		if candidate.account == nil || candidate.account.ID != accountID {
			continue
		}
		if i == 0 {
			return order
		}
		hoisted := make([]openAIAccountCandidateScore, 0, len(order))
		hoisted = append(hoisted, candidate)
		hoisted = append(hoisted, order[:i]...)
		hoisted = append(hoisted, order[i+1:]...)
		return hoisted
	}
	return order
}
