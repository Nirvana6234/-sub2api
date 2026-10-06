package master

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Control 实现 RelayControl 服务（控制连接上的同步调用）。选号、额度等由后续工作包加入。
type Control struct {
	relayv1.UnimplementedRelayControlServer
	config *ConfigPublisher
	quota  *quotaControl

	selector      Selector
	selectorEpoch string

	heartbeats    *Heartbeats
	heartbeatInfo func(ctx context.Context, nodeID int64) (version string, draining bool)
}

// NewControl 创建控制服务。
func NewControl(config *ConfigPublisher) *Control { return &Control{config: config} }

// Ping 用于连通性和时钟偏差检查。
func (c *Control) Ping(_ context.Context, req *relayv1.PingRequest) (*relayv1.PingResponse, error) {
	return &relayv1.PingResponse{Payload: req.GetPayload(), MasterTimeUnixMs: time.Now().UnixMilli()}, nil
}

// FetchConfig 返回调用方节点的配置快照。
func (c *Control) FetchConfig(ctx context.Context, req *relayv1.FetchConfigRequest) (*relayv1.ConfigSnapshot, error) {
	peer, ok := transport.PeerFromContext(ctx)
	if !ok || peer.Class != transport.PeerIssued {
		return nil, status.Error(codes.PermissionDenied, "config requires an issued certificate")
	}
	return c.config.FetchConfig(ctx, peer.NodeID, req.GetKnownVersion())
}
