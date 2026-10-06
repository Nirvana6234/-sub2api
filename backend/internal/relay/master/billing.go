package master

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxUsageBatch 是一批扣费记录的条数上限（从节点每 1~2 秒或攒够 100 条发一批，设计 5.1）。
const maxUsageBatch = 500

// Settler 是主节点的扣费入账（internal/relay/relaysettle，经 relaywire 注入）：把一条扣费记录交给
// 单机的入账函数。每条记录按凭证去重，重复提交只入账一次。
type Settler interface {
	Settle(ctx context.Context, nodeID int64, rec *relayv1.UsageRecord) *relayv1.UsageRecordResult
}

// SettleEnv 是入账从运行时拿到的东西（本次启动有效）。
type SettleEnv struct {
	// VerifyVoucher 验扣费凭证（签名、期限、上报节点）。
	VerifyVoucher func(raw []byte, reportingNodeID int64) (*relayv1.Voucher, error)
	// RefreshUser 入账消耗租约后刷新内存里的冻结额；没有额度服务时为 nil。
	RefreshUser func(ctx context.Context, userID int64) error
	// LastSuspectRevocation 返回节点最近一次因怀疑被攻破而吊销的时间（NodeStore.LastSuspectRevocation）：
	// 之前签发的凭证照常入账、记为待复核（设计 5.4）。
	LastSuspectRevocation func(ctx context.Context, nodeID int64) (time.Time, bool, error)
}

// Billing 实现 RelayBilling 服务（扣费连接）。
type Billing struct {
	relayv1.UnimplementedRelayBillingServer
	settler Settler

	// OnSettled 在一批记录入账后调用（节点、入账的条数——不含要重试的）；心跳对账用。
	OnSettled func(nodeID int64, records int)

	mu       sync.Mutex
	lastSeqs map[int64]uint64
}

// NewBilling 创建扣费服务。
func NewBilling(s Settler) *Billing {
	return &Billing{settler: s, lastSeqs: map[int64]uint64{}}
}

// SubmitUsage 逐条入账一批扣费记录。
func (b *Billing) SubmitUsage(ctx context.Context, batch *relayv1.UsageBatch) (*relayv1.UsageBatchAck, error) {
	peer, ok := transport.PeerFromContext(ctx)
	if !ok || peer.Class != transport.PeerIssued {
		return nil, status.Error(codes.PermissionDenied, "billing requires an issued certificate")
	}
	if len(batch.GetRecords()) > maxUsageBatch {
		return nil, status.Errorf(codes.InvalidArgument, "at most %d records per batch", maxUsageBatch)
	}
	b.checkSequence(peer.NodeID, batch.GetBatchSeq())
	ack := &relayv1.UsageBatchAck{Results: make([]*relayv1.UsageRecordResult, 0, len(batch.GetRecords()))}
	settled := 0
	for _, rec := range batch.GetRecords() {
		res := b.settler.Settle(ctx, peer.NodeID, rec)
		if res.GetStatus() != relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_RETRY {
			settled++
		}
		ack.Results = append(ack.Results, res)
	}
	if b.OnSettled != nil && settled > 0 {
		b.OnSettled(peer.NodeID, settled)
	}
	return ack, nil
}

// checkSequence 记下节点的批次序号：跳号说明有批次没到（丢失的记录不扣，设计 5.4），报警；
// 重复或倒退是重发，无害（入账按凭证去重）。
func (b *Billing) checkSequence(nodeID int64, seq uint64) {
	b.mu.Lock()
	last, seen := b.lastSeqs[nodeID]
	if !seen || seq > last {
		b.lastSeqs[nodeID] = seq
	}
	b.mu.Unlock()
	if seen && seq > last+1 {
		slog.Warn("relay usage batches missing", "node_id", nodeID, "last_seq", last, "seq", seq)
	}
}
