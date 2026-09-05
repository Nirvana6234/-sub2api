package connection_health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// endpointRecorder 记录探测实际打到的路径，用来断言回退是否发生。
type endpointRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (e *endpointRecorder) record(path string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paths = append(e.paths, path)
}

func (e *endpointRecorder) seen() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.paths...)
}

func apiKeyProbeRequest(baseURL string) ProbeRequest {
	return ProbeRequest{
		BaseURL:         baseURL,
		UpstreamKey:     "k",
		AccountPlatform: "openai",
		AccountType:     "apikey",
		ModelName:       "gpt-5.6-terra",
		MaxTokens:       1,
	}
}

// 生产实况回归：某中转的 /v1/responses 慢到卡在探测超时线上，而
// /v1/chat/completions 正常。此前探测只打前者，于是长期被记成
// network_fluctuation（"网络波动"），运营在 sub2api 里怎么测都是好的。
func TestProbeFallsBackToChatCompletionsWhenResponsesTimesOut(t *testing.T) {
	recorder := &endpointRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r.URL.Path)
		if r.URL.Path == "/v1/responses" {
			// 模拟挂住：客户端超时先到。
			time.Sleep(400 * time.Millisecond)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	runner := &RealProbeRunner{client: &http.Client{Timeout: 150 * time.Millisecond}}
	outcome := runner.Probe(context.Background(), apiKeyProbeRequest(server.URL))

	if outcome.Result != ResultOK {
		t.Fatalf("expected fallback to succeed, got %s (%s)", outcome.Result, outcome.Detail)
	}
	paths := recorder.seen()
	if len(paths) != 2 || paths[0] != "/v1/responses" || paths[1] != "/v1/chat/completions" {
		t.Fatalf("expected responses then chat/completions, got %v", paths)
	}
}

// 上游明确不认这个端点（404）时同样回退。
func TestProbeFallsBackWhenResponsesNotFound(t *testing.T) {
	recorder := &endpointRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r.URL.Path)
		if r.URL.Path == "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	runner := &RealProbeRunner{client: &http.Client{Timeout: 3 * time.Second}}
	if outcome := runner.Probe(context.Background(), apiKeyProbeRequest(server.URL)); outcome.Result != ResultOK {
		t.Fatalf("expected fallback to succeed, got %s", outcome.Result)
	}
	if paths := recorder.seen(); len(paths) != 2 {
		t.Fatalf("expected exactly one fallback attempt, got %v", paths)
	}
}

// 401 是上游的真实状态，不是端点不受支持：换端点重试既无意义，又会对着
// 故障上游多打一倍请求。
func TestProbeDoesNotFallBackOnAuthFailure(t *testing.T) {
	recorder := &endpointRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r.URL.Path)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	runner := &RealProbeRunner{client: &http.Client{Timeout: 3 * time.Second}}
	if outcome := runner.Probe(context.Background(), apiKeyProbeRequest(server.URL)); outcome.Result != ResultAuth {
		t.Fatalf("expected auth result, got %s", outcome.Result)
	}
	if paths := recorder.seen(); len(paths) != 1 {
		t.Fatalf("auth failure must not trigger a fallback, got %v", paths)
	}
}

// 限流与 5xx 同理：上游正忙或故障，重试只会加剧。
func TestProbeDoesNotFallBackOnRateLimitOrServerError(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway} {
		recorder := &endpointRecorder{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder.record(r.URL.Path)
			w.WriteHeader(status)
		}))
		runner := &RealProbeRunner{client: &http.Client{Timeout: 3 * time.Second}}
		runner.Probe(context.Background(), apiKeyProbeRequest(server.URL))
		if paths := recorder.seen(); len(paths) != 1 {
			t.Fatalf("status %d must not trigger a fallback, got %v", status, paths)
		}
		server.Close()
	}
}

// 已经显式配置走 chat/completions 的账号只打一次，不存在回退。
func TestProbeDoesNotFallBackWhenAlreadyChatCompletions(t *testing.T) {
	recorder := &endpointRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	req := apiKeyProbeRequest(server.URL)
	req.Extra = map[string]any{"openai_responses_mode": "force_chat_completions"}

	runner := &RealProbeRunner{client: &http.Client{Timeout: 3 * time.Second}}
	runner.Probe(context.Background(), req)

	paths := recorder.seen()
	if len(paths) != 1 || paths[0] != "/v1/chat/completions" {
		t.Fatalf("expected a single chat/completions attempt, got %v", paths)
	}
}

// 回退失败时必须保留首次（responses）的结论，不能用回退的错误覆盖它，
// 否则错误信息会指向一个运营根本没配置的端点。
func TestProbeKeepsOriginalOutcomeWhenFallbackAlsoFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	runner := &RealProbeRunner{client: &http.Client{Timeout: 3 * time.Second}}
	outcome := runner.Probe(context.Background(), apiKeyProbeRequest(server.URL))

	// 404 且响应体未指向模型不存在 → ResultUnsupported，这就是首次探测的结论，
	// 必须原样保留：用回退端点的 502 覆盖它会让错误信息指向运营根本没配的端点。
	if outcome.Result != ResultUnsupported {
		t.Fatalf("expected the original responses outcome to survive, got %s", outcome.Result)
	}
}
