package service

import (
	"context"
	"strings"
	"sync"
	"time"
)

// 分组模型白名单的来源，随 /groups/available 下发给客户端。
const (
	GroupModelAllowlistSourceManual = "manual"
	GroupModelAllowlistSourceAuto   = "auto"
)

// groupAutoModelAllowlistTTL 自动白名单的进程内缓存时长。客户端会频繁刷新分组列表，
// 而账号的模型映射很少变化，一分钟的滞后可以接受。
const groupAutoModelAllowlistTTL = time.Minute

// GroupAutoModelAccountLister 提供分组内可调度账号，用于推导自动白名单。
type GroupAutoModelAccountLister interface {
	ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error)
}

type groupAutoModelAllowlistEntry struct {
	models    []string
	expiresAt time.Time
}

type groupAutoModelAllowlist struct {
	lister GroupAutoModelAccountLister
	cache  sync.Map // groupID -> groupAutoModelAllowlistEntry
	now    func() time.Time
}

// SetGroupAutoModelAccountLister 启用分组自动白名单。
func (s *APIKeyService) SetGroupAutoModelAccountLister(lister GroupAutoModelAccountLister) {
	if s == nil || lister == nil {
		return
	}
	s.groupAutoModels = &groupAutoModelAllowlist{lister: lister, now: time.Now}
}

// ApplyAutoModelAllowlists 为没有手动开启白名单的分组补上自动白名单（只给 /groups/available 用）：
// 取分组内可调度账号支持的模型。手动开启的白名单原样保留。
//
// 自动白名单只用于向客户端展示分组支持哪些模型，不参与网关准入——
// 准入仍只认管理员手动开启的白名单（见 middleware.GroupModelAllowlist）。
func (s *APIKeyService) ApplyAutoModelAllowlists(ctx context.Context, groups []Group) {
	for i := range groups {
		group := &groups[i]
		if group.ModelAllowlist.Enabled {
			group.ModelAllowlistSource = GroupModelAllowlistSourceManual
			continue
		}
		if s == nil || s.groupAutoModels == nil {
			continue
		}
		models := s.groupAutoModels.modelsFor(ctx, group)
		if len(models) == 0 {
			continue
		}
		group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: models}
		group.ModelAllowlistSource = GroupModelAllowlistSourceAuto
	}
}

func (a *groupAutoModelAllowlist) modelsFor(ctx context.Context, group *Group) []string {
	if group == nil || group.ID <= 0 {
		return nil
	}
	now := a.now()
	if cached, ok := a.cache.Load(group.ID); ok {
		entry := cached.(groupAutoModelAllowlistEntry)
		if now.Before(entry.expiresAt) {
			return cloneStringSlice(entry.models)
		}
	}
	accounts, err := a.lister.ListSchedulableByGroupID(ctx, group.ID)
	if err != nil {
		// 查询失败不缓存，下一次请求重试；本次按"未知"处理，不下发白名单。
		return nil
	}
	models := autoModelAllowlistFromAccounts(group.Platform, accounts)
	a.cache.Store(group.ID, groupAutoModelAllowlistEntry{models: models, expiresAt: now.Add(groupAutoModelAllowlistTTL)})
	return cloneStringSlice(models)
}

// autoModelAllowlistFromAccounts 由分组内账号推导分组支持的模型：
//   - 只看与分组平台一致的账号（组合分组看所有具体平台的账号），与调度器的平台匹配规则一致；
//   - 账号配了模型映射，就取映射的键（即客户端可以写的模型名），带 * 的通配键不列出；
//   - 跨厂商的兼容别名（如 GLM 分组里的 gpt-5.6-sol）不列出；只有别名、没有本厂商模型时才退回列别名，
//     因为那时别名就是这个分组唯一能用的模型名；
//   - OpenAI 分组里只要有账号开了透传或没配映射，就补上 OpenAI 默认模型，因为这些账号什么模型都接；
//   - 没有任何账号配映射时，用平台默认模型；国产供应商没有可靠的默认列表，此时不下发。
func autoModelAllowlistFromAccounts(groupPlatform string, accounts []Account) []string {
	groupPlatform = strings.TrimSpace(groupPlatform)
	matched := make([]Account, 0, len(accounts))
	for i := range accounts {
		platform := strings.TrimSpace(accounts[i].Platform)
		if groupPlatform == PlatformComposite {
			if !isConcreteRequestPlatform(platform) {
				continue
			}
		} else if platform != groupPlatform {
			continue
		}
		matched = append(matched, accounts[i])
	}
	if len(matched) == 0 {
		return nil
	}

	models := make([]string, 0)
	aliases := make([]string, 0)
	openAIAcceptsAnyModel := false
	for i := range matched {
		account := &matched[i]
		if account.Platform == PlatformOpenAI && account.IsOpenAIPassthroughEnabled() {
			openAIAcceptsAnyModel = true
			continue
		}
		mapping := account.GetModelMapping()
		if len(mapping) == 0 {
			if account.Platform == PlatformOpenAI {
				openAIAcceptsAnyModel = true
			}
			continue
		}
		for model := range mapping {
			model = strings.TrimSpace(model)
			if model == "" || strings.Contains(model, "*") {
				continue
			}
			if !groupListsModel(groupPlatform, model) {
				aliases = append(aliases, model)
				continue
			}
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		models = aliases
	}

	if openAIAcceptsAnyModel {
		models = append(models, defaultModelsListCandidateIDs(PlatformOpenAI)...)
	}
	if len(models) == 0 {
		if !autoModelAllowlistHasReliableDefaults(groupPlatform) {
			return nil
		}
		models = defaultModelsListCandidateIDs(groupPlatform)
	}
	return dedupeAndSortModelIDs(models)
}

// autoModelAllowlistHasReliableDefaults 报告平台是否有可信的内置默认模型列表。
// defaultModelsListCandidateIDs 对未单列的平台（kimi/zhipu/deepseek/minimax）
// 会退回 Claude 模型列表，拿来当这些分组的白名单是错的。
func autoModelAllowlistHasReliableDefaults(platform string) bool {
	switch platform {
	case PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity,
		PlatformGrok, PlatformOpenCodeGo, PlatformComposite:
		return true
	default:
		return false
	}
}
