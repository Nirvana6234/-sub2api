package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

const (
	openAIFirstOutputStageMemoryLimit        = 64 * 1024
	openAIFirstOutputStageMaxBytes           = 8 * 1024 * 1024
	openAIFirstOutputScannerFramingAllowance = 64
	openAIFirstOutputGuardQueueSize          = 1
	openAIDefaultStreamQueueSize             = 16
)

var (
	errOpenAIFirstOutputStageLimit   = errors.New("openai first-output staging limit exceeded")
	errOpenAIFirstOutputScannerLimit = errors.New("openai pre-output scanner token limit exceeded")
)

type openAIFirstOutputStage struct {
	limit      int64
	size       int64
	memory     bytes.Buffer
	tempFile   *os.File
	tempPath   string
	createTemp func() (*os.File, error)
	removeFile func(string) error
	memoryOnly bool
	cleanupErr error
	closed     bool
}

func newOpenAIFirstOutputStage(limit int64) *openAIFirstOutputStage {
	if limit < 1 {
		limit = 1
	}
	return &openAIFirstOutputStage{
		limit:      limit,
		createTemp: func() (*os.File, error) { return os.CreateTemp("", "sub2api-openai-first-output-*") },
		removeFile: os.Remove,
		memoryOnly: runtime.GOOS == "windows",
	}
}

func newDefaultOpenAIFirstOutputStage() *openAIFirstOutputStage {
	return newOpenAIFirstOutputStage(openAIFirstOutputStageMaxBytes)
}

func openAIFirstOutputEventQueueSize(guardFirstOutput bool) int {
	if guardFirstOutput {
		return openAIFirstOutputGuardQueueSize
	}
	return openAIDefaultStreamQueueSize
}

func openAIFirstOutputDynamicScanLines(guardActive *atomic.Bool) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		advance, token, err = bufio.ScanLines(data, atEOF)
		if err != nil || guardActive == nil || !guardActive.Load() {
			return advance, token, err
		}
		limit := openAIFirstOutputStageMaxBytes + openAIFirstOutputScannerFramingAllowance
		if token != nil {
			if len(token) > limit {
				return 0, nil, errOpenAIFirstOutputScannerLimit
			}
			return advance, token, nil
		}
		// At the limit with no delimiter, another byte would necessarily exceed
		// the guarded token budget. Fail before Scanner grows toward MaxLineSize.
		if len(data) >= limit {
			return 0, nil, errOpenAIFirstOutputScannerLimit
		}
		return advance, token, nil
	}
}

func (s *openAIFirstOutputStage) Buffered() int64 {
	if s == nil {
		return 0
	}
	return s.size
}

func (s *openAIFirstOutputStage) WriteString(value string) (int, error) {
	if err := s.prepareWrite(len(value)); err != nil {
		return 0, err
	}
	var n int
	var err error
	if s.tempFile == nil {
		n, err = s.memory.WriteString(value)
	} else {
		n, err = io.WriteString(s.tempFile, value)
	}
	s.size += int64(n)
	if err != nil {
		return n, fmt.Errorf("write first-output stage: %w", err)
	}
	return n, nil
}

func (s *openAIFirstOutputStage) Write(p []byte) (int, error) {
	if err := s.prepareWrite(len(p)); err != nil {
		return 0, err
	}
	var n int
	var err error
	if s.tempFile == nil {
		n, err = s.memory.Write(p)
	} else {
		n, err = s.tempFile.Write(p)
	}
	s.size += int64(n)
	if err != nil {
		return n, fmt.Errorf("write first-output stage: %w", err)
	}
	return n, nil
}

func (s *openAIFirstOutputStage) prepareWrite(incoming int) error {
	if s == nil || s.closed {
		return os.ErrClosed
	}
	if int64(incoming) > s.limit-s.size {
		return fmt.Errorf("%w: buffered=%d incoming=%d limit=%d", errOpenAIFirstOutputStageLimit, s.size, incoming, s.limit)
	}
	if s.tempFile != nil || s.memoryOnly || s.size+int64(incoming) <= openAIFirstOutputStageMemoryLimit {
		return nil
	}
	file, err := s.createTemp()
	if err != nil {
		return fmt.Errorf("create first-output spool: %w", err)
	}
	path := file.Name()
	// Unlink before writing any request data. Unix keeps the file descriptor
	// readable, while crashes and SIGKILL cannot leave a named plaintext spool.
	if unlinkErr := s.removeFile(path); unlinkErr != nil {
		closeErr := file.Close()
		removeErr := s.removeFile(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		s.memoryOnly = true
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			s.tempPath = path
		}
		s.cleanupErr = errors.Join(
			s.cleanupErr,
			fmt.Errorf("unlink first-output spool before use: %w", unlinkErr),
			closeErr,
			removeErr,
		)
		return nil
	}
	if _, err := file.Write(s.memory.Bytes()); err != nil {
		_ = file.Close()
		return fmt.Errorf("initialize first-output spool: %w", err)
	}
	s.tempFile = file
	s.tempPath = path
	s.memory.Reset()
	return nil
}

func (s *openAIFirstOutputStage) CommitTo(dst io.Writer) error {
	if s == nil || s.closed {
		return os.ErrClosed
	}
	if s.tempFile == nil {
		if _, err := io.Copy(dst, bytes.NewReader(s.memory.Bytes())); err != nil {
			return err
		}
	} else {
		if _, err := s.tempFile.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seek first-output spool: %w", err)
		}
		if _, err := io.CopyN(dst, s.tempFile, s.size); err != nil {
			return err
		}
	}
	if err := s.Close(); err != nil {
		// Delivery succeeded. Preserve cleanup failures for the handler's deferred
		// cleanup/logging pass instead of turning committed bytes into a stream error.
		s.cleanupErr = errors.Join(s.cleanupErr, err)
	}
	return nil
}

func (s *openAIFirstOutputStage) Close() error {
	if s == nil {
		return nil
	}
	if s.closed && s.tempFile == nil && s.tempPath == "" && s.cleanupErr == nil {
		return nil
	}
	s.closed = true
	s.size = 0
	s.memory.Reset()
	closeErr := s.cleanupErr
	s.cleanupErr = nil
	if s.tempFile != nil {
		closeErr = errors.Join(closeErr, s.tempFile.Close())
		s.tempFile = nil
	}
	if s.tempPath != "" {
		removeErr := s.removeFile(s.tempPath)
		if removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			s.tempPath = ""
		} else {
			closeErr = errors.Join(closeErr, removeErr)
		}
	}
	return closeErr
}

func (s *OpenAIGatewayService) openAIFirstOutputTimeout(reasoningEffort string) time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds <= 0 {
		return 0
	}
	seconds := s.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds
	switch strings.ToLower(strings.TrimSpace(reasoningEffort)) {
	case "high", "xhigh", "max":
		if override := s.cfg.Gateway.OpenAIHighEffortFirstOutputTimeoutSeconds; override > 0 {
			seconds = override
		}
	}
	return time.Duration(seconds) * time.Second
}

// defaultOpenAIFirstOutputHardCapSeconds 是放弃一条迟迟不出首字的连接前等待的
// 默认上限。取 600 秒是为了明确压过正常慢请求的分布（实测成功请求首字 p99 约
// 56 秒、最慢 73.8 秒），确保碰到它的只可能是真死掉的连接。
const defaultOpenAIFirstOutputHardCapSeconds = 600

// openAIFirstOutputHardCap 返回防挂死硬上限。它永远不小于对应的首输出软时限，
// 否则软硬两道时限会倒挂，"只观测不截断"的语义就被悄悄破坏了。
func (s *OpenAIGatewayService) openAIFirstOutputHardCap(soft time.Duration) time.Duration {
	seconds := defaultOpenAIFirstOutputHardCapSeconds
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIFirstOutputHardCapSeconds > 0 {
		seconds = s.cfg.Gateway.OpenAIFirstOutputHardCapSeconds
	}
	hardCap := time.Duration(seconds) * time.Second
	if soft > 0 && hardCap < soft {
		return soft
	}
	return hardCap
}

// observeOpenAISlowFirstOutput 记录"这个请求偏慢但仍在正常进行"。
//
// 它刻意不产生任何失败语义：不写 ops 错误日志、不构造 failover、不影响账号健康。
// 慢请求的唯一后果就是慢。留这条日志是为了让"上游变慢"仍然看得见——以前这个
// 信息是靠把请求杀掉、记一条 504 才浮现的，代价是用户拿不到结果。
func (s *OpenAIGatewayService) observeOpenAISlowFirstOutput(
	account *Account,
	startTime time.Time,
	originalModel string,
	reasoningEffort string,
	soft time.Duration,
	phase string,
) {
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	logger.LegacyPrintf(
		"service.openai_gateway",
		"OpenAI first output slow (still waiting): account=%d model=%s effort=%s phase=%s elapsed=%s soft_limit=%s",
		accountID, originalModel, reasoningEffort, phase, time.Since(startTime), soft,
	)
}

// newOpenAIFirstOutputTimeoutError records the timeout as an upstream attempt
// and returns the failover error. proxyID/proxyName are supplied by the caller
// because the same deadline is enforced over HTTP and WebSocket transports,
// whose direct-route semantics differ (see opsUpstreamWSProxyAttribution).
func (s *OpenAIGatewayService) newOpenAIFirstOutputTimeoutError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	proxyID *int64,
	proxyName string,
	startTime time.Time,
	originalModel string,
	reasoningEffort string,
	timeout time.Duration,
	phase string,
	responseHeaders http.Header,
) *UpstreamFailoverError {
	elapsed := time.Since(startTime)
	logger.LegacyPrintf(
		"service.openai_gateway",
		"OpenAI first output timeout: account=%d model=%s effort=%s phase=%s elapsed=%s limit=%s",
		account.ID, originalModel, reasoningEffort, phase, elapsed, timeout,
	)
	requestID := strings.TrimSpace(responseHeaders.Get("x-request-id"))
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		ProxyID:   proxyID,
		ProxyName: proxyName,
		Platform:  account.Platform, AccountID: account.ID, AccountName: account.Name,
		UpstreamStatusCode: http.StatusGatewayTimeout, UpstreamRequestID: requestID,
		Kind: "first_output_timeout", Message: "OpenAI upstream produced no semantic output before the deadline",
		Detail: fmt.Sprintf("phase=%s elapsed_ms=%d timeout_ms=%d", phase, elapsed.Milliseconds(), timeout.Milliseconds()),
	})
	if s.rateLimitService != nil {
		s.rateLimitService.HandleStreamTimeout(ctx, account, originalModel)
	}
	return &UpstreamFailoverError{
		StatusCode:               http.StatusGatewayTimeout,
		ResponseBody:             []byte(`{"error":{"type":"first_output_timeout","message":"Upstream produced no output before the deadline"}}`),
		ResponseHeaders:          responseHeaders.Clone(),
		SafeToFailoverAfterWrite: true,
		// A first-output deadline measures provider/request latency. It does not
		// prove that this API key is unhealthy, so it must not feed the API-key
		// health breaker and turn a transient upstream stall into an account-wide
		// temporary scheduling block.
		RequestScopedTransient: true,
		Scope:                  GatewayFailureScopeProvider,
	}
}

type openAIFirstOutputHeaderGuard struct {
	cancel  context.CancelFunc
	release context.CancelFunc
	timer   *time.Timer
	fired   chan struct{}
	// hardTimer / hardFired 是防挂死的硬上限，只有它触发才真正取消上游请求。
	hardTimer *time.Timer
	hardFired atomic.Bool
	once      sync.Once
}

// newOpenAIFirstOutputHeaderGuard 安装两道时限，职责严格分开：
//
//   - softDeadline（首输出超时）到点只 close(fired)，**不取消上游请求**。
//     它表达的是"这个请求偏慢"，而慢不是错：请求已经发给上游、上游也还在
//     正常处理，没有任何理由把它杀掉再给用户回一个错误。到点后照常等下去，
//     调用方只把它当观测信号。
//   - hardDeadline 到点才 cancel。到这一步意味着"这条连接大概率已经死了"，
//     取消并交给换号链路才是对的。
//
// 这两者以前是同一个时限：30 秒到点即取消 + 换号，换号链路耗尽就给用户 502。
// 生产实测成功请求的首字 p90 在 35-38 秒、最慢的 73.8 秒也正常返回，于是大量
// 本来会成功的请求被我们自己掐死，用户侧表现为"账号明明能用却疯狂报 504"。
func newOpenAIFirstOutputHeaderGuard(
	ctx context.Context,
	release context.CancelFunc,
	softDeadline time.Time,
	hardDeadline time.Time,
) (context.Context, *openAIFirstOutputHeaderGuard) {
	guardedCtx, cancel := context.WithCancel(ctx)
	guard := &openAIFirstOutputHeaderGuard{cancel: cancel, release: release, fired: make(chan struct{})}
	soft := time.Until(softDeadline)
	if soft <= 0 {
		soft = time.Nanosecond
	}
	guard.timer = time.AfterFunc(soft, func() {
		close(guard.fired)
	})
	if hard := time.Until(hardDeadline); hard > 0 {
		guard.hardTimer = time.AfterFunc(hard, func() {
			guard.hardFired.Store(true)
			cancel()
		})
	}
	return guardedCtx, guard
}

// slowHeaderWait 报告是否越过了软时限（只用于观测与日志，不影响请求存活）。
func (g *openAIFirstOutputHeaderGuard) slowHeaderWait() bool {
	if g.timer.Stop() {
		return false
	}
	<-g.fired
	return true
}

// hardCapExceeded 报告是否越过了硬上限——只有它成立才代表"该放弃这条连接"。
func (g *openAIFirstOutputHeaderGuard) hardCapExceeded() bool {
	return g != nil && g.hardFired.Load()
}

func (g *openAIFirstOutputHeaderGuard) close() {
	g.once.Do(func() {
		g.timer.Stop()
		if g.hardTimer != nil {
			g.hardTimer.Stop()
		}
		g.cancel()
		g.release()
	})
}

type openAIRequestContextReadCloser struct {
	io.ReadCloser
	cleanup func()
	once    sync.Once
	err     error
}

func (r *openAIRequestContextReadCloser) Close() error {
	r.once.Do(func() {
		r.cleanup()
		r.err = r.ReadCloser.Close()
	})
	return r.err
}
