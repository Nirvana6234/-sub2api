//go:build unit

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// OpenAI 转发路径上的上游错误判定必须经 OpenAIUpstreamErrorDecider：直接调限流服务的话，
// 主从分流的从节点上这些判定会在没有数据库、没有调度状态的地方执行（或者根本不执行）。
func TestOpenAIForwardPathUsesTheUpstreamErrorDecider(t *testing.T) {
	forbidden := []string{
		"rateLimitService.HandleStreamTimeout(",
		"rateLimitService.CheckErrorPolicy(",
		"rateLimitService.HandleUpstreamError(",
		"handleOpenAIAccountUpstreamErrorLocal(",
	}
	// 判定的本机实现本身；以及还没接入主从分流的入口（接入时改走判定接口，从这里删掉）。
	allowed := map[string]string{
		"openai_upstream_error_decider.go":         "the local decider",
		"openai_account_runtime_block_fastpath.go": "the local decision itself",
		"openai_gateway_count_tokens.go":           "count_tokens is not served by relay nodes yet (WP7 later slice)",
	}
	files, err := filepath.Glob("openai_*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] != "" {
			continue
		}
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, pattern := range forbidden {
			require.NotContains(t, string(raw), pattern, "%s calls %s directly; route it through s.errorDecider()", f, pattern)
		}
	}
}

// OpenAI 转发文件不能碰限流服务，连 nil 判断也不行：从节点上限流服务是 nil，"有它才判定"的写法会让判定在
// 从节点上悄悄跳过（单机照常执行）。要判定就经 s.errorDecider()，要看有没有地方判定用 s.hasErrorDecider()。
func TestOpenAIForwardPathDoesNotTouchTheRateLimitService(t *testing.T) {
	allowed := map[string]string{
		"openai_upstream_error_decider.go":         "the local decider",
		"openai_account_runtime_block_fastpath.go": "the local decision itself",
		"openai_account_reporter.go":               "the local reporter",
		"openai_account_scheduler.go":              "scheduling runs on the master",
		"openai_gateway_scheduling.go":             "scheduling runs on the master",
		"openai_gateway_service.go":                "the constructor",
		"openai_gateway_usage.go":                  "RecordUsage runs on the master (relay settlement)",
		"openai_gateway_count_tokens.go":           "count_tokens is not served by relay nodes yet (WP7 later slice)",
	}
	files, err := filepath.Glob("openai_*.go")
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] != "" {
			continue
		}
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "rateLimitService", "%s uses the rate limit service; route it through s.errorDecider() / s.accountReporter()", f)
	}
}
