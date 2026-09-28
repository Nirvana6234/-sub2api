package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// GetCPASchedulingProfile is mounted only behind the dedicated read-only CPA
// token middleware. It never accepts administrator or inference credentials.
func (h *OpenAIGatewayHandler) GetCPASchedulingProfile(c *gin.Context) {
	if h == nil || h.gatewayService == nil {
		response.Error(c, http.StatusServiceUnavailable, "CPA scheduling source is unavailable")
		return
	}
	profile, err := h.gatewayService.CPASchedulingProfile(c.Request.Context())
	if err != nil {
		// Do not expose DB errors, configuration, or account credentials.
		response.Error(c, http.StatusServiceUnavailable, "CPA scheduling source is unavailable")
		return
	}
	c.Header("ETag", `"`+profile.Revision+`"`)
	response.Success(c, profile)
}
