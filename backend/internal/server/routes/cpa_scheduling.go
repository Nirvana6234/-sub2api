package routes

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// RegisterCPASchedulingRoutes exposes exactly one read-only capability. Its
// credential is not accepted by the admin or inference middleware.
func RegisterCPASchedulingRoutes(v1 *gin.RouterGroup, h *handler.Handlers, cfg *config.Config) {
	var export gin.HandlerFunc = (*handler.OpenAIGatewayHandler)(nil).GetCPASchedulingProfile
	if h != nil && h.OpenAIGateway != nil {
		export = h.OpenAIGateway.GetCPASchedulingProfile
	}
	v1.GET("/internal/cpa/scheduling-profile", cpaSchedulingAuth(cfg), export)
}

func cpaSchedulingAuth(cfg *config.Config) gin.HandlerFunc {
	token := ""
	if cfg != nil {
		token = cfg.CPAScheduling.SyncToken
	}
	expected := sha256.Sum256([]byte(token))
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		headers := c.Request.Header.Values("Authorization")
		if token == "" || len(headers) != 1 {
			response.Unauthorized(c, "Invalid CPA scheduling credential")
			c.Abort()
			return
		}
		parts := strings.Fields(headers[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.Unauthorized(c, "Invalid CPA scheduling credential")
			c.Abort()
			return
		}
		provided := sha256.Sum256([]byte(parts[1]))
		if subtle.ConstantTimeCompare(provided[:], expected[:]) != 1 {
			response.Unauthorized(c, "Invalid CPA scheduling credential")
			c.Abort()
			return
		}
		c.Next()
	}
}
