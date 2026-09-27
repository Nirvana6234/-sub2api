package node

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

// BillingClient 提交扣费批次（扣费连接，设计 5.1）。
type BillingClient struct {
	billing relayv1.RelayBillingClient
}

// NewBillingClient 创建扣费客户端。
func NewBillingClient(client *transport.Client) *BillingClient {
	return &BillingClient{billing: relayv1.NewRelayBillingClient(client.Conn(transport.TierBilling))}
}

// Submit 提交一批扣费记录，返回逐条的入账结果。没收到确认时整批重发即可（按凭证去重）。
func (b *BillingClient) Submit(ctx context.Context, batch *relayv1.UsageBatch) (*relayv1.UsageBatchAck, error) {
	return b.billing.SubmitUsage(ctx, batch)
}
