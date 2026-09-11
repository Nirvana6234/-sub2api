package connection_health

import (
	"context"
	"testing"

	"transithub/backend/internal/modules/upstream"
)

func targetActionBoolPtr(value bool) *bool { return &value }

func TestReconcileTargetRemoteAction_Sub2APIProtectsPermanentSwitch(t *testing.T) {
	repo := newFakeRepository()
	platform := &fakePlatformActioner{}
	service := &Service{repo: repo, dispatcher: newRemoteActionDispatcher(nil, nil, platform)}
	targetID := "sub2api:ws1:acc-1"
	repo.states[targetID] = map[string]ConnectionHealthState{
		"model-a": {ConnectionID: targetID, ModelName: "model-a", State: StateSuspended, CurrentWeight: 0},
	}
	repo.targetActionStates["user1|ws1|"+targetID] = TargetActionState{UserID: "user1", AdminAccountID: "ws1", TargetID: targetID, LastAppliedStatus: "inactive"}
	policy := Policy{ID: "p1", Enabled: true, AutoDegradeEnabled: true, AutoRemoteActionEnabled: true}
	target := AdminProbeTarget{
		TargetID: targetID, Platform: string(upstream.PlatformSub2API), AccountID: "acc-1",
		AccountStatus: "active", AccountSchedulable: targetActionBoolPtr(false),
		SchedulabilitySource: "manual",
	}

	action, err := service.reconcileTargetRemoteAction(context.Background(), "user1", "ws1", upstream.Session{Platform: upstream.PlatformSub2API}, target, []probeModelSpec{{modelName: "model-a", policy: policy}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != RemoteActionSkippedTargetInitiallyDisabled {
		t.Fatalf("manually disabled account must remain protected, action=%q", action)
	}
	if len(platform.sub2APICalls) != 0 {
		t.Fatalf("Sub2API permanent switches must never be written: %+v", platform.sub2APICalls)
	}
	if _, exists := repo.targetActionStates["user1|ws1|"+targetID]; exists {
		t.Fatal("legacy Sub2API action snapshot should be cleared locally")
	}
}

func TestReconcileTargetRemoteAction_RestoresOriginalNewAPIWeight(t *testing.T) {
	repo := newFakeRepository()
	platform := &fakePlatformActioner{}
	service := &Service{repo: repo, dispatcher: newRemoteActionDispatcher(nil, nil, platform)}
	targetID := "newapi:ws1:100"
	originalWeight, appliedWeight, currentWeight := 37, 25, 25
	repo.states[targetID] = map[string]ConnectionHealthState{"model-a": {ConnectionID: targetID, ModelName: "model-a", State: StateHealthy, CurrentWeight: 100}}
	repo.targetActionStates["user1|ws1|"+targetID] = TargetActionState{UserID: "user1", AdminAccountID: "ws1", TargetID: targetID, OriginalStatus: "1", OriginalWeight: &originalWeight, LastAppliedStatus: "1", LastAppliedWeight: &appliedWeight}
	policy := Policy{ID: "p1", Enabled: true, AutoDegradeEnabled: true, AutoRemoteActionEnabled: true}
	target := AdminProbeTarget{TargetID: targetID, Platform: string(upstream.PlatformNewAPI), AccountID: "100", AccountStatus: "1", AccountWeight: &currentWeight}

	action, err := service.reconcileTargetRemoteAction(context.Background(), "user1", "ws1", upstream.Session{Platform: upstream.PlatformNewAPI}, target, []probeModelSpec{{modelName: "model-a", policy: policy}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != "newapi_channel_weight_37" || len(platform.calls) != 1 || platform.calls[0].weight != 37 || platform.calls[0].status != 1 {
		t.Fatalf("expected exact original weight restore, action=%q calls=%+v", action, platform.calls)
	}
}

func TestReconcileTargetRemoteAction_ScalesNewAPIWeightFromOriginal(t *testing.T) {
	repo := newFakeRepository()
	platform := &fakePlatformActioner{}
	service := &Service{repo: repo, dispatcher: newRemoteActionDispatcher(nil, nil, platform)}
	targetID := "newapi:ws1:100"
	originalWeight, appliedWeight := 37, 0
	repo.states[targetID] = map[string]ConnectionHealthState{"model-a": {ConnectionID: targetID, ModelName: "model-a", State: StateDegraded, CurrentWeight: 75}}
	repo.targetActionStates["user1|ws1|"+targetID] = TargetActionState{UserID: "user1", AdminAccountID: "ws1", TargetID: targetID, OriginalStatus: "1", OriginalWeight: &originalWeight, LastAppliedStatus: "2", LastAppliedWeight: &appliedWeight}
	policy := Policy{ID: "p1", Enabled: true, AutoDegradeEnabled: true, AutoRemoteActionEnabled: true}
	target := AdminProbeTarget{TargetID: targetID, Platform: string(upstream.PlatformNewAPI), AccountID: "100", AccountStatus: "2", AccountWeight: &appliedWeight}

	action, err := service.reconcileTargetRemoteAction(context.Background(), "user1", "ws1", upstream.Session{Platform: upstream.PlatformNewAPI}, target, []probeModelSpec{{modelName: "model-a", policy: policy}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != "newapi_channel_weight_28" || len(platform.calls) != 1 || platform.calls[0].weight != 28 {
		t.Fatalf("75%% recovery of original weight 37 must write 28, action=%q calls=%+v", action, platform.calls)
	}
}

func TestRestoreUnmanagedTargetActions_ClearsLegacySub2APISnapshotWithoutWrite(t *testing.T) {
	repo := newFakeRepository()
	platform := &fakePlatformActioner{}
	service := &Service{repo: repo, dispatcher: newRemoteActionDispatcher(nil, nil, platform)}
	targetID := "sub2api:ws1:acc-1"
	stored := TargetActionState{UserID: "user1", AdminAccountID: "ws1", TargetID: targetID, OriginalStatus: "active", LastAppliedStatus: "inactive"}
	repo.targetActionStates["user1|ws1|"+targetID] = stored

	service.restoreUnmanagedTargetActions(context.Background(), nil, nil, nil, nil, []TargetActionState{stored}, make(adminInventoryCache))
	if len(platform.sub2APICalls) != 0 {
		t.Fatalf("legacy snapshot cleanup must not write Sub2API: %+v", platform.sub2APICalls)
	}
	if _, exists := repo.targetActionStates["user1|ws1|"+targetID]; exists {
		t.Fatal("legacy Sub2API snapshot must be removed")
	}
}
