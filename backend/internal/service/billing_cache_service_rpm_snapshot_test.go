package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type countingUserRPMCache struct {
	userGroupCalls atomic.Int32
}

func (c *countingUserRPMCache) IncrementUserGroupRPM(context.Context, int64, int64) (int, error) {
	c.userGroupCalls.Add(1)
	return 1, nil
}
func (c *countingUserRPMCache) IncrementUserRPM(context.Context, int64) (int, error) { return 1, nil }
func (c *countingUserRPMCache) GetUserGroupRPM(context.Context, int64, int64) (int, error) {
	return 0, nil
}
func (c *countingUserRPMCache) GetUserRPM(context.Context, int64) (int, error) { return 0, nil }

type countingRPMOverrideRepo struct {
	UserGroupRateRepository
	override *int
	calls    atomic.Int32
}

func (r *countingRPMOverrideRepo) GetRPMOverrideByUserAndGroup(context.Context, int64, int64) (*int, error) {
	r.calls.Add(1)
	return r.override, nil
}

func newRPMSnapshotTestService(t *testing.T, repo UserGroupRateRepository) (*BillingCacheService, *countingUserRPMCache) {
	t.Helper()
	cache := &countingUserRPMCache{}
	svc := NewBillingCacheService(nil, nil, nil, nil, cache, repo, &config.Config{}, nil)
	t.Cleanup(svc.Stop)
	return svc, cache
}

func TestCheckRPMTrustsSnapshotAbsenceForSameGroup(t *testing.T) {
	repo := &countingRPMOverrideRepo{}
	svc, _ := newRPMSnapshotTestService(t, repo)
	user := &User{ID: 1, UserGroupRPMOverrideGroupID: 10}

	require.NoError(t, svc.checkRPM(context.Background(), user, &Group{ID: 10}))
	require.EqualValues(t, 0, repo.calls.Load(), "a snapshot that resolved no override for this group must not query the database")
}

func TestCheckRPMUsesSnapshotOverrideForSameGroup(t *testing.T) {
	override := 5
	repo := &countingRPMOverrideRepo{}
	svc, cache := newRPMSnapshotTestService(t, repo)
	user := &User{ID: 1, UserGroupRPMOverride: &override, UserGroupRPMOverrideGroupID: 10}

	require.NoError(t, svc.checkRPM(context.Background(), user, &Group{ID: 10}))
	require.EqualValues(t, 0, repo.calls.Load())
	require.EqualValues(t, 1, cache.userGroupCalls.Load(), "the override must still be enforced")
}

func TestCheckRPMQueriesDatabaseWhenSnapshotIsForAnotherGroup(t *testing.T) {
	snapshotOverride := 5
	repo := &countingRPMOverrideRepo{}
	svc, cache := newRPMSnapshotTestService(t, repo)
	user := &User{ID: 1, UserGroupRPMOverride: &snapshotOverride, UserGroupRPMOverrideGroupID: 10}

	require.NoError(t, svc.checkRPM(context.Background(), user, &Group{ID: 20}))
	require.EqualValues(t, 1, repo.calls.Load(), "another group's override must not be reused")
	require.EqualValues(t, 0, cache.userGroupCalls.Load(), "group 20 has neither an override nor a group limit")
}

func TestCheckRPMKeepsLegacyOverrideWithoutGroup(t *testing.T) {
	override := 5
	repo := &countingRPMOverrideRepo{}
	svc, cache := newRPMSnapshotTestService(t, repo)
	user := &User{ID: 1, UserGroupRPMOverride: &override}

	require.NoError(t, svc.checkRPM(context.Background(), user, &Group{ID: 10}))
	require.EqualValues(t, 0, repo.calls.Load())
	require.EqualValues(t, 1, cache.userGroupCalls.Load())
}

func TestCheckRPMQueriesDatabaseWithoutSnapshot(t *testing.T) {
	repo := &countingRPMOverrideRepo{}
	svc, _ := newRPMSnapshotTestService(t, repo)

	require.NoError(t, svc.checkRPM(context.Background(), &User{ID: 1}, &Group{ID: 10}))
	require.EqualValues(t, 1, repo.calls.Load())
}
