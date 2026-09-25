package handler

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexBootstrapNormalizersLeaveOrdinaryRequestsUntouched(t *testing.T) {
	// The tool names appear in tools and in other items, but no input item is
	// a call output of those tools, so neither normalizer may rewrite the body.
	body := []byte(`{"model":"gpt-5","tools":[{"type":"function","namespace":"codex_app","name":"automation_update"},{"type":"function","namespace":"codex_app","name":"create_thread"}],` +
		`"input":[{"type":"message","role":"user","content":"automation_update create_thread"},` +
		`{"type":"function_call","call_id":"call-1","namespace":"codex_app","name":"create_thread"},` +
		`{"type":"function_call_output","call_id":"call-1","name":"shell","output":"done"}]}`)

	got, changed := normalizeCodexAutomationBootstrap(body)
	require.False(t, changed)
	require.Equal(t, body, got)

	got, changed = normalizeCodexDelegationBootstrap(body)
	require.False(t, changed)
	require.Equal(t, body, got)
}

func TestCodexBootstrapNormalizersFindCandidateAfterLongHistory(t *testing.T) {
	var items []string
	for i := range 200 {
		callID := "call-" + strconv.Itoa(i)
		items = append(items,
			`{"type":"function_call","call_id":"`+callID+`","name":"shell","arguments":"{}"}`,
			`{"type":"function_call_output","call_id":"`+callID+`","output":"`+strings.Repeat("x", 512)+`"}`)
	}
	items = append(items, `{"type":"function_call_output","namespace":"codex_app","name":"send_message_to_thread","output":"`+delegationEnvelope+`"}`)
	body := []byte(`{"model":"gpt-5","input":[` + strings.Join(items, ",") + `]}`)

	got, changed := normalizeCodexDelegationBootstrap(body)
	require.True(t, changed)
	last := gjson.GetBytes(got, "input.400")
	require.Equal(t, "message", last.Get("type").String())
	require.Equal(t, delegationEnvelope, last.Get("content.0.text").String())
}

func TestCodexBootstrapNormalizersMatchEscapedFields(t *testing.T) {
	// encoding/json unescapes member names and values, so the prefilter must
	// recognise the same candidate when a client escapes them.
	output := mustJSON(t, codexAutomationBootstrap("scheduled-review", "never", automationBootstrapPrompt))
	body := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + output + `}]}`)

	got, changed := normalizeCodexAutomationBootstrap(body)
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
}
