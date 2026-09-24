package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// guestTrialBodyLimit 试用请求只有文字，64 KB 足够容纳上限内的对话（中文按 3 字节计也远低于此）。
const guestTrialBodyLimit = 64 << 10

// RegisterGuestTrialRoutes 未注册访客的网页版试用：只注册「状态 / 人机验证 / 文字聊天」三个接口，
// 生图、改图、上传等能力在路由层就不存在。
func RegisterGuestTrialRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	compositeResolver *service.CompositeRouteResolver,
	cfg *config.Config,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	if v1 == nil || h == nil || h.GuestTrial == nil || h.Gateway == nil || h.OpenAIGateway == nil {
		return
	}
	if cfg == nil {
		cfg = &config.Config{}
	}

	trial := v1.Group("/trial")
	trial.Use(middleware.RequestBodyLimit(guestTrialBodyLimit))
	trial.Use(panelRateLimiter.PublicIP())
	{
		trial.GET("/config", h.GuestTrial.Config)
		trial.POST("/verify", h.GuestTrial.Verify)
		trial.POST(
			"/chat/completions",
			middleware.ClientRequestID(),
			handler.OpsErrorLoggerMiddleware(opsService),
			handler.InboundEndpointMiddleware(),
			middleware.PlaygroundRequestContext,
			middleware.GuestTrialChatGate(h.GuestTrial.Service(), apiKeyService, subscriptionService, cfg),
			autoGroupModelRoutingMiddleware(apiKeyService, subscriptionService),
			compositeTargetPlatformMiddleware(compositeResolver),
			middleware.RequireGroupAssignment(settingService, middleware.AnthropicErrorWriter),
			newPanelChatCompletionsHandler(h),
		)
	}
}
