package master

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
)

// hostLoad 是主节点自己的转发负载（设计 10.5）：同时转发的请求数和网卡近一分钟的收发速率。
// 主节点转发有上限（并发数、带宽，通用配置可调），超过时回"服务繁忙"；小白端和新 Key 的分配把它当一个"节点"，
// 超过负载阈值时这次改给从节点（设计 10.8）。
type hostLoad struct {
	inflight atomic.Int64

	mu          sync.Mutex
	samples     []hostSample
	last        hostSample
	capNotified time.Time
}

type hostSample struct {
	at     time.Time
	rx, tx uint64
	rxRate float64
	txRate float64
}

const hostSampleInterval = 5 * time.Second

// run 每 5 秒采一次网卡累计字节，直到 ctx 结束。
func (h *hostLoad) run(ctx context.Context) {
	t := time.NewTicker(hostSampleInterval)
	defer t.Stop()
	for {
		h.sample(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (h *hostLoad) sample(ctx context.Context, now time.Time) {
	counters, err := gnet.IOCountersWithContext(ctx, false)
	if err != nil || len(counters) == 0 {
		return
	}
	h.record(now, counters[0].BytesRecv, counters[0].BytesSent)
}

func (h *hostLoad) record(now time.Time, rx, tx uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cur := hostSample{at: now, rx: rx, tx: tx}
	if !h.last.at.IsZero() {
		if dt := now.Sub(h.last.at).Seconds(); dt > 0 && rx >= h.last.rx && tx >= h.last.tx {
			cur.rxRate, cur.txRate = float64(rx-h.last.rx)/dt, float64(tx-h.last.tx)/dt
		}
	}
	h.last = cur
	h.samples = append(h.samples, cur)
	for len(h.samples) > 0 && now.Sub(h.samples[0].at) > time.Minute {
		h.samples = h.samples[1:]
	}
}

// peakRate 返回近 1 分钟收发速率较大者（字节 / 秒）。
func (h *hostLoad) peakRate() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.samples) == 0 {
		return 0
	}
	var rx, tx float64
	for _, s := range h.samples {
		rx += s.rxRate
		tx += s.txRate
	}
	if tx > rx {
		rx = tx
	}
	return rx / float64(len(h.samples))
}

// masterLoad 返回主节点转发负载（0~1，超过 1 也行）：并发占上限的比例和带宽占上限的比例，取较大者；没设上限的那项为 0。
func (r *Runtime) masterLoad() float64 {
	cfg := r.cachedGeneralConfig(context.Background()).WithDefaults()
	load := 0.0
	if cfg.MasterMaxConcurrent > 0 {
		load = float64(r.host.inflight.Load()) / float64(cfg.MasterMaxConcurrent)
	}
	if cfg.MasterMaxBandwidthMbps > 0 {
		if bw := r.host.peakRate() * 8 / (float64(cfg.MasterMaxBandwidthMbps) * 1e6); bw > load {
			load = bw
		}
	}
	return load
}

// noteMasterCapReached 主节点转发达到上限、开始拒绝请求（设计 13）：每分钟最多通知一次，不在请求路径上等通知发完。
func (r *Runtime) noteMasterCapReached(detail map[string]any) {
	if r.deps.Notifier == nil {
		return
	}
	now := r.now()
	r.host.mu.Lock()
	due := now.Sub(r.host.capNotified) >= time.Minute
	if due {
		r.host.capNotified = now
	}
	r.host.mu.Unlock()
	if due {
		go r.deps.Notifier.Notify(context.Background(), Event{Kind: EventMasterCapReached, Severity: SeverityCritical, Detail: detail})
	}
}

// NodeContext 提供通知内容里的节点信息（实现 relaynotify.ContextSource）。
func (r *Runtime) NodeContext(ctx context.Context, nodeID int64) (NodeContext, bool) {
	n, err := r.deps.Store.GetByID(ctx, nodeID)
	if err != nil {
		return NodeContext{}, false
	}
	out := NodeContext{Name: n.Name, IP: n.LastSeenIP}
	if out.IP == "" {
		out.IP = n.RegisteredIP
	}
	if rr, err := r.runningRelay(); err == nil {
		if rr.heartbeats != nil {
			h := rr.heartbeats.Health(nodeID, n.BandwidthLimitMbps)
			out.LastHeartbeat = h.LastAt
			if h.Beat != nil {
				out.ActiveKeys, out.Inflight = int(h.Beat.GetActiveKeys()), int(h.Beat.GetInflightRequests())
			}
		}
		if rr.users != nil {
			if counts, err := rr.users.store.CountByNode(ctx); err == nil {
				out.AssignedUsers = counts[nodeID]
			}
		}
	}
	return out, true
}

// AcquireMasterSlot 实现 middleware.RelayMasterGate：主节点转发占一个并发名额；超过上限（通用配置 master_max_concurrent，
// 带宽超过 master_max_bandwidth_mbps 时同样）返回 ok=false，调用方回"服务繁忙"。主从分流没在运行时不限。
func (r *Runtime) AcquireMasterSlot(ctx context.Context) (release func(), ok bool) {
	if _, err := r.runningRelay(); err != nil {
		return func() {}, true
	}
	cfg := r.cachedGeneralConfig(ctx).WithDefaults()
	if cfg.MasterMaxConcurrent > 0 && r.host.inflight.Load() >= int64(cfg.MasterMaxConcurrent) {
		r.noteMasterCapReached(map[string]any{"reason": "concurrency", "limit": cfg.MasterMaxConcurrent})
		return nil, false
	}
	if cfg.MasterMaxBandwidthMbps > 0 && r.host.peakRate()*8/(float64(cfg.MasterMaxBandwidthMbps)*1e6) >= 1 {
		r.noteMasterCapReached(map[string]any{"reason": "bandwidth", "limit_mbps": cfg.MasterMaxBandwidthMbps})
		return nil, false
	}
	r.host.inflight.Add(1)
	var once sync.Once
	return func() { once.Do(func() { r.host.inflight.Add(-1) }) }, true
}
