package repository

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	accountProfileStatisticsCacheKeyPrefix = "codex_profile_stats:"
	// Profile statistics change slowly (daily usage buckets, streaks); a short
	// TTL is enough to stop every admin panel render from re-dialing OpenAI
	// while still keeping the data reasonably fresh.
	accountProfileStatisticsCacheTTL = 10 * time.Minute
)

type accountProfileStatisticsCache struct {
	rdb *redis.Client
}

// NewAccountProfileStatisticsCache 创建 Codex 用户画像缓存（按账号 ID 分 key，短 TTL）。
func NewAccountProfileStatisticsCache(rdb *redis.Client) service.AccountProfileStatisticsCache {
	return &accountProfileStatisticsCache{rdb: rdb}
}

func (c *accountProfileStatisticsCache) Get(ctx context.Context, accountID int64) (*service.CodexProfileStatistics, bool) {
	if c == nil || c.rdb == nil {
		return nil, false
	}

	data, err := c.rdb.Get(ctx, accountProfileStatisticsCacheKey(accountID)).Bytes()
	if err != nil {
		if err != redis.Nil {
			slog.Warn("codex_profile_statistics_cache_get_failed", "account_id", accountID, "error", err)
		}
		return nil, false
	}

	var stats service.CodexProfileStatistics
	if err := json.Unmarshal(data, &stats); err != nil {
		slog.Warn("codex_profile_statistics_cache_unmarshal_failed", "account_id", accountID, "error", err)
		return nil, false
	}
	return &stats, true
}

func (c *accountProfileStatisticsCache) Set(ctx context.Context, accountID int64, stats *service.CodexProfileStatistics) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	data, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, accountProfileStatisticsCacheKey(accountID), data, accountProfileStatisticsCacheTTL).Err()
}

func accountProfileStatisticsCacheKey(accountID int64) string {
	return accountProfileStatisticsCacheKeyPrefix + strconv.FormatInt(accountID, 10)
}
