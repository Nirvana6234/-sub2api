package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
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
	ID                 int64
	Name               string
	Description        string
	Platform           string
	RateMultiplier     float64
	UserRateMultiplier *float64
	SubscriptionType   string
	PeakRateEnabled    bool
	PeakStart          string
	PeakEnd            string
	PeakRateMultiplier float64
	Models             []PawModel
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
		if errors.Is(err, ErrAttributeDefinitionNotFound) {
			return PawDefaults{}, nil
		}
		return PawDefaults{}, err
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
		return pawConfigUnavailableError("Paw defaults store is unavailable")
	}
	def, err := s.Service.GetDefinitionByKey(ctx, PawDefaultsAttributeKey)
	if err != nil {
		if !errors.Is(err, ErrAttributeDefinitionNotFound) {
			return err
		}
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
	pricing  *PricingService
}

func NewPawConfigService(groups PawGroupSource, users PawUserSource, channels PawChannelSource, defaults PawDefaultsStore, pricing ...*PricingService) *PawConfigService {
	var pricingService *PricingService
	if len(pricing) > 0 {
		pricingService = pricing[0]
	}
	return &PawConfigService{groups: groups, users: users, channels: channels, defaults: defaults, pricing: pricingService}
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
	return s.buildConfig(ctx, user, groups, true, true)
}

// GetAvailableConfig returns the current selectable groups and models without
// validating persisted defaults. Chat requests must remain usable while a
// previously saved default is being replaced.
func (s *PawConfigService) GetAvailableConfig(ctx context.Context, userID int64) (*PawConfig, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	groups, err := s.groups.AvailableGroups(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get available groups: %w", err)
	}
	return s.buildConfig(ctx, user, groups, true, false)
}

func (s *PawConfigService) buildConfig(ctx context.Context, user *User, groups []Group, includeDefaults bool, validateDefaults bool) (*PawConfig, error) {
	result := &PawConfig{User: PawUser{ID: user.ID, Name: user.Username, Email: user.Email}, Groups: make([]PawGroup, 0, len(groups))}
	for _, group := range groups {
		if !group.IsActive() {
			continue
		}
		var channel *Channel
		if s.channels != nil {
			var err error
			channel, err = s.channels.GetChannelForGroup(ctx, group.ID)
			if err != nil {
				return nil, fmt.Errorf("get channel for group %d: %w", group.ID, err)
			}
		}
		models := s.buildPawModels(group, channel)
		if len(models) == 0 {
			continue
		}
		result.Groups = append(result.Groups, PawGroup{
			ID:                 group.ID,
			Name:               group.Name,
			Description:        group.Description,
			Platform:           group.Platform,
			RateMultiplier:     group.RateMultiplier,
			SubscriptionType:   group.SubscriptionType,
			PeakRateEnabled:    group.PeakRateEnabled,
			PeakStart:          group.PeakStart,
			PeakEnd:            group.PeakEnd,
			PeakRateMultiplier: group.PeakRateMultiplier,
			Models:             models,
		})
	}
	if includeDefaults && s.defaults != nil {
		defaults, err := s.defaults.GetPawDefaults(ctx, user.ID)
		if err != nil {
			return nil, fmt.Errorf("get Paw defaults: %w", err)
		}
		result.Defaults = defaults
		if validateDefaults && pawDefaultsConfigured(result.Defaults) && !pawDefaultAvailable(result.Groups, result.Defaults) {
			return nil, pawConfigUnavailableError("saved Paw defaults are no longer available")
		}
	}
	return result, nil
}

func (s *PawConfigService) buildPawModels(group Group, channel *Channel) []PawModel {
	entries := make([]SupportedModel, 0)
	seen := make(map[string]struct{})
	addSupported := func(modelID, ownedBy string, pricing *ChannelModelPricing) {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" || !modelPricingPlatformMatches(group.Platform, ownedBy) {
			return
		}
		key := strings.ToLower(modelID)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		entries = append(entries, SupportedModel{Name: modelID, Platform: ownedBy, Pricing: pricing})
	}

	if channel == nil {
		// Some installations only persist the group's model list and do not
		// have a channel row yet. Keep those authorized groups selectable.
		for _, modelID := range group.ModelsListConfig.Models {
			addSupported(modelID, group.Platform, nil)
		}
		for _, pattern := range group.ModelsListConfig.Models {
			prefix, wildcard := splitWildcardSuffix(strings.TrimSpace(pattern))
			if !wildcard || s.pricing == nil {
				continue
			}
			for _, candidate := range s.pricing.ListModelNamesByProvider(group.Platform) {
				if strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(prefix)) {
					addSupported(candidate, group.Platform, nil)
				}
			}
		}
		if len(entries) == 0 && !group.CustomModelsListEnabled() && s.pricing != nil && group.Platform != PlatformComposite {
			for _, candidate := range s.pricing.ListModelNamesByProvider(group.Platform) {
				addSupported(candidate, group.Platform, nil)
			}
		}
	} else {
		supported := channel.SupportedModels()

		// Channel.SupportedModels is the canonical view because it includes both
		// explicit pricing and model mappings. Expand wildcard pricing against the
		// server catalog so a wildcard does not become an unusable model option.
		for _, model := range supported {
			addSupported(model.Name, model.Platform, model.Pricing)
		}
		for _, pricing := range channel.ModelPricing {
			if !modelPricingPlatformMatches(group.Platform, pricing.Platform) || s.pricing == nil {
				continue
			}
			for _, pattern := range pricing.Models {
				prefix, wildcard := splitWildcardSuffix(strings.TrimSpace(pattern))
				if !wildcard {
					continue
				}
				for _, candidate := range s.pricing.ListModelNamesByProvider(pricing.Platform) {
					if strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(prefix)) {
						addSupported(candidate, pricing.Platform, &pricing)
					}
				}
			}
		}

		// An unrestricted channel may intentionally have no model list. In that
		// case expose the provider catalog so the client still has server-owned
		// choices instead of a disabled model selector.
		if len(entries) == 0 && !channel.RestrictModels && s.pricing != nil && group.Platform != PlatformComposite {
			for _, candidate := range s.pricing.ListModelNamesByProvider(group.Platform) {
				addSupported(candidate, group.Platform, nil)
			}
		}
	}

	models := make([]PawModel, 0, len(entries))
	for _, model := range entries {
		modelID := strings.TrimSpace(model.Name)
		if modelID == "" {
			continue
		}
		modelCatalog := s.modelCatalog(modelID)
		if modelCatalog == nil && model.Pricing != nil {
			for _, pricedModel := range model.Pricing.Models {
				if candidate := s.modelCatalog(pricedModel); candidate != nil {
					modelCatalog = candidate
					break
				}
			}
		}
		values := PawReasoningValues(group.Platform, modelID, modelCatalog)
		defaultValue := pawReasoningDefault(values)
		vision, imageGeneration, fileInput := pawModelCapabilities(&group, modelCatalog)
		models = append(models, PawModel{
			ID:      modelID,
			Name:    modelID,
			OwnedBy: model.Platform,
			Reasoning: PawReasoningCapability{
				Supported: len(values) > 0,
				Values:    values,
				Default:   defaultValue,
			},
			ReasoningValues:  values,
			ReasoningDefault: defaultValue,
			Vision:           vision,
			ImageGeneration:  imageGeneration,
			FileInput:        fileInput,
		})
	}
	return models
}

func (s *PawConfigService) SaveDefaults(ctx context.Context, userID int64, defaults PawDefaults) error {
	if s.defaults == nil {
		return pawConfigUnavailableError("Paw defaults store is unavailable")
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	groups, err := s.groups.AvailableGroups(ctx, userID)
	if err != nil {
		return fmt.Errorf("get available groups: %w", err)
	}
	config, err := s.buildConfig(ctx, user, groups, false, false)
	if err != nil {
		return err
	}
	if !pawDefaultAvailable(config.Groups, defaults) {
		return pawConfigUnavailableError("Paw defaults are no longer available")
	}
	return s.defaults.SavePawDefaults(ctx, userID, defaults)
}

func (s *PawConfigService) modelCatalog(modelID string) *LiteLLMModelPricing {
	if s == nil || s.pricing == nil {
		return nil
	}
	return s.pricing.GetIdentifiedModelPricing(modelID)
}

func modelPricingPlatformMatches(groupPlatform, pricingPlatform string) bool {
	return groupPlatform == PlatformComposite || groupPlatform == pricingPlatform
}

func PawReasoningValues(platform, modelID string, catalog *LiteLLMModelPricing) []string {
	if (platform != PlatformOpenAI && platform != PlatformComposite) || catalog == nil || !catalog.SupportsReasoning {
		return []string{}
	}
	values := make([]string, 0, len(openAIReasoningEffortValues))
	for _, value := range openAIReasoningEffortValues {
		switch value {
		case "minimal":
			if !catalog.SupportsMinimalReasoningEffort {
				continue
			}
		case "xhigh":
			if !catalog.SupportsXHighReasoningEffort {
				continue
			}
		case "max":
			if !catalog.SupportsMaxReasoningEffort {
				continue
			}
		}
		values = append(values, value)
	}
	return values
}

func pawReasoningDefault(values []string) string {
	if len(values) == 0 {
		return ""
	}
	if len(values) == 1 {
		return values[0]
	}
	return values[1]
}

func pawModelCapabilities(group *Group, catalog *LiteLLMModelPricing) (bool, bool, bool) {
	if group == nil || catalog == nil {
		return false, false, false
	}
	vision := catalog.SupportsVision
	imageGeneration := group.AllowImageGeneration &&
		(strings.EqualFold(catalog.Mode, "image_generation") || pawContainsStringFold(catalog.SupportedOutputModalities, "image"))
	return vision, imageGeneration, catalog.SupportsPDFInput
}

func pawContainsStringFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

func pawDefaultsConfigured(defaults PawDefaults) bool {
	return defaults.GroupID != 0 || strings.TrimSpace(defaults.ModelID) != "" || strings.TrimSpace(defaults.Reasoning) != ""
}

func pawConfigUnavailableError(message string) error {
	return infraerrors.ServiceUnavailable("CONFIG_UNAVAILABLE", message)
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
