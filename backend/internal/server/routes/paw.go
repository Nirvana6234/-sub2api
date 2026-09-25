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
	// AutoGroup 只为线上协议兼容而保留，永远不会被应用：这个端点只保存候选集，
	// 内部 key 的自动分组标志始终开着。为什么不提供关闭，见 pawSaveAutoGroupHandler。
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
	OpenAIResponses   gin.HandlerFunc
	GatewayResponses  gin.HandlerFunc
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

	paw.GET("/auto-group", pawGetAutoGroupHandler(deps.APIKeyService))
	paw.PUT("/auto-group", pawSaveAutoGroupHandler(deps.APIKeyService))

	paw.POST("/files", pawUploadHandler(attachmentService))
	paw.POST("/images/generations", pawImageGenerationHandler(imageService, deps))
	paw.POST("/images/edits", pawImageEditHandler(imageService, deps))
	paw.POST("/chat/completions", pawChatHandler(deps.ChatService, chatService, deps))
	paw.POST("/responses", pawResponsesHandler(deps.ChatService, chatService, deps))

	// Anthropic Messages：让 Claude Code 及基于它的编辑器插件不持 key 也能用账号，
	// 与 Codex 走 /paw/responses 同理。分组同样由 X-Paw-Group-Id 指名——请求体必须
	// 逐字保持 Anthropic 原样，带不了路由元数据。
	messagesChat := deps.ChatService
	if messagesChat == nil {
		messagesChat = chatService
	}
	paw.POST("/messages", pawMessagesHandler(messagesChat, deps, false))
	paw.POST("/messages/count_tokens", pawMessagesHandler(messagesChat, deps, true))

	// TypeSafe Jev 意图判断（小白端「探索」页签）。分组同样由 X-Paw-Group-Id 指名，
	// 请求体逐字是 TypeSafe 的 {state, model, questions}。/paw 没有挂分组模型白名单
	// 中间件，这里在换上分组 key 之后单独挂一次。
	paw.POST("/systemone", pawSystemOnePrepareHandler(messagesChat), middleware.GroupModelAllowlist(), pawSystemOneDispatchHandler(deps))
}

// pawSystemOnePrepareHandler 校验登录态和 X-Paw-Group-Id 指名的 typesafe 分组，
// 把带上该分组的内部 key 副本放进上下文。不走自动分组。
func pawSystemOnePrepareHandler(chat *service.PawChatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "Paw accepts only the authenticated account session")
			return
		}
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			pawChatError(c, http.StatusUnauthorized, PawErrorCodeAuthRequired, "authenticated user is required")
			return
		}
		if chat == nil {
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeConfigUnavailable, "Paw System One gateway is unavailable")
			return
		}
		groupID, err := strconv.ParseInt(strings.TrimSpace(c.GetHeader(PawGroupHeader)), 10, 64)
		if err != nil || groupID <= 0 {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "a valid Paw group is required")
			return
		}
		resolution, err := chat.PrepareMessages(c.Request.Context(), subject.UserID, groupID)
		if err != nil {
			pawChatServiceError(c, err)
			return
		}
		if resolution.Group.Platform != service.PlatformTypeSafe {
			pawChatError(c, http.StatusForbidden, "GROUP_FORBIDDEN", "selected group does not serve System One")
			return
		}
		middleware.ReplaceAuthenticatedAPIKey(c, resolution.APIKey, resolution.Subscription)
	}
}

func pawSystemOneDispatchHandler(deps PawRouteDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Gateway == nil {
			pawChatError(c, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable, "Paw System One gateway is unavailable")
			return
		}
		deps.Gateway.SystemOne(c)
	}
}

// pawRejectTypeSafeGroup 让 typesafe 分组只能走 /paw/systemone：它的账号只说
// TypeSafe 协议，聊天和 Responses 请求落过去只会被上游拒掉，还白占一次选号。
func pawRejectTypeSafeGroup(group *service.Group) bool {
	return group != nil && group.Platform == service.PlatformTypeSafe
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
		// 这把内部 key 的自动分组标志终身开着，所以 req.AutoGroup 被刻意忽略。
		// 桌面端跑在哪种模式，由它每次请求发的 X-Paw-Group-Id 决定，不由这个标志
		// 决定——这里根本没有什么可关的。
		//
		// 写 false 会打断两件从这个 handler 里看不见的事：hydrateAutoGroupIDs 在
		// !AutoGroup 时直接短路，候选集就读不出来了，客户端的对话框下次打开是空的；
		// 而网页版 Playground 的 isPlaygroundEligibleKey 要求 auto_group || group_id > 0，
		// 这把 key 又刻意不带自己的 group_id。
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
		if pawRejectTypeSafeGroup(resolution.Group) {
			pawChatError(c, http.StatusForbidden, "GROUP_FORBIDDEN", "selected group only serves System One")
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
// # 为什么不能并进 `/paw/chat/completions`
//
// 这个问题被问过一次，答案是一个**硬阻塞**：那条路带不了工具。
// `PawChatRequest` 没有 tools 字段，`PawChatMessage.Content` 是纯字符串（无
// tool_calls / tool_call_id），而 `Prepare` 拼给上游的 body 只有
// `{model, messages, stream, reasoning_effort}`。也就是说，**不管调用方怎么翻译，
// 工具定义都会在这一层被丢掉** —— codex 拿到一个一个工具都没有的模型，
// 永远调不出 `exec_command`，agent 只能聊天、不能做事。
//
// （顺带澄清一个容易误导的说法：Responses↔ChatCompletions 的互转**本仓库已经有**
// （`internal/pkg/apicompat/chatcompletions_responses_bridge.go`），不需要重写。
// 阻塞不在翻译，在 Paw 请求体本身装不下工具。）
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

		// 分组头缺省（不发、空串、"0"、"auto"）= 交给自动分组解析。
		//
		// 桌面端开了自动分组之后本来就不知道该填哪个组，而 Responses 的载荷必须
		// 逐字节保持 Codex 原样、带不了路由元数据，客户端只能用这个头表达分组。
		// 缺省一律 400 的话，自动分组的客户端每一轮都被挡在门外。
		//
		// 写错的值仍然是错：负数和非数字照旧 400，绝不静默回退到某个默认组——
		// 那等于替用户猜，也会把「这个分组 ID 有效」泄露出去。
		groupHeader := strings.TrimSpace(c.GetHeader(PawGroupHeader))
		groupID := int64(0)
		var err error
		if groupHeader != "" && !strings.EqualFold(groupHeader, "auto") {
			groupID, err = strconv.ParseInt(groupHeader, 10, 64)
		}
		if err != nil || groupID < 0 {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", PawGroupHeader+" header is invalid")
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
		if pawRejectTypeSafeGroup(resolution.Group) {
			pawChatError(c, http.StatusForbidden, "GROUP_FORBIDDEN", "selected group only serves System One")
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
		if pawRejectTypeSafeGroup(resolution.Group) {
			pawMessagesError(c, http.StatusForbidden, "permission_error", "selected group only serves System One")
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
