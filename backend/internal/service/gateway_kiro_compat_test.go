package service

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Codex sends the hosted web_search alongside a dozen function tools. Only the
// hosted declaration may go — stripping the rest would take away shell,
// apply_patch and everything else the session depends on.
func TestStripHostedWebSearchTool_RemovesOnlyTheHostedDeclaration(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.2-codex",
		"tools": [
			{"type": "function", "name": "shell"},
			{"type": "web_search"},
			{"type": "function", "name": "apply_patch"}
		]
	}`)

	out, changed := StripHostedWebSearchTool(body)

	require.True(t, changed)
	names := []string{}
	for _, tool := range gjson.GetBytes(out, "tools").Array() {
		assert.Equal(t, "function", tool.Get("type").String())
		names = append(names, tool.Get("name").String())
	}
	assert.Equal(t, []string{"shell", "apply_patch"}, names)
}

// Deleting by index has to run back to front, or removing an early entry shifts
// the ones still queued and the wrong tools disappear.
func TestStripHostedWebSearchTool_RemovesEveryHostedVariant(t *testing.T) {
	body := []byte(`{
		"tools": [
			{"type": "web_search"},
			{"type": "function", "name": "shell"},
			{"type": "web_search_20250305"},
			{"type": "function", "name": "apply_patch"},
			{"type": "google_search"}
		]
	}`)

	out, changed := StripHostedWebSearchTool(body)

	require.True(t, changed)
	tools := gjson.GetBytes(out, "tools").Array()
	require.Len(t, tools, 2)
	assert.Equal(t, "shell", tools[0].Get("name").String())
	assert.Equal(t, "apply_patch", tools[1].Get("name").String())
}

// A request with no hosted tool must come back byte-identical, so the flag stays
// inert for traffic it does not concern.
func TestStripHostedWebSearchTool_LeavesUnrelatedRequestsAlone(t *testing.T) {
	body := []byte(`{"model":"gpt-5.2-codex","tools":[{"type":"function","name":"shell"}]}`)

	out, changed := StripHostedWebSearchTool(body)

	assert.False(t, changed)
	assert.JSONEq(t, string(body), string(out))
}

// No tools at all, or a tools value that is not an array, is not an error.
func TestStripHostedWebSearchTool_ToleratesMissingOrOddTools(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-5.2-codex"}`,
		`{"model":"gpt-5.2-codex","tools":null}`,
		`{"model":"gpt-5.2-codex","tools":{}}`,
		`{"model":"gpt-5.2-codex","tools":[]}`,
	} {
		out, changed := StripHostedWebSearchTool([]byte(body))

		assert.False(t, changed, "body %s", body)
		assert.JSONEq(t, body, string(out), "body %s", body)
	}
}

// When the hosted tool was the only one, the key goes rather than leaving an
// empty array — the request never asked for a toolless turn spelled that way.
func TestStripHostedWebSearchTool_DropsTheKeyWhenNothingRemains(t *testing.T) {
	body := []byte(`{"model":"gpt-5.2-codex","tools":[{"type":"web_search"}]}`)

	out, changed := StripHostedWebSearchTool(body)

	require.True(t, changed)
	assert.False(t, gjson.GetBytes(out, "tools").Exists())
	assert.True(t, json.Valid(out))
}

// The rest of the request has to survive untouched: this runs before the body is
// frozen for forwarding, so anything it corrupts goes upstream.
func TestStripHostedWebSearchTool_PreservesEverythingElse(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.2-codex",
		"instructions": "you are a coding agent",
		"input": [{"role": "user", "content": "hi"}],
		"reasoning": {"effort": "high"},
		"stream": true,
		"tools": [{"type": "web_search"}, {"type": "function", "name": "shell"}]
	}`)

	out, changed := StripHostedWebSearchTool(body)

	require.True(t, changed)
	assert.Equal(t, "gpt-5.2-codex", gjson.GetBytes(out, "model").String())
	assert.Equal(t, "you are a coding agent", gjson.GetBytes(out, "instructions").String())
	assert.Equal(t, "hi", gjson.GetBytes(out, "input.0.content").String())
	assert.Equal(t, "high", gjson.GetBytes(out, "reasoning.effort").String())
	assert.True(t, gjson.GetBytes(out, "stream").Bool())
}

// On a Kiro group the account's wildcard mapping is the default-model
// mechanism: Codex asks for gpt-*, which the upstream cannot serve, and "*"
// rewrites it. A model the user actually chose has to survive that.
func TestShouldSkipModelMappingForKiro(t *testing.T) {
	gin.SetMode(gin.TestMode)

	kiro := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		MarkKiroCompat(c)
		return c
	}
	plain := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		return c
	}

	// Chosen Claude models keep their identity.
	for _, model := range []string{"claude-opus-5", "claude-sonnet-5", "anthropic/claude-opus-5", "sonnet-4-5"} {
		assert.True(t, shouldSkipModelMappingForKiro(kiro(), model),
			"model %q was chosen deliberately and must not be remapped", model)
	}

	// Anything the upstream cannot serve falls through to the wildcard.
	for _, model := range []string{"gpt-5.2-codex", "gpt-5.1", "o3"} {
		assert.False(t, shouldSkipModelMappingForKiro(kiro(), model),
			"model %q has no meaning upstream and needs the default", model)
	}

	// Off the Kiro line the flag changes nothing: mapping behaves as before.
	assert.False(t, shouldSkipModelMappingForKiro(plain(), "claude-opus-5"))
	assert.False(t, shouldSkipModelMappingForKiro(nil, "claude-opus-5"))
}

// IsClaude decides whether a choice was deliberate, so a routed id must not read
// as "not a Claude model" and get overwritten by the wildcard default.
func TestIsClaude_ToleratesRoutingSegments(t *testing.T) {
	for _, model := range []string{
		"claude-opus-5", "CLAUDE-SONNET-5", "anthropic/claude-opus-5",
		"claude-sonnet-5/variant", "opus-4-1", "haiku-4-5", " claude-opus-5 ",
	} {
		assert.True(t, IsClaude(model), "model %q", model)
	}
	for _, model := range []string{"gpt-5.2-codex", "gemini-3-pro", "", "kiro/gpt-5"} {
		assert.False(t, IsClaude(model), "model %q", model)
	}
}
