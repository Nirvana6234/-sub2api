package websearch

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

// QuotaStore keeps provider quota counters and proxy availability marks.
//
// A single server uses Redis (RedisQuotaStore, shared by all instances). A relay node has no Redis:
// it counts locally within the share the master grants and reports usage back (docs/MASTER_RELAY_NODES.md 3.3).
type QuotaStore interface {
	// Reserve takes one unit of cfg's quota. allowed=false means exhausted; reserved=true means a unit was
	// actually taken (and must be rolled back if the search fails).
	Reserve(ctx context.Context, cfg ProviderConfig) (allowed, reserved bool)
	Rollback(ctx context.Context, cfg ProviderConfig)
	Usage(ctx context.Context, providerType string) (int64, error)
	Reset(ctx context.Context, providerType string) error
	MarkProxyUnavailable(ctx context.Context, proxyID int64)
	ProxyAvailable(ctx context.Context, proxyID int64) bool
}

// RedisQuotaStore is the Redis-backed QuotaStore (the original behavior).
type RedisQuotaStore struct {
	redis *redis.Client
}

var _ QuotaStore = (*RedisQuotaStore)(nil)

// NewRedisQuotaStore creates the Redis store; a nil client skips quota checks (as before).
func NewRedisQuotaStore(client *redis.Client) *RedisQuotaStore {
	return &RedisQuotaStore{redis: client}
}

func (q *RedisQuotaStore) Reserve(ctx context.Context, cfg ProviderConfig) (bool, bool) {
	if q.redis == nil {
		slog.Warn("websearch: Redis unavailable, quota check skipped", "provider", cfg.Type)
		return true, false
	}
	key := quotaRedisKey(cfg.Type)
	ttlSec := int(quotaTTLFromSubscription(cfg.SubscribedAt).Seconds())
	newVal, err := quotaIncrScript.Run(ctx, q.redis, []string{key}, ttlSec).Int64()
	if err != nil {
		slog.Warn("websearch: quota Lua INCR failed, allowing request",
			"provider", cfg.Type, "error", err)
		return true, false
	}
	if newVal > cfg.QuotaLimit {
		if decrErr := q.redis.Decr(ctx, key).Err(); decrErr != nil {
			slog.Warn("websearch: quota over-limit DECR failed",
				"provider", cfg.Type, "error", decrErr)
		}
		slog.Info("websearch: provider quota exhausted",
			"provider", cfg.Type, "used", newVal, "limit", cfg.QuotaLimit)
		return false, false
	}
	return true, true
}

func (q *RedisQuotaStore) Rollback(ctx context.Context, cfg ProviderConfig) {
	if q.redis == nil {
		return
	}
	if err := q.redis.Decr(ctx, quotaRedisKey(cfg.Type)).Err(); err != nil {
		slog.Warn("websearch: quota rollback DECR failed",
			"provider", cfg.Type, "error", err)
	}
}

func (q *RedisQuotaStore) Usage(ctx context.Context, providerType string) (int64, error) {
	if q.redis == nil {
		return 0, nil
	}
	val, err := q.redis.Get(ctx, quotaRedisKey(providerType)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

func (q *RedisQuotaStore) Reset(ctx context.Context, providerType string) error {
	if q.redis == nil {
		return nil
	}
	return q.redis.Del(ctx, quotaRedisKey(providerType)).Err()
}

// quotaAddScript adds delta to the counter and sets the TTL when the key has none (like quotaIncrScript).
var quotaAddScript = redis.NewScript(`
local val = redis.call('INCRBY', KEYS[1], ARGV[2])
local ttl = redis.call('TTL', KEYS[1])
if ttl == -1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return val
`)

// Add adds usage reported by relay nodes to the shared counter and returns the new total.
func (q *RedisQuotaStore) Add(ctx context.Context, cfg ProviderConfig, delta int64) (int64, error) {
	if q.redis == nil {
		return 0, nil
	}
	ttlSec := int(quotaTTLFromSubscription(cfg.SubscribedAt).Seconds())
	return quotaAddScript.Run(ctx, q.redis, []string{quotaRedisKey(cfg.Type)}, ttlSec, delta).Int64()
}

func (q *RedisQuotaStore) MarkProxyUnavailable(ctx context.Context, proxyID int64) {
	if q.redis == nil {
		return
	}
	key := fmt.Sprintf(proxyUnavailableKey, proxyID)
	if err := q.redis.Set(ctx, key, "1", proxyUnavailableTTL).Err(); err != nil {
		slog.Warn("websearch: failed to mark proxy unavailable",
			"proxy_id", proxyID, "error", err)
	}
}

func (q *RedisQuotaStore) ProxyAvailable(ctx context.Context, proxyID int64) bool {
	if q.redis == nil {
		return true
	}
	val, err := q.redis.Get(ctx, fmt.Sprintf(proxyUnavailableKey, proxyID)).Result()
	if err != nil {
		return true // Redis error → assume available
	}
	return val == ""
}
