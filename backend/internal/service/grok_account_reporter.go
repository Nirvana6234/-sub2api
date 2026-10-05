package service

import (
	"context"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

// GrokAccountReporter 是 Grok 转发路径上写账号状态的接缝：错误响应的冷却 / 限流 / 额度快照、成功响应的用量快照与限流恢复、
// 流空闲后的临时不可调度。单机直接写（本机仓储和调度器的运行时屏蔽）；从节点没有仓储，把事实作为账号事件发给主节点，
// 主节点用同一段代码执行（Relay* 方法）——运行时屏蔽、每队列的账号调度状态都在主节点（选号在那里）。
type GrokAccountReporter interface {
	// UpstreamError 一次上游错误响应（handleGrokAccountUpstreamError 的输入）；model 是请求的上游模型（团队 + 模型限流用）。
	UpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte, model string)
	// UsageResponse 一次响应的用量头（updateGrokUsageFromResponse 的输入）；critical 为 true 时不能丢（限流窗口耗尽、恢复）。
	UsageResponse(ctx context.Context, account *Account, headers http.Header, statusCode int, model string, critical bool)
	// TempUnschedule 让账号临时不可调度（流空闲等）。
	TempUnschedule(ctx context.Context, account *Account, cooldown time.Duration, reason string)
}

type grokAccountReporterHolder struct{ r GrokAccountReporter }

// SetGrokAccountReporter 换掉 Grok 账号状态的上报（从节点装配用）；nil 恢复本机实现。
func (s *OpenAIGatewayService) SetGrokAccountReporter(r GrokAccountReporter) {
	if r == nil {
		s.grokReporterOverride.Store(nil)
		return
	}
	s.grokReporterOverride.Store(&grokAccountReporterHolder{r: r})
}

func (s *OpenAIGatewayService) grokReporter() GrokAccountReporter {
	if s == nil {
		return nil
	}
	if h := s.grokReporterOverride.Load(); h != nil {
		return h.r
	}
	return nil
}

// handleGrokAccountUpstreamError 处理 Grok 账号的一次上游错误响应（本机直接写，或经上报交给主节点）。
func (s *OpenAIGatewayService) handleGrokAccountUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte) {
	if r := s.grokReporter(); r != nil && account != nil {
		if isGrokContentPolicyRejection(statusCode, responseBody) {
			return
		}
		r.UpstreamError(ctx, account, statusCode, headers, responseBody, grokRequestedModelFromCtx(ctx))
		return
	}
	s.handleGrokAccountUpstreamErrorLocal(ctx, account, statusCode, headers, responseBody)
}

// updateGrokUsageFromResponse 用响应头更新 Grok 账号的用量快照、清掉已恢复的限流（本机直接写，或经上报交给主节点）。
func (s *OpenAIGatewayService) updateGrokUsageFromResponse(ctx context.Context, account *Account, headers http.Header, statusCode int) {
	if r := s.grokReporter(); r != nil && account != nil {
		snapshot := parseGrokQuotaSnapshot(headers, statusCode, time.Now())
		recovery := isSuccessfulGrokRateLimitRecovery(account, &xai.QuotaSnapshot{StatusCode: statusCode})
		if snapshot == nil && !recovery {
			return
		}
		critical := recovery || statusCode == http.StatusTooManyRequests
		if snapshot != nil && !critical {
			_, critical = grokRateLimitResetAtForAccount(account, snapshot, time.Now())
		}
		r.UsageResponse(ctx, account, headers, statusCode, grokRequestedModelFromCtx(ctx), critical)
		return
	}
	s.updateGrokUsageFromResponseLocal(ctx, account, headers, statusCode)
}

// tempUnscheduleGrok 让 Grok 账号临时不可调度（本机直接写，或经上报交给主节点）。
func (s *OpenAIGatewayService) tempUnscheduleGrok(ctx context.Context, account *Account, cooldown time.Duration, reason string) {
	if r := s.grokReporter(); r != nil && account != nil {
		r.TempUnschedule(ctx, account, cooldown, reason)
		return
	}
	s.tempUnscheduleGrokLocal(ctx, account, cooldown, reason)
}

// RelayGrokUpstreamError 执行从节点上报的 Grok 上游错误响应（主节点）。
func (s *OpenAIGatewayService) RelayGrokUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte, model string) {
	s.handleGrokAccountUpstreamErrorLocal(withGrokTeamRateLimitModel(ctx, model), account, statusCode, headers, body)
}

// RelayGrokUsageResponse 执行从节点上报的 Grok 响应用量头（主节点）。
func (s *OpenAIGatewayService) RelayGrokUsageResponse(ctx context.Context, account *Account, headers http.Header, statusCode int, model string) {
	s.updateGrokUsageFromResponseLocal(withGrokTeamRateLimitModel(ctx, model), account, headers, statusCode)
}

// RelayGrokTempUnschedule 执行从节点上报的 Grok 临时不可调度（主节点）。
func (s *OpenAIGatewayService) RelayGrokTempUnschedule(ctx context.Context, account *Account, cooldown time.Duration, reason string) {
	s.tempUnscheduleGrokLocal(ctx, account, cooldown, reason)
}
