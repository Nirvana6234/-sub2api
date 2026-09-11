package connection_health

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"transithub/backend/internal/modules/upstream"
)

type fakeSub2APIRecoveryActioner struct {
	calls    []string
	observed []*time.Time
	err      error
}

func (f *fakeSub2APIRecoveryActioner) RecoverSub2APIAdminAccountSchedulability(
	_ upstream.Session,
	accountID string,
	expectedChangedAt *time.Time,
) error {
	f.calls = append(f.calls, accountID)
	f.observed = append(f.observed, expectedChangedAt)
	return f.err
}

func TestIsDue_NeverProbedIsDue(t *testing.T) {
	repo := newFakeRepository()
	svc := &Service{repo: repo}
	if !svc.isDue(context.Background(), "conn-1", "m1", Policy{ProbeIntervalSeconds: 60}, time.Now()) {
		t.Fatalf("expected never-probed target to be due")
	}
}

func TestIsDue_DisabledNeverDue(t *testing.T) {
	repo := newFakeRepository()
	repo.states["conn-1"] = map[string]ConnectionHealthState{
		"m1": {ConnectionID: "conn-1", ModelName: "m1", State: StateDisabled},
	}
	svc := &Service{repo: repo}
	if svc.isDue(context.Background(), "conn-1", "m1", Policy{ProbeIntervalSeconds: 60}, time.Now()) {
		t.Fatalf("disabled state must never be due for automatic probing")
	}
}

func TestRecordTargetCredentialUnavailable_PreservesLegacyRemoteAction(t *testing.T) {
	repo := newFakeRepository()
	svc := &Service{repo: repo}
	targetID := "sub2api:ws1:acc-1"
	repo.states[targetID] = map[string]ConnectionHealthState{
		"gpt-4o": {
			ConnectionID: targetID, ModelName: "gpt-4o", UserID: "user1", AdminAccountID: "ws1",
			State: StateSuspended, LastRemoteAction: RemoteActionSub2APIStatusInactive,
		},
	}
	target := AdminProbeTarget{TargetID: targetID, Platform: string(upstream.PlatformSub2API), AccountID: "acc-1"}
	spec := probeModelSpec{modelName: "gpt-4o", policy: Policy{ID: "p1"}}

	svc.recordTargetCredentialUnavailable(context.Background(), "user1", "ws1", target, []probeModelSpec{spec}, upstream.ReasonCredentialUnavailable)
	stored := repo.states[targetID]["gpt-4o"]
	if stored.LastRemoteAction != RemoteActionSub2APIStatusInactive {
		t.Fatalf("credential failure must preserve legacy ownership evidence, got %+v", stored)
	}
}

func TestIsDue_WithinCooldownIsNotDue(t *testing.T) {
	repo := newFakeRepository()
	future := time.Now().Add(1 * time.Minute)
	repo.states["conn-1"] = map[string]ConnectionHealthState{
		"m1": {ConnectionID: "conn-1", ModelName: "m1", State: StateSuspended, CooldownUntil: &future},
	}
	svc := &Service{repo: repo}
	if svc.isDue(context.Background(), "conn-1", "m1", Policy{ProbeIntervalSeconds: 60}, time.Now()) {
		t.Fatalf("expected target within cooldown to not be due")
	}
}

func TestIsDue_RespectsIntervalAndBackoff(t *testing.T) {
	repo := newFakeRepository()
	now := time.Now()
	recentProbe := now.Add(-10 * time.Second)
	repo.states["conn-1"] = map[string]ConnectionHealthState{
		"m1": {ConnectionID: "conn-1", ModelName: "m1", State: StateHealthy, LastProbeAt: &recentProbe},
	}
	svc := &Service{repo: repo}

	if svc.isDue(context.Background(), "conn-1", "m1", Policy{ProbeIntervalSeconds: 60}, now) {
		t.Fatalf("expected not due within interval")
	}

	repo.states["conn-1"] = map[string]ConnectionHealthState{
		"m1": {ConnectionID: "conn-1", ModelName: "m1", State: StateDegraded, LastProbeAt: &recentProbe, ConsecutiveFailures: 2},
	}
	if svc.isDue(context.Background(), "conn-1", "m1", Policy{ProbeIntervalSeconds: 60}, now) {
		t.Fatalf("expected backoff window to still be active 10s after failure")
	}

	longAgo := now.Add(-6 * time.Minute)
	repo.states["conn-1"] = map[string]ConnectionHealthState{
		"m1": {ConnectionID: "conn-1", ModelName: "m1", State: StateDegraded, LastProbeAt: &longAgo, ConsecutiveFailures: 2},
	}
	if !svc.isDue(context.Background(), "conn-1", "m1", Policy{ProbeIntervalSeconds: 60}, now) {
		t.Fatalf("expected due after backoff window elapses")
	}
}

// schedulerReader 构造一个平台读取器：单分组，若干可探活 channel（带 base_url + models）。
func schedulerReader(accountIDs ...string) fakePlatformGroupReader {
	accounts := make([]upstream.AdminGroupAccountInfo, 0, len(accountIDs))
	for _, id := range accountIDs {
		accounts = append(accounts, upstream.AdminGroupAccountInfo{ID: id, Name: "ch-" + id, BaseURL: "https://up", Models: "gpt-4o"})
	}
	return fakePlatformGroupReader{
		groups:        []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{"g1": accounts},
	}
}

func sub2APISchedulerReader(accounts ...upstream.AdminGroupAccountInfo) fakePlatformGroupReader {
	return fakePlatformGroupReader{
		groups:        []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{"g1": accounts},
	}
}

func TestCollectAdminProbeJobs_UnschedulableTargetDoesNotSendModelProbe(t *testing.T) {
	repo := newFakeRepository()
	policy := Policy{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		ProbeIntervalSeconds: 60, DailyProbeBudget: 1,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}
	dayKey := "user1|ws1|p1|" + probeBudgetDayStart(time.Now()).Format(time.RFC3339)
	repo.budgetClaims[dayKey] = 1
	unschedulable := false
	schedulable := true
	reader := sub2APISchedulerReader(
		upstream.AdminGroupAccountInfo{ID: "healthy", Name: "healthy", Status: "active", Schedulable: &schedulable, Models: "gpt-4o"},
		upstream.AdminGroupAccountInfo{ID: "recover", Name: "recover", Status: "active", Schedulable: &unschedulable, Models: "gpt-4o"},
	)
	svc := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader,
	}
	assignments := []PolicyAssignment{
		{UserID: "user1", AdminAccountID: "ws1", TargetID: "sub2api:ws1:healthy", PolicyID: policy.ID},
		{UserID: "user1", AdminAccountID: "ws1", TargetID: "sub2api:ws1:recover", PolicyID: policy.ID},
	}

	jobs := svc.collectAdminProbeJobs(context.Background(), []Policy{policy}, assignments)
	if len(jobs) != 0 {
		t.Fatalf("unschedulable account must use admin-inventory recovery instead of model probes, got %+v", jobs)
	}
}

func TestCollectAdminProbeJobs_RuntimeBlockedTargetDoesNotSendModelProbe(t *testing.T) {
	repo := newFakeRepository()
	policy := Policy{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ProbeIntervalSeconds: 60, DailyProbeBudget: 100,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}
	schedulable := true
	blockedUntil := time.Now().Add(time.Hour)
	reader := sub2APISchedulerReader(upstream.AdminGroupAccountInfo{
		ID: "blocked", Status: "active", Schedulable: &schedulable, Models: "gpt-4o",
		TempUnschedulableUntil: &blockedUntil, TempUnschedulableReason: "upstream_transport",
	})
	svc := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader,
	}
	assignments := []PolicyAssignment{{UserID: "user1", AdminAccountID: "ws1", TargetID: "sub2api:ws1:blocked", PolicyID: policy.ID}}

	if jobs := svc.collectAdminProbeJobs(context.Background(), []Policy{policy}, assignments); len(jobs) != 0 {
		t.Fatalf("runtime-blocked account must rely on Sub2API state rather than a model probe: %+v", jobs)
	}
}

func TestCollectAdminProbeJobs_Sub2APIErrorSchedulesRecoveryOnly(t *testing.T) {
	repo := newFakeRepository()
	policy := Policy{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ProbeIntervalSeconds: 60, DailyProbeBudget: 100,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}
	schedulable := true
	reader := sub2APISchedulerReader(upstream.AdminGroupAccountInfo{
		ID: "17", Status: "error", Schedulable: &schedulable, Models: "gpt-4o",
		SchedulabilitySource: "automatic",
	})
	svc := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader,
	}
	assignments := []PolicyAssignment{{UserID: "user1", AdminAccountID: "ws1", TargetID: "sub2api:ws1:17", PolicyID: policy.ID}}

	jobs := svc.collectAdminProbeJobs(context.Background(), []Policy{policy}, assignments)
	if len(jobs) != 1 || !jobs[0].recoveryOnly || len(jobs[0].dueSpecs) != 1 {
		t.Fatalf("expected one real-generation recovery job, got %+v", jobs)
	}
}

func TestCollectAdminProbeJobs_MultiplierOnlySchedulesAutomaticRecovery(t *testing.T) {
	repo := newFakeRepository()
	policy := Policy{
		ID: "price-only", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		StrategyMode: StrategyModeMultiplierOnly, ProbeIntervalSeconds: 90, DailyProbeBudget: 1,
	}
	dayKey := "user1|ws1|price-only|" + probeBudgetDayStart(time.Now()).Format(time.RFC3339)
	repo.budgetClaims[dayKey] = 1
	disabled := false
	reader := sub2APISchedulerReader(upstream.AdminGroupAccountInfo{
		ID: "17", Status: "error", Schedulable: &disabled, Models: "gpt-5.6-terra,gpt-5.6-sol",
		SchedulabilitySource: SchedulabilitySourceAutomatic,
	})
	svc := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader,
	}
	assignments := []PolicyAssignment{{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "sub2api:ws1:17", PolicyID: policy.ID,
	}}

	jobs := svc.collectAdminProbeJobs(context.Background(), []Policy{policy}, assignments)
	if len(jobs) != 1 || !jobs[0].recoveryOnly || len(jobs[0].dueSpecs) != 1 {
		t.Fatalf("automatic disabled account must get one recovery-only job, got %+v", jobs)
	}
	if got := jobs[0].dueSpecs[0].modelName; got != "gpt-5.6-terra" {
		t.Fatalf("recovery must use the account's first advertised model, got %q", got)
	}
}

func TestCollectAdminProbeJobs_AutomaticRecoveryRespectsProbeInterval(t *testing.T) {
	repo := newFakeRepository()
	targetID := "sub2api:ws1:17"
	recentProbe := time.Now().Add(-10 * time.Second)
	repo.states[targetID] = map[string]ConnectionHealthState{
		"gpt-5.6-terra": {
			ConnectionID: targetID, ModelName: "gpt-5.6-terra", State: StateSuspended,
			LastProbeAt: &recentProbe,
		},
	}
	policy := Policy{
		ID: "price-only", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		StrategyMode: StrategyModeMultiplierOnly, ProbeIntervalSeconds: 90,
	}
	disabled := false
	reader := sub2APISchedulerReader(upstream.AdminGroupAccountInfo{
		ID: "17", Status: "error", Schedulable: &disabled, Models: "gpt-5.6-terra",
		SchedulabilitySource: SchedulabilitySourceAutomatic,
	})
	svc := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader,
	}
	assignments := []PolicyAssignment{{
		UserID: "user1", AdminAccountID: "ws1", TargetID: targetID, PolicyID: policy.ID,
	}}

	if jobs := svc.collectAdminProbeJobs(context.Background(), []Policy{policy}, assignments); len(jobs) != 0 {
		t.Fatalf("recent recovery probe must wait for its interval, got %+v", jobs)
	}
}

func TestCollectAdminProbeJobs_MultiplierOnlyDoesNotProbeOpenAccount(t *testing.T) {
	repo := newFakeRepository()
	policy := Policy{
		ID: "price-only", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		StrategyMode: StrategyModeMultiplierOnly, ProbeIntervalSeconds: 90,
	}
	enabled := true
	reader := sub2APISchedulerReader(upstream.AdminGroupAccountInfo{
		ID: "17", Status: "active", Schedulable: &enabled, Models: "gpt-5.6-terra",
		SchedulabilitySource: SchedulabilitySourceNone,
	})
	svc := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader,
	}
	assignments := []PolicyAssignment{{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "sub2api:ws1:17", PolicyID: policy.ID,
	}}

	if jobs := svc.collectAdminProbeJobs(context.Background(), []Policy{policy}, assignments); len(jobs) != 0 {
		t.Fatalf("open account under multiplier-only policy must not be probed, got %+v", jobs)
	}
}

func TestCollectAdminProbeJobs_AutomaticRecoveryIgnoresStaleDisabledHealthState(t *testing.T) {
	repo := newFakeRepository()
	targetID := "sub2api:ws1:17"
	repo.states[targetID] = map[string]ConnectionHealthState{
		"gpt-5.6-terra": {
			ConnectionID: targetID, ModelName: "gpt-5.6-terra", State: StateDisabled,
		},
	}
	policy := Policy{
		ID: "price-only", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		StrategyMode: StrategyModeMultiplierOnly, ProbeIntervalSeconds: 90,
	}
	disabled := false
	reader := sub2APISchedulerReader(upstream.AdminGroupAccountInfo{
		ID: "17", Status: "error", Schedulable: &disabled, Models: "gpt-5.6-terra",
		SchedulabilitySource: SchedulabilitySourceAutomatic,
	})
	svc := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader,
	}
	assignments := []PolicyAssignment{{
		UserID: "user1", AdminAccountID: "ws1", TargetID: targetID, PolicyID: policy.ID,
	}}

	jobs := svc.collectAdminProbeJobs(context.Background(), []Policy{policy}, assignments)
	if len(jobs) != 1 || !jobs[0].recoveryOnly {
		t.Fatalf("authoritative automatic state must override stale disabled probe state, got %+v", jobs)
	}
}

func TestRunSub2APIRecoveryProbe_UsesRealGenerationThenClearsRuntimeError(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("recovery must use a real generation request, got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	repo := newFakeRepository()
	recovery := &fakeSub2APIRecoveryActioner{}
	var recoveryEvents []AutomaticRecoveryEvent
	svc := &Service{repo: repo, probeRunner: NewRealProbeRunner(), recoveryActions: recovery, dispatcher: noopRemoteActionRunner{},
		autoRecoveryNotifier: AutomaticRecoveryNotifyFunc(func(_ context.Context, event AutomaticRecoveryEvent) {
			recoveryEvents = append(recoveryEvents, event)
		})}
	schedulable := true
	repo.states["sub2api:ws1:17"] = map[string]ConnectionHealthState{
		"gpt-4o": {
			ConnectionID: "sub2api:ws1:17", ModelName: "gpt-4o", State: StateSuspended,
			CurrentWeight: 0, ConsecutiveFailures: 4,
		},
	}
	repo.priorityStates["user1|ws1|group-1|sub2api:ws1:17"] = PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "group-1|sub2api:ws1:17",
		OriginalPriority: 100, LastAppliedPriority: 10000, NotificationCauseKey: "balance_exhausted",
	}
	target := AdminProbeTarget{
		TargetID: "sub2api:ws1:17", Platform: string(upstream.PlatformSub2API), AccountID: "17",
		AccountStatus: "error", AccountSchedulable: &schedulable,
		SchedulabilitySource: "automatic",
	}
	policy := Policy{ID: "p1", Enabled: true, ProbeMode: ProbeModeRealModel, AutoDegradeEnabled: false}
	job := adminProbeJob{
		userID: "user1", adminAccountID: "ws1", session: upstream.Session{Platform: upstream.PlatformSub2API},
		target: target, models: []probeModelSpec{{modelName: "gpt-4o", policy: policy}},
		dueSpecs: []probeModelSpec{{modelName: "gpt-4o", policy: policy}}, recoveryOnly: true,
	}

	svc.runSub2APIRecoveryProbe(context.Background(), job, upstream.ProbeCredential{BaseURL: server.URL, Key: "secret"})
	if len(methods) != 1 || methods[0] != "POST /v1/chat/completions" {
		t.Fatalf("unexpected probe requests: %+v", methods)
	}
	if len(recovery.calls) != 1 || recovery.calls[0] != "17" {
		t.Fatalf("runtime error was not cleared after successful probe: %+v", recovery.calls)
	}
	stored := repo.states[target.TargetID]["gpt-4o"]
	if stored.LastRemoteAction != "sub2api_schedulability_recovered" {
		t.Fatalf("expected recovery audit marker, got %+v", stored)
	}
	if stored.State != StateHealthy || stored.CurrentWeight != 100 || stored.ConsecutiveFailures != 0 {
		t.Fatalf("successful upstream recovery must also heal local state, got %+v", stored)
	}
	if len(recoveryEvents) != 1 || recoveryEvents[0].AccountID != "17" || recoveryEvents[0].ModelName != "gpt-4o" {
		t.Fatalf("successful recovery must emit one notification event: %+v", recoveryEvents)
	}
}

func TestRunSub2APIRecoveryProbe_DoesNotClearAfterFailedGeneration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("recovery must use a real generation request, got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer server.Close()

	repo := newFakeRepository()
	recovery := &fakeSub2APIRecoveryActioner{}
	notified := false
	svc := &Service{repo: repo, probeRunner: NewRealProbeRunner(), recoveryActions: recovery, dispatcher: noopRemoteActionRunner{},
		autoRecoveryNotifier: AutomaticRecoveryNotifyFunc(func(_ context.Context, _ AutomaticRecoveryEvent) { notified = true })}
	schedulable := true
	policy := Policy{ID: "p1", Enabled: true, ProbeMode: ProbeModeModelsEndpoint}
	job := adminProbeJob{
		userID: "user1", adminAccountID: "ws1", session: upstream.Session{Platform: upstream.PlatformSub2API},
		target:   AdminProbeTarget{TargetID: "sub2api:ws1:17", Platform: string(upstream.PlatformSub2API), AccountID: "17", AccountStatus: "error", AccountSchedulable: &schedulable},
		models:   []probeModelSpec{{modelName: "gpt-4o", policy: policy}},
		dueSpecs: []probeModelSpec{{modelName: "gpt-4o", policy: policy}}, recoveryOnly: true,
	}

	svc.runSub2APIRecoveryProbe(context.Background(), job, upstream.ProbeCredential{BaseURL: server.URL, Key: "secret"})
	if len(recovery.calls) != 0 {
		t.Fatalf("failed generation must not clear runtime error: %+v", recovery.calls)
	}
	if notified {
		t.Fatal("failed generation must not emit recovery notification")
	}
}

func TestSub2APIErrorRecoveryEligible_RespectsAdministratorSwitch(t *testing.T) {
	now := time.Now()
	disabled := false
	enabled := true
	base := AdminProbeTarget{Platform: string(upstream.PlatformSub2API), AccountStatus: "error"}
	base.AccountSchedulable = &disabled
	base.SchedulabilitySource = "manual"
	if sub2APIErrorRecoveryEligible(base, now) {
		t.Fatal("administrator-disabled account must never be auto-recovered")
	}
	base.AccountSchedulable = &enabled
	base.SchedulabilitySource = "automatic"
	if !sub2APIErrorRecoveryEligible(base, now) {
		t.Fatal("system error on a schedulable account should be recoverable")
	}
	future := now.Add(time.Minute)
	base.TempUnschedulableUntil = &future
	if sub2APIErrorRecoveryEligible(base, now) {
		t.Fatal("active temporary block window must expire before recovery probing")
	}
}

func TestCollectAdminProbeJobs_HealthyTargetRespectsProbeInterval(t *testing.T) {
	repo := newFakeRepository()
	lastProbe := time.Now().Add(-10 * time.Second)
	repo.states["sub2api:ws1:healthy"] = map[string]ConnectionHealthState{
		"gpt-4o": {
			ConnectionID: "sub2api:ws1:healthy", ModelName: "gpt-4o", State: StateHealthy,
			CurrentWeight: 100, LastProbeAt: &lastProbe,
		},
	}
	policy := Policy{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		ProbeIntervalSeconds: 60, DailyProbeBudget: 100,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}
	schedulable := true
	svc := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: sub2APISchedulerReader(upstream.AdminGroupAccountInfo{ID: "healthy", Name: "healthy", Status: "active", Schedulable: &schedulable, Models: "gpt-4o"}),
	}
	assignments := []PolicyAssignment{{UserID: "user1", AdminAccountID: "ws1", TargetID: "sub2api:ws1:healthy", PolicyID: policy.ID}}

	jobs := svc.collectAdminProbeJobs(context.Background(), []Policy{policy}, assignments)
	if len(jobs) != 0 {
		t.Fatalf("healthy target must wait for its configured interval, got %+v", jobs)
	}
}

// TestCollectAdminProbeJobs_GeneratesDueTargets 验证独立探活调度：为可探活、到期（从未探活）的
// 目标模型生成任务，禁用的模型目标不生成任务。
func TestCollectAdminProbeJobs_GeneratesDueTargets(t *testing.T) {
	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	svc := &Service{repo: repo, mySites: mySites, platformGroups: schedulerReader("100")}

	policies := []Policy{{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ProbeIntervalSeconds: 60,
		ModelTargets: []ModelTarget{
			{ModelName: "gpt-4o", Enabled: true},
			{ModelName: "disabled-model", Enabled: false},
		},
	}}
	assignments := []PolicyAssignment{
		{UserID: "user1", AdminAccountID: "ws1", TargetID: "newapi:ws1:100", PolicyID: "p1"},
	}
	jobs := svc.collectAdminProbeJobs(context.Background(), policies, assignments)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 target job, got %d", len(jobs))
	}
	if jobs[0].target.TargetID != "newapi:ws1:100" {
		t.Fatalf("unexpected targetId: %q", jobs[0].target.TargetID)
	}
	if len(jobs[0].dueSpecs) != 1 || jobs[0].dueSpecs[0].modelName != "gpt-4o" {
		t.Fatalf("expected only enabled gpt-4o due, got %+v", jobs[0].dueSpecs)
	}
}

// TestCollectAdminProbeJobs_UnassignedTargetNeverScheduled 验证核心新语义：即使 workspace 有启用
// 策略且模型能匹配，没有显式分配关系的 target 也绝不会自动探活。
func TestCollectAdminProbeJobs_UnassignedTargetNeverScheduled(t *testing.T) {
	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	svc := &Service{repo: repo, mySites: mySites, platformGroups: schedulerReader("100")}

	policies := []Policy{{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ProbeIntervalSeconds: 60,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}}
	jobs := svc.collectAdminProbeJobs(context.Background(), policies, nil)
	if len(jobs) != 0 {
		t.Fatalf("expected no jobs without any policy assignment, got %d", len(jobs))
	}
}

// TestCollectAdminProbeJobs_MultiplierOnlyNeverSchedulesRegularProbe guards the safety contract:
// even legacy or malformed rows cannot make an open/non-Sub2API target enter regular probing.
func TestCollectAdminProbeJobs_MultiplierOnlyNeverSchedulesRegularProbe(t *testing.T) {
	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	svc := &Service{repo: repo, mySites: mySites, platformGroups: schedulerReader("100")}
	policies := []Policy{{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		StrategyMode: StrategyModeMultiplierOnly, PriorityMode: PriorityModeMultiplier,
		ModelTargets: []ModelTarget{{ModelName: "legacy-model", Enabled: true}},
	}}
	assignments := []PolicyAssignment{{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "newapi:ws1:100", PolicyID: "p1",
	}}

	jobs := svc.collectAdminProbeJobs(context.Background(), policies, assignments)
	if len(jobs) != 0 {
		t.Fatalf("multiplier-only policy must never generate probe jobs: %+v", jobs)
	}
}

// TestCollectAdminProbeJobs_AssignmentToDisabledPolicyIgnored 验证分配指向的策略如果已被禁用，
// 该分配不生效（因为 policies 只包含 ListEnabledPolicies 的结果，policyByID 查不到）。
func TestCollectAdminProbeJobs_AssignmentToDisabledPolicyIgnored(t *testing.T) {
	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	svc := &Service{repo: repo, mySites: mySites, platformGroups: schedulerReader("100")}

	// 模拟调度器视角：runSchedulerTick 只会把 ListEnabledPolicies 的结果传进来，
	// 一条被禁用的策略永远不会出现在 policies 参数里，即使它有分配记录。
	policies := []Policy{}
	assignments := []PolicyAssignment{
		{UserID: "user1", AdminAccountID: "ws1", TargetID: "newapi:ws1:100", PolicyID: "disabled-policy"},
	}
	jobs := svc.collectAdminProbeJobs(context.Background(), policies, assignments)
	if len(jobs) != 0 {
		t.Fatalf("expected assignment to a disabled/nonexistent policy to be ignored, got %d jobs", len(jobs))
	}
}

// TestCollectAdminProbeJobs_OnlyUsesAssignedPolicies 验证 workspace 下其它启用策略，如果没有
// 分配给某个 target，就不会影响该 target 的候选模型计算——即使那条策略的模型池能匹配上。
func TestCollectAdminProbeJobs_OnlyUsesAssignedPolicies(t *testing.T) {
	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	svc := &Service{repo: repo, mySites: mySites, platformGroups: schedulerReader("100")}

	policies := []Policy{
		{ID: "assigned", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ProbeIntervalSeconds: 60,
			ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}}},
		{ID: "not-assigned", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ProbeIntervalSeconds: 60,
			ModelTargets: []ModelTarget{{ModelName: "gpt-4o-mini", Enabled: true}}},
	}
	assignments := []PolicyAssignment{
		{UserID: "user1", AdminAccountID: "ws1", TargetID: "newapi:ws1:100", PolicyID: "assigned"},
	}
	jobs := svc.collectAdminProbeJobs(context.Background(), policies, assignments)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 target job, got %d", len(jobs))
	}
	for _, spec := range jobs[0].dueSpecs {
		if spec.modelName == "gpt-4o-mini" {
			t.Fatalf("model from unassigned policy must not be scheduled: %+v", jobs[0].dueSpecs)
		}
	}
	if len(jobs[0].dueSpecs) != 1 || jobs[0].dueSpecs[0].modelName != "gpt-4o" {
		t.Fatalf("expected only gpt-4o (from assigned policy) due, got %+v", jobs[0].dueSpecs)
	}
}

// TestCollectAdminProbeJobs_SkipsUnavailableTargets 验证不可探活目标（new-api 缺 base_url）不排期。
func TestCollectAdminProbeJobs_SkipsUnavailableTargets(t *testing.T) {
	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	reader := fakePlatformGroupReader{
		groups:        []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{"g1": {{ID: "100", Name: "ch", Models: "gpt-4o"}}}, // 无 base_url
	}
	svc := &Service{repo: repo, mySites: mySites, platformGroups: reader}

	policies := []Policy{{ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ProbeIntervalSeconds: 60, ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}}}}
	assignments := []PolicyAssignment{{UserID: "user1", AdminAccountID: "ws1", TargetID: "newapi:ws1:100", PolicyID: "p1"}}
	jobs := svc.collectAdminProbeJobs(context.Background(), policies, assignments)
	if len(jobs) != 0 {
		t.Fatalf("expected unavailable target to be skipped, got %d jobs", len(jobs))
	}
}

// TestCollectAdminProbeJobs_CapsAtMaxJobsPerTick 验证单轮到期模型任务总数受 maxJobsPerTick 限制。
func TestCollectAdminProbeJobs_CapsAtMaxJobsPerTick(t *testing.T) {
	repo := newFakeRepository()
	ids := make([]string, 0, maxJobsPerTick+50)
	for i := range maxJobsPerTick + 50 {
		ids = append(ids, fmt.Sprintf("%d", i))
	}
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	svc := &Service{repo: repo, mySites: mySites, platformGroups: schedulerReader(ids...)}

	policies := []Policy{{ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ProbeIntervalSeconds: 60, ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}}}}
	assignments := make([]PolicyAssignment, 0, len(ids))
	for _, id := range ids {
		assignments = append(assignments, PolicyAssignment{UserID: "user1", AdminAccountID: "ws1", TargetID: buildTargetID("newapi", "ws1", id), PolicyID: "p1"})
	}
	jobs := svc.collectAdminProbeJobs(context.Background(), policies, assignments)
	total := 0
	for _, j := range jobs {
		total += len(j.dueSpecs)
	}
	if total != maxJobsPerTick {
		t.Fatalf("expected due model tasks capped at %d, got %d", maxJobsPerTick, total)
	}
}

// TestCollectAdminProbeJobs_MultiWorkspaceIsolation 验证多 workspace 隔离：每个 workspace 的策略
// 只为自己 workspace 生成目标（targetId 内嵌各自 adminAccountID）。
func TestCollectAdminProbeJobs_MultiWorkspaceIsolation(t *testing.T) {
	repo := newFakeRepository()
	mySites := fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}
	svc := &Service{repo: repo, mySites: mySites, platformGroups: schedulerReader("100")}

	policies := []Policy{
		{ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ProbeIntervalSeconds: 60, ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}}},
		{ID: "p2", UserID: "user1", AdminAccountID: "ws2", Enabled: true, ProbeIntervalSeconds: 60, ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}}},
	}
	assignments := []PolicyAssignment{
		{UserID: "user1", AdminAccountID: "ws1", TargetID: buildTargetID("newapi", "ws1", "100"), PolicyID: "p1"},
		{UserID: "user1", AdminAccountID: "ws2", TargetID: buildTargetID("newapi", "ws2", "100"), PolicyID: "p2"},
	}
	jobs := svc.collectAdminProbeJobs(context.Background(), policies, assignments)
	if len(jobs) != 2 {
		t.Fatalf("expected one job per workspace (2 total), got %d", len(jobs))
	}
	seen := map[string]bool{}
	for _, j := range jobs {
		seen[j.adminAccountID] = true
		if j.target.TargetID != buildTargetID("newapi", j.adminAccountID, "100") {
			t.Fatalf("target %q does not embed its workspace %q", j.target.TargetID, j.adminAccountID)
		}
	}
	if !seen["ws1"] || !seen["ws2"] {
		t.Fatalf("expected both workspaces scheduled, got %+v", seen)
	}
}
