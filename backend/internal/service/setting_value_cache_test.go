package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// countingSettingRepo counts reads so tests can tell cache hits from loads.
type countingSettingRepo struct {
	SettingRepository
	mu      sync.Mutex
	values  map[string]string
	reads   map[string]int
	failErr error
	onRead  func(key string)
}

func newCountingSettingRepo(values map[string]string) *countingSettingRepo {
	return &countingSettingRepo{values: values, reads: map[string]int{}}
}

func (r *countingSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	r.reads[key]++
	value, ok := r.values[key]
	failErr := r.failErr
	onRead := r.onRead
	r.mu.Unlock()
	if onRead != nil {
		onRead(key)
	}
	if failErr != nil {
		return "", failErr
	}
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *countingSettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *countingSettingRepo) readCount(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads[key]
}

func TestSettingValueCacheServesRepeatedReadsFromMemory(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{"k": "v"})
	cache := newSettingValueCache(repo, time.Minute)

	for range 5 {
		value, err := cache.GetValue(context.Background(), "k")
		require.NoError(t, err)
		require.Equal(t, "v", value)
	}
	require.Equal(t, 1, repo.readCount("k"))
}

func TestSettingValueCacheCachesAbsentSettings(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{})
	cache := newSettingValueCache(repo, time.Minute)

	for range 3 {
		_, err := cache.GetValue(context.Background(), "missing")
		require.ErrorIs(t, err, ErrSettingNotFound)
	}
	require.Equal(t, 1, repo.readCount("missing"))
}

func TestSettingValueCacheDoesNotCacheDatabaseErrors(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{"k": "v"})
	repo.failErr = errors.New("db down")
	cache := newSettingValueCache(repo, time.Minute)

	_, err := cache.GetValue(context.Background(), "k")
	require.EqualError(t, err, "db down")

	repo.failErr = nil
	value, err := cache.GetValue(context.Background(), "k")
	require.NoError(t, err)
	require.Equal(t, "v", value)
	require.Equal(t, 2, repo.readCount("k"))
}

func TestSettingValueCacheExpires(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{"k": "v1"})
	cache := newSettingValueCache(repo, 20*time.Millisecond)

	value, err := cache.GetValue(context.Background(), "k")
	require.NoError(t, err)
	require.Equal(t, "v1", value)

	require.NoError(t, repo.Set(context.Background(), "k", "v2"))
	time.Sleep(40 * time.Millisecond)
	value, err = cache.GetValue(context.Background(), "k")
	require.NoError(t, err)
	require.Equal(t, "v2", value)
}

func TestSettingValueCacheInvalidateReloads(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{"k": "v1"})
	cache := newSettingValueCache(repo, time.Minute)

	_, err := cache.GetValue(context.Background(), "k")
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), "k", "v2"))
	cache.invalidate()

	value, err := cache.GetValue(context.Background(), "k")
	require.NoError(t, err)
	require.Equal(t, "v2", value)
}

func TestSettingValueCacheDropsLoadRacingAnInvalidation(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{"k": "old"})
	cache := newSettingValueCache(repo, time.Minute)
	// The write lands and invalidates while the first load is reading.
	repo.onRead = func(string) {
		repo.onRead = nil
		repo.values["k"] = "new"
		cache.invalidate()
	}

	value, err := cache.GetValue(context.Background(), "k")
	require.NoError(t, err)
	require.Equal(t, "old", value, "the racing caller sees the value it read")

	value, err = cache.GetValue(context.Background(), "k")
	require.NoError(t, err)
	require.Equal(t, "new", value, "the pre-write value must not have been stored")
}

func TestSettingValueCacheGetMultipleOmitsAbsentKeys(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{"a": "1"})
	cache := newSettingValueCache(repo, time.Minute)

	values, err := cache.GetMultiple(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"a": "1"}, values)
}

func TestSetOpenAIFastPolicySettingsInvalidatesCachedPolicy(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{})
	svc := &SettingService{settingRepo: repo}

	before, err := svc.GetOpenAIFastPolicySettings(context.Background())
	require.NoError(t, err)
	require.Empty(t, before.Rules)

	updated := DefaultOpenAIFastPolicySettings()
	updated.Rules = []OpenAIFastPolicyRule{{ServiceTier: OpenAIFastTierPriority, Action: BetaPolicyActionBlock, Scope: BetaPolicyScopeAll}}
	require.NoError(t, svc.SetOpenAIFastPolicySettings(context.Background(), updated))

	after, err := svc.GetOpenAIFastPolicySettings(context.Background())
	require.NoError(t, err)
	require.Len(t, after.Rules, 1)
}
