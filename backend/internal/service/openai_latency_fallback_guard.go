package service

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

const (
	// openAIGroupAccountIDsTTL 是分组可调度账号清单的缓存时长。判断跑在选号热
	// 路径上，必须节流，不能每个请求都去查库。
	openAIGroupAccountIDsTTL = 30 * time.Second
	// openAILatencyFallbackSkipLogInterval 按分组节流"因为源组仍有健康账号而
	// 跳过兜底"的日志：这条判断每个请求都会走，不节流会淹掉日志。
	openAILatencyFallbackSkipLogInterval = time.Minute
)

type openAIGroupAccountIDsEntry struct {
	ids       []int64
	fetchedAt time.Time
}

// openAIGroupSchedulableAccountIDs 返回分组内 active + schedulable 的账号 ID，
// 带 TTL 缓存。
func (s *OpenAIGatewayService) openAIGroupSchedulableAccountIDs(ctx context.Context, groupID int64) []int64 {
	if s == nil || groupID <= 0 || s.accountRepo == nil {
		return nil
	}
	now := time.Now()
	if value, ok := s.openaiGroupAccountIDs.Load(groupID); ok {
		if entry, _ := value.(*openAIGroupAccountIDsEntry); entry != nil &&
			now.Sub(entry.fetchedAt) < openAIGroupAccountIDsTTL {
			return entry.ids
		}
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, &groupID, []string{PlatformOpenAI}, true)
	if err != nil {
		slog.Warn("openai_latency_fallback_group_accounts_failed", "group_id", groupID, "error", err)
		return nil
	}
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].ID)
	}
	s.openaiGroupAccountIDs.Store(groupID, &openAIGroupAccountIDsEntry{ids: ids, fetchedAt: now})
	return ids
}

// openAIGroupHasHealthyAccount 报告分组内是否还有首字延迟达标的账号。
//
// 存在的理由是一处统计量错配：延迟兜底的去留判断用的是**分组级 p90**，而调度
// 最终挑的是**单个账号**。分组 p90 会被组里最慢的成员拉高，于是一个拥有快号的
// 分组会被整体判为"慢"，流量被送去别的池子——哪怕那个池子里每一个账号都比
// 本组最快的账号更慢。
//
// 2026-09-15 生产实况：plus-free(20) 的 183 首字 7.5 秒，而被借号的兜底池 29 里
// 198/223/221 分别是 30.3 / 36.7 / 38.3 秒，全部高于 30 秒阈值。组级比较却因为
// 20 的 p90 被 155（93.5 秒）拉高而通过了"目标更快"的检查，结果把流量从一个
// 有 7.5 秒快号的组，导向了一个整体更慢的池子，既慢又亏（那一单成本是收入的
// 16 倍）。
//
// 因此：源组只要还有一个健康账号，就不该借号——让组内调度去挑它。只有当源组
// 所有账号都慢时，兜底才是真正的改善。
func (s *OpenAIGatewayService) openAIGroupHasHealthyAccount(
	ctx context.Context, groupID int64, thresholdMs int,
) (bool, int64, int) {
	if s == nil || groupID <= 0 || thresholdMs <= 0 {
		return false, 0, 0
	}
	tracker := s.getOpenAILatencyTracker()
	if tracker == nil {
		return false, 0, 0
	}
	accountIDs := s.openAIGroupSchedulableAccountIDs(ctx, groupID)
	bestID, bestTail := int64(0), 0
	for _, accountID := range accountIDs {
		tail, ok := tracker.AccountTail(accountID)
		if !ok {
			// 没有足够样本：不能当作健康，但也不该据此否定别的账号。
			continue
		}
		if tail <= thresholdMs && (bestID == 0 || tail < bestTail) {
			bestID, bestTail = accountID, tail
		}
	}
	return bestID > 0, bestID, bestTail
}

// logOpenAILatencyFallbackSkipped 按分组节流输出跳过原因，保证"为什么没兜底"
// 在生产上看得见，又不会把日志淹掉。
func (s *OpenAIGatewayService) logOpenAILatencyFallbackSkipped(
	groupID int64, groupTail int, accountID int64, accountTail, thresholdMs int,
) {
	if s == nil {
		return
	}
	value, _ := s.openaiLatencyFallbackSkipLogAt.LoadOrStore(groupID, &atomic.Int64{})
	last, _ := value.(*atomic.Int64)
	if last == nil {
		return
	}
	now := time.Now().UnixMilli()
	prev := last.Load()
	if prev != 0 && now-prev < openAILatencyFallbackSkipLogInterval.Milliseconds() {
		return
	}
	if !last.CompareAndSwap(prev, now) {
		return
	}
	slog.Info("openai_latency_fallback_skipped_group_has_healthy_account",
		"group_id", groupID,
		"group_ttft_p90_ms", groupTail,
		"healthy_account_id", accountID,
		"healthy_account_ttft_ms", accountTail,
		"threshold_ms", thresholdMs,
	)
}
