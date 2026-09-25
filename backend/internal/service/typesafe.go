package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// TypeSafe（Jev 意图判断模型）的转发。协议是 POST /v1/systemone，请求体
// {state, model, questions}，响应体 {model, answers, usage:{input_tokens, output_tokens}}，
// 与 OpenAI / Anthropic 都不兼容，所以不进两条网关的转发链路。
//
// 请求和响应里是用户的聊天文字：这里不记录、不审计、不写进运维错误详情。
// 上游错误体也不记——TypeSafe 的 422 会把出错的输入原样带回来。

// typeSafeMaxResponseBytes 是上游响应体的读取上限。正常响应只有几 KB。
const typeSafeMaxResponseBytes = 1 << 20

// DefaultTypeSafeModelIDs 是 typesafe 分组的默认模型列表。客户端固定用 jev-1.13.0，
// 版本下线时退回 jev-latest，两个都要在白名单和渠道定价里。
func DefaultTypeSafeModelIDs() []string {
	return []string{"jev-1.13.0", "jev-latest"}
}

// HasTypeSafePricing 报告模型在该分组下是否有显式配置的价格（分组或渠道定价，
// 按次或按 token 都算）。
//
// 只认显式定价，不认全局价格表：两条网关找不到价格时都按 0 元入账，全局表又会
// 按名字子串猜兜底价，这两种结果用在 Jev 上都不对。没有价格就不转发。
func (s *GatewayService) HasTypeSafePricing(ctx context.Context, model string, apiKey *APIKey) bool {
	if s == nil || apiKey == nil || strings.TrimSpace(model) == "" {
		return false
	}
	return s.resolveChannelPricing(ctx, model, apiKey) != nil
}

// TypeSafeClientError 表示上游的回答已经原样写给了客户端，不换号。
type TypeSafeClientError struct {
	StatusCode int
}

func (e *TypeSafeClientError) Error() string {
	return fmt.Sprintf("typesafe upstream returned status %d", e.StatusCode)
}

// typeSafeShouldFailover 报告上游状态码是否该换一个账号重试：凭据、额度、限流、过载
// 和服务端错误是账号或上游的问题；其余 4xx（400 Unknown model、422 格式错误）是请求
// 本身的问题，换号也一样，原样还给客户端。
func typeSafeShouldFailover(status int) bool {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusPaymentRequired,
		status == http.StatusForbidden, status == http.StatusTooManyRequests:
		return true
	case status >= 500:
		return true
	default:
		return false
	}
}

func typeSafeSystemOneURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/v1")
	return base + "/v1/systemone"
}

// ForwardTypeSafeSystemOne 把请求体原样发给账号的 /v1/systemone，只换 Authorization。
//
//   - 200：响应原样写给客户端，返回用量。
//   - 该换号的错误（见 typeSafeShouldFailover）和网络错误：不写响应，返回
//     *UpstreamFailoverError，由调用方换号。
//   - 其他错误：上游响应原样写给客户端，返回 *TypeSafeClientError。
func (s *GatewayService) ForwardTypeSafeSystemOne(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, errors.New("http upstream not configured")
	}
	if !account.IsTypeSafe() {
		return nil, errors.New("typesafe account required")
	}
	start := time.Now()
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	SetOpsUpstreamModel(c, model)

	token := strings.TrimSpace(account.GetCredential("api_key"))
	if token == "" {
		return nil, &UpstreamFailoverError{StatusCode: http.StatusUnauthorized, Reason: GatewayFailureReason("typesafe_missing_api_key")}
	}
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if baseURL == "" {
		baseURL = DefaultTypeSafeBaseURL
	}
	if s.cfg != nil {
		validated, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		baseURL = validated
	}

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, typeSafeSystemOneURL(baseURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build typesafe request: %w", err)
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+token)
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(upstreamReq.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			ProxyID:     opsUpstreamProxyID(account),
			ProxyName:   opsUpstreamProxyName(account),
			Platform:    account.Platform,
			AccountID:   account.ID,
			AccountName: account.Name,
			Kind:        "request_error",
			Message:     "typesafe upstream request failed",
		})
		return nil, &UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: GatewayFailureReason("typesafe_transport")}
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, typeSafeMaxResponseBytes))
	if readErr != nil {
		return nil, &UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: GatewayFailureReason("typesafe_read")}
	}

	if resp.StatusCode >= 400 {
		if typeSafeShouldFailover(resp.StatusCode) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				Kind:               "failover",
			})
			// 529 是 TypeSafe 整体过载，不是这个账号的问题；通用处理会给账号 10 分钟
			// 过载冷却，一次高峰就能把唯一的 Jev 账号停掉。只换号，不标记。
			if s.rateLimitService != nil && resp.StatusCode != 529 {
				s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, ResponseHeaders: resp.Header.Clone()}
		}
		writeTypeSafeResponse(c, resp.StatusCode, resp.Header, respBody)
		return nil, &TypeSafeClientError{StatusCode: resp.StatusCode}
	}

	writeTypeSafeResponse(c, resp.StatusCode, resp.Header, respBody)
	return &ForwardResult{
		RequestID:       firstNonEmptyString(resp.Header.Get("x-request-id"), resp.Header.Get("request-id")),
		UpstreamHeaders: resp.Header,
		Usage: ClaudeUsage{
			InputTokens:  int(gjson.GetBytes(respBody, "usage.input_tokens").Int()),
			OutputTokens: int(gjson.GetBytes(respBody, "usage.output_tokens").Int()),
		},
		Model:                 model,
		UpstreamResponseModel: strings.TrimSpace(gjson.GetBytes(respBody, "model").String()),
		Duration:              time.Since(start),
	}, nil
}

// writeTypeSafeResponse 原样写回上游的状态码和响应体，只带 Content-Type 和 Retry-After。
func writeTypeSafeResponse(c *gin.Context, status int, header http.Header, body []byte) {
	if c == nil || c.Writer.Written() {
		return
	}
	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Writer.Header().Set("Content-Type", contentType)
	if retryAfter := header.Get("Retry-After"); retryAfter != "" {
		c.Writer.Header().Set("Retry-After", retryAfter)
	}
	c.Writer.WriteHeader(status)
	_, _ = c.Writer.Write(body)
}

// DefaultTypeSafeTestModel 是「测试连接」默认用的模型：别名，永远指向当前版本。
const DefaultTypeSafeTestModel = "jev-latest"

// typeSafeTestBody 是「测试连接」的请求：一个 noul 问题，约 270 个输入 token。
// 只调 GET /v1/models 只能证明 key 有效，证明不了账号能正常计费。
func typeSafeTestBody(model string) []byte {
	body, _ := json.Marshal(map[string]any{
		"model": model,
		"state": map[string]any{"text": "Hello, this is a connection test."},
		"questions": map[string]any{
			"is_test": map[string]any{
				"type":         "noul",
				"instructions": "The text is a connection test.",
			},
		},
	})
	return body
}

// testTypeSafeAccountConnection 发一次最小的 /v1/systemone 请求，返回 200 且 answers
// 不为空才算通过。
func (s *AccountTestService) testTypeSafeAccountConnection(c *gin.Context, account *Account, modelID string) error {
	ctx := c.Request.Context()
	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = DefaultTypeSafeTestModel
	}
	testModelID = account.GetMappedModel(testModelID)

	token := strings.TrimSpace(account.GetCredential("api_key"))
	if token == "" {
		return s.sendErrorAndEnd(c, "No API key available")
	}
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if baseURL == "" {
		baseURL = DefaultTypeSafeBaseURL
	}
	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	s.sendEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, typeSafeSystemOneURL(normalizedBaseURL), bytes.NewReader(typeSafeTestBody(testModelID)))
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Request failed: %s", sanitizeUpstreamErrorMessage(err.Error())))
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, typeSafeMaxResponseBytes))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(gjson.GetBytes(body, "detail.message").String())
		if msg == "" {
			msg = strings.TrimSpace(gjson.GetBytes(body, "detail").String())
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return s.sendErrorAndEnd(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, msg))
	}
	answers := gjson.GetBytes(body, "answers")
	if !answers.IsObject() || len(answers.Map()) == 0 {
		return s.sendErrorAndEnd(c, "API returned no answers")
	}
	s.sendEvent(c, TestEvent{Type: "content", Text: fmt.Sprintf("System One OK: model %s, %d input tokens",
		gjson.GetBytes(body, "model").String(), gjson.GetBytes(body, "usage.input_tokens").Int())})
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}
