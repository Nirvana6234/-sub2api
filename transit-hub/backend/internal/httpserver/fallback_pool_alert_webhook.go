package httpserver

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"transithub/backend/internal/modules/settings"
	"transithub/backend/internal/modules/upstream"
)

type fallbackPoolAlertWebhook struct {
	RequestID        string    `json:"request_id"`
	SiteBaseURL      string    `json:"site_base_url"`
	WorkspaceUserID  string    `json:"workspace_user_id"`
	WorkspaceAdminID string    `json:"workspace_admin_account_id"`
	AccountID        int64     `json:"account_id"`
	AccountName      string    `json:"account_name"`
	Model            string    `json:"model"`
	SourceGroupID    int64     `json:"source_group_id"`
	SourceGroupName  string    `json:"source_group_name"`
	TargetGroupID    int64     `json:"target_group_id"`
	TargetGroupName  string    `json:"target_group_name"`
	ActualCost       float64   `json:"actual_cost"`
	CreatedAt        time.Time `json:"created_at"`
}

func registerFallbackPoolAlertWebhook(mux *http.ServeMux, sites *upstream.Service, settingsService *settings.Service, secret string) {
	if mux == nil || sites == nil || settingsService == nil || strings.TrimSpace(secret) == "" {
		return
	}
	mux.HandleFunc("POST /api/internal/fallback-pool-alert", func(w http.ResponseWriter, r *http.Request) {
		if !constantTimeHeaderMatch(r.Header.Get("X-Sub2API-Fallback-Alert-Secret"), secret) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var event fallbackPoolAlertWebhook
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		if err := decoder.Decode(&event); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if event.SourceGroupID <= 0 || event.TargetGroupID <= 0 ||
			(strings.TrimSpace(event.SiteBaseURL) == "" &&
				(strings.TrimSpace(event.WorkspaceUserID) == "" || strings.TrimSpace(event.WorkspaceAdminID) == "")) {
			http.Error(w, "invalid fallback event", http.StatusBadRequest)
			return
		}

		site, err := sites.FindSiteByBaseURL(r.Context(), event.SiteBaseURL)
		if err != nil || site == nil {
			if strings.TrimSpace(event.WorkspaceUserID) == "" || strings.TrimSpace(event.WorkspaceAdminID) == "" {
				if err != nil {
					log.Printf("[fallback-alert] site lookup failed site_base_url=%s err=%v", strings.TrimSpace(event.SiteBaseURL), err)
				} else {
					log.Printf("[fallback-alert] site not found site_base_url=%s", strings.TrimSpace(event.SiteBaseURL))
				}
				http.Error(w, "site not found", http.StatusNotFound)
				return
			}
			log.Printf("[fallback-alert] site not found, using configured workspace site_base_url=%s user_id=%s admin_account_id=%s",
				strings.TrimSpace(event.SiteBaseURL), strings.TrimSpace(event.WorkspaceUserID), strings.TrimSpace(event.WorkspaceAdminID))
		}
		userID := strings.TrimSpace(event.WorkspaceUserID)
		adminAccountID := strings.TrimSpace(event.WorkspaceAdminID)
		siteID := "workspace:" + adminAccountID
		siteName := strings.TrimSpace(event.SiteBaseURL)
		if site != nil {
			userID = site.UserID
			adminAccountID = site.AdminAccountID
			siteID = site.ID
			siteName = site.Name
		}
		strategy, err := settingsService.GetStrategyForWorkspace(r.Context(), userID, adminAccountID)
		if err != nil {
			http.Error(w, "settings unavailable", http.StatusServiceUnavailable)
			return
		}
		if !strategy.EnableFallbackPoolAlert || len(strategy.FallbackPoolNotifyBotIDs) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		claimed, err := settingsService.ClaimFallbackPoolAlert(r.Context(), userID, adminAccountID, siteID,
			strconvFormatInt64(event.SourceGroupID), strconvFormatInt64(event.TargetGroupID), fallbackPoolAlertCooldown)
		if err != nil {
			http.Error(w, "cooldown unavailable", http.StatusServiceUnavailable)
			return
		}
		if claimed {
			usageEvent := upstream.FallbackPoolUsageEvent{
				RequestID:       event.RequestID,
				AccountID:       strconvFormatInt64(event.AccountID),
				AccountName:     event.AccountName,
				Model:           event.Model,
				SourceGroupID:   strconvFormatInt64(event.SourceGroupID),
				SourceGroupName: event.SourceGroupName,
				TargetGroupID:   strconvFormatInt64(event.TargetGroupID),
				TargetGroupName: event.TargetGroupName,
				ActualCost:      event.ActualCost,
				CreatedAt:       event.CreatedAt,
			}
			settingsService.SendFormattedToBotsForWorkspace(r.Context(), userID, adminAccountID,
				strategy.FallbackPoolNotifyBotIDs, formatFallbackPoolUsageAlert(siteName, usageEvent, fallbackPoolAlertCooldown, strategy.FallbackPoolTemplate),
				strategy.FallbackPoolTemplateFormat)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func constantTimeHeaderMatch(actual, expected string) bool {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
	if actual == "" || expected == "" {
		return false
	}
	if len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func strconvFormatInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
