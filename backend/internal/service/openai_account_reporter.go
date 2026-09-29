package service

import (
	"context"
	"net/http"
	"strings"
)

// OpenAIAccountReporter 记录转发结果对账号状态的影响：调度统计与 API Key 健康熔断、换号计数、
// Codex 用量快照、传输错误的临时不可调度、Ollama Cloud 用量活动。调用方都不需要结果。
//
// 单机与主节点用本机实现（下面的 localOpenAIAccountReporter，就是原来的代码）；主从分流的从节点
// 换成经事件连接发给主节点的实现（设计 7.3"账号事件"，不等回复），主节点用同一段代码执行。
// 调度器、熔断计数、库里的账号状态都在主节点，这样两边的状态不会各记一份。
type OpenAIAccountReporter interface {
	// ReportScheduleResult 上报一次调度结果（原 ReportOpenAIAccountScheduleResultWithLatency）。
	ReportScheduleResult(account *Account, model string, success bool, firstTokenMs *int, servingGroupID int64, reasoningEffort string, observedErr error)
	// ObserveHealthFailure 记一次到不了调度结果的失败（原 ObserveOpenAIAccountHealthFailure）。
	ObserveHealthFailure(ctx context.Context, account *Account, observedErr error)
	// RecordAccountSwitch 记一次换号（调度器指标）。
	RecordAccountSwitch()
	// UpdateCodexUsageSnapshot 记下上游响应头里的 Codex 用量窗口。
	UpdateCodexUsageSnapshot(ctx context.Context, accountID int64, snapshot *OpenAICodexUsageSnapshot)
	// TempUnscheduleTransportError 持久的传输错误让账号临时不可调度。
	TempUnscheduleTransportError(ctx context.Context, account *Account, safeErr string)
	// OllamaCloudUsageActivity 记一次 Ollama Cloud 账号的用量活动。
	OllamaCloudUsageActivity(account *Account)
	// UpdateSessionWindow 按上游响应头（anthropic-ratelimit-unified-5h-*）更新账号的 5 小时会话窗口
	// （Anthropic 原生直通路径，原 rateLimitService.UpdateSessionWindow）。
	UpdateSessionWindow(ctx context.Context, account *Account, headers http.Header)
}

type openAIAccountReporterHolder struct{ r OpenAIAccountReporter }

// SetAccountReporter 换掉账号状态的上报（从节点装配用）；nil 恢复本机实现。
func (s *OpenAIGatewayService) SetAccountReporter(r OpenAIAccountReporter) {
	if r == nil {
		s.accountReporterOverride.Store(nil)
		return
	}
	s.accountReporterOverride.Store(&openAIAccountReporterHolder{r: r})
}

// LocalAccountReporter 返回本机实现（主节点处理从节点的账号事件时用）。
func (s *OpenAIGatewayService) LocalAccountReporter() OpenAIAccountReporter {
	return localOpenAIAccountReporter{s: s}
}

func (s *OpenAIGatewayService) accountReporter() OpenAIAccountReporter {
	if s != nil {
		if h := s.accountReporterOverride.Load(); h != nil {
			return h.r
		}
	}
	return localOpenAIAccountReporter{s: s}
}

type localOpenAIAccountReporter struct{ s *OpenAIGatewayService }

func (r localOpenAIAccountReporter) ReportScheduleResult(account *Account, model string, success bool, firstTokenMs *int, servingGroupID int64, reasoningEffort string, observedErr error) {
	var errs []error
	if observedErr != nil {
		errs = []error{observedErr}
	}
	r.s.reportOpenAIAccountScheduleResultLocal(account, model, success, firstTokenMs, servingGroupID, reasoningEffort, errs...)
}

func (r localOpenAIAccountReporter) ObserveHealthFailure(ctx context.Context, account *Account, observedErr error) {
	r.s.observeOpenAIAccountHealthFailureLocal(ctx, account, observedErr)
}

func (r localOpenAIAccountReporter) RecordAccountSwitch() {
	r.s.recordOpenAIAccountSwitchLocal()
}

func (r localOpenAIAccountReporter) UpdateCodexUsageSnapshot(ctx context.Context, accountID int64, snapshot *OpenAICodexUsageSnapshot) {
	r.s.updateCodexUsageSnapshotLocal(ctx, accountID, snapshot)
}

func (r localOpenAIAccountReporter) TempUnscheduleTransportError(ctx context.Context, account *Account, safeErr string) {
	r.s.tempUnscheduleOpenAITransportErrorLocal(ctx, account, safeErr)
}

func (r localOpenAIAccountReporter) OllamaCloudUsageActivity(account *Account) {
	if r.s == nil {
		return
	}
	scheduleOllamaCloudUsageActivity(r.s.deferredService, account)
}

func (r localOpenAIAccountReporter) UpdateSessionWindow(ctx context.Context, account *Account, headers http.Header) {
	if r.s == nil || r.s.rateLimitService == nil {
		return
	}
	r.s.rateLimitService.UpdateSessionWindow(ctx, account, headers)
}

// SessionWindowHeaders 取出会话窗口要看的响应头（anthropic-ratelimit-unified-*），从节点只把这些随账号事件发给主节点。
func SessionWindowHeaders(headers http.Header) http.Header {
	out := http.Header{}
	for name, values := range headers {
		if strings.HasPrefix(strings.ToLower(name), "anthropic-ratelimit-unified-") {
			out[name] = append([]string(nil), values...)
		}
	}
	return out
}

// OpenAIHealthFailureFacts 把一次失败化成健康熔断要看的事实（与 ObserveOpenAIAPIKeyHealthFailure 的分类一致）：
// 不计入熔断的失败 eligible 为 false。从节点把它随账号事件发给主节点。
func OpenAIHealthFailureFacts(err error) (statusCode int, body []byte, eligible bool) {
	return classifyOpenAIAPIKeyHealthFailure(err)
}

// OpenAIHealthFailureError 由从节点发来的事实还原出一个分类结果相同的错误（主节点执行账号事件时用）；
// 不计入熔断的返回 nil。
func OpenAIHealthFailureError(statusCode int, body []byte, eligible bool) error {
	if !eligible {
		return nil
	}
	return &UpstreamFailoverError{StatusCode: statusCode, ResponseBody: body}
}
