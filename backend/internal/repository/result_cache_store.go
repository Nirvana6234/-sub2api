package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const resultCacheKeyPrefix = "sub2api:result:"

type resultCacheStore struct {
	rdb *redis.Client
}

// NewResultCacheStore keeps service.ResultCache entries in Redis.
func NewResultCacheStore(rdb *redis.Client) service.ResultCacheStore {
	return &resultCacheStore{rdb: rdb}
}

func (s *resultCacheStore) Get(ctx context.Context, key string) ([]byte, error) {
	raw, err := s.rdb.Get(ctx, resultCacheKeyPrefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, service.ErrResultCacheMiss
	}
	return raw, err
}

func (s *resultCacheStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.rdb.Set(ctx, resultCacheKeyPrefix+key, value, ttl).Err()
}
