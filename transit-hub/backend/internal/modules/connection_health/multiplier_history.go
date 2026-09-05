package connection_health

import (
	"context"
	"log"
	"strings"
	"time"

	"transithub/backend/internal/modules/my_sites"
)

type multiplierHistoryRepository interface {
	UpsertUpstreamKeyMultiplierHistory(context.Context, upstreamKeyMultiplierHistoryRecord) error
	ListUpstreamKeyMultiplierHistory(context.Context, string, string, string, string) ([]UpstreamKeyMultiplierHistoryPoint, error)
}

type backgroundRealConnectionReader interface {
	ListAllRealConnectionsForBackground(context.Context) ([]my_sites.RealConnection, error)
}

type liveUpstreamKeyGroupReader interface {
	ListUpstreamKeyGroupSnapshotsForWorkspace(context.Context, string, string, string) ([]my_sites.UpstreamKeyGroupSnapshot, error)
}

const multiplierSnapshotInterval = time.Hour

// StartMultiplierSnapshotScheduler records upstream-key cost inputs separately
// from health probes, so unmonitored channels are still included in accounting.
func (s *Service) StartMultiplierSnapshotScheduler(ctx context.Context) {
	go func() {
		s.collectMultiplierSnapshotsSafely(ctx)
		ticker := time.NewTicker(multiplierSnapshotInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.collectMultiplierSnapshotsSafely(ctx)
			}
		}
	}()
}

func (s *Service) collectMultiplierSnapshotsSafely(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[connection-health] multiplier snapshot panic recovered: %v", recovered)
		}
	}()
	s.collectMultiplierSnapshots(ctx)
}

func (s *Service) collectMultiplierSnapshots(ctx context.Context) {
	repository, ok := s.repo.(multiplierHistoryRepository)
	if !ok {
		return
	}
	connectionsReader, ok := s.mySites.(backgroundRealConnectionReader)
	if !ok {
		return
	}
	liveReader, ok := s.mySites.(liveUpstreamKeyGroupReader)
	if !ok {
		return
	}
	connections, err := connectionsReader.ListAllRealConnectionsForBackground(ctx)
	if err != nil {
		log.Printf("[connection-health] multiplier snapshot list connections failed: %v", err)
		return
	}

	snapshotsBySite := make(map[string]map[string]my_sites.UpstreamKeyGroupSnapshot)
	failedSites := make(map[string]bool)
	manualByWorkspace := make(map[string]map[string]float64)
	for _, connection := range connections {
		userID, workspaceID := strings.TrimSpace(connection.UserID), strings.TrimSpace(connection.WorkspaceAdminAccountID)
		siteID, keyID, adminID := strings.TrimSpace(connection.UpstreamSiteID), strings.TrimSpace(connection.UpstreamKeyID), strings.TrimSpace(connection.AdminAccountID)
		platform := strings.TrimSpace(connection.AdminPlatform)
		if userID == "" || workspaceID == "" || siteID == "" || keyID == "" || adminID == "" || platform == "" {
			continue
		}
		targetID := buildTargetID(platform, workspaceID, adminID)
		cacheKey := userID + "|" + workspaceID + "|" + siteID
		values, loaded := snapshotsBySite[cacheKey]
		if !loaded && !failedSites[cacheKey] {
			items, readErr := liveReader.ListUpstreamKeyGroupSnapshotsForWorkspace(ctx, userID, workspaceID, siteID)
			if readErr != nil {
				failedSites[cacheKey] = true
			} else {
				values = make(map[string]my_sites.UpstreamKeyGroupSnapshot, len(items))
				for _, item := range items {
					if item.KeyID != "" {
						values[item.KeyID] = item
					}
				}
				snapshotsBySite[cacheKey] = values
			}
		}
		if snapshot, found := values[keyID]; found && snapshot.Multiplier != nil {
			if err := repository.UpsertUpstreamKeyMultiplierHistory(ctx, upstreamKeyMultiplierHistoryRecord{UserID: userID, AdminAccountID: workspaceID, TargetID: targetID, SiteID: siteID, KeyID: keyID, GroupID: snapshot.GroupID, GroupName: snapshot.GroupName, Multiplier: *snapshot.Multiplier, Source: "detected", ObservedAt: time.Now()}); err != nil {
				log.Printf("[connection-health] multiplier snapshot save failed target_id=%s err=%v", targetID, err)
			}
			continue
		}
		workspaceKey := userID + "|" + workspaceID
		manual := manualByWorkspace[workspaceKey]
		if manual == nil {
			items, listErr := s.repo.ListManualUpstreamKeyMultipliers(ctx, userID, workspaceID)
			if listErr != nil {
				continue
			}
			manual = make(map[string]float64, len(items))
			for _, item := range items {
				manual[item.TargetID] = item.Multiplier
			}
			manualByWorkspace[workspaceKey] = manual
		}
		if multiplier, found := manual[targetID]; found {
			if err := repository.UpsertUpstreamKeyMultiplierHistory(ctx, upstreamKeyMultiplierHistoryRecord{UserID: userID, AdminAccountID: workspaceID, TargetID: targetID, SiteID: siteID, KeyID: keyID, Multiplier: multiplier, Source: "manual", ObservedAt: time.Now()}); err != nil {
				log.Printf("[connection-health] manual multiplier snapshot save failed target_id=%s err=%v", targetID, err)
			}
		}
	}
}

func (s *Service) UpstreamKeyMultiplierHistory(ctx context.Context, userID, targetID, interval string) ([]UpstreamKeyMultiplierHistoryPoint, error) {
	workspaceID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return nil, err
	}
	repository, ok := s.repo.(multiplierHistoryRepository)
	if !ok {
		return []UpstreamKeyMultiplierHistoryPoint{}, nil
	}
	return repository.ListUpstreamKeyMultiplierHistory(ctx, userID, workspaceID, targetID, interval)
}
