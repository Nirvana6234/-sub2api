package nodegw

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// userMsgQueueCall 是经主节点执行用户消息串行队列一步的调用（node.SelectClient.UserMsgQueue）。
type userMsgQueueCall func(ctx context.Context, req *relayv1.UserMsgQueueRequest) (*relayv1.UserMsgQueueResponse, error)

// remoteUserMsgQueue 是从节点上的用户消息串行队列缓存（service.UserMsgQueueCache）：锁和完成时间在主节点的 Redis，
// 每一步问主节点。出错时与本地 Redis 出错一样由队列服务放行（fail-open）。
type remoteUserMsgQueue struct {
	call userMsgQueueCall
}

var _ service.UserMsgQueueCache = remoteUserMsgQueue{}

func (r remoteUserMsgQueue) AcquireLock(ctx context.Context, accountID int64, requestID string, lockTTLMs int) (bool, error) {
	resp, err := r.call(ctx, &relayv1.UserMsgQueueRequest{Op: relayv1.UserMsgQueueRequest_OP_ACQUIRE, AccountId: accountID, RequestId: requestID, LockTtlMs: int32(lockTTLMs)})
	if err != nil {
		return false, err
	}
	return resp.GetValue() == 1, nil
}

func (r remoteUserMsgQueue) ReleaseLock(ctx context.Context, accountID int64, requestID string) (bool, error) {
	resp, err := r.call(ctx, &relayv1.UserMsgQueueRequest{Op: relayv1.UserMsgQueueRequest_OP_RELEASE, AccountId: accountID, RequestId: requestID})
	if err != nil {
		return false, err
	}
	return resp.GetValue() == 1, nil
}

func (r remoteUserMsgQueue) GetLastCompletedMs(ctx context.Context, accountID int64) (int64, error) {
	resp, err := r.call(ctx, &relayv1.UserMsgQueueRequest{Op: relayv1.UserMsgQueueRequest_OP_LAST_COMPLETED_MS, AccountId: accountID})
	if err != nil {
		return 0, err
	}
	return resp.GetValue(), nil
}

func (r remoteUserMsgQueue) GetCurrentTimeMs(ctx context.Context) (int64, error) {
	resp, err := r.call(ctx, &relayv1.UserMsgQueueRequest{Op: relayv1.UserMsgQueueRequest_OP_NOW_MS})
	if err != nil {
		return 0, err
	}
	return resp.GetValue(), nil
}

// ReconcileExpiredLockCandidates：孤儿锁清理在主节点跑，从节点不起清理任务。
func (remoteUserMsgQueue) ReconcileExpiredLockCandidates(context.Context, int) (int, error) {
	return 0, nil
}

// remoteRPM 是从节点上排队代码读账号当前 RPM 用的计数（service.RPMCache）。计数的递增在主节点（转发成功的释放），
// 从节点的转发服务没有 RPM 计数，只有排队代码读它。
type remoteRPM struct {
	service.RPMCache
	call userMsgQueueCall
}

func (r remoteRPM) GetRPM(ctx context.Context, accountID int64) (int, error) {
	resp, err := r.call(ctx, &relayv1.UserMsgQueueRequest{Op: relayv1.UserMsgQueueRequest_OP_ACCOUNT_RPM, AccountId: accountID})
	if err != nil {
		return 0, err
	}
	return int(resp.GetValue()), nil
}
