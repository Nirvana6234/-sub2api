package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// RegisterRemoteRoutes registers phone ↔ desktop session sync.
//
// Everything needs the account's session. Reaching a computer additionally
// needs the X-Remote-Pairing token of a pairing the user confirmed on it; see
// handler.RemoteHandler.
func RegisterRemoteRoutes(v1 *gin.RouterGroup, h *handler.RemoteHandler, jwtAuth middleware.JWTAuthMiddleware, settingService *service.SettingService, panelRateLimiter *middleware.PanelRateLimiter) {
	if v1 == nil || h == nil {
		return
	}

	remote := v1.Group("/remote")
	remote.Use(gin.HandlerFunc(jwtAuth))
	remote.Use(middleware.BackendModeUserGuard(settingService))

	// The two long-lived connections are not counted by the panel limiter: one
	// assistant socket and one stream per open conversation are expected to stay.
	remote.GET("/agent", h.Agent)
	remote.GET("/devices/:device_id/sessions/:thread_id/stream", h.Stream)

	limited := remote.Group("")
	limited.Use(panelRateLimiter.Global())
	limited.POST("/pair/start", h.StartPairing)
	limited.POST("/pair/claim", h.ClaimPairing)
	limited.GET("/pairings/:id", h.PairingStatus)
	limited.DELETE("/pairings/:id", h.RevokePairing)
	limited.GET("/devices", h.ListDevices)
	limited.POST("/devices/:device_id/cmd", h.Command)
}
