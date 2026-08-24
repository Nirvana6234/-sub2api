package routes

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// RegisterCommonRoutes 注册通用路由（健康检查、状态等）
func RegisterCommonRoutes(r *gin.Engine, settingService *service.SettingService) {
	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Claude Code 遥测日志（忽略，直接返回200）
	r.POST("/api/event_logging/batch", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Setup status endpoint (always returns needs_setup: false in normal mode)
	// This is used by the frontend to detect when the service has restarted after setup
	r.GET("/setup/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"needs_setup": false,
				"step":        "completed",
			},
		})
	})

	// The configured client URL is external. Redirect to it without proxying or
	// storing the artifact on this server; the file host controls the download
	// response and filename.
	r.GET("/api/v1/download/client", clientDownloadHandler(settingService))
}

func clientDownloadHandler(settings *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settings == nil {
			c.Status(http.StatusNotFound)
			return
		}

		publicSettings, err := settings.GetPublicSettings(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"message": "download settings unavailable"})
			return
		}
		target, err := parseClientDownloadURL(publicSettings.ClientDownloadDirectURL)
		if err != nil || !publicSettings.ClientDownloadEnabled {
			c.Status(http.StatusNotFound)
			return
		}

		http.Redirect(c.Writer, c.Request, target.String(), http.StatusFound)
	}
}

func parseClientDownloadURL(raw string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("invalid client download URL")
	}
	return target, nil
}
