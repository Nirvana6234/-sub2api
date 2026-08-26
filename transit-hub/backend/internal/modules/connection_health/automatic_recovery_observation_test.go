package connection_health

import (
	"context"
	"testing"

	"transithub/backend/internal/modules/upstream"
)

func TestPriorityRecoveryObservationNotifiesOnlyForAutomaticallyDeprioritizedTarget(t *testing.T) {
	repo := newFakeRepository()
	targetID := "sub2api:ws1:acc-17"
	repo.priorityStates["user1|ws1|group-1|"+targetID] = PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "group-1|" + targetID,
		OriginalPriority: 100, LastAppliedPriority: 10000, EffectiveMultiplier: 0.055,
		NotificationCauseKey: "balance_exhausted",
	}
	notifier := &recordingAutomaticRecoveryNotifier{}
	service := &Service{repo: repo, autoRecoveryNotifier: notifier}
	target := AdminProbeTarget{TargetID: targetID, AccountID: "acc-17", AccountName: "upstream-key", AdminGroupName: "plus"}
	results := []targetProbeResult{{
		previousState: StateDegraded,
		state:         &ConnectionHealthState{State: StateRecovering},
		outcome:       ProbeOutcome{Result: ResultOK},
		spec:          probeModelSpec{modelName: "gpt-5.6-sol"},
	}}

	service.notifyPriorityRecoveryObservation(context.Background(), "user1", "ws1", upstream.Session{Platform: upstream.PlatformSub2API}, target, results)
	if len(notifier.events) != 1 {
		t.Fatalf("expected one observation notification, got %+v", notifier.events)
	}
	event := notifier.events[0]
	if event.Stage != AutomaticRecoveryStageObserving || event.GroupID != "group-1" || event.EffectiveMultiplier != 0.055 {
		t.Fatalf("unexpected observation event: %+v", event)
	}

	results[0].previousState = StateRecovering
	service.notifyPriorityRecoveryObservation(context.Background(), "user1", "ws1", upstream.Session{Platform: upstream.PlatformSub2API}, target, results)
	if len(notifier.events) != 1 {
		t.Fatalf("continuing observation must not duplicate notification: %+v", notifier.events)
	}
}

func TestPriorityRecoveryObservationSkipsTemporaryFailure(t *testing.T) {
	repo := newFakeRepository()
	targetID := "sub2api:ws1:acc-17"
	repo.priorityStates["user1|ws1|group-1|"+targetID] = PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "group-1|" + targetID,
		OriginalPriority: 100, LastAppliedPriority: 10000, NotificationCauseKey: "network_failure",
	}
	notifier := &recordingAutomaticRecoveryNotifier{}
	service := &Service{repo: repo, autoRecoveryNotifier: notifier}
	results := []targetProbeResult{{
		previousState: StateDegraded, state: &ConnectionHealthState{State: StateRecovering},
		outcome: ProbeOutcome{Result: ResultOK}, spec: probeModelSpec{modelName: "gpt-5.6-sol"},
	}}

	service.notifyPriorityRecoveryObservation(context.Background(), "user1", "ws1", upstream.Session{Platform: upstream.PlatformSub2API}, AdminProbeTarget{TargetID: targetID}, results)
	if len(notifier.events) != 0 {
		t.Fatalf("temporary failure recovery must not notify, got %+v", notifier.events)
	}
}
