package node

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"google.golang.org/protobuf/proto"
)

// 从节点本地扣费队列（设计 5.1、开发计划 3.1）：分段追加文件，每条记录 = 长度 + CRC32C + protobuf，
// 单独一个写入协程组提交落盘，主节点确认到哪就删到哪一段。
//
// 重启后把剩下的段全部重放、重新发送：主节点按扣费凭证去重，已入账的回"已入账"，所以不用另记确认位置。

var crc32c = crc32.MakeTable(crc32.Castagnoli)

const (
	walRecordHeader  = 8 // 长度 + CRC32C
	walMaxRecordSize = 4 << 20
	walSegmentPrefix = "usage-"
	walSegmentSuffix = ".wal"
	walReserveFile   = "reserve.bin"
)

// ErrUsageQueueUnavailable：本地队列写不进去或积压太多，从节点停止接收新请求（设计 5.1）。
var ErrUsageQueueUnavailable = errors.New("usage queue is unavailable; stop accepting requests")

// UsageWALOptions 是本地队列的参数。
type UsageWALOptions struct {
	// SegmentBytes：一段写满多大换新段（默认 64MB）。
	SegmentBytes int64
	// GroupCommitRecords、GroupCommitDelay：攒够多少条或等多久一起落盘（默认 256 条、20 毫秒）。
	GroupCommitRecords int
	GroupCommitDelay   time.Duration
	// MaxPending：积压（还没确认的）超过多少条就不健康（默认 200000）。
	MaxPending int
	// ReserveBytes：占位文件大小（默认 16MB）。磁盘写满时删掉它腾出空间，让队列把手里的写完、报告不健康。
	ReserveBytes int64
}

func (o UsageWALOptions) withDefaults() UsageWALOptions {
	if o.SegmentBytes <= 0 {
		o.SegmentBytes = 64 << 20
	}
	if o.GroupCommitRecords <= 0 {
		o.GroupCommitRecords = 256
	}
	if o.GroupCommitDelay <= 0 {
		o.GroupCommitDelay = 20 * time.Millisecond
	}
	if o.MaxPending <= 0 {
		o.MaxPending = 200_000
	}
	if o.ReserveBytes <= 0 {
		o.ReserveBytes = 16 << 20
	}
	return o
}

type walSegment struct {
	path     string
	firstSeq uint64
	pending  map[uint64]struct{} // 这段里还没确认的记录
	size     int64
}

type walWrite struct {
	rec  *relayv1.UsageRecord
	done chan error
}

// UsageWAL 是本地扣费队列。
type UsageWAL struct {
	dir  string
	opts UsageWALOptions

	writes chan walWrite
	stop   chan struct{}
	wg     sync.WaitGroup

	mu       sync.Mutex
	nextSeq  uint64
	segments []*walSegment
	active   *os.File
	pending  map[uint64]*relayv1.UsageRecord // 待发送（还没确认）
	bySeg    map[uint64]*walSegment
	failure  error
	closed   bool
	// queuedAt 记每条待发送记录进队列的时间（心跳报"最早一条的时间"；重放出来的按重放时间算）；appended 是本次启动以来写进队列的条数。
	queuedAt map[uint64]time.Time
	appended uint64
}

// OpenUsageWAL 打开（或新建）队列目录，重放剩下的记录。
func OpenUsageWAL(dir string, opts UsageWALOptions) (*UsageWAL, error) {
	opts = opts.withDefaults()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	w := &UsageWAL{dir: dir, opts: opts, writes: make(chan walWrite, opts.GroupCommitRecords*4), stop: make(chan struct{}),
		nextSeq: 1, pending: map[uint64]*relayv1.UsageRecord{}, bySeg: map[uint64]*walSegment{}, queuedAt: map[uint64]time.Time{}}
	if err := w.replay(); err != nil {
		return nil, err
	}
	if err := w.ensureReserve(); err != nil {
		return nil, err
	}
	if err := w.openActive(); err != nil {
		return nil, err
	}
	w.wg.Add(1)
	go w.writer()
	return w, nil
}

func segmentName(firstSeq uint64) string {
	return fmt.Sprintf("%s%020d%s", walSegmentPrefix, firstSeq, walSegmentSuffix)
}

// replay 按序读出所有段。最后一段末尾写了一半的记录（崩溃）截掉；中间损坏的段改名隔离并报警，
// 读出来的部分照常发送。
func (w *UsageWAL) replay() error {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), walSegmentPrefix) && strings.HasSuffix(e.Name(), walSegmentSuffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i, name := range names {
		first, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(name, walSegmentPrefix), walSegmentSuffix), 10, 64)
		if err != nil {
			continue
		}
		path := filepath.Join(w.dir, name)
		seg := &walSegment{path: path, firstSeq: first, pending: map[uint64]struct{}{}}
		good, damaged, err := w.readSegment(path, seg)
		if err != nil {
			return err
		}
		if damaged {
			if i == len(names)-1 {
				// 最后一段末尾是没写完的记录：截到最后一条完整记录。
				if err := os.Truncate(path, good); err != nil {
					return fmt.Errorf("truncate torn usage segment: %w", err)
				}
				slog.Warn("usage queue: truncated a torn record at the end of the last segment", "segment", name, "offset", good)
			} else {
				slog.Error("usage queue: segment is damaged; the records after the damage are lost", "segment", name, "offset", good)
				_ = os.Rename(path, path+".damaged")
				seg.path = path + ".damaged"
			}
		}
		seg.size = good
		w.segments = append(w.segments, seg)
	}
	return nil
}

// readSegment 读一段，返回最后一条完整记录之后的偏移，以及后面是否有损坏的数据。
func (w *UsageWAL) readSegment(path string, seg *walSegment) (int64, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = f.Close() }()
	var off int64
	header := make([]byte, walRecordHeader)
	for {
		if _, err := io.ReadFull(f, header); err != nil {
			return off, err != io.EOF, nil
		}
		n := binary.LittleEndian.Uint32(header[0:4])
		sum := binary.LittleEndian.Uint32(header[4:8])
		if n == 0 || n > walMaxRecordSize {
			return off, true, nil
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(f, body); err != nil {
			return off, true, nil
		}
		if crc32.Checksum(body, crc32c) != sum {
			return off, true, nil
		}
		var rec relayv1.UsageRecord
		if err := proto.Unmarshal(body, &rec); err != nil {
			return off, true, nil
		}
		w.pending[rec.GetSeq()] = &rec
		w.bySeg[rec.GetSeq()] = seg
		seg.pending[rec.GetSeq()] = struct{}{}
		if rec.GetSeq() >= w.nextSeq {
			w.nextSeq = rec.GetSeq() + 1
		}
		off += int64(walRecordHeader) + int64(n)
	}
}

// ensureReserve 建好占位文件（磁盘写满时删掉它，腾出空间报告不健康）。
func (w *UsageWAL) ensureReserve() error {
	path := filepath.Join(w.dir, walReserveFile)
	if st, err := os.Stat(path); err == nil && st.Size() >= w.opts.ReserveBytes {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	chunk := make([]byte, 1<<20)
	for written := int64(0); written < w.opts.ReserveBytes; written += int64(len(chunk)) {
		if _, err := f.Write(chunk); err != nil {
			return fmt.Errorf("create usage queue reserve: %w", err)
		}
	}
	return f.Sync()
}

// openActive 打开要追加的段：最后一段还没满就接着写，否则开新段。
func (w *UsageWAL) openActive() error {
	if n := len(w.segments); n > 0 {
		last := w.segments[n-1]
		if !strings.HasSuffix(last.path, ".damaged") && last.size < w.opts.SegmentBytes {
			f, err := os.OpenFile(last.path, os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return err
			}
			w.active = f
			return nil
		}
	}
	return w.rotateLocked()
}

// rotateLocked 开一个新段（第一条记录的序号是 nextSeq）。
func (w *UsageWAL) rotateLocked() error {
	if w.active != nil {
		_ = w.active.Close()
	}
	seg := &walSegment{path: filepath.Join(w.dir, segmentName(w.nextSeq)), firstSeq: w.nextSeq, pending: map[uint64]struct{}{}}
	f, err := os.OpenFile(seg.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	w.active = f
	w.segments = append(w.segments, seg)
	return nil
}

// Append 把一条记录写进队列，落盘后返回它的序号。队列不健康时返回 ErrUsageQueueUnavailable。
func (w *UsageWAL) Append(ctx context.Context, rec *relayv1.UsageRecord) (uint64, error) {
	if err := w.Healthy(); err != nil {
		return 0, err
	}
	done := make(chan error, 1)
	rec, ok := proto.Clone(rec).(*relayv1.UsageRecord)
	if !ok {
		return 0, errors.New("usage queue: unexpected record type")
	}
	select {
	case w.writes <- walWrite{rec: rec, done: done}:
	case <-w.stop:
		return 0, ErrUsageQueueUnavailable
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	// 进了写入队列就一定会被处理：等它落盘（客户端断开也要记账，不跟 ctx 走）。
	if err := <-done; err != nil {
		return 0, err
	}
	return rec.GetSeq(), nil
}

// writer 是唯一的写入协程：攒一批（条数或时间到）一起写、一起落盘（组提交）。
func (w *UsageWAL) writer() {
	defer w.wg.Done()
	for {
		var batch []walWrite
		select {
		case first := <-w.writes:
			batch = append(batch, first)
		case <-w.stop:
			w.drainOnStop()
			return
		}
		timer := time.NewTimer(w.opts.GroupCommitDelay)
	collect:
		for len(batch) < w.opts.GroupCommitRecords {
			select {
			case next := <-w.writes:
				batch = append(batch, next)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		err := w.commit(batch)
		for _, b := range batch {
			b.done <- err
		}
	}
}

// drainOnStop 关闭时把已经进了写入队列的记录写完。
func (w *UsageWAL) drainOnStop() {
	for {
		select {
		case b := <-w.writes:
			b.done <- w.commit([]walWrite{b})
		default:
			return
		}
	}
}

// commit 给这一批分配序号、写入、落盘，成功后加入待发送。
func (w *UsageWAL) commit(batch []walWrite) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure != nil {
		return ErrUsageQueueUnavailable
	}
	var buf []byte
	seqs := make([]uint64, 0, len(batch))
	for _, b := range batch {
		b.rec.Seq = w.nextSeq
		body, err := proto.Marshal(b.rec)
		if err != nil {
			return err
		}
		var header [walRecordHeader]byte
		binary.LittleEndian.PutUint32(header[0:4], uint32(len(body)))
		binary.LittleEndian.PutUint32(header[4:8], crc32.Checksum(body, crc32c))
		buf = append(buf, header[:]...)
		buf = append(buf, body...)
		seqs = append(seqs, w.nextSeq)
		w.nextSeq++
	}
	seg := w.segments[len(w.segments)-1]
	if _, err := w.active.Write(buf); err != nil {
		return w.failLocked(fmt.Errorf("write usage queue: %w", err))
	}
	if err := w.active.Sync(); err != nil {
		return w.failLocked(fmt.Errorf("sync usage queue: %w", err))
	}
	seg.size += int64(len(buf))
	now := time.Now()
	for i, b := range batch {
		w.pending[seqs[i]] = b.rec
		w.bySeg[seqs[i]] = seg
		w.queuedAt[seqs[i]] = now
		seg.pending[seqs[i]] = struct{}{}
	}
	w.appended += uint64(len(batch))
	if seg.size >= w.opts.SegmentBytes {
		if err := w.rotateLocked(); err != nil {
			return w.failLocked(fmt.Errorf("rotate usage queue: %w", err))
		}
	}
	return nil
}

// failLocked 写不进去（磁盘满、损坏）：删掉占位文件腾出空间，队列从此不健康，从节点停止接收新请求。
func (w *UsageWAL) failLocked(err error) error {
	w.failure = err
	_ = os.Remove(filepath.Join(w.dir, walReserveFile))
	slog.Error("usage queue failed; the node stops accepting requests", "error", err)
	return ErrUsageQueueUnavailable
}

// Healthy 报告队列能否接收新记录：写失败过、积压太多时返回 ErrUsageQueueUnavailable。
func (w *UsageWAL) Healthy() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.failure != nil || len(w.pending) >= w.opts.MaxPending {
		return ErrUsageQueueUnavailable
	}
	return nil
}

// Pending 返回按序号从小到大最多 limit 条待发送的记录（跳过 skip 里的）。
func (w *UsageWAL) Pending(limit int, skip map[uint64]bool) []*relayv1.UsageRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	seqs := make([]uint64, 0, len(w.pending))
	for seq := range w.pending {
		if !skip[seq] {
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	if len(seqs) > limit {
		seqs = seqs[:limit]
	}
	out := make([]*relayv1.UsageRecord, 0, len(seqs))
	for _, seq := range seqs {
		out = append(out, w.pending[seq])
	}
	return out
}

// Stats 返回本次启动以来写进队列的条数、待发送的条数和最早一条进队列的时间（心跳用；队列空时 oldest 为零值）。
func (w *UsageWAL) Stats() (appended uint64, pending int, oldest time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for seq := range w.pending {
		t, ok := w.queuedAt[seq]
		if !ok {
			continue
		}
		if oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
	}
	return w.appended, len(w.pending), oldest
}

// Len 返回待发送的条数。
func (w *UsageWAL) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pending)
}

// Ack 主节点确认了这些记录（入账、已入账或隔离）：从待发送里去掉，整段都确认了的旧段删掉。
func (w *UsageWAL) Ack(seqs ...uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, seq := range seqs {
		delete(w.pending, seq)
		delete(w.queuedAt, seq)
		if seg, ok := w.bySeg[seq]; ok {
			delete(seg.pending, seq)
			delete(w.bySeg, seq)
		}
	}
	// 只删前面连续的、已全部确认的非当前段。
	for len(w.segments) > 1 && len(w.segments[0].pending) == 0 {
		if err := os.Remove(w.segments[0].path); err != nil && !os.IsNotExist(err) {
			slog.Warn("usage queue: remove acknowledged segment failed", "segment", w.segments[0].path, "error", err)
			return
		}
		w.segments = w.segments[1:]
	}
}

// Close 停止写入协程（已进入写入队列的记录会写完）。
func (w *UsageWAL) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()
	close(w.stop)
	w.wg.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.active != nil {
		return w.active.Close()
	}
	return nil
}
