//go:build unit

package service

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 从节点上 Anthropic 转发路径的 GatewayService 只有一个"只写临时不可调度"的账号仓储（nodegw.relayAccountRepo，
// 作为账号事件交给主节点照写），其余方法没有实现、调到就会 panic。这些转发文件里用账号仓储只能是
// 临时不可调度、模型级 / 账号级限流、模型限流表的 UpdateExtra（nodegw.relayAccountRepo 实现的这几个）；只在主节点跑的行尾写
// "// relay:master-only ..."。
func TestAnthropicForwardPathOnlyTempUnschedulesAccounts(t *testing.T) {
	files := []string{
		"gateway_forward.go", "gateway_anthropic_passthrough.go", "gateway_bedrock.go", "bedrock_stream.go",
		"gateway_claude_oauth_body.go", "gateway_upstream_request.go", "gateway_upstream_response.go",
		"gateway_upstream_transport_error.go", "gateway_websearch_emulation.go", "gateway_messages_cache.go",
		// Antigravity 账号（混合调度进 Anthropic 分组的）经同一套从节点仓储：模型级 / 账号级限流、积分耗尽标记也是账号事件。
		"antigravity_gateway_service.go", "antigravity_gateway_claude.go", "antigravity_gateway_compat.go",
		"antigravity_gateway_compat_stream.go", "antigravity_gateway_retry.go", "antigravity_gateway_streaming.go",
		"antigravity_gateway_upstream.go", "antigravity_credits_overages.go", "antigravity_internal500_penalty.go",
		// Gemini 原生入口的转发路径：429 的账号级限流是账号事件（选号、Gemini 账号列表等只在主节点跑，行尾标 relay:master-only）。
		"gemini_messages_compat_service.go", "antigravity_gateway_gemini.go",
	}
	allowed := []string{"accountRepo.SetTempUnschedulable(", "accountRepo.SetModelRateLimit(", "accountRepo.SetRateLimited(", "accountRepo.UpdateExtra("}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err, f)
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") || !strings.Contains(line, "accountRepo.") {
				continue
			}
			if strings.Contains(line, "// relay:master-only") || lineContainsAny(line, allowed) {
				continue
			}
			t.Errorf("%s:%d uses the account repository on the forward path; relay nodes only support SetTempUnschedulable: %s", f, i+1, strings.TrimSpace(line))
		}
	}
}

func lineContainsAny(line string, parts []string) bool {
	for _, p := range parts {
		if strings.Contains(line, p) {
			return true
		}
	}
	return false
}

// Grok 转发路径（从节点上没有账号仓储）：错误响应、用量头、流空闲对账号状态的写入都经 GrokAccountReporter 交给主节点（见
// grok_account_reporter.go），本机实现（*Local 函数）只在主节点和单机上跑。这些文件里直接用账号仓储的行必须标
// "// relay:master-only"。
func TestGrokForwardPathWritesAccountStateOnlyThroughTheReporter(t *testing.T) {
	files := []string{
		"openai_gateway_grok.go", "openai_gateway_grok_chat_bridge.go", "openai_gateway_grok_cache.go", "openai_gateway_grok_compact.go",
		"grok_media.go", "grok_audio.go", "grok_upstream_errors.go", "grok_upstream_failure.go", "grok_upstream_headers.go",
		"grok_search_count.go", "grok_stream_idle.go", "seedance.go", "openai_gateway_cc_pipeline.go", "openai_gateway_chat_completions_raw.go",
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err, f)
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") || !strings.Contains(line, "accountRepo.") {
				continue
			}
			if strings.Contains(line, "// relay:master-only") {
				continue
			}
			t.Errorf("%s:%d uses the account repository on the Grok forward path; route it through GrokAccountReporter or mark it // relay:master-only: %s", f, i+1, strings.TrimSpace(line))
		}
	}
}
