package connection_health

import (
	"context"
	"testing"

	"transithub/backend/internal/modules/upstream"
)

func TestManualUpstreamKeyMultiplier_SetReadAndClear(t *testing.T) {
	repo := newFakeRepository()
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "plus"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Name: "keiko.lol", BaseURL: "https://up.example.com"}},
		},
	}
	service := newAdminGroupsService(reader, fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}, repo)
	ctx := context.Background()
	const targetID = "newapi:ws1:100"
	value := 0.08

	saved, err := service.SetManualUpstreamKeyMultiplier(ctx, "user1", targetID, ManualUpstreamKeyMultiplierInput{Multiplier: &value})
	if err != nil {
		t.Fatalf("set manual multiplier: %v", err)
	}
	if saved.Multiplier != value || saved.TargetID != targetID {
		t.Fatalf("saved manual multiplier = %+v", saved)
	}

	stored, err := repo.ListManualUpstreamKeyMultipliers(ctx, "user1", "ws1")
	if err != nil || len(stored) != 1 || stored[0].Multiplier != value {
		t.Fatalf("stored manual multiplier = %+v, err=%v", stored, err)
	}
	groups, err := service.AdminGroups(ctx, "user1")
	if err != nil {
		t.Fatalf("read groups after save: %v", err)
	}
	account := groups[0].Accounts[0]
	if account.UpstreamKeyGroupMultiplier == nil || *account.UpstreamKeyGroupMultiplier != value || account.UpstreamKeyGroupMultiplierSource != "manual" {
		t.Fatalf("manual multiplier was not returned as the fallback: %+v", account)
	}

	if err := service.ClearManualUpstreamKeyMultiplier(ctx, "user1", targetID); err != nil {
		t.Fatalf("clear manual multiplier: %v", err)
	}
	stored, err = repo.ListManualUpstreamKeyMultipliers(ctx, "user1", "ws1")
	if err != nil || len(stored) != 0 {
		t.Fatalf("manual multiplier remained after clear: %+v, err=%v", stored, err)
	}
	groups, err = service.AdminGroups(ctx, "user1")
	if err != nil {
		t.Fatalf("read groups after clear: %v", err)
	}
	account = groups[0].Accounts[0]
	if account.UpstreamKeyGroupMultiplier != nil || account.UpstreamKeyGroupMultiplierSource != "" {
		t.Fatalf("manual fallback remained after clear: %+v", account)
	}
}
