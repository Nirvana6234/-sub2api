//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type countingReporter struct {
	OpenAIAccountReporter
	results, health, switches, codex, transport, ollama atomic.Int64
}

func (r *countingReporter) ReportScheduleResult(account *Account, model string, success bool, firstTokenMs *int, servingGroupID int64, reasoningEffort string, observedErr error) {
	r.results.Add(1)
	r.OpenAIAccountReporter.ReportScheduleResult(account, model, success, firstTokenMs, servingGroupID, reasoningEffort, observedErr)
}

func (r *countingReporter) ObserveHealthFailure(ctx context.Context, account *Account, observedErr error) {
	r.health.Add(1)
	r.OpenAIAccountReporter.ObserveHealthFailure(ctx, account, observedErr)
}

func (r *countingReporter) RecordAccountSwitch() {
	r.switches.Add(1)
	r.OpenAIAccountReporter.RecordAccountSwitch()
}

func (r *countingReporter) UpdateCodexUsageSnapshot(ctx context.Context, accountID int64, snapshot *OpenAICodexUsageSnapshot) {
	r.codex.Add(1)
	r.OpenAIAccountReporter.UpdateCodexUsageSnapshot(ctx, accountID, snapshot)
}

func (r *countingReporter) TempUnscheduleTransportError(ctx context.Context, account *Account, safeErr string) {
	r.transport.Add(1)
	r.OpenAIAccountReporter.TempUnscheduleTransportError(ctx, account, safeErr)
}

func (r *countingReporter) UpdateSessionWindow(ctx context.Context, account *Account, headers http.Header) {
	r.OpenAIAccountReporter.UpdateSessionWindow(ctx, account, headers)
}

func (r *countingReporter) OllamaCloudUsageActivity(account *Account) {
	r.ollama.Add(1)
	r.OpenAIAccountReporter.OllamaCloudUsageActivity(account)
}

// 主从分流：从节点的转发代码不变，账号状态的上报改由主节点用同一段代码执行；状态记在主节点上。
func TestRemoteAccountReportsLandOnTheMaster(t *testing.T) {
	ctx := context.Background()
	local, _ := newDeciderTestGateway()
	master, _ := newDeciderTestGateway()
	node := &OpenAIGatewayService{}
	reporter := &countingReporter{OpenAIAccountReporter: master.LocalAccountReporter()}
	node.SetAccountReporter(reporter)
	node.SetUpstreamErrorDecider(master.LocalUpstreamErrorDecider())
	oauth := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKey := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	for _, gw := range []*OpenAIGatewayService{local, node} {
		// OAuth 429 打开同账号重试窗口；之后一次成功的调度结果关掉它。
		gw.handleOpenAIAccountUpstreamError(ctx, oauth, http.StatusTooManyRequests, http.Header{}, nil)
		gw.ReportOpenAIAccountScheduleResult(oauth, "gpt-5", true, nil)
		gw.tempUnscheduleOpenAITransportError(ctx, apiKey, "dial tcp: connection refused")
		gw.RecordOpenAIAccountSwitch()
		gw.ObserveOpenAIAccountHealthFailure(ctx, apiKey, errors.New("boom"))
	}
	for _, gw := range []*OpenAIGatewayService{local, master} {
		_, open := gw.openaiOAuth429RetryStartedAt.Load(oauth.ID)
		require.False(t, open, "a successful result closes the retry window")
		require.True(t, gw.isOpenAIAccountRuntimeBlocked(apiKey), "the transport error blocks the account where the scheduler runs")
	}
	require.False(t, node.isOpenAIAccountRuntimeBlocked(apiKey), "nothing is recorded on the node")
	require.Equal(t, int64(1), reporter.results.Load())
	require.Equal(t, int64(1), reporter.transport.Load())
	require.Equal(t, int64(1), reporter.switches.Load())
	require.Equal(t, int64(1), reporter.health.Load())

	node.UpdateCodexUsageSnapshotFromHeaders(ctx, oauth.ID, http.Header{"X-Codex-Primary-Used-Percent": {"40"}})
	require.Equal(t, int64(1), reporter.codex.Load())
	node.UpdateCodexUsageSnapshotFromHeaders(ctx, oauth.ID, http.Header{})
	require.Equal(t, int64(1), reporter.codex.Load(), "no snapshot, nothing to report")
}

// 健康熔断的失败事实经事件还原后，分类结果与原错误一致。
func TestHealthFailureFactsRoundTrip(t *testing.T) {
	cases := []error{
		&UpstreamFailoverError{StatusCode: 500, ResponseBody: []byte("oops")},
		&UpstreamFailoverError{StatusCode: 429},
		&UpstreamFailoverError{StatusCode: 400},
		&UpstreamFailoverError{StatusCode: 500, Stage: GatewayFailureStageAccountAuth},
		&UpstreamFailoverError{StatusCode: 503, RequestScopedTransient: true},
		&UpstreamFailoverError{StatusCode: 502, Scope: GatewayFailureScopeProvider},
		&UpstreamFailoverError{StatusCode: 500, RetryableOnSameAccount: true},
		fmt.Errorf("wrapped: %w", &UpstreamFailoverError{StatusCode: 502}),
		&OpenAIImagesUpstreamError{StatusCode: 500, Message: " image failed "},
		&OpenAIImagesUpstreamError{StatusCode: 400},
		context.Canceled,
		errors.New("plain"),
		nil,
	}
	for i, err := range cases {
		status, body, eligible := OpenAIHealthFailureFacts(err)
		rebuilt := OpenAIHealthFailureError(status, body, eligible)
		s2, b2, e2 := classifyOpenAIAPIKeyHealthFailure(rebuilt)
		require.Equal(t, eligible, e2, "case %d", i)
		if eligible {
			require.Equal(t, status, s2, "case %d", i)
			require.Equal(t, body, b2, "case %d", i)
		}
	}
}

// OpenAI 转发文件不能绕过 OpenAIAccountReporter 直接改账号状态。
func TestOpenAIForwardPathUsesTheAccountReporter(t *testing.T) {
	forbidden := []string{
		"reportOpenAIAccountScheduleResultLocal(",
		"observeOpenAIAccountHealthFailureLocal(",
		"recordOpenAIAccountSwitchLocal(",
		"updateCodexUsageSnapshotLocal(",
		"tempUnscheduleOpenAITransportErrorLocal(",
		"scheduleOllamaCloudUsageActivity(",
		"rateLimitService.UpdateSessionWindow(",
	}
	allowed := map[string]string{
		"openai_account_reporter.go":               "the local reporter",
		"openai_account_scheduler.go":              "the local reporting itself",
		"openai_gateway_usage.go":                  "the local snapshot update itself",
		"openai_upstream_transport_error.go":       "the local transport block itself",
		"openai_account_runtime_block_fastpath.go": "inside the upstream error decision, which already runs on the master",
	}
	files, err := filepath.Glob("openai_*.go")
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] != "" {
			continue
		}
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, pattern := range forbidden {
			require.NotContains(t, string(raw), pattern, "%s calls %s directly; route it through s.accountReporter()", f, pattern)
		}
	}
}
