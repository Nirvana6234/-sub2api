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

// pawFallbackAutoGroupIDs picks the candidate set used when the internal Paw key
// has to be created here rather than by EnsurePlaygroundAPIKeys.
//
// Every candidate must share one platform: validateAutoGroupIDs rejects a mixed
// set with AUTO_GROUP_CANDIDATE_PLATFORM_MISMATCH, which would fail the creation
// and take the whole Paw path down for that user. This path is only reached when
// EnsurePlaygroundAPIKeys declined to create the key, which it does when the user
// has no OpenAI group at all — so a user holding, say, an Anthropic group and a
// Gemini group used to land here with a mixed set and get no key at all.
//
// OpenAI is preferred to match selectPlaygroundGroupIDs; otherwise the first
// group's platform wins, so a single-platform user still gets a usable key.
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

// PrepareResponses validates the group/model selected by the desktop relay and
// resolves the authenticated internal key without rewriting the Responses body.
// Codex sends a Responses payload whose input shape is not interchangeable with
// the chat-completions payload handled by Prepare.
func (s *PawChatService) PrepareResponses(ctx context.Context, userID, groupID int64, modelID string) (*PawChatResolution, error) {
	if s == nil || s.config == nil || s.keySource == nil {
		return nil, errPawKeyUnavailable
	}
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "authenticated user is required")
	}
	resolution, _, err := s.resolvePawSelection(ctx, userID, groupID, modelID)
	if err != nil {
		return nil, err
	}
	return resolution, nil
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
	resolution, model, err := s.resolvePawSelection(ctx, userID, req.GroupID, modelID)
	if err != nil {
		return nil, err
	}
	if reasoning := strings.TrimSpace(req.Reasoning); reasoning != "" && !pawReasoningValueAvailable(model, reasoning) {
		return nil, errPawReasoningUnsupported
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
	resolution.Body = body
	return resolution, nil
}

func (s *PawChatService) resolvePawSelection(ctx context.Context, userID, groupID int64, modelID string) (*PawChatResolution, PawModel, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, PawModel{}, errPawModelUnavailable
	}
	config, err := s.config.GetAvailableConfig(ctx, userID)
	if err != nil {
		return nil, PawModel{}, errPawKeyUnavailable.WithCause(err)
	}
	if groupID <= 0 {
		autoSource, ok := s.keySource.(interface {
			ResolvePawAutoGroupForModel(context.Context, int64, string) (*APIKey, *UserSubscription, error)
		})
		if !ok {
			return nil, PawModel{}, errPawKeyUnavailable
		}
		apiKey, subscription, autoErr := autoSource.ResolvePawAutoGroupForModel(ctx, userID, modelID)
		if autoErr != nil || apiKey == nil || apiKey.Group == nil {
			if errors.Is(autoErr, ErrAutoGroupUnavailable) {
				return nil, PawModel{}, infraerrors.Forbidden("AUTO_GROUP_UNAVAILABLE", "No available group satisfies the automatic routing requirements")
			}
			return nil, PawModel{}, errPawKeyUnavailable.WithCause(autoErr)
		}
		if apiKey.Status == StatusAPIKeyQuotaExhausted || apiKey.IsQuotaExhausted() {
			return nil, PawModel{}, errPawQuotaExceeded
		}
		return &PawChatResolution{
			APIKey: apiKey, Subscription: subscription, Group: apiKey.Group, Model: modelID,
		}, PawModel{ID: modelID}, nil
	}
	group, model, ok := s.findPawChatSelection(ctx, config, groupID, modelID)
	if !ok {
		if pawGroupExists(config, groupID) {
			return nil, PawModel{}, errPawModelUnavailable
		}
		return nil, PawModel{}, errPawGroupForbidden
	}

	apiKey, subscription, err := s.keySource.ResolvePawAPIKey(ctx, userID, group.ID)
	if err != nil || apiKey == nil {
		return nil, PawModel{}, errPawKeyUnavailable.WithCause(err)
	}
	if apiKey.Status == StatusAPIKeyQuotaExhausted || apiKey.IsQuotaExhausted() {
		return nil, PawModel{}, errPawQuotaExceeded
	}
	if apiKey.Status != "" && apiKey.Status != StatusActive {
		return nil, PawModel{}, errPawKeyUnavailable
	}
	if apiKey.IsExpired() {
		return nil, PawModel{}, errPawKeyUnavailable
	}
	return &PawChatResolution{
		APIKey:       clonePawAPIKeyWithGroup(apiKey, group),
		Subscription: subscription,
		Group:        group,
		Model:        modelID,
	}, model, nil
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
