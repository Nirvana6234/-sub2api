package master

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// 心跳（设计 11.4）：每台从节点每 5 秒一次，连续 15 秒收不到标为离线（不再分配新用户和新 Key、发离线通知，额度等租约到期收回）。
// 主节点在内存里记每台最近一次心跳和近 1 分钟的速率，按分钟汇总写进 relay_node_minute_metrics；
// 负载（分配用，设计 10.1）= 近 1 分钟收发速率较大者 ÷ 带宽上限。

const (
	// 设计 13 的事件表里主节点自己产生的其余事件（WP13 的健康探测、域名、证书事件也在这里登记，由它们的实现发出）。
	EventNodeUnreachable       = "node_unreachable"
	EventNodeDegraded          = "node_degraded"
	EventNodeDNSMismatch       = "node_dns_mismatch"
	EventNodeCertFailed        = "node_cert_failed"
	EventNodeCertExpiring      = "node_cert_expiring"
	EventAllRelaysDownFallback = "all_relays_down_fallback"
	EventAllRelaysDownOutage   = "all_relays_down_outage"
	EventRelayRecovered        = "relay_recovered"
	EventMasterRatioChanged    = "master_ratio_changed"
	EventMasterCapReached      = "master_cap_reached"
	EventBillingBacklog        = "billing_backlog"
	EventNodeBlocked           = "node_blocked"
	EventNodeUnstable          = "node_unstable"
	EventNodeOverloaded        = "node_overloaded"
	EventNodeStaleVersion      = "node_stale_version"

	// EventNodeOffline、EventNodeOnline 是节点离线、恢复的通知事件（设计 13，WP15 接到飞书和邮件）。
	EventNodeOffline = "node_offline"
	EventNodeOnline  = "node_online"
	// EventNodeClockSkew：从节点与主节点的时钟偏差超过上限。
	EventNodeClockSkew = "node_clock_skew"
	// EventNodeSelectFlood：从节点的选号次数远超它自己报的请求数（刷选号 / 刷额度）。
	EventNodeSelectFlood = "node_select_flood"
	// EventNodeUnreleased：从节点的选号大量到定时清理才被放掉（选号后不上报释放）。
	EventNodeUnreleased = "node_selections_unreleased"
	// EventNodeVoucherShortfall：从节点写进本地队列的扣费记录迟迟没有入账（丢失 / 少报，设计 5.4）。
	EventNodeVoucherShortfall = "node_voucher_shortfall"

	// MaxClockSkew 是主从时钟偏差上限（设计第 19 节）：超过它从节点拒绝服务，主节点也告警。
	MaxClockSkew = 30 * time.Second

	rateWindow = time.Minute
)

// MinuteMetrics 是一台节点一分钟的汇总（relay_node_minute_metrics 的一行）。
type MinuteMetrics struct {
	NodeID            int64
	Minute            time.Time
	RxBps             int64 // 字节 / 秒（平均）
	TxBps             int64
	ClientConnections int
	InflightRequests  int
	Requests          int
	Errors            int
	ActiveUsers       int
	ActiveKeys        int
	ReservedMicros    int64
	BillingBacklog    int
	VouchersIssued    int
	VouchersConsumed  int
	CPUPercent        float32
	MemoryBytes       int64
	DiskFreeBytes     int64
}

// MetricsSink 保存分钟汇总（repository.RelayMetricsRepository）；nil 时不落库。
type MetricsSink interface {
	SaveMinutes(ctx context.Context, rows []MinuteMetrics) error
}

// NodeHealth 是管理页上一台节点的实时状态。
type NodeHealth struct {
	NodeID  int64                     `json:"node_id"`
	Online  bool                      `json:"online"`
	LastAt  *time.Time                `json:"last_heartbeat_at,omitempty"`
	Beat    *relayv1.HeartbeatRequest `json:"heartbeat,omitempty"`
	LoadPct float64                   `json:"load_percent"`
	// ClockSkewMs 是从节点本机时间减主节点时间（毫秒，粗略值：单向延迟没扣）。
	ClockSkewMs int64 `json:"clock_skew_ms"`
	// VoucherShortfall：宽限期之前写进从节点队列、至今没入账也不在队列里的扣费记录数。
	VoucherShortfall int `json:"voucher_shortfall"`
}

type rateSample struct {
	at     time.Time
	rx, tx uint64
}

type minuteAcc struct {
	minute           time.Time
	n                int
	rx, tx           uint64
	conns, inflight  int32
	requests, errors int32
	users, keys      int32
	reserved         int64
	backlog          int32
	cpu              float32
	mem, disk        uint64
	enqueuedAtStart  uint64
	consumedAtStart  uint64
}

type nodeBeat struct {
	last    *relayv1.HeartbeatRequest
	at      time.Time
	online  bool
	skew    time.Duration
	samples []rateSample
	acc     *minuteAcc
	// 扣费对账（本次启动以来）：节点报的写进本地队列的记录数、主节点自己入账的条数。
	enqueued, consumed uint64
	sessionStart       int64
	// 对账窗口点（每 10 秒一个，宽限期之前最近的那个做基准）。
	windows []recordWindow
	// 选号计数（每秒一个桶，近 1 分钟）和定时清理放掉的次数（近 10 分钟）。
	selects                                                   [60]selectBucket
	stale                                                     []time.Time
	skewAlerted, shortfallAlerted, floodAlerted, staleAlerted bool
	// 其余边沿事件：扣费队列积压过多、拒绝服务、压力持续超阈值、配置版本落后。
	backlogAlerted, blockedAlerted, overloadAlerted, versionAlerted bool
	overSince, versionBehindSince                                   time.Time
	shortfall                                                       int
}

type selectBucket struct {
	sec int64
	n   int32
}

// 扣费队列积压达到这么多条发 billing_backlog；压力超过阈值持续 overloadSustain 发 node_overloaded；
// 配置版本落后持续 versionBehindFor 发 node_stale_version（设计 13 的表，后两项默认关）。
const (
	billingBacklogAlert = 5000
	overloadSustain     = 5 * time.Minute
	versionBehindFor    = 10 * time.Minute
)

// 异常检测阈值：近 1 分钟选号次数超过 selectFloodFactor × 节点自己报的请求数 + selectFloodSlack 就告警（一个请求最多换号重试
// 十几次，正常不会到这个量）；近 10 分钟被定时清理放掉的选号达到 staleThreshold 次告警。
const (
	selectFloodFactor = 12
	selectFloodSlack  = 300
	staleWindow       = 10 * time.Minute
	staleThreshold    = 10
)

type recordWindow struct {
	at       time.Time
	enqueued uint64
}

// Heartbeats 记各节点的心跳、在线状态、近 1 分钟速率和凭证对账。并发安全。
type Heartbeats struct {
	now          func() time.Time
	offlineAfter func() time.Duration
	notifier     Notifier
	sink         MetricsSink
	bandwidths   func() map[int64]int
	threshold    func() float64
	bwCache      map[int64]int
	bwAt         time.Time

	mu    sync.Mutex
	nodes map[int64]*nodeBeat
	// pending 是已封口、等待写库的分钟汇总。
	pending []MinuteMetrics
}

// HeartbeatsOptions 配置心跳跟踪。
type HeartbeatsOptions struct {
	Now func() time.Time
	// OfflineAfter 返回离线判定时间（通用配置，默认 15 秒）。
	OfflineAfter func() time.Duration
	Notifier     Notifier
	Sink         MetricsSink
	// Bandwidths 返回各节点的带宽上限（Mbps，算负载用；周期任务里最多 30 秒取一次）；LoadThreshold 返回负载阈值（0~1）。
	Bandwidths    func() map[int64]int
	LoadThreshold func() float64
}

// NewHeartbeats 创建心跳跟踪。
func NewHeartbeats(o HeartbeatsOptions) *Heartbeats {
	h := &Heartbeats{now: o.Now, offlineAfter: o.OfflineAfter, notifier: o.Notifier, sink: o.Sink, bandwidths: o.Bandwidths, threshold: o.LoadThreshold, nodes: map[int64]*nodeBeat{}}
	if h.now == nil {
		h.now = time.Now
	}
	if h.offlineAfter == nil {
		h.offlineAfter = func() time.Duration { return 15 * time.Second }
	}
	return h
}

// recordGrace：写进本地队列的记录超过这么久还没入账、也不在从节点队列里才算丢失（正常情况下 1~2 秒内入账）。
const recordGrace = 10 * time.Minute

// shortfallThreshold：对账差额至少这么多条才告警（零星的在途不算）。
const shortfallThreshold = 20

// Record 记一次心跳。返回是否是离线后恢复。
func (h *Heartbeats) Record(ctx context.Context, nodeID int64, req *relayv1.HeartbeatRequest) {
	now := h.now()
	h.mu.Lock()
	n := h.nodes[nodeID]
	if n == nil {
		n = &nodeBeat{}
		h.nodes[nodeID] = n
	}
	wasOnline := n.online
	if n.sessionStart != req.GetStartedAtUnixMs() {
		// 从节点重启了：队列计数从头算，对账窗口作废。
		n.sessionStart, n.enqueued, n.consumed, n.windows, n.shortfall, n.shortfallAlerted = req.GetStartedAtUnixMs(), 0, 0, nil, 0, false
		n.acc = nil
	}
	n.enqueued = req.GetUsageRecordsEnqueuedTotal()
	n.last, n.at, n.online = req, now, true
	n.skew = time.UnixMilli(req.GetSentAtUnixMs()).Sub(now)
	n.samples = append(n.samples, rateSample{at: now, rx: req.GetRxBytesPerSec(), tx: req.GetTxBytesPerSec()})
	for len(n.samples) > 0 && now.Sub(n.samples[0].at) > rateWindow {
		n.samples = n.samples[1:]
	}
	h.accumulateLocked(nodeID, n, req, now)
	skewOver := n.skew > MaxClockSkew || n.skew < -MaxClockSkew
	alertSkew := skewOver && !n.skewAlerted
	n.skewAlerted = skewOver
	skew := n.skew
	var extra []Event
	backlog := int(req.GetBillingBacklog())
	if over := backlog >= billingBacklogAlert; over && !n.backlogAlerted {
		extra = append(extra, Event{Kind: EventBillingBacklog, Severity: SeverityWarning, NodeID: nodeID, Detail: map[string]any{"backlog": backlog}})
		n.backlogAlerted = true
	} else if !over {
		n.backlogAlerted = false
	}
	if reason := req.GetBlockedReason(); reason != "" && !n.blockedAlerted {
		extra = append(extra, Event{Kind: EventNodeBlocked, Severity: SeverityCritical, NodeID: nodeID, Detail: map[string]any{"reason": reason}})
		n.blockedAlerted = true
	} else if reason == "" {
		n.blockedAlerted = false
	}
	h.mu.Unlock()
	if h.notifier != nil {
		for _, e := range extra {
			h.notifier.Notify(ctx, e)
		}
	}

	if !wasOnline && h.notifier != nil {
		h.notifier.Notify(ctx, Event{Kind: EventNodeOnline, Severity: SeverityInfo, NodeID: nodeID})
	}
	if alertSkew && h.notifier != nil {
		h.notifier.Notify(ctx, Event{Kind: EventNodeClockSkew, Severity: SeverityWarning, NodeID: nodeID, Detail: map[string]any{"skew_ms": skew.Milliseconds()}})
	}
}

// accumulateLocked 把这次心跳并进当前分钟的汇总；分钟翻页时封口。
func (h *Heartbeats) accumulateLocked(nodeID int64, n *nodeBeat, req *relayv1.HeartbeatRequest, now time.Time) {
	minute := now.UTC().Truncate(time.Minute)
	if n.acc != nil && !n.acc.minute.Equal(minute) {
		h.pending = append(h.pending, n.acc.finish(nodeID, n))
		n.acc = nil
	}
	if n.acc == nil {
		n.acc = &minuteAcc{minute: minute, enqueuedAtStart: n.enqueued, consumedAtStart: n.consumed}
	}
	a := n.acc
	a.n++
	a.rx += req.GetRxBytesPerSec()
	a.tx += req.GetTxBytesPerSec()
	a.conns = max32(a.conns, req.GetClientConnections())
	a.inflight = max32(a.inflight, req.GetInflightRequests())
	a.requests, a.errors = req.GetRequests_1M(), req.GetErrors_1M()
	a.users, a.keys = req.GetActiveUsers(), req.GetActiveKeys()
	a.reserved, a.backlog = req.GetReservedTotalMicros(), req.GetBillingBacklog()
	a.cpu, a.mem, a.disk = req.GetCpuPercent(), req.GetMemoryBytes(), req.GetDiskFreeBytes()
}

func (a *minuteAcc) finish(nodeID int64, n *nodeBeat) MinuteMetrics {
	div := uint64(1)
	if a.n > 0 {
		div = uint64(a.n)
	}
	return MinuteMetrics{
		NodeID: nodeID, Minute: a.minute, RxBps: int64(a.rx / div), TxBps: int64(a.tx / div),
		ClientConnections: int(a.conns), InflightRequests: int(a.inflight), Requests: int(a.requests), Errors: int(a.errors),
		ActiveUsers: int(a.users), ActiveKeys: int(a.keys), ReservedMicros: a.reserved, BillingBacklog: int(a.backlog),
		VouchersIssued: int(n.enqueued - a.enqueuedAtStart), VouchersConsumed: int(n.consumed - a.consumedAtStart),
		CPUPercent: a.cpu, MemoryBytes: int64(a.mem), DiskFreeBytes: int64(a.disk),
	}
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// NoteSelect 数一次选号调用（申请频率异常检测）。
func (h *Heartbeats) NoteSelect(nodeID int64) {
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	n := h.nodes[nodeID]
	if n == nil {
		n = &nodeBeat{}
		h.nodes[nodeID] = n
	}
	b := &n.selects[now.Unix()%60]
	if b.sec != now.Unix() {
		*b = selectBucket{sec: now.Unix()}
	}
	b.n++
}

// NoteStale 记一次选号因为迟迟没有释放被定时清理放掉。
func (h *Heartbeats) NoteStale(nodeID int64) {
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	n := h.nodes[nodeID]
	if n == nil {
		n = &nodeBeat{}
		h.nodes[nodeID] = n
	}
	n.stale = append(n.stale, now)
}

// NoteSettled 数主节点从这台入账的记录条数（对账用，本次启动以来）。
func (h *Heartbeats) NoteSettled(nodeID int64, records int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := h.nodes[nodeID]
	if n == nil {
		n = &nodeBeat{}
		h.nodes[nodeID] = n
	}
	n.consumed += uint64(records)
}

// Online 报告节点现在是不是在线（最近一次心跳在判定时间之内）。
func (h *Heartbeats) Online(nodeID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := h.nodes[nodeID]
	return n != nil && n.online && h.now().Sub(n.at) <= h.offlineAfter()
}

// OnlineNodes 返回在线的节点 ID。
func (h *Heartbeats) OnlineNodes() []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []int64
	limit := h.offlineAfter()
	now := h.now()
	for id, n := range h.nodes {
		if n.online && n.last != nil && now.Sub(n.at) <= limit {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Load 返回节点的负载（设计 10.1）：近 1 分钟收发速率较大者 ÷ 带宽上限；没填带宽上限或没有心跳时为 0。
func (h *Heartbeats) Load(nodeID int64, bandwidthMbps int) float64 {
	if bandwidthMbps <= 0 {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	n := h.nodes[nodeID]
	if n == nil || len(n.samples) == 0 {
		return 0
	}
	var rx, tx uint64
	for _, s := range n.samples {
		rx += s.rx
		tx += s.tx
	}
	peak := rx
	if tx > peak {
		peak = tx
	}
	avgBytesPerSec := float64(peak) / float64(len(n.samples))
	return avgBytesPerSec * 8 / (float64(bandwidthMbps) * 1e6)
}

// ClockSkew 返回测得的主从时钟偏差（从节点减主节点）。
func (h *Heartbeats) ClockSkew(nodeID int64) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := h.nodes[nodeID]; n != nil {
		return n.skew
	}
	return 0
}

// Health 返回一台节点的实时状态（管理页）；bandwidthMbps 用来算负载。
func (h *Heartbeats) Health(nodeID int64, bandwidthMbps int) NodeHealth {
	out := NodeHealth{NodeID: nodeID}
	h.mu.Lock()
	n := h.nodes[nodeID]
	if n != nil && n.last != nil {
		at := n.at
		out.LastAt, out.Beat = &at, n.last
		out.Online = n.online && h.now().Sub(n.at) <= h.offlineAfter()
		out.ClockSkewMs = n.skew.Milliseconds()
		out.VoucherShortfall = n.shortfall
	}
	h.mu.Unlock()
	out.LoadPct = h.Load(nodeID, bandwidthMbps) * 100
	return out
}

// Forget 忘掉一台节点（停用后）。
func (h *Heartbeats) Forget(nodeID int64) {
	h.mu.Lock()
	delete(h.nodes, nodeID)
	h.mu.Unlock()
}

// Tick 做周期性的事：判定离线（发通知）、封口分钟汇总并写库、凭证对账。由 Run 每秒调一次，测试直接调。
func (h *Heartbeats) Tick(ctx context.Context) {
	now := h.now()
	var events []Event
	h.mu.Lock()
	limit := h.offlineAfter()
	for id, n := range h.nodes {
		if n.online && n.last != nil && now.Sub(n.at) > limit {
			n.online = false
			events = append(events, Event{Kind: EventNodeOffline, Severity: SeverityCritical, NodeID: id,
				Detail: map[string]any{"last_heartbeat_at": n.at, "offline_after_seconds": int(limit.Seconds())}})
		}
		if n.acc != nil && now.UTC().Truncate(time.Minute).After(n.acc.minute) {
			h.pending = append(h.pending, n.acc.finish(id, n))
			n.acc = nil
		}
		if e := h.reconcileLocked(id, n, now); e != nil {
			events = append(events, *e)
		}
		events = append(events, h.anomaliesLocked(id, n, now)...)
		events = append(events, h.loadAndVersionLocked(id, n, now)...)
	}
	rows := h.pending
	h.pending = nil
	h.mu.Unlock()

	if h.notifier != nil {
		for _, e := range events {
			h.notifier.Notify(ctx, e)
		}
	}
	if len(rows) > 0 && h.sink != nil {
		if err := h.sink.SaveMinutes(ctx, rows); err != nil {
			slog.Warn("relay node metrics could not be saved", "error", err)
		}
	}
}

// reconcileLocked 扣费对账（设计 5.4）：宽限期之前节点已经写进本地队列的记录，现在都该入账了；
// 入账数加上节点自己说还在队列里的，仍然少于它报的写入数，差额就是丢失或迟迟没发的记录（节点丢了、被改了、发送卡死）。
// 节点重启后计数从头算（队列里上一次启动留下的记录入账会让入账数偏大，只会漏报不会误报）。
func (h *Heartbeats) reconcileLocked(nodeID int64, n *nodeBeat, now time.Time) *Event {
	if n.last == nil {
		return nil
	}
	if len(n.windows) == 0 || now.Sub(n.windows[len(n.windows)-1].at) >= 10*time.Second {
		n.windows = append(n.windows, recordWindow{at: now, enqueued: n.enqueued})
	}
	var base *recordWindow
	keep := 0
	for i := range n.windows {
		if now.Sub(n.windows[i].at) >= recordGrace {
			base = &n.windows[i]
			keep = i
		}
	}
	if keep > 0 {
		n.windows = n.windows[keep:]
	}
	if base == nil {
		return nil
	}
	backlog := int(n.last.GetBillingBacklog())
	shortfall := int(base.enqueued) - int(n.consumed) - backlog
	if shortfall < 0 {
		shortfall = 0
	}
	n.shortfall = shortfall
	over := shortfall >= shortfallThreshold
	alert := over && !n.shortfallAlerted
	n.shortfallAlerted = over
	if !alert {
		return nil
	}
	return &Event{Kind: EventNodeVoucherShortfall, Severity: SeverityCritical, NodeID: nodeID,
		Detail: map[string]any{"enqueued": base.enqueued, "settled": n.consumed, "queued": backlog, "shortfall": shortfall}}
}

// anomaliesLocked 申请频率和选号后不上报的异常检测（设计第 13 节）：每种异常持续期间只告警一次。
func (h *Heartbeats) anomaliesLocked(nodeID int64, n *nodeBeat, now time.Time) []Event {
	var out []Event
	var selects int32
	for i := range n.selects {
		if b := n.selects[i]; b.sec != 0 && now.Unix()-b.sec < 60 {
			selects += b.n
		}
	}
	reported := int32(0)
	if n.last != nil {
		reported = n.last.GetRequests_1M()
	}
	flood := int(selects) > selectFloodFactor*int(reported)+selectFloodSlack
	if flood && !n.floodAlerted {
		out = append(out, Event{Kind: EventNodeSelectFlood, Severity: SeverityWarning, NodeID: nodeID,
			Detail: map[string]any{"selects_1m": selects, "reported_requests_1m": reported}})
	}
	n.floodAlerted = flood

	keep := n.stale[:0]
	for _, at := range n.stale {
		if now.Sub(at) < staleWindow {
			keep = append(keep, at)
		}
	}
	n.stale = keep
	unreleased := len(n.stale) >= staleThreshold
	if unreleased && !n.staleAlerted {
		out = append(out, Event{Kind: EventNodeUnreleased, Severity: SeverityWarning, NodeID: nodeID, Detail: map[string]any{"count_10m": len(n.stale)}})
	}
	n.staleAlerted = unreleased
	return out
}

// loadAndVersionLocked 压力持续超过阈值、配置版本持续落后（默认关的两类事件）：每种持续期间只告警一次。
func (h *Heartbeats) loadAndVersionLocked(nodeID int64, n *nodeBeat, now time.Time) []Event {
	var out []Event
	if h.bandwidths != nil && h.threshold != nil && n.online {
		if now.Sub(h.bwAt) >= 30*time.Second || h.bwCache == nil {
			h.bwCache, h.bwAt = h.bandwidths(), now
		}
		if bw := h.bwCache[nodeID]; bw > 0 && len(n.samples) > 0 {
			var peak uint64
			var rx, tx uint64
			for _, s := range n.samples {
				rx += s.rx
				tx += s.tx
			}
			peak = rx
			if tx > peak {
				peak = tx
			}
			load := float64(peak) / float64(len(n.samples)) * 8 / (float64(bw) * 1e6)
			if load >= h.threshold() {
				if n.overSince.IsZero() {
					n.overSince = now
				}
				if now.Sub(n.overSince) >= overloadSustain && !n.overloadAlerted {
					n.overloadAlerted = true
					out = append(out, Event{Kind: EventNodeOverloaded, Severity: SeverityWarning, NodeID: nodeID, Detail: map[string]any{"load_percent": int(load * 100)}})
				}
			} else {
				n.overSince, n.overloadAlerted = time.Time{}, false
			}
		}
	}
	if !n.versionBehindSince.IsZero() && now.Sub(n.versionBehindSince) >= versionBehindFor && !n.versionAlerted {
		n.versionAlerted = true
		out = append(out, Event{Kind: EventNodeStaleVersion, Severity: SeverityInfo, NodeID: nodeID, Detail: map[string]any{"config_version_behind_minutes": int(now.Sub(n.versionBehindSince).Minutes())}})
	}
	return out
}

// NoteConfigVersion 记节点报的配置版本和主节点给它的版本：持续对不上才算落后（刚推送、正在拉取的几秒不算）。
func (h *Heartbeats) NoteConfigVersion(nodeID int64, nodeVersion, masterVersion string) {
	if masterVersion == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	n := h.nodes[nodeID]
	if n == nil {
		return
	}
	if nodeVersion == masterVersion {
		n.versionBehindSince, n.versionAlerted = time.Time{}, false
		return
	}
	if n.versionBehindSince.IsZero() {
		n.versionBehindSince = h.now()
	}
}

// Shortfall 返回对账算出的少报凭证数（超过阈值的节点，主节点不再给它发额度，见 Quotas）。
func (h *Heartbeats) Shortfall(nodeID int64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := h.nodes[nodeID]; n != nil {
		return n.shortfall
	}
	return 0
}

// SuspectedUnderReporting 报告这台节点的少报是否超过阈值（疑似被篡改或故意不报）。
func (h *Heartbeats) SuspectedUnderReporting(nodeID int64) bool {
	return h.Shortfall(nodeID) >= shortfallThreshold
}

// Run 每秒做一次周期任务，直到 ctx 结束。
func (h *Heartbeats) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Tick(ctx)
		}
	}
}

// Heartbeat 实现 RelayControl.Heartbeat：节点按连接证书认定；回复带主节点时间。
func (c *Control) Heartbeat(ctx context.Context, req *relayv1.HeartbeatRequest) (*relayv1.HeartbeatResponse, error) {
	peer, ok := transport.PeerFromContext(ctx)
	if !ok || peer.Class != transport.PeerIssued {
		return nil, status.Error(codes.PermissionDenied, "heartbeat requires an issued certificate")
	}
	if c.heartbeats == nil {
		return &relayv1.HeartbeatResponse{MasterTimeUnixMs: time.Now().UnixMilli()}, nil
	}
	c.heartbeats.Record(ctx, peer.NodeID, req)
	if aware, ok := c.selector.(HeartbeatAware); ok && len(req.GetInflightSelectionIds()) > 0 {
		aware.NodeHeartbeat(peer.NodeID, req.GetInflightSelectionIds())
	}
	resp := &relayv1.HeartbeatResponse{MasterTimeUnixMs: time.Now().UnixMilli()}
	if c.heartbeatInfo != nil {
		resp.ConfigVersion, resp.Draining = c.heartbeatInfo(ctx, peer.NodeID)
		c.heartbeats.NoteConfigVersion(peer.NodeID, req.GetConfigVersion(), resp.ConfigVersion)
	}
	return resp, nil
}

// HeartbeatAware 是选号实现可以实现的接口：心跳带来这台节点进行中的选号 ID。
type HeartbeatAware interface {
	NodeHeartbeat(nodeID int64, selectionIDs []string)
}

// AttachHeartbeats 挂上心跳跟踪；info 返回给这台节点的配置版本和是否在排空（nil 时不带）。
func (c *Control) AttachHeartbeats(h *Heartbeats, info func(ctx context.Context, nodeID int64) (version string, draining bool)) {
	c.heartbeats, c.heartbeatInfo = h, info
}
