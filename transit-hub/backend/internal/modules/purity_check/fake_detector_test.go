package purity_check

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeDetectorBehaviour 描述假检测器的行为，用来构造 worker 要处理的各种局面。
type fakeDetectorBehaviour struct {
	// busy=true 时 start 返回检测器「已有会话在跑」的那个 400。
	busy bool
	// startError 非空时 start 返回这段 400 错误体（用于非 busy 的失败路径）。
	startError string
	// onStart 收到 start 请求体时回调，用于断言我们发了什么。
	onStart func(body map[string]any)
	// statusFor 每次 status 请求返回的 JSON。nil 时立刻返回 complete。
	statusFor func() string
	// report 是 /api/detector/report 的响应体。
	report string
}

// newFakeDetector 起一个实现了检测器 HTTP 契约的假服务。
// 契约细节（token 头、preset 结构、busy 的错误文案）都对齐真实检测器 v4.1.1，
// 是从本地实跑抓下来的，不是照文档猜的。
func newFakeDetector(t *testing.T, behaviour fakeDetectorBehaviour) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))

		case "/api/bootstrap":
			_, _ = w.Write([]byte(fakeBootstrap))

		case "/api/detector/start":
			if behaviour.onStart != nil {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				behaviour.onStart(body)
			}
			if behaviour.busy {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": "检测正在运行或停止中，请等待当前会话结束"}`))
				return
			}
			if behaviour.startError != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(behaviour.startError))
				return
			}
			_, _ = w.Write([]byte(`{"started":true,"session_id":"s1","official":true,"config_hash":"hash-low"}`))

		case "/api/detector/status":
			if behaviour.statusFor != nil {
				_, _ = w.Write([]byte(behaviour.statusFor()))
				return
			}
			_, _ = w.Write([]byte(`{"status":"complete","session_id":"s1","report_available":true}`))

		case "/api/detector/report":
			report := behaviour.report
			if report == "" {
				report = `{"overall_verdict":"通过","official":true}`
			}
			_, _ = w.Write([]byte(report))

		case "/api/detector/stop":
			_, _ = w.Write([]byte(`{"accepted":true,"stopping":true}`))

		case "/api/detector/estimate":
			_, _ = w.Write([]byte(`{"total_requests":19,"fixed_32k_requests":0,
				"approximate_input_tokens_total":857,
				"estimate_disclaimer_cn":"仅用于比较档位消耗，不是上游账单精确值。"}`))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}
