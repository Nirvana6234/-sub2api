package securityaudit

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// preparedOverWire 模拟从节点：算好审核输入、去掉请求体，经 JSON 发到主节点。
func preparedOverWire(t *testing.T, req Request) Request {
	t.Helper()
	prepared := PrepareRequest(req)
	require.Nil(t, prepared.Body)
	raw, err := json.Marshal(prepared.Prepared)
	require.NoError(t, err)
	var decoded PreparedInput
	require.NoError(t, json.Unmarshal(raw, &decoded))
	prepared.Prepared = &decoded
	return prepared
}

var preparedEquivalenceBodies = []struct{ protocol, body string }{
	{"openai_chat_completions", `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"old"},{"role":"assistant","content":"assistant turn"},{"role":"user","content":[{"type":"text","text":"最新😀"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`},
	{"openai_responses", `{"instructions":"be nice","input":[{"role":"user","content":[{"type":"input_text","text":"response text"},{"type":"input_image","image_url":"https://example.com/a.png"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"prev answer"}]},{"role":"user","content":"latest"}]}`},
	{"openai_responses", `{"input":"plain string input"}`},
	{"anthropic_messages", `{"system":[{"type":"text","text":"harness"}],"messages":[{"role":"user","content":[{"type":"text","text":"claude"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"BBBB"}}]},{"role":"assistant","content":"ok"},{"role":"user","content":"again"}]}`},
	{"gemini", `{"systemInstruction":{"parts":[{"text":"sys"}]},"contents":[{"role":"user","parts":[{"text":"gemini"},{"inline_data":{"mime_type":"image/png","data":"BASE64"}}]}]}`},
	{"openai_images", `{"prompt":"draw a cat","image":"BASE64SECRET"}`},
	{"responses_websocket", `{"type":"response.create","response":{"input":"turn two"}}`},
	{"responses_websocket", `{"type":"response.create","input":[{"role":"user","content":"ws flat"}]}`},
	{"responses_websocket", `{"type":"session.update"}`},
	{"openai_chat_completions", `{"messages":[]}`},
	{"openai_chat_completions", `{"messages":[{"role":"user","content":`},
	{"openai_chat_completions", ``},
	{"openai_chat_completions", `{"messages":[{"role":"user","content":[{"type":"text","text":"   
	 "},{"type":"text","text":" "}]}]}`},
	{"openai_chat_completions", `{"messages":[{"role":"user","content":[{"type":"text","text":"many"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},{"type":"image_url","image_url":{"url":"data:image/png;base64,BBBB"}},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},{"type":"image_url","image_url":{"url":"https://example.com/c.png"}}]}]}`},
	{"openai_responses", `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,CCCC"},{"type":"input_image","image_url":"data:image/png;base64,DDDD"}]}]}`},
}

// 提示词审计：拿请求体与拿预先抽好的输入（过一遍 JSON）得到的快照、错误完全一样，含最新一轮模式。
func TestPreparedRequestPromptSnapshotMatchesBody(t *testing.T) {
	for _, tc := range preparedEquivalenceBodies {
		req := Request{RequestID: "r1", UserID: 7, APIKeyID: 9, Protocol: tc.protocol, Model: "m", Body: []byte(tc.body), Stage: "http"}
		prepared := preparedOverWire(t, req)
		for _, latest := range []bool{false, true} {
			want, wantErr := ExtractBlockingPromptSnapshot(req, latest)
			got, gotErr := ExtractBlockingPromptSnapshot(prepared, latest)
			require.Equal(t, wantErr, gotErr, "%s %s latest=%v", tc.protocol, tc.body, latest)
			require.Equal(t, want, got, "%s %s latest=%v", tc.protocol, tc.body, latest)
		}
		want, wantErr := ExtractPromptSnapshot(req)
		got, gotErr := ExtractPromptSnapshot(prepared)
		require.Equal(t, wantErr, gotErr)
		require.Equal(t, want, got)
	}
}

// 内容审核：交给审核服务的输入与从请求体抽的判定一样：文字、是否为空、图片数、输入哈希（命中过的哈希拦截用）相同，
// 图片多时只带抽中的那张（审核接口一次只要一张，单机也是随机抽一张）。
func TestPreparedRequestModerationInputMatchesBody(t *testing.T) {
	for _, tc := range preparedEquivalenceBodies {
		req := Request{Protocol: tc.protocol, Body: []byte(tc.body)}
		want := service.ExtractContentModerationInput(tc.protocol, []byte(tc.body))
		prepared := preparedOverWire(t, req)
		got := preparedModeration(prepared)
		require.NotNil(t, got)
		got.Normalize()
		require.Equal(t, want.Text, got.Text, "%s %s", tc.protocol, tc.body)
		require.Equal(t, want.IsEmpty(), got.IsEmpty())
		require.Equal(t, want.ImageCount(), got.ImageCount())
		require.Equal(t, want.Hash(), got.Hash(), "%s %s", tc.protocol, tc.body)
		require.LessOrEqual(t, len(got.Images), 1)
		for _, image := range got.Images {
			require.Contains(t, want.Images, image)
		}
		if len(want.Images) <= 1 {
			require.Equal(t, want.ModerationInput(), got.ModerationInput())
		}
		// 带请求体时不用预先抽好的（单机路径不变）。
		require.Nil(t, preparedModeration(req))
	}
}

type recordingPromptEngine struct {
	mode Mode
	got  []Request
}

func (r *recordingPromptEngine) EffectiveMode() Mode { return r.mode }
func (r *recordingPromptEngine) Enqueue(_ context.Context, req Request) error {
	r.got = append(r.got, req)
	return nil
}
func (r *recordingPromptEngine) Evaluate(_ context.Context, req Request) (*PromptDecision, error) {
	r.got = append(r.got, req)
	return nil, nil
}

// 协调器把预先抽好的输入原样交给提示词审计（Clone 深拷贝，不共享切片）。
func TestCoordinatorPassesPreparedInputThrough(t *testing.T) {
	req := PrepareRequest(Request{Protocol: "openai_chat_completions", Body: []byte(`{"messages":[{"role":"user","content":"hi"}]}`)})
	engine := &recordingPromptEngine{mode: ModeAsync}
	NewCoordinator(nil, engine).Check(context.Background(), req)
	require.Len(t, engine.got, 1)
	require.Equal(t, req.Prepared, engine.got[0].Prepared)
	require.NotSame(t, req.Prepared, engine.got[0].Prepared)
	snapshot, err := ExtractPromptSnapshot(engine.got[0])
	require.NoError(t, err)
	require.Equal(t, "hi", snapshot.ScanText)
}
