package service

import (
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}

	// [DIAG] httptrace: 精确拆分出站请求的 DNS/连接/TLS/连接复用/首字节耗时，
	// 用于定位「透传但每条请求仍有 1s+ 固定开销」的问题。诊断用，定位后可移除。
	diagStart := time.Now()
	var getConnStart, gotConnAt, dnsStart, dnsDone, connectStart, connectDone, tlsStart, tlsDone, firstByteAt time.Time
	var reused bool
	trace := &httptrace.ClientTrace{
		GetConn:           func(string) { getConnStart = time.Now() },
		DNSStart:          func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:           func(httptrace.DNSDoneInfo) { dnsDone = time.Now() },
		ConnectStart:      func(string, string) { connectStart = time.Now() },
		ConnectDone:       func(string, string, error) { connectDone = time.Now() },
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { tlsDone = time.Now() },
		GotConn: func(info httptrace.GotConnInfo) {
			gotConnAt = time.Now()
			reused = info.Reused
		},
		GotFirstResponseByte: func() { firstByteAt = time.Now() },
	}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))

	resp, err := s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)

	total := time.Since(diagStart)
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	// pool_wait: 从 Transport.getConn 被调用到真正拿到一条连接（GotConn）为止的
	// 全部耗时——如果这段明显大于 dns+connect+tls 之和，说明是在等
	// MaxConnsPerHost（=account.Concurrency）名额，而不是握手本身慢。
	poolWaitElapsed := durationIfSet(getConnStart, gotConnAt)
	dnsElapsed := durationIfSet(dnsStart, dnsDone)
	connectElapsed := durationIfSet(connectStart, connectDone)
	tlsElapsed := durationIfSet(tlsStart, tlsDone)
	getConnElapsed := durationIfSet(diagStart, gotConnAt)
	ttfb := durationIfSet(diagStart, firstByteAt)
	logger.LegacyPrintf(
		"service.openai_gateway",
		"[DIAG] upstream httptrace: account=%d err=%v total=%s get_conn=%s pool_wait=%s dns=%s connect=%s tls=%s reused=%v ttfb=%s",
		accountID, err, total, getConnElapsed, poolWaitElapsed, dnsElapsed, connectElapsed, tlsElapsed, reused, ttfb,
	)

	return resp, err
}

func durationIfSet(start, end time.Time) time.Duration {
	if start.IsZero() || end.IsZero() {
		return 0
	}
	return end.Sub(start)
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	if useTLSFallback {
		return s.httpUpstream.DoWithTLS(
			request,
			proxyURL,
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}
