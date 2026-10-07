package handler

import (
	"net/http"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const systemOneOnlyPlatformMessage = "TypeSafe models are only available through POST /v1/systemone"

// rejectSystemOneOnlyPlatform stops TypeSafe traffic from entering a non System
// One protocol chain. TypeSafe accounts only speak the native System One
// protocol; letting them reach the Anthropic/OpenAI converters would send
// foreign payloads (and the account key) to the wrong upstream path and feed
// the resulting auth failures back into account state.
func rejectSystemOneOnlyPlatform(c *gin.Context, apiKey *service.APIKey, writeError func(*gin.Context, int, string, string)) bool {
	if _, forced := middleware2.GetForcePlatformFromContext(c); forced {
		return false
	}
	if effectiveAPIKeyPlatform(c, apiKey) != service.PlatformTypeSafe {
		return false
	}
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
	writeError(c, http.StatusNotFound, "not_found_error", systemOneOnlyPlatformMessage)
	return true
}
