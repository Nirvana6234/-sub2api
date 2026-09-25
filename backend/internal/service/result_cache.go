package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"golang.org/x/sync/singleflight"
)

// ErrResultCacheMiss is returned by ResultCacheStore.Get for an absent key.
var ErrResultCacheMiss = errors.New("result cache miss")

// ResultCacheStore persists computed read results, e.g. in Redis, so they are
// shared across instances and survive restarts.
type ResultCacheStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

// ResultCache serves read-only, display-oriented query results from a store
// for a short TTL. It suits endpoints that are polled far more often than
// their data needs to be exact, such as dashboards and usage listings; it is
// not for values that drive billing, authorization or scheduling. Entries are
// never invalidated on write, so a result can be up to one TTL old.
//
// A nil *ResultCache, a missing store or a store failure all fall back to
// computing the result directly.
type ResultCache struct {
	store ResultCacheStore
	loads singleflight.Group
}

func NewResultCache(store ResultCacheStore) *ResultCache {
	return &ResultCache{store: store}
}

// CachedResult returns the cached value for key, or computes it with load,
// stores it for ttl and returns it. Concurrent misses for the same key share
// one load. Errors from load are returned and never cached.
func CachedResult[T any](ctx context.Context, c *ResultCache, key string, ttl time.Duration, load func(context.Context) (T, error)) (T, error) {
	if c == nil || c.store == nil {
		return load(ctx)
	}
	if raw, err := c.store.Get(ctx, key); err == nil {
		var cached T
		if json.Unmarshal(raw, &cached) == nil {
			return cached, nil
		}
	}
	value, err, _ := c.loads.Do(key, func() (any, error) {
		loaded, err := load(ctx)
		if err != nil {
			return nil, err
		}
		if raw, marshalErr := json.Marshal(loaded); marshalErr == nil {
			// A failed write only costs the next caller a recomputation.
			_ = c.store.Set(context.WithoutCancel(ctx), key, raw, ttl)
		}
		return loaded, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return value.(T), nil
}
