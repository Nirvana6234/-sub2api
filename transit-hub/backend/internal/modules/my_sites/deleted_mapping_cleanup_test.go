package my_sites

import (
	"context"
	"testing"
)

type deletedMappingCleanupRepo struct {
	state *State
}

func (r *deletedMappingCleanupRepo) Get(context.Context, string, string) (*State, error) {
	return r.state, nil
}

func (r *deletedMappingCleanupRepo) Save(_ context.Context, state State) error {
	r.state = &state
	return nil
}

func TestCleanupDeletedUpstreamSitesRemovesOnlyDeletedTargets(t *testing.T) {
	repo := &deletedMappingCleanupRepo{state: &State{Mappings: []GroupMapping{
		{OwnGroup: "plus", UpstreamTargets: []UpstreamGroupRef{
			{SiteID: "deleted-site", GroupName: "old"},
			{SiteID: "live-site", GroupName: "current"},
		}},
		{OwnGroup: "only-old", UpstreamTargets: []UpstreamGroupRef{{SiteID: "deleted-site", GroupName: "other"}}},
	}}}
	service := NewService(repo, nil, nil)

	if err := service.CleanupDeletedUpstreamSites(context.Background(), "user-1", "admin-1", []string{"deleted-site"}); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if len(repo.state.Mappings) != 1 || repo.state.Mappings[0].OwnGroup != "plus" {
		t.Fatalf("deleted-only mappings were not removed: %+v", repo.state.Mappings)
	}
	targets := repo.state.Mappings[0].UpstreamTargets
	if len(targets) != 1 || targets[0].SiteID != "live-site" || targets[0].GroupName != "current" {
		t.Fatalf("live mapping changed during cleanup: %+v", targets)
	}
}

func TestCleanupDeletedUpstreamSitesIgnoresBlankIDs(t *testing.T) {
	repo := &deletedMappingCleanupRepo{state: &State{Mappings: []GroupMapping{{
		OwnGroup: "plus", UpstreamTargets: []UpstreamGroupRef{{SiteID: "live-site", GroupName: "current"}},
	}}}}
	service := NewService(repo, nil, nil)

	if err := service.CleanupDeletedUpstreamSites(context.Background(), "user-1", "admin-1", []string{"", "  "}); err != nil {
		t.Fatalf("blank site IDs should be a no-op: %v", err)
	}
	if len(repo.state.Mappings) != 1 || len(repo.state.Mappings[0].UpstreamTargets) != 1 {
		t.Fatalf("blank IDs changed mappings: %+v", repo.state.Mappings)
	}
}

func TestCleanupMissingUpstreamSitesUsesAuthoritativeInventory(t *testing.T) {
	repo := &deletedMappingCleanupRepo{state: &State{Mappings: []GroupMapping{{
		OwnGroup: "plus", UpstreamTargets: []UpstreamGroupRef{
			{SiteID: "live-site", GroupName: "current"},
			{SiteID: "deleted-site", GroupName: "old"},
		},
	}}}}
	service := NewService(repo, nil, nil)

	if err := service.CleanupMissingUpstreamSites(context.Background(), "user-1", "admin-1", []string{"live-site"}); err != nil {
		t.Fatalf("missing-site cleanup failed: %v", err)
	}
	if len(repo.state.Mappings) != 1 || len(repo.state.Mappings[0].UpstreamTargets) != 1 || repo.state.Mappings[0].UpstreamTargets[0].SiteID != "live-site" {
		t.Fatalf("unexpected mappings after missing-site cleanup: %+v", repo.state.Mappings)
	}
}

func TestCleanupMissingUpstreamSitesRemovesAllWhenInventoryIsEmpty(t *testing.T) {
	repo := &deletedMappingCleanupRepo{state: &State{Mappings: []GroupMapping{{
		OwnGroup: "plus", UpstreamTargets: []UpstreamGroupRef{{SiteID: "deleted-site", GroupName: "old"}},
	}}}}
	service := NewService(repo, nil, nil)

	if err := service.CleanupMissingUpstreamSites(context.Background(), "user-1", "admin-1", nil); err != nil {
		t.Fatalf("empty authoritative inventory cleanup failed: %v", err)
	}
	if len(repo.state.Mappings) != 0 {
		t.Fatalf("stale mappings remained after empty inventory cleanup: %+v", repo.state.Mappings)
	}
}
