package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// 三层计数的键共用同一个 hash tag（试用日），保证 Lua 脚本里的多键操作落在同一个 slot。
const (
	guestTrialQuotaPrefix    = "guest_trial:{%s}:"
	guestTrialVerifiedPrefix = "guest_trial:verified:"
	// 计数键在试用日结束后再保留一段时间即可自然过期。
	guestTrialQuotaTTL = 36 * time.Hour
)

// 先检查三层上限，全部未满才一起 +1；返回 {是否放行, 拒绝原因, 访客已用次数}。
var guestTrialConsumeScript = redis.NewScript(`
local visitor = tonumber(redis.call("GET", KEYS[1]) or "0")
local ip = tonumber(redis.call("GET", KEYS[2]) or "0")
local global = tonumber(redis.call("GET", KEYS[3]) or "0")
if visitor >= tonumber(ARGV[1]) then
  return {0, 1, visitor}
end
if ip >= tonumber(ARGV[2]) then
  return {0, 2, visitor}
end
if global >= tonumber(ARGV[3]) then
  return {0, 3, visitor}
end
visitor = redis.call("INCR", KEYS[1])
redis.call("EXPIRE", KEYS[1], ARGV[4])
redis.call("INCR", KEYS[2])
redis.call("EXPIRE", KEYS[2], ARGV[4])
redis.call("INCR", KEYS[3])
redis.call("EXPIRE", KEYS[3], ARGV[4])
return {1, 0, visitor}
`)

type guestTrialQuota struct {
	rdb *redis.Client
}

func NewGuestTrialQuota(rdb *redis.Client) service.GuestTrialQuota {
	return &guestTrialQuota{rdb: rdb}
}

func guestTrialQuotaKeys(keys service.GuestTrialQuotaKeys) []string {
	prefix := fmt.Sprintf(guestTrialQuotaPrefix, keys.Day)
	return []string{prefix + "device:" + keys.Device, prefix + "ip:" + keys.IP, prefix + "global"}
}

func (q *guestTrialQuota) Consume(ctx context.Context, keys service.GuestTrialQuotaKeys, limits service.GuestTrialQuotaLimits) (service.GuestTrialQuotaDenyReason, int, error) {
	if q == nil || q.rdb == nil {
		return service.GuestTrialQuotaGlobalFull, 0, errors.New("guest trial quota store unavailable")
	}
	raw, err := guestTrialConsumeScript.Run(ctx, q.rdb, guestTrialQuotaKeys(keys),
		limits.PerVisitor, limits.PerIP, limits.Global, int(guestTrialQuotaTTL.Seconds())).Slice()
	if err != nil {
		return service.GuestTrialQuotaGlobalFull, 0, err
	}
	if len(raw) != 3 {
		return service.GuestTrialQuotaGlobalFull, 0, errors.New("unexpected guest trial quota script result")
	}
	allowed, _ := raw[0].(int64)
	reason, _ := raw[1].(int64)
	used, _ := raw[2].(int64)
	if allowed == 1 {
		return service.GuestTrialQuotaAllowed, int(used), nil
	}
	return service.GuestTrialQuotaDenyReason(reason), int(used), nil
}

func (q *guestTrialQuota) VisitorUsed(ctx context.Context, keys service.GuestTrialQuotaKeys) (int, error) {
	if q == nil || q.rdb == nil {
		return 0, errors.New("guest trial quota store unavailable")
	}
	used, err := q.rdb.Get(ctx, guestTrialQuotaKeys(keys)[0]).Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return used, err
}

func (q *guestTrialQuota) MarkVerified(ctx context.Context, subject string, ttl time.Duration) error {
	if q == nil || q.rdb == nil {
		return errors.New("guest trial quota store unavailable")
	}
	return q.rdb.Set(ctx, guestTrialVerifiedPrefix+subject, "1", ttl).Err()
}

func (q *guestTrialQuota) IsVerified(ctx context.Context, subject string) (bool, error) {
	if q == nil || q.rdb == nil {
		return false, errors.New("guest trial quota store unavailable")
	}
	n, err := q.rdb.Exists(ctx, guestTrialVerifiedPrefix+subject).Result()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
