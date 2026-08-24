package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// maxAdminLoginFailures 返回当前生效的管理员登录失败上限。
func (s *AuthService) maxAdminLoginFailures(ctx context.Context) int64 {
	if s == nil {
		return DefaultMaxAdminLoginFailures
	}
	if s.settingService != nil {
		if v := s.settingService.GetMaxAdminLoginFailures(ctx); v > 0 {
			return int64(v)
		}
	}
	return DefaultMaxAdminLoginFailures
}

// ensureAdminLoginAllowed 在比对管理员密码之前检查该 IP 是否已被锁定。
//
// 计数器不可用时放行（fail-open）：Redis 故障不应让管理员完全无法登录——
// 那等于把一次缓存故障升级成管理后台不可用。爆破防护由后续失败计数继续兜底。
func (s *AuthService) ensureAdminLoginAllowed(ctx context.Context) error {
	if s == nil || s.adminLoginAttempts == nil {
		return nil
	}
	ip := ClientIPFromContext(ctx)
	if ip == "" {
		return nil
	}
	count, err := s.adminLoginAttempts.GetAdminLoginFailures(ctx, ip)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] admin login failure lookup failed for ip %s: %v", ip, err)
		return nil
	}
	if count >= s.maxAdminLoginFailures(ctx) {
		logger.LegacyPrintf("service.auth", "[Auth] admin login blocked for ip %s: %d failures in window", ip, count)
		return ErrAdminLoginAttemptsExceeded
	}
	return nil
}

// recordAdminLoginFailure 记录一次管理员密码错误。
func (s *AuthService) recordAdminLoginFailure(ctx context.Context) {
	if s == nil || s.adminLoginAttempts == nil {
		return
	}
	ip := ClientIPFromContext(ctx)
	if ip == "" {
		return
	}
	count, err := s.adminLoginAttempts.IncrementAdminLoginFailure(ctx, ip, AdminLoginAttemptWindow)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] failed to record admin login failure for ip %s: %v", ip, err)
		return
	}
	logger.LegacyPrintf("service.auth", "[Auth] admin login failure %d/%d for ip %s", count, s.maxAdminLoginFailures(ctx), ip)
}

// resetAdminLoginFailures 管理员登录成功后清零该 IP 的失败计数。
func (s *AuthService) resetAdminLoginFailures(ctx context.Context) {
	if s == nil || s.adminLoginAttempts == nil {
		return
	}
	ip := ClientIPFromContext(ctx)
	if ip == "" {
		return
	}
	if err := s.adminLoginAttempts.ResetAdminLoginFailures(ctx, ip); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] failed to reset admin login failures for ip %s: %v", ip, err)
	}
}
