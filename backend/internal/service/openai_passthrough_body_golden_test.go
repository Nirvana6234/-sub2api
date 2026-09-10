//go:build unit

package service

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// 用 `go test -tags=unit ./internal/service -run Golden -update-passthrough-golden`
// 重新生成 golden。**只有在确认出站字节变化是本意时才允许重新生成**——这个 flag
// 存在的意义是让"重生成"成为一个显式动作，而不是让人手改 golden 文件。
var updatePassthroughGolden = flag.Bool("update-passthrough-golden", false,
	"regenerate the non-strict passthrough golden from the current code")

const (
	codexResponsesFixturePath  = "testdata/codex_responses_request.json"
	codexResponsesAuthOnlyGold = "testdata/codex_responses_upstream_auth_only.golden.json"
)

// codexResponsesFixture 是 probe-local-proxy.py 录的真实 Codex 报文（已脱敏）。
// 保真边界见 backend/scripts/sanitize-codex-responses-fixture.py 与 fixture 里的
// _notes：tools、字段顺序与各开关字段逐字节保留；标识 UUID 与本机路径被换成固定
// 占位符；instructions 与 developer 长文被截断。
type codexResponsesFixture struct {
	Notes             string            `json:"_notes"`
	Truncated         []string          `json:"_truncated"`
	OriginalBodyBytes string            `json:"_originalBodyBytes"`
	Method            string            `json:"method"`
	Path              string            `json:"path"`
	Headers           map[string]string `json:"headers"`
	Body              json.RawMessage   `json:"body"`
}

func loadCodexResponsesFixture(t *testing.T) codexResponsesFixture {
	t.Helper()
	raw, err := os.ReadFile(codexResponsesFixturePath)
	require.NoError(t, err, "读不到 Codex 请求 fixture；重录方式见 fixture 的 _notes")

	var fixture codexResponsesFixture
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.NotEmpty(t, fixture.Body, "fixture 里没有 body")
	return fixture
}

// TestCodexResponsesFixture_ShapeGuard 锁住这份 fixture 必须携带的形状。
//
// 为什么需要它：本包后续多条断言（strict 是否少改了字段、namespace 工具是否被
// 展平、instructions 是否被合成）都靠这份 fixture 提供落点。哪天有人重录时漏了
// 其中一项，那些断言不会变红——它们会静默地什么都没验。这正是本仓库踩过的坑：
// 旧 fixture 只存了 body 的顶层键名，于是"零漂移"的结论是从没比过的内容里得出的。
func TestCodexResponsesFixture_ShapeGuard(t *testing.T) {
	fixture := loadCodexResponsesFixture(t)

	var body map[string]any
	require.NoError(t, json.Unmarshal(fixture.Body, &body))

	require.Equal(t, "/v1/responses", fixture.Path)

	instructions, ok := body["instructions"].(string)
	require.True(t, ok, "instructions 必须存在且为字符串——§2.5 取消合成/403 的落点")
	require.NotEmpty(t, instructions)

	input, ok := body["input"].([]any)
	require.True(t, ok, "input 必须是数组——§2.4 取消 input 归一的落点")
	require.NotEmpty(t, input)

	tools, ok := body["tools"].([]any)
	require.True(t, ok, "tools 必须存在")
	require.NotEmpty(t, tools)

	var hasNamespaceTool bool
	for _, tool := range tools {
		if entry, isMap := tool.(map[string]any); isMap && entry["type"] == "namespace" {
			hasNamespaceTool = true
			break
		}
	}
	require.True(t, hasNamespaceTool,
		"必须含 namespace 型工具——命名空间展平/还原那条链路的唯一落点")

	metadata, ok := body["client_metadata"].(map[string]any)
	require.True(t, ok, "client_metadata 必须存在——身份影射与指纹收敛的落点")
	require.Contains(t, metadata, "x-codex-installation-id")
	require.Contains(t, metadata, "x-codex-turn-metadata")

	require.Contains(t, body, "reasoning", "reasoning 必须存在")
	require.Equal(t, false, body["store"], "真实 Codex 自己就发 store=false")
	require.Equal(t, true, body["stream"])
}

// TestOpenAIPassthroughOAuthBody_AuthOnlyGolden 锁住**非 strict** 透传的出站字节。
//
// 这是"本变更不改变现状"的唯一硬证据：既有测试全绿只说明没测到的地方没崩，
// 只有 golden 能说明出站字节没变。golden 在阶段 2 动手之前用当时的代码生成。
func TestOpenAIPassthroughOAuthBody_AuthOnlyGolden(t *testing.T) {
	fixture := loadCodexResponsesFixture(t)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(fixture.Body, false)
	require.NoError(t, err)
	require.False(t, changed,
		"真实 Codex 报文本来就满足 store=false/stream=true 且不含被删字段，非 strict 路径不该改动它")

	if *updatePassthroughGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(codexResponsesAuthOnlyGold), 0o755))
		require.NoError(t, os.WriteFile(codexResponsesAuthOnlyGold, normalized, 0o644))
		t.Logf("regenerated %s (%d bytes)", codexResponsesAuthOnlyGold, len(normalized))
		return
	}

	want, err := os.ReadFile(codexResponsesAuthOnlyGold)
	require.NoError(t, err, "golden 缺失；用 -update-passthrough-golden 生成")
	require.True(t, bytes.Equal(want, normalized),
		"非 strict 透传的出站字节变了。若是本意，用 -update-passthrough-golden 重新生成并在 PR 里说明改了什么")
}
