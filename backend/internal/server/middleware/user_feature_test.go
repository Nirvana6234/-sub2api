package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequireUserFeature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		feature    UserFeature
		subject    AuthSubject
		role       string
		wantStatus int
	}{
		{name: "disabled account management", feature: UserFeatureAccountManagement, subject: AuthSubject{UserID: 1}, role: "user", wantStatus: http.StatusForbidden},
		{name: "enabled account management", feature: UserFeatureAccountManagement, subject: AuthSubject{UserID: 1, AccountManagementEnabled: true}, role: "user", wantStatus: http.StatusOK},
		{name: "other feature remains disabled", feature: UserFeatureContributionRooms, subject: AuthSubject{UserID: 1, AccountManagementEnabled: true}, role: "user", wantStatus: http.StatusForbidden},
		{name: "administrator bypass", feature: UserFeatureContributionRooms, subject: AuthSubject{UserID: 1}, role: "admin", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyUser), tt.subject)
				c.Set(string(ContextKeyUserRole), tt.role)
				c.Next()
			})
			router.GET("/feature", RequireUserFeature(tt.feature), func(c *gin.Context) { c.Status(http.StatusOK) })

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/feature", nil))
			require.Equal(t, tt.wantStatus, response.Code)
			if tt.wantStatus == http.StatusForbidden {
				require.Contains(t, response.Body.String(), "FEATURE_NOT_ENABLED")
			}
		})
	}
}
