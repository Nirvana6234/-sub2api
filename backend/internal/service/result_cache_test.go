package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type memoryResultCacheStore struct {
	mu      sync.Mutex
	values  map[string][]byte
	ttls    map[string]time.Duration
	getErr  error
	setErr  error
	setHits int
}

func newMemoryResultCacheStore() *memoryResultCacheStore {
	return &memoryResultCacheStore{values: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (s *memoryResultCacheStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	value, ok := s.values[key]
	if !ok {
		return nil, ErrResultCacheMiss
	}
	return value, nil
}

func (s *memoryResultCacheStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setHits++
	if s.setErr != nil {
		return s.setErr
	}
	s.values[key] = value
	s.ttls[key] = ttl
	return nil
}

type resultCacheSample struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestCachedResultServesStoredValue(t *testing.T) {
	store := newMemoryResultCacheStore()
	cache := NewResultCache(store)
	var loads atomic.Int32
	load := func(context.Context) (resultCacheSample, error) {
		loads.Add(1)
		return resultCacheSample{Name: "a", Count: 1}, nil
	}

	for range 3 {
		got, err := CachedResult(context.Background(), cache, "k", time.Minute, load)
		require.NoError(t, err)
		require.Equal(t, resultCacheSample{Name: "a", Count: 1}, got)
	}
	require.EqualValues(t, 1, loads.Load())
	require.Equal(t, time.Minute, store.ttls["k"])
}

func TestCachedResultDoesNotCacheErrors(t *testing.T) {
	store := newMemoryResultCacheStore()
	cache := NewResultCache(store)
	failing := errors.New("db down")

	_, err := CachedResult(context.Background(), cache, "k", time.Minute, func(context.Context) (int, error) {
		return 0, failing
	})
	require.ErrorIs(t, err, failing)
	require.Zero(t, store.setHits)
}

func TestCachedResultFallsBackWhenStoreFails(t *testing.T) {
	store := newMemoryResultCacheStore()
	store.getErr = errors.New("redis down")
	store.setErr = errors.New("redis down")
	cache := NewResultCache(store)

	got, err := CachedResult(context.Background(), cache, "k", time.Minute, func(context.Context) (int, error) {
		return 7, nil
	})
	require.NoError(t, err)
	require.Equal(t, 7, got)
}

func TestCachedResultReloadsUnreadableEntry(t *testing.T) {
	store := newMemoryResultCacheStore()
	store.values["k"] = []byte("not json")
	cache := NewResultCache(store)

	got, err := CachedResult(context.Background(), cache, "k", time.Minute, func(context.Context) (resultCacheSample, error) {
		return resultCacheSample{Name: "fresh"}, nil
	})
	require.NoError(t, err)
	require.Equal(t, "fresh", got.Name)
}

func TestCachedResultWithoutCacheAlwaysLoads(t *testing.T) {
	var loads int
	for range 2 {
		_, err := CachedResult(context.Background(), (*ResultCache)(nil), "k", time.Minute, func(context.Context) (int, error) {
			loads++
			return loads, nil
		})
		require.NoError(t, err)
	}
	require.Equal(t, 2, loads)
}
