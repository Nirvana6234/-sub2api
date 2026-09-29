//go:build unit

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 非 OpenAI 网关服务的转发文件（Anthropic、Gemini、Antigravity、Bedrock、Vertex、Ollama、Grok、TypeSafe）不能直接用
// 限流服务，连 nil 判断也不行：从节点上限流服务是 nil，"有它才判定"会让判定在从节点上悄悄跳过。判定经
// s.accountState()（AccountStateDecider）。只在主节点跑的（选号、调度）或还没接入主从分流的，行尾要写
// "// relay:master-only ..." 或 "// relay:pending ..."（后者要在开发计划的剩余事项总账里有对应项）。
func TestGatewayForwardPathUsesTheAccountStateDecider(t *testing.T) {
	var files []string
	for _, pattern := range []string{"gateway_*.go", "gemini_*.go", "antigravity_*.go", "bedrock*.go", "vertex*.go", "ollama*.go", "grok_*.go", "typesafe*.go"} {
		matched, err := filepath.Glob(pattern)
		require.NoError(t, err)
		files = append(files, matched...)
	}
	require.NotEmpty(t, files)
	allowed := map[string]string{
		"gateway_usage_billing.go": "usage billing runs on the master (relay settlement)",
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] != "" {
			continue
		}
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if !strings.Contains(line, "rateLimitService.") && !strings.Contains(line, "rateLimitService == nil") && !strings.Contains(line, "rateLimitService != nil") {
				continue
			}
			if strings.Contains(line, "// relay:master-only") || strings.Contains(line, "// relay:pending") {
				continue
			}
			t.Errorf("%s:%d uses the rate limit service directly; route it through s.accountState() or mark it // relay:master-only / // relay:pending: %s", f, i+1, strings.TrimSpace(line))
		}
	}
}
