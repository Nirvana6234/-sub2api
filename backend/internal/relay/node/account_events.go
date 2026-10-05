package node

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
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

	// grokMu、grokUsageAt：Grok 用量头事件的按账号节流。
	grokMu      sync.Mutex
	grokUsageAt map[int64]time.Time
}

var (
	_ service.OpenAIAccountReporter = (*RemoteAccountReporter)(nil)
	_ service.GrokAccountReporter   = (*RemoteAccountReporter)(nil)
)

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
// ModelRateLimit 报告 Antigravity 转发路径上设了模型级限流（本地写账号仓储），主节点照写。
func (r *RemoteAccountReporter) ModelRateLimit(accountID int64, modelKey string, resetAt time.Time) {
	if accountID <= 0 || modelKey == "" {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: accountID, Kind: &relayv1.AccountEvent_ModelRateLimit{
		ModelRateLimit: &relayv1.ModelRateLimitEvent{ModelKey: modelKey, ResetAtUnixMs: resetAt.UnixMilli()},
	}})
}

// RateLimited 报告 Antigravity 转发路径上设了账号级限流，主节点照写。
func (r *RemoteAccountReporter) RateLimited(accountID int64, resetAt time.Time) {
	if accountID <= 0 {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: accountID, Kind: &relayv1.AccountEvent_RateLimited{
		RateLimited: &relayv1.RateLimitedEvent{ResetAtUnixMs: resetAt.UnixMilli()},
	}})
}

// ReportGeminiCooldown 报告 Gemini 账号收到 429、上游没给重置时间（service.GeminiCooldownReporter）：冷却时长按档位定，
// 由主节点算并写账号级限流。
func (r *RemoteAccountReporter) ReportGeminiCooldown(accountID int64) {
	if accountID <= 0 {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: accountID, Kind: &relayv1.AccountEvent_GeminiCooldown{GeminiCooldown: &relayv1.GeminiCooldownEvent{}}})
}

// StickySessionCleared 报告转发路径上清掉了这个粘性会话绑定（accountID 是这次尝试用的账号），主节点只认这次请求自己的会话键。
func (r *RemoteAccountReporter) StickySessionCleared(accountID int64, sessionKey string) {
	if accountID <= 0 || sessionKey == "" {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: accountID, Kind: &relayv1.AccountEvent_StickyCleared{StickyCleared: &relayv1.StickySessionClearedEvent{SessionKey: sessionKey}}})
}

// ModelRateLimitsExtra 报告清除了积分耗尽标记后的整张模型级限流表，主节点写回账号 extra。
func (r *RemoteAccountReporter) ModelRateLimitsExtra(accountID int64, limitsJSON []byte) {
	if accountID <= 0 || len(limitsJSON) == 0 {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: accountID, Kind: &relayv1.AccountEvent_ModelRateLimitsExtra{
		ModelRateLimitsExtra: &relayv1.ModelRateLimitsExtraEvent{LimitsJson: limitsJSON},
	}})
}

// Internal500 报告 INTERNAL 500 渐进惩罚的一步（成功清零或重试耗尽），主节点计数并惩罚。
func (r *RemoteAccountReporter) Internal500(accountID int64, succeeded bool) {
	if accountID <= 0 {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: accountID, Kind: &relayv1.AccountEvent_Internal500{Internal500: &relayv1.Internal500Event{Succeeded: succeeded}}})
}

// MaskedSession 报告转发时用了这个伪装会话 ID（主节点写入并续期）。
func (r *RemoteAccountReporter) MaskedSession(accountID int64, sessionID string) {
	if accountID <= 0 || sessionID == "" {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: accountID, Kind: &relayv1.AccountEvent_MaskedSession{MaskedSession: &relayv1.MaskedSessionEvent{SessionId: sessionID}}})
}

// TempUnschedulable 让账号临时不可调度（非 OpenAI 网关转发路径上直接写账号仓储的那几处），主节点照写。
func (r *RemoteAccountReporter) TempUnschedulable(accountID int64, until time.Time, reason string) {
	if accountID <= 0 {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: accountID, Kind: &relayv1.AccountEvent_TempUnschedulable{
		TempUnschedulable: &relayv1.TempUnschedulableEvent{UntilUnixMs: until.UnixMilli(), Reason: reason},
	}})
}

// ---- Grok 账号状态（service.GrokAccountReporter）----

// grokUsageEventInterval：非关键的用量头事件，每个账号最多这个间隔发一次（本地同样按账号节流写快照）。
const grokUsageEventInterval = 2 * time.Second

// UpstreamError 上报 Grok 账号的一次上游错误响应，主节点用同一段代码判定冷却、限流、额度快照。
func (r *RemoteAccountReporter) UpstreamError(_ context.Context, account *service.Account, statusCode int, headers http.Header, body []byte, model string) {
	if account == nil {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: account.ID, Kind: &relayv1.AccountEvent_GrokUpstreamError{GrokUpstreamError: &relayv1.GrokUpstreamErrorEvent{
		StatusCode: int32(statusCode), Headers: headersToProto(headers), Body: capBody(body), Model: model,
	}}})
}

// UsageResponse 上报 Grok 账号一次响应的用量头；非关键的按账号节流。
func (r *RemoteAccountReporter) UsageResponse(_ context.Context, account *service.Account, headers http.Header, statusCode int, model string, critical bool) {
	if account == nil {
		return
	}
	if !critical {
		now := time.Now()
		r.grokMu.Lock()
		if last, ok := r.grokUsageAt[account.ID]; ok && now.Sub(last) < grokUsageEventInterval {
			r.grokMu.Unlock()
			return
		}
		if r.grokUsageAt == nil {
			r.grokUsageAt = map[int64]time.Time{}
		}
		r.grokUsageAt[account.ID] = now
		r.grokMu.Unlock()
	}
	r.send(&relayv1.AccountEvent{AccountId: account.ID, Kind: &relayv1.AccountEvent_GrokUsageResponse{GrokUsageResponse: &relayv1.GrokUsageResponseEvent{
		StatusCode: int32(statusCode), Headers: grokQuotaHeadersToProto(headers), Model: model,
	}}})
}

// TempUnschedule 上报 Grok 账号临时不可调度（流空闲）。
func (r *RemoteAccountReporter) TempUnschedule(_ context.Context, account *service.Account, cooldown time.Duration, reason string) {
	if account == nil || cooldown <= 0 {
		return
	}
	r.send(&relayv1.AccountEvent{AccountId: account.ID, Kind: &relayv1.AccountEvent_GrokTempUnschedule{GrokTempUnschedule: &relayv1.GrokTempUnscheduleEvent{
		CooldownMs: cooldown.Milliseconds(), Reason: reason,
	}}})
}

// grokQuotaHeadersToProto 只带额度快照要读的头（限流窗口、重试间隔、订阅档位）。
func grokQuotaHeadersToProto(h http.Header) []*relayv1.HeaderValues {
	out := make([]*relayv1.HeaderValues, 0, 8)
	for name, values := range h {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-ratelimit") || strings.HasPrefix(lower, "x-rate-limit") || strings.HasPrefix(lower, "x-xai") ||
			strings.HasPrefix(lower, "x-subscription") || lower == "retry-after" {
			out = append(out, &relayv1.HeaderValues{Name: name, Values: values})
		}
	}
	return out
}

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
