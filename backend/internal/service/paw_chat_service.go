package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	errPawGroupForbidden       = infraerrors.Forbidden("GROUP_FORBIDDEN", "selected group is not available to this user")
	errPawModelUnavailable     = infraerrors.BadRequest("MODEL_UNAVAILABLE", "selected model is not available in this group")
	errPawReasoningUnsupported = infraerrors.BadRequest("REASONING_UNSUPPORTED", "selected reasoning level is not supported by this model")
	errPawKeyUnavailable       = infraerrors.ServiceUnavailable("CONFIG_UNAVAILABLE", "Paw chat credentials are unavailable")
	errPawQuotaExceeded        = infraerrors.TooManyRequests("QUOTA_EXCEEDED", "Paw quota has been exhausted")
)

type PawChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type PawAttachmentReference struct {
	ID string `json:"id"`
}

type PawChatRequest struct {
	GroupID     int64                    `json:"group_id"`
	ModelID     string                   `json:"model_id"`
	Reasoning   string                   `json:"reasoning"`
	Messages    []PawChatMessage         `json:"messages"`
	Stream      bool                     `json:"stream"`
	Attachments []PawAttachmentReference `json:"attachments"`
}

type PawChatKeySource interface {
	ResolvePawAPIKey(ctx context.Context, userID, groupID int64) (*APIKey, *UserSubscription, error)
}

type PawAPIKeyLookup interface {
	SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error)
	EnsurePlaygroundAPIKeys(ctx context.Context, userID int64) error
	Create(ctx context.Context, userID int64, req CreateAPIKeyRequest) (*APIKey, error)
	GetByID(ctx context.Context, id int64) (*APIKey, error)
	GetAvailableGroups(ctx context.Context, userID int64) ([]Group, error)
	GetActiveSubscriptionForGroup(ctx context.Context, userID, groupID int64) (*UserSubscription, error)
}

type APIKeyPawChatKeySource struct {
	Service PawAPIKeyLookup
}

// ResolvePawGroupKey is ResolvePawAPIKey that also hands back the group it
// resolved. Callers that dispatch on the group's platform need it, and taking it
// from the same lookup avoids a second query per request. The group is nil when
// groupID is not positive, which is the automatic-routing case.
func (s APIKeyPawChatKeySource) ResolvePawGroupKey(ctx context.Context, userID, groupID int64) (*APIKey, *UserSubscription, *Group, error) {
	if s.Service == nil {
		return nil, nil, nil, errPawKeyUnavailable
	}
	groups, err := s.Service.GetAvailableGroups(ctx, userID)
	if err != nil {
		return nil, nil, nil, err
	}
	var selectedGroup *Group
	if groupID > 0 {
		for i := range groups {
			if groups[i].ID == groupID {
				selectedGroup = &groups[i]
				break
			}
		}
		if selectedGroup == nil {
			return nil, nil, nil, errPawGroupForbidden
		}
	}

	keys, err := s.Service.SearchAPIKeys(ctx, userID, PlaygroundChatAPIKeyName, 10)
	if err != nil {
		return nil, nil, nil, err
	}
	key := findPawInternalKey(keys)
	if key == nil {
		if err := s.Service.EnsurePlaygroundAPIKeys(ctx, userID); err != nil {
			return nil, nil, nil, err
		}
		keys, err = s.Service.SearchAPIKeys(ctx, userID, PlaygroundChatAPIKeyName, 10)
		if err != nil {
			return nil, nil, nil, err
		}
		key = findPawInternalKey(keys)
	}
	if key == nil {
		groupIDs := pawFallbackAutoGroupIDs(groups)
		key, err = s.Service.Create(ctx, userID, CreateAPIKeyRequest{
			Name:         PlaygroundChatAPIKeyName,
			AutoGroup:    true,
			AutoGroupIDs: groupIDs,
		})
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if key.ID > 0 {
		loaded, loadErr := s.Service.GetByID(ctx, key.ID)
		if loadErr != nil {
			return nil, nil, nil, loadErr
		}
		if loaded != nil {
			key = loaded
		}
	}
	var subscription *UserSubscription
	if selectedGroup != nil && selectedGroup.IsSubscriptionType() {
		subscription, err = s.Service.GetActiveSubscriptionForGroup(ctx, userID, groupID)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return key, subscription, selectedGroup, nil
}

func (s APIKeyPawChatKeySource) ResolvePawAPIKey(ctx context.Context, userID, groupID int64) (*APIKey, *UserSubscription, error) {
	key, subscription, _, err := s.ResolvePawGroupKey(ctx, userID, groupID)
	return key, subscription, err
}

// pawFallbackAutoGroupIDs 决定「内部 Paw key 不得不在这里创建」时用哪一批候选分组。
//
// 候选集必须同平台：validateAutoGroupIDs 对混平台的集合会报
// AUTO_GROUP_CANDIDATE_PLATFORM_MISMATCH，创建直接失败，那个用户的整条 Paw 通路
// 就断了。而走到这里的前提恰恰是 EnsurePlaygroundAPIKeys 拒绝创建——它在用户
// 一个 OpenAI 分组都没有时就会拒绝。于是手里只有 Anthropic 分组和 Gemini 分组的
// 用户，以前会带着一个混平台集合落到这里，最后一把 key 都拿不到。
//
// 优先取 OpenAI 以便与 selectPlaygroundGroupIDs 一致；没有 OpenAI 时取第一个
// 分组的平台，保证单平台用户仍能拿到可用的 key。
func pawFallbackAutoGroupIDs(groups []Group) []int64 {
	platform := ""
	for i := range groups {
		if groups[i].Platform == PlatformOpenAI {
			platform = PlatformOpenAI
			break
		}
	}
	if platform == "" {
		for i := range groups {
			if strings.TrimSpace(groups[i].Platform) != "" {
				platform = groups[i].Platform
				break
			}
		}
	}
	groupIDs := make([]int64, 0, len(groups))
	for i := range groups {
		if groups[i].Platform == platform {
			groupIDs = append(groupIDs, groups[i].ID)
		}
	}
	return groupIDs
}

func findPawInternalKey(keys []APIKey) *APIKey {
	for i := range keys {
		if strings.EqualFold(strings.TrimSpace(keys[i].Name), PlaygroundChatAPIKeyName) {
			key := keys[i]
			return &key
		}
	}
	return nil
}

type PawChatResolution struct {
	Body         []byte
	APIKey       *APIKey
	Subscription *UserSubscription
	Group        *Group
	Model        string
}

type PawChatService struct {
	config      *PawConfigService
	keySource   PawChatKeySource
	attachments *PawAttachmentService
}

func NewPawChatService(config *PawConfigService, keySource PawChatKeySource, attachments ...*PawAttachmentService) *PawChatService {
	var attachmentService *PawAttachmentService
	if len(attachments) > 0 {
		attachmentService = attachments[0]
	}
	return &PawChatService{config: config, keySource: keySource, attachments: attachmentService}
}

// pawGroupKeySource is what PrepareMessages needs beyond PawChatKeySource: the
// resolved group as well as the key. Asserted rather than added to the interface
// so existing stubs keep compiling.
type pawGroupKeySource interface {
	ResolvePawGroupKey(ctx context.Context, userID, groupID int64) (*APIKey, *UserSubscription, *Group, error)
}

// PrepareMessages resolves the group and internal key for an Anthropic Messages
// request coming from a desktop client (Claude Code and its editor extensions).
//
// Unlike PrepareResponses it does not check the model against the group's catalog.
// A Claude Code session names models the catalog has no reason to list — the small
// model it uses for background work, or an alias the account maps — and refusing
// them here would break the session while the same request through an API key is
// accepted. Which models a group can serve is decided where it is for an API key:
// by its accounts.
func (s *PawChatService) PrepareMessages(ctx context.Context, userID, groupID int64) (*PawChatResolution, error) {
	if s == nil || s.keySource == nil {
		return nil, errPawKeyUnavailable
	}
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "authenticated user is required")
	}
	if groupID <= 0 {
		return nil, errPawGroupForbidden
	}
	source, ok := s.keySource.(pawGroupKeySource)
	if !ok {
		return nil, errPawKeyUnavailable
	}
	apiKey, subscription, group, err := source.ResolvePawGroupKey(ctx, userID, groupID)
	if err != nil {
		if infraerrors.Reason(err) == errPawGroupForbidden.Reason {
			return nil, errPawGroupForbidden
		}
		return nil, errPawKeyUnavailable.WithCause(err)
	}
	if apiKey == nil || group == nil {
		return nil, errPawKeyUnavailable
	}
	if apiKey.Status == StatusAPIKeyQuotaExhausted || apiKey.IsQuotaExhausted() {
		return nil, errPawQuotaExceeded
	}
	if apiKey.Status != "" && apiKey.Status != StatusActive {
		return nil, errPawKeyUnavailable
	}
	if apiKey.IsExpired() {
		return nil, errPawKeyUnavailable
	}
	return &PawChatResolution{
		APIKey:       clonePawAPIKeyWithGroup(apiKey, group),
		Subscription: subscription,
		Group:        group,
	}, nil
}

func (s *PawChatService) Prepare(ctx context.Context, userID int64, req PawChatRequest) (*PawChatResolution, error) {
	if s == nil || s.config == nil || s.keySource == nil {
		return nil, errPawKeyUnavailable
	}
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "authenticated user is required")
	}
	if req.GroupID <= 0 {
		return nil, errPawGroupForbidden
	}
	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		return nil, errPawModelUnavailable
	}
	if len(req.Messages) == 0 {
		return nil, infraerrors.BadRequest("INVALID_REQUEST", "at least one message is required")
	}
	for _, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if !pawChatRoleAllowed(role) || strings.TrimSpace(message.Content) == "" {
			return nil, infraerrors.BadRequest("INVALID_REQUEST", "messages must contain a supported role and non-empty content")
		}
	}
	group, model, err := s.selectPawGroupModel(ctx, userID, req.GroupID, modelID)
	if err != nil {
		return nil, err
	}
	if reasoning := strings.TrimSpace(req.Reasoning); reasoning != "" && !pawReasoningValueAvailable(model, reasoning) {
		return nil, errPawReasoningUnsupported
	}

	resolvedKey, subscription, err := s.resolvePawKeyForGroup(ctx, userID, group)
	if err != nil {
		return nil, err
	}
	messages, err := s.buildPawChatMessages(ctx, userID, req.Messages, req.Attachments)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		Model           string                  `json:"model"`
		Messages        []apicompat.ChatMessage `json:"messages"`
		Stream          bool                    `json:"stream"`
		ReasoningEffort string                  `json:"reasoning_effort,omitempty"`
	}{
		Model:           modelID,
		Messages:        messages,
		Stream:          req.Stream,
		ReasoningEffort: strings.TrimSpace(req.Reasoning),
	})
	if err != nil {
		return nil, errPawKeyUnavailable.WithCause(err)
	}
	return &PawChatResolution{
		Body:         body,
		APIKey:       resolvedKey,
		Subscription: subscription,
		Group:        group,
		Model:        modelID,
	}, nil
}

// selectPawGroupModel 校验「这个用户能不能在这个分组里用这个模型」。
//
// 分组不可见和分组里没这个模型是**两种不同的错**，不能合并：
// 后者告诉用户换个模型，前者不能透露这个分组存在。
func (s *PawChatService) selectPawGroupModel(ctx context.Context, userID, groupID int64, modelID string) (*Group, PawModel, error) {
	config, err := s.config.GetAvailableConfig(ctx, userID)
	if err != nil {
		return nil, PawModel{}, errPawKeyUnavailable.WithCause(err)
	}
	group, model, ok := s.findPawChatSelection(ctx, config, groupID, modelID)
	if !ok {
		if pawGroupExists(config, groupID) {
			return nil, PawModel{}, errPawModelUnavailable
		}
		return nil, PawModel{}, errPawGroupForbidden
	}
	return group, model, nil
}

// resolvePawKeyForGroup 取服务端自己那把内部 key，并**钉死在这个分组上**。
//
// 钉死是关键：那把 key 是 auto_group 的，不钉就会按**请求体里的 model**
// 自己去选分组，而调用方已经明确选了一个。clonePawAPIKeyWithGroup 会把
// auto_group 那一排字段全清掉，让分组只能来自调用方的选择。
func (s *PawChatService) resolvePawKeyForGroup(ctx context.Context, userID int64, group *Group) (*APIKey, *UserSubscription, error) {
	apiKey, subscription, err := s.keySource.ResolvePawAPIKey(ctx, userID, group.ID)
	if err != nil || apiKey == nil {
		return nil, nil, errPawKeyUnavailable.WithCause(err)
	}
	if apiKey.Status == StatusAPIKeyQuotaExhausted || apiKey.IsQuotaExhausted() {
		return nil, nil, errPawQuotaExceeded
	}
	if apiKey.Status != "" && apiKey.Status != StatusActive {
		return nil, nil, errPawKeyUnavailable
	}
	if apiKey.IsExpired() {
		return nil, nil, errPawKeyUnavailable
	}
	return clonePawAPIKeyWithGroup(apiKey, group), subscription, nil
}

// PawResponsesRequest —— Responses 线协议这条的入参。
//
// 比 PawChatRequest 少了 Messages，是因为**请求体必须原样透传**：codex 发的是一份
// 完整的 Responses 载荷（instructions / tools / input 一应俱全，实测 ~47KB），我们没有
// 资格重新拼一份 —— 漏掉一个字段就是惄惄改变了 agent 的行为，而且不会报错。
// 所以这条路上我们**只校验，不改写**：分组从请求头来，模型从 body 里读出来看一眼。
type PawResponsesRequest struct {
	GroupID int64
	ModelID string
}

// PawResponsesResolution 比 PawChatResolution 少一个 Body，同样是因为 body 不经我们的手。
type PawResponsesResolution struct {
	APIKey       *APIKey
	Subscription *UserSubscription
	Group        *Group
	Model        string
}

// PrepareResponses 跟 Prepare 走同一套分组/模型/key 规则，只是不碰请求体。
//
// 它存在的理由：工作台里的 codex **只会说 Responses 一种线协议**，而 Paw 面原先
// 只开了 chat/completions。没有这条，客户端就只能自己握一把 API key 去打网关，
// 于是分组被绑死在 key 上（网关没有按请求选分组的入口）。走 Paw 这条之后，
// 分组回到**按请求**选，且客户端一把 key 都不需要拿。
func (s *PawChatService) PrepareResponses(ctx context.Context, userID int64, req PawResponsesRequest) (*PawResponsesResolution, error) {
	if s == nil || s.config == nil || s.keySource == nil {
		return nil, errPawKeyUnavailable
	}
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "authenticated user is required")
	}
	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		return nil, errPawModelUnavailable
	}
	// 没给分组 = 按 Key 上保存的自动分组设置来选，而不是报错。
	//
	// 只有这条路径这样：chat 那条的分组是用户在界面上选的，缺失就是请求有问题；
	// 这条的调用方是桌面端转发的 codex，开了自动分组之后它本来就不该自己挑组。
	//
	// 这条分支刻意不校验模型是否在分组目录里——分组就是按「哪个组支持这个模型」
	// 选出来的，再回头拿目录校一遍是重复的，也会和选组结果自相矛盾。
	if req.GroupID <= 0 {
		return s.prepareResponsesByAutoGroup(ctx, userID, modelID)
	}

	group, _, err := s.selectPawGroupModel(ctx, userID, req.GroupID, modelID)
	if err != nil {
		return nil, err
	}

	// 注意这里**没有** reasoning 校验，和 Prepare 不同，是故意的。
	//
	// chat 那条的 reasoning 是**用户在界面上选的**，挡下来是在帮用户；这条的
	// reasoning 是 **codex 自己发的**（每一轮都带 reasoning.effort），是一份我们已经
	// 承诺原样透传的载荷的一部分。只校验其中一半是矛盾的，而且一旦 Paw 的
	// 模型目录没列出那个档位，**每一轮**都会被一句「reasoning level not supported」
	// 退回来 —— 而上游其实接得住。
	resolvedKey, subscription, err := s.resolvePawKeyForGroup(ctx, userID, group)
	if err != nil {
		return nil, err
	}
	return &PawResponsesResolution{
		APIKey:       resolvedKey,
		Subscription: subscription,
		Group:        group,
		Model:        modelID,
	}, nil
}

func (s *PawChatService) buildPawChatMessages(ctx context.Context, userID int64, reqMessages []PawChatMessage, attachments []PawAttachmentReference) ([]apicompat.ChatMessage, error) {
	messages := make([]apicompat.ChatMessage, 0, len(reqMessages))
	attachmentParts, err := s.buildPawAttachmentParts(ctx, userID, attachments)
	if err != nil {
		return nil, err
	}
	attachIndex := pawLastUserMessageIndex(reqMessages)
	if len(attachmentParts) > 0 && attachIndex < 0 {
		return nil, infraerrors.BadRequest("ATTACHMENT_INVALID", "attachments require a user message")
	}
	for i, message := range reqMessages {
		content := strings.TrimSpace(message.Content)
		if i == attachIndex && len(attachmentParts) > 0 {
			parts := make([]apicompat.ChatContentPart, 0, 1+len(attachmentParts))
			if content != "" {
				parts = append(parts, apicompat.ChatContentPart{Type: "text", Text: content})
			}
			parts = append(parts, attachmentParts...)
			raw, marshalErr := json.Marshal(parts)
			if marshalErr != nil {
				return nil, errPawKeyUnavailable.WithCause(marshalErr)
			}
			messages = append(messages, apicompat.ChatMessage{Role: strings.TrimSpace(message.Role), Content: raw})
			continue
		}
		raw, marshalErr := json.Marshal(content)
		if marshalErr != nil {
			return nil, errPawKeyUnavailable.WithCause(marshalErr)
		}
		messages = append(messages, apicompat.ChatMessage{Role: strings.TrimSpace(message.Role), Content: raw})
	}
	return messages, nil
}

func (s *PawChatService) buildPawAttachmentParts(ctx context.Context, userID int64, refs []PawAttachmentReference) ([]apicompat.ChatContentPart, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if s == nil || s.attachments == nil {
		return nil, infraerrors.ServiceUnavailable("CONFIG_UNAVAILABLE", "Paw attachments are unavailable")
	}
	return s.attachments.BuildChatContentParts(ctx, userID, refs)
}

func pawLastUserMessageIndex(messages []PawChatMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			return i
		}
	}
	return -1
}

func (s *PawChatService) findPawChatSelection(ctx context.Context, config *PawConfig, groupID int64, modelID string) (*Group, PawModel, bool) {
	if s == nil || s.config == nil || config == nil {
		return nil, PawModel{}, false
	}
	groups, err := s.config.groups.AvailableGroups(ctx, config.User.ID)
	if err != nil {
		return nil, PawModel{}, false
	}
	for i := range config.Groups {
		group := &config.Groups[i]
		if group.ID != groupID {
			continue
		}
		for _, model := range group.Models {
			if model.ID == modelID {
				for j := range groups {
					if groups[j].ID == groupID {
						resolved := groups[j]
						return &resolved, model, true
					}
				}
				return nil, PawModel{}, false
			}
		}
		return nil, PawModel{}, false
	}
	return nil, PawModel{}, false
}

func pawGroupExists(config *PawConfig, groupID int64) bool {
	if config == nil {
		return false
	}
	for _, group := range config.Groups {
		if group.ID == groupID {
			return true
		}
	}
	return false
}

func pawReasoningValueAvailable(model PawModel, value string) bool {
	for _, supported := range model.Reasoning.Values {
		if strings.EqualFold(strings.TrimSpace(supported), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func pawChatRoleAllowed(role string) bool {
	switch role {
	case "system", "user", "assistant", "tool":
		return true
	default:
		return false
	}
}

func clonePawAPIKeyWithGroup(apiKey *APIKey, group *Group) *APIKey {
	if apiKey == nil {
		return nil
	}
	clone := *apiKey
	if apiKey.User != nil {
		user := *apiKey.User
		clone.User = &user
	}
	if group != nil {
		groupCopy := *group
		clone.Group = &groupCopy
		groupID := group.ID
		clone.GroupID = &groupID
	}
	clone.AutoGroup = false
	clone.AutoGroupIDs = nil
	clone.AutoGroupCurrentGroup = nil
	clone.AutoGroupCurrentModel = ""
	clone.AutoGroupCurrentSelectedAt = nil
	return &clone
}

// prepareResponsesByAutoGroup 按 API Key 上保存的自动分组设置解析出本次该用的分组。
//
// 与 selectPawGroupModel 那条的分工：那条回答「用户能不能在这个指定分组里用这个
// 模型」，这条回答「哪个分组能接这个模型」。后者的结果本身就蕴含了前者的答案，
// 所以不再重复校验模型目录。
func (s *PawChatService) prepareResponsesByAutoGroup(ctx context.Context, userID int64, modelID string) (*PawResponsesResolution, error) {
	autoSource, ok := s.keySource.(interface {
		ResolvePawAutoGroupForModel(context.Context, int64, string) (*APIKey, *UserSubscription, error)
	})
	if !ok {
		return nil, errPawGroupForbidden
	}
	apiKey, subscription, err := autoSource.ResolvePawAutoGroupForModel(ctx, userID, modelID)
	if err != nil || apiKey == nil || apiKey.Group == nil {
		if errors.Is(err, ErrAutoGroupUnavailable) {
			return nil, infraerrors.Forbidden("AUTO_GROUP_UNAVAILABLE", "No available group satisfies the automatic routing requirements")
		}
		if err != nil {
			return nil, errPawKeyUnavailable.WithCause(err)
		}
		return nil, errPawGroupForbidden
	}
	if apiKey.Status == StatusAPIKeyQuotaExhausted || apiKey.IsQuotaExhausted() {
		return nil, errPawQuotaExceeded
	}
	return &PawResponsesResolution{
		APIKey:       apiKey,
		Subscription: subscription,
		Group:        apiKey.Group,
		Model:        modelID,
	}, nil
}

// ResolvePawAutoGroupForModel 按 Key 上保存的自动分组设置为该模型解析分组。
//
// 走的是和网关 auto-group 中间件同一个 ResolveAutoGroupForModel，保证桌面端
// 自动分组的选组结果与普通 API Key 的自动分组一致。
func (s APIKeyPawChatKeySource) ResolvePawAutoGroupForModel(ctx context.Context, userID int64, model string) (*APIKey, *UserSubscription, error) {
	key, _, err := s.ResolvePawAPIKey(ctx, userID, 0)
	if err != nil || key == nil {
		return nil, nil, err
	}
	resolver, ok := s.Service.(interface {
		ResolveAutoGroupForModel(context.Context, *APIKey, string) (*APIKey, error)
	})
	if !ok || !key.AutoGroup || len(key.AutoGroupIDs) == 0 {
		return nil, nil, ErrAutoGroupUnavailable
	}
	resolved, err := resolver.ResolveAutoGroupForModel(ctx, key, model)
	if err != nil {
		return nil, nil, err
	}
	if resolved == nil || resolved.Group == nil {
		return nil, nil, ErrAutoGroupUnavailable
	}
	var subscription *UserSubscription
	if resolved.Group.IsSubscriptionType() {
		subscription, err = s.Service.GetActiveSubscriptionForGroup(ctx, userID, resolved.Group.ID)
		if err != nil {
			return nil, nil, err
		}
	}
	return resolved, subscription, nil
}
