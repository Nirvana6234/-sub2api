package service

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// kiroCompatContextKey carries the group's kiro_compat flag from the handler,
// which reads it off the API key, down to the forward path, which does not have
// the group.
const kiroCompatContextKey = "kiro_compat"

// MarkKiroCompat records that this request belongs to a Kiro relay group.
func MarkKiroCompat(c *gin.Context) {
	if c != nil {
		c.Set(kiroCompatContextKey, true)
	}
}

func isKiroCompat(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(kiroCompatContextKey)
	if !ok {
		return false
	}
	enabled, _ := value.(bool)
	return enabled
}

// IsClaude reports whether a model id names a Claude model.
//
// A routing prefix or suffix is tolerated ("anthropic/claude-opus-5",
// "claude-opus-5/variant"), because callers use this to decide whether a model
// was deliberately chosen. Missing a routed id there would quietly rewrite a
// user's explicit choice to a default.
func IsClaude(modelID string) bool {
	for _, segment := range strings.Split(strings.ToLower(strings.TrimSpace(modelID)), "/") {
		if strings.HasPrefix(segment, "claude-") ||
			strings.HasPrefix(segment, "opus-") ||
			strings.HasPrefix(segment, "sonnet-") ||
			strings.HasPrefix(segment, "haiku-") {
			return true
		}
	}
	return false
}

// shouldSkipModelMappingForKiro reports whether account model mapping must be
// left unapplied for this request.
//
// On a Kiro group the account's wildcard mapping is what supplies a default
// model: Codex asks for gpt-*, which the upstream has no idea about, and a "*"
// entry rewrites it to a Claude model. That default must not touch a model the
// user actually chose — a wildcard applies to everything, so without this an
// explicit claude-opus-5 would be rewritten to whatever the wildcard names.
//
// So the flag narrows the wildcard's reach from "map everything" to "map only
// what isn't already a Claude model". Requests naming a Claude model go upstream
// exactly as asked.
func shouldSkipModelMappingForKiro(c *gin.Context, requestedModel string) bool {
	return isKiroCompat(c) && IsClaude(requestedModel)
}

// StripHostedWebSearchTool removes the hosted web_search tool from a Responses
// API request body, returning the rewritten body and whether anything changed.
//
// Codex advertises web_search as a hosted tool — one the *provider* is expected
// to execute rather than hand back to the client. Anthropic implements that
// contract as a server tool (web_search_20250305), and ResponsesToAnthropicRequest
// converts it accordingly. A Kiro relay sits behind an Anthropic-shaped API but
// is not Anthropic and does not run the model's searches for it, so the tool
// arrives at a provider that cannot honour it.
//
// Dropping the declaration is what a group flags kiro_compat for. The model then
// simply has no search tool, which costs a capability but keeps the turn intact —
// preferable to advertising one that the upstream mishandles.
//
// Only the hosted declaration goes; function tools are untouched, so Codex keeps
// shell, apply_patch and the rest.
func StripHostedWebSearchTool(body []byte) ([]byte, bool) {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return body, false
	}

	entries := tools.Array()
	var hostedIndexes []int
	for i, tool := range entries {
		if isWebSearchToolJSON(tool) {
			hostedIndexes = append(hostedIndexes, i)
		}
	}
	if len(hostedIndexes) == 0 {
		return body, false
	}

	// Delete from the back so the earlier indexes stay valid as the array shrinks.
	out := body
	for i := len(hostedIndexes) - 1; i >= 0; i-- {
		next, err := sjson.DeleteBytes(out, "tools."+strconv.Itoa(hostedIndexes[i]))
		if err != nil {
			// A rewrite that half-succeeded would be worse than none: leave the
			// original body alone and let the request go out as it came in.
			return body, false
		}
		out = next
	}

	// An empty tools array is not the same as no tools to every provider, and the
	// request never asked for one — drop the key when nothing is left.
	if remaining := gjson.GetBytes(out, "tools"); remaining.IsArray() && len(remaining.Array()) == 0 {
		if next, err := sjson.DeleteBytes(out, "tools"); err == nil {
			out = next
		}
	}

	return out, true
}
