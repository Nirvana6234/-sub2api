package routes

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type pawDefaultsRequest struct {
	GroupID   int64  `json:"group_id"`
	ModelID   string `json:"model_id"`
	Reasoning string `json:"reasoning"`
}

type pawAutoGroupRequest struct {
	// AutoGroup is accepted for wire compatibility but never applied: this
	// endpoint only ever saves candidates, and the internal key's flag stays on.
	// See pawSaveAutoGroupHandler for why disabling it is not offered.
	AutoGroup         bool    `json:"auto_group"`
	AutoGroupIDs      []int64 `json:"auto_group_ids"`
	AutoGroupStrategy string  `json:"auto_group_strategy"`
}

type pawImageGenerationRequest struct {
	GroupID int64  `json:"group_id"`
	ModelID string `json:"model_id"`
	Prompt  string `json:"prompt"`
	Size    string `json:"size"`
	N       int    `json:"n"`
	Stream  bool   `json:"stream"`
}

type PawRouteDependencies struct {
	ChatService       *service.PawChatService
	OpenAIGateway     *handler.OpenAIGatewayHandler
	Gateway           *handler.GatewayHandler
	OpenAIChat        gin.HandlerFunc
	GatewayChat       gin.HandlerFunc
	CompositeResolver *service.CompositeRouteResolver
	APIKeyService     *service.APIKeyService
	OpsService        *service.OpsService
	Config            *config.Config
}

// PawGroupHeader is set by the desktop relay because the Responses payload
// must remain byte-for-byte a Codex payload and cannot carry routing metadata.
const PawGroupHeader = "X-Paw-Group-Id"

// PawClientUserAgentHeader carries the editor client's own User-Agent on the
// Messages routes.
//
// It cannot travel as User-Agent: the account session is bound to the network
// fingerprint it was issued under, IP and User-Agent together, and a mismatch
// revokes the whole session family. The desktop client therefore keeps its own
// User-Agent on the wire and sends the editor's separately, and the handler puts
// it back after that check has passed. Groups restricted to Claude Code look at
// exactly this value.
const PawClientUserAgentHeader = "X-Paw-Client-User-Agent"

func RegisterPawRoutes(v1 *gin.RouterGroup, svc *service.PawConfigService, jwtAuth middleware.JWTAuthMiddleware, settingService *service.SettingService, panelRateLimiter *middleware.PanelRateLimiter, dependencies ...PawRouteDependencies) {
	if v1 == nil || svc == nil {
		return
	}

	var deps PawRouteDependencies
	if len(dependencies) > 0 {
		deps = dependencies[0]
	}
	attachmentService := service.NewPawAttachmentService(service.NewPawAttachmentMemoryRepository(), 24*time.Hour, 20<<20)
	chatService := service.NewPawChatService(svc, service.APIKeyPawChatKeySource{Service: deps.APIKeyService}, attachmentService)
	responsesChat := deps.ChatService
	if responsesChat == nil {
		responsesChat = chatService
	}
	imageService := service.NewPawImageService(svc, service.APIKeyPawChatKeySource{Service: deps.APIKeyService}, attachmentService)

	paw := v1.Group("/paw")
	paw.Use(gin.HandlerFunc(jwtAuth))
	paw.Use(middleware.BackendModeUserGuard(settingService))
	paw.Use(panelRateLimiter.Global())
	if deps.Config != nil && deps.Config.Gateway.MaxBodySize > 0 {
		paw.Use(middleware.RequestBodyLimit(deps.Config.Gateway.MaxBodySize))
	}
	paw.Use(middleware.ClientRequestID())
	paw.Use(handler.OpsErrorLoggerMiddleware(deps.OpsService))
	paw.Use(handler.InboundEndpointMiddleware())
	paw.Use(middleware.PlaygroundRequestContext)

	paw.GET("/config", func(c *gin.Context) {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			return
		}
		config, err := svc.GetAvailableConfig(c.Request.Context(), subject.UserID)
		if err != nil {
			pawConfigError(c, err)
			return
		}
		c.JSON(http.StatusOK, PawConfigResponse{Data: toPawConfigData(config)})
	})

	paw.PUT("/config/defaults", func(c *gin.Context) {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			return
		}
		var req pawDefaultsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			pawConfigError(c, err)
			return
		}
		if err := svc.SaveDefaults(c.Request.Context(), subject.UserID, service.PawDefaults{GroupID: req.GroupID, ModelID: req.ModelID, Reasoning: req.Reasoning}); err != nil {
			pawConfigError(c, err)
			return
		}
		c.JSON(http.StatusOK, PawConfigResponse{Data: PawConfigData{Defaults: PawDefaults{GroupID: req.GroupID, ModelID: req.ModelID, Reasoning: req.Reasoning}}})
	})

	paw.GET("/auto-group", pawGetAutoGroupHandler(deps.APIKeyService))
	paw.PUT("/auto-group", pawSaveAutoGroupHandler(deps.APIKeyService))

	paw.POST("/files", pawUploadHandler(attachmentService))
	paw.POST("/images/generations", pawImageGenerationHandler(imageService, deps))
	paw.POST("/images/edits", pawImageEditHandler(imageService, deps))
	paw.POST("/chat/completions", pawChatHandler(deps.ChatService, chatService, deps))
	paw.POST("/responses", pawResponsesHandler(responsesChat, deps))
	paw.POST("/messages", pawMessagesHandler(responsesChat, deps, false))
	paw.POST("/messages/count_tokens", pawMessagesHandler(responsesChat, deps, true))
}

func pawGetAutoGroupHandler(apiKeys *service.APIKeyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			pawChatError(c, http.StatusUnauthorized, PawErrorCodeAuthRequired, "authenticated user is required")
			return
		}
		if apiKeys == nil {
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeConfigUnavailable, "Paw automatic routing is unavailable")
			return
		}
		key, _, err := (service.APIKeyPawChatKeySource{Service: apiKeys}).ResolvePawAPIKey(c.Request.Context(), subject.UserID, 0)
		if err != nil || key == nil {
			pawChatServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, PawAutoGroupResponse{Data: PawAutoGroupData{
			AutoGroup:         key.AutoGroup,
			AutoGroupIDs:      append([]int64(nil), key.AutoGroupIDs...),
			AutoGroupStrategy: key.AutoGroupStrategy,
		}})
	}
}

func pawSaveAutoGroupHandler(apiKeys *service.APIKeyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			pawChatError(c, http.StatusUnauthorized, PawErrorCodeAuthRequired, "authenticated user is required")
			return
		}
		if apiKeys == nil {
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeConfigUnavailable, "Paw automatic routing is unavailable")
			return
		}
		var req pawAutoGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid automatic routing settings")
			return
		}
		key, _, err := (service.APIKeyPawChatKeySource{Service: apiKeys}).ResolvePawAPIKey(c.Request.Context(), subject.UserID, 0)
		if err != nil || key == nil {
			pawChatServiceError(c, err)
			return
		}
		// Automatic routing stays enabled on this internal key for its whole life,
		// so req.AutoGroup is deliberately ignored. Which mode the desktop client
		// runs in is decided by the X-Paw-Group-Id header it sends on each request,
		// never by this flag — there is nothing here to switch off.
		//
		// Writing false would break two things that are invisible from this handler:
		// hydrateAutoGroupIDs short-circuits on !AutoGroup, so the candidate list
		// stops being readable and the client's dialog reopens empty; and the web
		// Playground's isPlaygroundEligibleKey requires auto_group || group_id > 0,
		// while this key intentionally never carries a group_id of its own.
		enabled := true
		ids := append([]int64(nil), req.AutoGroupIDs...)
		strategy := req.AutoGroupStrategy
		update := service.UpdateAPIKeyRequest{
			AutoGroup:         &enabled,
			AutoGroupIDs:      &ids,
			AutoGroupStrategy: &strategy,
		}
		updated, err := apiKeys.Update(c.Request.Context(), key.ID, subject.UserID, update)
		if err != nil {
			pawChatServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, PawAutoGroupResponse{Data: PawAutoGroupData{
			AutoGroup:         updated.AutoGroup,
			AutoGroupIDs:      append([]int64(nil), updated.AutoGroupIDs...),
			AutoGroupStrategy: updated.AutoGroupStrategy,
		}})
	}
}

func pawUploadHandler(attachments *service.PawAttachmentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAuthRequired, "Paw accepts only the authenticated account session")
			return
		}
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			pawChatError(c, http.StatusUnauthorized, PawErrorCodeAuthRequired, "authenticated user is required")
			return
		}
		if attachments == nil {
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeConfigUnavailable, "Paw attachments are unavailable")
			return
		}

		file, err := pawFirstMultipartFile(c)
		if err != nil {
			pawUploadError(c, err)
			return
		}
		if file == nil {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAttachmentInvalid, "file upload is required")
			return
		}
		handle, err := file.Open()
		if err != nil {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAttachmentInvalid, "failed to open uploaded file")
			return
		}
		defer handle.Close()

		data, err := io.ReadAll(handle)
		if err != nil {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAttachmentInvalid, "failed to read uploaded file")
			return
		}
		contentType := strings.TrimSpace(file.Header.Get("Content-Type"))
		attachment, err := attachments.Upload(c.Request.Context(), subject.UserID, file.Filename, contentType, data)
		if err != nil {
			pawUploadError(c, err)
			return
		}
		c.JSON(http.StatusOK, PawAttachmentResponse{Data: PawAttachmentData{
			ID:        attachment.ID,
			Filename:  attachment.Filename,
			MIMEType:  attachment.MIMEType,
			Size:      attachment.Size,
			ExpiresAt: attachment.ExpiresAt,
		}})
	}
}

func pawImageGenerationHandler(images *service.PawImageService, deps PawRouteDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAuthRequired, "Paw accepts only the authenticated account session")
			return
		}
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			pawChatError(c, http.StatusUnauthorized, PawErrorCodeAuthRequired, "authenticated user is required")
			return
		}
		if images == nil {
			pawImageError(c, http.StatusServiceUnavailable, PawErrorCodeConfigUnavailable, "Paw image configuration is unavailable")
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pawImageError(c, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body")
			return
		}
		resetRequestBody(c, body)
		if pawBodyCredentialSelectorPresent(body) {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAuthRequired, "Paw accepts only the authenticated account session")
			return
		}

		var req pawImageGenerationRequest
		if err := json.Unmarshal(body, &req); err != nil {
			pawImageError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid Paw image request")
			return
		}
		resolution, err := images.ValidateGeneration(c.Request.Context(), subject.UserID, service.PawImageGenerationRequest{
			GroupID: req.GroupID,
			ModelID: req.ModelID,
			Prompt:  req.Prompt,
			Size:    req.Size,
			N:       req.N,
			Stream:  req.Stream,
		})
		if err != nil {
			pawImageServiceError(c, err)
			return
		}
		middleware.ReplaceAuthenticatedAPIKey(c, resolution.APIKey, nil)
		resetRequestBody(c, body)
		pawDispatchImageRoute(c, deps, resolution)
	}
}

func pawImageEditHandler(images *service.PawImageService, deps PawRouteDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAuthRequired, "Paw accepts only the authenticated account session")
			return
		}
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			pawChatError(c, http.StatusUnauthorized, PawErrorCodeAuthRequired, "authenticated user is required")
			return
		}
		if images == nil {
			pawImageError(c, http.StatusServiceUnavailable, PawErrorCodeConfigUnavailable, "Paw image configuration is unavailable")
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pawImageError(c, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body")
			return
		}
		resetRequestBody(c, body)

		req, err := images.ParseEditMultipart(c.GetHeader("Content-Type"), body)
		if err != nil {
			pawImageServiceError(c, err)
			return
		}
		resolution, err := images.ValidateGeneration(c.Request.Context(), subject.UserID, service.PawImageGenerationRequest{
			GroupID: req.GroupID,
			ModelID: req.ModelID,
			Prompt:  req.Prompt,
			Size:    req.Size,
			N:       req.N,
		})
		if err != nil {
			pawImageServiceError(c, err)
			return
		}
		middleware.ReplaceAuthenticatedAPIKey(c, resolution.APIKey, nil)
		resetRequestBody(c, body)
		pawDispatchImageRoute(c, deps, resolution)
	}
}

func pawChatHandler(primaryChat *service.PawChatService, localChat *service.PawChatService, deps PawRouteDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAuthRequired, "Paw accepts only the authenticated account session")
			return
		}
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			pawChatError(c, http.StatusUnauthorized, PawErrorCodeAuthRequired, "authenticated user is required")
			return
		}
		if primaryChat == nil && localChat == nil {
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeConfigUnavailable, "Paw chat configuration is unavailable")
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body")
			return
		}
		resetRequestBody(c, body)
		if pawBodyCredentialSelectorPresent(body) {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAuthRequired, "Paw accepts only the authenticated account session")
			return
		}

		var request PawChatRequest
		if err := json.Unmarshal(body, &request); err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid Paw chat request")
			return
		}
		chat := primaryChat
		if chat == nil || len(request.Attachments) > 0 {
			chat = localChat
		}
		if chat == nil {
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeConfigUnavailable, "Paw chat configuration is unavailable")
			return
		}
		resolution, err := chat.Prepare(c.Request.Context(), subject.UserID, service.PawChatRequest{
			GroupID:   request.GroupID,
			ModelID:   request.ModelID,
			Reasoning: request.Reasoning,
			Messages: func() []service.PawChatMessage {
				messages := make([]service.PawChatMessage, 0, len(request.Messages))
				for _, message := range request.Messages {
					messages = append(messages, service.PawChatMessage{Role: message.Role, Content: message.Content})
				}
				return messages
			}(),
			Stream: request.Stream,
			Attachments: func() []service.PawAttachmentReference {
				attachments := make([]service.PawAttachmentReference, 0, len(request.Attachments))
				for _, attachment := range request.Attachments {
					attachments = append(attachments, service.PawAttachmentReference{ID: attachment.ID})
				}
				return attachments
			}(),
		})
		if err != nil {
			pawChatServiceError(c, err)
			return
		}
		middleware.ReplaceAuthenticatedAPIKey(c, resolution.APIKey, resolution.Subscription)
		resetRequestBody(c, resolution.Body)

		if deps.CompositeResolver != nil && resolution.Group != nil && resolution.Group.Platform == service.PlatformComposite {
			decision, resolveErr := deps.CompositeResolver.Resolve(c.Request.Context(), resolution.Group.ID, resolution.Model, service.CompositeRouteEndpointChatCompletions)
			if resolveErr != nil {
				pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable, "failed to resolve the selected model route")
				return
			}
			if decision.Matched {
				c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
			}
		}

		platform := resolution.Group.Platform
		if resolved, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
			platform = resolved
		}
		switch {
		case (platform == service.PlatformOpenAI || platform == service.PlatformGrok) && deps.OpenAIChat != nil:
			deps.OpenAIChat(c)
		case deps.GatewayChat != nil:
			deps.GatewayChat(c)
		default:
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable, "Paw chat gateway is unavailable")
		}
	}
}

func pawResponsesHandler(chat *service.PawChatService, deps PawRouteDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAuthRequired, "Paw accepts only the authenticated account session")
			return
		}
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			pawChatError(c, http.StatusUnauthorized, PawErrorCodeAuthRequired, "authenticated user is required")
			return
		}
		if chat == nil {
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeConfigUnavailable, "Paw chat configuration is unavailable")
			return
		}

		groupHeader := strings.TrimSpace(c.GetHeader(PawGroupHeader))
		groupID := int64(0)
		var err error
		if groupHeader != "" && !strings.EqualFold(groupHeader, "auto") {
			groupID, err = strconv.ParseInt(groupHeader, 10, 64)
		}
		if err != nil || groupID < 0 {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeGroupForbidden, "a valid Paw group is required")
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body")
			return
		}
		resetRequestBody(c, body)
		if pawBodyCredentialSelectorPresent(body) {
			pawChatError(c, http.StatusBadRequest, PawErrorCodeAuthRequired, "Paw accepts only the authenticated account session")
			return
		}
		var request struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body, &request); err != nil || strings.TrimSpace(request.Model) == "" {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "a Responses model is required")
			return
		}

		resolution, err := chat.PrepareResponses(c.Request.Context(), subject.UserID, groupID, request.Model)
		if err != nil {
			pawChatServiceError(c, err)
			return
		}
		middleware.ReplaceAuthenticatedAPIKey(c, resolution.APIKey, resolution.Subscription)
		resetRequestBody(c, body)

		if deps.CompositeResolver != nil && resolution.Group != nil && resolution.Group.Platform == service.PlatformComposite {
			decision, resolveErr := deps.CompositeResolver.Resolve(c.Request.Context(), resolution.Group.ID, resolution.Model, service.CompositeRouteEndpointResponses)
			if resolveErr != nil {
				pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable, "failed to resolve the selected model route")
				return
			}
			if decision.Matched {
				c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
			}
		}

		platform := resolution.Group.Platform
		if resolved, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
			platform = resolved
		}
		switch {
		case (platform == service.PlatformOpenAI || platform == service.PlatformGrok) && deps.OpenAIGateway != nil:
			deps.OpenAIGateway.Responses(c)
		case deps.Gateway != nil:
			// Responses for non-OpenAI platforms is handled by the generic gateway.
			deps.Gateway.Responses(c)
		default:
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable, "Paw Responses gateway is unavailable")
		}
		observeAutoGroupRequestResult(c, deps.APIKeyService, resolution.APIKey, request.Model)
	}
}

// pawMessagesHandler serves the Anthropic Messages protocol for a signed-in
// desktop client, so Claude Code and the editor extensions built on it can use the
// account without ever holding a key.
//
// Everything after group resolution is the API-key gateway, unchanged: the
// dispatch below is the one gateway.go makes for /v1/messages and
// /v1/messages/count_tokens, and it reads the group from the same place, so a
// request here is handled exactly like one made with a key for that group. What
// differs is only how the caller was authenticated and how the group was chosen —
// by the X-Paw-Group-Id header, because the body must stay a verbatim Anthropic
// request.
//
// There is deliberately no automatic routing here: it is defined over OpenAI
// candidates, and a Messages caller names its group explicitly.
func pawMessagesHandler(chat *service.PawChatService, deps PawRouteDependencies, countTokens bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawMessagesError(c, http.StatusBadRequest, "invalid_request_error", "Paw accepts only the authenticated account session")
			return
		}
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			pawMessagesError(c, http.StatusUnauthorized, "authentication_error", "authenticated user is required")
			return
		}
		if chat == nil {
			pawMessagesError(c, http.StatusServiceUnavailable, "api_error", "Paw messages gateway is unavailable")
			return
		}

		groupID, err := strconv.ParseInt(strings.TrimSpace(c.GetHeader(PawGroupHeader)), 10, 64)
		if err != nil || groupID <= 0 {
			pawMessagesError(c, http.StatusBadRequest, "invalid_request_error", "a valid Paw group is required")
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pawMessagesError(c, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
			return
		}
		if pawBodyCredentialSelectorPresent(body) {
			pawMessagesError(c, http.StatusBadRequest, "invalid_request_error", "Paw accepts only the authenticated account session")
			return
		}
		var request struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &request)

		resolution, err := chat.PrepareMessages(c.Request.Context(), subject.UserID, groupID)
		if err != nil {
			pawMessagesServiceError(c, err)
			return
		}
		middleware.ReplaceAuthenticatedAPIKey(c, resolution.APIKey, resolution.Subscription)
		resetRequestBody(c, body)
		restorePawClientUserAgent(c)

		if deps.CompositeResolver != nil && resolution.Group != nil && resolution.Group.Platform == service.PlatformComposite {
			endpoint := service.CompositeRouteEndpointMessages
			if countTokens {
				endpoint = service.CompositeRouteEndpointCountTokens
			}
			decision, resolveErr := deps.CompositeResolver.Resolve(c.Request.Context(), resolution.Group.ID, strings.TrimSpace(request.Model), endpoint)
			if resolveErr != nil {
				pawMessagesError(c, http.StatusServiceUnavailable, "api_error", "failed to resolve the selected model route")
				return
			}
			if decision.Matched {
				c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
			}
		}

		platform := resolution.Group.Platform
		if resolved, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
			platform = resolved
		}
		switch pawMessagesTargetFor(platform, countTokens) {
		case pawCountTokensOpenAI:
			if deps.OpenAIGateway != nil {
				deps.OpenAIGateway.CountTokens(c)
				return
			}
		case pawCountTokensGrok:
			if deps.OpenAIGateway != nil {
				deps.OpenAIGateway.GrokCountTokens(c)
				return
			}
		case pawCountTokensAnthropic:
			if deps.Gateway != nil {
				deps.Gateway.CountTokens(c)
				return
			}
		case pawMessagesOpenAI:
			if deps.OpenAIGateway != nil {
				deps.OpenAIGateway.Messages(c)
				return
			}
		case pawMessagesAnthropic:
			if deps.Gateway != nil {
				deps.Gateway.Messages(c)
				return
			}
		}
		pawMessagesError(c, http.StatusServiceUnavailable, "api_error", "Paw messages gateway is unavailable")
	}
}

// pawMessagesTarget names which gateway handler serves a Messages request.
type pawMessagesTarget int

const (
	pawMessagesAnthropic pawMessagesTarget = iota
	pawMessagesOpenAI
	pawCountTokensAnthropic
	pawCountTokensOpenAI
	pawCountTokensGrok
)

// pawMessagesTargetFor is the choice gateway.go makes for /v1/messages and
// /v1/messages/count_tokens, kept as one function so a test can hold it to that:
// OpenAI and Grok groups take Messages through the OpenAI gateway's bridge, every
// other platform through the Anthropic-compatible one, and count_tokens has its own
// three-way split — bridged upstream for OpenAI, estimated locally for Grok.
func pawMessagesTargetFor(platform string, countTokens bool) pawMessagesTarget {
	if countTokens {
		switch platform {
		case service.PlatformOpenAI:
			return pawCountTokensOpenAI
		case service.PlatformGrok:
			return pawCountTokensGrok
		default:
			return pawCountTokensAnthropic
		}
	}
	switch platform {
	case service.PlatformOpenAI, service.PlatformGrok:
		return pawMessagesOpenAI
	default:
		return pawMessagesAnthropic
	}
}

// restorePawClientUserAgent puts the editor client's User-Agent back where the
// gateway reads it. See PawClientUserAgentHeader for why it could not simply
// arrive as one. Only a plain printable value is accepted: this is a string the
// caller controls, and it ends up in logs and in the Claude Code check.
func restorePawClientUserAgent(c *gin.Context) {
	forwarded := strings.TrimSpace(c.GetHeader(PawClientUserAgentHeader))
	c.Request.Header.Del(PawClientUserAgentHeader)
	if forwarded == "" || len(forwarded) > 512 {
		return
	}
	for _, r := range forwarded {
		if r < 0x20 || r == 0x7f {
			return
		}
	}
	c.Request.Header.Set("User-Agent", forwarded)
}

// pawMessagesError answers in the shape Anthropic clients parse. The paw error
// envelope is not one they recognise, and Claude Code would show its raw JSON.
func pawMessagesError(c *gin.Context, status int, errorType, message string) {
	if c == nil {
		return
	}
	c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": errorType, "message": message}})
	c.Abort()
}

func pawMessagesServiceError(c *gin.Context, err error) {
	status := serviceErrorStatus(err)
	pawMessagesError(c, status, anthropicErrorTypeForStatus(status), serviceErrorMessage(err))
}

func anthropicErrorTypeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}

func pawDispatchImageRoute(c *gin.Context, deps PawRouteDependencies, resolution *service.PawImageResolution) {
	if resolution == nil || resolution.Group == nil {
		pawImageError(c, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable, "Paw image gateway is unavailable")
		return
	}
	if deps.CompositeResolver != nil && resolution.Group.Platform == service.PlatformComposite {
		decision, resolveErr := deps.CompositeResolver.Resolve(c.Request.Context(), resolution.Group.ID, resolution.Model.ID, service.CompositeRouteEndpointImages)
		if resolveErr != nil {
			pawImageError(c, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable, "failed to resolve the selected model route")
			return
		}
		if decision.Matched {
			c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
		}
	}

	platform := resolution.Group.Platform
	if resolved, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
		platform = resolved
	}
	switch platform {
	case service.PlatformOpenAI:
		if deps.OpenAIGateway != nil {
			deps.OpenAIGateway.Images(c)
			return
		}
	case service.PlatformGrok:
		if deps.OpenAIGateway != nil {
			deps.OpenAIGateway.GrokImages(c)
			return
		}
	}
	pawImageError(c, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable, "Paw image gateway is unavailable")
}

func pawFirstMultipartFile(c *gin.Context) (*multipart.FileHeader, error) {
	if c == nil || c.Request == nil {
		return nil, nil
	}
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil && err != http.ErrNotMultipart {
		return nil, err
	}
	if c.Request.MultipartForm == nil {
		return nil, nil
	}
	for _, files := range c.Request.MultipartForm.File {
		if len(files) > 0 && files[0] != nil {
			return files[0], nil
		}
	}
	return nil, nil
}

func pawUploadError(c *gin.Context, err error) {
	code := PawErrorCodeAttachmentInvalid
	message := "file upload failed"
	status := http.StatusBadRequest
	if err != nil {
		if appErr := infraerrors.FromError(err); appErr != nil {
			status = int(appErr.Code)
			if reason := infraerrors.Reason(err); reason != "" {
				code = reason
			}
			if msg := infraerrors.Message(err); msg != "" {
				message = msg
			}
		} else {
			message = err.Error()
		}
	}
	pawChatError(c, status, code, message)
}

func pawImageServiceError(c *gin.Context, err error) {
	status := serviceErrorStatus(err)
	code := serviceErrorReason(err)
	if code == "" {
		code = PawErrorCodeConfigUnavailable
	}
	pawImageError(c, status, code, serviceErrorMessage(err))
}

func pawImageError(c *gin.Context, status int, code, message string) {
	if c == nil {
		return
	}
	c.JSON(status, PawErrorResponse{Error: PawError{Code: code, Message: message}})
	c.Abort()
}

func pawCredentialSelectorPresent(c *gin.Context) bool {
	if c == nil {
		return false
	}
	for _, header := range []string{"x-api-key", "x-goog-api-key", middleware.PlaygroundKeyIDHeader} {
		if strings.TrimSpace(c.GetHeader(header)) != "" {
			return true
		}
	}
	for _, query := range []string{"key", "api_key", "key_id", "api_key_id"} {
		if strings.TrimSpace(c.Query(query)) != "" {
			return true
		}
	}
	return false
}

func pawBodyCredentialSelectorPresent(body []byte) bool {
	if len(body) == 0 || !json.Valid(body) {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return false
	}
	for _, field := range []string{"key", "api_key", "key_id", "api_key_id", "provider_api_key"} {
		if _, ok := fields[field]; ok {
			return true
		}
	}
	return false
}

func pawChatServiceError(c *gin.Context, err error) {
	status := serviceErrorStatus(err)
	code := serviceErrorReason(err)
	if code == "" {
		code = PawErrorCodeConfigUnavailable
	}
	pawChatError(c, status, code, serviceErrorMessage(err))
}

func serviceErrorStatus(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	if appErr := infraerrors.FromError(err); appErr != nil {
		return int(appErr.Code)
	}
	return http.StatusInternalServerError
}

func serviceErrorReason(err error) string {
	if err == nil {
		return ""
	}
	return infraerrors.Reason(err)
}

func serviceErrorMessage(err error) string {
	if err == nil {
		return "Paw chat request failed"
	}
	if message := infraerrors.Message(err); message != "" {
		return message
	}
	return err.Error()
}

func pawChatError(c *gin.Context, status int, code, message string) {
	if c == nil {
		return
	}
	c.JSON(status, PawErrorResponse{Error: PawError{Code: code, Message: message}})
	c.Abort()
}

func toPawConfigData(config *service.PawConfig) PawConfigData {
	result := PawConfigData{User: PawUser{ID: config.User.ID, Name: config.User.Name, Email: config.User.Email}, Defaults: PawDefaults{GroupID: config.Defaults.GroupID, ModelID: config.Defaults.ModelID, Reasoning: config.Defaults.Reasoning}}
	result.Groups = make([]PawGroup, 0, len(config.Groups))
	for _, group := range config.Groups {
		mapped := PawGroup{ID: group.ID, Name: group.Name, Description: group.Description, Models: make([]PawModel, 0, len(group.Models))}
		for _, model := range group.Models {
			mapped.Models = append(mapped.Models, PawModel{ID: model.ID, Name: model.Name, OwnedBy: model.OwnedBy, Reasoning: PawReasoningCapability{Supported: model.Reasoning.Supported, Values: model.Reasoning.Values, Default: model.Reasoning.Default}, Vision: model.Vision, ImageGeneration: model.ImageGeneration, FileInput: model.FileInput})
		}
		result.Groups = append(result.Groups, mapped)
	}
	return result
}

func pawConfigError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, PawErrorResponse{Error: PawError{Code: PawErrorCodeConfigUnavailable, Message: err.Error()}})
}
