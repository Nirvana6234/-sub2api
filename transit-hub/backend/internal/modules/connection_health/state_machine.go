package connection_health

import "time"

// TransitionInput contains the snapshot and probe result needed for one state decision.
type TransitionInput struct {
	Current              State
	CurrentWeight        int
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	ObservingUntil       *time.Time
	Now                  time.Time
	Result               ResultKey
	Policy               Policy
}

// TransitionOutput preserves the API shape used by callers while exposing the health decision.
type TransitionOutput struct {
	NextState            State
	Weight               int
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	CooldownUntil        *time.Time
	ObservingUntil       *time.Time
	TriggerRemoteDegrade bool
	TriggerRemoteRestore bool
}

func isHardFailure(result ResultKey) bool {
	switch result {
	case ResultServerError, ResultAuth, ResultModelNotFound:
		return true
	default:
		return false
	}
}

func isSoftFailure(result ResultKey) bool {
	switch result {
	case ResultNetworkFluctuation, ResultRateLimited, ResultInvalidResponse:
		return true
	default:
		return false
	}
}

// Transition keeps Disabled manual-only.
//
// 恢复是分级的，不是一次成功就满血：
//
//	suspended --成功--> observing（熔断后的观察期，权重按 recovery_step_percent 起步）
//	observing --连续成功达 success_threshold 且观察窗结束--> healthy
//	observing --失败--> suspended（直接退回并重置冷却，不消耗 failure_threshold）
//	healthy   --失败--> degraded（权重递减）--失败达 failure_threshold--> suspended
//	degraded  --成功但未达阈值--> recovering（权重递增）--达阈值--> healthy
//
// 这样 success_threshold 与 observation_seconds 两个策略配置才真正参与判定：
// 在此之前它们在界面上可配、代码里却从未被读取，一次抖动性成功就会把连接拉回
// 满权重，配了「连续成功 2 次才恢复」也不生效。observing 与 recovering 两个状态
// 同样是此前永远不会被产生的空枚举，现在分别承载「熔断后验证」与「降级后回升」，
// 与 transitHubHealthTier 既有的风险排序（observing 最不可信、recovering 优于
// degraded）一致。
//
// 远端动作严格对称：进入 suspended 时触发降级，离开 suspended 时触发恢复。
// 可用性不等待观察期——探测既然已经成功，就先把上游放回可用，只有健康度
// （权重与状态）按阈值和观察窗渐进。
func Transition(in TransitionInput) TransitionOutput {
	if in.Current == StateDisabled {
		return TransitionOutput{
			NextState:            StateDisabled,
			Weight:               0,
			ConsecutiveFailures:  in.ConsecutiveFailures,
			ConsecutiveSuccesses: in.ConsecutiveSuccesses,
			ObservingUntil:       in.ObservingUntil,
		}
	}

	switch {
	case in.Result == ResultOK:
		return transitionOnSuccess(in)
	case isHardFailure(in.Result), isSoftFailure(in.Result):
		return transitionOnFailure(in)
	default:
		return TransitionOutput{
			NextState:            in.Current,
			Weight:               in.CurrentWeight,
			ConsecutiveFailures:  in.ConsecutiveFailures,
			ConsecutiveSuccesses: in.ConsecutiveSuccesses,
			ObservingUntil:       in.ObservingUntil,
		}
	}
}

func transitionOnSuccess(in TransitionInput) TransitionOutput {
	successes := in.ConsecutiveSuccesses + 1
	// 离开熔断的那一次成功就恢复远端可用性，与失败侧进入熔断时触发降级严格
	// 对称。健康度仍要走完观察期，但没有理由让一个已经探测成功的上游继续
	// 停用——权重低只影响调度优先级，不影响可不可用。
	leavingSuspension := in.Current == StateSuspended

	if in.Current == StateHealthy || successRestoresHealth(in, successes) {
		return TransitionOutput{
			NextState:            StateHealthy,
			Weight:               100,
			ConsecutiveFailures:  0,
			ConsecutiveSuccesses: successes,
			TriggerRemoteRestore: in.Current != StateHealthy,
		}
	}

	// 尚未达到恢复条件：进入验证态，权重按 recovery_step_percent 逐级回升。
	out := TransitionOutput{
		Weight:               recoveringWeight(in),
		ConsecutiveFailures:  0,
		ConsecutiveSuccesses: successes,
		TriggerRemoteRestore: leavingSuspension,
	}
	switch in.Current {
	case StateSuspended:
		// 熔断后的首次成功：开一个观察窗，窗内即使成功次数够也不算恢复。
		observingUntil := in.Now.Add(observationWindow(in.Policy))
		out.NextState = StateObserving
		out.ObservingUntil = &observingUntil
	case StateObserving:
		// 观察窗沿用首次成功时定下的截止时间，不因每次成功而顺延。
		out.NextState = StateObserving
		out.ObservingUntil = in.ObservingUntil
	default:
		// degraded 及历史遗留状态：按「降级后回升」处理，无观察窗。
		out.NextState = StateRecovering
	}
	return out
}

// successRestoresHealth 判断本次成功能否让连接回到完全健康。
// 需要同时满足 success_threshold（连续成功次数）与 observation_seconds
// （熔断后的观察窗）；两者都不满足时连接停在验证态继续攒成功次数。
func successRestoresHealth(in TransitionInput, successes int) bool {
	if successes < successThreshold(in.Policy) {
		return false
	}
	if in.Current == StateObserving && in.ObservingUntil != nil && in.Now.Before(*in.ObservingUntil) {
		return false
	}
	return true
}

// recoveringWeight 让权重按 recovery_step_percent 逐级回升，上限 100。
// 熔断态权重为 0，因此第一次成功后从一个步进值起步而不是直接满血。
func recoveringWeight(in TransitionInput) int {
	base := in.CurrentWeight
	if base < 0 {
		base = 0
	}
	return minInt(100, base+stepPercent(in.Policy))
}

func transitionOnFailure(in TransitionInput) TransitionOutput {
	if in.Current == StateSuspended {
		cooldownUntil := in.Now.Add(cooldownWindow(in.Policy))
		return TransitionOutput{
			NextState:            StateSuspended,
			Weight:               0,
			ConsecutiveFailures:  in.ConsecutiveFailures + 1,
			ConsecutiveSuccesses: 0,
			CooldownUntil:        &cooldownUntil,
		}
	}
	failures := in.ConsecutiveFailures + 1

	// 熔断后的观察期内再次失败：立即退回熔断并重置冷却，不再消耗
	// failure_threshold 的宽容度——那是留给稳定连接偶发抖动的，不该让一个
	// 刚被证明不可靠的上游反复在 suspended 与 degraded 之间横跳。
	// 离开熔断时已恢复过远端，因此这里要重新触发降级。
	if in.Current == StateObserving {
		cooldownUntil := in.Now.Add(cooldownWindow(in.Policy))
		return TransitionOutput{
			NextState:            StateSuspended,
			Weight:               0,
			ConsecutiveFailures:  failures,
			ConsecutiveSuccesses: 0,
			CooldownUntil:        &cooldownUntil,
			TriggerRemoteDegrade: true,
		}
	}

	out := TransitionOutput{
		ConsecutiveFailures:  failures,
		ConsecutiveSuccesses: 0,
	}

	if failures >= failureThreshold(in.Policy) {
		cooldownUntil := in.Now.Add(cooldownWindow(in.Policy))
		out.NextState = StateSuspended
		out.Weight = 0
		out.CooldownUntil = &cooldownUntil
		out.TriggerRemoteDegrade = in.Current != StateSuspended
		return out
	}

	// Keep the connection usable while failures are being confirmed. Legacy
	// intermediate states are normalized to degraded for compatibility.
	out.NextState = StateDegraded
	baseWeight := in.CurrentWeight
	if baseWeight <= 0 {
		baseWeight = 100
	}
	out.Weight = maxInt(1, baseWeight-stepPercent(in.Policy))
	return out
}

func stepPercent(p Policy) int {
	if p.RecoveryStepPercent <= 0 {
		return 25
	}
	return p.RecoveryStepPercent
}

func successThreshold(p Policy) int {
	if p.SuccessThreshold <= 0 {
		return 2
	}
	return p.SuccessThreshold
}

func failureThreshold(p Policy) int {
	if p.FailureThreshold <= 0 {
		return 3
	}
	return p.FailureThreshold
}

func observationWindow(p Policy) time.Duration {
	if p.ObservationSeconds <= 0 {
		return 300 * time.Second
	}
	return time.Duration(p.ObservationSeconds) * time.Second
}

func cooldownWindow(p Policy) time.Duration {
	if p.CooldownSeconds <= 0 {
		return 300 * time.Second
	}
	return time.Duration(p.CooldownSeconds) * time.Second
}

// ProbeBackoff returns the delay before the next probe for consecutive failures.
func ProbeBackoff(consecutiveFailures int) time.Duration {
	switch {
	case consecutiveFailures <= 0:
		return 0
	case consecutiveFailures == 1:
		return 2 * time.Minute
	case consecutiveFailures == 2:
		return 5 * time.Minute
	default:
		return 10 * time.Minute
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
