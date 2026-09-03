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
	OpenAIChat        gin.HandlerFunc
	GatewayChat       gin.HandlerFunc
	OpenAIResponses   gin.HandlerFunc
	GatewayResponses  gin.HandlerFunc
	CompositeResolver *service.CompositeRouteResolver
	APIKeyService     *service.APIKeyService
	OpsService        *service.OpsService
	Config            *config.Config
}

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
		var userRates map[int64]float64
		if deps.APIKeyService != nil {
			userRates, _ = deps.APIKeyService.GetUserGroupRates(c.Request.Context(), subject.UserID)
		}
		c.JSON(http.StatusOK, PawConfigResponse{Data: toPawConfigData(config, userRates)})
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

	paw.POST("/files", pawUploadHandler(attachmentService))
	paw.POST("/images/generations", pawImageGenerationHandler(imageService, deps))
	paw.POST("/images/edits", pawImageEditHandler(imageService, deps))
	paw.POST("/chat/completions", pawChatHandler(deps.ChatService, chatService, deps))
	paw.POST("/responses", pawResponsesHandler(deps.ChatService, chatService, deps))
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

// PawGroupHeader 是 Responses 这条路上选分组的入口。
//
// 为什么是请求头而不是请求体：**请求体必须原样是一份 Responses 载荷**，
// 它是 codex 生成的，我们往里面塞自己的字段就可能被上游当成非法参数退回来。
const PawGroupHeader = "X-Paw-Group-Id"

// pawResponsesHandler —— 工作台里的 codex 走这条。
//
// 形状和 pawChatHandler 一样：JWT 进来 → 校验分组/模型 → 就地换上服务端自己的 key
// → 交给同一个网关 handler。**客户端全程拿不到任何 API key**。
//
// 两处和 chat 那条不同，都是被 codex 逼出来的：
//
//   - **请求体原样透传**。codex 发的是完整载荷（instructions / tools / input，实测 ~47KB），
//     重拼一份就是惄惄改 agent 的行为。所以这里只把 body 读出来**看一眼**拿 model。
//   - **分组走请求头**（见 PawGroupHeader），因为请求体不归我们支配。
//
// composite 在 handler 里就地解析，和 pawChatHandler 一致 —— **不能**改成网关那条路的
// autoGroupModelRouting 中间件：那条是按请求体里的 model 自己选分组的，会把调用方
// 明确指定的分组覆掉。
func pawResponsesHandler(primaryChat, localChat *service.PawChatService, deps PawRouteDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		chat := primaryChat
		if chat == nil {
			chat = localChat
		}
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

		groupID, err := strconv.ParseInt(strings.TrimSpace(c.GetHeader(PawGroupHeader)), 10, 64)
		if err != nil || groupID <= 0 {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", PawGroupHeader+" header is required")
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

		var payload pawResponsesPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid Responses payload")
			return
		}

		resolution, err := chat.PrepareResponses(c.Request.Context(), subject.UserID, service.PawResponsesRequest{
			GroupID: groupID,
			ModelID: payload.Model,
		})
		if err != nil {
			pawChatServiceError(c, err)
			return
		}
		middleware.ReplaceAuthenticatedAPIKey(c, resolution.APIKey, resolution.Subscription)

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
		case (platform == service.PlatformOpenAI || platform == service.PlatformGrok) && deps.OpenAIResponses != nil:
			deps.OpenAIResponses(c)
		case deps.GatewayResponses != nil:
			deps.GatewayResponses(c)
		default:
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable, "Paw responses gateway is unavailable")
		}
	}
}

// pawResponsesPayload 只描述我们要**读**的那几个字段。故意不写全：写全了就会有人
// 想把它序列化回去，而序列化回去就会丢掉 codex 发的、我们不认识的字段。
type pawResponsesPayload struct {
	Model string `json:"model"`
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

func toPawConfigData(config *service.PawConfig, userRates ...map[int64]float64) PawConfigData {
	result := PawConfigData{User: PawUser{ID: config.User.ID, Name: config.User.Name, Email: config.User.Email}, Defaults: PawDefaults{GroupID: config.Defaults.GroupID, ModelID: config.Defaults.ModelID, Reasoning: config.Defaults.Reasoning}}
	result.Groups = make([]PawGroup, 0, len(config.Groups))
	var rates map[int64]float64
	if len(userRates) > 0 {
		rates = userRates[0]
	}
	for _, group := range config.Groups {
		mapped := PawGroup{
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
			Models:             make([]PawModel, 0, len(group.Models)),
		}
		if rate, ok := rates[group.ID]; ok {
			rateValue := rate
			mapped.UserRateMultiplier = &rateValue
		}
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
