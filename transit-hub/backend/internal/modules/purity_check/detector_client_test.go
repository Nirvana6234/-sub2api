package purity_check

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// 一份最小的 bootstrap 响应，preset 里带上真实检测器会有的 config_hash/official，
// 用来验证我们是原样透传而不是自己重拼。
const fakeBootstrap = `{
	"session_token": "token-1",
	"single_presets": {
		"low": {"mode":"single","workers":8,"retries":2,"preset":"low",
		        "config_hash":"hash-low","official":true},
		"medium": {"mode":"single","workers":8,"retries":2,"preset":"medium",
		           "config_hash":"hash-medium","official":true}
	}
}`

// TestStartSendsPresetVerbatim 锁住整个方案里最容易被"优化"掉的一条不变量：
//
// 检测器判定「官方档位」的规则是把归一化后的配置算哈希、跟内置预设逐位比对
// （presets.py 的 official_rule）。只要我们自己拼配置——哪怕只是把 workers
// 从 8 改成 4 想少触发点限流——哈希就对不上，official 掉成 false，报告的结论
// 从「强烈指向 X」降级成「仅供参考的匹配度」，整份检测就白跑了。
//
// 所以 start 请求里的 config 必须与 bootstrap 返回的 preset 逐字节一致。
func TestStartSendsPresetVerbatim(t *testing.T) {
	var receivedConfig json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/bootstrap":
			_, _ = w.Write([]byte(fakeBootstrap))
		case "/api/detector/start":
			var body struct {
				Config json.RawMessage `json:"config"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			receivedConfig = body.Config
			_, _ = w.Write([]byte(`{"started":true,"session_id":"s1","official":true,"config_hash":"hash-low"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewDetectorClient(server.URL)
	preset, err := client.Preset(context.Background(), TierLow)
	if err != nil {
		t.Fatalf("Preset: %v", err)
	}

	if _, err := client.Start(context.Background(), StartRequest{
		BaseURL: "https://upstream.example/v1", APIKey: "sk-test",
		ClaimedModel: ModelSol, RequestModel: ModelSol, Config: preset,
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var sent, expected map[string]any
	if err := json.Unmarshal(receivedConfig, &sent); err != nil {
		t.Fatalf("发出去的 config 不是合法 JSON: %v", err)
	}
	if err := json.Unmarshal(preset, &expected); err != nil {
		t.Fatalf("preset 不是合法 JSON: %v", err)
	}
	if len(sent) != len(expected) {
		t.Fatalf("config 字段数变了：发出 %d 个，预设 %d 个 —— 不能自己拼配置", len(sent), len(expected))
	}
	for key, want := range expected {
		if got := sent[key]; got != want {
			t.Errorf("config[%q] 被改动了：预设是 %v，发出去的是 %v", key, want, got)
		}
	}
}

// TestStartDetectsBusy 确认「检测器已有会话在跑」被识别成 ErrDetectorBusy，
// 而不是普通失败。worker 靠这个区分「该重排队」和「该判失败」。
func TestStartDetectsBusy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/bootstrap" {
			_, _ = w.Write([]byte(fakeBootstrap))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error": "检测正在运行或停止中，请等待当前会话结束"}`))
	}))
	defer server.Close()

	client := NewDetectorClient(server.URL)
	_, err := client.Start(context.Background(), StartRequest{
		BaseURL: "https://upstream.example/v1", APIKey: "sk", ClaimedModel: ModelSol,
		RequestModel: ModelSol, Config: json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrDetectorBusy) {
		t.Fatalf("期望 ErrDetectorBusy，实际 %v", err)
	}
}

// TestStartOtherBadRequestIsNotBusy 确认别的 400 不会被误判成 busy——
// 误判会让一个真正配置错误的任务无限重排队，永远占着队首。
func TestStartOtherBadRequestIsNotBusy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/bootstrap" {
			_, _ = w.Write([]byte(fakeBootstrap))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error": "invalid api base url"}`))
	}))
	defer server.Close()

	client := NewDetectorClient(server.URL)
	_, err := client.Start(context.Background(), StartRequest{Config: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("期望报错")
	}
	if errors.Is(err, ErrDetectorBusy) {
		t.Fatal("普通 400 被误判成 busy，会导致任务无限重排队")
	}
}

// TestReBootstrapsOn403 覆盖检测器容器重启的场景：token 换了，旧 token 收到 403，
// 客户端应该自动重新 bootstrap 并重试，而不是把任务判失败。
func TestReBootstrapsOn403(t *testing.T) {
	var bootstraps int32
	var currentToken atomic.Value
	currentToken.Store("token-old")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/bootstrap" {
			atomic.AddInt32(&bootstraps, 1)
			token := currentToken.Load().(string)
			_, _ = w.Write([]byte(`{"session_token":"` + token + `","single_presets":{}}`))
			return
		}
		if r.Header.Get("X-GPT56-Session") != currentToken.Load().(string) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"本地会话令牌无效，请刷新页面"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"idle","report_available":false}`))
	}))
	defer server.Close()

	client := NewDetectorClient(server.URL)
	if _, err := client.Status(context.Background()); err != nil {
		t.Fatalf("首次 Status: %v", err)
	}
	if got := atomic.LoadInt32(&bootstraps); got != 1 {
		t.Fatalf("首次应 bootstrap 一次，实际 %d", got)
	}

	// 模拟检测器重启：token 变了。
	currentToken.Store("token-new")
	if _, err := client.Status(context.Background()); err != nil {
		t.Fatalf("token 轮换后 Status 应自愈，实际 %v", err)
	}
	if got := atomic.LoadInt32(&bootstraps); got != 2 {
		t.Fatalf("403 后应重新 bootstrap，bootstrap 次数 %d", got)
	}
}

// TestSendsNoOriginHeader 确认我们不发 Origin 头。
//
// 检测器的 _require_token 只在 Origin 存在时才要求它是 127.0.0.1/localhost。
// 不发就天然合规——不需要伪造一个假 Origin 去骗它。这条测试防止以后有人
// "顺手"给 HTTP 客户端加上 Origin，把所有请求打成 403。
func TestSendsNoOriginHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			t.Errorf("不应发送 Origin 头，实际发了 %q", origin)
		}
		if r.URL.Path == "/api/bootstrap" {
			_, _ = w.Write([]byte(fakeBootstrap))
			return
		}
		_, _ = w.Write([]byte(`{"status":"idle"}`))
	}))
	defer server.Close()

	client := NewDetectorClient(server.URL)
	if _, err := client.Status(context.Background()); err != nil {
		t.Fatalf("Status: %v", err)
	}
}

// TestNotConfiguredFailsFast 确认没配检测器地址时立刻报错，而不是去连空地址超时。
func TestNotConfiguredFailsFast(t *testing.T) {
	client := NewDetectorClient("  ")
	if client.Configured() {
		t.Fatal("空地址不应算已配置")
	}
	if _, err := client.Status(context.Background()); err == nil {
		t.Fatal("未配置时 Status 应直接报错")
	}
}
