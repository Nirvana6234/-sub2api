package service

import (
	"context"
	"encoding/json"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	errPawGroupForbidden       = infraerrors.Forbidden("GROUP_FORBIDDEN", "selected group is not available to this user")
	errPawModelUnavailable     = infraerrors.BadRequest("MODEL_UNAVAILABLE", "selected model is not available in this group")
	errPawReasoningUnsupported = infraerrors.BadRequest("REASONING_UNSUPPORTED", "selected reasoning level is not supported by this model")
	errPawAttachmentInvalid    = infraerrors.BadRequest("ATTACHMENT_INVALID", "attachments are not available for this chat request")
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

func (s APIKeyPawChatKeySource) ResolvePawAPIKey(ctx context.Context, userID, groupID int64) (*APIKey, *UserSubscription, error) {
	if s.Service == nil {
		return nil, nil, errPawKeyUnavailable
	}
	groups, err := s.Service.GetAvailableGroups(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	var selectedGroup *Group
	for i := range groups {
		if groups[i].ID == groupID {
			selectedGroup = &groups[i]
			break
		}
	}
	if selectedGroup == nil {
		return nil, nil, errPawGroupForbidden
	}

	keys, err := s.Service.SearchAPIKeys(ctx, userID, PlaygroundChatAPIKeyName, 10)
	if err != nil {
		return nil, nil, err
	}
	key := findPawInternalKey(keys)
	if key == nil {
		if err := s.Service.EnsurePlaygroundAPIKeys(ctx, userID); err != nil {
			return nil, nil, err
		}
		keys, err = s.Service.SearchAPIKeys(ctx, userID, PlaygroundChatAPIKeyName, 10)
		if err != nil {
			return nil, nil, err
		}
		key = findPawInternalKey(keys)
	}
	if key == nil {
		groupIDs := make([]int64, 0, len(groups))
		for _, group := range groups {
			groupIDs = append(groupIDs, group.ID)
		}
		key, err = s.Service.Create(ctx, userID, CreateAPIKeyRequest{
			Name:         PlaygroundChatAPIKeyName,
			AutoGroup:    true,
			AutoGroupIDs: groupIDs,
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if key.ID > 0 {
		loaded, loadErr := s.Service.GetByID(ctx, key.ID)
		if loadErr != nil {
			return nil, nil, loadErr
		}
		if loaded != nil {
			key = loaded
		}
	}
	var subscription *UserSubscription
	if selectedGroup.IsSubscriptionType() {
		subscription, err = s.Service.GetActiveSubscriptionForGroup(ctx, userID, groupID)
		if err != nil {
			return nil, nil, err
		}
	}
	return key, subscription, nil
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
	config    *PawConfigService
	keySource PawChatKeySource
}

func NewPawChatService(config *PawConfigService, keySource PawChatKeySource) *PawChatService {
	return &PawChatService{config: config, keySource: keySource}
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
	if len(req.Attachments) > 0 {
		return nil, errPawAttachmentInvalid
	}

	config, err := s.config.GetAvailableConfig(ctx, userID)
	if err != nil {
		return nil, errPawKeyUnavailable.WithCause(err)
	}
	group, model, ok := s.findPawChatSelection(ctx, config, req.GroupID, modelID)
	if !ok {
		if pawGroupExists(config, req.GroupID) {
			return nil, errPawModelUnavailable
		}
		return nil, errPawGroupForbidden
	}
	if reasoning := strings.TrimSpace(req.Reasoning); reasoning != "" && !pawReasoningValueAvailable(model, reasoning) {
		return nil, errPawReasoningUnsupported
	}

	apiKey, subscription, err := s.keySource.ResolvePawAPIKey(ctx, userID, group.ID)
	if err != nil || apiKey == nil {
		return nil, errPawKeyUnavailable.WithCause(err)
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
	resolvedKey := clonePawAPIKeyWithGroup(apiKey, group)
	body, err := json.Marshal(struct {
		Model           string           `json:"model"`
		Messages        []PawChatMessage `json:"messages"`
		Stream          bool             `json:"stream"`
		ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	}{
		Model:           modelID,
		Messages:        req.Messages,
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
