package connection_health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"transithub/backend/internal/modules/upstream"
)

func newAdminTargetsRemoteActionService(reader PlatformGroupReader, mySites MySitesReader, repo *fakeRepository, platform *fakePlatformActioner) *Service {
	return &Service{
		repo: repo, mySites: mySites, accounts: fakeAdminAccountResolver{id: "ws1"},
		dispatcher:  newRemoteActionDispatcher(fakeSiteLookup{}, fakeSessionProvider{}, platform),
		probeRunner: NewRealProbeRunner(), platformGroups: reader,
	}
}

func sub2APIProbePolicy(autoRemoteAction bool) Policy {
	return Policy{
		ID: "policy-1", UserID: "user1", AdminAccountID: "ws1", Name: "p", Enabled: true, DailyProbeBudget: 1000,
		AutoDegradeEnabled: true, AutoRemoteActionEnabled: autoRemoteAction,
		FailureThreshold: 3, SuccessThreshold: 2, CooldownSeconds: 300, ObservationSeconds: 300, RecoveryStepPercent: 25,
		ModelTargets: []ModelTarget{{ID: "t1", PolicyID: "policy-1", ModelName: "gpt-4o", ProviderFamily: ProviderOpenAI, Enabled: true, MaxProbeTokens: 1}},
	}
}

func adminTargetBoolPtr(value bool) *bool { return &value }

func TestProbeTargetOnce_Sub2APIFailureNeverWritesPermanentSchedulableSwitch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	repo := newFakeRepository()
	repo.policies = []Policy{sub2APIProbePolicy(true)}
	targetID := "sub2api:ws1:acc-1"
	repo.states[targetID] = map[string]ConnectionHealthState{
		"gpt-4o": {ConnectionID: targetID, ModelName: "gpt-4o", UserID: "user1", AdminAccountID: "ws1", State: StateHealthy, CurrentWeight: 100, ConsecutiveFailures: 2},
	}
	schedulable := true
	platform := &fakePlatformActioner{}
	reader := fakePlatformGroupReader{
		groups:        []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{"g1": {{ID: "acc-1", Status: "active", Schedulable: &schedulable, Models: "gpt-4o"}}},
		credByAccount: map[string]upstream.ProbeCredential{"acc-1": {BaseURL: server.URL, Key: "k"}},
	}
	svc := newAdminTargetsRemoteActionService(reader, fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}}, repo, platform)

	results, err := svc.ProbeTarget(context.Background(), "user1", targetID, []string{"gpt-4o"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].State != StateSuspended {
		t.Fatalf("expected health state to become suspended, got %+v", results)
	}
	if len(platform.sub2APICalls) != 0 {
		t.Fatalf("TransitHub must not write Sub2API administrator switches: %+v", platform.sub2APICalls)
	}
}

func TestProbeTargetOnce_Sub2APIRecoveryNeverEnablesPermanentSwitch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	repo := newFakeRepository()
	repo.policies = []Policy{sub2APIProbePolicy(true)}
	targetID := "sub2api:ws1:acc-1"
	repo.states[targetID] = map[string]ConnectionHealthState{
		"gpt-4o": {ConnectionID: targetID, ModelName: "gpt-4o", UserID: "user1", AdminAccountID: "ws1", State: StateSuspended, CurrentWeight: 0, ConsecutiveFailures: 3},
	}
	schedulable := true
	platform := &fakePlatformActioner{}
	reader := fakePlatformGroupReader{
		groups:        []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{"g1": {{ID: "acc-1", Status: "active", Schedulable: &schedulable, Models: "gpt-4o"}}},
		credByAccount: map[string]upstream.ProbeCredential{"acc-1": {BaseURL: server.URL, Key: "k"}},
	}
	svc := newAdminTargetsRemoteActionService(reader, fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}}, repo, platform)

	results, err := svc.ProbeTarget(context.Background(), "user1", targetID, []string{"gpt-4o"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 熔断后的首次成功进入观察期，不再一次成功就满血：策略配了
	// SuccessThreshold=2 与 ObservationSeconds=300，两者都必须真正参与判定。
	if len(results) != 1 || results[0].State != StateObserving {
		t.Fatalf("expected observing after first success, got %+v", results)
	}
	if results[0].CurrentWeight != 25 {
		t.Fatalf("expected weight to step up to RecoveryStepPercent, got %d", results[0].CurrentWeight)
	}
	if len(platform.sub2APICalls) != 0 {
		t.Fatalf("health recovery must not enable administrator switches: %+v", platform.sub2APICalls)
	}

	// 观察窗结束后再成功一次，累计成功次数达到阈值，连接回到完全健康。
	state := repo.states[targetID]["gpt-4o"]
	elapsed := time.Now().Add(-time.Second)
	state.ObservingUntil = &elapsed
	repo.states[targetID]["gpt-4o"] = state

	results, err = svc.ProbeTarget(context.Background(), "user1", targetID, []string{"gpt-4o"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].State != StateHealthy {
		t.Fatalf("expected health state recovery, got %+v", results)
	}
	if results[0].CurrentWeight != 100 {
		t.Fatalf("expected full weight after recovery, got %d", results[0].CurrentWeight)
	}
	if len(platform.sub2APICalls) != 0 {
		t.Fatalf("health recovery must not enable administrator switches: %+v", platform.sub2APICalls)
	}
}

func recoveryTarget(status string, schedulable bool) AdminProbeTarget {
	value := schedulable
	return AdminProbeTarget{
		Platform:             string(upstream.PlatformSub2API),
		SchedulabilitySource: SchedulabilitySourceAutomatic,
		AccountStatus:        status,
		AccountSchedulable:   &value,
	}
}

// 系统自动停用的账号会落进一条缝隙：403 冷却结束或管理员清错误后 status 回到
// active，但 schedulable 不会自己变回 true。此时常规探活因运行时阻塞跳过它，
// 若恢复探测再要求 status=error，两个门都进不去，账号永远不会被探活、也永远
// 不会自愈——生产上「订阅-147」就卡在这里。
func TestSub2APIRecoveryCoversStatusActiveButNotSchedulable(t *testing.T) {
	now := time.Now()

	eligible, reason := sub2APIErrorRecoveryDecision(recoveryTarget("active", false), now)
	if !eligible {
		t.Fatalf("status=active + schedulable=false must stay recoverable, got reason %q", reason)
	}

	// 原有形态继续成立。
	if eligible, reason := sub2APIErrorRecoveryDecision(recoveryTarget("error", false), now); !eligible {
		t.Fatalf("status=error must remain recoverable, got reason %q", reason)
	}
	if eligible, reason := sub2APIErrorRecoveryDecision(recoveryTarget("error", true), now); !eligible {
		t.Fatalf("status=error alone must remain recoverable, got reason %q", reason)
	}
}

// 完全正常的账号不得进入恢复路径，否则每轮都会多打一次真实生成探测。
func TestSub2APIRecoverySkipsHealthySchedulableAccount(t *testing.T) {
	eligible, reason := sub2APIErrorRecoveryDecision(recoveryTarget("active", true), time.Now())
	if eligible {
		t.Fatalf("a healthy schedulable account must not enter recovery probing")
	}
	if reason != recoverySkipStatusNotError {
		t.Fatalf("reason = %q, want %q", reason, recoverySkipStatusNotError)
	}
}

// 放宽的只是 status 维度，来源判定仍然是硬约束：管理员手动关闭的账号，
// 无论 schedulable 是什么都不得自动恢复。
func TestSub2APIRecoveryStillRefusesManualSource(t *testing.T) {
	target := recoveryTarget("active", false)
	target.SchedulabilitySource = SchedulabilitySourceManual

	eligible, reason := sub2APIErrorRecoveryDecision(target, time.Now())
	if eligible {
		t.Fatalf("manual source must never be auto-recovered")
	}
	if reason != recoverySkipSourceManual {
		t.Fatalf("reason = %q, want %q", reason, recoverySkipSourceManual)
	}
}

// 来源缺失同样保持失败关闭——放宽 status 不能顺带放宽来源。
func TestSub2APIRecoveryStillRefusesMissingSource(t *testing.T) {
	target := recoveryTarget("active", false)
	target.SchedulabilitySource = ""

	if eligible, reason := sub2APIErrorRecoveryDecision(target, time.Now()); eligible || reason != recoverySkipSourceMissing {
		t.Fatalf("missing source must fail closed, got eligible=%v reason=%q", eligible, reason)
	}
}
