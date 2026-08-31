package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type fallbackPoolAlertEvent struct {
	RequestID        string    `json:"request_id"`
	SiteBaseURL      string    `json:"site_base_url"`
	WorkspaceUserID  string    `json:"workspace_user_id,omitempty"`
	WorkspaceAdminID string    `json:"workspace_admin_account_id,omitempty"`
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

func notifyFallbackPoolUsage(ctx context.Context, cfg *config.Config, usageLog *UsageLog, account *Account) {
	if usageLog == nil || !usageLog.FallbackPoolUsed {
		return
	}

	event := fallbackPoolAlertEvent{
		RequestID:   usageLog.RequestID,
		AccountName: accountNameForFallbackAlert(account),
		Model:       usageLog.Model,
		ActualCost:  usageLog.ActualCost,
		CreatedAt:   usageLog.CreatedAt,
	}
	if account != nil {
		event.AccountID = account.ID
	}
	event.SiteBaseURL = fallbackPoolAlertSiteBaseURL(cfg)
	event.WorkspaceUserID = strings.TrimSpace(cfg.FallbackPoolAlert.WorkspaceUserID)
	event.WorkspaceAdminID = strings.TrimSpace(cfg.FallbackPoolAlert.WorkspaceAdminID)
	event.SourceGroupID, event.SourceGroupName = fallbackPoolAlertGroup(
		usageLog.FallbackSourceGroupID,
		usageLog.FallbackSourceGroupName,
	)
	event.TargetGroupID, event.TargetGroupName = fallbackPoolAlertGroup(
		usageLog.FallbackTargetGroupID,
		usageLog.FallbackTargetGroupName,
	)
	notifyFallbackPoolAlertEvent(cfg, event)
}

func notifyFallbackPoolAlertEvent(cfg *config.Config, event fallbackPoolAlertEvent) {
	if cfg == nil {
		log.Printf("[fallback-alert] 未发送兜底事件：通知配置为空 request_id=%s", event.RequestID)
		return
	}
	alertCfg := cfg.FallbackPoolAlert
	endpoint := strings.TrimRight(strings.TrimSpace(alertCfg.URL), "/")
	secret := strings.TrimSpace(alertCfg.Secret)
	siteBaseURL := strings.TrimSpace(alertCfg.SiteBaseURL)
	if endpoint == "" || secret == "" || siteBaseURL == "" {
		log.Printf("[fallback-alert] 未发送兜底事件：回调配置不完整 request_id=%s endpoint_set=%t secret_set=%t site_base_url_set=%t",
			event.RequestID, endpoint != "", secret != "", siteBaseURL != "")
		return
	}

	if event.SourceGroupID <= 0 || event.TargetGroupID <= 0 {
		return
	}
	event.SiteBaseURL = siteBaseURL
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	timeout := time.Duration(alertCfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	body, err := json.Marshal(event)
	if err != nil {
		log.Printf("[fallback-alert] 编码兜底事件失败 request_id=%s err=%v", event.RequestID, err)
		return
	}

	go func() {
		notifyCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(notifyCtx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			log.Printf("[fallback-alert] 创建回调请求失败 request_id=%s err=%v", event.RequestID, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Sub2API-Fallback-Alert-Secret", secret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("[fallback-alert] 回调 TransitHub 失败 request_id=%s source_group=%d target_group=%d err=%v", event.RequestID, event.SourceGroupID, event.TargetGroupID, err)
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			log.Printf("[fallback-alert] TransitHub 回调返回非 2xx request_id=%s status=%d", event.RequestID, resp.StatusCode)
			return
		}
	}()
}

func fallbackPoolAlertSiteBaseURL(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.FallbackPoolAlert.SiteBaseURL)
}

func accountNameForFallbackAlert(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.Name)
}

func fallbackPoolAlertGroup(id *int64, name *string) (int64, string) {
	if id == nil || *id <= 0 {
		return 0, ""
	}
	if name == nil {
		return *id, ""
	}
	return *id, strings.TrimSpace(*name)
}
