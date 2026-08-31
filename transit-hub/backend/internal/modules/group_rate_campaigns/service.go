package group_rate_campaigns

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"transithub/backend/internal/modules/upstream"
)

// AdminGroupOperator 是活动调价对 admin 自有分组的全部远端依赖，由 my_sites.Service 实现。
// 定义为窄接口而不是直接依赖 my_sites 包，避免跨模块的实现耦合。
type AdminGroupOperator interface {
	RequireSession(ctx context.Context, userID string, adminAccountID string) (upstream.Session, error)
	FetchAdminGroups(session upstream.Session) ([]upstream.AdminGroupInfo, error)
	UpdateAdminGroupMultiplier(session upstream.Session, group upstream.AdminGroupInfo, multiplier float64) error
}

// BotNotifier 机器人通知发送接口，由 settings.Service 实现。
type BotNotifier interface {
	SendToBots(ctx context.Context, userID string, botIDs []string, message string)
}

// GroupTypeLookup 按分组倍率页面已有的 type/search/platform 条件查询匹配的分组名，
// 由 group_rates.Service 实现，用于 "按分组类型" 和 "当前筛选结果" 两种选择模式。
type GroupTypeLookup interface {
	ListGroupNames(ctx context.Context, userID string, adminAccountID string, search string, groupType string, platform string) ([]string, error)
}

type AdminAccountResolver interface {
	RequireCurrentID(ctx context.Context, userID string) (string, error)
}

// AutoPricingReconciler 在活动释放分组控制权时，按最新自动调价目标重新计算倍率。
// my_sites.Service 通过结构性实现注入，避免两个业务模块互相 import。
type AutoPricingReconciler interface {
	ReconcileAutoPricingMultiplier(ctx context.Context, userID, adminAccountID, ownGroup string, fallback float64) (*float64, error)
}

// campaignRepository 是 Service 对存储层的全部依赖，由 *Repository 结构性满足。
// 定义为接口而不是直接依赖 *Repository 具体类型，使 startCampaign/endCampaign 等
// 涉及状态抢占的核心流程可以在不连接真实数据库的情况下用内存假实现单测覆盖。
type campaignRepository interface {
	EnsureSchema(ctx context.Context) error
	Insert(ctx context.Context, c Campaign) error
	Update(ctx context.Context, c Campaign) error
	Delete(ctx context.Context, userID, adminAccountID, id string) (bool, error)
	ListActiveGroupNames(ctx context.Context, userID, adminAccountID string) ([]string, error)
	SaveItems(ctx context.Context, items []CampaignItem) error
	UpdateItemApply(ctx context.Context, id string, originalMultiplier *float64, status string, reason string, appliedAt *time.Time) error
	UpdateItemCampaignMultiplier(ctx context.Context, id string, multiplier float64) error
	UpdateItemRestore(ctx context.Context, id string, restoredMultiplier *float64, status string, reason string, restoredAt *time.Time) error
	ListItems(ctx context.Context, campaignID string) ([]CampaignItem, error)
	ListItemsForCycle(ctx context.Context, campaignID string, cycle int) ([]CampaignItem, error)
	Get(ctx context.Context, userID string, adminAccountID string, id string) (*Campaign, error)
	GetByID(ctx context.Context, id string) (*Campaign, error)
	List(ctx context.Context, userID string, adminAccountID string, query ListQuery) ([]Campaign, int, error)
	ListDueScheduled(ctx context.Context, now time.Time) ([]string, error)
	ListDueRunning(ctx context.Context, now time.Time) ([]string, error)
	ClaimForRunning(ctx context.Context, id string) (bool, error)
	ClaimForEnding(ctx context.Context, id string) (bool, error)
	FinishStart(ctx context.Context, id string, status string) error
	FinishEnd(ctx context.Context, id string, status string) error
	RescheduleAfterEnd(ctx context.Context, id string, nextStart time.Time, nextEnd time.Time) error
	ClaimForCancel(ctx context.Context, id string) (bool, error)
}

// Config 是活动调价模块的环境变量默认值，均只作为创建活动时未填字段的兜底，不强制覆盖用户选择。
type Config struct {
	NotifyEnabledDefault bool
	DefaultNotifyBotIDs  []string
	StartTemplateDefault string
	EndTemplateDefault   string
	SchedulerInterval    time.Duration
}

type Service struct {
	repository campaignRepository
	operator   AdminGroupOperator
	notifier   BotNotifier
	reconciler AutoPricingReconciler
	typeLookup GroupTypeLookup
	accounts   AdminAccountResolver
	config     Config
	mu         sync.Mutex
}

func NewService(repository *Repository, operator AdminGroupOperator, notifier BotNotifier, typeLookup GroupTypeLookup, config Config) *Service {
	return &Service{repository: repository, operator: operator, notifier: notifier, typeLookup: typeLookup, config: config}
}

func (s *Service) EnsureSchema(ctx context.Context) error {
	return s.repository.EnsureSchema(ctx)
}

func (s *Service) SetAdminAccountResolver(accounts AdminAccountResolver) {
	s.accounts = accounts
}

func (s *Service) SetAutoPricingReconciler(reconciler AutoPricingReconciler) {
	s.reconciler = reconciler
}

// IsGroupRateCampaignActive reports whether a current campaign cycle owns a group.
// It is called by the automatic-pricing service before it writes a remote multiplier.
func (s *Service) IsGroupRateCampaignActive(ctx context.Context, userID, adminAccountID, groupName string) bool {
	names, err := s.repository.ListActiveGroupNames(ctx, userID, adminAccountID)
	if err != nil {
		log.Printf("[group-rate-campaigns] scan active group overrides failed user_id=%s admin_account_id=%s err=%v", userID, adminAccountID, err)
		return false
	}
	target := strings.TrimSpace(groupName)
	for _, name := range names {
		if strings.EqualFold(strings.TrimSpace(name), target) {
			return true
		}
	}
	return false
}

func (s *Service) currentAdminAccountID(ctx context.Context, userID string) (string, error) {
	if s.accounts == nil {
		return "", ErrNoCurrentAccount
	}
	return s.accounts.RequireCurrentID(ctx, userID)
}

// Preview 只读计算活动将影响的分组、原倍率和活动倍率，不落库、不修改远端倍率、不发送通知。
func (s *Service) Preview(ctx context.Context, userID string, adminAccountID string, req CreateCampaignRequest) (PreviewResponse, error) {
	req.Notify = normalizeNotify(req.Notify, s.config)
	if err := validateCreateRequest(req, time.Now()); err != nil {
		return PreviewResponse{}, err
	}
	session, err := s.operator.RequireSession(ctx, userID, adminAccountID)
	if err != nil {
		return PreviewResponse{}, err
	}
	groups, err := s.operator.FetchAdminGroups(session)
	if err != nil {
		return PreviewResponse{}, err
	}
	targets, err := s.resolveManualSelectionWithRates(req.Selection, groups)
	if err != nil {
		return PreviewResponse{}, err
	}
	items := make([]PreviewItem, 0, len(targets))
	for _, target := range targets {
		original := floatOrZero(target.group.Multiplier)
		items = append(items, PreviewItem{
			GroupID:            target.group.ID,
			GroupName:          target.group.Name,
			OriginalMultiplier: original,
			CampaignMultiplier: target.campaignMultiplier,
			RestoredMultiplier: original,
		})
	}
	return PreviewResponse{Items: items, Total: len(items)}, nil
}

// Create 校验并落库一个新活动。startMode=now 时落库后立即同步执行开启逻辑，
// 使 handler 返回时活动已经处于最终状态（running/partial/failed）。
func (s *Service) Create(ctx context.Context, userID string, adminAccountID string, req CreateCampaignRequest) (CampaignDetail, error) {
	now := time.Now()
	req.Notify = normalizeNotify(req.Notify, s.config)
	req.Schedule.Recurrence = normalizeRecurrence(req.Schedule.Recurrence)
	if err := validateCreateRequest(req, now); err != nil {
		return CampaignDetail{}, err
	}

	id, err := newID()
	if err != nil {
		return CampaignDetail{}, err
	}

	status := StatusDraft
	startAt := req.Schedule.StartAt
	switch req.Schedule.StartMode {
	case StartNow:
		// now 模式复用 scheduled -> running 的执行路径，start_at 记为发起时间用于展示。
		status = StatusScheduled
		t := now
		startAt = &t
	case StartScheduled:
		status = StatusScheduled
	case StartDraft:
		status = StatusDraft
	}

	campaign := Campaign{
		ID:             id,
		UserID:         userID,
		AdminAccountID: adminAccountID,
		Name:           strings.TrimSpace(req.Name),
		Description:    req.Description,
		Status:         status,
		Selection:      req.Selection,
		Adjustment:     req.Adjustment,
		Notify:         req.Notify,
		StartMode:      req.Schedule.StartMode,
		StartAt:        startAt,
		EndMode:        req.Schedule.EndMode,
		EndAt:          req.Schedule.EndAt,
		Recurrence:     req.Schedule.Recurrence,
		Cycle:          0,
	}
	if err := s.repository.Insert(ctx, campaign); err != nil {
		return CampaignDetail{}, err
	}

	if req.Schedule.StartMode == StartNow {
		s.startCampaign(ctx, id)
	}

	return s.Get(ctx, userID, adminAccountID, id)
}

// Update 修改活动配置。进行中的活动会在当前轮立即应用分组增删和倍率变化。
func (s *Service) Update(ctx context.Context, userID, adminAccountID, id string, req UpdateCampaignRequest) (CampaignDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	campaign, err := s.repository.Get(ctx, userID, adminAccountID, id)
	if err != nil {
		return CampaignDetail{}, err
	}
	if campaign == nil {
		return CampaignDetail{}, ErrNotFound
	}
	req.Notify = normalizeNotify(req.Notify, s.config)
	req.Schedule.Recurrence = normalizeRecurrence(req.Schedule.Recurrence)
	if err := validateUpdateRequest(req, *campaign); err != nil {
		return CampaignDetail{}, err
	}

	switch campaign.Status {
	case StatusDraft, StatusScheduled:
		updated := applyCampaignRequest(*campaign, req)
		if err := s.repository.Update(ctx, updated); err != nil {
			return CampaignDetail{}, err
		}
	case StatusRunning, StatusPartial:
		if s.operator == nil {
			return CampaignDetail{}, ErrInvalidState
		}
		updated := applyCampaignRequest(*campaign, req)
		// A running cycle keeps its actual start boundary. Only its end and
		// recurrence settings are editable while it is active.
		updated.StartMode = campaign.StartMode
		updated.StartAt = campaign.StartAt
		if err := s.repository.Update(ctx, updated); err != nil {
			return CampaignDetail{}, err
		}
		if err := s.applyRunningCampaignUpdate(ctx, &updated); err != nil {
			log.Printf("[group-rate-campaigns] apply running campaign update failed id=%s err=%v", id, err)
			return CampaignDetail{}, err
		}
	default:
		return CampaignDetail{}, ErrInvalidState
	}
	return s.Get(ctx, userID, adminAccountID, id)
}

// StopAfterCurrentCycle cancels only future recurrence. The current running
// cycle remains active and will be restored normally at its scheduled end.
func (s *Service) StopAfterCurrentCycle(ctx context.Context, userID, adminAccountID, id string) (CampaignDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	campaign, err := s.repository.Get(ctx, userID, adminAccountID, id)
	if err != nil {
		return CampaignDetail{}, err
	}
	if campaign == nil {
		return CampaignDetail{}, ErrNotFound
	}
	if campaign.Status != StatusRunning && campaign.Status != StatusPartial {
		return CampaignDetail{}, ErrInvalidState
	}
	campaign.Recurrence = Recurrence{Frequency: RecurrenceNone, Interval: 0}
	if err := s.repository.Update(ctx, *campaign); err != nil {
		return CampaignDetail{}, err
	}
	return s.Get(ctx, userID, adminAccountID, id)
}

// Delete hides a completed or not-yet-started campaign while retaining its
// execution items for operational history.
func (s *Service) Delete(ctx context.Context, userID, adminAccountID, id string) error {
	campaign, err := s.repository.Get(ctx, userID, adminAccountID, id)
	if err != nil {
		return err
	}
	if campaign == nil {
		return ErrNotFound
	}
	if campaign.Status == StatusRunning || campaign.Status == StatusPartial {
		return ErrInvalidState
	}
	ok, err := s.repository.Delete(ctx, userID, adminAccountID, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidState
	}
	return nil
}

func applyCampaignRequest(c Campaign, req CreateCampaignRequest) Campaign {
	c.Name = strings.TrimSpace(req.Name)
	c.Description = strings.TrimSpace(req.Description)
	c.Selection = req.Selection
	c.Adjustment = req.Adjustment
	c.Notify = req.Notify
	c.StartMode = req.Schedule.StartMode
	c.StartAt = req.Schedule.StartAt
	c.EndMode = req.Schedule.EndMode
	c.EndAt = req.Schedule.EndAt
	c.Recurrence = normalizeRecurrence(req.Schedule.Recurrence)
	return c
}

func (s *Service) applyRunningCampaignUpdate(ctx context.Context, campaign *Campaign) error {
	session, err := s.operator.RequireSession(ctx, campaign.UserID, campaign.AdminAccountID)
	if err != nil {
		return err
	}
	groups, err := s.operator.FetchAdminGroups(session)
	if err != nil {
		return err
	}
	targets, err := s.resolveManualSelectionWithRates(campaign.Selection, groups)
	if err != nil {
		return err
	}
	targetByName := make(map[string]manualGroupTarget, len(targets))
	for _, target := range targets {
		targetByName[strings.TrimSpace(target.group.Name)] = target
	}

	items, err := s.repository.ListItemsForCycle(ctx, campaign.ID, campaign.Cycle)
	if err != nil {
		return err
	}
	activeByName := make(map[string]CampaignItem, len(items))
	for _, item := range items {
		if item.ApplyStatus == ItemApplied && item.RestoreStatus == ItemPending {
			activeByName[strings.TrimSpace(item.GroupName)] = item
		}
	}

	failed := 0
	for name, item := range activeByName {
		target, stillSelected := targetByName[name]
		if !stillSelected {
			if _, err := s.restoreCampaignItem(ctx, session, groups, item); err != nil {
				failed++
			}
			continue
		}
		if multiplierChanged(item.CampaignMultiplier, target.campaignMultiplier) {
			if err := s.operator.UpdateAdminGroupMultiplier(session, target.group, target.campaignMultiplier); err != nil {
				_ = s.repository.UpdateItemApply(ctx, item.ID, item.OriginalMultiplier, ItemFailed, err.Error(), nil)
				failed++
				continue
			}
			if err := s.repository.UpdateItemCampaignMultiplier(ctx, item.ID, target.campaignMultiplier); err != nil {
				return err
			}
		}
		delete(targetByName, name)
	}

	newItems := make([]CampaignItem, 0, len(targetByName))
	for _, target := range targetByName {
		itemID, err := newID()
		if err != nil {
			return err
		}
		item := CampaignItem{
			ID:                 itemID,
			CampaignID:         campaign.ID,
			UserID:             campaign.UserID,
			AdminAccountID:     campaign.AdminAccountID,
			GroupID:            target.group.ID,
			GroupName:          target.group.Name,
			Cycle:              campaign.Cycle,
			OriginalMultiplier: target.group.Multiplier,
			CampaignMultiplier: target.campaignMultiplier,
		}
		newItems = append(newItems, item)
	}
	if err := s.repository.SaveItems(ctx, newItems); err != nil {
		return err
	}
	for _, item := range newItems {
		target := targetByName[strings.TrimSpace(item.GroupName)]
		original := floatOrZero(item.OriginalMultiplier)
		if !multiplierChanged(original, target.campaignMultiplier) {
			if err := s.repository.UpdateItemApply(ctx, item.ID, item.OriginalMultiplier, ItemApplied, "", timePtr(time.Now())); err != nil {
				return err
			}
			continue
		}
		if err := s.operator.UpdateAdminGroupMultiplier(session, target.group, target.campaignMultiplier); err != nil {
			_ = s.repository.UpdateItemApply(ctx, item.ID, item.OriginalMultiplier, ItemFailed, err.Error(), nil)
			failed++
			continue
		}
		if err := s.repository.UpdateItemApply(ctx, item.ID, item.OriginalMultiplier, ItemApplied, "", timePtr(time.Now())); err != nil {
			return err
		}
	}
	if failed > 0 {
		return s.repository.FinishStart(ctx, campaign.ID, StatusPartial)
	}
	return s.repository.FinishStart(ctx, campaign.ID, StatusRunning)
}

func (s *Service) restoreCampaignItem(ctx context.Context, session upstream.Session, groups []upstream.AdminGroupInfo, item CampaignItem) (float64, error) {
	target := floatOrZero(item.OriginalMultiplier)
	if s.reconciler != nil {
		resolved, err := s.reconciler.ReconcileAutoPricingMultiplier(ctx, item.UserID, item.AdminAccountID, item.GroupName, target)
		if err != nil {
			_ = s.repository.UpdateItemRestore(ctx, item.ID, nil, ItemFailed, err.Error(), nil)
			return 0, err
		}
		if resolved != nil {
			target = *resolved
		}
	}
	groupInfo := findGroup(groups, item.GroupName)
	if groupInfo == nil {
		err := errors.New("group not found")
		_ = s.repository.UpdateItemRestore(ctx, item.ID, nil, ItemFailed, err.Error(), nil)
		return 0, err
	}
	current := floatOrZero(groupInfo.Multiplier)
	if multiplierChanged(current, target) {
		if err := s.operator.UpdateAdminGroupMultiplier(session, *groupInfo, target); err != nil {
			_ = s.repository.UpdateItemRestore(ctx, item.ID, nil, ItemFailed, err.Error(), nil)
			return 0, err
		}
	}
	status := ItemRestored
	if !multiplierChanged(current, target) {
		status = ItemUnchanged
	}
	if err := s.repository.UpdateItemRestore(ctx, item.ID, &target, status, "", timePtr(time.Now())); err != nil {
		return 0, err
	}
	return target, nil
}

func (s *Service) Get(ctx context.Context, userID string, adminAccountID string, id string) (CampaignDetail, error) {
	campaign, err := s.repository.Get(ctx, userID, adminAccountID, id)
	if err != nil {
		return CampaignDetail{}, err
	}
	if campaign == nil {
		return CampaignDetail{}, ErrNotFound
	}
	items, err := s.repository.ListItemsForCycle(ctx, id, readCycle(*campaign))
	if err != nil {
		return CampaignDetail{}, err
	}
	return toDetail(*campaign, items), nil
}

func (s *Service) List(ctx context.Context, userID string, adminAccountID string, query ListQuery) (ListResult, error) {
	query = normalizeListQuery(query)
	campaigns, total, err := s.repository.List(ctx, userID, adminAccountID, query)
	if err != nil {
		return ListResult{}, err
	}
	listItems := make([]CampaignListItem, 0, len(campaigns))
	for _, c := range campaigns {
		items, err := s.repository.ListItemsForCycle(ctx, c.ID, readCycle(c))
		if err != nil {
			return ListResult{}, err
		}
		listItems = append(listItems, toListItem(c, items))
	}
	return ListResult{
		Items:      listItems,
		Total:      total,
		Page:       query.Page,
		PageSize:   query.PageSize,
		TotalPages: totalPages(total, query.PageSize),
		Defaults:   s.Defaults(),
	}, nil
}

// Defaults 返回环境变量提供的通知默认值，供前端创建活动时预填表单。
func (s *Service) Defaults() NotifyDefaults {
	return NotifyDefaults{
		Enabled:       s.config.NotifyEnabledDefault,
		BotIDs:        s.config.DefaultNotifyBotIDs,
		StartTemplate: s.config.StartTemplateDefault,
		EndTemplate:   s.config.EndTemplateDefault,
	}
}

// StartNow 是"立即开始"的 HTTP 入口，只允许 draft/scheduled 活动。
func (s *Service) StartNow(ctx context.Context, userID string, adminAccountID string, id string) (CampaignDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	campaign, err := s.repository.Get(ctx, userID, adminAccountID, id)
	if err != nil {
		return CampaignDetail{}, err
	}
	if campaign == nil {
		return CampaignDetail{}, ErrNotFound
	}
	if campaign.Status != StatusDraft && campaign.Status != StatusScheduled {
		return CampaignDetail{}, ErrInvalidState
	}
	s.startCampaignLocked(ctx, id)
	return s.Get(ctx, userID, adminAccountID, id)
}

// End 是"手动结束"的 HTTP 入口，允许 running 和 partial 活动。
// partial 活动开启阶段部分分组已经真实改价，必须能通过手动结束恢复这些分组，
// 否则会遗留未恢复的倍率，与前端详情页允许 partial 点击结束的行为不一致。
func (s *Service) End(ctx context.Context, userID string, adminAccountID string, id string) (CampaignDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	campaign, err := s.repository.Get(ctx, userID, adminAccountID, id)
	if err != nil {
		return CampaignDetail{}, err
	}
	if campaign == nil {
		return CampaignDetail{}, ErrNotFound
	}
	if campaign.Status != StatusRunning && campaign.Status != StatusPartial {
		return CampaignDetail{}, ErrInvalidState
	}
	s.endCampaignLocked(ctx, id, false)
	return s.Get(ctx, userID, adminAccountID, id)
}

// Cancel 只允许取消尚未执行的 draft/scheduled 活动；running 及之后的状态必须走"手动结束"恢复原倍率。
func (s *Service) Cancel(ctx context.Context, userID string, adminAccountID string, id string) (CampaignDetail, error) {
	campaign, err := s.repository.Get(ctx, userID, adminAccountID, id)
	if err != nil {
		return CampaignDetail{}, err
	}
	if campaign == nil {
		return CampaignDetail{}, ErrNotFound
	}
	ok, err := s.repository.ClaimForCancel(ctx, id)
	if err != nil {
		return CampaignDetail{}, err
	}
	if !ok {
		return CampaignDetail{}, ErrInvalidState
	}
	return s.Get(ctx, userID, adminAccountID, id)
}

// StartScheduler 启动后台调度协程：定期扫描到期的 scheduled/running 活动并执行开启/恢复。
// 启动时立即跑一轮，覆盖"进程重启后自动处理到期 running 活动"的要求。
func (s *Service) StartScheduler(ctx context.Context) {
	interval := s.config.SchedulerInterval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	go func() {
		s.runSchedulerTick(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runSchedulerTick(ctx)
			}
		}
	}()
}

func (s *Service) runSchedulerTick(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	dueScheduled, err := s.repository.ListDueScheduled(ctx, now)
	if err != nil {
		log.Printf("[group-rate-campaigns] scan due scheduled failed: %v", err)
	} else {
		for _, id := range dueScheduled {
			s.startCampaignLocked(ctx, id)
		}
	}
	dueRunning, err := s.repository.ListDueRunning(ctx, now)
	if err != nil {
		log.Printf("[group-rate-campaigns] scan due running failed: %v", err)
	} else {
		for _, id := range dueRunning {
			s.endCampaignLocked(ctx, id, true)
		}
	}
}

// startCampaign 执行一个活动的开启动作：抢占状态 -> 拉取会话与自有分组 -> 解析目标 -> 逐个改倍率。
// 单个分组失败不中断其余分组；调度器和手动"立即开始"共用此逻辑，条件更新保证幂等。
func (s *Service) startCampaign(ctx context.Context, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startCampaignLocked(ctx, id)
}

func (s *Service) startCampaignLocked(ctx context.Context, id string) {
	claimed, err := s.repository.ClaimForRunning(ctx, id)
	if err != nil {
		log.Printf("[group-rate-campaigns] claim for running failed id=%s err=%v", id, err)
		return
	}
	if !claimed {
		return
	}

	campaign, err := s.repository.GetByID(ctx, id)
	if err != nil || campaign == nil {
		log.Printf("[group-rate-campaigns] load campaign failed id=%s err=%v", id, err)
		_ = s.repository.FinishStart(ctx, id, StatusFailed)
		return
	}

	session, err := s.operator.RequireSession(ctx, campaign.UserID, campaign.AdminAccountID)
	if err != nil {
		log.Printf("[group-rate-campaigns] require session failed id=%s err=%v", id, err)
		_ = s.repository.FinishStart(ctx, id, StatusFailed)
		return
	}
	groups, err := s.operator.FetchAdminGroups(session)
	if err != nil {
		log.Printf("[group-rate-campaigns] fetch admin groups failed id=%s err=%v", id, err)
		_ = s.repository.FinishStart(ctx, id, StatusFailed)
		return
	}
	targets, err := s.resolveManualSelectionWithRates(campaign.Selection, groups)
	if err != nil {
		log.Printf("[group-rate-campaigns] resolve selection failed id=%s err=%v", id, err)
		_ = s.repository.FinishStart(ctx, id, StatusFailed)
		return
	}

	// 每个分组的活动倍率在创建活动时已经过 validateManualGroupRates 校验（有限数字、>= 0），
	// 这里不再需要 applyAdjustment 计算或处理逐分组的计算错误。
	pendings := make([]CampaignItem, 0, len(targets))
	for _, target := range targets {
		itemID, err := newID()
		if err != nil {
			continue
		}
		pendings = append(pendings, CampaignItem{
			ID:                 itemID,
			CampaignID:         id,
			UserID:             campaign.UserID,
			AdminAccountID:     campaign.AdminAccountID,
			GroupID:            target.group.ID,
			GroupName:          target.group.Name,
			Cycle:              campaign.Cycle,
			OriginalMultiplier: target.group.Multiplier,
			CampaignMultiplier: target.campaignMultiplier,
		})
	}

	if err := s.repository.SaveItems(ctx, pendings); err != nil {
		log.Printf("[group-rate-campaigns] save items failed id=%s err=%v", id, err)
		_ = s.repository.FinishStart(ctx, id, StatusFailed)
		return
	}

	applied, failed := 0, 0
	appliedAt := time.Now()
	for _, item := range pendings {
		originalValue := floatOrZero(item.OriginalMultiplier)
		if !multiplierChanged(originalValue, item.CampaignMultiplier) {
			_ = s.repository.UpdateItemApply(ctx, item.ID, item.OriginalMultiplier, ItemApplied, "", &appliedAt)
			applied++
			continue
		}
		groupInfo := findGroup(groups, item.GroupName)
		if groupInfo == nil {
			_ = s.repository.UpdateItemApply(ctx, item.ID, item.OriginalMultiplier, ItemFailed, "group not found", nil)
			failed++
			continue
		}
		if err := s.operator.UpdateAdminGroupMultiplier(session, *groupInfo, item.CampaignMultiplier); err != nil {
			_ = s.repository.UpdateItemApply(ctx, item.ID, item.OriginalMultiplier, ItemFailed, err.Error(), nil)
			failed++
			continue
		}
		_ = s.repository.UpdateItemApply(ctx, item.ID, item.OriginalMultiplier, ItemApplied, "", &appliedAt)
		applied++
	}

	status := StatusRunning
	if failed > 0 && applied == 0 {
		status = StatusFailed
	} else if failed > 0 {
		status = StatusPartial
	}
	if err := s.repository.FinishStart(ctx, id, status); err != nil {
		log.Printf("[group-rate-campaigns] finish start failed id=%s err=%v", id, err)
	}

	s.notifyStart(ctx, campaign, applied, failed, len(pendings))
}

// endCampaign 执行一个活动的恢复动作：抢占执行权 -> 拉取会话与自有分组 -> 逐个恢复。
// 通知失败或会话获取失败都不回滚已恢复的分组，也不阻塞活动状态流转到 partial/ended。
func (s *Service) endCampaign(ctx context.Context, id string, allowReschedule bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endCampaignLocked(ctx, id, allowReschedule)
}

func (s *Service) endCampaignLocked(ctx context.Context, id string, allowReschedule bool) {
	claimed, err := s.repository.ClaimForEnding(ctx, id)
	if err != nil {
		log.Printf("[group-rate-campaigns] claim for ending failed id=%s err=%v", id, err)
		return
	}
	if !claimed {
		return
	}

	campaign, err := s.repository.GetByID(ctx, id)
	if err != nil || campaign == nil {
		log.Printf("[group-rate-campaigns] load campaign failed id=%s err=%v", id, err)
		_ = s.repository.FinishEnd(ctx, id, StatusPartial)
		return
	}
	items, err := s.repository.ListItemsForCycle(ctx, id, campaign.Cycle)
	if err != nil {
		log.Printf("[group-rate-campaigns] list items failed id=%s err=%v", id, err)
		_ = s.repository.FinishEnd(ctx, id, StatusPartial)
		return
	}

	session, err := s.operator.RequireSession(ctx, campaign.UserID, campaign.AdminAccountID)
	if err != nil {
		log.Printf("[group-rate-campaigns] require session failed id=%s err=%v", id, err)
		failed := s.markAllRestoreFailed(ctx, items, err.Error())
		_ = s.repository.FinishEnd(ctx, id, endStatus(failed))
		s.notifyEnd(ctx, campaign, 0, failed)
		return
	}
	groups, err := s.operator.FetchAdminGroups(session)
	if err != nil {
		log.Printf("[group-rate-campaigns] fetch admin groups failed id=%s err=%v", id, err)
		failed := s.markAllRestoreFailed(ctx, items, err.Error())
		_ = s.repository.FinishEnd(ctx, id, endStatus(failed))
		s.notifyEnd(ctx, campaign, 0, failed)
		return
	}

	restored, failed := 0, 0
	restoredAt := time.Now()
	for _, item := range items {
		if item.ApplyStatus != ItemApplied {
			continue
		}
		if item.RestoreStatus == ItemRestored || item.RestoreStatus == ItemUnchanged {
			continue
		}
		if _, err := s.restoreCampaignItem(ctx, session, groups, item); err != nil {
			failed++
			continue
		}
		restored++
	}

	status := endStatus(failed)
	if allowReschedule && status == StatusEnded {
		if nextStart, nextEnd, ok := nextRecurrenceTimes(*campaign, restoredAt); ok {
			if err := s.repository.RescheduleAfterEnd(ctx, id, *nextStart, *nextEnd); err != nil {
				log.Printf("[group-rate-campaigns] reschedule recurring campaign failed id=%s err=%v", id, err)
				status = StatusEnded
			} else {
				s.notifyEnd(ctx, campaign, restored, failed)
				return
			}
		}
	}
	if err := s.repository.FinishEnd(ctx, id, status); err != nil {
		log.Printf("[group-rate-campaigns] finish end failed id=%s err=%v", id, err)
	}
	s.notifyEnd(ctx, campaign, restored, failed)
}

func endStatus(failed int) string {
	if failed > 0 {
		return StatusPartial
	}
	return StatusEnded
}

// markAllRestoreFailed 把所有仍待恢复的分组标记为失败，用于获取会话/拉取分组失败的兜底路径。
func (s *Service) markAllRestoreFailed(ctx context.Context, items []CampaignItem, reason string) int {
	count := 0
	for _, item := range items {
		if item.ApplyStatus != ItemApplied {
			continue
		}
		if item.RestoreStatus == ItemRestored || item.RestoreStatus == ItemUnchanged {
			continue
		}
		_ = s.repository.UpdateItemRestore(ctx, item.ID, nil, ItemFailed, reason, nil)
		count++
	}
	return count
}

// manualGroupTarget 是"手动选择分组 + 固定活动倍率"模式下，一个目标分组及其解析出的远端信息。
type manualGroupTarget struct {
	group              upstream.AdminGroupInfo
	campaignMultiplier float64
}

// resolveManualSelectionWithRates 把手动选择的分组名和各自的固定活动倍率解析成具体的 admin 自有分组。
// 每个引用的分组名都必须能在远端自有分组中找到，找不到时整体返回 ErrEmptySelection，
// 避免用户误以为部分未匹配的分组已经生效。
// campaignMultiplier 缺失的引用（历史遗留数据）会被跳过，不参与本次开始/预览。
func (s *Service) resolveManualSelectionWithRates(selection Selection, groups []upstream.AdminGroupInfo) ([]manualGroupTarget, error) {
	targets := make([]manualGroupTarget, 0, len(selection.Groups))
	for _, ref := range selection.Groups {
		name := strings.TrimSpace(ref.GroupName)
		if name == "" || ref.CampaignMultiplier == nil {
			continue
		}
		groupInfo := findGroup(groups, name)
		if groupInfo == nil {
			return nil, ErrEmptySelection
		}
		targets = append(targets, manualGroupTarget{group: *groupInfo, campaignMultiplier: *ref.CampaignMultiplier})
	}
	if len(targets) == 0 {
		return nil, ErrEmptySelection
	}
	return targets, nil
}

// resolveSelection 把活动的选择范围解析成具体的 admin 自有分组列表。
// all/manual 直接匹配；type/currentFilter 通过 GroupTypeLookup 取分组名集合后与自有分组名取交集。
// 新建活动只走 resolveManualSelectionWithRates；这里保留给历史活动数据结构和既有测试使用。
func (s *Service) resolveSelection(ctx context.Context, userID string, adminAccountID string, selection Selection, groups []upstream.AdminGroupInfo) ([]upstream.AdminGroupInfo, error) {
	var names map[string]struct{}
	switch selection.Mode {
	case SelectionAll:
		// names == nil 表示不过滤，全部自有分组都是目标。
	case SelectionManual:
		names = make(map[string]struct{}, len(selection.Groups))
		for _, g := range selection.Groups {
			name := strings.TrimSpace(g.GroupName)
			if name != "" {
				names[name] = struct{}{}
			}
		}
	case SelectionType:
		resolved, err := s.lookupNamesByTypes(ctx, userID, adminAccountID, selection.Types)
		if err != nil {
			return nil, err
		}
		names = toSet(resolved)
	case SelectionCurrentFilter:
		if s.typeLookup == nil {
			return nil, ErrEmptySelection
		}
		resolved, err := s.typeLookup.ListGroupNames(ctx, userID, adminAccountID, selection.Filter.Search, selection.Filter.Type, selection.Filter.Platform)
		if err != nil {
			return nil, err
		}
		names = toSet(resolved)
	default:
		return nil, ErrEmptySelection
	}

	var targets []upstream.AdminGroupInfo
	if names == nil {
		targets = groups
	} else {
		for _, g := range groups {
			if _, ok := names[strings.TrimSpace(g.Name)]; ok {
				targets = append(targets, g)
			}
		}
	}
	if len(targets) == 0 {
		return nil, ErrEmptySelection
	}
	return targets, nil
}

func (s *Service) lookupNamesByTypes(ctx context.Context, userID string, adminAccountID string, types []string) ([]string, error) {
	if s.typeLookup == nil {
		return nil, nil
	}
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		names, err := s.typeLookup.ListGroupNames(ctx, userID, adminAccountID, "", t, "")
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				result = append(result, name)
			}
		}
	}
	return result, nil
}

// notifyStart 发送活动开始通知：开启成功、部分失败或失败均发送，失败只记录日志不阻塞流程。
func (s *Service) notifyStart(ctx context.Context, campaign *Campaign, applied int, failed int, total int) {
	if s.notifier == nil || !campaign.Notify.Enabled || len(campaign.Notify.BotIDs) == 0 {
		return
	}
	tpl := campaign.Notify.StartTemplate
	if strings.TrimSpace(tpl) == "" {
		tpl = s.config.StartTemplateDefault
	}
	if strings.TrimSpace(tpl) == "" {
		return
	}
	vars := map[string]string{
		"activityName": campaign.Name,
		"description":  campaign.Description,
		"totalCount":   strconv.Itoa(total),
		"changedCount": strconv.Itoa(applied),
		"failedCount":  strconv.Itoa(failed),
		"startAt":      formatTime(campaign.StartAt),
		"endAt":        formatTime(campaign.EndAt),
		"operator":     campaign.UserID,
		"executedAt":   formatTime(timePtr(time.Now())),
	}
	message := renderTemplate(tpl, vars)
	s.notifier.SendToBots(ctx, campaign.UserID, campaign.Notify.BotIDs, message)
}

// notifyEnd 发送活动结束通知：恢复成功、部分失败均发送，失败只记录日志不阻塞流程。
func (s *Service) notifyEnd(ctx context.Context, campaign *Campaign, restored int, restoreFailed int) {
	if s.notifier == nil || !campaign.Notify.Enabled || len(campaign.Notify.BotIDs) == 0 {
		return
	}
	tpl := campaign.Notify.EndTemplate
	if strings.TrimSpace(tpl) == "" {
		tpl = s.config.EndTemplateDefault
	}
	if strings.TrimSpace(tpl) == "" {
		return
	}
	vars := map[string]string{
		"activityName":       campaign.Name,
		"description":        campaign.Description,
		"restoredCount":      strconv.Itoa(restored),
		"restoreFailedCount": strconv.Itoa(restoreFailed),
		"startAt":            formatTime(campaign.StartAt),
		"endAt":              formatTime(campaign.EndAt),
		"operator":           campaign.UserID,
		"executedAt":         formatTime(timePtr(time.Now())),
	}
	message := renderTemplate(tpl, vars)
	s.notifier.SendToBots(ctx, campaign.UserID, campaign.Notify.BotIDs, message)
}

// normalizeNotify 用环境变量默认值补全未启用/未填写的通知字段，只在字段为空时兜底，不覆盖显式选择。
func normalizeNotify(n Notify, cfg Config) Notify {
	if !n.Enabled {
		return Notify{Enabled: false}
	}
	if len(n.BotIDs) == 0 {
		n.BotIDs = cfg.DefaultNotifyBotIDs
	}
	if strings.TrimSpace(n.StartTemplate) == "" {
		n.StartTemplate = cfg.StartTemplateDefault
	}
	if strings.TrimSpace(n.EndTemplate) == "" {
		n.EndTemplate = cfg.EndTemplateDefault
	}
	return n
}

func normalizeListQuery(q ListQuery) ListQuery {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 20
	}
	if q.PageSize > 100 {
		q.PageSize = 100
	}
	q.Status = strings.TrimSpace(q.Status)
	return q
}

func totalPages(total int, pageSize int) int {
	if total == 0 || pageSize <= 0 {
		return 0
	}
	pages := total / pageSize
	if total%pageSize != 0 {
		pages++
	}
	return pages
}

func readCycle(c Campaign) int {
	if c.Cycle > 0 && (c.Status == StatusScheduled || c.Status == StatusCancelled) {
		return c.Cycle - 1
	}
	return c.Cycle
}

func toListItem(c Campaign, items []CampaignItem) CampaignListItem {
	summary := buildSummary(items)
	lastExecuted := c.StartedAt
	if c.EndedAt != nil {
		lastExecuted = c.EndedAt
	}
	return CampaignListItem{
		ID:             c.ID,
		Name:           c.Name,
		Status:         c.Status,
		StartMode:      c.StartMode,
		StartAt:        c.StartAt,
		EndMode:        c.EndMode,
		EndAt:          c.EndAt,
		Recurrence:     normalizeRecurrence(c.Recurrence),
		StartedAt:      c.StartedAt,
		EndedAt:        c.EndedAt,
		Summary:        summary,
		NotifyEnabled:  c.Notify.Enabled,
		CreatedBy:      c.UserID,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
		LastExecutedAt: lastExecuted,
	}
}

func toDetail(c Campaign, items []CampaignItem) CampaignDetail {
	views := make([]CampaignItemView, 0, len(items))
	for _, item := range items {
		views = append(views, CampaignItemView{
			GroupID:            item.GroupID,
			GroupName:          item.GroupName,
			OriginalMultiplier: item.OriginalMultiplier,
			CampaignMultiplier: item.CampaignMultiplier,
			RestoredMultiplier: item.RestoredMultiplier,
			ApplyStatus:        item.ApplyStatus,
			RestoreStatus:      item.RestoreStatus,
			ApplyReason:        item.ApplyReason,
			RestoreReason:      item.RestoreReason,
			AppliedAt:          item.AppliedAt,
			RestoredAt:         item.RestoredAt,
		})
	}
	return CampaignDetail{
		CampaignListItem: toListItem(c, items),
		Description:      c.Description,
		Selection:        c.Selection,
		Adjustment:       c.Adjustment,
		Notify:           c.Notify,
		Items:            views,
	}
}

func floatOrZero(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

func findGroup(groups []upstream.AdminGroupInfo, name string) *upstream.AdminGroupInfo {
	trimmed := strings.TrimSpace(name)
	for i := range groups {
		if strings.TrimSpace(groups[i].Name) == trimmed {
			return &groups[i]
		}
	}
	return nil
}

func toSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[strings.TrimSpace(v)] = struct{}{}
	}
	return set
}
