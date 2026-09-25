package transport

import (
	"container/list"
	"context"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// IdempotencyOptions 控制主节点保存幂等结果的时间和内存（开发计划 2.3）。
type IdempotencyOptions struct {
	// TTL 是结果保留多久（默认 10 分钟）。
	TTL time.Duration
	// MaxBytesPerPeer 是每个对端（每台节点）保存结果的字节上限（默认 64MB）。
	// 超出时淘汰最旧的结果。按每秒上千次选号算，10 分钟的全部结果会到 GB 级，
	// 所以必须有上限；被淘汰的键再重发会重新执行，影响限于并发槽和额度的
	// 重复占用，由心跳核对兜底（开发计划第 4 节"释放消息丢失"）。
	MaxBytesPerPeer int
}

func (o IdempotencyOptions) withDefaults() IdempotencyOptions {
	if o.TTL <= 0 {
		o.TTL = 10 * time.Minute
	}
	if o.MaxBytesPerPeer <= 0 {
		o.MaxBytesPerPeer = 64 << 20
	}
	return o
}

// idempotencyStore 按（对端, 纪元, 方法, 键）保存成功的响应。对端取自验证过的证书，
// 纪元是主节点本次启动的纪元，所以主节点重启后旧键天然失效。
// 同一个键的第二个请求在第一个还在执行时会等它的结果，不会执行两次。
// 只保存成功的结果：失败（包括过载）后重发会重新执行。
type idempotencyStore struct {
	opts  IdempotencyOptions
	now   func() time.Time
	mu    sync.Mutex
	peers map[string]*idempotencyPeer
}

type idempotencyPeer struct {
	entries map[string]*idempotencyEntry
	order   *list.List // 按完成时间排序的已完成条目，最旧的在前
	bytes   int
}

type idempotencyEntry struct {
	key      string
	done     chan struct{}
	msgType  protoreflect.MessageType
	payload  []byte
	err      error
	expires  time.Time
	element  *list.Element
	finished bool
}

func newIdempotencyStore(opts IdempotencyOptions) *idempotencyStore {
	return &idempotencyStore{opts: opts.withDefaults(), now: time.Now, peers: make(map[string]*idempotencyPeer)}
}

// do 执行 fn，或者返回同一个键上一次成功的结果。
func (s *idempotencyStore) do(ctx context.Context, peerKey, key string, fn func() (any, error)) (any, error) {
	s.mu.Lock()
	p := s.peers[peerKey]
	if p == nil {
		p = &idempotencyPeer{entries: make(map[string]*idempotencyEntry), order: list.New()}
		s.peers[peerKey] = p
	}
	now := s.now()
	s.evictExpiredLocked(p, now)
	if e, ok := p.entries[key]; ok {
		s.mu.Unlock()
		select {
		case <-e.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if e.err != nil {
			return nil, e.err
		}
		msg := e.msgType.New().Interface()
		if err := proto.Unmarshal(e.payload, msg); err != nil {
			return nil, err
		}
		return msg, nil
	}
	e := &idempotencyEntry{key: key, done: make(chan struct{})}
	p.entries[key] = e
	s.mu.Unlock()

	resp, err := fn()

	s.mu.Lock()
	defer s.mu.Unlock()
	defer close(e.done)
	if err != nil {
		e.err = err
		delete(p.entries, key)
		return resp, err
	}
	msg, ok := resp.(proto.Message)
	if !ok {
		delete(p.entries, key)
		return resp, nil
	}
	payload, merr := proto.Marshal(msg)
	if merr != nil || len(payload) > s.opts.MaxBytesPerPeer {
		delete(p.entries, key)
		return resp, nil
	}
	e.msgType = msg.ProtoReflect().Type()
	e.payload = payload
	e.expires = s.now().Add(s.opts.TTL)
	e.finished = true
	e.element = p.order.PushBack(e)
	p.bytes += entrySize(e)
	for p.bytes > s.opts.MaxBytesPerPeer && p.order.Len() > 0 {
		s.removeLocked(p, p.order.Front().Value.(*idempotencyEntry))
	}
	return resp, nil
}

func entrySize(e *idempotencyEntry) int { return len(e.payload) + len(e.key) + 96 }

func (s *idempotencyStore) evictExpiredLocked(p *idempotencyPeer, now time.Time) {
	for p.order.Len() > 0 {
		oldest := p.order.Front().Value.(*idempotencyEntry)
		if now.Before(oldest.expires) {
			return
		}
		s.removeLocked(p, oldest)
	}
}

func (s *idempotencyStore) removeLocked(p *idempotencyPeer, e *idempotencyEntry) {
	if e.element != nil {
		p.order.Remove(e.element)
		e.element = nil
		p.bytes -= entrySize(e)
	}
	if cur, ok := p.entries[e.key]; ok && cur == e {
		delete(p.entries, e.key)
	}
}

// bytesFor 返回某个对端当前保存的字节数（测试用）。
func (s *idempotencyStore) bytesFor(peerKey string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.peers[peerKey]; p != nil {
		return p.bytes
	}
	return 0
}
