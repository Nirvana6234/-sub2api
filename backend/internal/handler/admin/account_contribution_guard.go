package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// RequireAdminManagedAccount prevents the normal administrator account routes
// from operating on user-contributed resources. Contribution governance will
// live behind its own routes and never exposes credentials through this path.
func (h *AccountHandler) RequireAdminManagedAccount() gin.HandlerFunc {
	return func(c *gin.Context) {
		rawID := strings.TrimSpace(c.Param("id"))
		if rawID == "" {
			c.Next()
			return
		}
		accountID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || accountID <= 0 {
			c.Next()
			return
		}
		if _, err := h.adminService.GetAccount(c.Request.Context(), accountID); err != nil {
			response.ErrorFrom(c, err)
			c.Abort()
			return
		}
		c.Next()
	}
}
