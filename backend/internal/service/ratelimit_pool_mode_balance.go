package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// 池模式账号遇到上游明确的「余额不足」时的处置。
//
// 池模式默认把上游错误视为号池内部的瞬时抖动：不写本地账号状态，只在同账号上重试。
// 但中转站返回的余额不足描述的是本站这把 key 自身欠费——同账号重试、等号池轮换都不会恢复。
// 生产上曾因此让一个欠费账号每个请求先被同账号重试 3 次再换号兜底，单日累计近 900 次
// 无效上游调用，并把每个请求拖慢数秒。
//
// 这里按「可恢复」处理：临时停调一段时间（不置 error），冷却结束后自动回到调度；
// 仍欠费时下一次命中会再次停调，把无效调用压到每个冷却周期一次。充值后管理员也可在后台
// 手动解除临时停调。管理员显式配置的临时不可调度规则仍优先于这里。

const (
	poolModeInsufficientBalanceCooldown     = 30 * time.Minute
	poolModeInsufficientBalanceReasonPrefix = "pool_insufficient_balance"
)

// isPoolModeInsufficientBalanceError 识别上游明确返回的余额不足。
// 只看 402/403/429：这三类状态码下的余额文案才是计费拒绝，而不是请求内容里恰好带了相关字样。
func isPoolModeInsufficientBalanceError(statusCode int, responseBody []byte) bool {
	switch statusCode {
	case http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests:
	default:
		return false
	}
	if len(responseBody) == 0 {
		return false
	}
	// new-api / one-api 系中转：{"error":{"type":"INSUFFICIENT_BALANCE","message":"账户余额不足…"}}
	if strings.Contains(strings.ToLower(string(responseBody)), "insufficient_balance") {
		return true
	}
	return cnProviderResponseIndicatesInsufficientBalance(responseBody)
}

// tryPoolModeInsufficientBalance 命中余额不足时临时停调池模式账号并返回 true。
func (s *RateLimitService) tryPoolModeInsufficientBalance(ctx context.Context, account *Account, statusCode int, responseBody []byte) bool {
	if s == nil || account == nil || !account.IsPoolMode() {
		return false
	}
	if !isPoolModeInsufficientBalanceError(statusCode, responseBody) {
		return false
	}

	reason := poolModeInsufficientBalanceReasonPrefix + ": 上游余额不足，账号临时停调"
	if upstreamMsg := strings.TrimSpace(sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(responseBody))); upstreamMsg != "" {
		reason = poolModeInsufficientBalanceReasonPrefix + ": " + truncateForLog([]byte(upstreamMsg), 256)
	}

	until := time.Now().Add(poolModeInsufficientBalanceCooldown)
	s.notifyAccountSchedulingBlocked(account, until, poolModeInsufficientBalanceReasonPrefix)
	if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); err != nil {
		slog.Warn("pool_mode_insufficient_balance_set_temp_unschedulable_failed", "account_id", account.ID, "error", err)
	}
	slog.Warn("pool_mode_insufficient_balance",
		"account_id", account.ID,
		"platform", account.Platform,
		"status_code", statusCode,
		"until", until.UTC(),
	)
	return true
}
