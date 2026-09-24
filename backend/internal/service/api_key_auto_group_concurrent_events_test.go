// 这些回归用例来自 2026-09-24 对自动分组/兜底池改动的外部评审复现。

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func regressionAutoGroupSetup(strategy string, ids []int64) (*APIKeyService, *APIKey) {
	svc := newFailoverPersistService([]Group{
		{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: .1, ActiveAccountCount: 1},
		{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: .2, ActiveAccountCount: 1},
		{ID: 30, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: .3, ActiveAccountCount: 1},
	})
	return svc, &APIKey{ID: 98765, UserID: 7, AutoGroup: true, AutoGroupStrategy: strategy, AutoGroupIDs: ids}
}

func regressionResolve(t *testing.T, svc *APIKeyService, key *APIKey, excluded map[int64]struct{}) *APIKey {
	t.Helper()
	got, err := svc.ResolveAutoGroupForModelExcluding(context.Background(), key, "gpt-regression", excluded)
	if err != nil || got == nil || got.GroupID == nil {
		t.Fatalf("resolve: key=%v err=%v", got, err)
	}
	return got
}

// Two in-flight requests both routed to A. The first switches to B. The later
// failure belongs to A and must not mark the healthy replacement B as failed.
func TestAutoGroupStaleFailureMustNotEvictReplacement(t *testing.T) {
	svc, key := regressionAutoGroupSetup(autoGroupStrategyPrice, []int64{10, 20, 30})
	onA := regressionResolve(t, svc, key, nil)
	onB := regressionResolve(t, svc, onA, map[int64]struct{}{10: {}})
	if *onB.GroupID != 20 {
		t.Fatalf("first failover=%d, want 20", *onB.GroupID)
	}
	late := regressionResolve(t, svc, onA, map[int64]struct{}{10: {}})
	if *late.GroupID != 20 {
		t.Fatalf("late failure from group 10 evicted healthy replacement: got %d, want 20", *late.GroupID)
	}
}

func TestAutoGroupStaleObservationMustNotPoisonReplacement(t *testing.T) {
	svc, key := regressionAutoGroupSetup(autoGroupStrategyPrice, []int64{10, 20, 30})
	onA := regressionResolve(t, svc, key, nil)
	onB := regressionResolve(t, svc, onA, map[int64]struct{}{10: {}})
	for i := 0; i < autoGroupTransientFailureThreshold; i++ {
		svc.ObserveAutoGroupRequestResult(onA, "gpt-regression", 503, nil)
	}
	state := svc.autoGroupSelection(autoGroupSelectionKey(key, "gpt-regression"))
	if state.failureTriggered || state.transientFailureStreak != 0 {
		t.Fatalf("group %d received old group 10's failures: failureTriggered=%v streak=%d eventGroupID=%d", *onB.GroupID, state.failureTriggered, state.transientFailureStreak, state.eventGroupID)
	}
}

func TestAutoGroupAllStrategiesPreserveAndExhaust(t *testing.T) {
	for _, strategy := range []string{autoGroupStrategyPrice, autoGroupStrategyBalanced, autoGroupStrategySpeed} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit=%v", strategy, explicit), func(t *testing.T) {
				var ids []int64
				if explicit {
					ids = []int64{10, 20, 30}
				}
				svc, key := regressionAutoGroupSetup(strategy, ids)
				onA := regressionResolve(t, svc, key, nil)
				// Pre-existing voluntary switch budget is already exhausted.
				state, _ := svc.autoGroupSwitchStates.Load(autoGroupSwitchStateKey(key))
				switchState := state.(autoGroupSwitchState)
				switchState.switchedAt = []time.Time{time.Now()}
				svc.autoGroupSwitchStates.Store(autoGroupSwitchStateKey(key), switchState)
				onB := regressionResolve(t, svc, onA, map[int64]struct{}{10: {}})
				if *onB.GroupID != 20 || autoGroupConfigFingerprint(onB) != autoGroupConfigFingerprint(key) {
					t.Fatalf("first failover=%d or fingerprint mismatch", *onB.GroupID)
				}
				next := regressionResolve(t, svc, key, nil)
				if *next.GroupID != 20 {
					t.Fatalf("next request=%d want 20", *next.GroupID)
				}
				onC := regressionResolve(t, svc, onB, map[int64]struct{}{10: {}, 20: {}})
				if *onC.GroupID != 30 {
					t.Fatalf("second failover=%d want 30", *onC.GroupID)
				}
				got, err := svc.ResolveAutoGroupForModelExcluding(context.Background(), onC, "gpt-regression", map[int64]struct{}{10: {}, 20: {}, 30: {}})
				if got != nil || !errors.Is(err, ErrAutoGroupUnavailable) {
					t.Fatalf("exhausted got=%v err=%v", got, err)
				}
				state, _ = svc.autoGroupSwitchStates.Load(autoGroupSwitchStateKey(key))
				if got := len(state.(autoGroupSwitchState).switchedAt); got != 1 {
					t.Fatalf("budget slots=%d want original 1", got)
				}
			})
		}
	}
}

type regressionHookGroupRepo struct {
	failoverPersistGroupRepo
	hook func()
}

func (r regressionHookGroupRepo) ListActive(ctx context.Context) ([]Group, error) {
	if r.hook != nil {
		r.hook()
	}
	return r.failoverPersistGroupRepo.ListActive(ctx)
}

// Each hook is a deterministic interleaving of another request reporting the
// same hard failure after this resolver takes its revision snapshot.
func TestAutoGroupDuplicateFailureMustNotStarveResolver(t *testing.T) {
	svc, key := regressionAutoGroupSetup(autoGroupStrategyPrice, []int64{10, 20, 30})
	onA := regressionResolve(t, svc, key, nil)
	base := svc.groupRepo.(failoverPersistGroupRepo)
	calls := 0
	svc.groupRepo = regressionHookGroupRepo{failoverPersistGroupRepo: base, hook: func() {
		calls++
		svc.markAutoGroupSelectionForImmediateFailure(onA, "gpt-regression")
	}}
	got, err := svc.ResolveAutoGroupForModelExcluding(context.Background(), onA, "gpt-regression", map[int64]struct{}{10: {}})
	if err != nil || got == nil || *got.GroupID != 20 {
		t.Fatalf("duplicate hard failures exhausted resolver: calls=%d key=%v err=%v", calls, got, err)
	}
}

// In a latency-triggered reevaluation, every successful slow completion from
// the same group resets transient failures that the hook just observed. Both
// observations currently revise the selection even after reevaluation began.
func TestAutoGroupSameGroupObservationsMustNotStarveResolver(t *testing.T) {
	svc, key := regressionAutoGroupSetup(autoGroupStrategyPrice, []int64{10, 20, 30})
	onA := regressionResolve(t, svc, key, nil)
	selectionKey := autoGroupSelectionKey(key, "gpt-regression")
	state := svc.autoGroupSelection(selectionKey)
	state.needsEvaluation = true
	state.eventGroupID = 10
	svc.storeAutoGroupSelection(selectionKey, state)
	base := svc.groupRepo.(failoverPersistGroupRepo)
	calls := 0
	svc.groupRepo = regressionHookGroupRepo{failoverPersistGroupRepo: base, hook: func() {
		calls++
		svc.ObserveAutoGroupRequestResult(onA, "gpt-regression", 503, nil)
		svc.ObserveAutoGroupRequestResult(onA, "gpt-regression", 200, nil)
	}}
	got, err := svc.ResolveAutoGroupForModel(context.Background(), key, "gpt-regression")
	if err != nil || got == nil || *got.GroupID != 20 {
		t.Fatalf("same-group completion observations exhausted resolver: calls=%d key=%v err=%v", calls, got, err)
	}
}
