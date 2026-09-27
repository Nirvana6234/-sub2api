package master

import (
	"context"
	"crypto/ecdh"
	"errors"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Selector 是主节点的选号实现（设计 3.1 第 5 步、开发计划 WP7）。
//
// 实现在 internal/relay/relayselect：它要复用 handler 里的选号与准入代码，而 master 不能导入
// handler（handler → handler/admin → master 会循环），所以经 relaywire 注入。
// 运行时每次启动用 RuntimeDeps.NewSelector 建一个，停止时 Close。
type Selector interface {
	// Select 被拒绝时返回带 rejection 的回复，不返回 error；error 只表示主节点自身的故障。
	Select(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error)
	FetchCredentials(ctx context.Context, nodeID int64, req *relayv1.FetchCredentialsRequest) (*relayv1.FetchCredentialsResponse, error)
	RefillQuota(ctx context.Context, nodeID int64, req *relayv1.RefillQuotaRequest) (*relayv1.RefillQuotaResponse, error)
	// Release 处理事件连接上的释放消息（不回复，按选号 ID 幂等）。在事件流的接收协程里调用，
	// 不能阻塞：要访问 Redis 等的工作放到自己的协程里做。
	Release(nodeID int64, rel *relayv1.SelectionRelease)
	// Close 在运行时停止时调用：放掉还占着的并发槽。
	Close()
}

// ErrSelectionNotFound：选号已释放、已过期或不属于这台节点。
var ErrSelectionNotFound = errors.New("relay selection not found")

// SelectEnv 是选号实现从运行时拿到的东西（本次启动有效）。
type SelectEnv struct {
	// Epoch 是本次启动的纪元。
	Epoch string
	// Quotas 是额度服务；没有租约存储时为 nil（测试）。
	Quotas *Quotas
	// IssueVoucher 签发扣费凭证，返回 SignedToken 的编码和实际签入的内容。
	IssueVoucher func(v *relayv1.Voucher) ([]byte, *relayv1.Voucher, error)
	// NodeEncryptionKey 返回节点当前的加密公钥（上游凭据用它加密，设计 7.1）。
	NodeEncryptionKey func(nodeID int64) (*ecdh.PublicKey, bool)
	// ConfigVersion 返回节点当前的配置版本（选号回复里带着，设计 6.2）。
	ConfigVersion func(ctx context.Context, nodeID int64) (string, error)
}

// AttachSelector 挂上选号实现（运行时启动时）。
func (c *Control) AttachSelector(s Selector, epoch string) {
	c.selector = s
	c.selectorEpoch = epoch
}

// selectPeer 取调用方节点：签发证书、选号服务已就绪；checkEpoch 时还要带着当前纪元
// （选号占槽、补额度动状态，跨纪元一律重来；取凭据只读，选号 ID 跨纪元自然查不到）。
func (c *Control) selectPeer(ctx context.Context, checkEpoch bool) (int64, error) {
	peer, ok := transport.PeerFromContext(ctx)
	if !ok || peer.Class != transport.PeerIssued {
		return 0, status.Error(codes.PermissionDenied, "selection requires an issued certificate")
	}
	if c.selector == nil {
		return 0, status.Error(codes.Unavailable, "relay selection is not available")
	}
	if checkEpoch && transport.IncomingEpoch(ctx) != c.selectorEpoch {
		return 0, transport.EpochMismatch(ctx)
	}
	return peer.NodeID, nil
}

// Select 选号。
func (c *Control) Select(ctx context.Context, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	nodeID, err := c.selectPeer(ctx, true)
	if err != nil {
		return nil, err
	}
	if req.GetRequestId() == "" || req.GetAttempt() == 0 {
		return nil, status.Error(codes.InvalidArgument, "request_id and attempt are required")
	}
	resp, err := c.selector.Select(ctx, nodeID, req)
	if err == nil && ctx.Err() != nil {
		// 调用已取消：不返回成功结果，否则幂等缓存会把它记下来，从节点超时重发时拿到的是它。
		err = ctx.Err()
	}
	if err != nil {
		return nil, selectionError(ctx, "select", nodeID, err)
	}
	return resp, nil
}

// FetchCredentials 取进行中的选号所选账号的上游凭据。
func (c *Control) FetchCredentials(ctx context.Context, req *relayv1.FetchCredentialsRequest) (*relayv1.FetchCredentialsResponse, error) {
	nodeID, err := c.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.FetchCredentials(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "fetch_credentials", nodeID, err)
	}
	return resp, nil
}

// RefillQuota 按进行中的选号补充额度。
func (c *Control) RefillQuota(ctx context.Context, req *relayv1.RefillQuotaRequest) (*relayv1.RefillQuotaResponse, error) {
	nodeID, err := c.selectPeer(ctx, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.RefillQuota(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "refill_quota", nodeID, err)
	}
	return resp, nil
}

// RouteSelectionReleases 把事件连接上的释放消息交给选号实现。
func RouteSelectionReleases(events *EventHub, s Selector) {
	events.OnNodeEvent = func(nodeID int64, env *relayv1.NodeEnvelope) {
		if rel := env.GetSelectionRelease(); rel != nil {
			s.Release(nodeID, rel)
		}
	}
}

// selectionError 把选号实现的错误转成 gRPC 状态：已是状态的原样返回，选号不存在为 NotFound，
// 其余为 Unavailable（不把内部错误内容发给从节点）。
func selectionError(ctx context.Context, op string, nodeID int64, err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	if errors.Is(err, ErrSelectionNotFound) {
		return status.Error(codes.NotFound, "relay selection not found")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if ctx.Err() != nil {
			return status.FromContextError(ctx.Err()).Err()
		}
	}
	slog.Warn("relay selection call failed", "op", op, "node_id", nodeID, "error", err)
	return status.Error(codes.Unavailable, "relay selection failed")
}
