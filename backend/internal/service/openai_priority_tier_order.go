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
// buildOpenAIWeightedSelectionOrder 的 +1.0 保底（把池内最差账号的抽中权重也托到
// 最好账号的一半上下），优先级 3 那个 0.04x 中转号在 24 小时里拿到了约 9% 的会话
// 链头；粘性又把一次链头放大成整条会话，所以运营看到的就是"流量总往最差的号上跑"。
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

// openAIStickyBoundAccountID 返回本次请求已经绑定、且确实在候选池里的账号 ID，
// 没有绑定时返回 0。
//
// 取值顺序与打分里的粘性加成一致：先看 previous_response 绑定，再看会话粘性绑定。
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

// promoteOpenAIStickyCandidateToFirstTier 把已绑定会话的账号并入首档一起竞争。
//
// 分档解决的是"新会话该从哪一档起头"——生产上出问题的正是会话链头的分配。但已经
// 绑定的会话换号有真实代价（上游侧的上下文与缓存、previous_response 链），这个代价
// 不该被档位一刀切掉，所以绑定账号仍要有胜出的机会。
//
// 刻意做成"并入首档竞争"而不是"提到最前"：本包的既定设计是粘性只作为打分加成参与
// 竞争、不强制置顶（见
// TestOpenAIGatewayService_SelectAccountWithScheduler_StickyWeightedSessionInTopKDoesNotForceStickyFirst
// ——64 个会话全绑在优先级 100 的号上，断言两个号都必须被选中过）。并入首档后，
// 绑定账号带着 weights.SessionSticky / weights.Previous 的加成和首档账号同场加权随机，
// 既不被档位排除，也不垄断。
//
// 不能移动的 previous_response 绑定另有 openAIAccountScheduleLayerPreviousResponse
// 这一层在负载均衡之前拦下，不依赖这里的顺序。
func promoteOpenAIStickyCandidateToFirstTier(
	tiers [][]openAIAccountCandidateScore,
	req OpenAIAccountScheduleRequest,
) [][]openAIAccountCandidateScore {
	if len(tiers) < 2 {
		return tiers
	}
	stickyID := int64(0)
	for _, tier := range tiers {
		if stickyID = openAIStickyBoundAccountID(req, tier); stickyID > 0 {
			break
		}
	}
	if stickyID <= 0 {
		return tiers
	}
	for tierIndex := 1; tierIndex < len(tiers); tierIndex++ {
		for i, candidate := range tiers[tierIndex] {
			if candidate.account == nil || candidate.account.ID != stickyID {
				continue
			}
			rest := make([]openAIAccountCandidateScore, 0, len(tiers[tierIndex])-1)
			rest = append(rest, tiers[tierIndex][:i]...)
			rest = append(rest, tiers[tierIndex][i+1:]...)

			promoted := make([][]openAIAccountCandidateScore, len(tiers))
			copy(promoted, tiers)
			promoted[0] = append(append([]openAIAccountCandidateScore(nil), tiers[0]...), candidate)
			promoted[tierIndex] = rest
			return promoted
		}
	}
	return tiers
}
