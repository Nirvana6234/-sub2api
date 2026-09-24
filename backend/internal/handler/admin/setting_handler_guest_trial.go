package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetGuestTrialConfig 获取未注册访客网页版试用配置
// GET /api/v1/admin/settings/guest-trial
func (h *SettingHandler) GetGuestTrialConfig(c *gin.Context) {
	cfg, err := h.settingService.GetGuestTrialConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// UpdateGuestTrialConfig 更新未注册访客网页版试用配置
// PUT /api/v1/admin/settings/guest-trial
func (h *SettingHandler) UpdateGuestTrialConfig(c *gin.Context) {
	var cfg service.GuestTrialConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	saved, err := h.settingService.SaveGuestTrialConfig(c.Request.Context(), &cfg)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}
