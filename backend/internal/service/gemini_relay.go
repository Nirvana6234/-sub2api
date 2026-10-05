package service

import (
	"context"
	"time"
)

// 主从分流（开发计划 WP10）：Gemini 原生入口转发路径上写账号状态的几处（429 的账号级限流、清掉粘性会话绑定）在从节点上
// 交给主节点照写。下面是主节点执行从节点事件用的入口，写法与本地同一段代码。

// RelaySetRateLimited 写账号级限流（本地 handleGeminiUpstreamError 里 429 的结果）。
func (s *GeminiMessagesCompatService) RelaySetRateLimited(ctx context.Context, account *Account, resetAt time.Time) error {
	if s == nil || s.accountRepo == nil || account == nil || !resetAt.After(time.Now()) {
		return nil
	}
	return s.accountRepo.SetRateLimited(ctx, account.ID, clampRelayResetAt(resetAt))
}

// RelayGeminiCooldown 按档位冷却写账号级限流（本地 429 没有上游重置时间、Code Assist / Google One 账号的分支）。
func (s *GeminiMessagesCompatService) RelayGeminiCooldown(ctx context.Context, account *Account) error {
	if s == nil || s.accountRepo == nil || account == nil {
		return nil
	}
	cooldown := geminiCooldownForTier(account.GeminiTierID())
	if s.rateLimitService != nil {
		cooldown = s.rateLimitService.GeminiCooldown(ctx, account)
	}
	return s.accountRepo.SetRateLimited(ctx, account.ID, clampRelayResetAt(time.Now().Add(cooldown)))
}

// RelayClearStickySession 清掉粘性会话绑定（转发路径上 Antigravity 限流、重试失败时本地 clearStickySession）。
func (s *GeminiMessagesCompatService) RelayClearStickySession(ctx context.Context, groupID int64, sessionKey string) {
	if s == nil || s.cache == nil || sessionKey == "" {
		return
	}
	_ = s.cache.DeleteSessionAccountID(ctx, groupID, sessionKey)
}
