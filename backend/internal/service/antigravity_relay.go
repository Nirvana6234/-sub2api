package service

import (
	"context"
	"time"
)

// 主从分流（开发计划 WP10）：Antigravity 转发路径上直接写账号状态的几处（模型级限流、账号级限流、积分耗尽标记、
// INTERNAL 500 渐进惩罚）在从节点上交给主节点照写。下面这些是主节点执行从节点事件用的入口，写法与本地同一段代码。

// ModelRateLimitsExtraKey 是账号 extra 里模型级限流表的键（从节点的账号仓储据此识别清除积分耗尽标记的写入）。
const ModelRateLimitsExtraKey = modelRateLimitsKey

// maxRelayRateLimitDuration 是从节点报来的限流时长的上限：本地解析上游返回的重置时间，正常在小时级。
const maxRelayRateLimitDuration = 7 * 24 * time.Hour

func clampRelayResetAt(resetAt time.Time) time.Time {
	if limit := time.Now().Add(maxRelayRateLimitDuration); resetAt.After(limit) {
		return limit
	}
	return resetAt
}

// RelaySetModelRateLimit 写模型级限流并更新调度快照（本地 setModelRateLimitByModelName + updateAccountModelRateLimitInCache）。
func (s *AntigravityGatewayService) RelaySetModelRateLimit(ctx context.Context, account *Account, modelKey string, resetAt time.Time) error {
	if s == nil || s.accountRepo == nil || account == nil || modelKey == "" || !resetAt.After(time.Now()) {
		return nil
	}
	resetAt = clampRelayResetAt(resetAt)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, modelKey, resetAt); err != nil {
		return err
	}
	s.updateAccountModelRateLimitInCache(ctx, account, modelKey, resetAt)
	return nil
}

// RelaySetRateLimited 写账号级限流（本地 handleUpstreamError 里 429 无法解析模型时的兜底）。
func (s *AntigravityGatewayService) RelaySetRateLimited(ctx context.Context, account *Account, resetAt time.Time) error {
	if s == nil || s.accountRepo == nil || account == nil || !resetAt.After(time.Now()) {
		return nil
	}
	return s.accountRepo.SetRateLimited(ctx, account.ID, clampRelayResetAt(resetAt))
}

// RelayUpdateModelRateLimits 写回清除了积分耗尽标记的模型级限流表（本地 clearCreditsExhausted）。
func (s *AntigravityGatewayService) RelayUpdateModelRateLimits(ctx context.Context, account *Account, limits map[string]any) error {
	if s == nil || s.accountRepo == nil || account == nil {
		return nil
	}
	return s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{modelRateLimitsKey: limits})
}

// RelayInternal500 执行 INTERNAL 500 渐进惩罚的一步：重试耗尽时计数并惩罚，成功时清零（本地同一段代码）。
func (s *AntigravityGatewayService) RelayInternal500(ctx context.Context, account *Account, reset bool) {
	if s == nil || account == nil {
		return
	}
	if reset {
		s.resetInternal500Counter(ctx, "[relay]", account.ID)
		return
	}
	s.handleInternal500RetryExhausted(ctx, "[relay]", account)
}
