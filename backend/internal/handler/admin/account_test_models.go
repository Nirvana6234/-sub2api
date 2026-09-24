package admin

import (
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// AvailableModelsForAccount returns the same model choices used by the admin
// test surface without performing an upstream request.
func AvailableModelsForAccount(account *service.Account) any {
	if account == nil {
		return []any{}
	}

	if account.IsOpenAI() {
		if account.IsOpenAIPassthroughEnabled() {
			return openai.DefaultModels
		}
		mapping := account.GetModelMapping()
		if len(mapping) == 0 {
			return openai.DefaultModels
		}
		models := make([]openai.Model, 0, len(mapping))
		for requestedModel := range mapping {
			found := false
			for _, defaultModel := range openai.DefaultModels {
				if defaultModel.ID == requestedModel {
					models = append(models, defaultModel)
					found = true
					break
				}
			}
			if !found {
				models = append(models, openai.Model{
					ID: requestedModel, Object: "model", Type: "model", DisplayName: requestedModel,
				})
			}
		}
		return models
	}

	if account.IsGemini() {
		if account.IsOAuth() {
			return geminicli.DefaultModels
		}
		mapping := account.GetModelMapping()
		if len(mapping) == 0 {
			return geminicli.DefaultModels
		}
		models := make([]geminicli.Model, 0, len(mapping))
		for requestedModel := range mapping {
			found := false
			for _, defaultModel := range geminicli.DefaultModels {
				if defaultModel.ID == requestedModel {
					models = append(models, defaultModel)
					found = true
					break
				}
			}
			if !found {
				models = append(models, geminicli.Model{
					ID: requestedModel, Type: "model", DisplayName: requestedModel,
				})
			}
		}
		return models
	}

	if account.Platform == service.PlatformAntigravity {
		return antigravity.DefaultModels()
	}

	if account.Platform == service.PlatformGrok {
		defaultModels := xai.DefaultModels()
		hasExplicitMapping := false
		switch rawMapping := account.Credentials["model_mapping"].(type) {
		case map[string]any:
			hasExplicitMapping = len(rawMapping) > 0
		case map[string]string:
			hasExplicitMapping = len(rawMapping) > 0
		}
		if !hasExplicitMapping {
			return defaultModels
		}
		mapping := account.GetModelMapping()
		if len(mapping) == 0 {
			return defaultModels
		}
		defaultByID := make(map[string]xai.Model, len(defaultModels))
		for _, model := range defaultModels {
			defaultByID[model.ID] = model
		}
		requestedModels := make([]string, 0, len(mapping))
		for requestedModel := range mapping {
			requestedModels = append(requestedModels, requestedModel)
		}
		sort.Strings(requestedModels)
		models := make([]xai.Model, 0, len(requestedModels))
		for _, requestedModel := range requestedModels {
			if defaultModel, found := defaultByID[requestedModel]; found {
				models = append(models, defaultModel)
				continue
			}
			models = append(models, xai.Model{
				ID: requestedModel, Object: "model", OwnedBy: "xai", DisplayName: requestedModel,
			})
		}
		return models
	}

	if account.IsOAuth() {
		return claude.DefaultModels
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return claude.DefaultModels
	}
	models := make([]claude.Model, 0, len(mapping))
	for requestedModel := range mapping {
		found := false
		for _, defaultModel := range claude.DefaultModels {
			if defaultModel.ID == requestedModel {
				models = append(models, defaultModel)
				found = true
				break
			}
		}
		if !found {
			models = append(models, claude.Model{
				ID: requestedModel, Type: "model", DisplayName: requestedModel,
			})
		}
	}
	return models
}
