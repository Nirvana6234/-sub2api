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
