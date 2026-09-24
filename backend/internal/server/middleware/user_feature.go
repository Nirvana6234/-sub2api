package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// UserFeature identifies an optional self-service capability granted by an
// administrator. Features are intentionally evaluated from the authenticated
// user snapshot populated for this request, never from a JWT claim.
type UserFeature string

const (
	UserFeatureAccountManagement UserFeature = "account_management"
	UserFeatureContributionRooms UserFeature = "contribution_rooms"
)

// RequireUserFeature rejects direct API access when a user's optional feature
// is disabled. Administrators retain access for support and governance work.
func RequireUserFeature(feature UserFeature) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}

		role, _ := GetUserRoleFromContext(c)
		if role == "admin" {
			c.Next()
			return
		}

		allowed := false
		switch feature {
		case UserFeatureAccountManagement:
			allowed = subject.AccountManagementEnabled
		case UserFeatureContributionRooms:
			allowed = subject.ContributionRoomsEnabled
		}
		if !allowed {
			AbortWithError(c, http.StatusForbidden, "FEATURE_NOT_ENABLED", "This feature is not enabled for your account")
			return
		}

		c.Next()
	}
}
