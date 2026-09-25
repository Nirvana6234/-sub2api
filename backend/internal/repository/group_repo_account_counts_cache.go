package repository

import (
	"context"
	"sync"
	"time"
)

// activeGroupAccountCountsTTL bounds how stale the account counts attached to
// the active-group listings may be. Those listings back the user's available
// groups and automatic group routing, which ask on every client refresh and
// on every auto-group request; each fresh count is an aggregate join over
// account_groups and accounts.
const activeGroupAccountCountsTTL = 15 * time.Second

// groupAccountCountsCache holds per-group counts for the active listings. Its
// zero value is ready to use. Admin listings and GetByID do not use it and
// always read fresh counts.
type groupAccountCountsCache struct {
	mu      sync.Mutex
	entries map[int64]cachedGroupAccountCounts
}

type cachedGroupAccountCounts struct {
	counts    groupAccountCounts
	expiresAt time.Time
}

// cachedActiveAccountCounts returns counts for groupIDs, loading only the
// groups whose cached counts are missing or expired. A group without accounts
// is cached with zero counts, as loadAccountCounts omits it.
func (r *groupRepository) cachedActiveAccountCounts(ctx context.Context, groupIDs []int64) (map[int64]groupAccountCounts, error) {
	cache := &r.activeCounts
	now := time.Now()
	counts := make(map[int64]groupAccountCounts, len(groupIDs))
	var missing []int64

	cache.mu.Lock()
	for _, id := range groupIDs {
		if entry, ok := cache.entries[id]; ok && now.Before(entry.expiresAt) {
			counts[id] = entry.counts
		} else {
			missing = append(missing, id)
		}
	}
	cache.mu.Unlock()
	if len(missing) == 0 {
		return counts, nil
	}

	loaded, err := r.loadAccountCounts(ctx, missing)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(activeGroupAccountCountsTTL)
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[int64]cachedGroupAccountCounts)
	}
	for _, id := range missing {
		c := loaded[id]
		counts[id] = c
		cache.entries[id] = cachedGroupAccountCounts{counts: c, expiresAt: expiresAt}
	}
	cache.mu.Unlock()
	return counts, nil
}
