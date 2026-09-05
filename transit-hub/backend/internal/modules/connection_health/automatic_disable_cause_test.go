package connection_health

import (
	"testing"
	"time"
)

func TestAutomaticPriorityDisableCausePrefersBalanceFailureOverRuntimeFlag(t *testing.T) {
	cause := automaticPriorityDisableCause(managedPriorityCandidate{
		runtimeBlocked: true,
		states: []ConnectionHealthState{{
			ModelName: "gpt-5.6-terra", State: StateSuspended, CurrentWeight: 0,
			LastErrorKey:    string(ResultAuth),
			LastErrorDetail: `{"code":"INSUFFICIENT_BALANCE","message":"账户余额不足，请充值后继续使用。"}`,
			UpdatedAt:       time.Now(),
		}},
	})
	if cause.key != "balance_exhausted" || cause.modelName != "gpt-5.6-terra" {
		t.Fatalf("unexpected cause: %+v", cause)
	}
	if cause.detail == "" || cause.reason != "balance exhausted" {
		t.Fatalf("balance response must be retained for the alert: %+v", cause)
	}
}

func TestAutomaticPriorityDisableCauseClassifiesInvalidCredential(t *testing.T) {
	cause := automaticPriorityDisableCause(managedPriorityCandidate{states: []ConnectionHealthState{{
		State: StateSuspended, CurrentWeight: 0, LastErrorKey: string(ResultAuth),
		LastErrorDetail: `{"code":"INVALID_API_KEY"}`,
	}}})
	if cause.key != "invalid_credential" || cause.reason != "invalid credential" {
		t.Fatalf("unexpected credential cause: %+v", cause)
	}
}

func TestAutomaticPriorityDisableCauseFallsBackToRuntimeFlag(t *testing.T) {
	cause := automaticPriorityDisableCause(managedPriorityCandidate{runtimeBlocked: true})
	if cause.key != "upstream_runtime_limited" || cause.detail != "" {
		t.Fatalf("unexpected runtime fallback: %+v", cause)
	}
}

func TestAutomaticDisableCauseNotificationPolicy(t *testing.T) {
	for _, test := range []struct {
		causeKey string
		want      bool
	}{
		{causeKey: "balance_exhausted", want: true},
		{causeKey: "invalid_credential", want: true},
		{causeKey: "authentication_failed", want: true},
		{causeKey: "model_unavailable", want: true},
		{causeKey: "network_failure", want: false},
		{causeKey: "rate_limited", want: false},
		{causeKey: "upstream_server_error", want: false},
		{causeKey: "upstream_runtime_limited", want: false},
	} {
		if got := automaticDisableCauseKeyNotifiable(test.causeKey); got != test.want {
			t.Errorf("cause %q notifiable=%v, want %v", test.causeKey, got, test.want)
		}
	}
}
