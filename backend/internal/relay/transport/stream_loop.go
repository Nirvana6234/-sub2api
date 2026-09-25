package transport

import (
	"context"
	"math/rand/v2"
	"time"
)

// Backoff 是指数退避加随机抖动（开发计划 3.2 "重连风暴"：0.5~30 秒）。
// 主节点重启后，大量从节点不会在同一时刻一起重连。
type Backoff struct {
	Base   time.Duration
	Max    time.Duration
	Factor float64
	// Jitter 是随机抖动比例：实际等待在 [d*(1-Jitter), d*(1+Jitter)] 之间。
	Jitter float64

	attempt int
}

// DefaultBackoff 返回 0.5~30 秒的默认退避。
func DefaultBackoff() *Backoff {
	return &Backoff{Base: 500 * time.Millisecond, Max: 30 * time.Second, Factor: 1.6, Jitter: 0.2}
}

// Next 返回下一次等待时间。
func (b *Backoff) Next() time.Duration {
	d := float64(b.Base)
	for i := 0; i < b.attempt; i++ {
		d *= b.Factor
		if d >= float64(b.Max) {
			d = float64(b.Max)
			break
		}
	}
	b.attempt++
	if b.Jitter > 0 {
		d *= 1 - b.Jitter + 2*b.Jitter*rand.Float64() //nolint:gosec // 抖动不需要密码学随机数
	}
	if d > float64(b.Max) {
		d = float64(b.Max)
	}
	return time.Duration(d)
}

// Reset 在连接稳定后重置退避。
func (b *Backoff) Reset() { b.attempt = 0 }

// RunStreamLoop 反复执行 run（打开一条长期流并处理到它结束），断开后按退避重开，
// 直到 ctx 结束。流保持超过 stableAfter 才算稳定，之后的断开从最短退避重新开始。
// onError 可为 nil，用于记录每次断开的原因。
func RunStreamLoop(ctx context.Context, b *Backoff, stableAfter time.Duration, run func(context.Context) error, onError func(error)) {
	if b == nil {
		b = DefaultBackoff()
	}
	for {
		started := time.Now()
		err := run(ctx)
		if ctx.Err() != nil {
			return
		}
		if onError != nil && err != nil {
			onError(err)
		}
		if time.Since(started) >= stableAfter {
			b.Reset()
		}
		wait := time.NewTimer(b.Next())
		select {
		case <-ctx.Done():
			wait.Stop()
			return
		case <-wait.C:
		}
	}
}
