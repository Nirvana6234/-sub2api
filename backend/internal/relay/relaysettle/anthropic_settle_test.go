package relaysettle

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// useAnthropicGateway 给测试世界装上 Anthropic 的网关服务（与 OpenAI 的共用记录入账的假仓储）。
func (w *world) useAnthropicGateway() *service.GatewayService {
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	gw := service.NewGatewayService(nil, nil, w.logs, w.billing, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, &service.BillingCacheService{}, nil, nil, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	w.settler.deps.AnthropicGateway = gw
	return gw
}

func (w *world) anthropicVoucher(t *testing.T) []byte {
	t.Helper()
	raw, _, err := sign.IssueVoucher(w.signer, &relayv1.Voucher{
		NodeId: node, SelectionId: "sel-a", UserId: 3, ApiKeyId: 11, AccountId: 7, GroupId: 5,
		BillingMode: relayv1.BillingMode_BILLING_MODE_BALANCE, RequestedModel: "claude-sonnet-4-5", AllowedBillingModels: []string{"claude-sonnet-4-5"},
		Context: &relayv1.SelectionContext{
			PricingAtUnixMs: pricingAt.UnixMilli(), QuotaPlatform: service.PlatformAnthropic,
			ChannelId: 4, ChannelMappedModel: "claude-sonnet-4-5", BillingModelSource: service.BillingModelSourceRequested,
			FallbackSourceGroupId: 20, FallbackSourceGroupName: "free", FallbackTargetGroupId: 5, FallbackTargetGroupName: "plus",
		},
	}, time.Now())
	require.NoError(t, err)
	return raw
}

func anthropicForwardResult() *service.ForwardResult {
	effort := "high"
	return &service.ForwardResult{
		RequestID: "msg_parity", Model: "claude-sonnet-4-5", Duration: 1500 * time.Millisecond, Stream: true,
		Usage:           service.ClaudeUsage{InputTokens: 1200, OutputTokens: 340, CacheCreationInputTokens: 80, CacheReadInputTokens: 200},
		ReasoningEffort: &effort,
	}
}

// Anthropic Messages：主从入账与单机入账（处理函数构造的 RecordUsageInput）写出同样的扣费命令和用量记录，
// 包括换号时强制按缓存计费（设计 5.2 的验收方式）。
func TestRelayAnthropicSettlementMatchesLocalBilling(t *testing.T) {
	w := newWorld(t)
	gw := w.useAnthropicGateway()

	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "creq-1")
	ctx = context.WithValue(ctx, ctxkey.RequestID, "req-1")
	ctx = service.WithFallbackPoolTrace(ctx, service.FallbackPoolTrace{SourceGroupID: 20, SourceGroupName: "free", TargetGroupID: 5, TargetGroupName: "plus"})
	account := w.account
	mapping := service.ChannelMappingResult{ChannelID: 4, MappedModel: "claude-sonnet-4-5", BillingModelSource: service.BillingModelSourceRequested}
	result := anthropicForwardResult()
	require.NoError(t, gw.RecordUsage(ctx, &service.RecordUsageInput{
		Result: result, APIKey: w.key, User: w.key.User, Account: &account, PricingAt: pricingAt,
		InboundEndpoint: "/v1/messages", UpstreamEndpoint: "/v1/messages", UserAgent: "claude-cli/2.1", IPAddress: "5.6.7.8",
		SessionID: "sess-1", RequestPayloadHash: "hash-1", ForceCacheBilling: true, QuotaPlatform: service.PlatformAnthropic,
		ChannelUsageFields: mapping.ToUsageFields("claude-sonnet-4-5", result.UpstreamModel),
	}))
	require.Len(t, w.billing.commands, 1)
	require.Len(t, w.logs.logs, 1)

	raw, err := json.Marshal(anthropicForwardResult())
	require.NoError(t, err)
	res := w.settler.Settle(context.Background(), node, &relayv1.UsageRecord{
		Seq: 1, Voucher: w.anthropicVoucher(t), Kind: relayv1.UsageRecordKind_USAGE_RECORD_KIND_ANTHROPIC, ResultJson: raw,
		InboundEndpoint: "/v1/messages", UpstreamEndpoint: "/v1/messages", UserAgent: "claude-cli/2.1", IpAddress: "5.6.7.8",
		SessionId: "sess-1", RequestPayloadHash: "hash-1", ClientRequestId: "creq-1", RequestId: "req-1", ForceCacheBilling: true,
	})
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, res.GetStatus(), res.GetReason())
	require.Len(t, w.billing.commands, 2)
	require.Len(t, w.logs.logs, 2)

	require.Equal(t, w.billing.commands[0], w.billing.commands[1], "same billing command")
	require.Greater(t, w.billing.commands[1].BalanceCost, 0.0)
	require.Equal(t, node, *w.logs.logs[1].NodeID)
	relayed := *w.logs.logs[1]
	relayed.NodeID = nil
	require.Equal(t, comparableLog(w.logs.logs[0]), comparableLog(&relayed), "same usage log apart from the node")
	require.Equal(t, 0, w.logs.logs[1].InputTokens, "forced cache billing moves input tokens to cache reads")
}

// 没有装 Anthropic 网关服务的主节点不收这种记录（隔离，不重试）。
func TestRelayAnthropicSettlementNeedsTheGateway(t *testing.T) {
	w := newWorld(t)
	raw, err := json.Marshal(anthropicForwardResult())
	require.NoError(t, err)
	res := w.settler.Settle(context.Background(), node, &relayv1.UsageRecord{
		Seq: 1, Voucher: w.anthropicVoucher(t), Kind: relayv1.UsageRecordKind_USAGE_RECORD_KIND_ANTHROPIC, ResultJson: raw,
	})
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED, res.GetStatus())
}

// Gemini 原生入口的记录（同一种记录种类）：开着渠道映射时主从入账与单机入账一致，上报的模型（转发模型、上游模型）都在凭证允许
// 的范围内，不会被记成待复核。
func TestRelayGeminiNativeSettlementMatchesLocalBilling(t *testing.T) {
	w := newWorld(t)
	gw := w.useAnthropicGateway()
	account := w.account
	mapping := service.ChannelMappingResult{ChannelID: 4, MappedModel: "gemini-2.5-flash", Mapped: true, BillingModelSource: service.BillingModelSourceRequested}
	forward := func() *service.ForwardResult {
		return &service.ForwardResult{
			RequestID: "gem_parity", Model: "gemini-2.5-flash", UpstreamModel: "gemini-2.5-flash", Duration: 900 * time.Millisecond,
			Usage: service.ClaudeUsage{InputTokens: 700, OutputTokens: 120, CacheReadInputTokens: 50},
		}
	}
	result := forward()
	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "req-g")
	require.NoError(t, gw.RecordUsage(ctx, &service.RecordUsageInput{
		Result: result, APIKey: w.key, User: w.key.User, Account: &account, PricingAt: pricingAt,
		InboundEndpoint: "/v1beta/models", UpstreamEndpoint: "/v1beta/models/*action", UserAgent: "gemini-cli", IPAddress: "5.6.7.8",
		SessionID: "sess-g", RequestPayloadHash: "hash-g", QuotaPlatform: service.PlatformGemini,
		ChannelUsageFields: mapping.ToUsageFields("gemini-2.5-pro", result.UpstreamModel),
	}))
	require.Len(t, w.billing.commands, 1)

	voucher, _, err := sign.IssueVoucher(w.signer, &relayv1.Voucher{
		NodeId: node, SelectionId: "sel-g", UserId: 3, ApiKeyId: 11, AccountId: 7, GroupId: 5,
		BillingMode: relayv1.BillingMode_BILLING_MODE_BALANCE, RequestedModel: "gemini-2.5-pro", AllowedBillingModels: []string{"gemini-2.5-pro", "gemini-2.5-flash"},
		Context: &relayv1.SelectionContext{
			PricingAtUnixMs: pricingAt.UnixMilli(), QuotaPlatform: service.PlatformGemini,
			ChannelId: 4, ChannelMapped: true, ChannelMappedModel: "gemini-2.5-flash", BillingModelSource: service.BillingModelSourceRequested,
		},
	}, time.Now())
	require.NoError(t, err)
	raw, err := json.Marshal(forward())
	require.NoError(t, err)
	res := w.settler.Settle(context.Background(), node, &relayv1.UsageRecord{
		Seq: 1, Voucher: voucher, Kind: relayv1.UsageRecordKind_USAGE_RECORD_KIND_ANTHROPIC, ResultJson: raw,
		InboundEndpoint: "/v1beta/models", UpstreamEndpoint: "/v1beta/models/*action", UserAgent: "gemini-cli", IpAddress: "5.6.7.8",
		SessionId: "sess-g", RequestPayloadHash: "hash-g", RequestId: "req-g",
	})
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, res.GetStatus(), res.GetReason())
	require.Len(t, w.billing.commands, 2)
	require.Equal(t, w.billing.commands[0], w.billing.commands[1], "same billing command")
	relayed := *w.logs.logs[1]
	relayed.NodeID = nil
	require.Equal(t, comparableLog(w.logs.logs[0]), comparableLog(&relayed), "same usage log apart from the node")
	require.NotContains(t, w.billing.reviews, "pending_review", "the reported models are inside the voucher")
}
