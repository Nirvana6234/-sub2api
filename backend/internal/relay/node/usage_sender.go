package node

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
)

// UsageSubmitter 提交一批扣费记录（BillingClient 满足）。
type UsageSubmitter interface {
	Submit(ctx context.Context, batch *relayv1.UsageBatch) (*relayv1.UsageBatchAck, error)
}

// UsageSenderOptions 是发送的参数。
type UsageSenderOptions struct {
	// Interval：多久发一批（默认 1 秒）；BatchSize：一批最多多少条（默认 100，攒够就提前发）。
	Interval  time.Duration
	BatchSize int
	// RetryDelay：主节点回"稍后重发"的记录多久后再发（默认 10 秒）。
	RetryDelay time.Duration
	// OnResult 在每条记录有了结果时调用（入账、已入账、隔离）：从节点据此修正本地额度。
	OnResult func(rec *relayv1.UsageRecord, res *relayv1.UsageRecordResult)
}

func (o UsageSenderOptions) withDefaults() UsageSenderOptions {
	if o.Interval <= 0 {
		o.Interval = time.Second
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 100
	}
	if o.RetryDelay <= 0 {
		o.RetryDelay = 10 * time.Second
	}
	return o
}

// UsageSender 把本地队列里的记录按批发给主节点（设计 5.1）：主节点确认了的才从队列删除，
// 发送失败下次整批重发（主节点按凭证去重）。
type UsageSender struct {
	wal    *UsageWAL
	submit UsageSubmitter
	opts   UsageSenderOptions
	now    func() time.Time

	mu       sync.Mutex
	batchSeq uint64
	retryAt  map[uint64]time.Time
	kick     chan struct{}
}

// NewUsageSender 创建发送。
func NewUsageSender(wal *UsageWAL, submit UsageSubmitter, opts UsageSenderOptions) *UsageSender {
	return &UsageSender{wal: wal, submit: submit, opts: opts.withDefaults(), now: time.Now, retryAt: map[uint64]time.Time{}, kick: make(chan struct{}, 1)}
}

// Kick 让发送尽快发一批（攒够一批时调用）。
func (s *UsageSender) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run 按间隔发送，直到 ctx 结束。
func (s *UsageSender) Run(ctx context.Context) {
	t := time.NewTicker(s.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
		for {
			sent, err := s.Flush(ctx)
			if err != nil {
				slog.Warn("relay usage batch failed, will resend", "error", err)
				break
			}
			if sent < s.opts.BatchSize {
				break
			}
		}
	}
}

// Flush 发一批，返回发出的条数。
func (s *UsageSender) Flush(ctx context.Context) (int, error) {
	s.mu.Lock()
	now := s.now()
	skip := map[uint64]bool{}
	for seq, at := range s.retryAt {
		if now.Before(at) {
			skip[seq] = true
		}
	}
	s.mu.Unlock()

	recs := s.wal.Pending(s.opts.BatchSize, skip)
	if len(recs) == 0 {
		return 0, nil
	}
	// 批次序号只给真正发出的批次（主节点按跳号报警），空轮询不占号。
	s.mu.Lock()
	s.batchSeq++
	batchSeq := s.batchSeq
	s.mu.Unlock()
	ack, err := s.submit.Submit(ctx, &relayv1.UsageBatch{BatchSeq: batchSeq, Records: recs})
	if err != nil {
		return 0, err
	}
	bySeq := make(map[uint64]*relayv1.UsageRecord, len(recs))
	for _, r := range recs {
		bySeq[r.GetSeq()] = r
	}
	var done []uint64
	type outcome struct {
		rec *relayv1.UsageRecord
		res *relayv1.UsageRecordResult
	}
	var outcomes []outcome
	s.mu.Lock()
	for _, res := range ack.GetResults() {
		rec, ok := bySeq[res.GetSeq()]
		if !ok {
			continue
		}
		switch res.GetStatus() {
		case relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_ALREADY_SETTLED:
			done = append(done, res.GetSeq())
		case relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED:
			// 主节点隔离了这条（凭证验不过、格式错误等）：不再重发，报警。
			slog.Error("relay usage record rejected by the master", "seq", res.GetSeq(), "reason", res.GetReason())
			done = append(done, res.GetSeq())
		default:
			s.retryAt[res.GetSeq()] = now.Add(s.opts.RetryDelay)
			continue
		}
		delete(s.retryAt, res.GetSeq())
		outcomes = append(outcomes, outcome{rec: rec, res: res})
	}
	s.mu.Unlock()
	s.wal.Ack(done...)
	if s.opts.OnResult != nil {
		for _, o := range outcomes {
			s.opts.OnResult(o.rec, o.res)
		}
	}
	return len(recs), nil
}
