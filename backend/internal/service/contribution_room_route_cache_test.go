package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type countingContributionRoutes struct {
	route *ContributionRoomRoute
	err   error
	calls atomic.Int32
}

func (r *countingContributionRoutes) ResolveRouteForAPIKey(context.Context, int64, int64) (*ContributionRoomRoute, error) {
	r.calls.Add(1)
	return r.route, r.err
}

func TestCachedContributionRoomRoutesReusesRouteWithinTTL(t *testing.T) {
	inner := &countingContributionRoutes{route: &ContributionRoomRoute{ExplicitlySelected: true}}
	repo := newCachedContributionRoomRoutes(inner, time.Minute)

	for range 3 {
		route, err := repo.ResolveRouteForAPIKey(context.Background(), 1, 2)
		require.NoError(t, err)
		require.True(t, route.IsExplicitSelection())
	}
	require.EqualValues(t, 1, inner.calls.Load())
}

func TestCachedContributionRoomRoutesCachesAbsentRoute(t *testing.T) {
	inner := &countingContributionRoutes{}
	repo := newCachedContributionRoomRoutes(inner, time.Minute)

	for range 2 {
		route, err := repo.ResolveRouteForAPIKey(context.Background(), 1, 2)
		require.NoError(t, err)
		require.Nil(t, route)
	}
	require.EqualValues(t, 1, inner.calls.Load())
}

func TestCachedContributionRoomRoutesKeysByUserAndAPIKey(t *testing.T) {
	inner := &countingContributionRoutes{}
	repo := newCachedContributionRoomRoutes(inner, time.Minute)

	_, _ = repo.ResolveRouteForAPIKey(context.Background(), 1, 2)
	_, _ = repo.ResolveRouteForAPIKey(context.Background(), 1, 3)
	_, _ = repo.ResolveRouteForAPIKey(context.Background(), 4, 2)
	require.EqualValues(t, 3, inner.calls.Load())
}

func TestCachedContributionRoomRoutesDoesNotCacheErrors(t *testing.T) {
	inner := &countingContributionRoutes{err: errors.New("db down")}
	repo := newCachedContributionRoomRoutes(inner, time.Minute)

	_, err := repo.ResolveRouteForAPIKey(context.Background(), 1, 2)
	require.Error(t, err)
	inner.err = nil
	_, err = repo.ResolveRouteForAPIKey(context.Background(), 1, 2)
	require.NoError(t, err)
	require.EqualValues(t, 2, inner.calls.Load())
}

func TestCachedContributionRoomRoutesExpires(t *testing.T) {
	inner := &countingContributionRoutes{}
	repo := newCachedContributionRoomRoutes(inner, 20*time.Millisecond)

	_, _ = repo.ResolveRouteForAPIKey(context.Background(), 1, 2)
	time.Sleep(40 * time.Millisecond)
	_, _ = repo.ResolveRouteForAPIKey(context.Background(), 1, 2)
	require.EqualValues(t, 2, inner.calls.Load())
}

func TestNewCachedContributionRoomRoutesKeepsNilRepository(t *testing.T) {
	require.Nil(t, newCachedContributionRoomRoutes(nil, time.Minute))
}
