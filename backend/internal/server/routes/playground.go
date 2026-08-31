package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type playgroundGatewayDispatch struct {
	models            gin.HandlerFunc
	chatCompletions   gin.HandlerFunc
	imagesGenerations gin.HandlerFunc
	videoGeneration   gin.HandlerFunc
	videoEdit         gin.HandlerFunc
	videoStatus       gin.HandlerFunc
	videoContent      gin.HandlerFunc
	voiceTTS          gin.HandlerFunc
	voiceSTT          gin.HandlerFunc
	historyGet        gin.HandlerFunc
	historySave       gin.HandlerFunc
}

// RegisterPlaygroundRoutes registers authenticated panel gateway adapters.
func RegisterPlaygroundRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	jwtAuth middleware.JWTAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	compositeResolver *service.CompositeRouteResolver,
	cfg *config.Config,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	if v1 == nil || h == nil {
		return
	}

	isOpenAIResponsesCompatibleGatewayPlatform := func(c *gin.Context) bool {
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI, service.PlatformGrok:
			return true
		default:
			return false
		}
	}
	isOpenAIGatewayPlatform := func(c *gin.Context) bool {
		return getGroupPlatform(c) == service.PlatformOpenAI
	}
	modelsHandler := func(c *gin.Context) {
		if isOpenAIGatewayPlatform(c) && c.Query("client_version") != "" {
			h.OpenAIGateway.CodexModels(c)
			return
		}
		h.Gateway.Models(c)
	}
	chatCompletionsHandler := func(c *gin.Context) {
		if isOpenAIResponsesCompatibleGatewayPlatform(c) {
			h.OpenAIGateway.ChatCompletions(c)
			return
		}
		h.Gateway.ChatCompletions(c)
	}
	imagesGenerationsHandler := func(c *gin.Context) {
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI:
			h.OpenAIGateway.Images(c)
		case service.PlatformGrok:
			h.OpenAIGateway.GrokImages(c)
		default:
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(404, gin.H{"error": gin.H{
				"type":    "not_found_error",
				"message": "Images API is not supported for this platform",
			}})
		}
	}
	videoGenerationHandler := func(c *gin.Context) {
		if platform := getGroupPlatform(c); platform == service.PlatformGrok || platform == service.PlatformComposite {
			h.OpenAIGateway.GrokVideoGeneration(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(404, gin.H{"error": gin.H{"type": "not_found_error", "message": "Videos API is not supported for this platform"}})
	}
	videoEditHandler := func(c *gin.Context) {
		if platform := getGroupPlatform(c); platform == service.PlatformGrok || platform == service.PlatformComposite {
			h.OpenAIGateway.GrokVideoEdit(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(404, gin.H{"error": gin.H{"type": "not_found_error", "message": "Videos API is not supported for this platform"}})
	}
	videoStatusHandler := func(c *gin.Context) {
		if platform := getGroupPlatform(c); platform == service.PlatformGrok || platform == service.PlatformComposite {
			h.OpenAIGateway.GrokVideoStatus(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(404, gin.H{"error": gin.H{"type": "not_found_error", "message": "Videos API is not supported for this platform"}})
	}
	videoContentHandler := func(c *gin.Context) {
		if platform := getGroupPlatform(c); platform == service.PlatformGrok || platform == service.PlatformComposite {
			h.OpenAIGateway.GrokVideoContent(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(404, gin.H{"error": gin.H{"type": "not_found_error", "message": "Videos API is not supported for this platform"}})
	}
	voiceTTSHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok {
			h.OpenAIGateway.GrokVoice(c, "tts")
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(404, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
	}
	voiceSTTHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok {
			h.OpenAIGateway.GrokVoice(c, "stt")
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(404, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
	}
	var historyGet, historySave gin.HandlerFunc
	if h.PlaygroundHistory != nil {
		historyGet = h.PlaygroundHistory.Get
		historySave = h.PlaygroundHistory.Save
	}

	registerPlaygroundRoutes(
		v1,
		playgroundGatewayDispatch{
			models:            modelsHandler,
			chatCompletions:   chatCompletionsHandler,
			imagesGenerations: imagesGenerationsHandler,
			videoGeneration:   videoGenerationHandler,
			videoEdit:         videoEditHandler,
			videoStatus:       videoStatusHandler,
			videoContent:      videoContentHandler,
			voiceTTS:          voiceTTSHandler,
			voiceSTT:          voiceSTTHandler,
			historyGet:        historyGet,
			historySave:       historySave,
		},
		jwtAuth,
		apiKeyService,
		subscriptionService,
		opsService,
		settingService,
		compositeResolver,
		cfg,
		panelRateLimiter,
	)
}

func registerPlaygroundRoutes(
	v1 *gin.RouterGroup,
	dispatch playgroundGatewayDispatch,
	jwtAuth middleware.JWTAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	compositeResolver *service.CompositeRouteResolver,
	cfg *config.Config,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	if v1 == nil || dispatch.models == nil || dispatch.chatCompletions == nil || dispatch.imagesGenerations == nil {
		return
	}
	if cfg == nil {
		cfg = &config.Config{}
	}

	bodyLimit := middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize)
	historyBodyLimit := middleware.RequestBodyLimit(service.PlaygroundHistoryMaxBytes)
	clientRequestID := middleware.ClientRequestID()
	opsErrorLogger := handler.OpsErrorLoggerMiddleware(opsService)
	endpointNorm := handler.InboundEndpointMiddleware()
	compositeTarget := compositeTargetPlatformMiddleware(compositeResolver)
	autoGroupModelRouting := autoGroupModelRoutingMiddleware(apiKeyService, subscriptionService)
	requireGroupAnthropic := middleware.RequireGroupAssignment(settingService, middleware.AnthropicErrorWriter)

	playground := v1.Group("/playground")
	playground.Use(bodyLimit)
	playground.Use(clientRequestID)
	playground.Use(opsErrorLogger)
	playground.Use(endpointNorm)
	playground.Use(middleware.PlaygroundRequestContext)
	playground.Use(gin.HandlerFunc(jwtAuth))
	playground.Use(middleware.BackendModeUserGuard(settingService))
	playground.Use(panelRateLimiter.Global())
	playground.Use(middleware.PlaygroundFeatureGate(settingService))
	{
		if dispatch.historyGet != nil && dispatch.historySave != nil {
			playground.GET("/history", dispatch.historyGet)
			playground.PUT("/history", historyBodyLimit, middleware.PlaygroundCredentialBodyGuard, dispatch.historySave)
		}
	}

	gateway := playground.Group("")
	gateway.Use(gin.HandlerFunc(middleware.PlaygroundSelectedAPIKeyAuth(apiKeyService, subscriptionService, cfg)))
	gateway.Use(autoGroupModelRouting)
	{
		gateway.GET("/models", requireGroupAnthropic, dispatch.models)
		gateway.POST("/chat/completions", middleware.PlaygroundCredentialBodyGuard, compositeTarget, requireGroupAnthropic, dispatch.chatCompletions)
		gateway.POST("/images/generations", middleware.PlaygroundCredentialBodyGuard, compositeTarget, requireGroupAnthropic, dispatch.imagesGenerations)
		gateway.POST("/images/edits", middleware.PlaygroundCredentialBodyGuard, compositeTarget, requireGroupAnthropic, dispatch.imagesGenerations)
		if dispatch.videoGeneration != nil {
			gateway.POST("/videos/generations", middleware.PlaygroundCredentialBodyGuard, compositeTarget, requireGroupAnthropic, dispatch.videoGeneration)
		}
		if dispatch.videoEdit != nil {
			gateway.POST("/videos/edits", middleware.PlaygroundCredentialBodyGuard, compositeTarget, requireGroupAnthropic, dispatch.videoEdit)
		}
		if dispatch.videoStatus != nil && dispatch.videoContent != nil {
			gateway.GET("/videos/:request_id", requireGroupAnthropic, dispatch.videoStatus)
			gateway.GET("/videos/:request_id/content", requireGroupAnthropic, dispatch.videoContent)
		}
		if dispatch.voiceTTS != nil {
			gateway.POST("/audio/speech", middleware.PlaygroundCredentialBodyGuard, compositeTarget, requireGroupAnthropic, dispatch.voiceTTS)
		}
		if dispatch.voiceSTT != nil {
			gateway.POST("/audio/transcriptions", middleware.PlaygroundCredentialBodyGuard, compositeTarget, requireGroupAnthropic, dispatch.voiceSTT)
		}
	}
}
