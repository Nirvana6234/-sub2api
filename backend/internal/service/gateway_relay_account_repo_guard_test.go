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
// SetTempUnschedulable；只在主节点跑的行尾写 "// relay:master-only ..."。
func TestAnthropicForwardPathOnlyTempUnschedulesAccounts(t *testing.T) {
	files := []string{
		"gateway_forward.go", "gateway_anthropic_passthrough.go", "gateway_bedrock.go", "bedrock_stream.go",
		"gateway_claude_oauth_body.go", "gateway_upstream_request.go", "gateway_upstream_response.go",
		"gateway_upstream_transport_error.go", "gateway_websearch_emulation.go", "gateway_messages_cache.go",
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err, f)
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") || !strings.Contains(line, "accountRepo.") {
				continue
			}
			if strings.Contains(line, "accountRepo.SetTempUnschedulable(") || strings.Contains(line, "// relay:master-only") {
				continue
			}
			t.Errorf("%s:%d uses the account repository on the forward path; relay nodes only support SetTempUnschedulable: %s", f, i+1, strings.TrimSpace(line))
		}
	}
}
