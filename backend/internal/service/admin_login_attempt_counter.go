package service

import (
	"context"
	"time"
)

// AdminLoginAttemptWindow 是管理员登录失败计数的滑动窗口长度。
const AdminLoginAttemptWindow = 24 * time.Hour

// DefaultMaxAdminLoginFailures 是同一 IP 在窗口内允许的管理员登录失败次数。
// 达到该值后该 IP 被拒绝，直到窗口自然过期或有一次成功登录清零。
const DefaultMaxAdminLoginFailures = 3

// AdminLoginAttemptCache 记录「某 IP 针对管理员账号的连续登录失败次数」。
//
// 只计失败、成功即清零，是刻意的取舍：若把成功登录也计入，管理员一天正常登录
// 3 次之后就会把自己锁在门外，而这类限制的目的是防爆破，不是限制正常使用。
type AdminLoginAttemptCache interface {
	// IncrementAdminLoginFailure 原子递增失败计数并返回递增后的值。
	IncrementAdminLoginFailure(ctx context.Context, ip string, window time.Duration) (int64, error)
	// GetAdminLoginFailures 读取当前失败计数；键不存在返回 0。
	GetAdminLoginFailures(ctx context.Context, ip string) (int64, error)
	// ResetAdminLoginFailures 登录成功后清零。
	ResetAdminLoginFailures(ctx context.Context, ip string) error
}
