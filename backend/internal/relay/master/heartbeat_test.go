package master_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/stretchr/testify/require"
)

type captureNotifier struct {
	mu     sync.Mutex
	events []master.Event
}

func (n *captureNotifier) Notify(_ context.Context, e master.Event) {
	n.mu.Lock()
	n.events = append(n.events, e)
	n.mu.Unlock()
}

func (n *captureNotifier) kinds() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, e := range n.events {
		out = append(out, e.Kind)
	}
	return out
}

type captureSink struct {
	mu   sync.Mutex
	rows []master.MinuteMetrics
}

func (s *captureSink) SaveMinutes(_ context.Context, rows []master.MinuteMetrics) error {
	s.mu.Lock()
	s.rows = append(s.rows, rows...)
	s.mu.Unlock()
	return nil
}

type hbClock struct{ t time.Time }

func (c *hbClock) now() time.Time          { return c.t }
func (c *hbClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newHeartbeats(t *testing.T) (*master.Heartbeats, *hbClock, *captureNotifier, *captureSink) {
	t.Helper()
	clock := &hbClock{t: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	n, s := &captureNotifier{}, &captureSink{}
	h := master.NewHeartbeats(master.HeartbeatsOptions{Now: clock.now, Notifier: n, Sink: s})
	return h, clock, n, s
}

func beat(clock *hbClock, mutate func(*relayv1.HeartbeatRequest)) *relayv1.HeartbeatRequest {
	req := &relayv1.HeartbeatRequest{StartedAtUnixMs: 1000, SentAtUnixMs: clock.now().UnixMilli()}
	if mutate != nil {
		mutate(req)
	}
	return req
}

// 设计 11.4：连续 15 秒收不到心跳标为离线（发离线通知），再收到就恢复（发恢复通知）；离线的节点不在可分配的在线名单里。
func TestHeartbeatsMarkNodesOfflineAfterFifteenSeconds(t *testing.T) {
	ctx := context.Background()
	h, clock, notifier, _ := newHeartbeats(t)
	require.False(t, h.Online(7))

	h.Record(ctx, 7, beat(clock, nil))
	require.True(t, h.Online(7))
	require.Equal(t, []int64{7}, h.OnlineNodes())

	clock.advance(14 * time.Second)
	h.Tick(ctx)
	require.True(t, h.Online(7), "14 seconds is still fine")
	require.Equal(t, []string{master.EventNodeOnline}, notifier.kinds())

	clock.advance(2 * time.Second)
	h.Tick(ctx)
	require.False(t, h.Online(7))
	require.Empty(t, h.OnlineNodes())
	require.Equal(t, []string{master.EventNodeOnline, master.EventNodeOffline}, notifier.kinds())
	h.Tick(ctx)
	require.Len(t, notifier.kinds(), 2, "offline is announced once")

	h.Record(ctx, 7, beat(clock, nil))
	require.True(t, h.Online(7))
	require.Equal(t, master.EventNodeOnline, notifier.kinds()[2])
}

// 负载 = 近 1 分钟收发速率较大者 ÷ 带宽上限（设计 10.1）；没填带宽上限时为 0。
func TestHeartbeatsComputeLoadFromBandwidth(t *testing.T) {
	ctx := context.Background()
	h, clock, _, _ := newHeartbeats(t)
	// 100 Mbps 的节点，下行 6.25 MB/s = 50 Mbps，上行 2 MB/s。
	for i := 0; i < 4; i++ {
		h.Record(ctx, 3, beat(clock, func(r *relayv1.HeartbeatRequest) { r.RxBytesPerSec, r.TxBytesPerSec = 6_250_000, 2_000_000 }))
		clock.advance(5 * time.Second)
	}
	require.InDelta(t, 0.5, h.Load(3, 100), 0.001)
	require.Zero(t, h.Load(3, 0), "no bandwidth limit configured")
	require.Zero(t, h.Load(99, 100), "no heartbeat yet")

	// 1 分钟之前的样本不再算：之后几乎没有流量，负载很快降下来。
	clock.advance(2 * time.Minute)
	h.Record(ctx, 3, beat(clock, func(r *relayv1.HeartbeatRequest) { r.RxBytesPerSec = 125_000 }))
	require.InDelta(t, 0.01, h.Load(3, 100), 0.001)
	health := h.Health(3, 100)
	require.True(t, health.Online)
	require.InDelta(t, 1.0, health.LoadPct, 0.01)
}

// 主从时钟偏差超过 30 秒：告警一次（设计第 19 节）。
func TestHeartbeatsReportClockSkew(t *testing.T) {
	ctx := context.Background()
	h, clock, notifier, _ := newHeartbeats(t)
	h.Record(ctx, 5, &relayv1.HeartbeatRequest{StartedAtUnixMs: 1, SentAtUnixMs: clock.now().Add(time.Minute).UnixMilli()})
	require.Equal(t, time.Minute, h.ClockSkew(5))
	h.Record(ctx, 5, &relayv1.HeartbeatRequest{StartedAtUnixMs: 1, SentAtUnixMs: clock.now().Add(time.Minute).UnixMilli()})
	skewEvents := 0
	for _, k := range notifier.kinds() {
		if k == master.EventNodeClockSkew {
			skewEvents++
		}
	}
	require.Equal(t, 1, skewEvents, "announced once while it lasts")
	require.Equal(t, time.Minute.Milliseconds(), h.Health(5, 0).ClockSkewMs)
}

// 心跳按分钟汇总写库（relay_node_minute_metrics）：速率取平均、连接数和进行中的请求取最大、计数取分钟末的值。
func TestHeartbeatsSaveMinuteMetrics(t *testing.T) {
	ctx := context.Background()
	h, clock, _, sink := newHeartbeats(t)
	clock.t = time.Date(2026, 10, 6, 12, 0, 10, 0, time.UTC)
	h.Record(ctx, 4, beat(clock, func(r *relayv1.HeartbeatRequest) {
		r.RxBytesPerSec, r.TxBytesPerSec, r.ClientConnections, r.InflightRequests = 1000, 200, 5, 3
	}))
	clock.advance(20 * time.Second)
	h.Record(ctx, 4, beat(clock, func(r *relayv1.HeartbeatRequest) {
		r.RxBytesPerSec, r.TxBytesPerSec, r.ClientConnections, r.InflightRequests = 3000, 400, 2, 9
		r.Requests_1M, r.Errors_1M, r.ActiveUsers, r.ActiveKeys, r.BillingBacklog = 120, 4, 6, 8, 2
		r.CpuPercent, r.MemoryBytes, r.DiskFreeBytes, r.ReservedTotalMicros = 12.5, 1<<30, 5<<30, 300_000_000
	}))
	h.Tick(ctx)
	require.Empty(t, sink.rows, "the minute is still open")

	clock.advance(time.Minute)
	h.Tick(ctx)
	require.Len(t, sink.rows, 1)
	got := sink.rows[0]
	require.Equal(t, int64(4), got.NodeID)
	require.Equal(t, time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC), got.Minute)
	require.Equal(t, int64(2000), got.RxBps)
	require.Equal(t, int64(300), got.TxBps)
	require.Equal(t, 5, got.ClientConnections)
	require.Equal(t, 9, got.InflightRequests)
	require.Equal(t, 120, got.Requests)
	require.Equal(t, 4, got.Errors)
	require.Equal(t, 6, got.ActiveUsers)
	require.Equal(t, 8, got.ActiveKeys)
	require.Equal(t, 2, got.BillingBacklog)
	require.Equal(t, int64(300_000_000), got.ReservedMicros)
	require.Equal(t, int64(1<<30), got.MemoryBytes)

	// 同一分钟不会写第二次。
	clock.advance(time.Second)
	h.Tick(ctx)
	require.Len(t, sink.rows, 1)
}

// 扣费对账（设计 5.4）：宽限期之前写进从节点队列的记录，到现在没入账、也不在队列里的差额超过阈值就告警，
// 并且这台不再被主节点发选号和额度；入账补上后恢复；从节点重启后计数从头算。
func TestHeartbeatsDetectMissingUsageRecords(t *testing.T) {
	ctx := context.Background()
	h, clock, notifier, _ := newHeartbeats(t)
	enqueued := uint64(0)
	beatWith := func(backlog int32) {
		h.Record(ctx, 8, beat(clock, func(r *relayv1.HeartbeatRequest) { r.UsageRecordsEnqueuedTotal, r.BillingBacklog = enqueued, backlog }))
	}
	// 10 分钟里写了 100 条，入账 100 条：没有差额。
	for i := 0; i < 120; i++ {
		enqueued++
		if i%6 == 0 {
			h.NoteSettled(8, 5)
		}
		beatWith(0)
		clock.advance(5 * time.Second)
		h.Tick(ctx)
	}
	require.Zero(t, h.Shortfall(8))
	require.False(t, h.SuspectedUnderReporting(8))

	// 又写了 60 条，一条也没发出去、队列里也没有（丢了）：过了宽限期差额出现。
	enqueued += 60
	for i := 0; i < 130; i++ {
		beatWith(0)
		clock.advance(5 * time.Second)
		h.Tick(ctx)
	}
	require.GreaterOrEqual(t, h.Shortfall(8), 20)
	require.True(t, h.SuspectedUnderReporting(8))
	require.Contains(t, notifier.kinds(), master.EventNodeVoucherShortfall)
	require.Equal(t, h.Shortfall(8), h.Health(8, 0).VoucherShortfall)

	// 记录其实还在队列里（发送慢）：不算丢失。
	h2, clock2, notifier2, _ := newHeartbeats(t)
	for i := 0; i < 200; i++ {
		h2.Record(ctx, 9, beat(clock2, func(r *relayv1.HeartbeatRequest) { r.UsageRecordsEnqueuedTotal, r.BillingBacklog = 500, 500 }))
		clock2.advance(5 * time.Second)
		h2.Tick(ctx)
	}
	require.Zero(t, h2.Shortfall(9))
	require.NotContains(t, notifier2.kinds(), master.EventNodeVoucherShortfall)

	// 从节点重启（启动时间变了）：计数从头算，丢失标记清掉。
	h.Record(ctx, 8, &relayv1.HeartbeatRequest{StartedAtUnixMs: 99999, SentAtUnixMs: clock.now().UnixMilli()})
	require.Zero(t, h.Shortfall(8))
}

// 停用的节点不是掉线：忘掉它的心跳状态，不发离线通知。
func TestHeartbeatsForgetDisabledNodes(t *testing.T) {
	ctx := context.Background()
	h, clock, notifier, _ := newHeartbeats(t)
	h.Record(ctx, 6, beat(clock, nil))
	h.Forget(6)
	clock.advance(time.Minute)
	h.Tick(ctx)
	require.NotContains(t, notifier.kinds(), master.EventNodeOffline)
	require.False(t, h.Online(6))
}

// 异常检测（设计第 13 节）：选号次数远超节点自己报的请求数（申请频率），或选号大量到定时清理才被放掉（选号后不上报），各告警一次。
func TestHeartbeatsDetectSelectFloodAndUnreleasedSelections(t *testing.T) {
	ctx := context.Background()
	h, clock, notifier, _ := newHeartbeats(t)
	h.Record(ctx, 3, beat(clock, func(r *relayv1.HeartbeatRequest) { r.Requests_1M = 10 }))

	for i := 0; i < 400; i++ {
		h.NoteSelect(3)
	}
	h.Tick(ctx)
	require.NotContains(t, notifier.kinds(), master.EventNodeSelectFlood, "400 selects for 10 reported requests is within retries + slack")
	for i := 0; i < 100; i++ {
		h.NoteSelect(3)
	}
	h.Tick(ctx)
	require.Contains(t, notifier.kinds(), master.EventNodeSelectFlood, "500 > 12×10 + 300")
	before := len(notifier.kinds())
	h.Tick(ctx)
	require.Len(t, notifier.kinds(), before, "announced once while it lasts")

	// 一分钟后选号数滑出窗口，异常解除；再来一轮又会告警。
	clock.advance(2 * time.Minute)
	h.Tick(ctx)
	for i := 0; i < 500; i++ {
		h.NoteSelect(3)
	}
	h.Tick(ctx)
	count := 0
	for _, k := range notifier.kinds() {
		if k == master.EventNodeSelectFlood {
			count++
		}
	}
	require.Equal(t, 2, count)

	for i := 0; i < 9; i++ {
		h.NoteStale(3)
	}
	h.Tick(ctx)
	require.NotContains(t, notifier.kinds(), master.EventNodeUnreleased)
	h.NoteStale(3)
	h.Tick(ctx)
	require.Contains(t, notifier.kinds(), master.EventNodeUnreleased)
}
