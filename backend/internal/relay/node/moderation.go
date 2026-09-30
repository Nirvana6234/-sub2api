package node

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// moderationCallTimeout 是一次违规上报、通知调用的最长时间（在审核的后台任务里，不在请求路径上）。
const moderationCallTimeout = 10 * time.Second

// moderationRetryFor 是上报失败后重试多久（短于主节点认可的准入窗口 30 分钟）。
const moderationRetryFor = 20 * time.Minute

// moderationRetryQueue 是待重试的上报上限；满了丢最旧的（记错误）。
const moderationRetryQueue = 10000

// RemoteModerationActions 是从节点上内容审核命中后的账号动作（service.ContentModerationAccountActions，设计 3.4）：
// 判定和审核记录在从节点，累计违规次数、封号、通知邮件经主节点用单机同一段代码执行。
//
// 上报调不通时与单机"违规次数查不到"一样按第 1 次记、不封号，并在后台重试（同一个幂等键，主节点只记一次），
// 保证这次违规最终计入；从节点重启会丢掉还没重试成功的。主节点换了纪元时不重试（幂等结果已丢，重发可能记两次）。
type RemoteModerationActions struct {
	control relayv1.RelayControlClient

	mu      sync.Mutex
	pending []pendingViolation
	wake    chan struct{}
	// retryEvery 是重试间隔（默认 5 秒；测试调短）。
	retryEvery time.Duration
}

type pendingViolation struct {
	key   string
	log   []byte
	since time.Time
}

var _ service.ContentModerationAccountActions = (*RemoteModerationActions)(nil)

// NewRemoteModerationActions 创建上报；ctx 结束时后台重试停止。
func NewRemoteModerationActions(ctx context.Context, client *transport.Client) *RemoteModerationActions {
	return newRemoteModerationActions(ctx, relayv1.NewRelayControlClient(client.Conn(transport.TierControl)), 5*time.Second)
}

func newRemoteModerationActions(ctx context.Context, control relayv1.RelayControlClient, retryEvery time.Duration) *RemoteModerationActions {
	r := &RemoteModerationActions{control: control, wake: make(chan struct{}, 1), retryEvery: retryEvery}
	go r.retryLoop(ctx)
	return r
}

func flaggedWithUser(log *service.ContentModerationLog) bool {
	return log != nil && log.Flagged && log.UserID != nil && *log.UserID > 0
}

// Apply 见 service.ContentModerationAccountActions。
func (r *RemoteModerationActions) Apply(ctx context.Context, _ *service.ContentModerationConfig, log *service.ContentModerationLog) bool {
	if !flaggedWithUser(log) {
		return false
	}
	raw, err := json.Marshal(log)
	if err != nil {
		slog.Error("relay moderation violation: encode", "error", err)
		return false
	}
	key := "moderation/" + NewRequestID()
	resp, err := r.report(ctx, key, raw)
	if err != nil {
		slog.Warn("relay moderation violation report failed; will retry", "user_id", *log.UserID, "error", err)
		log.ViolationCount = 1
		r.enqueue(pendingViolation{key: key, log: raw, since: time.Now()})
		return false
	}
	log.ViolationCount = int(resp.GetViolationCount())
	log.AutoBanned = resp.GetAutoBanned()
	return resp.GetAutoBanJustApplied()
}

// Notify 见 service.ContentModerationAccountActions（尽力而为：单机发信失败同样不重发）。
func (r *RemoteModerationActions) Notify(ctx context.Context, _ *service.ContentModerationConfig, log *service.ContentModerationLog, autoBanJustApplied, cyber bool) bool {
	if !flaggedWithUser(log) {
		return false
	}
	raw, err := json.Marshal(log)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), moderationCallTimeout)
	defer cancel()
	resp, err := r.control.ModerationNotify(ctx, &relayv1.ModerationNotifyRequest{Log: raw, AutoBanJustApplied: autoBanJustApplied, Cyber: cyber})
	if err != nil {
		slog.Warn("relay moderation notify failed", "user_id", *log.UserID, "error", err)
		return false
	}
	return resp.GetEmailSent()
}

func (r *RemoteModerationActions) report(ctx context.Context, key string, raw []byte) (*relayv1.ModerationViolationResponse, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), moderationCallTimeout)
	defer cancel()
	return r.control.ModerationViolation(transport.WithIdempotencyKey(ctx, key), &relayv1.ModerationViolationRequest{Log: raw})
}

func (r *RemoteModerationActions) enqueue(p pendingViolation) {
	r.mu.Lock()
	if len(r.pending) >= moderationRetryQueue {
		slog.Error("relay moderation retry queue is full; dropping the oldest violation report")
		r.pending = r.pending[1:]
	}
	r.pending = append(r.pending, p)
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *RemoteModerationActions) retryLoop(ctx context.Context) {
	t := time.NewTicker(r.retryEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.wake:
			// 刚失败的等下一轮再试。
			continue
		}
		r.mu.Lock()
		batch := r.pending
		r.pending = nil
		r.mu.Unlock()
		var keep []pendingViolation
		for _, p := range batch {
			if time.Since(p.since) > moderationRetryFor {
				slog.Error("relay moderation violation report gave up", "key", p.key)
				continue
			}
			_, err := r.report(ctx, p.key, p.log)
			switch {
			case err == nil:
			case errors.Is(err, transport.ErrEpochChanged), isPermanent(err):
				slog.Error("relay moderation violation report dropped", "key", p.key, "error", err)
			default:
				keep = append(keep, p)
			}
		}
		if len(keep) > 0 {
			r.mu.Lock()
			r.pending = append(keep, r.pending...)
			r.mu.Unlock()
		}
	}
}

// isPermanent：主节点明确拒绝（用户不是这台准入的、记录不对），重试也没用。
func isPermanent(err error) bool {
	switch status.Code(err) {
	case codes.PermissionDenied, codes.InvalidArgument:
		return true
	}
	return false
}
