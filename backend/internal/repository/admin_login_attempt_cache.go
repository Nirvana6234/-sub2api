package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const adminLoginFailurePrefix = "admin_login_fail:ip:"

// 首次写入时设置 TTL；若键因异常丢失 TTL（-1）则补设，避免计数永久驻留。
var adminLoginFailureIncrScript = redis.NewScript(`
	local key = KEYS[1]
	local ttl = tonumber(ARGV[1])

	local count = redis.call('INCR', key)
	local cur = redis.call('TTL', key)
	if count == 1 or cur == -1 then
		redis.call('EXPIRE', key, ttl)
	end

	return count
`)

type adminLoginAttemptCache struct {
	rdb *redis.Client
}

// NewAdminLoginAttemptCache 构造基于 Redis 的管理员登录失败计数器。
func NewAdminLoginAttemptCache(rdb *redis.Client) service.AdminLoginAttemptCache {
	return &adminLoginAttemptCache{rdb: rdb}
}

func (c *adminLoginAttemptCache) key(ip string) string {
	return fmt.Sprintf("%s%s", adminLoginFailurePrefix, ip)
}

func (c *adminLoginAttemptCache) IncrementAdminLoginFailure(ctx context.Context, ip string, window time.Duration) (int64, error) {
	if c == nil || c.rdb == nil || ip == "" {
		return 0, nil
	}
	seconds := int64(window / time.Second)
	if seconds < 60 {
		seconds = 60
	}
	n, err := adminLoginFailureIncrScript.Run(ctx, c.rdb, []string{c.key(ip)}, seconds).Int64()
	if err != nil {
		return 0, fmt.Errorf("increment admin login failure: %w", err)
	}
	return n, nil
}

func (c *adminLoginAttemptCache) GetAdminLoginFailures(ctx context.Context, ip string) (int64, error) {
	if c == nil || c.rdb == nil || ip == "" {
		return 0, nil
	}
	n, err := c.rdb.Get(ctx, c.key(ip)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get admin login failures: %w", err)
	}
	return n, nil
}

func (c *adminLoginAttemptCache) ResetAdminLoginFailures(ctx context.Context, ip string) error {
	if c == nil || c.rdb == nil || ip == "" {
		return nil
	}
	return c.rdb.Del(ctx, c.key(ip)).Err()
}
