package upstream

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

// Shared group-rate reads are intentionally short-lived: the upstream may
// change a multiplier without a TransitHub synchronization request, while a
// health scheduler tick must not fan out one request per page/target.
const sharedGroupRateCacheTTL = 5 * time.Minute

type groupRateCacheEntry struct {
	groups     []GroupInfo
	observedAt time.Time
}

type groupRateFetch struct {
	done   chan struct{}
	groups []GroupInfo
	err    error
}

// CurrentGroupReader is the single source used by UI-facing multiplier
// consumers. Implementations must scope reads by TransitHub user/workspace.
type CurrentGroupReader interface {
	CurrentGroups(ctx context.Context, userID, adminAccountID, siteID string) ([]GroupInfo, error)
}

// GroupRateSnapshotRefresher lets the group-rate page refresh the same source
// before reading its persisted history. It is deliberately a small interface
// to avoid coupling upstream and group_rates packages.
type GroupRateSnapshotRefresher interface {
	RefreshGroupRateSnapshots(ctx context.Context, userID, adminAccountID string) error
}

func (s *Service) CurrentGroups(ctx context.Context, userID, adminAccountID, siteID string) ([]GroupInfo, error) {
	userID = strings.TrimSpace(userID)
	adminAccountID = strings.TrimSpace(adminAccountID)
	siteID = strings.TrimSpace(siteID)
	if userID == "" || adminAccountID == "" || siteID == "" {
		return nil, errors.New("invalid group-rate scope")
	}
	site, err := s.cache.Get(ctx, siteID)
	if err != nil {
		return nil, err
	}
	if site == nil || site.UserID != userID || site.AdminAccountID != adminAccountID || site.Session == nil {
		return nil, errors.New("upstream site is not available in workspace")
	}

	key := userID + "|" + adminAccountID + "|" + siteID
	s.groupRateMu.Lock()
	if s.groupRateCache == nil {
		s.groupRateCache = make(map[string]groupRateCacheEntry)
	}
	if s.groupRateFetch == nil {
		s.groupRateFetch = make(map[string]*groupRateFetch)
	}
	if entry, ok := s.groupRateCache[key]; ok && time.Since(entry.observedAt) < sharedGroupRateCacheTTL {
		groups := cloneGroupInfos(entry.groups)
		s.groupRateMu.Unlock()
		return groups, nil
	}
	if fetch, ok := s.groupRateFetch[key]; ok {
		s.groupRateMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-fetch.done:
			return cloneGroupInfos(fetch.groups), fetch.err
		}
	}
	fetch := &groupRateFetch{done: make(chan struct{})}
	s.groupRateFetch[key] = fetch
	s.groupRateMu.Unlock()

	// Mapping and health pages can be opened long after the site was added. Make
	// the upstream session current before reading groups; otherwise an expired
	// JWT keeps producing 401 responses while the upstream dashboard itself is
	// still usable. Older sessions may not have ExpiresAt, so retry once through
	// the refresh token when the first group request is rejected.
	session := *site.Session
	if refreshed, refreshErr := s.platformService.RefreshSession(session); refreshErr == nil {
		session = refreshed
		s.persistRefreshedSiteSession(ctx, site, session)
	} else {
		log.Printf("[group-rates] upstream session refresh failed site_id=%s err=%v; trying current session", siteID, refreshErr)
	}

	groups, fetchErr := s.platformService.FetchAdminGroups(session)
	if fetchErr != nil && errorKey(fetchErr) == ErrorAuth && session.Platform == PlatformSub2API && strings.TrimSpace(session.RefreshToken) != "" {
		if refreshed, refreshErr := s.platformService.refreshSub2APISession(session); refreshErr == nil {
			session = refreshed
			s.persistRefreshedSiteSession(ctx, site, session)
			groups, fetchErr = s.platformService.FetchAdminGroups(session)
		} else {
			log.Printf("[group-rates] forced upstream session refresh failed site_id=%s err=%v", siteID, refreshErr)
		}
	}
	if fetchErr == nil {
		s.rememberCurrentGroups(key, groups, time.Now())
		s.saveGroupsSnapshot(ctx, site, groups)
	}

	s.groupRateMu.Lock()
	delete(s.groupRateFetch, key)
	fetch.groups = cloneGroupInfos(groups)
	fetch.err = fetchErr
	close(fetch.done)
	s.groupRateMu.Unlock()
	return groups, fetchErr
}

func (s *Service) persistRefreshedSiteSession(ctx context.Context, site *Site, session Session) {
	if site == nil || sessionsEqual(*site.Session, session) {
		return
	}
	site.Session = &session
	if err := s.cache.Set(ctx, site); err != nil {
		log.Printf("[group-rates] refreshed session cache write failed site_id=%s err=%v", site.ID, err)
	}
	if s.repository != nil {
		if err := s.repository.SaveSite(ctx, *site); err != nil {
			log.Printf("[group-rates] refreshed session persistence failed site_id=%s err=%v", site.ID, err)
		}
	}
}

func sessionsEqual(left, right Session) bool {
	return left.Platform == right.Platform && left.BaseURL == right.BaseURL &&
		left.Cookie == right.Cookie && left.UserID == right.UserID &&
		left.AccessToken == right.AccessToken && left.AdminAPIKey == right.AdminAPIKey &&
		left.RefreshToken == right.RefreshToken && left.TokenType == right.TokenType &&
		(left.ExpiresAt == nil && right.ExpiresAt == nil || left.ExpiresAt != nil && right.ExpiresAt != nil && *left.ExpiresAt == *right.ExpiresAt)
}

// RefreshGroupRateSnapshots refreshes every site in the requested workspace.
// A single failed site does not hide the other persisted snapshots.
func (s *Service) RefreshGroupRateSnapshots(ctx context.Context, userID, adminAccountID string) error {
	sites, err := s.cache.ListByUser(ctx, strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	var firstErr error
	for _, site := range sites {
		if site == nil || site.AdminAccountID != strings.TrimSpace(adminAccountID) {
			continue
		}
		if _, err := s.CurrentGroups(ctx, userID, adminAccountID, site.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) RefreshGroupRateSnapshot(ctx context.Context, userID, adminAccountID, siteID string) error {
	_, err := s.CurrentGroups(ctx, userID, adminAccountID, siteID)
	return err
}

func (s *Service) rememberCurrentGroups(key string, groups []GroupInfo, observedAt time.Time) {
	s.groupRateMu.Lock()
	s.groupRateCache[key] = groupRateCacheEntry{groups: cloneGroupInfos(groups), observedAt: observedAt}
	s.groupRateMu.Unlock()
}

func (s *Service) saveGroupsSnapshot(ctx context.Context, site *Site, groups []GroupInfo) {
	if s.snapshotWriter == nil || site == nil || strings.TrimSpace(site.UserID) == "" {
		return
	}
	snapshots := make([]SnapshotGroup, 0, len(groups))
	for _, group := range groups {
		snapshots = append(snapshots, SnapshotGroup{ID: group.ID, Name: group.Name, Platform: group.Platform, Multiplier: group.Multiplier})
	}
	snapshotCtx, cancel := context.WithTimeout(ctx, persistenceTimeout)
	defer cancel()
	if err := s.snapshotWriter.SaveSiteSnapshot(snapshotCtx, site.UserID, site.AdminAccountID, site.ID, site.Name, site.Platform, snapshots); err != nil {
		log.Printf("group rate snapshot failed site_id=%s: %v", site.ID, err)
	}
}

func cloneGroupInfos(groups []GroupInfo) []GroupInfo {
	if groups == nil {
		return nil
	}
	cloned := make([]GroupInfo, len(groups))
	for i, group := range groups {
		cloned[i] = group
		if group.Multiplier != nil {
			value := *group.Multiplier
			cloned[i].Multiplier = &value
		}
		if group.DefaultMultiplier != nil {
			value := *group.DefaultMultiplier
			cloned[i].DefaultMultiplier = &value
		}
		if group.DedicatedMultiplier != nil {
			value := *group.DedicatedMultiplier
			cloned[i].DedicatedMultiplier = &value
		}
	}
	return cloned
}

// compile-time assertion documents the intended server wiring.
var _ CurrentGroupReader = (*Service)(nil)
var _ GroupRateSnapshotRefresher = (*Service)(nil)
