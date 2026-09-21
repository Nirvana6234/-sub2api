package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// productionPlusPoolCandidates 复刻 2026-09-20 生产 plus(2) 的候选池形态。
//
// 分数是按生产实际权重（priority=1, load=1, queue=0.7, error_rate=8, ttft=0.75,
// upstream_cost=1.5）算出的可区分部分：公共项在 (score-minScore) 里会抵消，真正拉开
// 差距的只有 priority 因子和 TTFT 因子。优先级集合 {1,1,1,2,2,3,50} 里那个 50 把
// min-max 归一化的分母撑到 49，于是优先级 1 与 3 之间只剩 0.041 分，而 TTFT 满量程
// 有 0.75 分——这正是"手工排的优先级失效"的数值来源。
//
// 注意 155（优先级 2，1.685 分）的总分高于 120（优先级 1，1.610 分）：跨档倒挂是
// 真实存在的，所以"按总分排"和"按档位排"会给出不同答案，用例才有区分力。
func productionPlusPoolCandidates() []openAIAccountCandidateScore {
	return []openAIAccountCandidateScore{
		{account: &Account{ID: 182, GroupIDs: []int64{2}}, loadInfo: &AccountLoadInfo{}, priority: 1, score: 1.750},
		{account: &Account{ID: 155, GroupIDs: []int64{2, 20}}, loadInfo: &AccountLoadInfo{}, priority: 2, score: 1.685},
		{account: &Account{ID: 120, GroupIDs: []int64{2}}, loadInfo: &AccountLoadInfo{}, priority: 1, score: 1.610},
		{account: &Account{ID: 184, GroupIDs: []int64{2}}, loadInfo: &AccountLoadInfo{}, priority: 1, score: 1.487},
		{account: &Account{ID: 183, GroupIDs: []int64{2, 20}}, loadInfo: &AccountLoadInfo{}, priority: 2, score: 1.425},
		{account: &Account{ID: 221, GroupIDs: []int64{2, 29, 37}}, loadInfo: &AccountLoadInfo{}, priority: 3, score: 0.959},
		{account: &Account{ID: 175, GroupIDs: []int64{2}}, loadInfo: &AccountLoadInfo{}, priority: 50, score: 0.375},
	}
}

func priorityOfAccountInOrder(t *testing.T, order []openAIAccountCandidateScore) []int {
	t.Helper()
	priorities := make([]int, 0, len(order))
	for _, candidate := range order {
		require.NotNil(t, candidate.account)
		priorities = append(priorities, candidate.priority)
	}
	return priorities
}

// 组内优先级必须是档位语义：优先级 1 的号全部排在优先级 2 之前，2 全部排在 3 之前。
//
// 可证伪性：把 buildSelectionOrder 改回直接 rankSpecializedFirst(pool)（即回到按总分
// 加权随机），155/221 就会穿插到优先级 1 的号前面，这里必炸。扫 60 个不同的会话锚点
// 是为了排除"某一个种子恰好抽出了顺序结果"的侥幸。
func TestSelectionOrderIsStrictlyPriorityTiered(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}

	for i := 0; i < 60; i++ {
		req := OpenAIAccountScheduleRequest{
			SessionHash:    fmt.Sprintf("sess-%d", i),
			RequestedModel: "gpt-5.6-sol",
		}
		order := scheduler.buildOpenAISelectionOrder(req, openAIAccountLoadPlan{
			candidates: productionPlusPoolCandidates(),
			topK:       10,
		})
		require.Len(t, order, 7, "第 %d 轮：分档是排序不是过滤，一个都不能少", i)

		priorities := priorityOfAccountInOrder(t, order)
		for j := 1; j < len(priorities); j++ {
			require.LessOrEqualf(t, priorities[j-1], priorities[j],
				"第 %d 轮：优先级 %v 出现倒挂——低优先级账号排到了高优先级前面",
				i, priorities)
		}
	}
}

// 221 是兜底组那个 0.04x 中转号，组内优先级 3、延迟最差。它必须排在所有优先级 1、2
// 的号之后，但**不能**被剔除——前面整档全部满载或抢不到并发槽时还要靠它兜住。
func TestWorstTierAccountRanksLastButStaysAvailable(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}

	order := scheduler.buildOpenAISelectionOrder(
		OpenAIAccountScheduleRequest{SessionHash: "admin-chain", RequestedModel: "gpt-5.6-sol"},
		openAIAccountLoadPlan{candidates: productionPlusPoolCandidates(), topK: 10},
	)

	positions := make(map[int64]int, len(order))
	for i, candidate := range order {
		positions[candidate.account.ID] = i
	}

	for _, betterID := range []int64{182, 120, 184, 155, 183} {
		require.Lessf(t, positions[betterID], positions[221],
			"账号 %d 的组内优先级高于 221，必须排在它前面", betterID)
	}
	require.Contains(t, positions, int64(221),
		"不得把低优先级账号从序列里删掉——高优先级档全满时它是唯一的降级出路")
	require.Equal(t, len(order)-1, positions[175],
		"优先级 50 是运营明确的最后一档，必须排在最末")
}

// 分档必须让位于粘性：会话已经绑在低优先级账号上时，它仍要排在最前面。
// 否则 previous_response 链会被前面整档的账号挤断，用户直接收到错误。
func TestStickyAccountSurvivesPriorityTiering(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}

	req := OpenAIAccountScheduleRequest{
		SessionHash:             "bound-session",
		RequestedModel:          "gpt-5.6-sol",
		StickyWeighted:          true,
		StickyAccountID:         221,
		PreviousResponseCanMove: false,
	}
	order := scheduler.buildOpenAISelectionOrder(req, openAIAccountLoadPlan{
		candidates: productionPlusPoolCandidates(),
		topK:       10,
	})

	require.Equal(t, int64(221), order[0].account.ID,
		"已绑定的会话不受分档影响，换号就意味着把用户的会话链打断")
	require.Len(t, order, 7, "置顶只是移动，不得丢账号")
}

// previous_response 绑定同样要跨档置顶。
func TestStickyPreviousResponseAccountSurvivesPriorityTiering(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}

	order := scheduler.buildOpenAISelectionOrder(OpenAIAccountScheduleRequest{
		SessionHash:             "prev-session",
		RequestedModel:          "gpt-5.6-sol",
		StickyWeighted:          true,
		StickyPreviousAccountID: 183,
		PreviousResponseCanMove: true,
	}, openAIAccountLoadPlan{candidates: productionPlusPoolCandidates(), topK: 10})

	require.Equal(t, int64(183), order[0].account.ID)
}

// 同一档位内部不分档，完全交回原有的加权随机——负载均衡在档内照常工作。
func TestSinglePriorityTierKeepsWeightedRandomBehaviour(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}
	candidates := []openAIAccountCandidateScore{
		{account: &Account{ID: 1}, loadInfo: &AccountLoadInfo{}, priority: 1, score: 3},
		{account: &Account{ID: 2}, loadInfo: &AccountLoadInfo{}, priority: 1, score: 2},
		{account: &Account{ID: 3}, loadInfo: &AccountLoadInfo{}, priority: 1, score: 1},
	}

	seen := make(map[int64]int)
	for i := 0; i < 80; i++ {
		order := scheduler.buildOpenAISelectionOrder(
			OpenAIAccountScheduleRequest{SessionHash: fmt.Sprintf("s-%d", i)},
			openAIAccountLoadPlan{candidates: candidates, topK: 3},
		)
		require.Len(t, order, 3)
		seen[order[0].account.ID]++
	}
	require.Len(t, seen, 3,
		"同档位内仍应是加权随机：三个号都要有机会排第一，否则档内负载均衡没了")
}

// 粘性逃逸已经判定某个号当前不可用（TTFT 差 / 错误率高 / 并发满），分档不得按优先级
// 把它重新提到最前——否则会话永远卡在坏号上，逃逸判定形同虚设。
//
// 这条是加分档时真炸出来的：21101 优先级 0（更好）但已被逃逸，21102 优先级 1。
func TestEscapedStickyAccountIsDemotedDespiteBetterPriority(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}
	candidates := []openAIAccountCandidateScore{
		{account: &Account{ID: 21101}, loadInfo: &AccountLoadInfo{}, priority: 0, score: 5},
		{account: &Account{ID: 21102}, loadInfo: &AccountLoadInfo{}, priority: 1, score: 1},
	}

	order := scheduler.buildOpenAISelectionOrder(OpenAIAccountScheduleRequest{
		SessionHash:            "escaped",
		EscapedStickyAccountID: 21101,
	}, openAIAccountLoadPlan{candidates: candidates, topK: 10})

	require.Equal(t, int64(21102), order[0].account.ID,
		"被逃逸的号优先级更好也不能排第一，否则换号换了个寂寞")
	require.Equal(t, int64(21101), order[len(order)-1].account.ID,
		"只降级不剔除：后面的号都抢不到槽位时它仍是最后的出路")
}

// top-K 之外的绑定账号不得借分档复活。
//
// 分档给每档各留 topK 名额，合起来比全局 topK 宽；LBTopK=1 时全局只有一个名额，
// 绑定账号不在其中就应该换号。这条同样是加分档时炸出来的。
func TestStickyAccountOutsideGlobalTopKIsNotHoisted(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}
	candidates := []openAIAccountCandidateScore{
		{account: &Account{ID: 37111}, loadInfo: &AccountLoadInfo{}, priority: 100, score: 0},
		{account: &Account{ID: 37112}, loadInfo: &AccountLoadInfo{}, priority: 0, score: 1},
	}

	order := scheduler.buildOpenAISelectionOrder(OpenAIAccountScheduleRequest{
		SessionHash:             "unmovable",
		StickyWeighted:          true,
		StickyPreviousAccountID: 37111,
		PreviousResponseCanMove: true,
	}, openAIAccountLoadPlan{candidates: candidates, topK: 1})

	require.Equal(t, int64(37112), order[0].account.ID,
		"全局 top-K 只留一个名额，绑定账号 37111 不在其中，分档不该把它救回来")
}

func TestSplitOpenAICandidatesByPriorityTier(t *testing.T) {
	t.Run("按优先级升序分档且档内保序", func(t *testing.T) {
		tiers := splitOpenAICandidatesByPriorityTier([]openAIAccountCandidateScore{
			{account: &Account{ID: 221}, priority: 3},
			{account: &Account{ID: 182}, priority: 1},
			{account: &Account{ID: 120}, priority: 1},
			{account: &Account{ID: 183}, priority: 2},
		})
		require.Len(t, tiers, 3)
		require.Equal(t, []int64{182, 120},
			[]int64{tiers[0][0].account.ID, tiers[0][1].account.ID})
		require.Equal(t, int64(183), tiers[1][0].account.ID)
		require.Equal(t, int64(221), tiers[2][0].account.ID)
	})

	t.Run("空池返回空", func(t *testing.T) {
		require.Nil(t, splitOpenAICandidatesByPriorityTier(nil))
	})

	t.Run("同优先级归为一档", func(t *testing.T) {
		tiers := splitOpenAICandidatesByPriorityTier([]openAIAccountCandidateScore{
			{account: &Account{ID: 1}, priority: 7},
			{account: &Account{ID: 2}, priority: 7},
		})
		require.Len(t, tiers, 1)
		require.Len(t, tiers[0], 2)
	})
}

func TestHoistOpenAICandidate(t *testing.T) {
	order := []openAIAccountCandidateScore{
		{account: &Account{ID: 1}}, {account: &Account{ID: 2}}, {account: &Account{ID: 3}},
	}

	t.Run("提到最前其余保序", func(t *testing.T) {
		hoisted := hoistOpenAICandidate(order, 3)
		require.Equal(t, []int64{3, 1, 2},
			[]int64{hoisted[0].account.ID, hoisted[1].account.ID, hoisted[2].account.ID})
	})

	t.Run("不在序列里就原样返回", func(t *testing.T) {
		require.Len(t, hoistOpenAICandidate(order, 99), 3)
		require.Equal(t, int64(1), hoistOpenAICandidate(order, 99)[0].account.ID)
	})

	t.Run("没有绑定时不动", func(t *testing.T) {
		require.Equal(t, int64(1), hoistOpenAICandidate(order, 0)[0].account.ID)
	})
}

func TestOpenAIStickyBoundAccountID(t *testing.T) {
	pool := productionPlusPoolCandidates()

	t.Run("未启用粘性时不返回绑定", func(t *testing.T) {
		require.Zero(t, openAIStickyBoundAccountID(
			OpenAIAccountScheduleRequest{StickyAccountID: 221}, pool))
	})

	t.Run("previous_response 绑定优先于会话绑定", func(t *testing.T) {
		require.Equal(t, int64(183), openAIStickyBoundAccountID(OpenAIAccountScheduleRequest{
			StickyWeighted: true, StickyPreviousAccountID: 183, StickyAccountID: 221,
		}, pool))
	})

	t.Run("绑定的号不在候选池里就不算数", func(t *testing.T) {
		require.Zero(t, openAIStickyBoundAccountID(OpenAIAccountScheduleRequest{
			StickyWeighted: true, StickyAccountID: 999,
		}, pool))
	})
}
