package relaynotify

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// OpsAlerts 把通知事件记进现有运维告警（service.OpsService）：后台的告警列表里能看到、确认、恢复。
type OpsAlerts struct{ Ops *service.OpsService }

// CreateAlert 实现 AlertSink。规则 ID 为空（不属于任何阈值规则），在运维告警列表里按事件显示。
func (a OpsAlerts) CreateAlert(ctx context.Context, severity, title, description string, dimensions map[string]any) (int64, error) {
	if a.Ops == nil {
		return 0, nil
	}
	ev, err := a.Ops.CreateAlertEvent(ctx, &service.OpsAlertEvent{
		Severity: severity, Status: service.OpsAlertStatusFiring, Title: title, Description: description, Dimensions: dimensions, FiredAt: time.Now(),
	})
	if err != nil || ev == nil {
		return 0, err
	}
	return ev.ID, nil
}

// ResolveAlert 实现 AlertSink。
func (a OpsAlerts) ResolveAlert(ctx context.Context, id int64) error {
	if a.Ops == nil || id <= 0 {
		return nil
	}
	now := time.Now()
	return a.Ops.UpdateAlertEventStatus(ctx, id, service.OpsAlertStatusResolved, &now)
}

// OpsEmail 用运维告警的邮件配置（收件人）和邮件服务发信。
type OpsEmail struct {
	Ops   *service.OpsService
	Email *service.EmailService
}

// Recipients 实现 EmailSender：运维告警邮件配置里的收件人（没打开告警邮件时没有）。
func (e OpsEmail) Recipients(ctx context.Context) []string {
	if e.Ops == nil {
		return nil
	}
	cfg, err := e.Ops.GetEmailNotificationConfig(ctx)
	if err != nil || cfg == nil || !cfg.Alert.Enabled {
		return nil
	}
	return cfg.Alert.Recipients
}

// Send 实现 EmailSender。
func (e OpsEmail) Send(ctx context.Context, to, subject, body string) error {
	if e.Email == nil {
		return nil
	}
	return e.Email.SendEmail(ctx, to, subject, body)
}
