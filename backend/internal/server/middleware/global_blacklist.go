package middleware

import (
	"net/http"

	appconfig "github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GlobalBlacklistIP rejects blacklisted client addresses before API-key
// authentication, so blocked IPs cannot probe the gateway with invalid keys.
func GlobalBlacklistIP(settings *service.SettingService, cfg *appconfig.Config) gin.HandlerFunc {
	return globalBlacklist(settings, cfg, false)
}

// GlobalBlacklistAccount performs the account check after API-key auth has
// populated the authenticated user context.
func GlobalBlacklistAccount(settings *service.SettingService, cfg *appconfig.Config) gin.HandlerFunc {
	return globalBlacklist(settings, cfg, true)
}

func globalBlacklist(settings *service.SettingService, cfg *appconfig.Config, checkAccount bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settings == nil {
			c.Next()
			return
		}
		trustForwarded := cfg != nil && cfg.TrustForwardedIPForAPIKeyACL()
		clientIP := ip.GetSecurityClientIP(c, trustForwarded)
		var userID int64
		if checkAccount {
			if apiKey, ok := GetAPIKeyFromContext(c); ok && apiKey != nil {
				userID = apiKey.UserID
				if apiKey.User != nil && apiKey.User.ID > 0 {
					userID = apiKey.User.ID
				}
			}
		}
		matched, entry, err := settings.IsGloballyBlacklisted(c.Request.Context(), userID, clientIP)
		if err != nil {
			// A blacklist read failure must fail closed for security-sensitive access.
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{"code": "BLACKLIST_UNAVAILABLE", "message": "Access control is temporarily unavailable"},
			})
			return
		}
		if matched {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"code": "BLACKLISTED", "message": "Access denied by administrator policy", "kind": entry.Kind},
			})
			return
		}
		c.Next()
	}
}
