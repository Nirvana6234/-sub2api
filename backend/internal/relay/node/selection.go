package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

// SelectTimeout 是一次选号调用的最长时间：主节点上可能要排队等账号槽（等待计划最长 45 秒），
// 比控制连接默认的 5 秒长得多。排队期间从节点自己给客户端保活。
const SelectTimeout = 60 * time.Second

// SelectClient 是从节点调用主节点选号的客户端（设计 3.1 第 5 步）。
type SelectClient struct {
	control relayv1.RelayControlClient
	outbox  *EventOutbox
}

// NewSelectClient 创建选号客户端；释放消息经 outbox 走事件连接。
func NewSelectClient(client *transport.Client, outbox *EventOutbox) *SelectClient {
	return &SelectClient{control: relayv1.NewRelayControlClient(client.Conn(transport.TierControl)), outbox: outbox}
}

// NewRequestID 生成一次客户端请求的 ID（同一请求的多次选号共用）。
func NewRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Select 选号。幂等键是"请求 ID/第几次"：超时重发拿回同一个结果，不会多占一个槽。
// 主节点纪元变了返回 transport.ErrEpochChanged：调用方先核对租约（ReportLeases），再以新的一次选号重来。
func (s *SelectClient) Select(ctx context.Context, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, SelectTimeout)
	defer cancel()
	ctx = transport.WithIdempotencyKey(ctx, "select/"+req.GetRequestId()+"/"+strconv.FormatUint(uint64(req.GetAttempt()), 10))
	return s.control.Select(ctx, req)
}

// FetchCredentials 取这次选号所选账号的上游凭据（本机缓存里没有这个版本时）。
func (s *SelectClient) FetchCredentials(ctx context.Context, selectionID string) (*relayv1.AccountSnapshot, error) {
	resp, err := s.control.FetchCredentials(ctx, &relayv1.FetchCredentialsRequest{SelectionId: selectionID})
	if err != nil {
		return nil, err
	}
	return resp.GetAccount(), nil
}

// RefillQuota 按进行中的选号补充额度。
func (s *SelectClient) RefillQuota(ctx context.Context, req *relayv1.RefillQuotaRequest) ([]*relayv1.QuotaGrant, error) {
	resp, err := s.control.RefillQuota(withNewKey(ctx), req)
	if err != nil {
		return nil, err
	}
	return resp.GetGrants(), nil
}

// Release 释放一次选号：经事件连接发送，不等回复（设计 3.1）。
func (s *SelectClient) Release(rel *relayv1.SelectionRelease) {
	s.outbox.Enqueue(&relayv1.NodeEnvelope{Body: &relayv1.NodeEnvelope_SelectionRelease{SelectionRelease: rel}})
}

// EventOutbox 是从节点经事件连接发给主节点的消息队列（释放等）。
// 消息发送成功才出队：断线期间留着，重连后接着发（这些消息都按 ID 幂等，重复发送无害）。
// 队列有上限，满了丢最旧的：丢掉的释放由主节点的核对与槽位过期兜底。
type EventOutbox struct {
	mu      sync.Mutex
	pending []*relayv1.NodeEnvelope
	limit   int
	notify  chan struct{}
}

// NewEventOutbox 创建发送队列；limit 为最多积压的条数。
func NewEventOutbox(limit int) *EventOutbox {
	if limit <= 0 {
		limit = 100_000
	}
	return &EventOutbox{limit: limit, notify: make(chan struct{}, 1)}
}

// Enqueue 放入一条消息。
func (o *EventOutbox) Enqueue(env *relayv1.NodeEnvelope) {
	o.mu.Lock()
	if len(o.pending) >= o.limit {
		o.pending = o.pending[1:]
	}
	o.pending = append(o.pending, env)
	o.mu.Unlock()
	select {
	case o.notify <- struct{}{}:
	default:
	}
}

// Len 返回积压的条数。
func (o *EventOutbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.pending)
}

func (o *EventOutbox) peek() *relayv1.NodeEnvelope {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.pending) == 0 {
		return nil
	}
	return o.pending[0]
}

func (o *EventOutbox) pop(env *relayv1.NodeEnvelope) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.pending) > 0 && o.pending[0] == env {
		o.pending = o.pending[1:]
	}
}

// drain 把队列里的消息逐条发出去，直到 ctx 结束或发送失败（流断了，由重连后的下一轮接着发）。
func (o *EventOutbox) drain(ctx context.Context, send func(*relayv1.NodeEnvelope) error) error {
	for {
		for env := o.peek(); env != nil; env = o.peek() {
			if err := send(env); err != nil {
				return err
			}
			o.pop(env)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-o.notify:
		}
	}
}
