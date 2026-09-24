package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestListContributionsReturnsWhitelistedGovernanceData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminSvc := newStubAdminService()
	now := time.Now().UTC()
	adminSvc.accounts = []service.Account{
		{
			ID:          91,
			Name:        "contributed-openai",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 2,
			Credentials: map[string]any{"access_token": "must-not-leak"},
			Extra: map[string]any{
				service.AccountContributionSourceKey:          service.AccountContributionSourceValue,
				service.AccountContributorUserIDKey:           float64(42),
				service.AccountContributorEmailKey:            "owner@example.com",
				service.AccountShareModeKey:                   service.AccountShareModePool,
				service.AccountShareTotalBudgetKey:            10.0,
				service.AccountShareDailyBudgetKey:            2.0,
				service.AccountShareExpiresAtKey:              now.Add(time.Hour).Format(time.RFC3339),
				service.AccountContributionGovernanceStateKey: service.AccountContributionGovernancePaused,
				"unrelated_secret":                            "must-not-leak",
			},
			CreatedAt: now,
		},
		{ID: 92, Name: "admin-account", Status: service.StatusActive, CreatedAt: now},
	}
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/api/v1/admin/contributions", handler.ListContributions)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/contributions?page=1&page_size=20", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "contributed-openai")
	require.Contains(t, body, "o***@example.com")
	require.NotContains(t, body, "must-not-leak")
	require.NotContains(t, body, "credentials")
	require.NotContains(t, body, "unrelated_secret")
}
