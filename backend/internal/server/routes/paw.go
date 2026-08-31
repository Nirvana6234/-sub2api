package routes

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type pawDefaultsRequest struct {
	GroupID   int64  `json:"group_id"`
	ModelID   string `json:"model_id"`
	Reasoning string `json:"reasoning"`
}

func RegisterPawRoutes(v1 *gin.RouterGroup, svc *service.PawConfigService, jwtAuth middleware.JWTAuthMiddleware, settingService *service.SettingService, panelRateLimiter *middleware.PanelRateLimiter) {
	paw := v1.Group("/paw")
	paw.Use(gin.HandlerFunc(jwtAuth))
	paw.Use(middleware.BackendModeUserGuard(settingService))
	paw.Use(panelRateLimiter.Global())
	paw.GET("/config", func(c *gin.Context) {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			return
		}
		config, err := svc.GetConfig(c.Request.Context(), subject.UserID)
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
