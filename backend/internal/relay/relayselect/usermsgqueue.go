package relayselect

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UserMsgQueue 见 RelayControl.UserMsgQueue：用户消息串行队列的一步在主节点的 Redis 上执行（与本地同一个缓存实现），
// 从节点的处理函数照本地同一段排队代码。只认这台节点正在用（或刚用过）的账号。
func (s *selector) UserMsgQueue(ctx context.Context, nodeID int64, req *relayv1.UserMsgQueueRequest) (*relayv1.UserMsgQueueResponse, error) {
	accountID := req.GetAccountId()
	// 读 Redis 时钟不涉及账号；其余只认这台节点正在用的账号。
	if req.GetOp() != relayv1.UserMsgQueueRequest_OP_NOW_MS && s.accountForEvent(nodeID, accountID) == nil {
		return nil, master.ErrSelectionNotFound
	}
	cache, rpm := s.deps.UserMsgQueue, s.deps.RPM
	out := &relayv1.UserMsgQueueResponse{}
	switch req.GetOp() {
	case relayv1.UserMsgQueueRequest_OP_ACQUIRE:
		if cache == nil {
			out.Value = 1 // 与本地没有缓存时一样放行（fail-open）
			return out, nil
		}
		acquired, err := cache.AcquireLock(ctx, accountID, req.GetRequestId(), int(req.GetLockTtlMs()))
		if err != nil {
			return nil, err
		}
		if acquired {
			out.Value = 1
		}
	case relayv1.UserMsgQueueRequest_OP_RELEASE:
		if cache == nil {
			return out, nil
		}
		released, err := cache.ReleaseLock(ctx, accountID, req.GetRequestId())
		if err != nil {
			return nil, err
		}
		if released {
			out.Value = 1
		}
	case relayv1.UserMsgQueueRequest_OP_LAST_COMPLETED_MS:
		if cache == nil {
			return out, nil
		}
		ms, err := cache.GetLastCompletedMs(ctx, accountID)
		if err != nil {
			return nil, err
		}
		out.Value = ms
	case relayv1.UserMsgQueueRequest_OP_NOW_MS:
		if cache == nil {
			return out, nil
		}
		ms, err := cache.GetCurrentTimeMs(ctx)
		if err != nil {
			return nil, err
		}
		out.Value = ms
	case relayv1.UserMsgQueueRequest_OP_ACCOUNT_RPM:
		if rpm == nil {
			return out, nil
		}
		n, err := rpm.GetRPM(ctx, accountID)
		if err != nil {
			return nil, err
		}
		out.Value = int64(n)
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown user message queue operation")
	}
	return out, nil
}
