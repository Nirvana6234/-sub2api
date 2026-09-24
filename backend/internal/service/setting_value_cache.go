package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	settingValueCacheTTL       = 5 * time.Second
	settingValueCacheDBTimeout = 3 * time.Second
)

// settingValueCache keeps settings that the request path reads on every
// request in process for a few seconds. Relay traffic reaches the same
// settings at every hop, so reading them from the database each time adds
// queries proportional to traffic. Absent settings are cached too, since most
// of these keys are never set.
//
// Writes that go through the owning service call invalidate, so they apply
// immediately on this instance; other writers (backup restore, direct SQL)
// take effect within the TTL. The zero value is not usable; use
// newSettingValueCache.
type settingValueCache struct {
	repo       SettingRepository
	ttl        time.Duration
	entries    sync.Map // key -> *cachedSettingValue
	loads      singleflight.Group
	generation atomic.Uint64
}

type cachedSettingValue struct {
	value      string
	found      bool
	expiresAt  time.Time
	generation uint64
}

func newSettingValueCache(repo SettingRepository, ttl time.Duration) *settingValueCache {
	return &settingValueCache{repo: repo, ttl: ttl}
}

// GetValue mirrors SettingRepository.GetValue, including ErrSettingNotFound
// for absent settings. Database errors are returned and never cached.
func (c *settingValueCache) GetValue(ctx context.Context, key string) (string, error) {
	generation := c.generation.Load()
	if entry, ok := c.entries.Load(key); ok {
		cached := entry.(*cachedSettingValue)
		if cached.generation == generation && time.Now().Before(cached.expiresAt) {
			return cachedSettingResult(cached)
		}
	}
	result, err, _ := c.loads.Do(key, func() (any, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		// One caller's cancellation must not fail the others sharing this load.
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settingValueCacheDBTimeout)
		defer cancel()
		value, err := c.repo.GetValue(dbCtx, key)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			return nil, err
		}
		loaded := &cachedSettingValue{
			value:      value,
			found:      err == nil,
			expiresAt:  time.Now().Add(c.ttl),
			generation: generation,
		}
		// A write that invalidated the cache during this load bumped the
		// generation; storing would bring back the pre-write value.
		if c.generation.Load() == generation {
			c.entries.Store(key, loaded)
		}
		return loaded, nil
	})
	if err != nil {
		return "", err
	}
	return cachedSettingResult(result.(*cachedSettingValue))
}

// GetMultiple mirrors SettingRepository.GetMultiple: absent keys are left out.
func (c *settingValueCache) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		value, err := c.GetValue(ctx, key)
		if errors.Is(err, ErrSettingNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, nil
}

// invalidate drops every cached value, including loads still in flight.
func (c *settingValueCache) invalidate() {
	c.generation.Add(1)
	c.entries.Range(func(key, _ any) bool {
		c.entries.Delete(key)
		return true
	})
}

// hotSettings returns the cache for settings read on every gateway request.
// It is created lazily so services built as struct literals work unchanged.
func (s *SettingService) hotSettings() *settingValueCache {
	s.hotValuesOnce.Do(func() {
		s.hotValues = newSettingValueCache(s.settingRepo, settingValueCacheTTL)
	})
	return s.hotValues
}

func (s *SettingService) invalidateHotSettings() {
	if s != nil {
		s.hotSettings().invalidate()
	}
}

func cachedSettingResult(entry *cachedSettingValue) (string, error) {
	if !entry.found {
		return "", ErrSettingNotFound
	}
	return entry.value, nil
}
