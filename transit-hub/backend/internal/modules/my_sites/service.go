package my_sites

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"transithub/backend/internal/modules/purity_check"
	"transithub/backend/internal/modules/upstream"
)

// StateRepository 分组映射状态的持久化接口，由 Repository 实现。
type StateRepository interface {
	Get(ctx context.Context, userID string, adminAccountID string) (*State, error)
	Save(ctx context.Context, state State) error
}

type TransactionalStateRepository interface {
	MutateState(ctx context.Context, userID string, adminAccountID string, mutate StateMutation) (*State, error)
}

// RealConnectionRepository 真实对接绑定记录的持久化接口。
type RealConnectionRepository interface {
	SaveRealConnection(ctx context.Context, conn RealConnection) error
	ListRealConnections(ctx context.Context, userID string, adminAccountID string) ([]RealConnection, error)
	GetRealConnection(ctx context.Context, id string, userID string, adminAccountID string) (*RealConnection, error)
	DeleteRealConnection(ctx context.Context, id string, userID string, adminAccountID string) error
}

type AtomicRealDisconnectRepository interface {
	RemoveUpstreamMappingAndDeleteConnection(ctx context.Context, userID string, adminAccountID string, connectionID string, siteID string, groupName string) error
}

// AtomicRealConnectionRepository is implemented by the PostgreSQL repository.
// Keeping it optional preserves lightweight test repositories and rolling code
// paths while production gets one local transaction for connection + pricing.
type AtomicRealConnectionRepository interface {
	SaveRealConnectionWithPricingMapping(ctx context.Context, conn RealConnection) error
}

type IdempotentRealConnectionRepository interface {
	GetRealConnectionByOperationID(ctx context.Context, userID string, adminAccountID string, operationID string) (*RealConnection, error)
}

type ScopedRealDisconnectRepository interface {
	DeleteRealConnectionWithPricingMapping(ctx context.Context, conn RealConnection, removePricingMapping bool) error
}

// UpstreamSiteLookup 根据 ID 获取上游站点信息（含 Session），供真实对接流程使用。
type UpstreamSiteLookup interface {
	GetSite(ctx context.Context, siteID string) (*upstream.Site, error)
}

// BotNotifier 机器人通知发送接口，由 settings.Service 实现。
// 自动调价成功后通过此接口向用户配置的机器人发送通知。
type BotNotifier interface {
	SendToBots(ctx context.Context, userID string, botIDs []string, message string)
}

// PurityIssueReader keeps the pricing-mapping module independent from detector
// internals: it only receives account ids and a compact current issue summary.
type PurityIssueReader interface {
	LatestAccountIssues(ctx context.Context, userID string, adminAccountID string, accountIDs []string) ([]purity_check.AccountIssue, error)
}

// Service 负责分组映射的查询与保存，以及真实对接的编排。
// 供仪表盘分组弹窗和分组倍率页面复用。
type Service struct {
	repository      StateRepository
	connRepository  RealConnectionRepository
	platformService *upstream.PlatformService
	upstreamLookup  UpstreamSiteLookup
	botNotifier     BotNotifier
	accounts        AdminAccountResolver
	purityIssues    PurityIssueReader
	// keyTester 提供上游 Key 连通性测试。由 httpserver 注入，为 nil 时
	// 只有测试接口不可用，其余功能不受影响。
	keyTester UpstreamKeyTester
}

type AdminAccountResolver interface {
	RequireCurrentID(ctx context.Context, userID string) (string, error)
}

func NewService(repository StateRepository, platformService *upstream.PlatformService, upstreamLookup UpstreamSiteLookup) *Service {
	return &Service{repository: repository, platformService: platformService, upstreamLookup: upstreamLookup}
}

func (s *Service) EnsureSchema(ctx context.Context) error {
	if repo, ok := s.repository.(*Repository); ok {
		s.connRepository = repo
		return repo.EnsureSchema(ctx)
	}
	return nil
}

// UpstreamGroupKey 拼出「某站点的某个上游分组」的唯一标识。
// 倍率变更预警和分组映射两处必须用同一套拼法，否则过滤会静默失效。
func UpstreamGroupKey(siteID, groupName string) string {
	return siteID + "|" + strings.TrimSpace(groupName)
}

// ListMappedUpstreamGroups 返回当前工作区被分组映射真正引用的上游分组集合。
// 倍率变更预警靠它区分「对接了的」和「上游站点上碰巧存在的」——
// 后者变动与本方定价无关，报出来只会变成噪音。
func (s *Service) ListMappedUpstreamGroups(ctx context.Context, userID, adminAccountID string) (map[string]struct{}, error) {
	state, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil {
		return nil, err
	}
	mapped := make(map[string]struct{})
	if state == nil {
		return mapped, nil
	}
	for _, mapping := range state.Mappings {
		for _, target := range mapping.UpstreamTargets {
			mapped[UpstreamGroupKey(target.SiteID, target.GroupName)] = struct{}{}
		}
	}
	return mapped, nil
}

// SetBotNotifier 注入机器人通知发送能力，供自动调价成功后发送通知。
func (s *Service) SetBotNotifier(notifier BotNotifier) {
	s.botNotifier = notifier
}

// SetPurityIssueReader supplies the compact account-level status displayed on
// mapping targets. It is optional so existing tests and rolling deployments
// keep working when the purity module is not configured.
func (s *Service) SetPurityIssueReader(reader PurityIssueReader) {
	s.purityIssues = reader
}

func (s *Service) SetAdminAccountResolver(accounts AdminAccountResolver) {
	s.accounts = accounts
}

// TargetAccountCostRange reads account_cost from this workspace's own Sub2API
// instance. It deliberately does not query a remote upstream site's
// actual_cost: that figure is the upstream's user charge, not our purchase.
// 归集不到成本的目标一律进 Unresolved，绝不静默丢弃——
// 少算了谁必须能在简报里看见，否则总成本偏低而无人察觉。
func (s *Service) TargetAccountCostRange(ctx context.Context, userID, adminAccountID, startDate, endDate string) AccountCostResult {
	var result AccountCostResult

	state, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil || state == nil || state.Session.Platform != upstream.PlatformSub2API || !state.Session.IsAuthenticated() {
		return result
	}
	groups, err := s.platformService.FetchAdminAllGroups(state.Session)
	if err != nil {
		log.Printf("[daily-report] 读取自有分组失败: %v", err)
		return result
	}
	groupIDs := make(map[string]string, len(groups))
	for _, group := range groups {
		groupIDs[strings.TrimSpace(group.Name)] = strings.TrimSpace(group.ID)
	}
	var fallbackAccounts map[string]string
	if s.connRepository != nil {
		connections, listErr := s.connRepository.ListRealConnections(ctx, userID, adminAccountID)
		if listErr != nil {
			log.Printf("[daily-report] 读取真实对接记录失败，成本账号回退不可用: %v", listErr)
		} else {
			fallbackAccounts = realConnectionAccountIndex(connections)
		}
	}

	type query struct {
		accountID, groupID string
		ownGroup           string
		targets            []TargetAccountCost
	}
	queries := make(map[string]*query)

	for _, mapping := range state.Mappings {
		ownGroup := strings.TrimSpace(mapping.OwnGroup)
		groupID := groupIDs[ownGroup]
		for _, target := range mapping.UpstreamTargets {
			ref := TargetAccountCost{SiteID: target.SiteID, GroupName: target.GroupName}

			if groupID == "" {
				result.Unresolved = append(result.Unresolved, unresolvedTarget(ownGroup, ref, ReasonGroupMissing))
				continue
			}
			accountID := normalizeSub2APIAccountID(target.Sub2APIAccountID)
			if accountID == nil {
				accountID = fallbackRealConnectionAccount(fallbackAccounts, target.SiteID, target.GroupName)
			}
			if accountID == nil {
				result.Unresolved = append(result.Unresolved, unresolvedTarget(ownGroup, ref, ReasonUnbound))
				continue
			}

			key := *accountID + "|" + groupID
			if existing, ok := queries[key]; ok {
				// 完全相同的目标重复出现不算冲突，只有指向不同上游时才是。
				if !containsTarget(existing.targets, ref) {
					existing.targets = append(existing.targets, ref)
				}
				continue
			}
			queries[key] = &query{
				accountID: *accountID, groupID: groupID,
				ownGroup: ownGroup, targets: []TargetAccountCost{ref},
			}
		}
	}

	result.Costs = make([]TargetAccountCost, 0, len(queries))
	for key, item := range queries {
		// 同一个「账号 + 分组」被多个上游目标引用时归属不明：
		// 把这笔成本算到其中任何一个头上都是错的，全部跳过并报出来。
		if len(item.targets) > 1 {
			log.Printf("[daily-report] 成本账号绑定归属冲突，跳过 account_group=%s 涉及 %d 个上游目标",
				key, len(item.targets))
			for _, ref := range item.targets {
				result.Unresolved = append(result.Unresolved, unresolvedTarget(item.ownGroup, ref, ReasonAmbiguous))
			}
			continue
		}

		cost, err := s.platformService.FetchSub2APIAccountCostRange(state.Session, startDate, endDate, item.accountID, item.groupID)
		if err != nil {
			log.Printf("[daily-report] 读取账号成本失败 account=%s group=%s err=%v", item.accountID, item.groupID, err)
			result.Unresolved = append(result.Unresolved, unresolvedTarget(item.ownGroup, item.targets[0], ReasonQueryFailed))
			continue
		}
		// cost == 0 是「确实没花钱」，属于已归集，不进 Unresolved。
		if cost > 0 {
			ref := item.targets[0]
			ref.CostCNY = cost
			result.Costs = append(result.Costs, ref)
		}
	}
	return result
}

func unresolvedTarget(ownGroup string, ref TargetAccountCost, reason UnresolvedReason) UnresolvedTarget {
	return UnresolvedTarget{
		OwnGroup:  ownGroup,
		SiteID:    ref.SiteID,
		GroupName: ref.GroupName,
		Reason:    reason,
	}
}

func containsTarget(targets []TargetAccountCost, ref TargetAccountCost) bool {
	for _, target := range targets {
		if target.SiteID == ref.SiteID && target.GroupName == ref.GroupName {
			return true
		}
	}
	return false
}

// MappingOptions 获取分组映射选项：自有分组通过 admin 接口拉取全量，上游分组从缓存读取。
// 该查询保持只读：已失效的自有分组和上游目标通过附加字段返回，由用户确认后再修改，
// 避免远端接口偶发返回不完整数据时，仅仅打开页面就永久删除映射和自动调价配置。
func (s *Service) MappingOptions(ctx context.Context, userID string) (MappingOptionsResponse, error) {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return MappingOptionsResponse{}, err
	}
	state, err := s.authenticatedState(ctx, userID, adminAccountID)
	if err != nil {
		return MappingOptionsResponse{}, err
	}
	adminGroups, err := s.platformService.FetchAdminAllGroups(state.Session)
	if err != nil {
		return MappingOptionsResponse{}, err
	}
	// 构造最新自有分组视图。State.OwnGroups 是旧版本留下的缓存字段，本查询不再写回它；
	// 自动调价执行时始终以远端 FetchAdminAllGroups 返回的数据为准。
	freshOwnGroups := make([]GroupOption, 0, len(adminGroups))
	idToName := make(map[string]string, len(adminGroups))
	for _, g := range adminGroups {
		name := strings.TrimSpace(g.Name)
		if name != "" {
			idToName[g.ID] = name
		}
		multiplier := 0.0
		if g.Multiplier != nil {
			multiplier = *g.Multiplier
		}
		freshOwnGroups = append(freshOwnGroups, GroupOption{Name: name, Multiplier: multiplier})
	}
	freshGroupSet := make(map[string]struct{}, len(freshOwnGroups))
	for _, g := range freshOwnGroups {
		if name := strings.TrimSpace(g.Name); name != "" {
			freshGroupSet[name] = struct{}{}
		}
	}

	viewState := cloneStateForMutation(state)
	// Historical rows may contain a missing/null upstreamTargets field. Keep the
	// stored JSON untouched on this read path, but always expose an array so old
	// data cannot crash array-based clients.
	for index := range viewState.Mappings {
		if viewState.Mappings[index].UpstreamTargets == nil {
			viewState.Mappings[index].UpstreamTargets = []UpstreamGroupRef{}
		}
	}
	viewState.OwnGroups = freshOwnGroups
	if s.connRepository != nil {
		backfillConnections, listErr := s.connRepository.ListRealConnections(ctx, userID, adminAccountID)
		if listErr != nil {
			return MappingOptionsResponse{}, listErr
		}
		// 真实对接记录只补偿到本次响应，避免 GET 请求产生持久化副作用。
		applyMappingsFromRealConnections(viewState, idToName, backfillConnections)
	}
	s.applyMappingPurityIssues(ctx, userID, adminAccountID, viewState)

	staleOwnGroups := make([]string, 0)
	staleOwnSeen := make(map[string]struct{})
	for _, mapping := range viewState.Mappings {
		ownGroup := strings.TrimSpace(mapping.OwnGroup)
		if _, exists := freshGroupSet[ownGroup]; exists || ownGroup == "" {
			continue
		}
		if _, exists := staleOwnSeen[ownGroup]; exists {
			continue
		}
		staleOwnSeen[ownGroup] = struct{}{}
		staleOwnGroups = append(staleOwnGroups, ownGroup)
	}
	sort.Strings(staleOwnGroups)

	liveTargetMultipliers := s.liveMappingTargetMultipliers(ctx, userID, adminAccountID, viewState.Mappings)
	missingTargetKeys := make(map[string]struct{})
	for _, target := range liveTargetMultipliers {
		if target.Stale {
			missingTargetKeys[targetKey(target.SiteID, target.GroupName)] = struct{}{}
		}
	}
	staleTargets := make([]UpstreamGroupRef, 0, len(missingTargetKeys))
	staleTargetSeen := make(map[string]struct{}, len(missingTargetKeys))
	for _, mapping := range viewState.Mappings {
		for _, target := range mapping.UpstreamTargets {
			key := targetKey(target.SiteID, target.GroupName)
			if _, missing := missingTargetKeys[key]; !missing {
				continue
			}
			if _, exists := staleTargetSeen[key]; exists {
				continue
			}
			staleTargetSeen[key] = struct{}{}
			staleTargets = append(staleTargets, target)
		}
	}
	sort.Slice(staleTargets, func(i, j int) bool {
		if staleTargets[i].SiteID == staleTargets[j].SiteID {
			return staleTargets[i].GroupName < staleTargets[j].GroupName
		}
		return staleTargets[i].SiteID < staleTargets[j].SiteID
	})

	groups := make([]MappingOwnGroupOption, 0, len(adminGroups))
	for _, g := range adminGroups {
		name := strings.TrimSpace(g.Name)
		if name != "" {
			multiplier := 0.0
			if g.Multiplier != nil {
				multiplier = *g.Multiplier
			}
			groups = append(groups, MappingOwnGroupOption{
				ID:               g.ID,
				SiteName:         viewState.Email,
				GroupName:        name,
				Multiplier:       multiplier,
				Platform:         g.Platform,
				Status:           g.Status,
				IsExclusive:      g.IsExclusive,
				SubscriptionType: g.SubscriptionType,
			})
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].SiteName == groups[j].SiteName {
			return groups[i].GroupName < groups[j].GroupName
		}
		return groups[i].SiteName < groups[j].SiteName
	})
	return MappingOptionsResponse{
		OwnGroups:                 groups,
		Mappings:                  viewState.Mappings,
		UpstreamTargetMultipliers: liveTargetMultipliers,
		StaleOwnGroups:            staleOwnGroups,
		StaleTargets:              staleTargets,
		ConnectionCapabilities:    connectionCapabilities(viewState.Session.Platform),
		CostAccounts:              s.mappingCostAccounts(viewState.Session),
	}, nil
}

func (s *Service) applyMappingPurityIssues(ctx context.Context, userID string, adminAccountID string, state *State) {
	if s.purityIssues == nil || state == nil {
		return
	}
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, mapping := range state.Mappings {
		for _, target := range mapping.UpstreamTargets {
			accountID := normalizeSub2APIAccountID(target.Sub2APIAccountID)
			if accountID == nil {
				continue
			}
			if _, exists := seen[*accountID]; !exists {
				seen[*accountID] = struct{}{}
				ids = append(ids, *accountID)
			}
		}
	}
	issues, err := s.purityIssues.LatestAccountIssues(ctx, userID, adminAccountID, ids)
	if err != nil {
		// A purity-status lookup must never make normal pricing configuration
		// unavailable. The next page refresh will retry it.
		log.Printf("[my-sites] read purity issue summary failed: %v", err)
		return
	}
	byAccount := make(map[string]purity_check.AccountIssue, len(issues))
	for _, issue := range issues {
		byAccount[issue.AccountID] = issue
	}
	for mappingIndex := range state.Mappings {
		for targetIndex := range state.Mappings[mappingIndex].UpstreamTargets {
			target := &state.Mappings[mappingIndex].UpstreamTargets[targetIndex]
			target.PurityIssue = nil
			accountID := normalizeSub2APIAccountID(target.Sub2APIAccountID)
			if accountID == nil {
				continue
			}
			if issue, exists := byAccount[*accountID]; exists {
				target.PurityIssue = &PurityIssue{Kind: issue.Kind, DetectedAt: issue.DetectedAt}
			}
		}
	}
}

// mappingCostAccounts 拉取本方 Sub2API 账号清单，供前端绑定调价数据源与核算成本。
//
// 拉取失败只降级不报错：调价映射页面的主体是分组与上游倍率，账号清单缺失时
// 绑定界面暂时无候选可选，已保存的绑定则按"成本未知"处理并被排除出毛利计算——
// 这比让整个页面 500 要好。非 Sub2API 平台（new-api）没有对应概念，返回空。
func (s *Service) mappingCostAccounts(session upstream.Session) []MappingCostAccount {
	if session.Platform != upstream.PlatformSub2API || !session.IsAuthenticated() {
		return []MappingCostAccount{}
	}
	accounts, err := s.platformService.ListAdminAllAccounts(session)
	if err != nil {
		return []MappingCostAccount{}
	}
	options := make([]MappingCostAccount, 0, len(accounts))
	for _, account := range accounts {
		id := strings.TrimSpace(account.ID)
		if id == "" {
			continue
		}
		options = append(options, MappingCostAccount{
			ID:                 id,
			Name:               strings.TrimSpace(account.Name),
			BaseURL:            strings.TrimSpace(account.BaseURL),
			CostRateMultiplier: account.CostRateMultiplier,
			CostRateSource:     account.CostRateSource,
		})
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].Name == options[j].Name {
			return options[i].ID < options[j].ID
		}
		return options[i].Name < options[j].Name
	})
	return options
}

// SaveMappings 保存用户的分组映射关系，包含自动调价配置。
// 对自动调价字段做基础归一化和校验：
//   - AutoPricingSource 为空时默认 primary_upstream
//   - AutoPricingStrategy 为空时默认 percentage
//   - EnableAutoPricing=true 且 source=primary_upstream 时，主上游必须在 UpstreamTargets 中
//   - MinMultiplier 和 MaxMultiplier 同时设置时必须 min <= max
func (s *Service) SaveMappings(ctx context.Context, userID string, mappings []MappingRequest) (StatusResponse, error) {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return StatusResponse{}, err
	}
	state, err := s.authenticatedState(ctx, userID, adminAccountID)
	if err != nil {
		return StatusResponse{}, err
	}
	next := make([]GroupMapping, 0, len(mappings))
	for _, mapping := range mappings {
		groupMapping, include, normalizeErr := normalizeMappingRequest(mapping)
		if normalizeErr != nil {
			return StatusResponse{}, normalizeErr
		}
		if !include {
			continue
		}
		next = append(next, groupMapping)
	}
	state, err = s.mutateState(ctx, userID, adminAccountID, func(latest *State) error {
		merged := make([]GroupMapping, len(next))
		for i := range next {
			merged[i] = cloneGroupMappingValue(next[i])
		}
		mergeLastAutoPricingRunByOwnGroup(merged, latest.Mappings)
		latest.Mappings = merged
		return nil
	})
	if err != nil {
		return StatusResponse{}, err
	}
	if state == nil {
		return StatusResponse{}, requestError(ErrorAuthRequired)
	}
	return StatusResponse{Authenticated: true, BaseURL: state.BaseURL, Email: state.Email, Mappings: state.Mappings}, nil
}

// SaveMapping 原子更新单个自有分组，保留同一 workspace 中其他分组的最新映射。
// 该方法与 SaveMappings 共用归一化和校验规则，避免新旧客户端产生不同的数据语义。
func (s *Service) SaveMapping(ctx context.Context, userID string, mapping MappingRequest) (StatusResponse, error) {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return StatusResponse{}, err
	}
	if _, err = s.authenticatedState(ctx, userID, adminAccountID); err != nil {
		return StatusResponse{}, err
	}
	next, include, err := normalizeMappingRequest(mapping)
	if err != nil {
		return StatusResponse{}, err
	}
	if !include {
		return StatusResponse{}, requestError(ErrorRequest)
	}

	state, err := s.mutateState(ctx, userID, adminAccountID, func(latest *State) error {
		index := findMappingIndexByOwnGroup(latest.Mappings, next.OwnGroup)
		if index >= 0 {
			if latest.Mappings[index].LastAutoPricingRun != nil {
				next.LastAutoPricingRun = latest.Mappings[index].LastAutoPricingRun
			}
			latest.Mappings[index] = cloneGroupMappingValue(next)
			return nil
		}
		latest.Mappings = append(latest.Mappings, cloneGroupMappingValue(next))
		return nil
	})
	if err != nil {
		return StatusResponse{}, err
	}
	if state == nil {
		return StatusResponse{}, requestError(ErrorAuthRequired)
	}
	return StatusResponse{Authenticated: true, BaseURL: state.BaseURL, Email: state.Email, Mappings: state.Mappings}, nil
}

// RemoveMapping removes one mapping by normalized own-group name while retaining
// every other mapping and its latest server-owned auto-pricing run state.
func (s *Service) RemoveMapping(ctx context.Context, userID string, ownGroup string) (StatusResponse, error) {
	ownGroup = strings.TrimSpace(ownGroup)
	if ownGroup == "" {
		return StatusResponse{}, requestError(ErrorRequest)
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return StatusResponse{}, err
	}
	if _, err = s.authenticatedState(ctx, userID, adminAccountID); err != nil {
		return StatusResponse{}, err
	}
	state, err := s.mutateState(ctx, userID, adminAccountID, func(latest *State) error {
		index := findMappingIndexByOwnGroup(latest.Mappings, ownGroup)
		if index < 0 {
			return requestError(ErrorRequest)
		}
		latest.Mappings = append(latest.Mappings[:index], latest.Mappings[index+1:]...)
		return nil
	})
	if err != nil {
		return StatusResponse{}, err
	}
	if state == nil {
		return StatusResponse{}, requestError(ErrorAuthRequired)
	}
	return StatusResponse{Authenticated: true, BaseURL: state.BaseURL, Email: state.Email, Mappings: state.Mappings}, nil
}

// CleanupDeletedUpstreamSites removes mapping targets for upstream site
// records that were explicitly deleted from this workspace. It is intentionally
// driven by the site lifecycle, not by a failed sync or an incomplete upstream
// group response, so transient outages cannot erase pricing configuration.
func (s *Service) CleanupDeletedUpstreamSites(ctx context.Context, userID, adminAccountID string, siteIDs []string) error {
	deleted := make(map[string]struct{}, len(siteIDs))
	for _, siteID := range siteIDs {
		if siteID = strings.TrimSpace(siteID); siteID != "" {
			deleted[siteID] = struct{}{}
		}
	}
	if len(deleted) == 0 {
		return nil
	}
	_, err := s.mutateState(ctx, userID, adminAccountID, func(state *State) error {
		if state == nil {
			return nil
		}
		cleaned := make([]GroupMapping, 0, len(state.Mappings))
		for _, mapping := range state.Mappings {
			targets := make([]UpstreamGroupRef, 0, len(mapping.UpstreamTargets))
			for _, target := range mapping.UpstreamTargets {
				if _, remove := deleted[strings.TrimSpace(target.SiteID)]; remove {
					continue
				}
				targets = append(targets, target)
			}
			if len(targets) == 0 {
				continue
			}
			mapping.UpstreamTargets = targets
			cleaned = append(cleaned, mapping)
		}
		state.Mappings = cleaned
		return nil
	})
	return err
}

// CleanupMissingUpstreamSites removes mappings whose site ID is absent from
// the authoritative upstream_sites table. Callers must only pass a successfully
// read site inventory; an empty successful inventory means all site mappings
// are stale and should be removed.
func (s *Service) CleanupMissingUpstreamSites(ctx context.Context, userID, adminAccountID string, liveSiteIDs []string) error {
	live := make(map[string]struct{}, len(liveSiteIDs))
	for _, siteID := range liveSiteIDs {
		if siteID = strings.TrimSpace(siteID); siteID != "" {
			live[siteID] = struct{}{}
		}
	}
	_, err := s.mutateState(ctx, userID, adminAccountID, func(state *State) error {
		if state == nil {
			return nil
		}
		cleaned := make([]GroupMapping, 0, len(state.Mappings))
		for _, mapping := range state.Mappings {
			targets := make([]UpstreamGroupRef, 0, len(mapping.UpstreamTargets))
			for _, target := range mapping.UpstreamTargets {
				if _, exists := live[strings.TrimSpace(target.SiteID)]; !exists {
					continue
				}
				targets = append(targets, target)
			}
			if len(targets) == 0 {
				continue
			}
			mapping.UpstreamTargets = targets
			cleaned = append(cleaned, mapping)
		}
		state.Mappings = cleaned
		return nil
	})
	return err
}

// normalizeMappingRequest applies the stable defaults and validation shared by
// full-array PUT and single-group PATCH. The boolean is false for an empty group name.
func normalizeMappingRequest(mapping MappingRequest) (GroupMapping, bool, error) {
	ownGroup := strings.TrimSpace(mapping.OwnGroup)
	if ownGroup == "" {
		return GroupMapping{}, false, nil
	}
	targets := make([]UpstreamGroupRef, 0, len(mapping.UpstreamTargets))
	seenTargets := make(map[string]struct{}, len(mapping.UpstreamTargets))
	for _, target := range mapping.UpstreamTargets {
		siteID := strings.TrimSpace(target.SiteID)
		groupName := strings.TrimSpace(target.GroupName)
		if siteID == "" || groupName == "" {
			continue
		}
		key := targetKey(siteID, groupName)
		if _, exists := seenTargets[key]; exists {
			continue
		}
		seenTargets[key] = struct{}{}
		targets = append(targets, UpstreamGroupRef{
			SiteID:           siteID,
			GroupName:        groupName,
			Sub2APIAccountID: normalizeSub2APIAccountID(target.Sub2APIAccountID),
		})
	}

	source := strings.TrimSpace(mapping.AutoPricingSource)
	if source == "" {
		source = "primary_upstream"
	}
	strategy := strings.TrimSpace(mapping.AutoPricingStrategy)
	if strategy == "" {
		strategy = "percentage"
	}
	fixedIncrease := floatOrDefault(mapping.FixedIncrease, 0.1)
	percentageIncrease := floatOrDefault(mapping.PercentageIncrease, 10)
	thresholdPercent := floatOrDefault(mapping.AdjustThresholdPercent, 10)
	if fixedIncrease < 0 || percentageIncrease < 0 || thresholdPercent < 0 ||
		(mapping.MinMultiplier != nil && *mapping.MinMultiplier < 0) ||
		(mapping.MaxMultiplier != nil && *mapping.MaxMultiplier < 0) ||
		(mapping.MinMultiplier != nil && mapping.MaxMultiplier != nil && *mapping.MinMultiplier > *mapping.MaxMultiplier) {
		return GroupMapping{}, false, requestError(ErrorInvalidAutoPricingConf)
	}

	primarySiteID := strings.TrimSpace(mapping.PrimaryUpstreamSiteID)
	primaryGroupName := strings.TrimSpace(mapping.PrimaryUpstreamGroupName)
	if mapping.EnableAutoPricing && source == "primary_upstream" {
		found := false
		for _, target := range targets {
			if target.SiteID == primarySiteID && target.GroupName == primaryGroupName {
				found = true
				break
			}
		}
		if primarySiteID == "" || primaryGroupName == "" || !found {
			return GroupMapping{}, false, requestError(ErrorInvalidAutoPricingConf)
		}
	}

	notifyBotIDs := filterEmptyStrings(mapping.AutoPricingNotifyBotIDs)
	if mapping.EnableAutoPricingNotify && len(notifyBotIDs) == 0 {
		return GroupMapping{}, false, requestError(ErrorInvalidAutoPricingConf)
	}
	return GroupMapping{
		OwnGroup:                  ownGroup,
		UpstreamTargets:           targets,
		EnableAutoPricing:         mapping.EnableAutoPricing,
		AutoPricingSource:         source,
		PrimaryUpstreamSiteID:     primarySiteID,
		PrimaryUpstreamGroupName:  primaryGroupName,
		AutoPricingStrategy:       strategy,
		FixedIncrease:             fixedIncrease,
		PercentageIncrease:        percentageIncrease,
		AdjustThresholdPercent:    thresholdPercent,
		MinMultiplier:             mapping.MinMultiplier,
		MaxMultiplier:             mapping.MaxMultiplier,
		EnableAutoPricingNotify:   mapping.EnableAutoPricingNotify,
		AutoPricingNotifyBotIDs:   notifyBotIDs,
		AutoPricingNotifyTemplate: strings.TrimSpace(mapping.AutoPricingNotifyTemplate),
	}, true, nil
}

// RunAutoPricingNow 手动触发单个自有分组的自动调价。
// 手动运行使用当前上游缓存倍率作为参考值，不依赖同步前后快照，也不执行阈值拦截。
func (s *Service) RunAutoPricingNow(ctx context.Context, userID string, req AutoPricingRunRequest) (AutoPricingRunResponse, error) {
	ownGroup := strings.TrimSpace(req.OwnGroup)
	if ownGroup == "" {
		return AutoPricingRunResponse{}, requestError(ErrorRequest)
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return AutoPricingRunResponse{}, err
	}
	state, err := s.authenticatedState(ctx, userID, adminAccountID)
	if err != nil {
		return AutoPricingRunResponse{}, err
	}

	mapping, ok := findMappingByOwnGroup(state.Mappings, ownGroup)
	if !ok || !mapping.EnableAutoPricing {
		return AutoPricingRunResponse{}, requestError(ErrorRequest)
	}
	adminGroups, err := s.platformService.FetchAdminAllGroups(state.Session)
	if err != nil {
		return AutoPricingRunResponse{}, err
	}
	adminGroupMap := make(map[string]upstream.AdminGroupInfo, len(adminGroups))
	for _, group := range adminGroups {
		adminGroupMap[group.Name] = group
	}
	result, updatedMapping, err := s.processManualAutoPricing(ctx, userID, adminAccountID, state, mapping, adminGroupMap, s.buildWorkspaceLookupMultiplier(ctx, userID, adminAccountID))
	if err != nil {
		return AutoPricingRunResponse{}, err
	}
	response := AutoPricingRunResponse{Mapping: updatedMapping}
	if updatedMapping.LastAutoPricingRun != nil {
		response.Result = *updatedMapping.LastAutoPricingRun
	} else {
		response.Result = autoPricingStatusFromResult(result, "manual", time.Now())
	}
	return response, nil
}

// RealConnect 执行真实对接流程：按平台分支创建上游 Key/Token 和 admin 端转发目标（账号/Channel），最后持久化绑定记录。
func (s *Service) RealConnect(ctx context.Context, userID string, req RealConnectRequest) (RealConnectResponse, error) {
	return s.realConnectManaged(ctx, userID, req)
}

// groupTypeToNewAPIChannelType 将分组平台类型映射为 new-api channel type 数字（回退用）。
func groupTypeToNewAPIChannelType(groupType string) int {
	switch strings.ToLower(groupType) {
	case "openai":
		return 1
	case "anthropic":
		return 14
	case "gemini":
		return 24
	case "deepseek":
		return 43
	default:
		return 1
	}
}

// newAPIChannelTypeName 返回 new-api channel type ID 对应的短名称，用于 channel 命名前缀。
var newAPIChannelTypeNames = map[int]string{
	1: "OpenAI", 2: "Midjourney", 3: "Azure", 4: "Ollama",
	5: "MJ+", 6: "OpenAIMax", 7: "OhMyGPT", 8: "Custom",
	9: "AILS", 10: "AIProxy", 11: "PaLM", 12: "API2GPT",
	13: "AIGC2D", 14: "Anthropic", 15: "Baidu", 16: "Zhipu",
	17: "Ali", 18: "Xunfei", 19: "360", 20: "OpenRouter",
	21: "AIProxyLib", 22: "FastGPT", 23: "Tencent", 24: "Gemini",
	25: "Moonshot", 26: "ZhipuV4", 27: "Perplexity", 31: "LingYi",
	33: "AWS", 34: "Cohere", 35: "MiniMax", 36: "SunoAPI",
	37: "Dify", 38: "Jina", 39: "Cloudflare", 40: "SiliconFlow",
	41: "VertexAI", 42: "Mistral", 43: "DeepSeek", 44: "MokaAI",
	45: "VolcEngine", 46: "BaiduV2", 47: "Xinference", 48: "xAI",
	49: "Coze", 50: "Kling", 51: "Jimeng", 52: "Vidu",
	53: "Submodel", 54: "DoubaoVideo", 55: "Sora", 56: "Replicate",
	57: "Codex",
}

func newAPIChannelTypeName(channelType int) string {
	if name, ok := newAPIChannelTypeNames[channelType]; ok {
		return name
	}
	return "OpenAI"
}

func connectionCapabilities(platform upstream.Platform) *ConnectionCapabilities {
	if platform != upstream.PlatformNewAPI {
		return &ConnectionCapabilities{Mode: "account", RequiresGroupType: true}
	}
	ids := make([]int, 0, len(newAPIChannelTypeNames))
	for id := range newAPIChannelTypeNames {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	options := make([]ChannelTypeOption, 0, len(ids))
	for _, id := range ids {
		options = append(options, ChannelTypeOption{ID: id, Name: newAPIChannelTypeNames[id]})
	}
	return &ConnectionCapabilities{
		Mode:                "channel",
		RequiresChannelType: true,
		ChannelTypes:        options,
		SuggestedChannelTypeByGroup: map[string]int{
			"openai": 1, "anthropic": 14, "gemini": 24, "deepseek": 43,
		},
	}
}

// ListUpstreamKeys 获取指定上游站点的 API Key 列表。
// 通过上游站点的 session 调用其 /api/v1/keys 接口，返回 key 列表供前端手动绑定时选择。
// ListUpstreamKeys 平台中性地获取上游站点的 Key/Token 列表。
// sub2api 列 API Key，new-api 列 Token（返回统一的 Sub2APIKeyItem 结构）。
func (s *Service) ListUpstreamKeys(ctx context.Context, userID string, siteID string) ([]upstream.Sub2APIKeyItem, error) {
	if strings.TrimSpace(siteID) == "" {
		return nil, requestError(ErrorRequest)
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return nil, err
	}
	upstreamSite, err := s.upstreamLookup.GetSite(ctx, siteID)
	if err != nil || upstreamSite == nil || upstreamSite.Session == nil || upstreamSite.UserID != userID || upstreamSite.AdminAccountID != adminAccountID {
		return nil, requestError(ErrorRequest)
	}
	session := *upstreamSite.Session
	var keys []upstream.Sub2APIKeyItem
	switch session.Platform {
	case upstream.PlatformNewAPI:
		keys, err = s.platformService.ListNewAPITokens(session)
	default:
		keys, err = s.platformService.ListSub2APIKeys(session)
	}
	if err != nil {
		log.Printf("[list-upstream-keys] 获取上游 key 列表失败 site=%s platform=%s err=%v", upstreamSite.Name, session.Platform, err)
		return nil, err
	}
	return keys, nil
}

// RealBind 手动绑定已有的上游 Key/Token，仅创建绑定记录。
// new-api 场景下 token 列表返回的 key 是脱敏的，需要通过 /api/token/:id/key 获取完整 key。
func (s *Service) RealBind(ctx context.Context, userID string, req RealBindRequest) (RealConnectResponse, error) {
	return s.realBindExisting(ctx, userID, req)
}

// ListRealConnections 获取指定用户的所有真实对接绑定记录。
func (s *Service) ListRealConnections(ctx context.Context, userID string) ([]RealConnection, error) {
	if s.connRepository == nil {
		return nil, nil
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return nil, err
	}
	connections, err := s.connRepository.ListRealConnections(ctx, userID, adminAccountID)
	if err != nil {
		return nil, err
	}
	connections = s.reconcileMissingSub2APIConnections(ctx, userID, adminAccountID, connections)
	for i := range connections {
		connections[i] = publicRealConnection(connections[i])
	}
	return connections, nil
}

// reconcileMissingSub2APIConnections makes external deletion safe. A Sub2API
// admin account can be removed outside TransitHub; in that case the matching
// upstream key and local connection must not remain orphaned. Reconciliation is
// best effort and only runs after a successful, authenticated remote inventory
// read, so transient upstream failures never cause local data loss.
func (s *Service) reconcileMissingSub2APIConnections(ctx context.Context, userID, adminAccountID string, connections []RealConnection) []RealConnection {
	if len(connections) == 0 || s.platformService == nil || s.repository == nil || s.connRepository == nil {
		return connections
	}
	state, err := s.authenticatedState(ctx, userID, adminAccountID)
	if err != nil || state == nil || state.Session.Platform != upstream.PlatformSub2API {
		return connections
	}
	accounts, err := s.platformService.ListAdminAllAccounts(state.Session)
	if err != nil {
		log.Printf("[real-connections] skip remote reconciliation: %v", err)
		return connections
	}
	present := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		if id := strings.TrimSpace(account.ID); id != "" {
			present[id] = struct{}{}
		}
	}
	result := make([]RealConnection, 0, len(connections))
	for _, conn := range connections {
		if conn.AdminPlatform != string(upstream.PlatformSub2API) || strings.TrimSpace(conn.AdminAccountID) == "" {
			result = append(result, conn)
			continue
		}
		if _, ok := present[strings.TrimSpace(conn.AdminAccountID)]; ok {
			result = append(result, conn)
			continue
		}
		if !s.cleanupExternallyDeletedConnection(ctx, userID, adminAccountID, conn) {
			result = append(result, conn)
		}
	}
	return result
}

func (s *Service) cleanupExternallyDeletedConnection(ctx context.Context, userID, adminAccountID string, conn RealConnection) bool {
	if s.upstreamLookup != nil && strings.TrimSpace(conn.UpstreamKeyID) != "" {
		site, err := s.upstreamLookup.GetSite(ctx, conn.UpstreamSiteID)
		if err != nil || site == nil || site.Session == nil || site.UserID != userID || site.AdminAccountID != adminAccountID {
			return false
		}
		if conn.UpstreamPlatform != "" && conn.UpstreamPlatform != string(site.Session.Platform) {
			return false
		}
		if err := s.deleteUpstreamCredential(*site.Session, conn.UpstreamKeyID); err != nil && !upstream.IsNotFound(err) {
			log.Printf("[real-connections] keep stale connection id=%s: upstream key cleanup failed: %v", conn.ID, err)
			return false
		}
	}
	removePricing := conn.PricingMappingEnabled
	if repository, ok := s.connRepository.(ScopedRealDisconnectRepository); ok {
		if err := repository.DeleteRealConnectionWithPricingMapping(ctx, conn, removePricing); err != nil {
			log.Printf("[real-connections] local cleanup failed id=%s: %v", conn.ID, err)
			return false
		}
		return true
	}
	if err := s.connRepository.DeleteRealConnection(ctx, conn.ID, userID, adminAccountID); err != nil {
		log.Printf("[real-connections] local cleanup failed id=%s: %v", conn.ID, err)
		return false
	}
	return true
}

// ListRealConnectionsForWorkspace 按显式传入的 userID + adminAccountID 查询真实对接绑定记录，
// 不解析"当前" workspace。供没有 HTTP 请求上下文的后台调度器使用：调度器持有的策略
// （connection_health_policies）本身就记录了 user_id/admin_account_id，必须按策略自带的
// workspace 读取对应连接，不能依赖 authctx/admin_accounts 的"当前工作区"语义（那是请求态概念）。
func (s *Service) ListRealConnectionsForWorkspace(ctx context.Context, userID string, adminAccountID string) ([]RealConnection, error) {
	if s.connRepository == nil {
		return nil, nil
	}
	return s.connRepository.ListRealConnections(ctx, userID, adminAccountID)
}

// ListAllRealConnectionsForBackground returns private connection inventory for
// trusted background work only. HTTP handlers must continue using the scoped
// public list above.
func (s *Service) ListAllRealConnectionsForBackground(ctx context.Context) ([]RealConnection, error) {
	repository, ok := s.connRepository.(interface {
		ListAllRealConnections(context.Context) ([]RealConnection, error)
	})
	if !ok {
		return nil, nil
	}
	return repository.ListAllRealConnections(ctx)
}

// ListUpstreamKeyGroupSnapshotsForWorkspace reads credentials and pricing
// groups from the same live upstream session. It deliberately does not use the
// site metrics cache, which can be stale between normal site synchronizations.
func (s *Service) ListUpstreamKeyGroupSnapshotsForWorkspace(ctx context.Context, userID, adminAccountID, siteID string) ([]UpstreamKeyGroupSnapshot, error) {
	if strings.TrimSpace(siteID) == "" {
		return nil, requestError(ErrorRequest)
	}
	site, err := s.upstreamLookup.GetSite(ctx, siteID)
	if err != nil || site == nil || site.Session == nil || site.UserID != userID || site.AdminAccountID != adminAccountID {
		return nil, requestError(ErrorRequest)
	}
	session := *site.Session
	var keys []upstream.Sub2APIKeyItem
	if session.Platform == upstream.PlatformNewAPI {
		keys, err = s.platformService.ListNewAPITokens(session)
	} else {
		keys, err = s.platformService.ListSub2APIKeys(session)
	}
	if err != nil {
		return nil, err
	}
	// Upstream management uses the same user-visible group endpoint as mapping
	// and group-rate history. The upstream service deduplicates this request,
	// caches the result briefly, and persists the snapshot for other views.
	var groups []upstream.GroupInfo
	if reader, ok := s.upstreamLookup.(upstream.CurrentGroupReader); ok {
		groups, err = reader.CurrentGroups(ctx, userID, adminAccountID, siteID)
	} else {
		groups, err = s.platformService.FetchAdminGroups(session)
	}
	if err != nil {
		return nil, err
	}
	byID := make(map[string]upstream.GroupInfo, len(groups))
	byName := make(map[string]upstream.GroupInfo, len(groups))
	for _, group := range groups {
		if id := strings.TrimSpace(group.ID); id != "" {
			byID[id] = group
		}
		if name := strings.ToLower(strings.TrimSpace(group.Name)); name != "" {
			byName[name] = group
		}
	}
	result := make([]UpstreamKeyGroupSnapshot, 0, len(keys))
	for _, key := range keys {
		group, found := byID[strings.TrimSpace(key.GroupID)]
		if !found {
			group, found = byName[strings.ToLower(strings.TrimSpace(key.GroupName))]
		}
		if !found {
			continue
		}
		var multiplier *float64
		if group.Multiplier != nil {
			value := *group.Multiplier
			multiplier = &value
		}
		result = append(result, UpstreamKeyGroupSnapshot{SiteID: siteID, KeyID: strings.TrimSpace(key.ID), GroupID: strings.TrimSpace(group.ID), GroupName: strings.TrimSpace(group.Name), Multiplier: multiplier})
	}
	return result, nil
}

// RealDisconnect 取消真实对接：根据 mode 决定是仅删除记录还是同时删除上游 Key。
// mode == "unlink"：仅删除 real_connections 记录（所有平台通用）。
// mode == "delete-key"：删除该对接使用的上游 Key，再删除记录；Admin 转发账号始终保留。
// 旧客户端的 mode == "full" 也按 delete-key 处理，防止取消对接误删转发账号。
func (s *Service) RealDisconnect(ctx context.Context, userID string, req RealDisconnectRequest) error {
	return s.realDisconnectConnection(ctx, userID, req)
}

// removeUpstreamMappingAndDeleteConnection atomically removes the local mapping target and real_connection row.
func (s *Service) removeUpstreamMappingAndDeleteConnection(ctx context.Context, userID, adminAccountID, connectionID, siteID, groupName string) error {
	if repo, ok := s.connRepository.(AtomicRealDisconnectRepository); ok {
		return repo.RemoveUpstreamMappingAndDeleteConnection(ctx, userID, adminAccountID, connectionID, siteID, groupName)
	}
	state, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil {
		return err
	}
	before := cloneStateForMutation(state)
	if state != nil {
		removeMappingTargetFromState(state, siteID, groupName)
		if err := s.repository.Save(ctx, *state); err != nil {
			return err
		}
	}
	if err := s.connRepository.DeleteRealConnection(ctx, connectionID, userID, adminAccountID); err != nil {
		if before != nil {
			_ = s.repository.Save(ctx, *before)
		}
		return err
	}
	return nil
}

// backfillMappingsFromRealConnections uses real_connections as the source of truth for
// existing real-connect/manual-bind records and repairs my_site_states.mappings before the
// dashboard group modal is returned. This covers historical records created while mapping
// sync failed or before the mapping cache existed.
func (s *Service) backfillMappingsFromRealConnections(ctx context.Context, state *State, idToName map[string]string) error {
	if s.connRepository == nil || state == nil {
		return nil
	}
	connections, err := s.connRepository.ListRealConnections(ctx, state.UserID, state.AdminAccountID)
	if err != nil {
		return err
	}
	if len(connections) == 0 {
		return nil
	}
	applyMappingsFromRealConnections(state, idToName, connections)
	return nil
}

func applyMappingsFromRealConnections(state *State, idToName map[string]string, connections []RealConnection) {
	if state == nil || len(connections) == 0 {
		return
	}
	existing := make(map[string]int, len(state.Mappings))
	for i := range state.Mappings {
		existing[state.Mappings[i].OwnGroup] = i
	}

	for _, conn := range connections {
		// Only legacy records need a response-only repair. Managed and existing
		// connections persist their mapping when created, so treating them as the
		// source of truth here would make a user's explicit removal reappear after
		// every refresh.
		if conn.ProvisioningMode != "" && conn.ProvisioningMode != ProvisioningModeLegacy {
			continue
		}
		target := UpstreamGroupRef{SiteID: conn.UpstreamSiteID, GroupName: conn.UpstreamGroupName}
		for _, ownID := range conn.OwnGroupIDs {
			ownName, ok := idToName[ownID]
			if !ok {
				log.Printf("[mapping-backfill] 未找到分组 ID=%s 对应的名称，跳过 conn_id=%s", ownID, conn.ID)
				continue
			}
			mappingIndex, found := existing[ownName]
			if !found {
				state.Mappings = append(state.Mappings, GroupMapping{
					OwnGroup:        ownName,
					UpstreamTargets: []UpstreamGroupRef{target},
				})
				existing[ownName] = len(state.Mappings) - 1
				continue
			}
			if !hasUpstreamTarget(state.Mappings[mappingIndex].UpstreamTargets, target) {
				state.Mappings[mappingIndex].UpstreamTargets = append(state.Mappings[mappingIndex].UpstreamTargets, target)
			}
		}
	}
}

func hasUpstreamTarget(targets []UpstreamGroupRef, target UpstreamGroupRef) bool {
	for _, existing := range targets {
		if existing.SiteID == target.SiteID && existing.GroupName == target.GroupName {
			return true
		}
	}
	return false
}

// addUpstreamMapping 将上游站点+分组添加到用户 my_site_states.mappings 中每个关联的自有分组里。
// 如果自有分组尚未有映射记录则创建，如果已有则在 upstreamTargets 中追加（去重）。
// 注意：mappings 中 OwnGroup 存储的是分组名称（非数字 ID），与仪表盘分组关联一致。
func (s *Service) addUpstreamMapping(ctx context.Context, userID string, adminAccountID string, ownGroupIDs []string, siteID, groupName, adminPlatform, upstreamAccountID string) {
	state, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil || state == nil {
		return
	}

	// 获取 admin 分组列表，构建 ID → 分组名称 的映射
	// mappings 中 OwnGroup 使用分组名称（与 MappingOptions 清理逻辑和前端 GroupListModal 一致）
	adminGroups, err := s.platformService.FetchAdminAllGroups(state.Session)
	if err != nil {
		log.Printf("[add-upstream-mapping] 获取 admin 分组失败 err=%v", err)
		return
	}
	idToName := make(map[string]string, len(adminGroups))
	for _, g := range adminGroups {
		if name := strings.TrimSpace(g.Name); name != "" {
			idToName[g.ID] = name
		}
	}

	target := UpstreamGroupRef{SiteID: siteID, GroupName: groupName}
	if adminPlatform == string(upstream.PlatformSub2API) && strings.TrimSpace(upstreamAccountID) != "" {
		accountID := strings.TrimSpace(upstreamAccountID)
		target.Sub2APIAccountID = &accountID
	}

	existing := make(map[string]*GroupMapping, len(state.Mappings))
	for i := range state.Mappings {
		existing[state.Mappings[i].OwnGroup] = &state.Mappings[i]
	}

	for _, ownID := range ownGroupIDs {
		// 将数字 ID 解析为分组名称
		ownName, ok := idToName[ownID]
		if !ok {
			log.Printf("[add-upstream-mapping] 未找到分组 ID=%s 对应的名称，跳过", ownID)
			continue
		}

		if m, found := existing[ownName]; found {
			alreadyHas := false
			for index, t := range m.UpstreamTargets {
				if t.SiteID == siteID && t.GroupName == groupName {
					if m.UpstreamTargets[index].Sub2APIAccountID == nil && target.Sub2APIAccountID != nil {
						m.UpstreamTargets[index].Sub2APIAccountID = target.Sub2APIAccountID
					}
					alreadyHas = true
					break
				}
			}
			if !alreadyHas {
				m.UpstreamTargets = append(m.UpstreamTargets, target)
			}
		} else {
			newMapping := GroupMapping{
				OwnGroup:        ownName,
				UpstreamTargets: []UpstreamGroupRef{target},
			}
			state.Mappings = append(state.Mappings, newMapping)
			existing[ownName] = &state.Mappings[len(state.Mappings)-1]
		}
	}
	_ = s.repository.Save(ctx, *state)
}

// keyPrefixes 创建 API Key 时随机选取的名称前缀池，契合 TransitHub（流量枢纽）项目主题。
var keyPrefixes = []string{
	"Relay",    // 中继站
	"Express",  // 快线
	"Conduit",  // 管道
	"Nexus",    // 枢纽
	"Voyage",   // 航程
	"Shuttle",  // 穿梭
	"Beacon",   // 信标
	"Meridian", // 子午线
	"Transit",  // 中转
	"Vector",   // 航向
	"Flux",     // 流
	"Pulse",    // 脉冲
	"Arc",      // 弧线
	"Drift",    // 漂流
	"Link",     // 链路
	"Orbit",    // 轨道
}

// randomKeyPrefix 从前缀池中随机选取一个，用于 API Key 命名。
func randomKeyPrefix() string {
	b := make([]byte, 1)
	_, _ = rand.Read(b)
	return keyPrefixes[int(b[0])%len(keyPrefixes)]
}

// groupTypePrefix 根据分组类型返回账号名称前缀（A=OpenAI, B=Anthropic, C=Gemini, D=Antigravity）。
func groupTypePrefix(groupType string) string {
	switch strings.ToLower(groupType) {
	case "openai":
		return "A"
	case "anthropic":
		return "B"
	case "gemini":
		return "C"
	case "antigravity":
		return "D"
	default:
		return "X"
	}
}

// resolveGroupInfo 从上游站点缓存的分组列表中查找指定分组的平台类型和倍率显示文本。
// 返回小写的平台名（如 "openai"、"anthropic"）和倍率显示文本（如 "1.5x"），未找到时返回空字符串。
func resolveGroupInfo(groups []upstream.GroupInfo, groupID string) (groupType string, multiplierDisplay string) {
	for _, g := range groups {
		if g.ID == groupID {
			if g.Platform != nil && strings.TrimSpace(*g.Platform) != "" {
				groupType = strings.ToLower(strings.TrimSpace(*g.Platform))
			}
			multiplierDisplay = g.MultiplierDisplay
			return
		}
	}
	return
}

// stringsToInts 将字符串切片转为整数切片（Sub2API 接口要求 group_ids 为整数数组）。
func stringsToInts(ss []string) ([]int, error) {
	result := make([]int, 0, len(ss))
	for _, s := range ss {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("invalid group id %q: %w", s, err)
		}
		result = append(result, n)
	}
	return result, nil
}

// buildAccountPayload 按分组类型组装 admin 站点创建转发账号的请求体。
// 不同类型有不同的 platform、extra、credentials 配置，详见计划文档中的类型表。
func buildAccountPayload(groupType, baseURL, apiKey string, ownGroupIDs []int, accountName string) map[string]any {
	credentials := map[string]any{
		"base_url": baseURL,
		"api_key":  apiKey,
	}

	payload := map[string]any{
		"name":        accountName,
		"type":        "apikey",
		"credentials": credentials,
		"priority":    1,
		"group_ids":   ownGroupIDs,
	}

	switch strings.ToLower(groupType) {
	case "openai":
		payload["platform"] = "openai"
		credentials["pool_mode"] = true
		payload["concurrency"] = 1000
	case "anthropic":
		payload["platform"] = "anthropic"
		credentials["pool_mode"] = true
		payload["concurrency"] = 1000
	case "gemini":
		payload["platform"] = "gemini"
		credentials["pool_mode"] = true
		credentials["tier_id"] = "aistudio_free"
		payload["concurrency"] = 1000
	case "antigravity":
		payload["platform"] = "antigravity"
		payload["concurrency"] = 10
	default:
		payload["platform"] = groupType
		payload["concurrency"] = 100
	}

	return payload
}

// randomConnID 生成真实对接绑定记录的唯一 ID。
func randomConnID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate connection id: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// authenticatedState 获取并校验用户的 admin 会话（平台感知），必要时刷新令牌。
func (s *Service) authenticatedState(ctx context.Context, userID string, adminAccountID string) (*State, error) {
	state, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil {
		return nil, err
	}
	if state == nil || !state.Session.IsAuthenticated() {
		return nil, requestError(ErrorAuthRequired)
	}
	return s.validatedState(ctx, state)
}

// RequireSession 获取并校验用户的 admin 会话（必要时刷新令牌），供活动调价模块
// （group_rate_campaigns.AdminGroupOperator）在开启/恢复活动时复用同一套会话管理逻辑，
// 避免活动调价模块重复实现 token 刷新和 admin 角色校验。
func (s *Service) RequireSession(ctx context.Context, userID string, adminAccountID string) (upstream.Session, error) {
	state, err := s.authenticatedState(ctx, userID, adminAccountID)
	if err != nil {
		return upstream.Session{}, err
	}
	return state.Session, nil
}

func (s *Service) mutateState(ctx context.Context, userID string, adminAccountID string, mutate StateMutation) (*State, error) {
	if repo, ok := s.repository.(TransactionalStateRepository); ok {
		return repo.MutateState(ctx, userID, adminAccountID, mutate)
	}
	state, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil || state == nil {
		return state, err
	}
	if err := mutate(state); err != nil {
		return nil, err
	}
	if err := s.repository.Save(ctx, *state); err != nil {
		return nil, err
	}
	return state, nil
}

// FetchAdminGroups 透传 platformService 拉取 admin 自有分组列表。
func (s *Service) FetchAdminGroups(session upstream.Session) ([]upstream.AdminGroupInfo, error) {
	return s.platformService.FetchAdminAllGroups(session)
}

// UpdateAdminGroupMultiplier 透传 platformService 修改 admin 自有分组倍率。
func (s *Service) UpdateAdminGroupMultiplier(session upstream.Session, group upstream.AdminGroupInfo, multiplier float64) error {
	return s.platformService.UpdateAdminGroupMultiplier(session, group, multiplier)
}

// validatedState 刷新临期令牌并校验 admin 角色（平台中性）。
func (s *Service) validatedState(ctx context.Context, state *State) (*State, error) {
	if !state.Session.IsAuthenticated() {
		return nil, requestError(ErrorAuthRequired)
	}
	refreshedSession, err := s.platformService.RefreshSession(state.Session)
	if err != nil {
		return nil, requestError(ErrorAdminOnly)
	}
	if refreshedSession.AccessToken != state.Session.AccessToken || refreshedSession.RefreshToken != state.Session.RefreshToken ||
		refreshedSession.Cookie != state.Session.Cookie {
		state.Session = refreshedSession
		if err := s.repository.Save(ctx, *state); err != nil {
			return nil, err
		}
	}
	if err := s.platformService.VerifyAdmin(state.Session); err != nil {
		return nil, requestError(ErrorAdminOnly)
	}
	return state, nil
}

// SyncAdminSession 实现 dashboard.MySiteStateSync 接口。
// dashboard 登录成功后调用此方法，将 admin session 同步到 my_site_states 表，
// 使 RealConnect 等依赖 my_site_states 的功能可以使用 admin 会话。
// 保留已有的 mappings 和 own_groups，仅更新 session 和身份信息。
func (s *Service) SyncAdminSession(ctx context.Context, userID string, adminAccountID string, session upstream.Session, identity string) error {
	existing, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil {
		return err
	}
	if existing == nil {
		existing = &State{
			UserID:         userID,
			AdminAccountID: adminAccountID,
			Mappings:       []GroupMapping{},
		}
	}
	existing.AdminAccountID = adminAccountID
	existing.BaseURL = session.BaseURL
	existing.Email = identity
	existing.Session = session
	return s.repository.Save(ctx, *existing)
}

// StoredSession reads the persisted credential without refreshing or contacting
// the upstream site. Dashboard reconciliation uses it to avoid overwriting a
// newer PostgreSQL session merely because an upstream request failed transiently.
func (s *Service) StoredSession(ctx context.Context, userID string, adminAccountID string) (upstream.Session, bool, error) {
	state, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil {
		return upstream.Session{}, false, err
	}
	if state == nil || !state.Session.IsAuthenticated() {
		return upstream.Session{}, false, nil
	}
	return state.Session, true, nil
}

func (s *Service) currentAdminAccountID(ctx context.Context, userID string) (string, error) {
	if s.accounts == nil {
		return "", requestError("admin.adminAccounts.errors.noCurrentAccount")
	}
	return s.accounts.RequireCurrentID(ctx, userID)
}

// floatOrDefault 解引用指针，nil 时返回默认值，非 nil 时返回实际值（含 0）。
func floatOrDefault(p *float64, defaultVal float64) float64 {
	if p == nil {
		return defaultVal
	}
	return *p
}

func cloneGroupMappingValue(mapping GroupMapping) GroupMapping {
	copy := mapping
	if mapping.UpstreamTargets != nil {
		copy.UpstreamTargets = append([]UpstreamGroupRef(nil), mapping.UpstreamTargets...)
	}
	if mapping.AutoPricingNotifyBotIDs != nil {
		copy.AutoPricingNotifyBotIDs = append([]string(nil), mapping.AutoPricingNotifyBotIDs...)
	}
	return copy
}

func cloneStateForMutation(state *State) *State {
	if state == nil {
		return nil
	}
	copy := *state
	if state.Mappings != nil {
		copy.Mappings = make([]GroupMapping, len(state.Mappings))
		for i := range state.Mappings {
			copy.Mappings[i] = cloneGroupMappingValue(state.Mappings[i])
		}
	}
	if state.OwnGroups != nil {
		copy.OwnGroups = append([]GroupOption(nil), state.OwnGroups...)
	}
	return &copy
}

func targetKey(siteID string, groupName string) string {
	return strings.TrimSpace(siteID) + "\x00" + strings.TrimSpace(groupName)
}

func (s *Service) liveMappingTargetMultipliers(ctx context.Context, userID string, adminAccountID string, mappings []GroupMapping) []MappingUpstreamTargetRate {
	targetsBySite := make(map[string]map[string]string)
	for _, mapping := range mappings {
		for _, target := range mapping.UpstreamTargets {
			siteID, groupName := strings.TrimSpace(target.SiteID), strings.TrimSpace(target.GroupName)
			if siteID == "" || groupName == "" {
				continue
			}
			if targetsBySite[siteID] == nil {
				targetsBySite[siteID] = make(map[string]string)
			}
			targetsBySite[siteID][strings.ToLower(groupName)] = groupName
		}
	}

	siteIDs := make([]string, 0, len(targetsBySite))
	for siteID := range targetsBySite {
		siteIDs = append(siteIDs, siteID)
	}
	sort.Strings(siteIDs)
	result := make([]MappingUpstreamTargetRate, 0)
	for _, siteID := range siteIDs {
		groupNames := targetsBySite[siteID]
		groups, ok := s.fetchWorkspaceUpstreamGroups(ctx, userID, adminAccountID, siteID)
		authoritative := ok
		source := "live"
		if !ok {
			// Keep the last successful site observation visible when a transient
			// upstream 401/network failure prevents a fresh read. This is display
			// and ranking input only; auto-pricing still requires a live read.
			if site, err := s.upstreamLookup.GetSite(ctx, siteID); err == nil && site != nil {
				groups = site.Metrics.Groups
				if len(groups) > 0 {
					ok = true
					source = "cached"
				}
			}
		}
		byName := make(map[string]upstream.GroupInfo, len(groups))
		if ok {
			for _, group := range groups {
				name := strings.ToLower(strings.TrimSpace(group.Name))
				if name != "" {
					byName[name] = group
				}
			}
		}
		orderedNames := make([]string, 0, len(groupNames))
		for normalizedName := range groupNames {
			orderedNames = append(orderedNames, normalizedName)
		}
		sort.Strings(orderedNames)
		for _, normalizedName := range orderedNames {
			target := MappingUpstreamTargetRate{SiteID: siteID, GroupName: groupNames[normalizedName], Source: source}
			if group, found := byName[normalizedName]; found {
				if group.Multiplier != nil {
					value := *group.Multiplier
					target.Multiplier = &value
				}
			} else if authoritative {
				target.Stale = true
			}
			result = append(result, target)
		}
	}
	return result
}

// fetchWorkspaceUpstreamGroups is the shared source for pricing-mapping
// multipliers. The upstream service owns the short-lived cache and persists
// successful observations; Site.Metrics remains only the normal sync cache.
func (s *Service) fetchWorkspaceUpstreamGroups(ctx context.Context, userID string, adminAccountID string, siteID string) ([]upstream.GroupInfo, bool) {
	if reader, ok := s.upstreamLookup.(upstream.CurrentGroupReader); ok {
		groups, err := reader.CurrentGroups(ctx, userID, adminAccountID, siteID)
		return groups, err == nil
	}
	site, err := s.upstreamLookup.GetSite(ctx, siteID)
	if err != nil || site == nil || site.Session == nil || site.UserID != userID || site.AdminAccountID != adminAccountID {
		return nil, false
	}
	groups, err := s.platformService.FetchAdminGroups(*site.Session)
	if err != nil {
		return nil, false
	}
	return groups, true
}

func pruneTargetsByKey(targets []UpstreamGroupRef, missing map[string]struct{}) []UpstreamGroupRef {
	if len(missing) == 0 {
		return targets
	}
	cleaned := make([]UpstreamGroupRef, 0, len(targets))
	for _, target := range targets {
		if _, drop := missing[targetKey(target.SiteID, target.GroupName)]; drop {
			continue
		}
		cleaned = append(cleaned, target)
	}
	return cleaned
}

func removeMappingTargetFromState(state *State, siteID string, groupName string) {
	if state == nil || len(state.Mappings) == 0 {
		return
	}
	cleaned := make([]GroupMapping, 0, len(state.Mappings))
	for _, mapping := range state.Mappings {
		targets := make([]UpstreamGroupRef, 0, len(mapping.UpstreamTargets))
		for _, target := range mapping.UpstreamTargets {
			if target.SiteID == siteID && target.GroupName == groupName {
				continue
			}
			targets = append(targets, target)
		}
		if len(targets) > 0 {
			mapping.UpstreamTargets = targets
			cleaned = append(cleaned, mapping)
		}
	}
	state.Mappings = cleaned
}

// changedGroup 表示一个上游分组在同步前后倍率发生了变化。
type changedGroup struct {
	GroupName     string
	OldMultiplier float64
	NewMultiplier float64
}

// groupMultiplierChange 记录单个分组在本次同步中的旧/新倍率。
// 用于构建同步站点的倍率变化快照，避免聚合来源从缓存读取到已被覆盖的新值。
type groupMultiplierChange struct {
	Old float64
	New float64
}

// changedUpstreamGroups 对比同步前后的 Metrics，返回倍率发生变化的上游分组列表。
// 使用 group.ID + "|" + group.Name 作为匹配 key，与通知逻辑保持一致。
func changedUpstreamGroups(oldMetrics, newMetrics upstream.Metrics) []changedGroup {
	if len(oldMetrics.Groups) == 0 || len(newMetrics.Groups) == 0 {
		return nil
	}
	oldMap := make(map[string]float64, len(oldMetrics.Groups))
	oldNameMap := make(map[string]string, len(oldMetrics.Groups))
	for _, g := range oldMetrics.Groups {
		if g.Multiplier != nil {
			key := g.ID + "|" + g.Name
			oldMap[key] = *g.Multiplier
			oldNameMap[key] = g.Name
		}
	}
	var result []changedGroup
	for _, g := range newMetrics.Groups {
		if g.Multiplier == nil {
			continue
		}
		key := g.ID + "|" + g.Name
		oldVal, existed := oldMap[key]
		if !existed || oldVal == *g.Multiplier {
			continue
		}
		result = append(result, changedGroup{
			GroupName:     g.Name,
			OldMultiplier: oldVal,
			NewMultiplier: *g.Multiplier,
		})
	}
	return result
}

// mappingUsesTarget 检查 mapping 的 UpstreamTargets 是否引用了指定的 siteID + groupName。
func mappingUsesTarget(mapping GroupMapping, siteID, groupName string) bool {
	for _, t := range mapping.UpstreamTargets {
		if t.SiteID == siteID && t.GroupName == groupName {
			return true
		}
	}
	return false
}

// autoPricingResult 记录单个分组自动调价的计算结果。
type autoPricingResult struct {
	OwnGroup         string
	OldReference     float64
	NewReference     float64
	OldReferenceSet  bool
	NewReferenceSet  bool
	OldOwnMultiplier *float64
	NewOwnMultiplier *float64
	TargetMultiplier float64
	TargetSet        bool
	Status           string // applied, threshold_exceeded, skipped, failed
	Reason           string
	PersistError     error
}

// percentEpsilon 阈值比较的浮点容差，避免 IEEE 754 精度问题把刚好等于阈值的变化误判为超限。
const percentEpsilon = 1e-9

// thresholdExceeded 判断参考倍率的变化百分比是否严格超过阈值。
// 等于阈值不算超限，使用 epsilon 容差消除浮点精度误差。
// 调用方须保证 oldRef > 0（除零保护在调用侧）。
func thresholdExceeded(oldRef, newRef, thresholdPercent float64) bool {
	changePercent := math.Abs(newRef-oldRef) / oldRef * 100
	return changePercent-thresholdPercent > percentEpsilon
}

// computeReferenceMultipliers 根据 mapping 的 AutoPricingSource 和本次同步站点的倍率变化快照
// 计算参考倍率（old 和 new），是可单元测试的纯函数。
//
// 参数：
//   - source: 调价来源（primary_upstream / lowest_upstream / highest_upstream / average_upstream）
//   - targets: mapping 关联的上游分组列表
//   - primarySiteID, primaryGroupName: 主上游配置
//   - syncSiteID: 本次同步的站点 ID
//   - changesByGroup: 本次同步站点所有变化分组的 old/new 快照（按 GroupName 索引）
//   - newMetricsGroups: 本次同步站点的最新分组列表（用于查找未变化分组的当前倍率）
//   - lookupMultiplier: 查询其他站点分组倍率的回调（从缓存读取）
func computeReferenceMultipliers(
	source string,
	targets []UpstreamGroupRef,
	primarySiteID, primaryGroupName string,
	syncSiteID string,
	changesByGroup map[string]groupMultiplierChange,
	newMetricsGroups []upstream.GroupInfo,
	lookupMultiplier func(siteID, groupName string) *float64,
) (oldRef, newRef float64, ok bool, reason string) {
	switch source {
	case "primary_upstream":
		// 主上游来源：仅当主上游在本次同步站点且发生了变化时才处理
		if primarySiteID != syncSiteID {
			return 0, 0, false, "primary_upstream_not_affected"
		}
		change, found := changesByGroup[primaryGroupName]
		if !found {
			return 0, 0, false, "primary_upstream_not_affected"
		}
		return change.Old, change.New, true, ""

	case "lowest_upstream", "highest_upstream", "average_upstream":
		// 聚合来源：收集所有关联上游的倍率，本次同步站点内的变化分组使用快照值
		var oldMultipliers, newMultipliers []float64
		for _, t := range targets {
			if t.SiteID == syncSiteID {
				// 同步站点内的分组：优先从变化快照取值
				if change, changed := changesByGroup[t.GroupName]; changed {
					oldMultipliers = append(oldMultipliers, change.Old)
					newMultipliers = append(newMultipliers, change.New)
				} else {
					// 同步站点但未变化的分组：old=new=当前值；缺失目标按前端预览口径跳过。
					m := findGroupMultiplier(newMetricsGroups, t.GroupName)
					if m == nil {
						continue
					}
					oldMultipliers = append(oldMultipliers, *m)
					newMultipliers = append(newMultipliers, *m)
				}
			} else {
				// 其他站点的分组：从缓存读取（不受本次同步影响）；缺失目标按前端预览口径跳过。
				m := lookupMultiplier(t.SiteID, t.GroupName)
				if m == nil {
					continue
				}
				oldMultipliers = append(oldMultipliers, *m)
				newMultipliers = append(newMultipliers, *m)
			}
		}
		if len(oldMultipliers) == 0 {
			return 0, 0, false, "missing_reference_multiplier"
		}
		return aggregateMultipliers(source, oldMultipliers),
			aggregateMultipliers(source, newMultipliers),
			true, ""

	default:
		return 0, 0, false, "unknown_pricing_source"
	}
}

// buildLookupMultiplier 构建从缓存查询其他站点分组倍率的回调函数。
func (s *Service) buildLookupMultiplier(ctx context.Context) func(siteID, groupName string) *float64 {
	return func(siteID, groupName string) *float64 {
		site, err := s.upstreamLookup.GetSite(ctx, siteID)
		if err != nil || site == nil {
			return nil
		}
		return findGroupMultiplier(site.Metrics.Groups, groupName)
	}
}

// buildWorkspaceLookupMultiplier reads each referenced upstream site once per
// execution and reuses the live effective group multiplier for every mapping.
func (s *Service) buildWorkspaceLookupMultiplier(ctx context.Context, userID string, adminAccountID string) func(siteID, groupName string) *float64 {
	groupsBySite := make(map[string]map[string]upstream.GroupInfo)
	loadedSites := make(map[string]bool)
	return func(siteID, groupName string) *float64 {
		siteID = strings.TrimSpace(siteID)
		if !loadedSites[siteID] {
			loadedSites[siteID] = true
			groupsByName := make(map[string]upstream.GroupInfo)
			if groups, ok := s.fetchWorkspaceUpstreamGroups(ctx, userID, adminAccountID, siteID); ok {
				for _, group := range groups {
					name := strings.ToLower(strings.TrimSpace(group.Name))
					if name != "" {
						groupsByName[name] = group
					}
				}
			}
			groupsBySite[siteID] = groupsByName
		}
		group, found := groupsBySite[siteID][strings.ToLower(strings.TrimSpace(groupName))]
		if !found || group.Multiplier == nil {
			return nil
		}
		value := *group.Multiplier
		return &value
	}
}

// pruneAuthoritativeMissingTargets 只在本地上游缓存可被视为权威时移除缺失目标。
// 缺失站点、离线/错误站点、从未成功同步的站点都保留目标，避免误删暂时不可确认的映射。
func (s *Service) pruneAuthoritativeMissingTargets(ctx context.Context, userID string, adminAccountID string, targets []UpstreamGroupRef) []UpstreamGroupRef {
	cleaned := make([]UpstreamGroupRef, 0, len(targets))
	for _, target := range targets {
		site, err := s.upstreamLookup.GetSite(ctx, target.SiteID)
		if err != nil || site == nil || site.UserID != userID || site.AdminAccountID != adminAccountID || site.Status != upstream.StatusConnected || site.LastSyncedAt == nil {
			cleaned = append(cleaned, target)
			continue
		}
		if hasUpstreamGroup(site.Metrics.Groups, target.GroupName) {
			cleaned = append(cleaned, target)
		}
	}
	return cleaned
}

func hasUpstreamGroup(groups []upstream.GroupInfo, groupName string) bool {
	for _, group := range groups {
		if group.Name == groupName {
			return true
		}
	}
	return false
}

func normalizedOwnGroupKey(ownGroup string) string {
	return strings.ToLower(strings.TrimSpace(ownGroup))
}

func mergeLastAutoPricingRunByOwnGroup(next []GroupMapping, existing []GroupMapping) {
	statusByOwnGroup := make(map[string]*AutoPricingRunStatus, len(existing))
	for _, mapping := range existing {
		if mapping.LastAutoPricingRun != nil {
			statusByOwnGroup[normalizedOwnGroupKey(mapping.OwnGroup)] = mapping.LastAutoPricingRun
		}
	}
	for i := range next {
		if status := statusByOwnGroup[normalizedOwnGroupKey(next[i].OwnGroup)]; status != nil {
			next[i].LastAutoPricingRun = status
		}
	}
}

func findMappingByOwnGroup(mappings []GroupMapping, ownGroup string) (GroupMapping, bool) {
	key := normalizedOwnGroupKey(ownGroup)
	for _, mapping := range mappings {
		if normalizedOwnGroupKey(mapping.OwnGroup) == key {
			return mapping, true
		}
	}
	return GroupMapping{}, false
}

func findMappingIndexByOwnGroup(mappings []GroupMapping, ownGroup string) int {
	key := normalizedOwnGroupKey(ownGroup)
	for i, mapping := range mappings {
		if normalizedOwnGroupKey(mapping.OwnGroup) == key {
			return i
		}
	}
	return -1
}

func pointerFloat64(value float64) *float64 {
	return &value
}

// findGroupMultiplier 在分组列表中按 Name 查找倍率。
func findGroupMultiplier(groups []upstream.GroupInfo, name string) *float64 {
	for _, g := range groups {
		if g.Name == name && g.Multiplier != nil {
			return g.Multiplier
		}
	}
	return nil
}

// aggregateMultipliers 按聚合策略计算多个倍率的聚合值。
func aggregateMultipliers(source string, multipliers []float64) float64 {
	switch source {
	case "lowest_upstream":
		min := multipliers[0]
		for _, m := range multipliers[1:] {
			if m < min {
				min = m
			}
		}
		return min
	case "highest_upstream":
		max := multipliers[0]
		for _, m := range multipliers[1:] {
			if m > max {
				max = m
			}
		}
		return max
	case "average_upstream":
		sum := 0.0
		for _, m := range multipliers {
			sum += m
		}
		return sum / float64(len(multipliers))
	default:
		return multipliers[0]
	}
}

// calculateAutoPricingTarget 根据自动调价策略和限制范围计算目标倍率。
// 返回目标倍率，四舍五入到 4 位小数。
func calculateAutoPricingTarget(mapping GroupMapping, newReference float64) float64 {
	var target float64
	if mapping.AutoPricingStrategy == "fixed" {
		target = newReference + mapping.FixedIncrease
	} else {
		target = newReference * (1 + mapping.PercentageIncrease/100)
	}
	// 套用最低/最高倍率限制
	if mapping.MinMultiplier != nil && target < *mapping.MinMultiplier {
		target = *mapping.MinMultiplier
	}
	if mapping.MaxMultiplier != nil && target > *mapping.MaxMultiplier {
		target = *mapping.MaxMultiplier
	}
	// 四舍五入到 4 位小数
	return math.Round(target*10000) / 10000
}

// ApplyAutoPricingAfterSync 在上游站点同步完成后，对所有启用自动调价的自有分组执行倍率调整。
// 只处理本次同步站点 siteID 相关的 mappings，每个 mapping 最多计算和更新一次。
// 使用 oldMetrics/newMetrics 构建变化快照，避免从缓存读取已被同步覆盖的旧值。
func (s *Service) ApplyAutoPricingAfterSync(ctx context.Context, userID, adminAccountID, siteID, siteName string, oldMetrics, newMetrics upstream.Metrics) {
	// 1. 构建本次同步站点的倍率变化快照（按 GroupName 索引）
	changesByGroup := buildChangesByGroup(oldMetrics, newMetrics)
	if len(changesByGroup) == 0 {
		return
	}

	// 2. 读取用户的 admin 状态和 mappings
	state, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil || state == nil || !state.Session.IsAuthenticated() {
		log.Printf("[auto-pricing] 无法读取用户状态或未认证 user_id=%s err=%v", userID, err)
		return
	}

	// 刷新 session（如果需要），但不做完整的 admin 校验以避免额外请求
	refreshedSession, err := s.platformService.RefreshSession(state.Session)
	if err != nil {
		log.Printf("[auto-pricing] session 刷新失败 user_id=%s err=%v", userID, err)
		return
	}
	if refreshedSession.AccessToken != state.Session.AccessToken || refreshedSession.RefreshToken != state.Session.RefreshToken ||
		refreshedSession.Cookie != state.Session.Cookie {
		state.Session = refreshedSession
		_ = s.repository.Save(ctx, *state)
	}

	// 3. 筛选启用自动调价的 mappings
	var autoPricingMappings []GroupMapping
	for _, m := range state.Mappings {
		if !m.EnableAutoPricing {
			continue
		}
		autoPricingMappings = append(autoPricingMappings, m)
	}
	if len(autoPricingMappings) == 0 {
		return
	}

	// 4. 获取 admin 端全量分组（用于匹配 OwnGroup → 分组 ID 和当前倍率）
	adminGroups, err := s.platformService.FetchAdminAllGroups(state.Session)
	if err != nil {
		log.Printf("[auto-pricing] 获取 admin 分组失败 user_id=%s err=%v", userID, err)
		return
	}
	adminGroupMap := make(map[string]upstream.AdminGroupInfo, len(adminGroups))
	for _, g := range adminGroups {
		adminGroupMap[g.Name] = g
	}

	// 5. 遍历自动调价 mappings（非 changes×mappings），每个 mapping 最多处理一次
	lookupFn := s.buildWorkspaceLookupMultiplier(ctx, userID, adminAccountID)
	for _, mapping := range autoPricingMappings {
		// 检查该 mapping 是否引用了本次同步站点中发生变化的任意上游分组
		affected := false
		for _, t := range mapping.UpstreamTargets {
			if t.SiteID == siteID {
				if _, changed := changesByGroup[t.GroupName]; changed {
					affected = true
					break
				}
			}
		}
		if !affected {
			continue
		}

		result := s.processAutoPricing(ctx, userID, adminAccountID, state, mapping, siteID, siteName, changesByGroup, newMetrics.Groups, adminGroupMap, lookupFn)
		logAutoPricingResult(siteName, result)
	}
}

// buildChangesByGroup 从同步前后的 Metrics 构建按 GroupName 索引的倍率变化快照。
// 只包含倍率确实发生变化的分组。
func buildChangesByGroup(oldMetrics, newMetrics upstream.Metrics) map[string]groupMultiplierChange {
	if len(oldMetrics.Groups) == 0 || len(newMetrics.Groups) == 0 {
		return nil
	}
	oldMap := make(map[string]float64, len(oldMetrics.Groups))
	for _, g := range oldMetrics.Groups {
		if g.Multiplier != nil {
			oldMap[g.ID+"|"+g.Name] = *g.Multiplier
		}
	}
	result := make(map[string]groupMultiplierChange)
	for _, g := range newMetrics.Groups {
		if g.Multiplier == nil {
			continue
		}
		key := g.ID + "|" + g.Name
		oldVal, existed := oldMap[key]
		if !existed || oldVal == *g.Multiplier {
			continue
		}
		result[g.Name] = groupMultiplierChange{Old: oldVal, New: *g.Multiplier}
	}
	return result
}

// processAutoPricing 处理单个 mapping 的自动调价逻辑。
// 使用 changesByGroup 快照和 newMetricsGroups 计算参考倍率，保证每个 mapping 只处理一次。
// siteName 为触发同步的上游站点名称，用于调价成功通知的模板变量。
func (s *Service) processAutoPricing(ctx context.Context, userID string, adminAccountID string, state *State, mapping GroupMapping, siteID, siteName string, changesByGroup map[string]groupMultiplierChange, newMetricsGroups []upstream.GroupInfo, adminGroupMap map[string]upstream.AdminGroupInfo, lookupFn func(string, string) *float64) (result autoPricingResult) {
	result = autoPricingResult{OwnGroup: mapping.OwnGroup}
	defer func() {
		if result.Status == "" {
			return
		}
		var updatedMultiplier *float64
		if result.Status == "applied" {
			updatedMultiplier = pointerFloat64(result.TargetMultiplier)
		}
		if _, err := s.persistAutoPricingRunStatus(ctx, userID, adminAccountID, result, "after_sync", updatedMultiplier); err != nil {
			result.PersistError = err
			result.Status = "failed"
			result.Reason = "status_persist_failed"
		}
	}()

	// 计算参考倍率（纯函数，不依赖缓存读取本次同步站点的数据）
	oldRef, newRef, ok, reason := computeReferenceMultipliers(
		mapping.AutoPricingSource,
		mapping.UpstreamTargets,
		mapping.PrimaryUpstreamSiteID, mapping.PrimaryUpstreamGroupName,
		siteID,
		changesByGroup,
		newMetricsGroups,
		lookupFn,
	)
	if !ok {
		result.Status = "skipped"
		result.Reason = reason
		return result
	}
	result.OldReference = oldRef
	result.NewReference = newRef
	result.OldReferenceSet = true
	result.NewReferenceSet = true

	// 阈值判断：oldRef <= 0 防除零，thresholdExceeded 使用 epsilon 消除浮点误判
	if oldRef <= 0 {
		result.Status = "skipped"
		result.Reason = "invalid_old_reference_multiplier"
		return result
	}
	if thresholdExceeded(oldRef, newRef, mapping.AdjustThresholdPercent) {
		result.Status = "threshold_exceeded"
		result.Reason = "threshold_exceeded"
		return result
	}

	// 计算目标倍率
	target := calculateAutoPricingTarget(mapping, newRef)
	result.TargetMultiplier = target
	result.TargetSet = true

	// 查找 admin 端对应的自有分组
	adminGroup, found := adminGroupMap[mapping.OwnGroup]
	if !found {
		result.Status = "skipped"
		result.Reason = "own_group_not_found_in_admin"
		return result
	}
	result.OldOwnMultiplier = adminGroup.Multiplier

	// 检查目标倍率是否与当前一致
	if adminGroup.Multiplier != nil && math.Round(*adminGroup.Multiplier*10000)/10000 == target {
		result.Status = "skipped"
		result.Reason = "target_unchanged"
		result.NewOwnMultiplier = adminGroup.Multiplier
		return result
	}

	// 记录调整前倍率，用于通知模板
	oldOwnMultiplier := adminGroup.Multiplier

	// 调用远端 API 更新倍率
	if err := s.platformService.UpdateAdminGroupMultiplier(state.Session, adminGroup, target); err != nil {
		log.Printf("[auto-pricing] 远端倍率更新失败 own_group=%s target=%.4f err=%v", mapping.OwnGroup, target, err)
		result.Status = "failed"
		result.Reason = "remote_update_failed"
		result.NewOwnMultiplier = adminGroup.Multiplier
		return result
	}

	// 更新本地缓存的分组倍率
	for i, g := range state.OwnGroups {
		if g.Name == mapping.OwnGroup {
			state.OwnGroups[i].Multiplier = target
			break
		}
	}
	result.NewOwnMultiplier = pointerFloat64(target)
	result.Status = "applied"

	// 自动调价成功后发送通知（仅在开启通知且配置了机器人时）
	if mapping.EnableAutoPricingNotify && len(mapping.AutoPricingNotifyBotIDs) > 0 && s.botNotifier != nil {
		msg := formatAutoPricingNotify(mapping, siteName, result, oldOwnMultiplier)
		s.botNotifier.SendToBots(ctx, userID, mapping.AutoPricingNotifyBotIDs, msg)
	}

	return result
}

// processManualAutoPricing 使用当前缓存倍率执行一次手动自动调价，并持久化本次运行状态。
func (s *Service) processManualAutoPricing(ctx context.Context, userID string, adminAccountID string, state *State, mapping GroupMapping, adminGroupMap map[string]upstream.AdminGroupInfo, lookupFn func(string, string) *float64) (autoPricingResult, GroupMapping, error) {
	result := autoPricingResult{OwnGroup: mapping.OwnGroup}
	ref, ok, reason := computeCurrentReferenceMultiplier(mapping, lookupFn)
	if !ok {
		result.Status = "skipped"
		result.Reason = reason
		updated, err := s.persistAutoPricingRunStatus(ctx, userID, adminAccountID, result, "manual", nil)
		return result, updated, err
	}
	result.NewReference = ref
	result.NewReferenceSet = true
	target := calculateAutoPricingTarget(mapping, ref)
	result.TargetMultiplier = target
	result.TargetSet = true

	adminGroup, found := adminGroupMap[mapping.OwnGroup]
	if !found {
		result.Status = "skipped"
		result.Reason = "own_group_not_found_in_admin"
		updated, err := s.persistAutoPricingRunStatus(ctx, userID, adminAccountID, result, "manual", nil)
		return result, updated, err
	}
	oldOwnMultiplier := adminGroup.Multiplier
	result.OldOwnMultiplier = oldOwnMultiplier
	if adminGroup.Multiplier != nil && math.Round(*adminGroup.Multiplier*10000)/10000 == target {
		result.Status = "skipped"
		result.Reason = "target_unchanged"
		result.NewOwnMultiplier = adminGroup.Multiplier
		updated, err := s.persistAutoPricingRunStatus(ctx, userID, adminAccountID, result, "manual", nil)
		return result, updated, err
	}
	if err := s.platformService.UpdateAdminGroupMultiplier(state.Session, adminGroup, target); err != nil {
		log.Printf("[auto-pricing] 手动运行远端倍率更新失败 own_group=%s target=%.4f err=%v", mapping.OwnGroup, target, err)
		result.Status = "failed"
		result.Reason = "remote_update_failed"
		result.NewOwnMultiplier = adminGroup.Multiplier
		updated, persistErr := s.persistAutoPricingRunStatus(ctx, userID, adminAccountID, result, "manual", nil)
		return result, updated, persistErr
	}
	result.NewOwnMultiplier = pointerFloat64(target)
	result.Status = "applied"
	updated, err := s.persistAutoPricingRunStatus(ctx, userID, adminAccountID, result, "manual", pointerFloat64(target))
	if err != nil {
		return result, GroupMapping{}, err
	}
	if mapping.EnableAutoPricingNotify && len(mapping.AutoPricingNotifyBotIDs) > 0 && s.botNotifier != nil {
		msg := formatAutoPricingNotify(mapping, "manual", result, oldOwnMultiplier)
		s.botNotifier.SendToBots(ctx, userID, mapping.AutoPricingNotifyBotIDs, msg)
	}
	return result, updated, nil
}

// computeCurrentReferenceMultiplier 计算手动运行需要的当前参考倍率，不使用同步阈值或旧值快照。
func computeCurrentReferenceMultiplier(mapping GroupMapping, lookupFn func(string, string) *float64) (float64, bool, string) {
	switch mapping.AutoPricingSource {
	case "primary_upstream":
		if strings.TrimSpace(mapping.PrimaryUpstreamSiteID) == "" || strings.TrimSpace(mapping.PrimaryUpstreamGroupName) == "" {
			return 0, false, "invalid_auto_pricing_config"
		}
		multiplier := lookupFn(mapping.PrimaryUpstreamSiteID, mapping.PrimaryUpstreamGroupName)
		if multiplier == nil {
			return 0, false, "missing_reference_multiplier"
		}
		return *multiplier, true, ""
	case "lowest_upstream", "highest_upstream", "average_upstream":
		multipliers := make([]float64, 0, len(mapping.UpstreamTargets))
		for _, target := range mapping.UpstreamTargets {
			multiplier := lookupFn(target.SiteID, target.GroupName)
			if multiplier == nil {
				continue
			}
			multipliers = append(multipliers, *multiplier)
		}
		if len(multipliers) == 0 {
			return 0, false, "missing_reference_multiplier"
		}
		return aggregateMultipliers(mapping.AutoPricingSource, multipliers), true, ""
	default:
		return 0, false, "unknown_pricing_source"
	}
}

func autoPricingStatusFromResult(result autoPricingResult, trigger string, ranAt time.Time) AutoPricingRunStatus {
	status := AutoPricingRunStatus{
		Status:  result.Status,
		Reason:  result.Reason,
		Trigger: trigger,
		RanAt:   ranAt,
	}
	if result.OldReferenceSet {
		status.OldReference = pointerFloat64(result.OldReference)
	}
	if result.NewReferenceSet {
		status.NewReference = pointerFloat64(result.NewReference)
	}
	status.OldOwnMultiplier = result.OldOwnMultiplier
	status.NewOwnMultiplier = result.NewOwnMultiplier
	if result.TargetSet {
		status.TargetMultiplier = pointerFloat64(result.TargetMultiplier)
	}
	return status
}

// persistAutoPricingRunStatus 重读当前 JSON 状态后只合并服务端运行状态，降低整段 mappings 覆盖的并发风险。
func (s *Service) persistAutoPricingRunStatus(ctx context.Context, userID string, adminAccountID string, result autoPricingResult, trigger string, updatedOwnMultiplier *float64) (GroupMapping, error) {
	var updated GroupMapping
	latest, err := s.mutateState(ctx, userID, adminAccountID, func(latest *State) error {
		index := findMappingIndexByOwnGroup(latest.Mappings, result.OwnGroup)
		if index < 0 {
			return requestError(ErrorRequest)
		}
		latest.Mappings[index].LastAutoPricingRun = pointerAutoPricingRunStatus(autoPricingStatusFromResult(result, trigger, time.Now()))
		if updatedOwnMultiplier != nil {
			for i, group := range latest.OwnGroups {
				if normalizedOwnGroupKey(group.Name) == normalizedOwnGroupKey(result.OwnGroup) {
					latest.OwnGroups[i].Multiplier = *updatedOwnMultiplier
					break
				}
			}
		}
		updated = cloneGroupMappingValue(latest.Mappings[index])
		return nil
	})
	if err != nil {
		return GroupMapping{}, err
	}
	if latest == nil {
		return GroupMapping{}, requestError(ErrorRequest)
	}
	return updated, nil
}

func pointerAutoPricingRunStatus(status AutoPricingRunStatus) *AutoPricingRunStatus {
	return &status
}

// logAutoPricingResult 记录自动调价执行结果日志。
func logAutoPricingResult(siteName string, result autoPricingResult) {
	if result.PersistError != nil {
		log.Printf("[auto-pricing] 状态持久化失败 site=%s own_group=%s err=%v", siteName, result.OwnGroup, result.PersistError)
	}
	switch result.Status {
	case "applied":
		log.Printf("[auto-pricing] 已更新倍率 site=%s own_group=%s old_ref=%.4f new_ref=%.4f target=%.4f",
			siteName, result.OwnGroup, result.OldReference, result.NewReference, result.TargetMultiplier)
	case "threshold_exceeded":
		log.Printf("[auto-pricing] 阈值超限跳过 site=%s own_group=%s old_ref=%.4f new_ref=%.4f reason=%s",
			siteName, result.OwnGroup, result.OldReference, result.NewReference, result.Reason)
	case "skipped":
		log.Printf("[auto-pricing] 跳过 site=%s own_group=%s reason=%s",
			siteName, result.OwnGroup, result.Reason)
	case "failed":
		log.Printf("[auto-pricing] 执行失败 site=%s own_group=%s target=%.4f reason=%s",
			siteName, result.OwnGroup, result.TargetMultiplier, result.Reason)
	}
}

// filterEmptyStrings 过滤切片中的空字符串，保持输入顺序。
func filterEmptyStrings(ss []string) []string {
	result := make([]string, 0, len(ss))
	for _, s := range ss {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// defaultAutoPricingNotifyTemplate 自动调价成功通知的默认模板。
const defaultAutoPricingNotifyTemplate = "【自动调价】{ownGroup} 已自动从 {oldOwnMultiplier}x 调整为 {newOwnMultiplier}x。参考来源：{upstreamSiteName} / {upstreamGroupName}，参考倍率 {oldReference}x -> {newReference}x。"

// autoPricingSourceLabel 返回 AutoPricingSource 的可读说明，用于通知模板中 {upstreamGroupName} 变量。
func autoPricingSourceLabel(source string) string {
	switch source {
	case "lowest_upstream":
		return "最低倍率上游"
	case "highest_upstream":
		return "最高倍率上游"
	case "average_upstream":
		return "平均倍率"
	default:
		return ""
	}
}

// formatAutoPricingNotify 格式化自动调价成功通知消息。
// mapping 提供模板和策略配置，siteName 为触发同步的上游站点名，
// result 提供参考倍率和目标倍率，oldOwnMultiplier 为调整前的自有分组倍率。
func formatAutoPricingNotify(mapping GroupMapping, siteName string, result autoPricingResult, oldOwnMultiplier *float64) string {
	tpl := mapping.AutoPricingNotifyTemplate
	if tpl == "" {
		tpl = defaultAutoPricingNotifyTemplate
	}

	oldOwnStr := "-"
	if oldOwnMultiplier != nil {
		oldOwnStr = fmt.Sprintf("%.4f", *oldOwnMultiplier)
	}

	// {upstreamGroupName}：主上游模式用主上游分组名，聚合模式用可读来源说明
	upstreamGroupName := mapping.PrimaryUpstreamGroupName
	if mapping.AutoPricingSource != "primary_upstream" {
		label := autoPricingSourceLabel(mapping.AutoPricingSource)
		if label != "" {
			upstreamGroupName = label
		}
	}

	// {strategy} 可读策略说明
	strategyStr := "percentage"
	if mapping.AutoPricingStrategy == "fixed" {
		strategyStr = "fixed"
	}

	r := strings.NewReplacer(
		"{ownGroup}", mapping.OwnGroup,
		"{upstreamSiteName}", siteName,
		"{upstreamGroupName}", upstreamGroupName,
		"{oldReference}", fmt.Sprintf("%.4f", result.OldReference),
		"{newReference}", fmt.Sprintf("%.4f", result.NewReference),
		"{oldOwnMultiplier}", oldOwnStr,
		"{newOwnMultiplier}", fmt.Sprintf("%.4f", result.TargetMultiplier),
		"{strategy}", strategyStr,
		"{fixedIncrease}", fmt.Sprintf("%.4f", mapping.FixedIncrease),
		"{percentageIncrease}", fmt.Sprintf("%.2f", mapping.PercentageIncrease),
		"{threshold}", fmt.Sprintf("%.2f", mapping.AdjustThresholdPercent),
	)
	return r.Replace(tpl)
}

type requestError string

func (e requestError) Error() string { return string(e) }
