package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func (r *countingSettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failErr != nil {
		return r.failErr
	}
	delete(r.values, key)
	return nil
}

func TestGetAdminAPIKeyIsServedFromCache(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{SettingKeyAdminAPIKey: "admin-old"})
	svc := &SettingService{settingRepo: repo}

	for range 3 {
		key, err := svc.GetAdminAPIKey(context.Background())
		require.NoError(t, err)
		require.Equal(t, "admin-old", key)
	}
	require.Equal(t, 1, repo.readCount(SettingKeyAdminAPIKey))
}

func TestGenerateAdminAPIKeyRevokesCachedKeyAtOnce(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{SettingKeyAdminAPIKey: "admin-old"})
	svc := &SettingService{settingRepo: repo}
	_, err := svc.GetAdminAPIKey(context.Background())
	require.NoError(t, err)

	newKey, err := svc.GenerateAdminAPIKey(context.Background())
	require.NoError(t, err)

	current, err := svc.GetAdminAPIKey(context.Background())
	require.NoError(t, err)
	require.Equal(t, newKey, current, "the replaced key must not be served from cache")
}

func TestDeleteAdminAPIKeyRevokesCachedKeyEvenWhenDeleteFails(t *testing.T) {
	repo := newCountingSettingRepo(map[string]string{SettingKeyAdminAPIKey: "admin-old"})
	svc := &SettingService{settingRepo: repo}
	_, err := svc.GetAdminAPIKey(context.Background())
	require.NoError(t, err)

	require.NoError(t, svc.DeleteAdminAPIKey(context.Background()))
	key, err := svc.GetAdminAPIKey(context.Background())
	require.NoError(t, err)
	require.Empty(t, key, "a deleted key must stop authenticating at once")

	// A failed delete still drops the cache, so the next check rereads storage.
	repo.values[SettingKeyAdminAPIKey] = "admin-other"
	_, err = svc.GetAdminAPIKey(context.Background())
	require.NoError(t, err)
	reads := repo.readCount(SettingKeyAdminAPIKey)
	repo.failErr = errors.New("db down")
	require.Error(t, svc.DeleteAdminAPIKey(context.Background()))
	repo.failErr = nil
	_, _ = svc.GetAdminAPIKey(context.Background())
	require.Equal(t, reads+1, repo.readCount(SettingKeyAdminAPIKey))
}

type firstAdminCountingRepo struct {
	avatarCountingUserRepo
	firstAdminReads int
}

func (r *firstAdminCountingRepo) GetFirstAdmin(context.Context) (*User, error) {
	r.firstAdminReads++
	return &User{ID: 1, Role: RoleAdmin, Email: "admin@example.com", Status: StatusActive}, nil
}

func TestGetFirstAdminForAuthReusesResultAndReturnsCopies(t *testing.T) {
	repo := &firstAdminCountingRepo{}
	svc := &UserService{userRepo: repo}

	first, err := svc.GetFirstAdminForAuth(context.Background())
	require.NoError(t, err)
	first.Email = "mutated@example.com"

	second, err := svc.GetFirstAdminForAuth(context.Background())
	require.NoError(t, err)
	require.Equal(t, "admin@example.com", second.Email, "callers must not share the cached user")
	require.Equal(t, 1, repo.firstAdminReads)
}
