package service

import (
	"context"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// contributionRoomRouteCacheTTL bounds how long a room selection, membership
// or rate change can take to reach scheduling. Each request resolves the route
// at least twice (sticky check and candidate listing, again on failover), so
// even a short TTL removes most lookups.
const contributionRoomRouteCacheTTL = 2 * time.Second

// cachedContributionRoomRoutes memoizes ResolveRouteForAPIKey per user and API
// key. A route also authorizes access to other users' contributed accounts, so
// a revoked selection stays usable for at most the TTL. Returned routes are
// shared and must be treated as read-only; lookup errors are not cached.
type cachedContributionRoomRoutes struct {
	inner   ContributionRoomRoutingRepository
	ttl     time.Duration
	entries sync.Map // "userID:apiKeyID" -> *cachedContributionRoomRoute
	loads   singleflight.Group
}

type cachedContributionRoomRoute struct {
	route     *ContributionRoomRoute
	expiresAt time.Time
}

func newCachedContributionRoomRoutes(inner ContributionRoomRoutingRepository, ttl time.Duration) ContributionRoomRoutingRepository {
	if inner == nil {
		return nil
	}
	return &cachedContributionRoomRoutes{inner: inner, ttl: ttl}
}

func (c *cachedContributionRoomRoutes) ResolveRouteForAPIKey(ctx context.Context, userID, apiKeyID int64) (*ContributionRoomRoute, error) {
	key := strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(apiKeyID, 10)
	if entry, ok := c.entries.Load(key); ok {
		cached := entry.(*cachedContributionRoomRoute)
		if time.Now().Before(cached.expiresAt) {
			return cached.route, nil
		}
	}
	result, err, _ := c.loads.Do(key, func() (any, error) {
		route, err := c.inner.ResolveRouteForAPIKey(ctx, userID, apiKeyID)
		if err != nil {
			return nil, err
		}
		c.entries.Store(key, &cachedContributionRoomRoute{route: route, expiresAt: time.Now().Add(c.ttl)})
		return route, nil
	})
	if err != nil {
		return nil, err
	}
	route, _ := result.(*ContributionRoomRoute)
	return route, nil
}
