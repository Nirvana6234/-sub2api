package node

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// healthFailureBodyLimit 是随账号事件发送的上游响应体摘要上限（主节点只把它写进临时不可调度的原因）。
const healthFailureBodyLimit = 8 << 10

// RemoteAccountReporter 是从节点上的账号状态上报：把转发结果对账号状态的影响作为账号事件经事件连接发给
// 主节点（设计 7.3，不等回复），主节点用单机同一段代码执行。装在从节点的 OpenAIGatewayService 上
// （SetAccountReporter）。
type RemoteAccountReporter struct {
	outbox *EventOutbox
	now    func() time.Time
}

var _ service.OpenAIAccountReporter = (*RemoteAccountReporter)(nil)

// NewRemoteAccountReporter 创建远端上报，事件经 outbox 发送。
func NewRemoteAccountReporter(outbox *EventOutbox) *RemoteAccountReporter {
	return &RemoteAccountReporter{outbox: outbox, now: time.Now}
}

func (r *RemoteAccountReporter) send(ev *relayv1.AccountEvent) {
	r.outbox.Enqueue(&relayv1.NodeEnvelope{Body: &relayv1.NodeEnvelope_AccountEvent{AccountEvent: ev}})
}

func healthFacts(err error) *relayv1.HealthFailureFacts {
	if err == nil {
		return nil
	}
	status, body, eligible := service.OpenAIHealthFailureFacts(err)
	if !eligible {
		return &relayv1.HealthFailureFacts{}
	}
	if len(body) > healthFailureBodyLimit {
		body = body[:healthFailureBodyLimit]
	}
	return &relayv1.HealthFailureFacts{Eligible: true, StatusCode: int32(status), Body: body}
}

// ReportScheduleResult 见 service.OpenAIAccountReporter。
func (r *RemoteAccountReporter) ReportScheduleResult(account *service.Account, model string, success bool, firstTokenMs *int, servingGroupID int64, reasoningEffort string, observedErr error) {
	if account == nil {
		return
	}
	ev := &relayv1.ScheduleResultEvent{Model: model, Success: success, ServingGroupId: servingGroupID, ReasoningEffort: reasoningEffort, Failure: healthFacts(observedErr)}
	if firstTokenMs != nil {
		ev.HasFirstTokenMs, ev.FirstTokenMs = true, int32(*firstTokenMs)
	}
	r.send(&relayv1.AccountEvent{AccountId: account.ID, Kind: &relayv1.AccountEvent_ScheduleResult{ScheduleResult: ev}})
}

// ObserveHealthFailure 见 service.OpenAIAccountReporter。不计入熔断的失败不发。
func (r *RemoteAccountReporter) ObserveHealthFailure(_ context.Context, account *service.Account, observedErr error) {
	facts := healthFacts(observedErr)
	if account == nil || facts == nil || !facts.GetEligible() {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: account.ID, Kind: &relayv1.AccountEvent_HealthFailure{HealthFailure: &relayv1.HealthFailureEvent{Failure: facts}}})
}

// RecordAccountSwitch 见 service.OpenAIAccountReporter。
func (r *RemoteAccountReporter) RecordAccountSwitch() {
	r.send(&relayv1.AccountEvent{Kind: &relayv1.AccountEvent_AccountSwitch{AccountSwitch: &relayv1.AccountSwitchEvent{}}})
}

// UpdateCodexUsageSnapshot 见 service.OpenAIAccountReporter。窗口的重置时间按收到响应的时间算，
// 所以没带时间的快照补上本机当前时间，事件晚到不会让重置时间后移。
func (r *RemoteAccountReporter) UpdateCodexUsageSnapshot(_ context.Context, accountID int64, snapshot *service.OpenAICodexUsageSnapshot) {
	if snapshot == nil || accountID <= 0 {
		return
	}
	copied := *snapshot
	if copied.UpdatedAt == "" {
		copied.UpdatedAt = r.now().UTC().Format(time.RFC3339)
	}
	raw, err := json.Marshal(copied)
	if err != nil {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: accountID, Kind: &relayv1.AccountEvent_CodexUsage{CodexUsage: &relayv1.CodexUsageEvent{SnapshotJson: raw}}})
}

// TempUnscheduleTransportError 见 service.OpenAIAccountReporter。
func (r *RemoteAccountReporter) TempUnscheduleTransportError(_ context.Context, account *service.Account, safeErr string) {
	if account == nil {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: account.ID, Kind: &relayv1.AccountEvent_TransportError{TransportError: &relayv1.TransportErrorEvent{Message: safeErr}}})
}

// OllamaCloudUsageActivity 见 service.OpenAIAccountReporter。
// UpdateSessionWindow 见 service.OpenAIAccountReporter：没有会话窗口头时不发。
func (r *RemoteAccountReporter) UpdateSessionWindow(_ context.Context, account *service.Account, headers http.Header) {
	if account == nil {
		return
	}
	window := service.SessionWindowHeaders(headers)
	if len(window) == 0 {
		return
	}
	ev := &relayv1.SessionWindowEvent{}
	for name, values := range window {
		ev.Headers = append(ev.Headers, &relayv1.HeaderValues{Name: name, Values: values})
	}
	r.send(&relayv1.AccountEvent{AccountId: account.ID, Kind: &relayv1.AccountEvent_SessionWindow{SessionWindow: ev}})
}

func (r *RemoteAccountReporter) OllamaCloudUsageActivity(account *service.Account) {
	if account == nil || !service.IsOllamaCloudUsageAccount(account) {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: account.ID, Kind: &relayv1.AccountEvent_OllamaActivity{OllamaActivity: &relayv1.OllamaActivityEvent{}}})
}
