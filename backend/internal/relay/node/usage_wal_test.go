package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/stretchr/testify/require"
)

func walOpts() UsageWALOptions {
	return UsageWALOptions{SegmentBytes: 256, GroupCommitDelay: time.Millisecond, ReserveBytes: 1 << 20}
}

func record(i int) *relayv1.UsageRecord {
	return &relayv1.UsageRecord{Voucher: []byte{byte(i)}, Kind: relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI,
		ResultJson: []byte(`{"RequestID":"r"}`), UserAgent: "ua"}
}

func segments(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "usage-*.wal"))
	require.NoError(t, err)
	return m
}

// 写入即落盘、按序编号；确认后删掉整段都确认了的旧段；重启后没确认的全部重放。
func TestUsageWALAppendAckAndReplay(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	w, err := OpenUsageWAL(dir, walOpts())
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	var wg sync.WaitGroup
	seqs := make([]uint64, 40)
	for i := 0; i < 10; i++ { // 并发写入：一起落盘，编号不重复
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seq, err := w.Append(ctx, record(i))
			require.NoError(t, err)
			seqs[i] = seq
		}(i)
	}
	wg.Wait()
	for i := 10; i < 40; i++ { // 逐条写入：段写满就换
		seq, err := w.Append(ctx, record(i))
		require.NoError(t, err)
		seqs[i] = seq
	}
	require.Equal(t, 40, w.Len())
	seen := map[uint64]bool{}
	for _, s := range seqs {
		require.False(t, seen[s], "sequence numbers are unique")
		seen[s] = true
	}
	require.Greater(t, len(segments(t, dir)), 2, "small segments rotate")

	pending := w.Pending(100, nil)
	require.Len(t, pending, 40)
	require.Equal(t, uint64(1), pending[0].GetSeq())
	var first20 []uint64
	for _, r := range pending[:20] {
		first20 = append(first20, r.GetSeq())
	}
	before := len(segments(t, dir))
	w.Ack(first20...)
	require.Equal(t, 20, w.Len())
	require.Less(t, len(segments(t, dir)), before, "fully acknowledged segments are removed")
	require.NoError(t, w.Close())

	// 重启：没确认的 20 条都回来；和它们同段、已确认的也会重发（主节点按凭证去重，回"已入账"），
	// 整段都确认了、已删掉的段不再回来。新记录接着编号。
	w2, err := OpenUsageWAL(dir, walOpts())
	require.NoError(t, err)
	defer func() { _ = w2.Close() }()
	replayed := w2.Pending(100, nil)
	got := map[uint64]bool{}
	for _, r := range replayed {
		got[r.GetSeq()] = true
		require.Equal(t, "ua", r.GetUserAgent())
	}
	for seq := uint64(21); seq <= 40; seq++ {
		require.True(t, got[seq], "unacknowledged record %d is replayed", seq)
	}
	require.Less(t, len(replayed), 40, "records in fully acknowledged segments are gone")
	seq, err := w2.Append(ctx, record(99))
	require.NoError(t, err)
	require.Equal(t, uint64(41), seq)
}

// 崩溃时最后一条只写了一半：重启时截掉它，前面的照常重放，之后接着写。
func TestUsageWALTruncatesATornTail(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	opts := walOpts()
	opts.SegmentBytes = 1 << 20
	w, err := OpenUsageWAL(dir, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	for i := 0; i < 3; i++ {
		_, err := w.Append(ctx, record(i))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	seg := segments(t, dir)[0]
	f, err := os.OpenFile(seg, os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.Write([]byte{50, 0, 0, 0, 1, 2, 3, 4, 9, 9}) // 头说有 50 字节，只写了 2 字节
	require.NoError(t, err)
	require.NoError(t, f.Close())

	w2, err := OpenUsageWAL(dir, opts)
	require.NoError(t, err)
	defer func() { _ = w2.Close() }()
	require.Equal(t, 3, w2.Len())
	seq, err := w2.Append(ctx, record(3))
	require.NoError(t, err)
	require.Equal(t, uint64(4), seq)
	require.NoError(t, w2.Close())
	w3, err := OpenUsageWAL(dir, opts)
	require.NoError(t, err)
	defer func() { _ = w3.Close() }()
	require.Equal(t, 4, w3.Len(), "the record written after the truncation is readable")
}

// 积压太多、写入失败时队列不健康，从节点停止接收新请求。
func TestUsageWALReportsUnhealthy(t *testing.T) {
	ctx := context.Background()
	opts := walOpts()
	opts.MaxPending = 2
	w, err := OpenUsageWAL(t.TempDir(), opts)
	require.NoError(t, err)
	defer func() { _ = w.Close() }()
	_, err = w.Append(ctx, record(1))
	require.NoError(t, err)
	_, err = w.Append(ctx, record(2))
	require.NoError(t, err)
	_, err = w.Append(ctx, record(3))
	require.ErrorIs(t, err, ErrUsageQueueUnavailable)
	w.Ack(1)
	require.NoError(t, w.Healthy())

	w.mu.Lock()
	_ = w.active.Close() // 模拟磁盘出错
	w.mu.Unlock()
	_, err = w.Append(ctx, record(4))
	require.ErrorIs(t, err, ErrUsageQueueUnavailable)
	require.ErrorIs(t, w.Healthy(), ErrUsageQueueUnavailable)
	_, statErr := os.Stat(filepath.Join(w.dir, walReserveFile))
	require.True(t, os.IsNotExist(statErr), "the reserve is released to make room")
}

type scriptedSubmitter struct {
	mu      sync.Mutex
	batches []*relayv1.UsageBatch
	status  func(seq uint64) relayv1.UsageRecordStatus
	fail    error
}

func (s *scriptedSubmitter) Submit(_ context.Context, b *relayv1.UsageBatch) (*relayv1.UsageBatchAck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return nil, s.fail
	}
	s.batches = append(s.batches, b)
	ack := &relayv1.UsageBatchAck{}
	for _, r := range b.GetRecords() {
		ack.Results = append(ack.Results, &relayv1.UsageRecordResult{Seq: r.GetSeq(), Status: s.status(r.GetSeq()),
			Consumed: []*relayv1.LeaseConsumption{{LeaseId: 1, Amount: 100}}})
	}
	return ack, nil
}

// 发送：确认了的出队并回调；"稍后重发"的等一会儿再发；发送失败下次整批重发；批次序号递增。
func TestUsageSender(t *testing.T) {
	ctx := context.Background()
	w, err := OpenUsageWAL(t.TempDir(), walOpts())
	require.NoError(t, err)
	defer func() { _ = w.Close() }()
	for i := 0; i < 5; i++ {
		_, err := w.Append(ctx, record(i))
		require.NoError(t, err)
	}
	sub := &scriptedSubmitter{status: func(seq uint64) relayv1.UsageRecordStatus {
		switch seq {
		case 2:
			return relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_RETRY
		case 3:
			return relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED
		case 4:
			return relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_ALREADY_SETTLED
		}
		return relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED
	}}
	var results []uint64
	s := NewUsageSender(w, sub, UsageSenderOptions{BatchSize: 3, RetryDelay: time.Minute,
		OnResult: func(rec *relayv1.UsageRecord, res *relayv1.UsageRecordResult) {
			results = append(results, rec.GetSeq())
		}})
	now := time.Now()
	s.now = func() time.Time { return now }

	sub.fail = errors.New("master unreachable")
	_, err = s.Flush(ctx)
	require.Error(t, err)
	require.Equal(t, 5, w.Len(), "nothing leaves the queue without an ack")
	sub.fail = nil

	n, err := s.Flush(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	n, err = s.Flush(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n, "the retrying record waits")
	require.Equal(t, 1, w.Len())
	require.Equal(t, []uint64{1, 3, 4, 5}, results)
	n, err = s.Flush(ctx)
	require.NoError(t, err)
	require.Zero(t, n)

	now = now.Add(2 * time.Minute)
	n, err = s.Flush(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, uint64(2), sub.batches[len(sub.batches)-1].GetRecords()[0].GetSeq())
	require.Greater(t, sub.batches[len(sub.batches)-1].GetBatchSeq(), sub.batches[0].GetBatchSeq())
}
