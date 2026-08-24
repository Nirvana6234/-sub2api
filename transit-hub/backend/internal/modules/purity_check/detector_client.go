package purity_check

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DetectorClient 封装旁路检测器的 HTTP API。
//
// 鉴权：检测器进程启动时用 secrets.token_urlsafe(32) 随机生成一个 token，
// 客户端要先 GET /api/bootstrap 拿到它，之后每个请求带 X-GPT56-Session 头。
// 容器重启 token 就变了，所以这里缓存 token 并在收到 403 时自动重新 bootstrap。
//
// 另外检测器有一条 Origin 校验：只在请求带了 Origin 头时才要求 hostname 是
// 127.0.0.1/localhost。我们不发 Origin 头，天然绕开，不需要伪造任何东西。
type DetectorClient struct {
	baseURL string
	http    *http.Client

	mu    sync.Mutex
	token string
	// bootstrap 返回的 single_presets 原文，按档位缓存。
	// 【必须原样透传】：检测器用归一化配置的哈希与内置预设逐位比对来决定
	// official，自己拼一份哪怕只改 workers 都会掉成自定义档，结论降级。
	presets map[Tier]json.RawMessage
}

func NewDetectorClient(baseURL string) *DetectorClient {
	return &DetectorClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http: &http.Client{
			// 检测器是本地旁路服务，正常都在毫秒级；给 30s 是为了容忍它在
			// start 时同步做配置归一化和落盘。
			Timeout: 30 * time.Second,
		},
	}
}

// Configured 表示是否配置了检测器地址。没配时整个模块的接口都返回
// detectorUnavailable，而不是 panic 或空转。
func (c *DetectorClient) Configured() bool { return c != nil && c.baseURL != "" }

// ErrDetectorBusy 表示检测器已有会话在跑。worker 收到它要把任务放回队列，
// 而不是判失败——这是并发争抢的正常结果，不是检测本身出错。
var ErrDetectorBusy = errors.New("detector busy")

type detectorHTTPError struct {
	Status  int
	Message string
}

func (e *detectorHTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("detector http %d", e.Status)
	}
	return fmt.Sprintf("detector http %d: %s", e.Status, e.Message)
}

// StartRequest 是 POST /api/detector/start 的请求体。
// APIKey 是明文上游凭据：它只在这个结构体里短暂存在，序列化后直接发给检测器，
// 不进日志、不进数据库、不进任何响应。
type StartRequest struct {
	BaseURL         string            `json:"base_url"`
	ProxyURL        string            `json:"proxy_url,omitempty"`
	APIKey          string            `json:"api_key"`
	HeaderOverrides map[string]string `json:"header_overrides,omitempty"`
	ClaimedModel    string            `json:"claimed_model"`
	RequestModel    string            `json:"request_model"`
	Config          json.RawMessage   `json:"config"`
}

type StartResponse struct {
	Started    bool   `json:"started"`
	SessionID  string `json:"session_id"`
	Official   bool   `json:"official"`
	ConfigHash string `json:"config_hash"`
}

// StatusResponse 只声明我们真正要用的字段。检测器还会返回一堆恢复态相关的
// 字段，用不上就不解析，免得它加字段我们就得跟着改。
type StatusResponse struct {
	Status          string `json:"status"`
	SessionID       string `json:"session_id"`
	ReportAvailable bool   `json:"report_available"`
	Progress        *struct {
		Planned          int `json:"planned"`
		LogicalCompleted int `json:"logical_completed"`
		Successful       int `json:"successful"`
		Errors           int `json:"errors"`
		Cancelled        int `json:"cancelled"`
		Pending          int `json:"pending"`
		HTTPAttempts     int `json:"http_attempts"`
		InFlight         int `json:"in_flight"`
	} `json:"progress"`
}

// Terminal 判断是否已经跑完（不管成败）。检测器的状态枚举是
// idle/running/stopping/interrupted/complete/stopped/error。
func (s StatusResponse) Terminal() bool {
	switch s.Status {
	case "complete", "stopped", "error", "interrupted", "idle":
		return true
	}
	return false
}

type EstimateResponse struct {
	TotalRequests          int    `json:"total_requests"`
	Fixed32KRequests       int    `json:"fixed_32k_requests"`
	ApproximateInputTokens int    `json:"approximate_input_tokens_total"`
	EstimateDisclaimerCN   string `json:"estimate_disclaimer_cn"`
}

// Preset 返回指定档位的官方预设配置原文，必要时先 bootstrap。
func (c *DetectorClient) Preset(ctx context.Context, tier Tier) (json.RawMessage, error) {
	c.mu.Lock()
	cached, ok := c.presets[tier]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}
	if err := c.bootstrap(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	preset, ok := c.presets[tier]
	if !ok {
		return nil, fmt.Errorf("detector has no preset for tier %q", tier)
	}
	return preset, nil
}

func (c *DetectorClient) Start(ctx context.Context, req StartRequest) (StartResponse, error) {
	var out StartResponse
	err := c.do(ctx, http.MethodPost, "/api/detector/start", req, &out)
	if err != nil {
		var httpErr *detectorHTTPError
		// 检测器对「已有会话在跑」返回 400 + 中文错误串。它没有错误码，
		// 只能认这段文案；认不出来就当普通失败处理，不会误判成 busy。
		if errors.As(err, &httpErr) && httpErr.Status == http.StatusBadRequest &&
			strings.Contains(httpErr.Message, "检测正在运行或停止中") {
			return out, ErrDetectorBusy
		}
		return out, err
	}
	return out, nil
}

func (c *DetectorClient) Status(ctx context.Context) (StatusResponse, error) {
	var out StatusResponse
	err := c.do(ctx, http.MethodGet, "/api/detector/status", nil, &out)
	return out, err
}

// Report 返回报告 JSON 原文。不解析成结构体：检测器版本会增减字段，
// 存原文才不用跟着它改。
func (c *DetectorClient) Report(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, "/api/detector/report", nil, &out)
	return out, err
}

func (c *DetectorClient) Stop(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/detector/stop", map[string]any{}, nil)
}

// ResetSession 丢弃检测器里残留的会话，让下一次 start 从零开始。
//
// 【为什么需要它】检测器进程重启会把没跑完的会话标成 interrupted，而它的
// start 接口看到 interrupted 就自动尝试续跑，只要新任务的 config_hash /
// 申报型号 / 端点跟旧会话对不上就 400——文案还被它自己盖成
// 「本地运行发生未分类异常」。一次容器重启就能让所有账号的检测永久失败，
// 而且 vendor 的 stop 对非 running 会话什么都不做，自己爬不出来。
//
// 端点由我们的 serve.py 提供（不在 vendor 里）。老部署没有这个端点会返回
// 404，调用方按「重置不可用」处理即可，不该让整个任务失败。
func (c *DetectorClient) ResetSession(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/detector/reset-session", map[string]any{}, nil)
}

// StatusIsInterrupted 判断检测器是不是卡在「上次没跑完」的中断态。
func (s StatusResponse) StatusIsInterrupted() bool { return s.Status == "interrupted" }

func (c *DetectorClient) Estimate(ctx context.Context, tier Tier) (EstimateResponse, error) {
	var out EstimateResponse
	preset, err := c.Preset(ctx, tier)
	if err != nil {
		return out, err
	}
	body := map[string]json.RawMessage{"config": preset}
	err = c.do(ctx, http.MethodPost, "/api/detector/estimate", body, &out)
	return out, err
}

// Health 打健康检查。它不需要 token，用于「检测器是否可达」的探测。
func (c *DetectorClient) Health(ctx context.Context) error {
	if !c.Configured() {
		return errors.New("detector url not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return &detectorHTTPError{Status: resp.StatusCode}
	}
	return nil
}

// bootstrap 取 session token 与官方预设。
func (c *DetectorClient) bootstrap(ctx context.Context) error {
	if !c.Configured() {
		return errors.New("detector url not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/bootstrap", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &detectorHTTPError{Status: resp.StatusCode, Message: detectorErrorMessage(payload)}
	}

	var parsed struct {
		SessionToken  string                     `json:"session_token"`
		SinglePresets map[string]json.RawMessage `json:"single_presets"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return err
	}
	if strings.TrimSpace(parsed.SessionToken) == "" {
		return errors.New("detector bootstrap returned no session token")
	}

	presets := make(map[Tier]json.RawMessage, len(parsed.SinglePresets))
	for name, raw := range parsed.SinglePresets {
		presets[Tier(name)] = raw
	}

	c.mu.Lock()
	c.token = parsed.SessionToken
	c.presets = presets
	c.mu.Unlock()
	return nil
}

// do 发一次带 token 的请求；遇 403 说明检测器重启过，重新 bootstrap 后重试一次。
func (c *DetectorClient) do(ctx context.Context, method string, path string, body any, out any) error {
	if !c.Configured() {
		return errors.New("detector url not configured")
	}
	if c.currentToken() == "" {
		if err := c.bootstrap(ctx); err != nil {
			return err
		}
	}

	err := c.doOnce(ctx, method, path, body, out)
	var httpErr *detectorHTTPError
	if errors.As(err, &httpErr) && httpErr.Status == http.StatusForbidden {
		if bootErr := c.bootstrap(ctx); bootErr != nil {
			return bootErr
		}
		return c.doOnce(ctx, method, path, body, out)
	}
	return err
}

func (c *DetectorClient) doOnce(ctx context.Context, method string, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-GPT56-Session", c.currentToken())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// 刻意不设 Origin：检测器只在 Origin 存在时才校验它是不是本机。

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &detectorHTTPError{Status: resp.StatusCode, Message: detectorErrorMessage(payload)}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(payload, out)
}

func (c *DetectorClient) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// detectorErrorMessage 取检测器错误体里的 error 字段。取不到就返回截断后的原文，
// 便于排障——检测器已在源码层面对 Bearer / sk- 做过替换。
func detectorErrorMessage(payload []byte) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(payload, &parsed); err == nil && parsed.Error != "" {
		return parsed.Error
	}
	text := strings.TrimSpace(string(payload))
	if len(text) > 300 {
		return text[:300]
	}
	return text
}
