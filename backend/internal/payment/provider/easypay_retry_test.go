package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// 上游 CDN 的坏节点会直接 RST 连接，单次成功率一度只有 13/20，而每次失败都会
// 变成用户看到的一次「支付渠道失效」。这里验证第一次 RST 之后会重试并最终成功。
func TestEasyPayPostRawRetriesOnConnectionReset(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			// 劫持连接直接断开，模拟坏节点的 RST：客户端会拿到传输层错误而非 HTTP 响应。
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("测试服务器不支持 Hijack，无法模拟连接重置")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("Hijack 失败: %v", err)
				return
			}
			if tcp, ok := conn.(*net.TCPConn); ok {
				// SetLinger(0) 让 Close 发 RST 而不是正常四次挥手
				_ = tcp.SetLinger(0)
			}
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"code":1,"trade_no":"T123","payurl":"https://example.com/pay"}`))
	}))
	defer srv.Close()

	e := &EasyPay{httpClient: &http.Client{Timeout: 5 * time.Second}}
	body, status, err := e.postRaw(context.Background(), srv.URL, map[string]string{"pid": "1"})
	if err != nil {
		t.Fatalf("重试后应当成功，实际失败: %v（尝试 %d 次）", err, atomic.LoadInt32(&attempts))
	}
	if status != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", status)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("应当共尝试 2 次（1 次失败 + 1 次重试），实际 %d 次", got)
	}
	if len(body) == 0 {
		t.Fatal("成功响应的 body 不应为空")
	}
}

// 上游返回 5xx 属于业务响应而不是传输失败，绝不能重试：重试可能对一笔已经被
// 受理的订单再下一单。
func TestEasyPayPostRawDoesNotRetryHTTPErrorStatus(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":0,"msg":"upstream busy"}`))
	}))
	defer srv.Close()

	e := &EasyPay{httpClient: &http.Client{Timeout: 5 * time.Second}}
	_, status, err := e.postRaw(context.Background(), srv.URL, map[string]string{"pid": "1"})
	if err != nil {
		t.Fatalf("HTTP 500 是正常响应，不应返回 error: %v", err)
	}
	if status != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d, 期望 500", status)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("业务错误不得重试，应只请求 1 次，实际 %d 次", got)
	}
}

// 全部尝试都失败时要如实返回错误，而不是吞掉变成成功。
func TestEasyPayPostRawGivesUpAfterMaxRetries(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	e := &EasyPay{httpClient: &http.Client{Timeout: 5 * time.Second}}
	_, _, err := e.postRaw(context.Background(), srv.URL, map[string]string{"pid": "1"})
	if err == nil {
		t.Fatal("全部尝试失败时必须返回 error")
	}
	if got := atomic.LoadInt32(&attempts); got != easypayMaxRetries+1 {
		t.Fatalf("应当尝试 %d 次，实际 %d 次", easypayMaxRetries+1, got)
	}
}

// context 取消后必须立刻停手，不能继续占用退避时间。
func TestEasyPayPostRawStopsOnCanceledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	e := &EasyPay{httpClient: &http.Client{Timeout: 5 * time.Second}}
	start := time.Now()
	_, _, err := e.postRaw(ctx, srv.URL, map[string]string{"pid": "1"})
	if err == nil {
		t.Fatal("context 已取消，应当返回 error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("取消后应立即返回，实际耗时 %v", elapsed)
	}
}

func TestIsRetryableEasypayTransportError(t *testing.T) {
	timeoutErr := &net.OpError{Op: "dial", Err: &timeoutError{}}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil 不重试", nil, false},
		{"连接重置要重试", errors.New("read tcp 1.2.3.4:80: connection reset by peer"), true},
		{"HTTP/2 INTERNAL_ERROR 要重试", errors.New("stream error: stream ID 1; INTERNAL_ERROR"), true},
		{"连接被拒要重试", errors.New("dial tcp 1.2.3.4:443: connection refused"), true},
		{"EOF 要重试", errors.New("unexpected EOF"), true},
		// 超时不重试：对端慢或不可达，再等一轮只会让用户多等十几秒
		{"超时不重试", timeoutErr, false},
		{"context 取消不重试", context.Canceled, false},
		{"context 超时不重试", context.DeadlineExceeded, false},
		{"未知错误不重试", errors.New("some parse failure"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableEasypayTransportError(tc.err); got != tc.want {
				t.Fatalf("isRetryableEasypayTransportError(%v) = %v, 期望 %v", tc.err, got, tc.want)
			}
		})
	}
}

type timeoutError struct{}

func (e *timeoutError) Error() string   { return "i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }
