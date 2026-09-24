package service

import (
	"context"
	"testing"
)

func TestNextFallbackGroupIDTriesTargetsInOrder(t *testing.T) {
	groups := map[int64]*Group{
		1: {ID: 1, Platform: PlatformOpenAI, Status: StatusActive, FallbackGroupIDs: []int64{2, 3}},
		2: {ID: 2, Platform: PlatformOpenAI, Status: "inactive", IsFallbackPool: true},
		3: {ID: 3, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
	}
	id, _, ok := nextFallbackGroupID(context.Background(), 1, fallbackGroupState{}, fallbackTraversal{
		logNS:          "test",
		resolveGroup:   func(_ context.Context, id int64) *Group { return groups[id] },
		currentGroupOK: func(group *Group) bool { return group.Status == StatusActive },
		fallbackGroupOK: func(group *Group) (bool, string) {
			if group.Status != StatusActive || !group.IsFallbackPool {
				return false, "not_available"
			}
			return true, ""
		},
	})
	if id != 3 {
		t.Fatalf("next fallback id = %v, want 3", id)
	}
	if !ok {
		t.Fatal("expected fallback target to be accepted")
	}
}
