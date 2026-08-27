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
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type fallbackPoolAlertEvent struct {
	RequestID       string    `json:"request_id"`
	SiteBaseURL     string    `json:"site_base_url"`
	AccountID       int64     `json:"account_id"`
	AccountName     string    `json:"account_name"`
	Model           string    `json:"model"`
	SourceGroupID   int64     `json:"source_group_id"`
	SourceGroupName string    `json:"source_group_name"`
	TargetGroupID   int64     `json:"target_group_id"`
	TargetGroupName string    `json:"target_group_name"`
	ActualCost      float64   `json:"actual_cost"`
	CreatedAt       time.Time `json:"created_at"`
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

// notifyFallbackPoolSelection reports the routing decision immediately after
// A->B fallback traversal succeeds. It intentionally does not require a
// selected account or a usage log: both pools may be unavailable after the
// fallback group is activated.
func notifyFallbackPoolSelection(ctx context.Context, cfg *config.Config, trace fallbackPoolUsageTrace) {
	if trace.SourceGroupID <= 0 || trace.TargetGroupID <= 0 {
		return
	}
	event := fallbackPoolAlertEvent{
		RequestID:       fallbackAlertRequestID(ctx),
		SiteBaseURL:     fallbackPoolAlertSiteBaseURL(cfg),
		SourceGroupID:   trace.SourceGroupID,
		SourceGroupName: trace.SourceGroupName,
		TargetGroupID:   trace.TargetGroupID,
		TargetGroupName: trace.TargetGroupName,
		CreatedAt:       time.Now().UTC(),
	}
	notifyFallbackPoolAlertEvent(cfg, event)
}

func notifyFallbackPoolAlertEvent(cfg *config.Config, event fallbackPoolAlertEvent) {
	if cfg == nil {
		return
	}
	alertCfg := cfg.FallbackPoolAlert
	endpoint := strings.TrimRight(strings.TrimSpace(alertCfg.URL), "/")
	secret := strings.TrimSpace(alertCfg.Secret)
	siteBaseURL := strings.TrimSpace(alertCfg.SiteBaseURL)
	if endpoint == "" || secret == "" || siteBaseURL == "" {
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

func fallbackAlertRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if requestID, ok := ctx.Value(ctxkey.RequestID).(string); ok && strings.TrimSpace(requestID) != "" {
		return strings.TrimSpace(requestID)
	}
	if requestID, ok := ctx.Value(ctxkey.ClientRequestID).(string); ok {
		return strings.TrimSpace(requestID)
	}
	return ""
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
