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
	// Admit 准入：按本地中间件链复查 API Key，通过时回 Key 快照（设计 3.2）。被拒绝时返回带 rejection 的回复。
	Admit(ctx context.Context, nodeID int64, req *relayv1.AdmitRequest) (*relayv1.AdmitResponse, error)
	// Select 被拒绝时返回带 rejection 的回复，不返回 error；error 只表示主节点自身的故障。
	Select(ctx context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error)
	FetchCredentials(ctx context.Context, nodeID int64, req *relayv1.FetchCredentialsRequest) (*relayv1.FetchCredentialsResponse, error)
	RefillQuota(ctx context.Context, nodeID int64, req *relayv1.RefillQuotaRequest) (*relayv1.RefillQuotaResponse, error)
	// UpstreamError 上游错误决策（设计 3.1 第 10 步）。账号不是这台节点正在用的时返回 ErrSelectionNotFound。
	UpstreamError(ctx context.Context, nodeID int64, req *relayv1.UpstreamErrorRequest) (*relayv1.UpstreamErrorResponse, error)
	// BeginTurn / TurnMapping：Responses WebSocket 的一轮（开发计划 WP10-3）。选号不是这台节点进行中的时返回
	// ErrSelectionNotFound。
	BeginTurn(ctx context.Context, nodeID int64, req *relayv1.BeginTurnRequest) (*relayv1.BeginTurnResponse, error)
	TurnMapping(ctx context.Context, nodeID int64, req *relayv1.TurnMappingRequest) (*relayv1.TurnMappingResponse, error)
	// WebSocketLease 每个 Key 的 WebSocket 连接数租约。
	WebSocketLease(ctx context.Context, nodeID int64, req *relayv1.WebSocketLeaseRequest) (*relayv1.WebSocketLeaseResponse, error)
	// CyberPolicyHit 上游 cyber 策略命中（设计 3.4）。选号不是这台节点进行中的时返回 ErrSelectionNotFound。
	CyberPolicyHit(ctx context.Context, nodeID int64, req *relayv1.CyberPolicyHitRequest) (*relayv1.CyberPolicyHitResponse, error)
	// ModerationViolation / ModerationNotify 内容审核命中后的账号动作（设计 3.4）。用户不是这台节点最近准入过的
	// 返回 PermissionDenied。
	ModerationViolation(ctx context.Context, nodeID int64, req *relayv1.ModerationViolationRequest) (*relayv1.ModerationViolationResponse, error)
	ModerationNotify(ctx context.Context, nodeID int64, req *relayv1.ModerationNotifyRequest) (*relayv1.ModerationNotifyResponse, error)
	// FetchFlaggedHashes / RecordFlaggedHash 命中过的输入名单的整份拉取与从节点新命中（设计 3.4）。
	FetchFlaggedHashes(ctx context.Context, nodeID int64, req *relayv1.FetchFlaggedHashesRequest) (*relayv1.FetchFlaggedHashesResponse, error)
	RecordFlaggedHash(ctx context.Context, nodeID int64, req *relayv1.RecordFlaggedHashRequest) (*relayv1.RecordFlaggedHashResponse, error)
	// SecurityAudit 转发前的安全审计（设计 3.4，走审核连接）。Key 复查不通过时回 skipped，不返回 error。
	SecurityAudit(ctx context.Context, nodeID int64, req *relayv1.SecurityAuditRequest) (*relayv1.SecurityAuditResponse, error)
	// Release 处理事件连接上的释放消息（不回复，按选号 ID 幂等）。在事件流的接收协程里调用，
	// 不能阻塞：要访问 Redis 等的工作放到自己的协程里做。
	Release(nodeID int64, rel *relayv1.SelectionRelease)
	// AccountEvent 处理事件连接上的账号事件（不回复）。与 Release 一样不能阻塞。
	AccountEvent(nodeID int64, ev *relayv1.AccountEvent)
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
	// VerifyVoucher 验一张扣费凭证（签名、期限、上报节点）。
	VerifyVoucher func(raw []byte, reportingNodeID int64) (*relayv1.Voucher, error)
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

// Admit 准入。只读：不校验纪元，也不需要幂等键。
func (c *Control) Admit(ctx context.Context, req *relayv1.AdmitRequest) (*relayv1.AdmitResponse, error) {
	nodeID, err := c.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.Admit(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "admit", nodeID, err)
	}
	return resp, nil
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
	if err == nil && ctx.Err() != nil && resp.GetSelection() == nil {
		// 调用已取消时的拒绝多半是取消造成的：按错误返回，幂等缓存不会记住它，从节点超时重发时重新判断。
		// 选中的结果照常返回（缓存下来，重发拿到的就是它，槽位和额度不会白占）。
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

// UpstreamError 上游错误决策。
func (c *Control) UpstreamError(ctx context.Context, req *relayv1.UpstreamErrorRequest) (*relayv1.UpstreamErrorResponse, error) {
	nodeID, err := c.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.UpstreamError(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "upstream_error", nodeID, err)
	}
	return resp, nil
}

// BeginTurn Responses WebSocket 的一轮开始。占槽、签凭证：校验纪元。
func (c *Control) BeginTurn(ctx context.Context, req *relayv1.BeginTurnRequest) (*relayv1.BeginTurnResponse, error) {
	nodeID, err := c.selectPeer(ctx, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.BeginTurn(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "begin_turn", nodeID, err)
	}
	return resp, nil
}

// TurnMapping 一轮的渠道映射（只读）。
func (c *Control) TurnMapping(ctx context.Context, req *relayv1.TurnMappingRequest) (*relayv1.TurnMappingResponse, error) {
	nodeID, err := c.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.TurnMapping(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "turn_mapping", nodeID, err)
	}
	return resp, nil
}

// WebSocketLease WebSocket 连接数租约（租约在 Redis 里，跨纪元有效）。
func (c *Control) WebSocketLease(ctx context.Context, req *relayv1.WebSocketLeaseRequest) (*relayv1.WebSocketLeaseResponse, error) {
	nodeID, err := c.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.WebSocketLease(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "websocket_lease", nodeID, err)
	}
	return resp, nil
}

// CyberPolicyHit 上游 cyber 策略命中。
func (c *Control) CyberPolicyHit(ctx context.Context, req *relayv1.CyberPolicyHitRequest) (*relayv1.CyberPolicyHitResponse, error) {
	nodeID, err := c.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.CyberPolicyHit(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "cyber_policy_hit", nodeID, err)
	}
	return resp, nil
}

// ModerationViolation 内容审核命中后累计违规、判封号。
func (c *Control) ModerationViolation(ctx context.Context, req *relayv1.ModerationViolationRequest) (*relayv1.ModerationViolationResponse, error) {
	nodeID, err := c.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.ModerationViolation(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "moderation_violation", nodeID, err)
	}
	return resp, nil
}

// ModerationNotify 内容审核命中与封号的通知邮件。
func (c *Control) ModerationNotify(ctx context.Context, req *relayv1.ModerationNotifyRequest) (*relayv1.ModerationNotifyResponse, error) {
	nodeID, err := c.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.ModerationNotify(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "moderation_notify", nodeID, err)
	}
	return resp, nil
}

// FetchFlaggedHashes 分页拉取命中过的输入名单。
func (c *Control) FetchFlaggedHashes(ctx context.Context, req *relayv1.FetchFlaggedHashesRequest) (*relayv1.FetchFlaggedHashesResponse, error) {
	nodeID, err := c.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.FetchFlaggedHashes(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "fetch_flagged_hashes", nodeID, err)
	}
	return resp, nil
}

// RecordFlaggedHash 从节点新命中的输入。
func (c *Control) RecordFlaggedHash(ctx context.Context, req *relayv1.RecordFlaggedHashRequest) (*relayv1.RecordFlaggedHashResponse, error) {
	nodeID, err := c.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.selector.RecordFlaggedHash(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "record_flagged_hash", nodeID, err)
	}
	return resp, nil
}

// ModerationServer 是审核连接上的服务（设计 3.4）：同一个选号实现，走单独的连接和限流类别，
// 审核慢时不挤占选号。
type ModerationServer struct {
	relayv1.UnimplementedRelayModerationServer
	control *Control
}

// NewModerationServer 创建审核连接上的服务。
func NewModerationServer(control *Control) *ModerationServer {
	return &ModerationServer{control: control}
}

// SecurityAudit 转发前的安全审计。只读（审核记录、累计封号是审计本身的副作用，与单机一样每次判定一次）：
// 不校验纪元，不带幂等键。
func (m *ModerationServer) SecurityAudit(ctx context.Context, req *relayv1.SecurityAuditRequest) (*relayv1.SecurityAuditResponse, error) {
	nodeID, err := m.control.selectPeer(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := m.control.selector.SecurityAudit(ctx, nodeID, req)
	if err != nil {
		return nil, selectionError(ctx, "security_audit", nodeID, err)
	}
	return resp, nil
}

// RouteNodeEvents 把事件连接上的释放消息和账号事件交给选号实现。
func RouteNodeEvents(events *EventHub, s Selector) {
	events.OnNodeEvent = func(nodeID int64, env *relayv1.NodeEnvelope) {
		switch body := env.Body.(type) {
		case *relayv1.NodeEnvelope_SelectionRelease:
			s.Release(nodeID, body.SelectionRelease)
		case *relayv1.NodeEnvelope_AccountEvent:
			s.AccountEvent(nodeID, body.AccountEvent)
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
