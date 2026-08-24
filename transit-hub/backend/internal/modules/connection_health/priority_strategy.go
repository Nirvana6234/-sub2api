package connection_health

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"transithub/backend/internal/modules/upstream"
)

// TargetPriorityActioner 是倍率排序策略对 upstream 模块的唯一写依赖。真实实现根据 session
// 平台更新 New API channel 或 Sub2API account 的 priority，并由 upstream 模块保证字段级写入安全。
type TargetPriorityActioner interface {
	UpdateAdminTargetPriority(session upstream.Session, targetID string, priority int) error
	UpdateAdminTargetGroupPriority(session upstream.Session, groupID, targetID string, priority int) error
}

type priorityTargetInventory struct {
	syncTargetID       string
	groupID            string
	target             AdminProbeTarget
	account            upstream.AdminGroupAccountInfo
	policies           []Policy
	upstreamMultiplier *float64
	currentPriority    int
}

// managedPriorityCandidate contains only observations owned by TransitHub.
// Existing upstream priority is retained for conflict detection, never ranking.
type managedPriorityCandidate struct {
	targetID           string
	groupID            string
	currentPriority    int
	upstreamMultiplier *float64
	latencyMs          *int
	schedulerScore     *float64
	currentConcurrency *int
	concurrency        *int
	priorityMode       string
	priorityStrategy   string
	states             []ConnectionHealthState
	expectedModels     int
	schedulable        *bool
	runtimeBlocked     bool
}

func prioritySyncTargetID(platform, groupID, targetID string) string {
	if platform != string(upstream.PlatformSub2API) || strings.TrimSpace(groupID) == "" {
		return targetID
	}
	return groupID + "|" + targetID
}

func parsePrioritySyncTargetID(value string) (groupID, targetID string, grouped bool) {
	parts := strings.SplitN(value, "|", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", value, false
	}
	return parts[0], parts[1], true
}

// syncMultiplierPriorities synchronizes upstream priorities before each probe cycle. Every mode consumes persisted health state; multiplier-only mode does not initiate new probes.
func (s *Service) syncMultiplierPriorities(
	ctx context.Context,
	policies []Policy,
	targetAssignments []PolicyAssignment,
	groupAssignments []GroupPolicyAssignment,
	exclusions []GroupTargetExclusion,
	allSyncStates []PrioritySyncState,
) {
	s.syncMultiplierPrioritiesWithCache(ctx, policies, targetAssignments, groupAssignments, exclusions, allSyncStates, make(adminInventoryCache))
}

func (s *Service) syncMultiplierPrioritiesWithCache(
	ctx context.Context,
	policies []Policy,
	targetAssignments []PolicyAssignment,
	groupAssignments []GroupPolicyAssignment,
	exclusions []GroupTargetExclusion,
	allSyncStates []PrioritySyncState,
	inventoryCache adminInventoryCache,
) {
	if s.priorityActions == nil || s.platformGroups == nil {
		return
	}
	automaticDisableEvents := make([]AutomaticDisableEvent, 0)

	assignedTargets := assignedEnabledPoliciesByTarget(policies, targetAssignments)
	assignedGroups := assignedEnabledPoliciesByGroup(policies, groupAssignments)
	excluded := groupTargetExclusionIndex(exclusions)
	statesByWorkspace := make(map[string][]PrioritySyncState)
	workspaceIdentity := make(map[string][2]string)
	for _, state := range allSyncStates {
		key := state.UserID + "|" + state.AdminAccountID
		statesByWorkspace[key] = append(statesByWorkspace[key], state)
		workspaceIdentity[key] = [2]string{state.UserID, state.AdminAccountID}
	}
	for _, policy := range policies {
		key := policy.UserID + "|" + policy.AdminAccountID
		workspaceIdentity[key] = [2]string{policy.UserID, policy.AdminAccountID}
	}
	for _, assignment := range targetAssignments {
		key := assignment.UserID + "|" + assignment.AdminAccountID
		workspaceIdentity[key] = [2]string{assignment.UserID, assignment.AdminAccountID}
	}
	for _, assignment := range groupAssignments {
		key := assignment.UserID + "|" + assignment.AdminAccountID
		workspaceIdentity[key] = [2]string{assignment.UserID, assignment.AdminAccountID}
	}

	for workspaceKey, identity := range workspaceIdentity {
		userID, adminAccountID := identity[0], identity[1]
		inventorySnapshot, err := s.loadAdminInventory(ctx, userID, adminAccountID, inventoryCache)
		if err != nil {
			log.Printf("[connection-health] priority sync load admin inventory failed user_id=%s admin_account_id=%s err=%v", userID, adminAccountID, err)
			continue
		}
		session := inventorySnapshot.session
		inventory, inventoryComplete, err := s.priorityInventoryForSnapshot(
			inventorySnapshot, adminAccountID, assignedTargets[workspaceKey], assignedGroups[workspaceKey], excluded[workspaceKey],
		)
		if err != nil {
			log.Printf("[connection-health] priority sync inventory failed user_id=%s admin_account_id=%s err=%v", userID, adminAccountID, err)
			continue
		}
		states, err := s.repo.ListStatesByWorkspace(ctx, userID, adminAccountID)
		if err != nil {
			log.Printf("[connection-health] priority sync list health states failed user_id=%s admin_account_id=%s err=%v", userID, adminAccountID, err)
			continue
		}
		events, eventErr := s.repo.ListRecentEventsByWorkspace(ctx, userID, adminAccountID, 500)
		if eventErr != nil {
			log.Printf("[connection-health] priority sync list latency samples failed user_id=%s admin_account_id=%s err=%v", userID, adminAccountID, eventErr)
		}
		s.applyTransitHubCostInputs(ctx, userID, adminAccountID, string(session.Platform), inventory)
		automaticDisableEvents = append(automaticDisableEvents,
			s.syncWorkspacePriorities(ctx, session, userID, adminAccountID, inventory, inventoryComplete, states, events, statesByWorkspace[workspaceKey])...)
	}
	s.notifyAutomaticDisables(ctx, automaticDisableEvents)
}

func (s *Service) applyTransitHubCostInputs(
	ctx context.Context,
	userID string,
	adminAccountID string,
	platform string,
	inventory map[string]*priorityTargetInventory,
) {
	detected := s.upstreamKeyGroupsByAdminAccount(ctx, userID, adminAccountID, platform)
	manual, err := s.repo.ListManualUpstreamKeyMultipliers(ctx, userID, adminAccountID)
	if err != nil {
		log.Printf("[connection-health] priority sync manual upstream multiplier load failed user_id=%s admin_account_id=%s err=%v", userID, adminAccountID, err)
	}
	manualByTarget := make(map[string]float64, len(manual))
	for _, value := range manual {
		if value.Multiplier > 0 {
			manualByTarget[value.TargetID] = value.Multiplier
		}
	}
	for _, item := range inventory {
		if info, ok := detected[item.target.AccountID]; ok && info.multiplier != nil && *info.multiplier > 0 {
			value := *info.multiplier
			item.upstreamMultiplier = &value
			continue
		}
		if value, ok := manualByTarget[item.target.TargetID]; ok {
			copy := value
			item.upstreamMultiplier = &copy
		}
	}
}

func (s *Service) priorityInventoryForSnapshot(
	snapshot *adminWorkspaceInventory,
	adminAccountID string,
	targetPolicies map[string][]Policy,
	groupPolicies map[string][]Policy,
	excludedByGroup map[string]map[string]bool,
) (map[string]*priorityTargetInventory, bool, error) {
	session := snapshot.session
	platform := string(session.Platform)
	inventory := make(map[string]*priorityTargetInventory)
	inventoryComplete := true
	for _, groupInventory := range snapshot.groups {
		group := groupInventory.group
		if groupInventory.err != nil {
			// 单个分组失败不阻断其它分组排序；目标如果只存在于失败分组，本轮保持原值。
			inventoryComplete = false
			log.Printf("[connection-health] priority sync group accounts failed group_id=%s err=%v", group.ID, groupInventory.err)
			continue
		}
		for _, account := range groupInventory.accounts {
			targetID := buildTargetID(platform, adminAccountID, account.ID)
			syncTargetID := prioritySyncTargetID(platform, group.ID, targetID)
			item := inventory[syncTargetID]
			if item == nil {
				item = &priorityTargetInventory{
					syncTargetID: syncTargetID,
					groupID:      group.ID,
					target: AdminProbeTarget{
						TargetID: targetID, Platform: platform, AdminGroupID: group.ID, AdminGroupName: group.Name,
						AccountID: account.ID, AccountName: account.Name, AccountStatus: account.Status,
						AccountSchedulable: cloneBoolPointer(account.Schedulable), AccountWeight: cloneIntPointer(account.Weight),
						ProviderFamily: account.Platform, Models: splitModelList(account.Models),
					},
					account: account,
				}
				applySub2APIRuntimeState(&item.target, account)
				if account.GroupPriority != nil && session.Platform == upstream.PlatformSub2API {
					item.currentPriority = *account.GroupPriority
				} else if account.Priority != nil {
					item.currentPriority = *account.Priority
				}
				inventory[syncTargetID] = item
			}
			inherited := groupPolicies[group.ID]
			excluded := excludedByGroup[group.ID][targetID]
			if excluded {
				inherited = nil
			}
			item.policies = mergePoliciesByID(item.policies, targetPolicies[targetID], inherited)
		}
	}
	return inventory, inventoryComplete, nil
}

func (s *Service) syncWorkspacePriorities(
	ctx context.Context,
	session upstream.Session,
	userID string,
	adminAccountID string,
	inventory map[string]*priorityTargetInventory,
	inventoryComplete bool,
	healthStates []ConnectionHealthState,
	healthEvents []ConnectionHealthEvent,
	syncStates []PrioritySyncState,
) []AutomaticDisableEvent {
	automaticDisableEvents := make([]AutomaticDisableEvent, 0)
	statesByTarget := make(map[string][]ConnectionHealthState)
	for _, state := range healthStates {
		if _, isTarget := parseTargetID(state.ConnectionID); isTarget {
			statesByTarget[state.ConnectionID] = append(statesByTarget[state.ConnectionID], state)
		}
	}

	managed := make(map[string]*priorityTargetInventory)
	for targetID, item := range inventory {
		if !hasManagedPriorityPolicy(item.policies) {
			continue
		}
		managed[targetID] = item
	}

	storedByTarget := make(map[string]PrioritySyncState, len(syncStates))
	for _, state := range syncStates {
		storedByTarget[state.TargetID] = state
	}

	candidates := make([]managedPriorityCandidate, 0, len(managed))
	for targetID, item := range managed {
		if stored, exists := storedByTarget[targetID]; exists && stored.Conflict && item.currentPriority != stored.LastAppliedPriority {
			continue
		}
		activeModels := make(map[string]struct{})
		for _, spec := range candidateModelSpecs(item.target.Models, item.policies) {
			// Priority ranking consumes persisted probe state regardless of whether
			// Auto Degrade is enabled. A paused or degraded target must never be
			// scored as healthy merely because remote actions are disabled.
			activeModels[spec.modelName] = struct{}{}
		}

		activeStates := make([]ConnectionHealthState, 0, len(activeModels))
		expectedModels := len(activeModels)
		// A Sub2API multiplier-only policy deliberately does not monitor health.
		// Do not let stale probe states from a previous policy pin currently
		// schedulable accounts at the reserved last slot. If a health-probe policy
		// is also assigned, its persisted states remain relevant to the ranking.
		if hasMultiplierOnlyPolicy(item.policies) &&
			(session.Platform != upstream.PlatformSub2API || hasEnabledProbePolicy(item.policies)) {
			// Multiplier-only mode does not probe, but it must still use health
			// states already persisted by another policy or an earlier probe run.
			// Priority is account/channel-wide, so a blocked model is enough to
			// move the upstream target to the reserved last slot.
			activeStates = append(activeStates, statesByTarget[item.target.TargetID]...)
			expectedModels = len(activeStates)
		} else {
			for _, state := range statesByTarget[item.target.TargetID] {
				if _, active := activeModels[state.ModelName]; active {
					activeStates = append(activeStates, state)
				}
			}
		}
		probeP95 := transitHubP95SuccessfulLatency(healthEvents, item.target.TargetID, activeModels, time.Now())
		candidates = append(candidates, managedPriorityCandidate{
			targetID:           targetID,
			groupID:            item.groupID,
			currentPriority:    item.currentPriority,
			upstreamMultiplier: item.upstreamMultiplier,
			latencyMs:          preferredTransitHubLatency(item.account.UsageP95FirstTokenMs, probeP95),
			schedulerScore:     item.account.SchedulerScore,
			currentConcurrency: item.account.CurrentConcurrency,
			concurrency:        item.account.Concurrency,
			priorityMode:       priorityModeForPolicies(item.policies),
			priorityStrategy:   priorityStrategyForPolicies(item.policies),
			states:             activeStates,
			expectedModels:     expectedModels,
			schedulable:        item.account.Schedulable,
			runtimeBlocked:     sub2APIAccountRuntimeBlocked(item.account, time.Now()),
		})
	}
	desiredByTarget := make(map[string]int, len(candidates))
	activeAccountsByGroup := activePriorityCandidatesByGroup(candidates)
	if session.Platform == upstream.PlatformSub2API {
		candidatesByGroup := make(map[string][]managedPriorityCandidate)
		for _, candidate := range candidates {
			candidatesByGroup[candidate.groupID] = append(candidatesByGroup[candidate.groupID], candidate)
		}
		for _, groupCandidates := range candidatesByGroup {
			for targetID, priority := range desiredTransitHubPriorities(session.Platform, groupCandidates) {
				desiredByTarget[targetID] = priority
			}
		}
	} else {
		desiredByTarget = desiredTransitHubPriorities(session.Platform, candidates)
	}

	for targetID, item := range managed {
		multiplier := 0.0
		if item.upstreamMultiplier != nil {
			multiplier = *item.upstreamMultiplier
		}
		desired := desiredByTarget[targetID]
		stored, exists := storedByTarget[targetID]
		if !exists {
			stored = PrioritySyncState{
				UserID: userID, AdminAccountID: adminAccountID, TargetID: targetID,
				OriginalPriority: item.currentPriority, LastAppliedPriority: item.currentPriority,
			}
		}
		if stored.Conflict {
			// A conflict may have been recorded from a stale/global priority
			// response. If the current group priority now matches our last
			// successful write, the operator has not changed the managed value;
			// reclaim it and let the normal reconciliation path continue.
			if item.currentPriority != stored.LastAppliedPriority {
				continue
			}
			stored.Conflict = false
			stored.LastConflictPriority = nil
		}
		if stored.PendingPriority != nil && item.currentPriority == *stored.PendingPriority {
			stored.LastAppliedPriority = *stored.PendingPriority
			stored.PendingPriority = nil
		}
		if exists && item.currentPriority != stored.LastAppliedPriority && stored.PendingPriority == nil {
			current := item.currentPriority
			stored.Conflict = true
			stored.LastConflictPriority = &current
			stored.EffectiveMultiplier = multiplier
			if err := s.repo.UpsertPrioritySyncState(ctx, stored); err != nil {
				log.Printf("[connection-health] priority conflict state save failed target_id=%s err=%v", targetID, err)
			}
			continue
		}
		if exists && stored.PendingPriority != nil && item.currentPriority != stored.LastAppliedPriority {
			current := item.currentPriority
			stored.Conflict = true
			stored.PendingPriority = nil
			stored.LastConflictPriority = &current
			stored.EffectiveMultiplier = multiplier
			if err := s.repo.UpsertPrioritySyncState(ctx, stored); err != nil {
				log.Printf("[connection-health] priority pending conflict state save failed target_id=%s err=%v", targetID, err)
			}
			continue
		}
		var automaticDisableEvent *AutomaticDisableEvent
		if item.currentPriority != desired {
			priorityWasAutomaticallyDisabled := session.Platform == upstream.PlatformSub2API &&
				item.currentPriority != 10000 && desired == 10000 &&
				!isSub2APIManuallyDisabled(item.target)
			pending := desired
			stored.PendingPriority = &pending
			stored.EffectiveMultiplier = multiplier
			if err := s.repo.UpsertPrioritySyncState(ctx, stored); err != nil {
				log.Printf("[connection-health] priority sync intent save failed target_id=%s err=%v", targetID, err)
				continue
			}
			if err := s.priorityActions.UpdateAdminTargetGroupPriority(session, item.groupID, item.target.AccountID, desired); err != nil {
				log.Printf("[connection-health] priority sync update failed target_id=%s err=%v", targetID, err)
				continue
			}
			// Defer the notification until the final state save succeeds. That
			// state is the deduplication checkpoint for later scheduler ticks.
			if priorityWasAutomaticallyDisabled {
				event := AutomaticDisableEvent{
					UserID: userID, AdminAccountID: adminAccountID, Platform: string(session.Platform),
					GroupID: item.groupID, GroupName: item.target.AdminGroupName,
					AccountID: item.target.AccountID, AccountName: item.target.AccountName,
					PreviousPriority: item.currentPriority, CurrentPriority: desired,
					EffectiveMultiplier: multiplier,
					ActiveAccountCount:  activeAccountsByGroup[item.groupID],
					RecentUsageSamples:  item.account.UsageSampleCount,
					Reason:              automaticPriorityDisableReason(candidateForTarget(candidates, targetID)),
				}
				automaticDisableEvent = &event
			}
		}
		stored.LastAppliedPriority = desired
		stored.PendingPriority = nil
		stored.EffectiveMultiplier = multiplier
		stored.Conflict = false
		stored.LastConflictPriority = nil
		if err := s.repo.UpsertPrioritySyncState(ctx, stored); err != nil {
			log.Printf("[connection-health] priority sync state save failed target_id=%s err=%v", targetID, err)
			continue
		}
		if automaticDisableEvent != nil {
			automaticDisableEvents = append(automaticDisableEvents, *automaticDisableEvent)
		}
	}

	// 不再被任何倍率策略覆盖的目标恢复接管前优先级。若管理员已经人工改过，则保留人工值。
	for targetID, stored := range storedByTarget {
		if _, stillManaged := managed[targetID]; stillManaged {
			continue
		}
		item := inventory[targetID]
		if item == nil {
			if !inventoryComplete {
				// 分组读取失败时无法证明目标已经消失，保留当前优先级和同步快照，
				// 等下一次完整扫描再决定是否恢复。
				continue
			}
			if stored.Conflict {
				// 已确认目标不再受策略管理，但人工修改过的值不能被原始快照覆盖。
				if err := s.repo.DeletePrioritySyncState(ctx, userID, adminAccountID, targetID); err != nil {
					log.Printf("[connection-health] missing conflicted target priority state delete failed target_id=%s err=%v", targetID, err)
				}
				continue
			}
			_, rawTargetID, grouped := parsePrioritySyncTargetID(targetID)
			parsed, ok := parseTargetID(rawTargetID)
			if !ok || parsed.adminAccountID != adminAccountID || parsed.platform != string(session.Platform) {
				continue
			}
			if grouped {
				// The account is no longer a member of this successfully loaded
				// group, so its group-priority binding no longer exists to restore.
				if err := s.repo.DeletePrioritySyncState(ctx, userID, adminAccountID, targetID); err != nil {
					log.Printf("[connection-health] missing target group priority state delete failed target_id=%s err=%v", targetID, err)
				}
				continue
			}
			legacyManualChange := false
			if session.Platform == upstream.PlatformSub2API {
				for _, candidate := range inventory {
					if candidate.target.TargetID != rawTargetID || candidate.account.Priority == nil {
						continue
					}
					if *candidate.account.Priority != stored.LastAppliedPriority {
						legacyManualChange = true
						break
					}
					break
				}
			}
			if legacyManualChange {
				if err := s.repo.DeletePrioritySyncState(ctx, userID, adminAccountID, targetID); err != nil {
					log.Printf("[connection-health] legacy priority state delete failed target_id=%s err=%v", targetID, err)
				}
				continue
			}
			pending := stored.OriginalPriority
			stored.PendingPriority = &pending
			if err := s.repo.UpsertPrioritySyncState(ctx, stored); err != nil {
				log.Printf("[connection-health] missing target priority restore intent save failed target_id=%s err=%v", targetID, err)
				continue
			}
			if err := s.priorityActions.UpdateAdminTargetPriority(session, parsed.accountID, stored.OriginalPriority); err != nil {
				log.Printf("[connection-health] missing target priority restore failed target_id=%s err=%v", targetID, err)
				continue
			}
			if err := s.repo.DeletePrioritySyncState(ctx, userID, adminAccountID, targetID); err != nil {
				log.Printf("[connection-health] missing target priority state delete failed target_id=%s err=%v", targetID, err)
			}
			continue
		}
		if stored.PendingPriority != nil && item.currentPriority == *stored.PendingPriority {
			stored.LastAppliedPriority = *stored.PendingPriority
			stored.PendingPriority = nil
		}
		if !stored.Conflict && item.currentPriority == stored.LastAppliedPriority && item.currentPriority != stored.OriginalPriority {
			pending := stored.OriginalPriority
			stored.PendingPriority = &pending
			if err := s.repo.UpsertPrioritySyncState(ctx, stored); err != nil {
				log.Printf("[connection-health] priority restore intent save failed target_id=%s err=%v", targetID, err)
				continue
			}
			if err := s.priorityActions.UpdateAdminTargetGroupPriority(session, item.groupID, item.target.AccountID, stored.OriginalPriority); err != nil {
				log.Printf("[connection-health] priority restore failed target_id=%s err=%v", targetID, err)
				continue
			}
		}
		if err := s.repo.DeletePrioritySyncState(ctx, userID, adminAccountID, targetID); err != nil {
			log.Printf("[connection-health] priority sync state delete failed target_id=%s err=%v", targetID, err)
		}
	}
	return automaticDisableEvents
}

func aggregateAutomaticDisableEvents(events []AutomaticDisableEvent) []AutomaticDisableEvent {
	byAccount := make(map[string]*AutomaticDisableEvent, len(events))
	keys := make([]string, 0, len(events))
	for _, event := range events {
		key := event.UserID + "\x00" + event.AdminAccountID + "\x00" + event.Platform + "\x00" + event.AccountID
		aggregated, exists := byAccount[key]
		if !exists {
			copyEvent := event
			copyEvent.Groups = nil
			byAccount[key] = &copyEvent
			keys = append(keys, key)
			aggregated = &copyEvent
		}
		aggregated.Groups = append(aggregated.Groups, AutomaticDisableGroup{
			GroupID: event.GroupID, GroupName: event.GroupName,
			PreviousPriority: event.PreviousPriority, CurrentPriority: event.CurrentPriority,
			EffectiveMultiplier: event.EffectiveMultiplier, ActiveAccountCount: event.ActiveAccountCount,
		})
	}
	sort.Strings(keys)
	result := make([]AutomaticDisableEvent, 0, len(keys))
	for _, key := range keys {
		event := *byAccount[key]
		sort.Slice(event.Groups, func(i, j int) bool {
			if event.Groups[i].GroupName == event.Groups[j].GroupName {
				return event.Groups[i].GroupID < event.Groups[j].GroupID
			}
			return event.Groups[i].GroupName < event.Groups[j].GroupName
		})
		result = append(result, event)
	}
	return result
}

// activePriorityCandidatesByGroup counts only targets that remain schedulable
// under the same observations used for priority assignment. This prevents an
// alert from calling a paused, runtime-limited, or health-blocked account alive.
func activePriorityCandidatesByGroup(candidates []managedPriorityCandidate) map[string]int {
	counts := make(map[string]int)
	for _, candidate := range candidates {
		if !managedPriorityHardBlocked(candidate) {
			counts[candidate.groupID]++
		}
	}
	return counts
}

func candidateForTarget(candidates []managedPriorityCandidate, targetID string) managedPriorityCandidate {
	for _, candidate := range candidates {
		if candidate.targetID == targetID {
			return candidate
		}
	}
	return managedPriorityCandidate{}
}

func automaticPriorityDisableReason(candidate managedPriorityCandidate) string {
	if candidate.runtimeBlocked {
		return "upstream runtime limited"
	}
	if candidate.schedulable != nil && !*candidate.schedulable {
		return "upstream marked unschedulable"
	}
	for _, state := range candidate.states {
		if state.State == StateDisabled || state.State == StateSuspended || state.CurrentWeight <= 0 {
			return "health policy marked unavailable"
		}
	}
	return "unavailable"
}

func preferredTransitHubLatency(sub2APIUsageP95, transitHubProbeP95 *int) *int {
	if sub2APIUsageP95 != nil {
		value := *sub2APIUsageP95
		return &value
	}
	if transitHubProbeP95 != nil {
		value := *transitHubProbeP95
		return &value
	}
	return nil
}

// desiredTransitHubPriorities ports Sub2API Auto Group's price/speed scoring
// to TransitHub account/channel candidates. Health controls admission, while
// historical upstream priority remains conflict metadata and never feeds the score.
func desiredTransitHubPriorities(platform upstream.Platform, candidates []managedPriorityCandidate) map[string]int {
	desired := make(map[string]int, len(candidates))
	if len(candidates) == 0 {
		return desired
	}

	usable := make([]managedPriorityCandidate, 0, len(candidates))
	blocked := make([]managedPriorityCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if managedPriorityHardBlocked(candidate) {
			blocked = append(blocked, candidate)
		} else {
			usable = append(usable, candidate)
		}
	}
	mode, strategy := prioritySettingsForCandidates(usable)
	lowestMultiplier := lowestKnownMultiplier(usable)
	sort.SliceStable(usable, func(i, j int) bool {
		left, right := usable[i], usable[j]
		if mode == PriorityModeAuto {
			leftScore := transitHubAutoGroupScore(left, lowestMultiplier, strategy)
			rightScore := transitHubAutoGroupScore(right, lowestMultiplier, strategy)
			if leftScore != rightScore {
				return leftScore > rightScore
			}
		} else if result := compareMultiplier(left.upstreamMultiplier, right.upstreamMultiplier); result != 0 {
			return result < 0
		}
		if result := compareFloatDescending(left.schedulerScore, right.schedulerScore); result != 0 {
			return result < 0
		}
		if result := compareHeadroom(left, right); result != 0 {
			return result < 0
		}
		if leftHealth, rightHealth := transitHubHealthTier(left), transitHubHealthTier(right); leftHealth != rightHealth {
			return leftHealth < rightHealth
		}
		if result := compareMultiplier(left.upstreamMultiplier, right.upstreamMultiplier); result != 0 {
			return result < 0
		}
		if result := compareLatency(left.latencyMs, right.latencyMs); result != 0 {
			return result < 0
		}
		return left.targetID < right.targetID
	})

	const (
		priorityStep   = 100
		blockedSub2API = 10000
		blockedNewAPI  = 1
	)
	for index, candidate := range usable {
		if platform == upstream.PlatformSub2API {
			desired[candidate.targetID] = priorityStep * (index + 1)
		} else {
			desired[candidate.targetID] = blockedSub2API - priorityStep*(index+1)
		}
	}
	for _, candidate := range blocked {
		if platform == upstream.PlatformSub2API {
			desired[candidate.targetID] = blockedSub2API
		} else {
			desired[candidate.targetID] = blockedNewAPI
		}
	}
	return desired
}

const (
	transitHubAutoMetricWindow       = time.Hour
	transitHubAutoSampleLimit        = 20
	transitHubAutoMinReliableSamples = 3
	transitHubAutoMaxLatencyMs       = 30_000
)

func prioritySettingsForCandidates(candidates []managedPriorityCandidate) (string, string) {
	strategy := ""
	for _, candidate := range candidates {
		if candidate.priorityMode != PriorityModeAuto {
			continue
		}
		candidateStrategy := normalizePriorityStrategy(candidate.priorityStrategy)
		if strategy == "" {
			strategy = candidateStrategy
			continue
		}
		if strategy != candidateStrategy {
			// A group has only one ordered priority list. Conflicting policies
			// cannot be compared coherently, so use the profit-safe default.
			return PriorityModeAuto, PriorityStrategyPrice
		}
	}
	if strategy != "" {
		return PriorityModeAuto, strategy
	}
	return PriorityModeMultiplier, PriorityStrategyPrice
}

func lowestKnownMultiplier(candidates []managedPriorityCandidate) *float64 {
	var lowest *float64
	for _, candidate := range candidates {
		if candidate.upstreamMultiplier == nil || *candidate.upstreamMultiplier <= 0 {
			continue
		}
		if lowest == nil || *candidate.upstreamMultiplier < *lowest {
			value := *candidate.upstreamMultiplier
			lowest = &value
		}
	}
	return lowest
}

func transitHubAutoGroupScore(candidate managedPriorityCandidate, lowestMultiplier *float64, strategy string) float64 {
	priceScore := 0.0
	if lowestMultiplier != nil && candidate.upstreamMultiplier != nil && *candidate.upstreamMultiplier > 0 {
		priceScore = clampTransitHubAutoScore(*lowestMultiplier / *candidate.upstreamMultiplier)
	}
	speedScore := 0.0
	if candidate.latencyMs != nil {
		speedScore = clampTransitHubAutoScore(float64(transitHubAutoMaxLatencyMs-*candidate.latencyMs) / transitHubAutoMaxLatencyMs)
	}
	switch normalizePriorityStrategy(strategy) {
	case PriorityStrategySpeed:
		return speedScore
	case PriorityStrategyBalanced:
		return 0.70*priceScore + 0.30*speedScore
	default:
		return priceScore
	}
}

func clampTransitHubAutoScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func compareMultiplier(left, right *float64) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return 1
	}
	if right == nil {
		return -1
	}
	if *left < *right {
		return -1
	}
	if *left > *right {
		return 1
	}
	return 0
}

func compareLatency(left, right *int) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return 1
	}
	if right == nil {
		return -1
	}
	if *left < *right {
		return -1
	}
	if *left > *right {
		return 1
	}
	return 0
}

func compareFloatDescending(left, right *float64) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return 1
	}
	if right == nil {
		return -1
	}
	if *left > *right {
		return -1
	}
	if *left < *right {
		return 1
	}
	return 0
}

func compareHeadroom(left, right managedPriorityCandidate) int {
	leftHeadroom, leftKnown := transitHubConcurrencyHeadroom(left)
	rightHeadroom, rightKnown := transitHubConcurrencyHeadroom(right)
	if !leftKnown && !rightKnown {
		return 0
	}
	if !leftKnown {
		return 1
	}
	if !rightKnown {
		return -1
	}
	if leftHeadroom > rightHeadroom {
		return -1
	}
	if leftHeadroom < rightHeadroom {
		return 1
	}
	return 0
}

func transitHubConcurrencyHeadroom(candidate managedPriorityCandidate) (float64, bool) {
	if candidate.concurrency == nil || candidate.currentConcurrency == nil || *candidate.concurrency <= 0 {
		return 0, false
	}
	return clampTransitHubAutoScore(1 - float64(*candidate.currentConcurrency)/float64(*candidate.concurrency)), true
}

func transitHubSuccessfulLatency(states []ConnectionHealthState) *int {
	var best *int
	for _, state := range states {
		if state.LastLatencyMs == nil || *state.LastLatencyMs < 0 || state.LastErrorKey != "" {
			continue
		}
		if state.State != StateHealthy && state.State != StateRecovering {
			continue
		}
		if best == nil || *state.LastLatencyMs < *best {
			value := *state.LastLatencyMs
			best = &value
		}
	}
	return best
}

func transitHubP95SuccessfulLatency(events []ConnectionHealthEvent, targetID string, activeModels map[string]struct{}, now time.Time) *int {
	samples := make([]int, 0, transitHubAutoSampleLimit)
	cutoff := now.Add(-transitHubAutoMetricWindow)
	for _, event := range events {
		if event.ConnectionID != targetID || event.Result != string(ResultOK) || event.LatencyMs == nil || *event.LatencyMs < 0 || event.CreatedAt.Before(cutoff) {
			continue
		}
		if len(activeModels) > 0 {
			if _, ok := activeModels[event.ModelName]; !ok {
				continue
			}
		}
		samples = append(samples, *event.LatencyMs)
		if len(samples) == transitHubAutoSampleLimit {
			break
		}
	}
	if len(samples) < transitHubAutoMinReliableSamples {
		return nil
	}
	sort.Ints(samples)
	index := (95*len(samples) + 99) / 100
	value := samples[index-1]
	return &value
}

func transitHubHealthTier(candidate managedPriorityCandidate) int {
	if candidate.expectedModels > 0 && len(candidate.states) < candidate.expectedModels {
		return 4
	}
	tier := 0
	for _, state := range candidate.states {
		current := 0
		switch state.State {
		case StateRecovering:
			current = 1
		case StateDegraded:
			current = 2
		case StateObserving:
			current = 3
		}
		if current > tier {
			tier = current
		}
	}
	return tier
}

func managedPriorityHardBlocked(candidate managedPriorityCandidate) bool {
	if candidate.runtimeBlocked || (candidate.schedulable != nil && !*candidate.schedulable) {
		return true
	}
	for _, state := range candidate.states {
		if state.State == StateDisabled || state.State == StateSuspended || state.CurrentWeight <= 0 {
			return true
		}
	}
	return false
}

func sub2APIAccountRuntimeBlocked(account upstream.AdminGroupAccountInfo, now time.Time) bool {
	target := AdminProbeTarget{Platform: string(upstream.PlatformSub2API), AccountStatus: account.Status}
	applySub2APIRuntimeState(&target, account)
	return isSub2APIRuntimeBlocked(target, now)
}

func isManagedPriorityMode(mode string) bool {
	normalized := normalizePriorityMode(mode)
	return normalized == PriorityModeMultiplier || normalized == PriorityModeAuto
}

func priorityModeForPolicies(policies []Policy) string {
	mode := PriorityModeNone
	for _, policy := range policies {
		if !policy.Enabled {
			continue
		}
		switch normalizePriorityMode(policy.PriorityMode) {
		case PriorityModeAuto:
			return PriorityModeAuto
		case PriorityModeMultiplier:
			mode = PriorityModeMultiplier
		}
	}
	return mode
}

func priorityStrategyForPolicies(policies []Policy) string {
	strategy := ""
	for _, policy := range policies {
		if !policy.Enabled || normalizePriorityMode(policy.PriorityMode) != PriorityModeAuto {
			continue
		}
		candidate := normalizePriorityStrategy(policy.PriorityStrategy)
		if strategy == "" {
			strategy = candidate
			continue
		}
		if strategy != candidate {
			// A group has one ordered priority list. If assigned auto policies
			// disagree, use the profit-safe low-price default deterministically.
			return PriorityStrategyPrice
		}
	}
	if strategy != "" {
		return strategy
	}
	return PriorityStrategyPrice
}

func hasManagedPriorityPolicy(policies []Policy) bool {
	for _, policy := range policies {
		if policy.Enabled && isManagedPriorityMode(policy.PriorityMode) {
			return true
		}
	}
	return false
}

// hasMultiplierOnlyPolicy 让明确的仅倍率策略成为同一目标的优先级依据。即使目标还叠加了
// 一条负责记录健康状态的探活策略，健康状态也不会重新参与 priority 排名。
func hasMultiplierOnlyPolicy(policies []Policy) bool {
	for _, policy := range policies {
		if policy.Enabled && normalizeStrategyMode(policy.StrategyMode) == StrategyModeMultiplierOnly {
			return true
		}
	}
	return false
}
