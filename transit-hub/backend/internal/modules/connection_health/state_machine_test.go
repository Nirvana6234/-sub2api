package connection_health

import (
	"testing"
	"time"
)

func testPolicy() Policy {
	return Policy{
		FailureThreshold:    3,
		CooldownSeconds:     300,
		RecoveryStepPercent: 25,
	}
}

// 成功次数已经达到 success_threshold（且不在观察窗内）时，任何非 disabled 状态
// 都回到完全健康。前提很重要：ConsecutiveSuccesses 已经攒够，测试才看得到满血
// 恢复——单次成功的行为由 TestTransition_SuspendedRecoversThroughObservation 覆盖。
func TestTransition_ResultOKRestoresHealthOnceThresholdMet(t *testing.T) {
	policy := testPolicy()
	now := time.Now()
	for _, state := range []State{StateHealthy, StateDegraded, StateObserving, StateRecovering, StateSuspended} {
		out := Transition(TransitionInput{
			Current:              state,
			CurrentWeight:        25,
			ConsecutiveFailures:  4,
			ConsecutiveSuccesses: 2,
			Now:                  now,
			Result:               ResultOK,
			Policy:               policy,
		})
		if out.NextState != StateHealthy || out.Weight != 100 {
			t.Fatalf("state %s: expected healthy/100, got %s/%d", state, out.NextState, out.Weight)
		}
		if out.ConsecutiveFailures != 0 {
			t.Fatalf("state %s: expected failures reset, got %d", state, out.ConsecutiveFailures)
		}
		wantRestore := state != StateHealthy
		if out.TriggerRemoteRestore != wantRestore {
			t.Fatalf("state %s: TriggerRemoteRestore=%v, want %v", state, out.TriggerRemoteRestore, wantRestore)
		}
		if out.TriggerRemoteDegrade {
			t.Fatalf("state %s: success must not trigger remote degrade", state)
		}
	}
}

func TestTransition_ResultOKDoesNotExitDisabled(t *testing.T) {
	out := Transition(TransitionInput{
		Current:             StateDisabled,
		CurrentWeight:       0,
		ConsecutiveFailures: 3,
		Now:                 time.Now(),
		Result:              ResultOK,
		Policy:              testPolicy(),
	})
	if out.NextState != StateDisabled || out.Weight != 0 {
		t.Fatalf("disabled state must remain disabled/0, got %s/%d", out.NextState, out.Weight)
	}
	if out.TriggerRemoteRestore || out.TriggerRemoteDegrade {
		t.Fatalf("disabled state must not trigger remote actions: %+v", out)
	}
}

func TestTransition_FirstFailureKeepsHealthyUpstreamAvailable(t *testing.T) {
	for _, result := range []ResultKey{ResultServerError, ResultAuth, ResultModelNotFound, ResultNetworkFluctuation, ResultRateLimited, ResultInvalidResponse} {
		out := Transition(TransitionInput{
			Current:       StateHealthy,
			CurrentWeight: 100,
			Now:           time.Now(),
			Result:        result,
			Policy:        testPolicy(),
		})
		if out.NextState != StateDegraded || out.Weight != 75 {
			t.Fatalf("result %s: expected degraded/75 after first failure, got %s/%d", result, out.NextState, out.Weight)
		}
		if out.TriggerRemoteDegrade {
			t.Fatalf("result %s: first failure must not trigger remote degrade", result)
		}
		if out.ConsecutiveFailures != 1 || out.ConsecutiveSuccesses != 0 {
			t.Fatalf("result %s: unexpected counters failures=%d successes=%d", result, out.ConsecutiveFailures, out.ConsecutiveSuccesses)
		}
	}
}

func TestTransition_FailureThresholdSuspendsAndDegradesRemotely(t *testing.T) {
	policy := testPolicy()
	now := time.Now()
	for _, state := range []State{StateHealthy, StateDegraded, StateObserving, StateRecovering} {
		out := Transition(TransitionInput{
			Current:             state,
			CurrentWeight:       50,
			ConsecutiveFailures: 2,
			Now:                 now,
			Result:              ResultRateLimited,
			Policy:              policy,
		})
		if out.NextState != StateSuspended || out.Weight != 0 {
			t.Fatalf("state %s: expected suspended/0, got %s/%d", state, out.NextState, out.Weight)
		}
		if !out.TriggerRemoteDegrade {
			t.Fatalf("state %s: expected remote degrade on suspension", state)
		}
		if out.CooldownUntil == nil || !out.CooldownUntil.After(now) {
			t.Fatalf("state %s: expected future cooldown", state)
		}
	}
}

func TestTransition_SuspendedFailureRemainsSuspendedWithoutRepeatedDegrade(t *testing.T) {
	out := Transition(TransitionInput{
		Current:             StateSuspended,
		CurrentWeight:       0,
		ConsecutiveFailures: 0,
		Now:                 time.Now(),
		Result:              ResultServerError,
		Policy:              testPolicy(),
	})
	if out.NextState != StateSuspended || out.Weight != 0 || out.TriggerRemoteDegrade {
		t.Fatalf("suspended failure should remain suspended without repeated action: %+v", out)
	}
}

func TestTransition_UnsupportedResultLeavesStateUnchanged(t *testing.T) {
	observingUntil := time.Now().Add(time.Minute)
	out := Transition(TransitionInput{
		Current:              StateHealthy,
		CurrentWeight:        64,
		ConsecutiveFailures:  2,
		ConsecutiveSuccesses: 3,
		ObservingUntil:       &observingUntil,
		Result:               ResultKey("unsupported"),
		Policy:               testPolicy(),
	})
	if out.NextState != StateHealthy || out.Weight != 64 || out.ConsecutiveFailures != 2 || out.ConsecutiveSuccesses != 3 {
		t.Fatalf("unsupported result should preserve state, got %+v", out)
	}
}

func TestProbeBackoff(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 0},
		{1, 2 * time.Minute},
		{2, 5 * time.Minute},
		{3, 10 * time.Minute},
		{9, 10 * time.Minute},
	}
	for _, c := range cases {
		if got := ProbeBackoff(c.failures); got != c.want {
			t.Fatalf("ProbeBackoff(%d) = %s, want %s", c.failures, got, c.want)
		}
	}
}

// gradedRecoveryPolicy 显式配置 success_threshold 与 observation_seconds，
// 用来钉死这两个策略项真正参与判定——它们此前在界面上可配、代码里从未被读取。
func gradedRecoveryPolicy() Policy {
	return Policy{
		FailureThreshold:    3,
		SuccessThreshold:    2,
		CooldownSeconds:     300,
		ObservationSeconds:  600,
		RecoveryStepPercent: 25,
	}
}

func TestTransition_SuspendedRecoversThroughObservation(t *testing.T) {
	policy := gradedRecoveryPolicy()
	now := time.Now()

	// 熔断后的首次成功不再满血，而是进入观察期并按步进给权重。
	first := Transition(TransitionInput{
		Current: StateSuspended, CurrentWeight: 0, ConsecutiveFailures: 3,
		Now: now, Result: ResultOK, Policy: policy,
	})
	if first.NextState != StateObserving {
		t.Fatalf("expected observing after leaving suspension, got %s", first.NextState)
	}
	if first.Weight != 25 {
		t.Fatalf("expected weight to step up by RecoveryStepPercent, got %d", first.Weight)
	}
	if first.ConsecutiveSuccesses != 1 || first.ConsecutiveFailures != 0 {
		t.Fatalf("unexpected counters: successes=%d failures=%d", first.ConsecutiveSuccesses, first.ConsecutiveFailures)
	}
	if !first.TriggerRemoteRestore {
		t.Fatalf("leaving suspension must restore upstream availability immediately")
	}
	if first.ObservingUntil == nil || !first.ObservingUntil.Equal(now.Add(600*time.Second)) {
		t.Fatalf("expected observation window from policy, got %v", first.ObservingUntil)
	}

	// 观察窗未结束：即使成功次数已达阈值也不算恢复。
	inWindow := Transition(TransitionInput{
		Current: StateObserving, CurrentWeight: 25, ConsecutiveSuccesses: 1,
		ObservingUntil: first.ObservingUntil, Now: now.Add(time.Minute),
		Result: ResultOK, Policy: policy,
	})
	if inWindow.NextState != StateObserving {
		t.Fatalf("observation window must hold the connection in observing, got %s", inWindow.NextState)
	}
	if inWindow.Weight != 50 {
		t.Fatalf("expected weight to keep stepping up, got %d", inWindow.Weight)
	}
	if inWindow.ObservingUntil == nil || !inWindow.ObservingUntil.Equal(*first.ObservingUntil) {
		t.Fatalf("observation deadline must not be extended by further successes")
	}
	if inWindow.TriggerRemoteRestore {
		t.Fatalf("remote restore already happened when leaving suspension; must not repeat")
	}

	// 观察窗结束且成功次数达阈值：回到完全健康。
	done := Transition(TransitionInput{
		Current: StateObserving, CurrentWeight: 50, ConsecutiveSuccesses: 1,
		ObservingUntil: first.ObservingUntil, Now: now.Add(601 * time.Second),
		Result: ResultOK, Policy: policy,
	})
	if done.NextState != StateHealthy || done.Weight != 100 {
		t.Fatalf("expected healthy/100 after observation, got %s/%d", done.NextState, done.Weight)
	}
}

func TestTransition_ObservationFailureReturnsToSuspensionImmediately(t *testing.T) {
	policy := gradedRecoveryPolicy()
	now := time.Now()
	observingUntil := now.Add(600 * time.Second)

	out := Transition(TransitionInput{
		Current: StateObserving, CurrentWeight: 50, ConsecutiveFailures: 0, ConsecutiveSuccesses: 2,
		ObservingUntil: &observingUntil, Now: now, Result: ResultServerError, Policy: policy,
	})
	// 观察期失败不该再消耗 failure_threshold 的宽容度：一个刚被证明不可靠的
	// 上游必须立刻退回熔断，否则会在 suspended 与 degraded 之间反复横跳。
	if out.NextState != StateSuspended {
		t.Fatalf("expected immediate suspension on observation failure, got %s", out.NextState)
	}
	if out.Weight != 0 {
		t.Fatalf("expected zero weight, got %d", out.Weight)
	}
	if out.CooldownUntil == nil || !out.CooldownUntil.Equal(now.Add(300*time.Second)) {
		t.Fatalf("expected cooldown to be reset, got %v", out.CooldownUntil)
	}
	if !out.TriggerRemoteDegrade {
		t.Fatalf("availability was restored when leaving suspension, so it must be degraded again")
	}
	if out.ConsecutiveSuccesses != 0 {
		t.Fatalf("expected success streak reset, got %d", out.ConsecutiveSuccesses)
	}
}

func TestTransition_DegradedSuccessEntersRecoveringWithoutObservationWindow(t *testing.T) {
	policy := gradedRecoveryPolicy()
	now := time.Now()

	out := Transition(TransitionInput{
		Current: StateDegraded, CurrentWeight: 75, ConsecutiveFailures: 1,
		Now: now, Result: ResultOK, Policy: policy,
	})
	// 降级只是权重下调、连接始终可用，因此回升不需要熔断那套观察窗。
	if out.NextState != StateRecovering {
		t.Fatalf("expected recovering after degraded success, got %s", out.NextState)
	}
	if out.Weight != 100 {
		t.Fatalf("expected weight to step up to the cap, got %d", out.Weight)
	}
	if out.ObservingUntil != nil {
		t.Fatalf("degraded recovery must not open an observation window, got %v", out.ObservingUntil)
	}
	if out.TriggerRemoteRestore {
		t.Fatalf("degraded never triggered a remote degrade, so it must not trigger a restore")
	}

	// 再成功一次，累计达到 success_threshold，回到完全健康。
	final := Transition(TransitionInput{
		Current: StateRecovering, CurrentWeight: out.Weight, ConsecutiveSuccesses: out.ConsecutiveSuccesses,
		Now: now, Result: ResultOK, Policy: policy,
	})
	if final.NextState != StateHealthy || final.Weight != 100 {
		t.Fatalf("expected healthy/100, got %s/%d", final.NextState, final.Weight)
	}
}

func TestTransition_SuccessThresholdOfOneRestoresImmediately(t *testing.T) {
	// 阈值为 1 时保持旧的「一次成功即恢复」行为，运营可以显式选择它。
	policy := gradedRecoveryPolicy()
	policy.SuccessThreshold = 1
	policy.ObservationSeconds = 0

	out := Transition(TransitionInput{
		Current: StateSuspended, CurrentWeight: 0, ConsecutiveFailures: 3,
		Now: time.Now(), Result: ResultOK, Policy: policy,
	})
	if out.NextState != StateHealthy || out.Weight != 100 {
		t.Fatalf("expected immediate healthy/100, got %s/%d", out.NextState, out.Weight)
	}
	if !out.TriggerRemoteRestore {
		t.Fatalf("expected remote restore")
	}
}
