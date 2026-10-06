package nodegw

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

// Stats 收集从节点心跳要报的本机计数（设计 11.4）：连接数、进行中的请求、近 1 分钟请求和错误、活跃用户和 Key、
// 网卡实测速率、CPU、内存、磁盘，以及按（级别, 组件）、（错误类型, 状态码）汇总的条数。并发安全。
type Stats struct {
	now     func() time.Time
	started time.Time

	inflight atomic.Int32
	conns    atomic.Int32

	mu      sync.Mutex
	buckets [60]statBucket
	users   map[int64]time.Time
	keys    map[int64]time.Time
	// 上一次采样的网卡累计字节，用来算速率。
	lastNIC   nicSample
	lastLog   map[[2]string]int32
	lastError map[[2]string]int32
	curLog    map[[2]string]int32
	curError  map[[2]string]int32
	countFrom time.Time
}

type statBucket struct {
	sec      int64
	requests int32
	errors   int32
}

type nicSample struct {
	at     time.Time
	rx, tx uint64
	ok     bool
}

// activeWindow 是"活跃用户 / 活跃 Key"的统计窗口。
const activeWindow = 5 * time.Minute

// NewStats 创建收集器。
func NewStats() *Stats {
	return &Stats{now: time.Now, started: time.Now(), users: map[int64]time.Time{}, keys: map[int64]time.Time{},
		curLog: map[[2]string]int32{}, curError: map[[2]string]int32{}, countFrom: time.Now()}
}

// Started 返回进程启动时间。
func (s *Stats) Started() time.Time { return s.started }

// Middleware 数进行中的请求、近 1 分钟请求和错误（5xx）、活跃用户和 Key（鉴权通过之后）。
func (s *Stats) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.inflight.Add(1)
		defer s.inflight.Add(-1)
		c.Next()
		status := c.Writer.Status()
		now := s.now()
		s.mu.Lock()
		b := &s.buckets[now.Unix()%60]
		if b.sec != now.Unix() {
			*b = statBucket{sec: now.Unix()}
		}
		b.requests++
		if status >= http.StatusInternalServerError {
			b.errors++
		}
		if key, ok := middleware2.GetAPIKeyFromContext(c); ok && key != nil {
			s.keys[key.ID] = now
			if key.User != nil {
				s.users[key.User.ID] = now
			} else if key.UserID > 0 {
				s.users[key.UserID] = now
			}
		}
		s.mu.Unlock()
	}
}

// ConnState 是 http.Server.ConnState：数客户端连接（被升级走的 WebSocket 连接不再算，它们的流量在网卡速率里）。
func (s *Stats) ConnState(_ net.Conn, state http.ConnState) {
	switch state {
	case http.StateNew:
		s.conns.Add(1)
	case http.StateClosed, http.StateHijacked:
		s.conns.Add(-1)
	}
}

// NoteLog 记一条程序日志（级别 + 组件，每分钟汇总报给主节点用于告警，不含内容）。
func (s *Stats) NoteLog(level, component string) {
	s.mu.Lock()
	s.curLog[[2]string{level, component}]++
	s.mu.Unlock()
}

// NoteError 记一次请求错误（错误类型 + 状态码，每分钟汇总）。
func (s *Stats) NoteError(errType, status string) {
	s.mu.Lock()
	s.curError[[2]string{errType, status}]++
	s.mu.Unlock()
}

// recent 返回近 1 分钟的请求和错误数。
func (s *Stats) recent(now time.Time) (requests, errs int32) {
	for i := range s.buckets {
		if b := s.buckets[i]; now.Unix()-b.sec < 60 && b.sec != 0 {
			requests += b.requests
			errs += b.errors
		}
	}
	return requests, errs
}

// Snapshot 返回心跳里由 Stats 负责的那部分。
func (s *Stats) Snapshot(ctx context.Context, dataDir string) *relayv1.HeartbeatRequest {
	now := s.now()
	req := &relayv1.HeartbeatRequest{
		StartedAtUnixMs:   s.started.UnixMilli(),
		ClientConnections: max(s.conns.Load(), 0),
		InflightRequests:  s.inflight.Load(),
	}
	s.mu.Lock()
	req.Requests_1M, req.Errors_1M = s.recent(now)
	for id, at := range s.users {
		if now.Sub(at) > activeWindow {
			delete(s.users, id)
		}
	}
	for id, at := range s.keys {
		if now.Sub(at) > activeWindow {
			delete(s.keys, id)
		}
	}
	req.ActiveUsers, req.ActiveKeys = int32(len(s.users)), int32(len(s.keys))
	// 汇总的条数每分钟换一批：上一分钟的作为"近 1 分钟"报，当前分钟的继续攒。
	if now.Sub(s.countFrom) >= time.Minute {
		s.lastLog, s.lastError = s.curLog, s.curError
		s.curLog, s.curError = map[[2]string]int32{}, map[[2]string]int32{}
		s.countFrom = now
	}
	req.LogCounts, req.ErrorCounts = buckets(s.lastLog), buckets(s.lastError)
	prev := s.lastNIC
	s.mu.Unlock()

	if rx, tx, ok := nicTotals(ctx); ok {
		if prev.ok {
			if dt := now.Sub(prev.at).Seconds(); dt > 0 && rx >= prev.rx && tx >= prev.tx {
				req.RxBytesPerSec, req.TxBytesPerSec = uint64(float64(rx-prev.rx)/dt), uint64(float64(tx-prev.tx)/dt)
			}
		}
		s.mu.Lock()
		s.lastNIC = nicSample{at: now, rx: rx, tx: tx, ok: true}
		s.mu.Unlock()
	}
	if pct, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(pct) > 0 {
		req.CpuPercent = float32(pct[0])
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		req.MemoryBytes = vm.Used
	}
	if dataDir != "" {
		if u, err := disk.UsageWithContext(ctx, dataDir); err == nil {
			req.DiskFreeBytes = u.Free
		}
	}
	return req
}

func buckets(m map[[2]string]int32) []*relayv1.CountBucket {
	out := make([]*relayv1.CountBucket, 0, len(m))
	for k, n := range m {
		out = append(out, &relayv1.CountBucket{A: k[0], B: k[1], Count: n})
	}
	return out
}

// nicTotals 返回所有网卡累计收发字节。
func nicTotals(ctx context.Context) (rx, tx uint64, ok bool) {
	counters, err := gnet.IOCountersWithContext(ctx, false)
	if err != nil || len(counters) == 0 {
		return 0, 0, false
	}
	return counters[0].BytesRecv, counters[0].BytesSent, true
}
