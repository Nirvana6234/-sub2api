package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const PawDefaultsAttributeKey = "paw_defaults"

type PawDefaults struct {
	GroupID   int64  `json:"group_id"`
	ModelID   string `json:"model_id"`
	Reasoning string `json:"reasoning"`
}

type PawModel struct {
	ID               string
	Name             string
	OwnedBy          string
	Reasoning        PawReasoningCapability
	ReasoningValues  []string
	ReasoningDefault string
	Vision           bool
	ImageGeneration  bool
	FileInput        bool
}

type PawReasoningCapability struct {
	Supported bool
	Values    []string
	Default   string
}

type PawGroup struct {
	ID          int64
	Name        string
	Description string
	Models      []PawModel
}

type PawUser struct {
	ID    int64
	Name  string
	Email string
}

type PawConfig struct {
	User     PawUser
	Groups   []PawGroup
	Defaults PawDefaults
}

type PawGroupSource interface {
	AvailableGroups(ctx context.Context, userID int64) ([]Group, error)
}

type APIKeyPawGroupSource struct{ Service *APIKeyService }

func (s APIKeyPawGroupSource) AvailableGroups(ctx context.Context, userID int64) ([]Group, error) {
	return s.Service.GetAvailableGroups(ctx, userID)
}

type UserAttributePawDefaultsStore struct{ Service *UserAttributeService }

func (s UserAttributePawDefaultsStore) GetPawDefaults(ctx context.Context, userID int64) (PawDefaults, error) {
	if s.Service == nil {
		return PawDefaults{}, nil
	}
	def, err := s.Service.GetDefinitionByKey(ctx, PawDefaultsAttributeKey)
	if err != nil {
		return PawDefaults{}, nil
	}
	values, err := s.Service.GetUserAttributes(ctx, userID)
	if err != nil {
		return PawDefaults{}, err
	}
	for _, value := range values {
		if value.AttributeID == def.ID {
			var defaults PawDefaults
			if value.Value == "" {
				return defaults, nil
			}
			if err := json.Unmarshal([]byte(value.Value), &defaults); err != nil {
				return PawDefaults{}, err
			}
			return defaults, nil
		}
	}
	return PawDefaults{}, nil
}
func (s UserAttributePawDefaultsStore) SavePawDefaults(ctx context.Context, userID int64, defaults PawDefaults) error {
	if s.Service == nil {
		return fmt.Errorf("Paw defaults store is unavailable")
	}
	def, err := s.Service.GetDefinitionByKey(ctx, PawDefaultsAttributeKey)
	if err != nil {
		def, err = s.Service.CreateDefinition(ctx, CreateAttributeDefinitionInput{Key: PawDefaultsAttributeKey, Name: "Paw defaults", Type: AttributeTypeText, Enabled: true})
		if err != nil {
			return err
		}
	}
	raw, err := json.Marshal(defaults)
	if err != nil {
		return err
	}
	return s.Service.UpdateUserAttributes(ctx, userID, []UpdateUserAttributeInput{{AttributeID: def.ID, Value: string(raw)}})
}

type PawUserSource interface {
	GetByID(ctx context.Context, userID int64) (*User, error)
}

type PawChannelSource interface {
	GetChannelForGroup(ctx context.Context, groupID int64) (*Channel, error)
}

type PawDefaultsStore interface {
	GetPawDefaults(ctx context.Context, userID int64) (PawDefaults, error)
	SavePawDefaults(ctx context.Context, userID int64, defaults PawDefaults) error
}

type PawConfigService struct {
	groups   PawGroupSource
	users    PawUserSource
	channels PawChannelSource
	defaults PawDefaultsStore
}

func NewPawConfigService(groups PawGroupSource, users PawUserSource, channels PawChannelSource, defaults PawDefaultsStore) *PawConfigService {
	return &PawConfigService{groups: groups, users: users, channels: channels, defaults: defaults}
}

func (s *PawConfigService) GetConfig(ctx context.Context, userID int64) (*PawConfig, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	groups, err := s.groups.AvailableGroups(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get available groups: %w", err)
	}
	result := &PawConfig{User: PawUser{ID: user.ID, Name: user.Username, Email: user.Email}, Groups: make([]PawGroup, 0, len(groups))}
	for _, group := range groups {
		if !group.IsActive() {
			continue
		}
		channel, err := s.channels.GetChannelForGroup(ctx, group.ID)
		if err != nil {
			return nil, fmt.Errorf("get channel for group %d: %w", group.ID, err)
		}
		if channel == nil {
			continue
		}
		models := make([]PawModel, 0)
		for _, pricing := range channel.ModelPricing {
			if !modelPricingPlatformMatches(group.Platform, pricing.Platform) {
				continue
			}
			for _, modelID := range pricing.Models {
				values := PawReasoningValues(group.Platform)
				defaultValue := pawReasoningDefault(values)
				models = append(models, PawModel{ID: modelID, Name: modelID, OwnedBy: pricing.Platform, Reasoning: PawReasoningCapability{Supported: len(values) > 0, Values: values, Default: defaultValue}, ReasoningValues: values, ReasoningDefault: defaultValue})
			}
		}
		result.Groups = append(result.Groups, PawGroup{ID: group.ID, Name: group.Name, Description: group.Description, Models: models})
	}
	if s.defaults != nil {
		result.Defaults, err = s.defaults.GetPawDefaults(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("get Paw defaults: %w", err)
		}
	}
	return result, nil
}

func (s *PawConfigService) SaveDefaults(ctx context.Context, userID int64, defaults PawDefaults) error {
	config, err := s.GetConfig(ctx, userID)
	if err != nil {
		return err
	}
	if !pawDefaultAvailable(config.Groups, defaults) {
		return fmt.Errorf("Paw default is unavailable")
	}
	return s.defaults.SavePawDefaults(ctx, userID, defaults)
}

func PawReasoningValues(platform string) []string {
	if platform == PlatformOpenAI || platform == PlatformComposite {
		return append([]string(nil), openAIReasoningEffortValues...)
	}
	return nil
}

func modelPricingPlatformMatches(groupPlatform, pricingPlatform string) bool {
	return groupPlatform == PlatformComposite || groupPlatform == pricingPlatform
}

func pawReasoningDefault(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[1]
}

func pawDefaultAvailable(groups []PawGroup, defaults PawDefaults) bool {
	for _, group := range groups {
		if group.ID != defaults.GroupID {
			continue
		}
		for _, model := range group.Models {
			if model.ID != defaults.ModelID {
				continue
			}
			if defaults.Reasoning == "" {
				return len(model.ReasoningValues) == 0
			}
			for _, value := range model.ReasoningValues {
				if strings.EqualFold(value, defaults.Reasoning) {
					return true
				}
			}
			return false
		}
	}
	return false
}
