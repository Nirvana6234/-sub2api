package connection_health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbe_AllProviderFamiliesUseOpenAICompatibleGatewayEndpoint(t *testing.T) {
	providerFamilies := []string{ProviderGemini, ProviderAnthropic, ProviderOpenAI, ProviderCustom}

	for _, family := range providerFamilies {
		t.Run(family, func(t *testing.T) {
			var gotPath string
			var gotAuth string
			var gotMaxTokens float64

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				gotMaxTokens, _ = body["max_tokens"].(float64)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
			}))
			defer server.Close()

			runner := NewRealProbeRunner()
			outcome := runner.Probe(context.Background(), ProbeRequest{
				BaseURL: server.URL, UpstreamKey: "gateway-key", ProviderFamily: family, MaxTokens: 1,
			})
			if outcome.Result != ResultOK {
				t.Fatalf("expected ok, got %s (%s)", outcome.Result, outcome.Detail)
			}
			if gotPath != "/v1/chat/completions" {
				t.Fatalf("expected gateway-compatible path /v1/chat/completions, got %s", gotPath)
			}
			if gotAuth != "Bearer gateway-key" {
				t.Fatalf("expected Bearer auth with gateway key, got %q", gotAuth)
			}
			if gotMaxTokens != 1 {
				t.Fatalf("expected max_probe_tokens=1 to propagate, got %v", gotMaxTokens)
			}
		})
	}
}

func TestProbe_DefaultModelPerProviderWhenModelNameEmpty(t *testing.T) {
	cases := map[string]string{
		ProviderGemini:    "gemini-1.5-flash",
		ProviderAnthropic: "claude-3-haiku-20240307",
		ProviderOpenAI:    "gpt-4o-mini",
		ProviderCustom:    "gpt-4o-mini",
	}

	for family, wantModel := range cases {
		var gotModel string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotModel, _ = body["model"].(string)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		}))

		runner := NewRealProbeRunner()
		runner.Probe(context.Background(), ProbeRequest{BaseURL: server.URL, UpstreamKey: "k", ProviderFamily: family})
		server.Close()

		if gotModel != wantModel {
			t.Fatalf("provider=%s: expected default model %s, got %s", family, wantModel, gotModel)
		}
	}
}

func TestProbe_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL, UpstreamKey: "secret-key", ProviderFamily: ProviderAnthropic,
	})
	if outcome.Result != ResultRateLimited {
		t.Fatalf("expected rate_limited, got %s", outcome.Result)
	}
}

func TestProbe_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL, UpstreamKey: "secret-key", ProviderFamily: ProviderOpenAI,
	})
	if outcome.Result != ResultServerError {
		t.Fatalf("expected server_error, got %s", outcome.Result)
	}
}

func TestProbe_AuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL, UpstreamKey: "secret-key", ProviderFamily: ProviderOpenAI,
	})
	if outcome.Result != ResultAuth {
		t.Fatalf("expected auth, got %s", outcome.Result)
	}
}

func TestProbe_ModelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"No such model"}}`))
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL, UpstreamKey: "secret-key", ProviderFamily: ProviderGemini, ModelName: "does-not-exist",
	})
	if outcome.Result != ResultModelNotFound {
		t.Fatalf("expected model_not_found, got %s", outcome.Result)
	}
}

func TestProbe_InvalidResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL, UpstreamKey: "secret-key", ProviderFamily: ProviderOpenAI,
	})
	if outcome.Result != ResultInvalidResponse {
		t.Fatalf("expected invalid_response, got %s", outcome.Result)
	}
}

func TestProbe_TimeoutClassifiedAsNetworkFluctuation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	runner := &RealProbeRunner{client: &http.Client{Timeout: 20 * time.Millisecond}}
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL, UpstreamKey: "secret-key", ProviderFamily: ProviderOpenAI,
	})
	if outcome.Result != ResultNetworkFluctuation {
		t.Fatalf("expected network_fluctuation on timeout, got %s", outcome.Result)
	}
}

func TestProbe_KeyNeverLeaksIntoDetail(t *testing.T) {
	const secret = "sk-super-secret-upstream-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`error using key ` + secret))
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL, UpstreamKey: secret, ProviderFamily: ProviderAnthropic,
	})
	if strings.Contains(outcome.Detail, secret) {
		t.Fatalf("upstream key leaked into probe outcome detail: %s", outcome.Detail)
	}
}

func TestProbe_OpenAIOAuthUsesCodexResponsesEndpoint(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotAccept string
	var gotBeta string
	var gotOriginator string
	var gotUA string
	var body map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotBeta = r.Header.Get("OpenAI-Beta")
		gotOriginator = r.Header.Get("Originator")
		gotUA = r.Header.Get("User-Agent")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL, UpstreamKey: "oauth-token", ProviderFamily: ProviderOpenAI,
		AccountPlatform: "openai", AccountType: "oauth", ModelName: "gpt-5.6-sol",
	})
	if outcome.Result != ResultOK {
		t.Fatalf("expected ok, got %s (%s)", outcome.Result, outcome.Detail)
	}
	if gotPath != "/backend-api/codex/responses" {
		t.Fatalf("expected codex responses path, got %s", gotPath)
	}
	if gotAuth != "Bearer oauth-token" || gotAccept != "text/event-stream" || gotBeta != "responses=experimental" || gotOriginator != "codex_cli_rs" || !strings.Contains(gotUA, "codex_cli_rs/0.144.1") {
		t.Fatalf("unexpected headers auth=%q accept=%q beta=%q originator=%q ua=%q", gotAuth, gotAccept, gotBeta, gotOriginator, gotUA)
	}
	if _, hasMessages := body["messages"]; hasMessages {
		t.Fatalf("oauth responses probe must not send chat messages body: %+v", body)
	}
	if _, hasInput := body["input"]; !hasInput || body["store"] != false {
		t.Fatalf("expected responses input and store=false, got %+v", body)
	}
}

func TestProbe_OpenAIAPIKeyResponsesRouting(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL + "/v1", UpstreamKey: "api-key", ProviderFamily: ProviderOpenAI,
		AccountPlatform: "openai", AccountType: "apikey", ModelName: "gpt-5.6-sol",
	})
	if outcome.Result != ResultOK {
		t.Fatalf("expected ok, got %s (%s)", outcome.Result, outcome.Detail)
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("expected /v1/responses, got %s", gotPath)
	}
}

func TestProbe_OpenAIAPIKeyResponsesAcceptsCreatedEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("expected /v1/responses, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n"))
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL, UpstreamKey: "api-key", ProviderFamily: ProviderOpenAI,
		AccountPlatform: "openai", AccountType: "apikey", ModelName: "gpt-5.6-sol",
	})
	if outcome.Result != ResultOK {
		t.Fatalf("expected ok, got %s (%s)", outcome.Result, outcome.Detail)
	}
}

func TestProbe_OpenAIAPIKeyCanForceChatCompletions(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{
		BaseURL: server.URL, UpstreamKey: "api-key", ProviderFamily: ProviderOpenAI,
		AccountPlatform: "openai", AccountType: "apikey", ModelName: "gpt-5.6-sol",
		Extra: map[string]any{"openai_responses_supported": false},
	})
	if outcome.Result != ResultOK {
		t.Fatalf("expected ok, got %s (%s)", outcome.Result, outcome.Detail)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("expected /v1/chat/completions, got %s", gotPath)
	}
}

func TestProbe_PathNotFoundIsUnsupportedNotModelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"route not found"}`))
	}))
	defer server.Close()

	runner := NewRealProbeRunner()
	outcome := runner.Probe(context.Background(), ProbeRequest{BaseURL: server.URL, UpstreamKey: "secret-key", ProviderFamily: ProviderOpenAI})
	if outcome.Result != ResultUnsupported {
		t.Fatalf("expected unsupported for endpoint 404, got %s", outcome.Result)
	}
}
