package service

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// openAISelectionGroupAuditLogInterval 按 (分组, 账号) 节流告警。异常一旦发生
// 往往是整段会话持续复现，不节流会瞬间淹掉日志。
const openAISelectionGroupAuditLogInterval = time.Minute

// auditSelectedAccountGroupMembership 核对"选出来的账号是否属于被请求的分组"。
//
// 纯观测：不修改选号结果、不影响任何调度决策。命中异常才输出一条 WARN。
//
// 正常情况下这条日志永远不该出现——候选池本身就是按分组取的，粘性命中路径也
// 各自调用了 openAIAccountMatchesSchedulingGroup。它存在是因为生产上出现了静态
// 排查无法解释的现象，需要在真实流量上定位究竟是哪条路径把非成员账号交了出来。
//
// 走兜底链路时账号本来就该来自兜底组，那是设计内的跨组借号，不算异常，因此这里
// 跳过；判定用的分组是兜底后真正生效的服务分组。
func (s *OpenAIGatewayService) auditSelectedAccountGroupMembership(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	previousResponseID string,
	requestedModel string,
	selection *AccountSelectionResult,
) {
	if s == nil || selection == nil || selection.Account == nil || groupID == nil || *groupID <= 0 {
		return
	}
	servingGroupID := OpenAIServingGroupID(ctx, *groupID)
	if servingGroupID <= 0 {
		return
	}
	account := selection.Account
	if openAIStickyAccountMatchesGroup(account, &servingGroupID) {
		return
	}
	if len(account.GroupIDs) == 0 && len(account.AccountGroups) == 0 {
		return
	}
	if !s.shouldLogSelectionGroupAudit(servingGroupID, account.ID) {
		return
	}

	memberOf := make([]string, 0, len(account.GroupIDs)+len(account.AccountGroups))
	seen := make(map[int64]struct{}, cap(memberOf))
	for _, id := range account.GroupIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		memberOf = append(memberOf, strconv.FormatInt(id, 10))
	}
	for _, ag := range account.AccountGroups {
		if _, dup := seen[ag.GroupID]; dup {
			continue
		}
		seen[ag.GroupID] = struct{}{}
		memberOf = append(memberOf, strconv.FormatInt(ag.GroupID, 10))
	}

	slog.Warn("openai_selection_account_not_in_requested_group",
		"requested_group_id", *groupID,
		"serving_group_id", servingGroupID,
		"account_id", account.ID,
		"account_name", account.Name,
		"account_member_of", strings.Join(memberOf, ","),
		"model", requestedModel,
		// 这三个字段用来区分是哪条路径交出的账号：粘性会话、previous_response
		// 粘连，还是常规候选池（两者都为空时）。
		"has_session_hash", sessionHash != "",
		"has_previous_response_id", strings.TrimSpace(previousResponseID) != "",
		"fallback_sourcing", isOpenAIFallbackPoolSourcing(ctx),
		"sticky_fallback_request", isOpenAIStickyFallbackRequest(ctx),
	)
}

func (s *OpenAIGatewayService) shouldLogSelectionGroupAudit(groupID, accountID int64) bool {
	key := strconv.FormatInt(groupID, 10) + ":" + strconv.FormatInt(accountID, 10)
	value, _ := s.openaiSelectionGroupAuditLogAt.LoadOrStore(key, &atomic.Int64{})
	last, _ := value.(*atomic.Int64)
	if last == nil {
		return false
	}
	now := time.Now().UnixMilli()
	prev := last.Load()
	if prev != 0 && now-prev < openAISelectionGroupAuditLogInterval.Milliseconds() {
		return false
	}
	return last.CompareAndSwap(prev, now)
}

// selectionEscapedRequestedGroup 是"选出的账号必须属于服务分组"这条不变量的
// 统一闸门：命中越界时作废本次选号（释放并发槽、清掉越界的粘性绑定），并报告
// 调用方需要重选。
//
// 为什么要在汇合点兜一道：这条不变量原本散落在各条选号路径里各查各的
// （selectBySessionHash / 粘性命中路径各有一次 openAIAccountMatchesSchedulingGroup，
// 负载均衡路径则依赖 recheckSelectedOpenAIAccountFromDB —— 而后者在
// schedulerSnapshot 或 accountRepo 为空的早退分支里并不校验分组）。与其继续逐条
// 排查，不如把不变量收口到唯一出口强制执行。
func (s *OpenAIGatewayService) selectionEscapedRequestedGroup(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	previousResponseID string,
	requestedModel string,
	selection *AccountSelectionResult,
) bool {
	if s == nil || selection == nil || selection.Account == nil || groupID == nil || *groupID <= 0 {
		return false
	}
	// 简单模式下分组归属本就是被刻意忽略的（见
	// TestOpenAIGatewayService_PreviousResponseSimpleModeIgnoresGroupMembership：
	// previous_response 绑定的账号即便不属于请求分组也照用）。这条不变量在该模式
	// 下不成立，闸门必须整体让开。
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return false
	}
	account := selection.Account
	// 分组归属没随账号带出来时无从判定，不能据此作废——否则只要某条路径没水合
	// AccountGroups，正常流量就会被全部打掉。
	if len(account.GroupIDs) == 0 && len(account.AccountGroups) == 0 {
		return false
	}

	escaped := false
	if servingGroupID, ok := s.openAISelectionServingGroupID(ctx, *groupID, selection); ok && servingGroupID > 0 {
		escaped = !openAIStickyAccountMatchesGroup(account, &servingGroupID)
	} else {
		// 判不出服务分组时，仍然有一类越界是确定的：账号既不属于请求分组，也不属于
		// 沿兜底链能到达的任何分组。
		//
		// 2026-09-20 生产实测：gpt-6-astra 请求 plus(2)，落到只属 gpt-pro(34) 的
		// 账号 320（20:30–20:47 共 10 次，横跨两个 auto-group Key）。plus 配了兜底
		// [29]，若只因"配了兜底"就判定无从判定并放行，这类越界一次都拦不住——而 34
		// 既不是 2 也不在 2 的兜底链上。
		escaped = s.openAIAccountOutsideGroupAndFallbacks(ctx, *groupID, account)
	}
	if !escaped {
		return false
	}

	s.auditSelectedAccountGroupMembership(ctx, groupID, sessionHash, previousResponseID, requestedModel, selection)

	// 必须先还并发槽再丢弃结果，否则这个槽位会被永久占住。
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	// 清掉把越界账号焊死在这个分组上的粘性绑定，否则下一次请求还会命中它。
	if strings.TrimSpace(sessionHash) != "" {
		_ = s.deleteStickySessionAccountID(ctx, groupID, sessionHash)
	}
	return true
}

// openAISelectionServingGroupID 判定本次选号真正的"服务分组"，并报告这个判定
// 是否可信。
//
// 必须优先用 selection.fallbackPoolUsageTrace，不能只读 ctx：兜底状态安装在调度栈
// 内部的局部 ctx 上（nextOpenAIFallbackGroup 返回的 fallbackCtx 随函数栈一起丢弃），
// 传不到选号汇合点。该 trace 则由 attachSelectionProfitGate 在选号返回点捕获，是唯一
// 能跨出调度栈的兜底事实。
//
// 2026-09-20 的教训：第一版闸门只读 ctx，于是每一次合法兜底在汇合点看到的都是
// 「serving_group == 请求组、fallback_sourcing == false」，被误判成越界并作废重选
// ——上线后实时打掉了 plus(2) 走兜底池(29) 的正常选号。当时那条"兜底不得误伤"的
// 测试是用人工构造的 ctx 跑的，而生产里这个 ctx 根本到不了汇合点，测试通过给了
// 虚假的信心。
//
// ok=false 表示无从判定，调用方必须放行而不是作废。
func (s *OpenAIGatewayService) openAISelectionServingGroupID(
	ctx context.Context, requestedGroupID int64, selection *AccountSelectionResult,
) (int64, bool) {
	if s == nil || selection == nil || requestedGroupID <= 0 {
		return 0, false
	}
	// 1) 选号结果自带的兜底事实最可信。
	if selection.fallbackPoolUsageTrace != nil && selection.fallbackPoolUsageTrace.TargetGroupID > 0 {
		return selection.fallbackPoolUsageTrace.TargetGroupID, true
	}
	// 2) ctx 上确实带着兜底状态时（同栈内调用）也采信。
	if serving := OpenAIServingGroupID(ctx, requestedGroupID); serving > 0 && serving != requestedGroupID {
		return serving, true
	}
	// 3) 没有任何兜底迹象。但"没有迹象"不等于"确实没兜底"——兜底链路若没能带出
	//    trace，这里就会把合法兜底误判成越界。所以只有在请求分组压根没配兜底时，
	//    才敢断言服务分组就是请求分组；配了兜底的分组交给调用方的兜底链判定。
	if s.openAIGroupMayFallback(ctx, requestedGroupID) {
		return 0, false
	}
	return requestedGroupID, true
}

// openAIAccountOutsideGroupAndFallbacks 报告账号是否「确定地」既不属于请求分组，
// 也不属于沿兜底链能到达的任何分组。
//
// 这是 openAISelectionServingGroupID 判不出服务分组时唯一还能安全执行的断言：
// 无论这次兜底有没有发生、兜到了第几层，合法的选号结果都必然落在这个集合之内；
// 落在集合之外的账号，不管兜底状态如何都不该被交出来。因此它不可能误伤正常兜底
// ——兜底组里的账号按定义就在集合里。
//
// 兜底链上只要有一环读不到配置，集合就是不完整的，一律返回 false（放行），沿用
// 闸门「宁可漏拦不可误杀」的取向。
func (s *OpenAIGatewayService) openAIAccountOutsideGroupAndFallbacks(
	ctx context.Context, requestedGroupID int64, account *Account,
) bool {
	if s == nil || s.schedulerSnapshot == nil || account == nil || requestedGroupID <= 0 {
		return false
	}
	allowed := make(map[int64]struct{}, 4)
	pending := []int64{requestedGroupID}
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		if _, seen := allowed[current]; seen {
			continue
		}
		allowed[current] = struct{}{}
		group, err := s.schedulerSnapshot.GetGroupByID(ctx, current)
		if err != nil || group == nil {
			return false
		}
		pending = append(pending, normalizeFallbackGroupIDs(group.FallbackGroupIDs, group.FallbackGroupID)...)
	}
	for candidateGroupID := range allowed {
		groupID := candidateGroupID
		if openAIStickyAccountMatchesGroup(account, &groupID) {
			return false
		}
	}
	return true
}

// openAIGroupMayFallback 报告该分组是否可能把请求兜底到别的分组。
// 读不到配置时保守返回 true（视为可能兜底），让闸门放行。
func (s *OpenAIGatewayService) openAIGroupMayFallback(ctx context.Context, groupID int64) bool {
	if s == nil || s.schedulerSnapshot == nil || groupID <= 0 {
		return true
	}
	group, err := s.schedulerSnapshot.GetGroupByID(ctx, groupID)
	if err != nil || group == nil {
		return true
	}
	return len(normalizeFallbackGroupIDs(group.FallbackGroupIDs, group.FallbackGroupID)) > 0
}
