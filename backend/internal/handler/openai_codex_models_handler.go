package handler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CodexModels serves the Codex models manifest for Codex clients.
//
// Codex CLI and the Codex desktop app refresh their model picker from
// GET {base_url}/models?client_version=... (custom provider mode) or
// GET /backend-api/codex/models (chatgpt_base_url mode). Both routes land
// here. ChatGPT manifests are proxied verbatim; custom API key manifests receive
// provider-compatibility normalization and use a short-lived, asynchronously
// revalidated cache to tolerate canceled client requests.
func (h *OpenAIGatewayHandler) CodexModels(c *gin.Context) {
	if c.Request.Context().Err() != nil {
		return
	}
	if isLocalModelsCatalogRequest(c) {
		writeWorkspaceCodexModelsManifest(c, h.gatewayService.GetWorkspaceAvailableModels(c.Request.Context()))
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "invalid_request_error", "API key group is required")
		return
	}
	if apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex models manifest is only available for OpenAI and Composite groups")
		return
	}

	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	maxAccountSwitches = maxAccountSwitchesForRequest(c.Request.Context(), maxAccountSwitches)
	failedAccountIDs := make(map[int64]struct{})
	switchCount := 0
	var lastUpstreamErr error

	for {
		account, err := h.gatewayService.SelectAccountForModelWithExclusions(c.Request.Context(), apiKey.GroupID, "", "", failedAccountIDs)
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if lastUpstreamErr != nil {
				h.errorResponse(c, infraerrors.Code(lastUpstreamErr), "upstream_error", infraerrors.Message(lastUpstreamErr))
				return
			}
			h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI accounts")
			return
		}
		// 让 ops 错误日志携带实际选中的上游账号，便于定位失效账号（#4544）。
		setOpsSelectedAccount(c, account.ID, account.Platform)

		manifest, err := h.gatewayService.FetchCodexModelsManifest(c.Request.Context(), account, c.Query("client_version"), c.GetHeader("If-None-Match"))
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if service.IsRetryableCodexModelsManifestError(err) && switchCount < maxAccountSwitches {
				failedAccountIDs[account.ID] = struct{}{}
				switchCount++
				lastUpstreamErr = err
				continue
			}
			h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}

		if manifest.ETag != "" {
			c.Header("ETag", manifest.ETag)
		}
		if manifest.NotModified {
			c.Status(http.StatusNotModified)
			return
		}
		c.Data(http.StatusOK, "application/json", manifest.Body)
		return
	}
}

type workspaceCodexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type workspaceCodexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int    `json:"limit"`
}

type workspaceCodexModel struct {
	Slug                       string                         `json:"slug"`
	DisplayName                string                         `json:"display_name"`
	Description                string                         `json:"description"`
	DefaultReasoningLevel      string                         `json:"default_reasoning_level"`
	SupportedReasoningLevels   []workspaceCodexReasoningLevel `json:"supported_reasoning_levels"`
	ShellType                  string                         `json:"shell_type"`
	Visibility                 string                         `json:"visibility"`
	SupportedInAPI             bool                           `json:"supported_in_api"`
	Priority                   int                            `json:"priority"`
	AdditionalSpeedTiers       []any                          `json:"additional_speed_tiers"`
	ServiceTiers               []any                          `json:"service_tiers"`
	SupportsReasoningSummaries bool                           `json:"supports_reasoning_summaries"`
	DefaultReasoningSummary    string                         `json:"default_reasoning_summary"`
	SupportVerbosity           bool                           `json:"support_verbosity"`
	DefaultVerbosity           string                         `json:"default_verbosity"`
	ApplyPatchToolType         string                         `json:"apply_patch_tool_type"`
	TruncationPolicy           workspaceCodexTruncationPolicy `json:"truncation_policy"`
	SupportsParallelToolCalls  bool                           `json:"supports_parallel_tool_calls"`
	ContextWindow              int                            `json:"context_window"`
	MaxContextWindow           int                            `json:"max_context_window"`
	EffectiveContextPercent    int                            `json:"effective_context_window_percent"`
	ExperimentalSupportedTools []any                          `json:"experimental_supported_tools"`
	InputModalities            []string                       `json:"input_modalities"`
	SupportsSearchTool         bool                           `json:"supports_search_tool"`
}

func writeWorkspaceCodexModelsManifest(c *gin.Context, availableByPlatform map[string]service.WorkspacePlatformModels) {
	modelsByPlatform := workspaceModelIDsByPlatform(availableByPlatform)
	models := make([]workspaceCodexModel, 0)
	priority := 1000
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic, service.PlatformGrok} {
		for _, modelID := range modelsByPlatform[platform] {
			item := workspaceModelListItemForPlatform(platform, modelID)
			defaultEffort := "medium"
			if platform == service.PlatformGrok && grokModelSupportsConfigurableReasoning(modelID) {
				defaultEffort = "high"
			}
			models = append(models, workspaceCodexModel{
				Slug:                  modelID,
				DisplayName:           item.DisplayName,
				Description:           fmt.Sprintf("%s via local relay.", item.DisplayName),
				DefaultReasoningLevel: defaultEffort,
				SupportedReasoningLevels: []workspaceCodexReasoningLevel{
					{Effort: "low", Description: "Fast responses with lighter reasoning"},
					{Effort: "medium", Description: "Balances speed and reasoning depth"},
					{Effort: "high", Description: "Greater reasoning depth for complex tasks"},
				},
				ShellType:                  "shell_command",
				Visibility:                 "list",
				SupportedInAPI:             true,
				Priority:                   priority,
				AdditionalSpeedTiers:       []any{},
				ServiceTiers:               []any{},
				SupportsReasoningSummaries: true,
				DefaultReasoningSummary:    "none",
				SupportVerbosity:           true,
				DefaultVerbosity:           "low",
				ApplyPatchToolType:         "freeform",
				TruncationPolicy:           workspaceCodexTruncationPolicy{Mode: "tokens", Limit: 10000},
				SupportsParallelToolCalls:  true,
				ContextWindow:              200000,
				MaxContextWindow:           200000,
				EffectiveContextPercent:    95,
				ExperimentalSupportedTools: []any{},
				InputModalities:            []string{"text", "image"},
				SupportsSearchTool:         true,
			})
			priority--
		}
	}
	payload := gin.H{"models": models}
	body, _ := json.Marshal(payload)
	etag := fmt.Sprintf(`W/"%x"`, sha256.Sum256(body))
	c.Header("ETag", etag)
	if strings.TrimSpace(c.GetHeader("If-None-Match")) == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.JSON(http.StatusOK, payload)
}
