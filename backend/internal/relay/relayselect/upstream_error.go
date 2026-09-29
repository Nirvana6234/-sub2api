package relayselect

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UpstreamError 上游错误决策：用单机同一段代码（service.OpenAIGatewayService.LocalUpstreamErrorDecider）
// 判定并记录账号状态。账号必须是这台节点正在用的（有进行中的选号），用选号记录里的账号对象，
// 与单机转发时用选中的账号对象判定一致。
func (s *selector) UpstreamError(ctx context.Context, nodeID int64, req *relayv1.UpstreamErrorRequest) (*relayv1.UpstreamErrorResponse, error) {
	account := s.accountInUse(nodeID, req.GetSelectionId(), req.GetAccountId())
	if account == nil {
		return nil, master.ErrSelectionNotFound
	}
	// 判定会写库、改调度状态：从节点断开也要做完（单机里这些写入同样不跟客户端的连接走）。
	ctx = context.WithoutCancel(ctx)
	ctx = service.WithOpenAIUpstreamErrorFlags(ctx, service.OpenAIUpstreamErrorFlags{
		ImagesSelfBuilt: req.GetImagesSelfBuilt(), ImagesEndpoint: req.GetImagesEndpoint(),
	})
	decider := s.deps.Gateway.LocalUpstreamErrorDecider()
	var models []string
	if req.GetHasModel() {
		models = []string{req.GetModel()}
	}
	resp := &relayv1.UpstreamErrorResponse{}
	switch req.GetKind() {
	case relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RESPONSE:
		resp.ShouldDisable = decider.HandleUpstreamError(ctx, account, int(req.GetStatusCode()), headersFromProto(req.GetHeaders()), req.GetBody(), models...)
	case relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_OAUTH429_RETRY:
		retry, deadline := decider.OAuth429SameAccountRetry(ctx, account)
		resp.RetrySameAccount = retry
		if !deadline.IsZero() {
			resp.RetryDeadlineUnixMs = deadline.UnixMilli()
		}
	case relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_STREAM_TIMEOUT:
		resp.ShouldDisable = decider.HandleStreamTimeout(ctx, account, req.GetModel())
	case relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_ERROR_POLICY:
		resp.ErrorPolicy = int32(decider.CheckErrorPolicy(ctx, account, int(req.GetStatusCode()), req.GetBody(), req.GetModel()))
	case relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RATE_LIMIT:
		resp.ShouldDisable = decider.HandleRateLimitError(ctx, account, int(req.GetStatusCode()), headersFromProto(req.GetHeaders()), req.GetBody(), models...)
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown upstream error kind")
	}
	return resp, nil
}

// accountInUse 返回这台节点正在用的账号：给了选号 ID 时必须是它且账号一致，否则找这台节点任一个
// 进行中的、选中这个账号的选号。
func (s *selector) accountInUse(nodeID int64, selectionID string, accountID int64) *service.Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	if selectionID != "" {
		sel, ok := s.selections[selectionID]
		if !ok || sel.nodeID != nodeID || sel.account == nil || sel.account.ID != accountID {
			return nil
		}
		return sel.account
	}
	for _, sel := range s.selections {
		if sel.nodeID == nodeID && sel.account != nil && sel.account.ID == accountID {
			return sel.account
		}
	}
	return nil
}

func headersFromProto(list []*relayv1.HeaderValues) http.Header {
	h := make(http.Header, len(list))
	for _, v := range list {
		for _, value := range v.GetValues() {
			h.Add(v.GetName(), value)
		}
	}
	return h
}
