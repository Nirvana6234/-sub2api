package service

import (
	"context"
	"net"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// DefaultMaxAccountsPerRegisterIP 是同一客户端 IP 允许注册的账号数上限。
const DefaultMaxAccountsPerRegisterIP = 3

// WithClientIP 把客户端真实 IP 放进 context，供注册配额判定使用。
// 空值不写入，避免用空字符串覆盖上游已解析出的 IP。
func WithClientIP(ctx context.Context, ip string) context.Context {
	ip = NormalizeClientIP(ip)
	if ip == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxkey.ClientIP, ip)
}

// ClientIPFromContext 读取 WithClientIP 写入的客户端 IP，没有则返回空串。
func ClientIPFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	ip, _ := ctx.Value(ctxkey.ClientIP).(string)
	return NormalizeClientIP(ip)
}

// NormalizeClientIP 归一化 IP 文本形式。
// IPv6 大小写不敏感且有多种等价写法（2001:DB8::1 / 2001:db8:0:0:0:0:0:1），
// 不归一会让同一来源被当成多个 IP，从而绕过配额。
func NormalizeClientIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	// 少数代理会把 IP 写成 host:port，取主机部分
	if host, _, err := net.SplitHostPort(ip); err == nil && host != "" {
		ip = host
	}
	ip = strings.Trim(ip, "[]")
	if parsed := net.ParseIP(ip); parsed != nil {
		return parsed.String()
	}
	return strings.ToLower(ip)
}

// maxAccountsPerRegisterIP 返回当前生效的每 IP 注册上限。
// 预留 settingService 覆盖位：管理员未配置时用内置默认值。
func (s *AuthService) maxAccountsPerRegisterIP(ctx context.Context) int {
	if s == nil {
		return DefaultMaxAccountsPerRegisterIP
	}
	if s.settingService != nil {
		if v := s.settingService.GetMaxAccountsPerRegisterIP(ctx); v > 0 {
			return v
		}
	}
	return DefaultMaxAccountsPerRegisterIP
}

// validateRegisterIPQuota 在真正建号之前做一次快速拒绝。
//
// 这只是"早失败"优化，不是并发安全的判定：两个并发请求可能同时读到未超限。
// 真正的把关在 CreateWithEmailAliasGuardAndRegisterIPLimit 里——它在 IP
// advisory 锁内、与用户写入同一事务复查配额，与域名配额的做法一致。
func (s *AuthService) validateRegisterIPQuota(ctx context.Context) error {
	if s == nil || s.userRepo == nil {
		return nil
	}
	ip := ClientIPFromContext(ctx)
	if ip == "" {
		// 取不到来源 IP 时不拦截：宁可放过也不能因为代理头缺失把正常注册全部堵死。
		// 事务内复查同样会跳过（ip 为空 => quota 不激活），行为一致。
		return nil
	}
	quotaRepo, ok := s.userRepo.(RegistrationIPQuotaRepository)
	if !ok {
		return nil
	}
	limit := s.maxAccountsPerRegisterIP(ctx)
	count, err := quotaRepo.CountUsersByRegisterIP(ctx, ip)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to count registrations for ip %s: %v", ip, err)
		return ErrServiceUnavailable
	}
	if count >= limit {
		return ErrRegisterIPLimit
	}
	return nil
}

// createUserWithRegisterIPQuota 走带 IP 配额的原子创建路径。
// 拿不到 IP、仓储不支持该能力时回落到调用方给的 fallback，保持原有行为。
func (s *AuthService) createUserWithRegisterIPQuota(
	ctx context.Context,
	user *User,
	fallback func(context.Context, *User) error,
) error {
	ip := ClientIPFromContext(ctx)
	if ip == "" || user == nil {
		return fallback(ctx, user)
	}
	quotaRepo, ok := s.userRepo.(RegistrationIPQuotaRepository)
	if !ok {
		return fallback(ctx, user)
	}
	// 记录来源 IP，后续该 IP 的注册计数才能把这个账号算进去
	ipCopy := ip
	user.RegisterIP = &ipCopy
	return quotaRepo.CreateWithEmailAliasGuardAndRegisterIPLimit(ctx, user, ip, s.maxAccountsPerRegisterIP(ctx))
}
