package node

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

// MaxClockSkew 是主从时钟偏差上限（设计第 19 节）：超过它从节点拒绝服务（签发的扣费凭证、额度到期都按时间判断）。
const MaxClockSkew = 30 * time.Second

// DefaultHeartbeatInterval 是心跳间隔（设计 11.4）。
const DefaultHeartbeatInterval = 5 * time.Second

// heartbeatTimeout 是一次心跳调用的最长时间。
const heartbeatTimeout = 5 * time.Second

// Heartbeater 每隔一个间隔向主节点发一次心跳，同时测主从时钟偏差、记下主节点说这台是否在排空。
type Heartbeater struct {
	control  relayv1.RelayControlClient
	collect  func() *relayv1.HeartbeatRequest
	interval time.Duration
	now      func() time.Time

	mu       sync.Mutex
	offset   time.Duration // 主节点时间减本机时间
	measured bool
	draining bool
	lastOK   time.Time
}

// NewHeartbeater 创建心跳发送。collect 返回这一次的内容（版本、速率、计数等；时间字段由这里填）。
func NewHeartbeater(client *transport.Client, collect func() *relayv1.HeartbeatRequest, interval time.Duration) *Heartbeater {
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	return &Heartbeater{control: relayv1.NewRelayControlClient(client.Conn(transport.TierControl)), collect: collect, interval: interval, now: time.Now}
}

// Beat 发一次心跳。
func (h *Heartbeater) Beat(ctx context.Context) error {
	req := h.collect()
	if req == nil {
		req = &relayv1.HeartbeatRequest{}
	}
	ctx, cancel := context.WithTimeout(ctx, heartbeatTimeout)
	defer cancel()
	t0 := h.now()
	req.SentAtUnixMs = t0.UnixMilli()
	resp, err := h.control.Heartbeat(ctx, req)
	t1 := h.now()
	if err != nil {
		return err
	}
	h.mu.Lock()
	// 主节点时间减去请求往返的中点：不扣单向延迟，偏差上限 30 秒面前误差可以忽略。
	mid := t0.Add(t1.Sub(t0) / 2)
	h.offset = time.UnixMilli(resp.GetMasterTimeUnixMs()).Sub(mid)
	h.measured = true
	h.draining = resp.GetDraining()
	h.lastOK = t1
	h.mu.Unlock()
	return nil
}

// Run 按间隔发心跳直到 ctx 结束；失败只记日志（主节点那边连续 15 秒收不到会标离线）。
func (h *Heartbeater) Run(ctx context.Context) {
	t := time.NewTicker(h.interval)
	defer t.Stop()
	for {
		if err := h.Beat(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("relay heartbeat failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Skew 返回测得的主从时钟偏差（绝对值；没测过时为 0）。额度租约的停用时间点按它再提前（设计 4.2）。
func (h *Heartbeater) Skew() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.offset < 0 {
		return -h.offset
	}
	return h.offset
}

// ClockHealthy 报告时钟偏差没有超过上限（还没测过时算正常：刚启动的头几秒不拒绝服务）。
func (h *Heartbeater) ClockHealthy() bool { return h.Skew() <= MaxClockSkew }

// Draining 报告主节点说这台在排空中。
func (h *Heartbeater) Draining() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.draining
}
