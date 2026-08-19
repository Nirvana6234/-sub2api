package middleware

import (
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

func AdminComplianceGuard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService == nil ||
			isAdminComplianceBypassPath(c.Request.URL.Path) ||
			isLocalDesktopComplianceBypass(c) {
			c.Next()
			return
		}

		subject, ok := GetAuthSubjectFromContext(c)
		if !ok {
			AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}

		acknowledged, err := settingService.IsAdminComplianceAcknowledged(c.Request.Context(), subject.UserID)
		if err != nil {
			AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
			return
		}
		if acknowledged {
			c.Next()
			return
		}

		c.JSON(http.StatusLocked, gin.H{
			"code":    "ADMIN_COMPLIANCE_ACK_REQUIRED",
			"message": "administrator compliance acknowledgement is required",
			"metadata": gin.H{
				"version":          service.AdminComplianceVersion,
				"document_path_zh": service.AdminComplianceDocumentPathZH,
				"document_path_en": service.AdminComplianceDocumentPathEN,
				"document_url_zh":  service.AdminComplianceDocumentURLZH,
				"document_url_en":  service.AdminComplianceDocumentURLEN,
			},
		})
		c.Abort()
	}
}

func isLocalDesktopComplianceBypass(c *gin.Context) bool {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("LOCAL_DESKTOP_MODE")))
	if mode != "true" && mode != "1" && mode != "yes" {
		return false
	}

	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		host = c.Request.RemoteAddr
	}
	peer := net.ParseIP(strings.Trim(host, "[]"))
	return peer != nil && peer.IsLoopback()
}

func isAdminComplianceBypassPath(path string) bool {
	path = strings.TrimSpace(path)
	return path == "/api/v1/admin/compliance" || strings.HasPrefix(path, "/api/v1/admin/compliance/")
}
