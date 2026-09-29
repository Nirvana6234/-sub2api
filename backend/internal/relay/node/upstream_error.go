package node

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// upstreamErrorBodyLimit 是随上游错误决策发送的响应体上限（转发代码读错误体默认只读 512KB）。
const upstreamErrorBodyLimit = 1 << 20

type selectionIDKey struct{}

// WithSelectionID 把这次尝试的选号 ID 放进转发用的 ctx（上游错误决策带上它）。
func WithSelectionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, selectionIDKey{}, id)
}

// SelectionIDFrom 取 ctx 里这次尝试的选号 ID（没有时为空）。
func SelectionIDFrom(ctx context.Context) string { return selectionIDFrom(ctx) }

func selectionIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(selectionIDKey{}).(string)
	return id
}

// RemoteUpstreamErrorDecider 是从节点上的上游错误判定：同步问主节点，主节点用单机同一段代码判定并
// 记录账号状态（设计 3.1 第 10 步）。装在从节点的 OpenAIGatewayService 上（SetUpstreamErrorDecider）。
//
// 主节点不可达时按"不改变账号状态"回答（不停用、不同账号重试、没命中错误策略）。
type RemoteUpstreamErrorDecider struct {
	control relayv1.RelayControlClient
}

var _ service.OpenAIUpstreamErrorDecider = (*RemoteUpstreamErrorDecider)(nil)

// NewRemoteUpstreamErrorDecider 创建远端判定。
func NewRemoteUpstreamErrorDecider(client *transport.Client) *RemoteUpstreamErrorDecider {
	return &RemoteUpstreamErrorDecider{control: relayv1.NewRelayControlClient(client.Conn(transport.TierControl))}
}

func (d *RemoteUpstreamErrorDecider) call(ctx context.Context, req *relayv1.UpstreamErrorRequest) *relayv1.UpstreamErrorResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	req.SelectionId = selectionIDFrom(ctx)
	flags := service.OpenAIUpstreamErrorFlagsFromContext(ctx)
	req.ImagesSelfBuilt, req.ImagesEndpoint = flags.ImagesSelfBuilt, flags.ImagesEndpoint
	// 客户端断开也要把判定做完（单机里账号状态的写入同样不跟客户端的连接走）。
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	resp, err := d.control.UpstreamError(cctx, req)
	if err != nil {
		slog.Warn("relay upstream error decision failed", "kind", req.GetKind().String(), "account_id", req.GetAccountId(), "error", err)
		return &relayv1.UpstreamErrorResponse{}
	}
	return resp
}

func accountID(account *service.Account) int64 {
	if account == nil {
		return 0
	}
	return account.ID
}

// HandleUpstreamError 见 service.OpenAIUpstreamErrorDecider。
func (d *RemoteUpstreamErrorDecider) HandleUpstreamError(ctx context.Context, account *service.Account, statusCode int, headers http.Header, body []byte, canonicalModel ...string) bool {
	if account == nil {
		return false
	}
	req := &relayv1.UpstreamErrorRequest{
		Kind: relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RESPONSE, AccountId: account.ID, StatusCode: int32(statusCode),
		Headers: headersToProto(headers), Body: capBody(body),
	}
	if len(canonicalModel) > 0 {
		req.Model, req.HasModel = canonicalModel[0], true
	}
	return d.call(ctx, req).GetShouldDisable()
}

// OAuth429SameAccountRetry 见 service.OpenAIUpstreamErrorDecider。
func (d *RemoteUpstreamErrorDecider) OAuth429SameAccountRetry(ctx context.Context, account *service.Account) (bool, time.Time) {
	resp := d.call(ctx, &relayv1.UpstreamErrorRequest{Kind: relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_OAUTH429_RETRY, AccountId: accountID(account)})
	if !resp.GetRetrySameAccount() {
		return false, time.Time{}
	}
	var deadline time.Time
	if ms := resp.GetRetryDeadlineUnixMs(); ms > 0 {
		deadline = time.UnixMilli(ms)
	}
	return true, deadline
}

// HandleStreamTimeout 见 service.OpenAIUpstreamErrorDecider。
func (d *RemoteUpstreamErrorDecider) HandleStreamTimeout(ctx context.Context, account *service.Account, model string) bool {
	if account == nil {
		return false
	}
	return d.call(ctx, &relayv1.UpstreamErrorRequest{
		Kind: relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_STREAM_TIMEOUT, AccountId: account.ID, Model: model, HasModel: true,
	}).GetShouldDisable()
}

// HandleRateLimitError 见 service.OpenAIUpstreamErrorDecider。
func (d *RemoteUpstreamErrorDecider) HandleRateLimitError(ctx context.Context, account *service.Account, statusCode int, headers http.Header, body []byte, requestedModel ...string) bool {
	if account == nil {
		return false
	}
	req := &relayv1.UpstreamErrorRequest{
		Kind: relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RATE_LIMIT, AccountId: account.ID, StatusCode: int32(statusCode),
		Headers: headersToProto(headers), Body: capBody(body),
	}
	if len(requestedModel) > 0 {
		req.Model, req.HasModel = requestedModel[0], true
	}
	return d.call(ctx, req).GetShouldDisable()
}

// CheckErrorPolicy 见 service.OpenAIUpstreamErrorDecider。
func (d *RemoteUpstreamErrorDecider) CheckErrorPolicy(ctx context.Context, account *service.Account, statusCode int, body []byte, model string) service.ErrorPolicyResult {
	if account == nil {
		return service.ErrorPolicyNone
	}
	return service.ErrorPolicyResult(d.call(ctx, &relayv1.UpstreamErrorRequest{
		Kind: relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_ERROR_POLICY, AccountId: account.ID, StatusCode: int32(statusCode),
		Body: capBody(body), Model: model, HasModel: true,
	}).GetErrorPolicy())
}

func headersToProto(h http.Header) []*relayv1.HeaderValues {
	out := make([]*relayv1.HeaderValues, 0, len(h))
	for name, values := range h {
		if strings.EqualFold(name, "Set-Cookie") {
			continue
		}
		out = append(out, &relayv1.HeaderValues{Name: name, Values: values})
	}
	return out
}

func capBody(b []byte) []byte {
	if len(b) > upstreamErrorBodyLimit {
		return b[:upstreamErrorBodyLimit]
	}
	return b
}
