package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type globalBlacklistRepoStub struct{ values map[string]string }

func (r *globalBlacklistRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	return nil, ErrSettingNotFound
}
func (r *globalBlacklistRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}
func (r *globalBlacklistRepoStub) Set(ctx context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *globalBlacklistRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}
func (r *globalBlacklistRepoStub) SetMultiple(ctx context.Context, values map[string]string) error {
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}
func (r *globalBlacklistRepoStub) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *globalBlacklistRepoStub) Delete(ctx context.Context, key string) error {
	delete(r.values, key)
	return nil
}

func TestGlobalBlacklistMatchesAccountIPAndExpiry(t *testing.T) {
	repo := &globalBlacklistRepoStub{values: map[string]string{}}
	svc := NewSettingService(repo, nil)
	entry, err := svc.AddGlobalBlacklistEntry(context.Background(), GlobalBlacklistEntry{Kind: "ip", Value: "203.0.113.0/24", Enabled: true})
	require.NoError(t, err)
	require.NotEmpty(t, entry.ID)
	_, err = svc.AddGlobalBlacklistEntry(context.Background(), GlobalBlacklistEntry{Kind: "account", Value: "42", Enabled: true})
	require.NoError(t, err)
	matched, _, err := svc.IsGloballyBlacklisted(context.Background(), 0, "203.0.113.9")
	require.NoError(t, err)
	require.True(t, matched)
	matched, _, err = svc.IsGloballyBlacklisted(context.Background(), 42, "198.51.100.5")
	require.NoError(t, err)
	require.True(t, matched)

	expires := time.Now().Add(-time.Minute)
	_, err = svc.ReplaceGlobalBlacklist(context.Background(), []GlobalBlacklistEntry{{Kind: "ip", Value: "198.51.100.5", ExpiresAt: &expires, Enabled: true}})
	require.NoError(t, err)
	matched, _, err = svc.IsGloballyBlacklisted(context.Background(), 0, "198.51.100.5")
	require.NoError(t, err)
	require.False(t, matched)
}

func TestNormalizeGlobalBlacklistRejectsInvalidValues(t *testing.T) {
	_, err := normalizeGlobalBlacklist([]GlobalBlacklistEntry{{Kind: "ip", Value: "not-an-ip"}}, false)
	require.Error(t, err)
	_, err = normalizeGlobalBlacklist([]GlobalBlacklistEntry{{Kind: "account", Value: "0"}}, false)
	require.Error(t, err)
}
