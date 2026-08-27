package connection_health

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ProbeTimeout = 10 * time.Second

const defaultProbePrompt = "hi"

const (
	chatGPTCodexResponsesURL = "https://chatgpt.com/backend-api/codex/responses"
	codexCLIVersion          = "0.144.1"
	codexCLIUserAgent        = "codex_cli_rs/0.144.1 (Ubuntu 22.4.0; x86_64) xterm-256color"
)

type ProbeRequest struct {
	BaseURL         string
	UpstreamKey     string
	ProviderFamily  string
	ModelName       string
	MaxTokens       int
	ProbePrompt     string
	AccountPlatform string
	AccountType     string
	Extra           map[string]any
	HeaderOverrides map[string]string
	// ProbeMode 控制探活方式：空值/"real_model" = 真实模型请求（消耗 token）；
	// "models_endpoint" = GET /v1/models 验证 key 有效性（不消耗 token）。
	ProbeMode string
	// RequireCompletedGeneration rejects acknowledgement-only responses such
	// as response.created. Automatic error recovery enables this flag because
	// clearing status=error requires proof that generation actually completed.
	RequireCompletedGeneration bool
}

type RealProbeRunner struct {
	client *http.Client
}

func NewRealProbeRunner() *RealProbeRunner {
	return &RealProbeRunner{client: &http.Client{Timeout: ProbeTimeout}}
}

func (r *RealProbeRunner) Probe(ctx context.Context, req ProbeRequest) ProbeOutcome {
	if req.ProbeMode == ProbeModeModelsEndpoint {
		return r.probeModelsEndpoint(ctx, req)
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1
	}
	prompt := strings.TrimSpace(req.ProbePrompt)
	if prompt == "" {
		prompt = defaultProbePrompt
	}

	outcome := r.probeOnce(ctx, req, prompt, maxTokens)

	// Responses API 探测失败时回退一次 chat/completions。
	//
	// 默认假设上游支持 /v1/responses，但很多第三方中转只实现了
	// /v1/chat/completions。不支持时的表现往往不是干脆的 404，而是挂住直到超时，
	// 于是被归类成 network_fluctuation——运营看到"网络波动"只会去查网络，永远
	// 查不到真因。更隐蔽的是两个端点都可用、只是 responses 明显更慢的上游：生产
	// 上有个中转 /v1/responses 要 10.35s、/v1/chat/completions 只要 2.96s，正好
	// 卡在 10s 探测超时线两侧，于是健康探测长期误判，而人工在 sub2api 里怎么测
	// 都是正常的。
	//
	// 回退只在真正可能是"端点不受支持"的失败上触发（超时/连接类错误、404、
	// 405、400），不覆盖 auth、限流、5xx——那些是上游真实状态，换端点重试既
	// 无意义又会放大对故障上游的压力。
	if shouldRetryWithChatCompletions(req, outcome) {
		if fallback := r.probeOnce(ctx, chatCompletionsProbeRequest(req), prompt, maxTokens); fallback.Result == ResultOK {
			return fallback
		}
	}
	return outcome
}

// probeOnce 执行一次探测请求，不做任何回退。
func (r *RealProbeRunner) probeOnce(ctx context.Context, req ProbeRequest, prompt string, maxTokens int) ProbeOutcome {
	httpReq, buildErr := buildProbeRequest(ctx, req, prompt, maxTokens)
	if buildErr != nil {
		return ProbeOutcome{Result: ResultInvalidResponse, Detail: redact(buildErr.Error(), req.UpstreamKey)}
	}

	started := time.Now()
	resp, err := r.client.Do(httpReq)
	latencyMs := int(time.Since(started).Milliseconds())
	if err != nil {
		return ProbeOutcome{Result: classifyTransportError(err), LatencyMs: latencyMs, Detail: redact(err.Error(), req.UpstreamKey)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return classifyHTTPResponse(resp.StatusCode, body, req.UpstreamKey, latencyMs, req.RequireCompletedGeneration)
}

// shouldRetryWithChatCompletions 判断本次失败是否值得换用 chat/completions 重试。
//
// 只针对「本来走了 /v1/responses 的 API Key 账号」。OAuth 账号排除在外：
// ChatGPT 后端只有 responses 端点，对它回退 chat/completions 必然失败，只会
// 白白多打一次请求。
func shouldRetryWithChatCompletions(req ProbeRequest, outcome ProbeOutcome) bool {
	platform := firstNonEmptyProbeString(req.AccountPlatform, req.ProviderFamily)
	if !isOpenAIAPIKeyProbe(platform, strings.TrimSpace(req.AccountType)) {
		return false
	}
	if !shouldUseResponsesAPI(req.Extra) {
		return false
	}
	switch outcome.Result {
	// ResultUnsupported 正是 404 且响应体未指向模型不存在的分类，语义就是
	// 「上游不认这个端点」，是最该回退的一种；ResultModelNotFound 也要回退——
	// 有些中转只在 chat/completions 上注册模型，responses 上会报模型不存在。
	case ResultNetworkFluctuation, ResultInvalidResponse, ResultModelNotFound, ResultUnsupported:
		return true
	default:
		return false
	}
}

// chatCompletionsProbeRequest 复制一份强制走 chat/completions 的请求描述。
// 通过 Extra 上的显式开关表达，与运营手工配置同一条路径，不新增第二套判定。
func chatCompletionsProbeRequest(req ProbeRequest) ProbeRequest {
	extra := make(map[string]any, len(req.Extra)+1)
	for key, value := range req.Extra {
		extra[key] = value
	}
	extra["openai_responses_mode"] = "force_chat_completions"
	req.Extra = extra
	return req
}

// probeModelsEndpoint 通过 GET /v1/models 验证 key 有效性和端点可达性，不消耗任何 token。
// 适用于只需要判断"key 是否仍然存活"而不需要 TTFT 数据的场景。
func (r *RealProbeRunner) probeModelsEndpoint(ctx context.Context, req ProbeRequest) ProbeOutcome {
	endpoint := buildOpenAIEndpointURL(req.BaseURL, "/v1/models")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ProbeOutcome{Result: ResultInvalidResponse, Detail: redact(err.Error(), req.UpstreamKey)}
	}
	httpReq.Header.Set("Authorization", "Bearer "+req.UpstreamKey)
	httpReq.Header.Set("Accept", "application/json")
	applyHeaderOverrides(httpReq.Header, req.HeaderOverrides)

	started := time.Now()
	resp, err := r.client.Do(httpReq)
	latencyMs := int(time.Since(started).Milliseconds())
	if err != nil {
		return ProbeOutcome{Result: classifyTransportError(err), LatencyMs: latencyMs, Detail: redact(err.Error(), req.UpstreamKey)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return classifyModelsEndpointResponse(resp.StatusCode, body, req.UpstreamKey, latencyMs)
}

// classifyModelsEndpointResponse 判断 GET /v1/models 的响应：
// 2xx + 合法 JSON → key 有效；401/403 → key 失效；429 → 限流；5xx → 服务故障。
func classifyModelsEndpointResponse(status int, body []byte, upstreamKey string, latencyMs int) ProbeOutcome {
	detail := redact(truncate(string(body), 500), upstreamKey)
	switch {
	case status == http.StatusOK || status == http.StatusCreated:
		if json.Valid(bytes.TrimSpace(body)) {
			return ProbeOutcome{Result: ResultOK, LatencyMs: latencyMs}
		}
		return ProbeOutcome{Result: ResultInvalidResponse, LatencyMs: latencyMs, Detail: detail}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ProbeOutcome{Result: ResultAuth, LatencyMs: latencyMs, Detail: detail}
	case status == http.StatusTooManyRequests:
		return ProbeOutcome{Result: ResultRateLimited, LatencyMs: latencyMs, Detail: detail}
	case status >= 500:
		return ProbeOutcome{Result: ResultServerError, LatencyMs: latencyMs, Detail: detail}
	default:
		return ProbeOutcome{Result: ResultInvalidResponse, LatencyMs: latencyMs, Detail: detail}
	}
}

func buildProbeRequest(ctx context.Context, req ProbeRequest, prompt string, maxTokens int) (*http.Request, error) {
	model := strings.TrimSpace(req.ModelName)
	if model == "" {
		model = defaultModelForProvider(req.ProviderFamily)
	}

	platform := firstNonEmptyProbeString(req.AccountPlatform, req.ProviderFamily)
	accountType := strings.TrimSpace(req.AccountType)

	var endpoint string
	var payload map[string]any
	headers := map[string]string{"Authorization": "Bearer " + req.UpstreamKey}

	switch {
	case isOpenAIOAuthProbe(platform, accountType):
		endpoint = buildOpenAIOAuthResponsesEndpoint(req.BaseURL)
		payload = buildResponsesProbePayload(model, prompt, maxTokens, true)
		headers["Accept"] = "text/event-stream"
		addCodexHeaders(headers)
	case isOpenAIAPIKeyProbe(platform, accountType) && shouldUseResponsesAPI(req.Extra):
		endpoint = buildOpenAIEndpointURL(defaultStringValue(req.BaseURL, "https://api.openai.com"), "/v1/responses")
		payload = buildResponsesProbePayload(model, prompt, maxTokens, false)
		headers["Accept"] = "text/event-stream"
		addCodexHeaders(headers)
		headers["X-Codex-Window-ID"] = fmt.Sprintf("probe-%d", time.Now().UnixNano())
	default:
		endpoint = buildOpenAIEndpointURL(req.BaseURL, "/v1/chat/completions")
		payload = map[string]any{
			"model":      model,
			"max_tokens": maxTokens,
			"messages":   []map[string]any{{"role": "user", "content": prompt}},
			"stream":     true,
		}
		headers["Accept"] = "text/event-stream"
	}

	httpReq, err := newJSONRequest(ctx, http.MethodPost, endpoint, payload, headers)
	if err != nil {
		return nil, err
	}
	applyHeaderOverrides(httpReq.Header, req.HeaderOverrides)
	return httpReq, nil
}

func buildResponsesProbePayload(model string, prompt string, maxTokens int, oauth bool) map[string]any {
	payload := map[string]any{
		"model":             model,
		"max_output_tokens": maxTokens,
		"input": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": prompt,
			}},
		}},
		"stream": true,
	}
	if oauth {
		payload["store"] = false
	}
	return payload
}

func addCodexHeaders(headers map[string]string) {
	headers["OpenAI-Beta"] = "responses=experimental"
	headers["Originator"] = "codex_cli_rs"
	headers["User-Agent"] = codexCLIUserAgent
	headers["Version"] = codexCLIVersion
}

func defaultModelForProvider(providerFamily string) string {
	switch providerFamily {
	case ProviderGemini:
		return "gemini-1.5-flash"
	case ProviderAnthropic:
		return "claude-3-haiku-20240307"
	default:
		return "gpt-4o-mini"
	}
}

func newJSONRequest(ctx context.Context, method string, endpoint string, payload any, headers map[string]string) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

func classifyTransportError(err error) ResultKey {
	if errors.Is(err, context.DeadlineExceeded) {
		return ResultNetworkFluctuation
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return ResultNetworkFluctuation
	}
	return ResultNetworkFluctuation
}

func classifyHTTPResponse(status int, body []byte, upstreamKey string, latencyMs int, requireCompletedGeneration bool) ProbeOutcome {
	detail := redact(truncate(string(body), 500), upstreamKey)

	switch {
	case status == http.StatusOK || status == http.StatusCreated:
		successful := isSuccessfulProbeBody(body)
		if requireCompletedGeneration {
			successful = isCompletedGenerationBody(body)
		}
		if !successful {
			return ProbeOutcome{Result: ResultInvalidResponse, LatencyMs: latencyMs, Detail: detail}
		}
		return ProbeOutcome{Result: ResultOK, LatencyMs: latencyMs, Detail: ""}
	case status == http.StatusTooManyRequests:
		return ProbeOutcome{Result: ResultRateLimited, LatencyMs: latencyMs, Detail: detail}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ProbeOutcome{Result: ResultAuth, LatencyMs: latencyMs, Detail: detail}
	case status == http.StatusNotFound:
		if looksLikeModelNotFound(body) {
			return ProbeOutcome{Result: ResultModelNotFound, LatencyMs: latencyMs, Detail: detail}
		}
		return ProbeOutcome{Result: ResultUnsupported, LatencyMs: latencyMs, Detail: detail}
	case status >= 500:
		return ProbeOutcome{Result: ResultServerError, LatencyMs: latencyMs, Detail: detail}
	default:
		return ProbeOutcome{Result: ResultInvalidResponse, LatencyMs: latencyMs, Detail: detail}
	}
}

func isCompletedGenerationBody(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	if !json.Valid(trimmed) {
		return completedGenerationSSEBody(string(trimmed))
	}
	var item map[string]any
	if err := json.Unmarshal(trimmed, &item); err != nil {
		return false
	}
	if _, failed := item["error"]; failed {
		return false
	}
	if choices, ok := item["choices"].([]any); ok && len(choices) > 0 {
		return true
	}
	status, _ := item["status"].(string)
	eventType, _ := item["type"].(string)
	return strings.EqualFold(status, "completed") || eventType == "response.completed" || eventType == "response.done"
}

func completedGenerationSSEBody(body string) bool {
	seenChoice := false
	seenDone := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			seenDone = true
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(data), &item); err != nil {
			continue
		}
		switch eventType, _ := item["type"].(string); eventType {
		case "response.completed", "response.done":
			return true
		case "response.failed", "error":
			return false
		}
		if choices, ok := item["choices"].([]any); ok && len(choices) > 0 {
			seenChoice = true
		}
	}
	return seenChoice && seenDone
}

func isSuccessfulProbeBody(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	if json.Valid(trimmed) {
		return true
	}
	return successfulSSEProbeBody(string(trimmed))
}

func successfulSSEProbeBody(body string) bool {
	seenJSON := false
	seenDone := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			seenDone = true
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(data), &item); err != nil {
			continue
		}
		seenJSON = true
		switch eventType, _ := item["type"].(string); eventType {
		case "response.created", "response.completed", "response.done":
			// A Responses acknowledgement proves that the upstream accepted the probe.
			return true
		case "response.failed", "error":
			return false
		}
		if choices, ok := item["choices"].([]any); ok && len(choices) > 0 {
			seenDone = true
		}
	}
	return seenJSON && seenDone
}

func looksLikeModelNotFound(body []byte) bool {
	lower := strings.ToLower(string(body))
	if lower == "" {
		return false
	}
	modelSignals := []string{"model_not_found", "model not found", "model_not_exist", "no such model", "does not exist", "not found model"}
	for _, signal := range modelSignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	pathSignals := []string{"route not found", "path not found", "endpoint not found", "cannot post", "not found: /", "404 page not found"}
	for _, signal := range pathSignals {
		if strings.Contains(lower, signal) {
			return false
		}
	}
	return false
}

func isOpenAIOAuthProbe(platform string, accountType string) bool {
	return strings.EqualFold(strings.TrimSpace(platform), "openai") &&
		(strings.EqualFold(strings.TrimSpace(accountType), "oauth") || strings.EqualFold(strings.TrimSpace(accountType), "setup-token"))
}

func isOpenAIAPIKeyProbe(platform string, accountType string) bool {
	return strings.EqualFold(strings.TrimSpace(platform), "openai") &&
		(strings.EqualFold(strings.TrimSpace(accountType), "apikey") || strings.EqualFold(strings.TrimSpace(accountType), "api_key"))
}

func shouldUseResponsesAPI(extra map[string]any) bool {
	if extra == nil {
		return true
	}
	if mode, ok := extra["openai_responses_mode"].(string); ok {
		switch strings.TrimSpace(mode) {
		case "force_chat_completions":
			return false
		case "force_responses":
			return true
		}
	}
	if supported, ok := extra["openai_responses_supported"].(bool); ok {
		return supported
	}
	return true
}

func buildOpenAIOAuthResponsesEndpoint(base string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	if trimmed == "" {
		return chatGPTCodexResponsesURL
	}
	parsed, err := url.Parse(trimmed)
	if err == nil && strings.EqualFold(parsed.Host, "chatgpt.com") {
		return chatGPTCodexResponsesURL
	}
	if strings.HasSuffix(strings.TrimRight(parsedPath(trimmed), "/"), "/backend-api/codex/responses") ||
		strings.HasSuffix(strings.TrimRight(parsedPath(trimmed), "/"), "/responses") {
		return trimmed
	}
	return trimmed + "/backend-api/codex/responses"
}

func buildOpenAIEndpointURL(base string, endpoint string) string {
	normalized := strings.TrimSpace(base)
	endpoint = "/" + strings.TrimLeft(strings.TrimSpace(endpoint), "/")
	if normalized == "" {
		return endpoint
	}
	relative := strings.TrimPrefix(endpoint, "/v1")
	parsed, err := url.Parse(normalized)
	if err != nil {
		return strings.TrimRight(normalized, "/") + endpoint
	}
	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, endpoint) && !strings.HasSuffix(path, relative) {
		if openAIBaseURLHasVersionSuffix(path) {
			path += relative
		} else {
			path += endpoint
		}
	}
	parsed.Path = path
	parsed.RawPath = ""
	parsed.Fragment = ""
	return parsed.String()
}

func openAIBaseURLHasVersionSuffix(raw string) bool {
	segment := strings.TrimRight(strings.TrimSpace(raw), "/")
	if i := strings.LastIndex(segment, "/"); i >= 0 {
		segment = segment[i+1:]
	}
	segment = strings.ToLower(segment)
	if len(segment) < 2 || segment[0] != 'v' || segment[1] < '0' || segment[1] > '9' {
		return false
	}
	for i := 1; i < len(segment); i++ {
		if segment[i] >= '0' && segment[i] <= '9' {
			continue
		}
		return strings.HasPrefix(segment[i:], "alpha") || strings.HasPrefix(segment[i:], "beta") || strings.HasPrefix(segment[i:], "preview") || segment[i] == '.'
	}
	return true
}

func parsedPath(raw string) string {
	if parsed, err := url.Parse(raw); err == nil {
		return parsed.Path
	}
	return raw
}

func applyHeaderOverrides(headers http.Header, overrides map[string]string) {
	for name, value := range overrides {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		for existing := range headers {
			if strings.EqualFold(existing, name) {
				delete(headers, existing)
			}
		}
		headers.Set(name, value)
	}
}

func firstNonEmptyProbeString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func defaultStringValue(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func redact(s string, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
