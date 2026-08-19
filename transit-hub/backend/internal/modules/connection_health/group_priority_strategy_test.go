package connection_health

import (
	"context"
	"errors"
	"testing"
	"time"

	"transithub/backend/internal/modules/upstream"
)

func TestCollectAdminProbeJobs_GroupAssignmentAndExclusion(t *testing.T) {
	repo := newFakeRepository()
	service := &Service{
		repo:           repo,
		mySites:        fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups: schedulerReader("100"),
	}
	policies := []Policy{{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ProbeIntervalSeconds: 60,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}}
	groupAssignments := []GroupPolicyAssignment{{
		UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", AdminGroupName: "vip", PolicyID: "p1",
	}}

	jobs := service.collectAdminProbeJobsWithGroups(context.Background(), policies, nil, groupAssignments, nil)
	if len(jobs) != 1 || jobs[0].target.TargetID != "newapi:ws1:100" {
		t.Fatalf("group policy should auto-include target, got %+v", jobs)
	}

	exclusions := []GroupTargetExclusion{{
		UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", TargetID: "newapi:ws1:100",
	}}
	jobs = service.collectAdminProbeJobsWithGroups(context.Background(), policies, nil, groupAssignments, exclusions)
	if len(jobs) != 0 {
		t.Fatalf("excluded target must not inherit group policy, got %+v", jobs)
	}

	// 旧版显式 target 分配优先于分组排除，保证已有线上配置不被新功能一棍子打死。
	explicit := []PolicyAssignment{{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "newapi:ws1:100", PolicyID: "p1",
	}}
	jobs = service.collectAdminProbeJobsWithGroups(context.Background(), policies, explicit, groupAssignments, exclusions)
	if len(jobs) != 1 {
		t.Fatalf("legacy explicit assignment must survive group exclusion, got %+v", jobs)
	}
}

func TestCollectAdminProbeJobs_PreservesPolicySourceGroupForSharedTarget(t *testing.T) {
	repo := newFakeRepository()
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "first"}, {ID: "g2", Name: "second"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", BaseURL: "https://up", Models: "model-a,model-b"}},
			"g2": {{ID: "100", BaseURL: "https://up", Models: "model-a,model-b"}},
		},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups: reader,
	}
	policies := []Policy{
		{ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ModelTargets: []ModelTarget{{ModelName: "model-a", Enabled: true}}},
		{ID: "p2", UserID: "user1", AdminAccountID: "ws1", Enabled: true, ModelTargets: []ModelTarget{{ModelName: "model-b", Enabled: true}}},
	}
	assignments := []GroupPolicyAssignment{
		{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", AdminGroupName: "first", PolicyID: "p1"},
		{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g2", AdminGroupName: "second", PolicyID: "p2"},
	}

	jobs := service.collectAdminProbeJobsWithGroups(context.Background(), policies, nil, assignments, nil)
	if len(jobs) != 1 || len(jobs[0].dueSpecs) != 2 {
		t.Fatalf("shared target should produce one two-model job, got %+v", jobs)
	}
	groupsByModel := make(map[string]string)
	for _, spec := range jobs[0].dueSpecs {
		groupsByModel[spec.modelName] = spec.eventAdminGroupID
	}
	if groupsByModel["model-a"] != "g1" || groupsByModel["model-b"] != "g2" {
		t.Fatalf("each policy event must retain its source group: %+v", groupsByModel)
	}
}

func TestAdminGroups_ReportsInheritedPolicyAndExclusion(t *testing.T) {
	repo := newFakeRepository()
	repo.policies = []Policy{probePolicy()}
	repo.groupAssignments = []GroupPolicyAssignment{{
		UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", AdminGroupName: "vip", PolicyID: "policy-1",
	}}
	repo.groupExclusions = []GroupTargetExclusion{{
		UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", TargetID: "newapi:ws1:200",
	}}
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Multiplier: float64Ptr(0.5)}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {
				{ID: "100", Name: "included", BaseURL: "https://up", Models: "gpt-4o"},
				{ID: "200", Name: "excluded", BaseURL: "https://up", Models: "gpt-4o"},
			},
		},
	}
	service := newAdminGroupsService(reader, fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}, repo)

	groups, err := service.AdminGroups(context.Background(), "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 || groups[0].MonitoredAccountCount != 1 || groups[0].ExcludedAccountCount != 1 {
		t.Fatalf("unexpected group assignment summary: %+v", groups)
	}
	for _, account := range groups[0].Accounts {
		if account.ID == "100" && (!account.HasAssignedPolicy || account.PolicyAssignmentSource != "group") {
			t.Fatalf("included account should inherit group policy: %+v", account)
		}
		if account.ID == "200" && (!account.ExcludedFromGroupPolicy || account.HasAssignedPolicy) {
			t.Fatalf("excluded account should not inherit group policy: %+v", account)
		}
	}
}

func TestSetAdminGroupPolicyConfiguration_ValidatesAndPersistsScope(t *testing.T) {
	repo := newFakeRepository()
	repo.policies = []Policy{probePolicy()}
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Name: "channel", BaseURL: "https://up", Models: "gpt-4o"}},
		},
	}
	service := newAdminGroupsService(reader, fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}, repo)
	targetID := "newapi:ws1:100"

	configuration, err := service.SetAdminGroupPolicyConfiguration(context.Background(), "user1", "g1", AdminGroupPolicyConfigurationInput{
		PolicyIDs: []string{"policy-1", "policy-1"}, ExcludedTargetIDs: []string{targetID, targetID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configuration.PolicyIDs) != 1 || configuration.PolicyIDs[0] != "policy-1" ||
		len(configuration.ExcludedTargetIDs) != 1 || configuration.ExcludedTargetIDs[0] != targetID {
		t.Fatalf("configuration should be deduplicated and persisted: %+v", configuration)
	}

	_, err = service.SetAdminGroupPolicyConfiguration(context.Background(), "user1", "g1", AdminGroupPolicyConfigurationInput{
		PolicyIDs: []string{"policy-1"}, ExcludedTargetIDs: []string{"newapi:ws1:not-in-group"},
	})
	if err == nil || err.Error() != ErrorProbeTargetNotFound {
		t.Fatalf("cross-group exclusion must be rejected, got %v", err)
	}
}

func TestSetAdminGroupPolicyConfiguration_QuickPolicyCreatesAndBindsTogether(t *testing.T) {
	repo := newFakeRepository()
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Name: "channel", BaseURL: "https://up", Models: "gpt-4o"}},
		},
	}
	service := newAdminGroupsService(reader, fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}, repo)
	configuration, err := service.SetAdminGroupPolicyConfiguration(context.Background(), "user1", "g1", AdminGroupPolicyConfigurationInput{
		QuickPolicy: &PolicyInput{
			Name: "quick", Enabled: true, AutoDegradeEnabled: true, AutoRemoteActionEnabled: true,
			ModelTargets: []ModelTargetInput{{ModelName: "gpt-4o", ProviderFamily: ProviderOpenAI, Enabled: true}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configuration.PolicyIDs) != 1 || len(repo.policies) != 1 || repo.policies[0].ID != configuration.PolicyIDs[0] {
		t.Fatalf("quick policy must be created and bound in one operation: config=%+v policies=%+v", configuration, repo.policies)
	}
}

func TestSetAdminGroupPolicyConfiguration_AllowsAccountLevelCostWithoutGroupMultiplier(t *testing.T) {
	repo := newFakeRepository()
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Multiplier: nil}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Name: "channel"}},
		},
	}
	service := newAdminGroupsService(reader, fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}, repo)
	configuration, err := service.SetAdminGroupPolicyConfiguration(context.Background(), "user1", "g1", AdminGroupPolicyConfigurationInput{
		QuickPolicy: &PolicyInput{
			Name: "price only", Enabled: true, StrategyMode: StrategyModeMultiplierOnly,
		},
	})
	if err != nil {
		t.Fatalf("group multiplier is not an account cost input and must not block the policy: %v", err)
	}
	if len(repo.policies) != 1 || len(configuration.PolicyIDs) != 1 {
		t.Fatalf("policy must be persisted so account-level upstream key rates can drive ranking: config=%+v policies=%+v", configuration, repo.policies)
	}
}

func TestSetAdminGroupPolicyConfiguration_ReclaimsOnlyConflictedPriorities(t *testing.T) {
	repo := newFakeRepository()
	repo.policies = []Policy{probePolicy()}
	conflictedTarget := "newapi:ws1:100"
	healthyTarget := "newapi:ws1:200"
	repo.priorityStates["user1|ws1|"+conflictedTarget] = PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: conflictedTarget, Conflict: true,
	}
	repo.priorityStates["user1|ws1|"+healthyTarget] = PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: healthyTarget, OriginalPriority: 7, LastAppliedPriority: 40000,
	}
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100"}, {ID: "200"}},
		},
	}
	service := newAdminGroupsService(reader, fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}}, repo)

	if _, err := service.SetAdminGroupPolicyConfiguration(context.Background(), "user1", "g1", AdminGroupPolicyConfigurationInput{PolicyIDs: []string{"policy-1"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, exists := repo.priorityStates["user1|ws1|"+conflictedTarget]; exists {
		t.Fatal("saving group configuration should clear conflicted state so the scheduler can reclaim it")
	}
	if _, exists := repo.priorityStates["user1|ws1|"+healthyTarget]; !exists {
		t.Fatal("non-conflicted state must retain its original priority baseline")
	}
}

func TestSetAdminGroupPolicyConfiguration_ReclaimsSub2APIGroupedPriority(t *testing.T) {
	repo := newFakeRepository()
	repo.policies = []Policy{probePolicy()}
	repo.priorityStates["user1|ws1|g1|sub2api:ws1:100"] = PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "g1|sub2api:ws1:100", Conflict: true,
	}
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Platform: string(upstream.PlatformSub2API)}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100"}},
		},
	}
	service := newAdminGroupsService(reader, fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}}, repo)

	if _, err := service.SetAdminGroupPolicyConfiguration(context.Background(), "user1", "g1", AdminGroupPolicyConfigurationInput{PolicyIDs: []string{"policy-1"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, exists := repo.priorityStates["user1|ws1|g1|sub2api:ws1:100"]; exists {
		t.Fatal("saving Sub2API group configuration should clear grouped conflicted priority state")
	}
}

type priorityUpdateCall struct {
	groupID  string
	targetID string
	priority int
}

type fakeTargetPriorityActioner struct {
	calls []priorityUpdateCall
}

func (f *fakeTargetPriorityActioner) UpdateAdminTargetPriority(session upstream.Session, targetID string, priority int) error {
	f.calls = append(f.calls, priorityUpdateCall{targetID: targetID, priority: priority})
	return nil
}

func (f *fakeTargetPriorityActioner) UpdateAdminTargetGroupPriority(session upstream.Session, groupID, targetID string, priority int) error {
	f.calls = append(f.calls, priorityUpdateCall{groupID: groupID, targetID: targetID, priority: priority})
	return nil
}

func TestMultiplierPrioritySync_Sub2APIUsesIndependentGroupBindings(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	accountPriority, sharedGroupPriority, peerGroupPriority := 999, 10, 1
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{
			{ID: "10", Name: "plus", Multiplier: float64Ptr(0.5)},
			{ID: "20", Name: "plus-line", Multiplier: float64Ptr(0.5)},
		},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"10": {
				{ID: "shared", Priority: &accountPriority, GroupPriority: &sharedGroupPriority, Models: "gpt-4o"},
				{ID: "peer-10", Priority: &accountPriority, GroupPriority: &peerGroupPriority, Models: "gpt-4o"},
			},
			"20": {
				{ID: "shared", Priority: &accountPriority, GroupPriority: &sharedGroupPriority, Models: "gpt-4o"},
				{ID: "peer-20", Priority: &accountPriority, GroupPriority: &peerGroupPriority, Models: "gpt-4o"},
			},
		},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader, priorityActions: priorityActions,
	}
	policy := Policy{ID: "priority", UserID: "user1", AdminAccountID: "ws1", Enabled: true, PriorityMode: PriorityModeMultiplier,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}}}
	assignments := []GroupPolicyAssignment{
		{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "10", PolicyID: policy.ID},
		{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "20", PolicyID: policy.ID},
	}
	sharedTargetID := "sub2api:ws1:shared"
	repo.states[sharedTargetID] = map[string]ConnectionHealthState{
		"gpt-4o": {ConnectionID: sharedTargetID, ModelName: "gpt-4o", State: StateSuspended, CurrentWeight: 0, UserID: "user1", AdminAccountID: "ws1"},
	}

	service.syncMultiplierPriorities(context.Background(), []Policy{policy}, nil, assignments, nil, nil)

	for _, groupID := range []string{"10", "20"} {
		key := "user1|ws1|" + groupID + "|" + sharedTargetID
		if _, exists := repo.priorityStates[key]; !exists {
			t.Fatalf("missing independent sync state for group %s: %+v", groupID, repo.priorityStates)
		}
	}
	updatedGroups := make(map[string]bool)
	for _, call := range priorityActions.calls {
		if call.targetID == "shared" {
			updatedGroups[call.groupID] = true
		}
	}
	if !updatedGroups["10"] || !updatedGroups["20"] {
		t.Fatalf("shared account must be updated in both groups, calls=%+v", priorityActions.calls)
	}
}

func TestMultiplierPrioritySync_Sub2APIMigratesLegacyGlobalCheckpoint(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	globalPriority, groupPriority := 40000, 7
	targetID := "sub2api:ws1:100"
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Priority: &globalPriority, GroupPriority: &groupPriority, Models: "gpt-4o"}},
		},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader, priorityActions: priorityActions,
	}
	policy := Policy{
		ID: "priority", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		PriorityMode: PriorityModeMultiplier, ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}
	assignment := GroupPolicyAssignment{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", PolicyID: policy.ID}
	legacy := PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: targetID,
		OriginalPriority: 5, LastAppliedPriority: globalPriority,
	}
	repo.priorityStates["user1|ws1|"+targetID] = legacy

	service.syncMultiplierPriorities(
		context.Background(), []Policy{policy}, nil, []GroupPolicyAssignment{assignment}, nil, []PrioritySyncState{legacy},
	)

	if _, exists := repo.priorityStates["user1|ws1|"+targetID]; exists {
		t.Fatal("legacy global checkpoint must be released after group-scoped priority takes ownership")
	}
	groupedKey := "user1|ws1|g1|" + targetID
	if state, exists := repo.priorityStates[groupedKey]; !exists || state.LastAppliedPriority != 100 {
		t.Fatalf("group-scoped checkpoint must be persisted independently: %+v", repo.priorityStates)
	}
	if len(priorityActions.calls) != 2 || priorityActions.calls[0].groupID != "g1" || priorityActions.calls[0].priority != 100 ||
		priorityActions.calls[1].groupID != "" || priorityActions.calls[1].priority != legacy.OriginalPriority {
		t.Fatalf("migration must set the group slot and restore the legacy global priority: %+v", priorityActions.calls)
	}
}

func TestMultiplierPrioritySyncAndManualConflict(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	accountPriority := 7
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Multiplier: float64Ptr(0.4)}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Name: "channel", Priority: &accountPriority, BaseURL: "https://up", Models: "gpt-4o"}},
		},
	}
	service := &Service{
		repo:            repo,
		mySites:         fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups:  reader,
		priorityActions: priorityActions,
	}
	policies := []Policy{{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, PriorityMode: PriorityModeMultiplier,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}}
	groupAssignments := []GroupPolicyAssignment{{
		UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", AdminGroupName: "vip", PolicyID: "p1",
	}}

	service.syncMultiplierPriorities(context.Background(), policies, nil, groupAssignments, nil, nil)
	if len(priorityActions.calls) != 1 || priorityActions.calls[0].priority != 9900 {
		t.Fatalf("a managed single target must receive the deterministic first slot, calls=%+v", priorityActions.calls)
	}
	stored := repo.priorityStates["user1|ws1|newapi:ws1:100"]
	if stored.OriginalPriority != 7 || stored.LastAppliedPriority != 9900 || stored.Conflict {
		t.Fatalf("unexpected stored priority state: %+v", stored)
	}

	// 模拟管理员在上游把系统写入值手动改为 23；下一轮只能标记冲突，不能再次覆盖。
	manualPriority := 23
	reader.accountsByGrp["g1"] = []upstream.AdminGroupAccountInfo{{
		ID: "100", Name: "channel", Priority: &manualPriority, BaseURL: "https://up", Models: "gpt-4o",
	}}
	service.platformGroups = reader
	priorityActions.calls = nil
	service.syncMultiplierPriorities(context.Background(), policies, nil, groupAssignments, nil, []PrioritySyncState{stored})
	if len(priorityActions.calls) != 0 {
		t.Fatalf("manual priority change must not be overwritten, calls=%+v", priorityActions.calls)
	}
	stored = repo.priorityStates["user1|ws1|newapi:ws1:100"]
	if !stored.Conflict || stored.LastConflictPriority == nil || *stored.LastConflictPriority != manualPriority {
		t.Fatalf("manual change should be recorded as conflict: %+v", stored)
	}
}

func TestMultiplierPrioritySyncReclaimsStaleConflictWhenCurrentPriorityMatchesLastApplied(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	currentPriority := 7
	targetID := "newapi:ws1:100"
	repo.priorityStates["user1|ws1|"+targetID] = PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: targetID,
		OriginalPriority: 1, LastAppliedPriority: currentPriority, Conflict: true,
	}
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Multiplier: float64Ptr(0.4)}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Name: "channel", Priority: &currentPriority, Models: "gpt-4o"}},
		},
	}
	service := &Service{
		repo:            repo,
		mySites:         fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups:  reader,
		priorityActions: priorityActions,
	}
	policy := Policy{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		PriorityMode: PriorityModeMultiplier,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}
	assignment := GroupPolicyAssignment{
		UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", AdminGroupName: "vip", PolicyID: "p1",
	}

	service.syncMultiplierPriorities(context.Background(), []Policy{policy}, nil, []GroupPolicyAssignment{assignment}, nil, []PrioritySyncState{repo.priorityStates["user1|ws1|"+targetID]})

	stored := repo.priorityStates["user1|ws1|"+targetID]
	if stored.Conflict {
		t.Fatalf("stale conflict should be reclaimed when current priority matches last applied: %+v", stored)
	}
	if stored.LastConflictPriority != nil {
		t.Fatalf("reclaimed conflict should clear last conflict priority: %+v", stored)
	}
	if len(priorityActions.calls) != 1 || priorityActions.calls[0].priority != 9900 {
		t.Fatalf("reclaimed target must be reconciled to the deterministic slot, calls=%+v", priorityActions.calls)
	}
}

func TestMultiplierPrioritySync_IgnoresFrozenHealthWhenAutoDegradeDisabled(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	targetID := "newapi:ws1:100"
	currentPriority := 1
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Multiplier: float64Ptr(0.4)}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Priority: &currentPriority, Models: "gpt-4o"}},
		},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups: reader, priorityActions: priorityActions,
	}
	policy := Policy{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		AutoDegradeEnabled: false, PriorityMode: PriorityModeMultiplier,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}
	assignment := GroupPolicyAssignment{
		UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", PolicyID: policy.ID,
	}
	repo.states[targetID] = map[string]ConnectionHealthState{
		"gpt-4o": {ConnectionID: targetID, ModelName: "gpt-4o", State: StateSuspended, CurrentWeight: 0, UserID: "user1", AdminAccountID: "ws1"},
	}
	stored := PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: targetID,
		OriginalPriority: 7, LastAppliedPriority: currentPriority,
	}
	repo.priorityStates["user1|ws1|"+targetID] = stored

	service.syncMultiplierPriorities(
		context.Background(), []Policy{policy}, nil, []GroupPolicyAssignment{assignment}, nil,
		[]PrioritySyncState{stored},
	)
	if len(priorityActions.calls) != 0 {
		t.Fatalf("frozen suspended state must not pin multiplier priority, calls=%+v", priorityActions.calls)
	}
}

func TestMultiplierPrioritySync_MultiplierOnlySingleTargetKeepsCurrentPriority(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	targetID := "newapi:ws1:100"
	currentPriority := 1
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Multiplier: float64Ptr(0.4)}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Priority: &currentPriority, Models: "gpt-4o"}},
		},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups: reader, priorityActions: priorityActions,
	}
	healthPolicy := Policy{
		ID: "health", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		StrategyMode: StrategyModeHealthProbe, AutoDegradeEnabled: true, PriorityMode: PriorityModeMultiplier,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}
	pricePolicy := Policy{
		ID: "price", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		StrategyMode: StrategyModeMultiplierOnly, PriorityMode: PriorityModeMultiplier,
	}
	assignments := []GroupPolicyAssignment{
		{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", PolicyID: healthPolicy.ID},
		{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", PolicyID: pricePolicy.ID},
	}
	repo.states[targetID] = map[string]ConnectionHealthState{
		"gpt-4o": {ConnectionID: targetID, ModelName: "gpt-4o", State: StateSuspended, CurrentWeight: 0, UserID: "user1", AdminAccountID: "ws1"},
	}

	service.syncMultiplierPriorities(context.Background(), []Policy{healthPolicy, pricePolicy}, nil, assignments, nil, nil)
	if len(priorityActions.calls) != 0 {
		t.Fatalf("a single target has no peer range to rebalance, calls=%+v", priorityActions.calls)
	}
}

func TestMultiplierPrioritySync_MultiplierOnlyUsesPersistedPausedState(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	healthyPriority, pausedPriority := 100, 100
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Multiplier: float64Ptr(0.4)}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {
				{ID: "healthy", Priority: &healthyPriority, Models: "gpt-4o"},
				{ID: "paused", Priority: &pausedPriority, Models: "gpt-4o"},
			},
		},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups: reader, priorityActions: priorityActions,
	}
	policy := Policy{
		ID: "price", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		StrategyMode: StrategyModeMultiplierOnly, PriorityMode: PriorityModeMultiplier,
	}
	assignment := GroupPolicyAssignment{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", PolicyID: policy.ID}
	pausedTargetID := "newapi:ws1:paused"
	repo.states[pausedTargetID] = map[string]ConnectionHealthState{
		"gpt-4o": {ConnectionID: pausedTargetID, ModelName: "gpt-4o", State: StateSuspended, CurrentWeight: 0, UserID: "user1", AdminAccountID: "ws1"},
	}

	service.syncMultiplierPriorities(context.Background(), []Policy{policy}, nil, []GroupPolicyAssignment{assignment}, nil, nil)
	got := make(map[string]int, len(priorityActions.calls))
	for _, call := range priorityActions.calls {
		got[call.targetID] = call.priority
	}
	if got["paused"] >= got["healthy"] {
		t.Fatalf("NewAPI paused target must be lower priority than healthy target: %+v", got)
	}
}

func TestMultiplierPrioritySync_Sub2APIMultiplierOnlyIgnoresStaleProbeState(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	healthyPriority, pausedPriority, disabledPriority := 50, 50, 50
	schedulable := true
	unschedulable := false
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "12", Name: "plus-专线", Multiplier: float64Ptr(0.4)}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"12": {
				{ID: "101", Name: "healthy", GroupPriority: &healthyPriority, Schedulable: &schedulable, Models: "gpt-4o"},
				{ID: "102", Name: "stale-paused", GroupPriority: &pausedPriority, Schedulable: &schedulable, Models: "gpt-4o"},
				{ID: "103", Name: "unschedulable", GroupPriority: &disabledPriority, Schedulable: &unschedulable, Models: "gpt-4o"},
			},
		},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader, priorityActions: priorityActions,
	}
	policy := Policy{
		ID: "price", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		StrategyMode: StrategyModeMultiplierOnly, PriorityMode: PriorityModeMultiplier,
	}
	assignment := GroupPolicyAssignment{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "12", PolicyID: policy.ID}
	repo.manualMultipliers["user1|ws1|sub2api:ws1:101"] = ManualUpstreamKeyMultiplier{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "sub2api:ws1:101", Multiplier: 0.08,
	}
	repo.manualMultipliers["user1|ws1|sub2api:ws1:102"] = ManualUpstreamKeyMultiplier{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "sub2api:ws1:102", Multiplier: 0.04,
	}
	repo.manualMultipliers["user1|ws1|sub2api:ws1:103"] = ManualUpstreamKeyMultiplier{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "sub2api:ws1:103", Multiplier: 0.01,
	}
	staleTargetID := "sub2api:ws1:102"
	repo.states[staleTargetID] = map[string]ConnectionHealthState{
		"gpt-4o": {ConnectionID: staleTargetID, ModelName: "gpt-4o", State: StateSuspended, CurrentWeight: 0, UserID: "user1", AdminAccountID: "ws1"},
	}

	service.syncMultiplierPriorities(context.Background(), []Policy{policy}, nil, []GroupPolicyAssignment{assignment}, nil, nil)
	got := make(map[string]int, len(priorityActions.calls))
	for _, call := range priorityActions.calls {
		got[call.targetID] = call.priority
	}
	if got["102"] != 100 || got["101"] != 200 || got["103"] != 10000 {
		t.Fatalf("Sub2API multiplier-only must rank schedulable accounts by price and reserve 10000 for current unschedulability: %+v", got)
	}
}

func TestMultiplierPrioritySync_ConfirmsPendingSystemWrite(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	desired := 9900
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Multiplier: float64Ptr(0.4)}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Priority: &desired, Models: "gpt-4o"}},
		},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups: reader, priorityActions: priorityActions,
	}
	policy := Policy{ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, PriorityMode: PriorityModeMultiplier, ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}}}
	assignment := GroupPolicyAssignment{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", PolicyID: policy.ID}
	pending := desired
	stored := PrioritySyncState{UserID: "user1", AdminAccountID: "ws1", TargetID: "newapi:ws1:100", OriginalPriority: 7, LastAppliedPriority: 7, PendingPriority: &pending}

	service.syncMultiplierPriorities(context.Background(), []Policy{policy}, nil, []GroupPolicyAssignment{assignment}, nil, []PrioritySyncState{stored})
	updated := repo.priorityStates["user1|ws1|newapi:ws1:100"]
	if updated.Conflict || updated.PendingPriority != nil || updated.LastAppliedPriority != desired {
		t.Fatalf("pending priority write should be confirmed without conflict: %+v", updated)
	}
	if len(priorityActions.calls) != 0 {
		t.Fatalf("already-applied priority must not be written twice: %+v", priorityActions.calls)
	}
}

func TestMultiplierPrioritySync_DoesNotRestoreWhenInventoryIsIncomplete(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	reader := fakePlatformGroupReader{
		groups:   []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Multiplier: float64Ptr(0.4)}},
		errByGrp: map[string]error{"g1": errors.New("temporary upstream failure")},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups: reader, priorityActions: priorityActions,
	}
	policy := Policy{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true, PriorityMode: PriorityModeMultiplier,
		ModelTargets: []ModelTarget{{ModelName: "gpt-4o", Enabled: true}},
	}
	assignment := GroupPolicyAssignment{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", PolicyID: policy.ID}
	stored := PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "newapi:ws1:100",
		OriginalPriority: 7, LastAppliedPriority: 40999,
	}
	repo.priorityStates["user1|ws1|"+stored.TargetID] = stored

	service.syncMultiplierPriorities(context.Background(), []Policy{policy}, nil, []GroupPolicyAssignment{assignment}, nil, []PrioritySyncState{stored})
	if len(priorityActions.calls) != 0 {
		t.Fatalf("incomplete inventory must not restore or rewrite priority: %+v", priorityActions.calls)
	}
	if _, exists := repo.priorityStates["user1|ws1|"+stored.TargetID]; !exists {
		t.Fatal("incomplete inventory must retain the priority checkpoint for the next scan")
	}
}

func TestMultiplierPrioritySync_MissingMultiplierUsesUnknownCostSlot(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	currentPriority := 40999
	reader := fakePlatformGroupReader{
		groups: []upstream.AdminGroupInfo{{ID: "g1", Name: "vip", Multiplier: nil}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{
			"g1": {{ID: "100", Name: "channel", Priority: &currentPriority}},
		},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups: reader, priorityActions: priorityActions,
	}
	policy := Policy{
		ID: "p1", UserID: "user1", AdminAccountID: "ws1", Enabled: true,
		PriorityMode: PriorityModeMultiplier, StrategyMode: StrategyModeMultiplierOnly,
	}
	assignment := GroupPolicyAssignment{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", PolicyID: policy.ID}
	stored := PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "newapi:ws1:100",
		OriginalPriority: 7, LastAppliedPriority: currentPriority, EffectiveMultiplier: 0.4,
	}
	repo.priorityStates["user1|ws1|"+stored.TargetID] = stored

	service.syncMultiplierPriorities(context.Background(), []Policy{policy}, nil, []GroupPolicyAssignment{assignment}, nil, []PrioritySyncState{stored})
	if len(priorityActions.calls) != 1 || priorityActions.calls[0].priority != 9900 {
		t.Fatalf("unknown cost remains manageable and must use the deterministic available slot: %+v", priorityActions.calls)
	}
	if got, exists := repo.priorityStates["user1|ws1|"+stored.TargetID]; !exists || got.LastAppliedPriority != 9900 {
		t.Fatalf("unknown cost must persist the applied slot: %+v", got)
	}
}

func TestDesiredTransitHubPriorities_AutoGroupStrategies(t *testing.T) {
	base := []managedPriorityCandidate{
		{targetID: "cheap-slow", upstreamMultiplier: float64Ptr(0.04), latencyMs: intValuePtr(29_000), priorityMode: PriorityModeAuto},
		{targetID: "costlier-fast", upstreamMultiplier: float64Ptr(0.05), latencyMs: intValuePtr(1_000), priorityMode: PriorityModeAuto},
	}

	price := append([]managedPriorityCandidate(nil), base...)
	price[0].priorityStrategy, price[1].priorityStrategy = PriorityStrategyPrice, PriorityStrategyPrice
	priceDesired := desiredTransitHubPriorities(upstream.PlatformSub2API, price)
	if priceDesired["cheap-slow"] >= priceDesired["costlier-fast"] {
		t.Fatalf("price strategy must choose the lowest rate: %+v", priceDesired)
	}

	balanced := append([]managedPriorityCandidate(nil), base...)
	balanced[0].priorityStrategy, balanced[1].priorityStrategy = PriorityStrategyBalanced, PriorityStrategyBalanced
	balancedDesired := desiredTransitHubPriorities(upstream.PlatformSub2API, balanced)
	if balancedDesired["costlier-fast"] >= balancedDesired["cheap-slow"] {
		t.Fatalf("70/30 balanced score should let a materially faster near-price target win: %+v", balancedDesired)
	}

	speed := append([]managedPriorityCandidate(nil), base...)
	speed[0].priorityStrategy, speed[1].priorityStrategy = PriorityStrategySpeed, PriorityStrategySpeed
	speedDesired := desiredTransitHubPriorities(upstream.PlatformSub2API, speed)
	if speedDesired["costlier-fast"] >= speedDesired["cheap-slow"] {
		t.Fatalf("speed strategy must choose the lower reliable latency: %+v", speedDesired)
	}
}

func TestPrioritySettingsForCandidates_ConflictingStrategiesUsePrice(t *testing.T) {
	candidates := []managedPriorityCandidate{
		{targetID: "speed", priorityMode: PriorityModeAuto, priorityStrategy: PriorityStrategySpeed},
		{targetID: "balanced", priorityMode: PriorityModeAuto, priorityStrategy: PriorityStrategyBalanced},
	}
	for _, values := range [][]managedPriorityCandidate{candidates, {candidates[1], candidates[0]}} {
		mode, strategy := prioritySettingsForCandidates(values)
		if mode != PriorityModeAuto || strategy != PriorityStrategyPrice {
			t.Fatalf("mixed group strategies must deterministically use price: mode=%s strategy=%s", mode, strategy)
		}
	}
}

func TestPriorityStrategyForPolicies_ConflictingStrategiesUsePrice(t *testing.T) {
	policies := []Policy{
		{Enabled: true, PriorityMode: PriorityModeAuto, PriorityStrategy: PriorityStrategySpeed},
		{Enabled: true, PriorityMode: PriorityModeAuto, PriorityStrategy: PriorityStrategyBalanced},
	}
	if got := priorityStrategyForPolicies(policies); got != PriorityStrategyPrice {
		t.Fatalf("mixed policies must deterministically use price, got %s", got)
	}
}

func TestTransitHubP95SuccessfulLatency_UsesReliableRecentSuccessesOnly(t *testing.T) {
	now := time.Now()
	latency := func(value int) *int { return &value }
	events := []ConnectionHealthEvent{
		{ConnectionID: "target", ModelName: "gpt", Result: string(ResultOK), LatencyMs: latency(100), CreatedAt: now.Add(-time.Minute)},
		{ConnectionID: "target", ModelName: "gpt", Result: string(ResultOK), LatencyMs: latency(200), CreatedAt: now.Add(-2 * time.Minute)},
		{ConnectionID: "target", ModelName: "gpt", Result: string(ResultOK), LatencyMs: latency(500), CreatedAt: now.Add(-3 * time.Minute)},
		{ConnectionID: "target", ModelName: "gpt", Result: string(ResultServerError), LatencyMs: latency(9000), CreatedAt: now.Add(-4 * time.Minute)},
		{ConnectionID: "target", ModelName: "gpt", Result: string(ResultOK), LatencyMs: latency(8000), CreatedAt: now.Add(-2 * time.Hour)},
	}
	got := transitHubP95SuccessfulLatency(events, "target", map[string]struct{}{"gpt": {}}, now)
	if got == nil || *got != 500 {
		t.Fatalf("p95 must use the three recent successful samples, got %v", got)
	}
}

func TestPreferredTransitHubLatency_UsesSub2APIUsageBeforeProbeFallback(t *testing.T) {
	usageP95, probeP95 := 1800, 300
	if got := preferredTransitHubLatency(&usageP95, &probeP95); got == nil || *got != usageP95 {
		t.Fatalf("Sub2API usage P95 must override local probe P95, got %v", got)
	}
	if got := preferredTransitHubLatency(nil, &probeP95); got == nil || *got != probeP95 {
		t.Fatalf("local probe P95 must be used when Sub2API usage is insufficient, got %v", got)
	}
	if got := preferredTransitHubLatency(nil, nil); got != nil {
		t.Fatalf("missing usage and probe samples must remain unknown, got %v", got)
	}
}

func TestMultiplierPrioritySync_MissingConflictedTargetIsNotOverwritten(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformNewAPI}},
		platformGroups: fakePlatformGroupReader{}, priorityActions: priorityActions,
	}
	stored := PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "newapi:ws1:100",
		OriginalPriority: 7, LastAppliedPriority: 40999, Conflict: true,
	}
	repo.priorityStates["user1|ws1|"+stored.TargetID] = stored

	service.syncMultiplierPriorities(context.Background(), nil, nil, nil, nil, []PrioritySyncState{stored})
	if len(priorityActions.calls) != 0 {
		t.Fatalf("missing target with a manual conflict must not be overwritten: %+v", priorityActions.calls)
	}
	if _, exists := repo.priorityStates["user1|ws1|"+stored.TargetID]; exists {
		t.Fatal("unmanaged conflicted target should release its stale checkpoint without a remote write")
	}
}

func TestMultiplierPrioritySync_MissingSub2APIGroupBindingDropsCheckpointWithoutWrite(t *testing.T) {
	repo := newFakeRepository()
	priorityActions := &fakeTargetPriorityActioner{}
	reader := fakePlatformGroupReader{
		groups:        []upstream.AdminGroupInfo{{ID: "g1", Name: "vip"}},
		accountsByGrp: map[string][]upstream.AdminGroupAccountInfo{"g1": {}},
	}
	service := &Service{
		repo: repo, mySites: fakeMySitesReader{session: upstream.Session{Platform: upstream.PlatformSub2API}},
		platformGroups: reader, priorityActions: priorityActions,
	}
	stored := PrioritySyncState{
		UserID: "user1", AdminAccountID: "ws1", TargetID: "g1|sub2api:ws1:100",
		OriginalPriority: 7, LastAppliedPriority: 100,
	}
	repo.priorityStates["user1|ws1|"+stored.TargetID] = stored

	service.syncMultiplierPriorities(context.Background(), nil, nil, nil, nil, []PrioritySyncState{stored})

	if len(priorityActions.calls) != 0 {
		t.Fatalf("a removed group membership has no remote priority binding to restore: %+v", priorityActions.calls)
	}
	if _, exists := repo.priorityStates["user1|ws1|"+stored.TargetID]; exists {
		t.Fatal("removed group membership must release its stale local checkpoint")
	}
}

func TestDesiredTransitHubPriorities_PriceDominatesSpeedHealthAndOldPriority(t *testing.T) {
	candidates := []managedPriorityCandidate{
		{
			targetID: "cheap-slow", currentPriority: 9000, upstreamMultiplier: float64Ptr(0.04), latencyMs: intValuePtr(9000),
			states: []ConnectionHealthState{{State: StateDegraded, CurrentWeight: 75}}, expectedModels: 1,
		},
		{
			targetID: "expensive-fast", currentPriority: 1, upstreamMultiplier: float64Ptr(0.07), latencyMs: intValuePtr(100),
			states: []ConnectionHealthState{{State: StateHealthy, CurrentWeight: 100}}, expectedModels: 1,
		},
	}

	for _, platform := range []upstream.Platform{upstream.PlatformSub2API, upstream.PlatformNewAPI} {
		desired := desiredTransitHubPriorities(platform, candidates)
		if platform == upstream.PlatformSub2API && desired["cheap-slow"] >= desired["expensive-fast"] {
			t.Fatalf("Sub2API must keep the cheaper usable target first: %+v", desired)
		}
		if platform == upstream.PlatformNewAPI && desired["cheap-slow"] <= desired["expensive-fast"] {
			t.Fatalf("NewAPI must keep the cheaper usable target first: %+v", desired)
		}
	}
}

func TestDesiredTransitHubPriorities_UsesSuccessfulLatencyForEqualPrice(t *testing.T) {
	candidates := []managedPriorityCandidate{
		{
			targetID: "slow", upstreamMultiplier: float64Ptr(0.05), latencyMs: intValuePtr(2500),
			states: []ConnectionHealthState{{State: StateHealthy, CurrentWeight: 100}}, expectedModels: 1,
		},
		{
			targetID: "fast", upstreamMultiplier: float64Ptr(0.05), latencyMs: intValuePtr(500),
			states: []ConnectionHealthState{{State: StateHealthy, CurrentWeight: 100}}, expectedModels: 1,
		},
	}

	desired := desiredTransitHubPriorities(upstream.PlatformSub2API, candidates)
	if desired["fast"] >= desired["slow"] {
		t.Fatalf("equal-price targets must use probe latency as the second ordering key: %+v", desired)
	}
}

func TestDesiredTransitHubPriorities_BlockedTargetsUseReservedLastSlot(t *testing.T) {
	disabled := false
	candidates := []managedPriorityCandidate{
		{
			targetID: "healthy", upstreamMultiplier: float64Ptr(0.07), latencyMs: intValuePtr(2000),
			states: []ConnectionHealthState{{State: StateHealthy, CurrentWeight: 100}}, expectedModels: 1,
		},
		{
			targetID: "paused-cheap", upstreamMultiplier: float64Ptr(0.01), latencyMs: intValuePtr(50),
			states: []ConnectionHealthState{{State: StateSuspended, CurrentWeight: 0}}, expectedModels: 1,
		},
		{
			targetID: "unschedulable", upstreamMultiplier: float64Ptr(0.01), latencyMs: intValuePtr(40), schedulable: &disabled,
		},
	}

	desired := desiredTransitHubPriorities(upstream.PlatformSub2API, candidates)
	if desired["healthy"] >= desired["paused-cheap"] || desired["healthy"] >= desired["unschedulable"] {
		t.Fatalf("blocked targets must stay behind every usable target regardless of price or speed: %+v", desired)
	}
	if desired["paused-cheap"] != desired["unschedulable"] {
		t.Fatalf("all blocked targets should share the reserved last slot: %+v", desired)
	}
}

func TestDesiredTransitHubPriorities_RuntimeBlockExpiresBackIntoPriceOrder(t *testing.T) {
	candidates := []managedPriorityCandidate{
		{targetID: "cheap", upstreamMultiplier: float64Ptr(0.01), latencyMs: intValuePtr(800), runtimeBlocked: true},
		{targetID: "expensive", upstreamMultiplier: float64Ptr(0.08), latencyMs: intValuePtr(100)},
	}

	blocked := desiredTransitHubPriorities(upstream.PlatformSub2API, candidates)
	if blocked["cheap"] <= blocked["expensive"] {
		t.Fatalf("runtime-blocked cheap target must be reserved last: %+v", blocked)
	}

	candidates[0].runtimeBlocked = false
	recovered := desiredTransitHubPriorities(upstream.PlatformSub2API, candidates)
	if recovered["cheap"] >= recovered["expensive"] {
		t.Fatalf("after runtime recovery, lower price must regain priority before speed: %+v", recovered)
	}
}

func TestSub2APIAccountRuntimeBlocked_UsesOnlyActiveRuntimeWindows(t *testing.T) {
	now := time.Now()
	schedulable := true
	future := now.Add(time.Minute)
	past := now.Add(-time.Minute)

	if !sub2APIAccountRuntimeBlocked(upstream.AdminGroupAccountInfo{Status: "active", Schedulable: &schedulable, RateLimitResetAt: &future}, now) {
		t.Fatal("future rate-limit window must block scheduling")
	}
	if sub2APIAccountRuntimeBlocked(upstream.AdminGroupAccountInfo{Status: "active", Schedulable: &schedulable, TempUnschedulableUntil: &past}, now) {
		t.Fatal("expired temporary block must automatically become schedulable again")
	}
}

func TestFilterToAssignedTargetEvents_SameNameGroupExclusionDoesNotHideOtherAssignment(t *testing.T) {
	repo := newFakeRepository()
	repo.groupAssignments = []GroupPolicyAssignment{
		{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", AdminGroupName: "same", PolicyID: "p1"},
		{UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g2", AdminGroupName: "same", PolicyID: "p1"},
	}
	targetID := "newapi:ws1:100"
	repo.groupExclusions = []GroupTargetExclusion{{
		UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g1", TargetID: targetID,
	}}
	service := &Service{repo: repo}
	events, err := service.filterToAssignedTargetEvents(context.Background(), "user1", "ws1", []ConnectionHealthEvent{{
		ConnectionID: targetID, OwnGroupName: "same",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("assignment from the non-excluded same-name group should retain the event, got %+v", events)
	}
}

func TestFilterToAssignedTargetEvents_DropsUnassignedAdminGroupEvent(t *testing.T) {
	repo := newFakeRepository()
	service := &Service{repo: repo}
	targetID := "newapi:ws1:100"
	events, err := service.filterToAssignedTargetEvents(context.Background(), "user1", "ws1", []ConnectionHealthEvent{{
		ConnectionID: targetID, AdminGroupID: "removed", OwnGroupName: "removed",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("event from an unassigned admin group must be filtered, got %+v", events)
	}
}

func TestFilterToAssignedTargetEvents_KeepsUnmanagedRestoreAudit(t *testing.T) {
	repo := newFakeRepository()
	service := &Service{repo: repo}
	targetID := "newapi:ws1:100"
	events, err := service.filterToAssignedTargetEvents(context.Background(), "user1", "ws1", []ConnectionHealthEvent{{
		ConnectionID: targetID, Result: "policy_unmanaged_restore",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("automatic restore must remain visible after the final policy is unbound: %+v", events)
	}
}

func TestFilterToAssignedTargetEvents_UsesPolicyForLegacyWrongGroupMetadata(t *testing.T) {
	repo := newFakeRepository()
	repo.groupAssignments = []GroupPolicyAssignment{{
		UserID: "user1", AdminAccountID: "ws1", AdminGroupID: "g2", AdminGroupName: "second", PolicyID: "p2",
	}}
	service := &Service{repo: repo}
	targetID := "newapi:ws1:100"
	events, err := service.filterToAssignedTargetEvents(context.Background(), "user1", "ws1", []ConnectionHealthEvent{{
		ConnectionID: targetID, PolicyID: "p2", AdminGroupID: "removed-g1", OwnGroupName: "first",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("legacy event must follow its still-assigned policy when stored group metadata is stale: %+v", events)
	}
}

func float64Ptr(value float64) *float64 { return &value }

func intValuePtr(value int) *int { return &value }
