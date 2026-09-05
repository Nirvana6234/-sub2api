//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type autoGroupMetricRepoForReview struct {
	personal      map[int64][]int64
	peer          map[int64][]int64
	sharedPeer    map[int64]map[int64][]int64
	personalCalls int
	peerCalls     int
}

func (r *autoGroupMetricRepoForReview) ListRecentGroupFirstTokenSamples(_ context.Context, _ int64, _ []int64, _ string, _ time.Time, _ int) (map[int64][]int64, error) {
	r.personalCalls++
	return r.personal, nil
}

func (r *autoGroupMetricRepoForReview) ListRecentPeerGroupFirstTokenSamples(_ context.Context, _ []int64, _ string, _ time.Time, _ int) (map[int64]map[int64][]int64, error) {
	r.peerCalls++
	if r.sharedPeer != nil {
		return r.sharedPeer, nil
	}
	shared := make(map[int64]map[int64][]int64, len(r.peer))
	for groupID, samples := range r.peer {
		shared[groupID] = map[int64][]int64{8: samples}
	}
	return shared, nil
}

func newAutoGroupReviewService(metrics *autoGroupMetricRepoForReview) *APIKeyService {
	svc := NewAPIKeyService(
		nil,
		&userRepoStub{user: &User{ID: 7, Status: StatusActive}},
		&playgroundGroupRepoStub{groups: []Group{
			{ID: 10, Name: "current", Status: StatusActive, Platform: PlatformOpenAI, RateMultiplier: 0.6, ActiveAccountCount: 1},
			{ID: 20, Name: "cheaper", Status: StatusActive, Platform: PlatformOpenAI, RateMultiplier: 0.2, ActiveAccountCount: 1},
		}},
		playgroundSubscriptionRepoStub{},
		&userGroupRateRepoStubForGroupRate{},
		nil,
		nil,
	)
	svc.SetAutoGroupMetricRepository(metrics)
	return svc
}

func storeSettledAutoGroupForReview(svc *APIKeyService, apiKey *APIKey, reviewAt time.Time) string {
	key := autoGroupSelectionKey(apiKey, "gpt-test")
	group := &Group{ID: 10, Name: "current", Status: StatusActive, Platform: PlatformOpenAI, RateMultiplier: 0.6, ActiveAccountCount: 1}
	svc.autoGroupSelections.Store(key, autoGroupSelection{
		userID:            apiKey.UserID,
		candidateGroupIDs: append([]int64(nil), apiKey.AutoGroupIDs...),
		groupID:           group.ID,
		selectedGroup:     group,
		configFingerprint: autoGroupConfigFingerprint(apiKey),
		priceReviewAt:     reviewAt,
		settled:           true,
	})
	return key
}

func TestResolveAutoGroupPriceReviewWakesForPeerVerifiedCheaperGroup(t *testing.T) {
	metrics := &autoGroupMetricRepoForReview{
		personal: map[int64][]int64{10: {4_000, 4_100, 4_200}},
		peer:     map[int64][]int64{20: {2_000, 2_100, 2_200}},
	}
	svc := newAutoGroupReviewService(metrics)
	apiKey := &APIKey{ID: 91, UserID: 7, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyBalanced, AutoGroupIDs: []int64{10, 20}}
	storeSettledAutoGroupForReview(svc, apiKey, time.Now().Add(-time.Second))

	resolved, err := svc.ResolveAutoGroupForModel(context.Background(), apiKey, "gpt-test")

	require.NoError(t, err)
	require.NotNil(t, resolved.GroupID)
	require.Equal(t, int64(20), *resolved.GroupID)
	require.Equal(t, 1, metrics.peerCalls)
	require.Equal(t, 1, metrics.personalCalls)
}

func TestResolveAutoGroupPriceReviewKeepsCurrentWhenPeerSpeedIsUnreliable(t *testing.T) {
	metrics := &autoGroupMetricRepoForReview{
		peer: map[int64][]int64{20: {31_000, 31_100, 31_200}},
	}
	svc := newAutoGroupReviewService(metrics)
	apiKey := &APIKey{ID: 92, UserID: 7, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyBalanced, AutoGroupIDs: []int64{10, 20}}
	selectionKey := storeSettledAutoGroupForReview(svc, apiKey, time.Now().Add(-time.Second))

	resolved, err := svc.ResolveAutoGroupForModel(context.Background(), apiKey, "gpt-test")

	require.NoError(t, err)
	require.NotNil(t, resolved.GroupID)
	require.Equal(t, int64(10), *resolved.GroupID)
	require.Equal(t, 1, metrics.peerCalls)
	require.Zero(t, metrics.personalCalls)
	require.True(t, svc.autoGroupSelection(selectionKey).priceReviewAt.After(time.Now().Add(14*time.Minute)))
}

func TestRecentPeerAutoGroupMetricsSharesCacheAndExcludesCurrentUser(t *testing.T) {
	metrics := &autoGroupMetricRepoForReview{
		sharedPeer: map[int64]map[int64][]int64{
			20: {
				7: {7_000, 7_100, 7_200},
				8: {8_000, 8_100, 8_200},
			},
		},
	}
	svc := newAutoGroupReviewService(metrics)
	groups := []Group{{ID: 20, ActiveAccountCount: 1}}

	forUser7, err := svc.recentPeerAutoGroupMetrics(context.Background(), 7, groups, "gpt-test")
	require.NoError(t, err)
	require.Equal(t, []int64{8_000, 8_100, 8_200}, forUser7[20])

	forUser8, err := svc.recentPeerAutoGroupMetrics(context.Background(), 8, groups, "gpt-test")
	require.NoError(t, err)
	require.Equal(t, []int64{7_000, 7_100, 7_200}, forUser8[20])
	require.Equal(t, 1, metrics.peerCalls)
}

func TestAutoGroupCacheSweepRemovesExpiredEntries(t *testing.T) {
	svc := &APIKeyService{}
	now := time.Now()
	svc.autoGroupMetricsCache.Store("personal", autoGroupMetricsCacheEntry{expiresAt: now.Add(-time.Second)})
	svc.autoGroupPeerMetricsCache.Store("peer", autoGroupPeerMetricsCacheEntry{expiresAt: now.Add(-time.Second)})
	svc.autoGroupSelections.Store("selection", autoGroupSelection{expiresAt: now.Add(-time.Second)})
	svc.autoGroupCacheOperations.Store(autoGroupCacheSweepEvery - 1)

	svc.maybeSweepAutoGroupCaches(now)

	_, personalFound := svc.autoGroupMetricsCache.Load("personal")
	_, peerFound := svc.autoGroupPeerMetricsCache.Load("peer")
	_, selectionFound := svc.autoGroupSelections.Load("selection")
	require.False(t, personalFound)
	require.False(t, peerFound)
	require.False(t, selectionFound)
}
