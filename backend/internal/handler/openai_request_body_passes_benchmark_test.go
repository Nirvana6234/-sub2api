package handler

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var openAIRequestBodyPassesBenchmarkSink []byte

// buildLongCodexSessionBody 构造一份接近 Codex 长会话的 /responses 请求体：
// 多轮消息、带加密内容的 reasoning、function_call / function_call_output 交替，
// tools 里也声明了 automation_update 等名字——它们会出现在正文里，但不构成
// 需要归一化的调用输出。每一跳中转都会对这样一份请求体做整包处理。
func buildLongCodexSessionBody(tb testing.TB, targetBytes int) []byte {
	tb.Helper()
	tools := []map[string]any{
		{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}},
		{"type": "function", "namespace": "codex_app", "name": "automation_update", "parameters": map[string]any{"type": "object"}},
		{"type": "function", "namespace": "codex_app", "name": "create_thread", "parameters": map[string]any{"type": "object"}},
	}
	input := make([]map[string]any, 0, 256)
	encrypted := strings.Repeat("gAAAAABo", 512)
	toolOutput := strings.Repeat("line of command output with some text\\n", 64)
	size := 0
	for turn := 0; size < targetBytes; turn++ {
		callID := "call_" + strconv.Itoa(turn)
		items := []map[string]any{
			{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "please continue with step " + strconv.Itoa(turn)}}},
			{"type": "reasoning", "summary": []any{}, "encrypted_content": encrypted},
			{"type": "function_call", "call_id": callID, "name": "shell", "arguments": `{"command":["ls","-la"]}`},
			{"type": "function_call_output", "call_id": callID, "output": toolOutput},
		}
		for _, item := range items {
			raw, err := json.Marshal(item)
			if err != nil {
				tb.Fatal(err)
			}
			size += len(raw)
		}
		input = append(input, items...)
	}
	body, err := json.Marshal(map[string]any{
		"model":            "gpt-5.5",
		"stream":           true,
		"store":            false,
		"instructions":     strings.Repeat("You are Codex. ", 200),
		"tools":            tools,
		"input":            input,
		"prompt_cache_key": "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b",
		"reasoning":        map[string]any{"effort": "high"},
	})
	if err != nil {
		tb.Fatal(err)
	}
	return body
}

func BenchmarkOpenAIRequestBodyPasses_LongCodexSession(b *testing.B) {
	body := buildLongCodexSessionBody(b, 1<<20)

	b.Run("codex_bootstrap_normalizers", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()
		for range b.N {
			out, _ := normalizeCodexAutomationBootstrap(body)
			out, _ = normalizeCodexDelegationBootstrap(out)
			openAIRequestBodyPassesBenchmarkSink = out
		}
	})

	b.Run("compaction_trigger_order", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()
		for range b.N {
			out, _, err := service.NormalizeCompactionTriggerInputOrder(body)
			if err != nil {
				b.Fatal(err)
			}
			openAIRequestBodyPassesBenchmarkSink = out
		}
	})
}
