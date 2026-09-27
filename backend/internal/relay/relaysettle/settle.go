// Package relaysettle 是主节点的扣费入账（设计第 5 节、开发计划 WP8）：把从节点上报的扣费记录
// 交给单机同一个入账函数（service.OpenAIGatewayService.RecordUsage），不另写简化版。
//
// 价格、倍率按主节点入账时的配置算，与单机一致（单机也是请求结束后异步入账、读当时的配置，
// 只有计价时间在请求开始时定下）；选号定下的值从凭证的选号上下文恢复，只有转发节点知道的事实
// 取自记录（字段清单见 relayselect 的字段守卫）。
package relaysettle

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Deps 是入账用到的服务。
type Deps struct {
	Gateway  *service.OpenAIGatewayService
	APIKeys  *service.APIKeyService
	Accounts interface {
		GetByID(ctx context.Context, id int64) (*service.Account, error)
	}
	Groups interface {
		GetByID(ctx context.Context, id int64) (*service.Group, error)
	}
	Subscriptions interface {
		GetByID(ctx context.Context, id int64) (*service.UserSubscription, error)
	}
	Vouchers service.RelayVoucherRecorder
	// VerifyVoucher 验凭证（签名、期限、上报节点，master.Runtime.VerifyVoucher）。
	VerifyVoucher func(raw []byte, reportingNodeID int64) (*relayv1.Voucher, error)
	// RefreshUser 入账消耗了租约后刷新主节点内存里的冻结额（master.Quotas.RefreshUser）；nil 时不刷。
	RefreshUser func(ctx context.Context, userID int64) error
}

// Settler 入账从节点上报的扣费记录。
type Settler struct{ deps Deps }

// New 创建入账。
func New(d Deps) *Settler { return &Settler{deps: d} }

// NewFactory 返回运行时用来新建入账的函数（master.RuntimeDeps.NewSettler）：验凭证、刷新冻结额取自本次启动。
func NewFactory(d Deps) func(master.SettleEnv) master.Settler {
	return func(env master.SettleEnv) master.Settler {
		deps := d
		deps.VerifyVoucher, deps.RefreshUser = env.VerifyVoucher, env.RefreshUser
		return New(deps)
	}
}

// errReject：记录本身有问题，隔离、不再重试。
type rejectError struct{ reason string }

func (e *rejectError) Error() string { return e.reason }

func reject(format string, args ...any) error {
	return &rejectError{reason: fmt.Sprintf(format, args...)}
}

// Settle 入账一条记录。返回的结果里带着按租约消耗的金额；主节点自身的故障返回 RETRY。
func (s *Settler) Settle(ctx context.Context, nodeID int64, rec *relayv1.UsageRecord) *relayv1.UsageRecordResult {
	out := &relayv1.UsageRecordResult{Seq: rec.GetSeq()}
	relay, err := s.settle(ctx, nodeID, rec)
	var rej *rejectError
	switch {
	case errors.As(err, &rej):
		slog.Warn("relay usage record rejected", "node_id", nodeID, "seq", rec.GetSeq(), "reason", rej.reason)
		out.Status, out.Reason = relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED, rej.reason
		return out
	case err != nil:
		slog.Warn("relay usage record settlement failed, the node will retry", "node_id", nodeID, "seq", rec.GetSeq(), "error", err)
		out.Status, out.Reason = relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_RETRY, "settlement failed"
		return out
	}
	out.Status = relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED
	if relay.AlreadySettled {
		out.Status = relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_ALREADY_SETTLED
	}
	for _, c := range relay.Consumed {
		out.Consumed = append(out.Consumed, &relayv1.LeaseConsumption{
			LeaseId: c.LeaseID, Amount: c.Amount,
			Scope: &relayv1.QuotaScope{Dimension: c.Dimension, ScopeId: c.ScopeID, ScopeKey: c.ScopeKey},
		})
	}
	return out
}

func (s *Settler) settle(ctx context.Context, nodeID int64, rec *relayv1.UsageRecord) (*service.RelaySettlement, error) {
	if rec.GetKind() != relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI {
		return nil, reject("unsupported usage record kind %v", rec.GetKind())
	}
	v, err := s.deps.VerifyVoucher(rec.GetVoucher(), nodeID)
	if err != nil {
		return nil, reject("invalid voucher: %v", err)
	}
	var result service.OpenAIForwardResult
	if err := json.Unmarshal(rec.GetResultJson(), &result); err != nil {
		return nil, reject("malformed forward result: %v", err)
	}
	input, err := s.buildOpenAIInput(ctx, v, rec, &result)
	if err != nil {
		return nil, err
	}
	sc := v.GetContext()
	relay := &service.RelaySettlement{
		VoucherID: uuidString(v.GetVoucherId()), IssuedAt: time.UnixMilli(v.GetIssuedAtUnixMs()).UTC(),
		NodeID: nodeID, UserID: v.GetUserId(), GroupID: v.GetGroupId(), APIKeyID: v.GetApiKeyId(),
		Platform: sc.GetQuotaPlatform(),
	}
	sctx := context.WithoutCancel(ctx)
	sctx = service.WithRelaySettlement(sctx, relay)
	sctx = service.WithFallbackPoolTrace(sctx, service.FallbackPoolTrace{
		SourceGroupID: sc.GetFallbackSourceGroupId(), SourceGroupName: sc.GetFallbackSourceGroupName(),
		TargetGroupID: sc.GetFallbackTargetGroupId(), TargetGroupName: sc.GetFallbackTargetGroupName(),
	})
	if id := rec.GetClientRequestId(); id != "" {
		sctx = context.WithValue(sctx, ctxkey.ClientRequestID, id)
	}
	if id := rec.GetRequestId(); id != "" {
		sctx = context.WithValue(sctx, ctxkey.RequestID, id)
	}

	recordErr := s.deps.Gateway.RecordUsage(sctx, input)
	if !relay.Handled {
		// 入账没走到扣费事务（简易模式、没有扣费命令、扣费出错）：把凭证记成零消耗，与单机一样这一笔
		// 不再补扣；记不下来（数据库故障）就让从节点重发。
		if err := s.deps.Vouchers.RecordRelayVoucher(sctx, relay); err != nil {
			return nil, fmt.Errorf("record relay voucher: %w (record usage: %v)", err, recordErr)
		}
		if recordErr != nil {
			slog.Error("relay usage billing failed; recorded without charge like a single server would", "node_id", nodeID, "voucher_user", v.GetUserId(), "error", recordErr)
		}
	}
	if s.deps.RefreshUser != nil && len(relay.Consumed) > 0 && !relay.AlreadySettled {
		if err := s.deps.RefreshUser(sctx, v.GetUserId()); err != nil {
			slog.Warn("relay: refresh reserved balance after settlement failed", "user_id", v.GetUserId(), "error", err)
		}
	}
	return relay, nil
}

// buildOpenAIInput 按单机处理函数构造入账输入的方式组装：选号定下的取自凭证，转发事实取自记录。
func (s *Settler) buildOpenAIInput(ctx context.Context, v *relayv1.Voucher, rec *relayv1.UsageRecord, result *service.OpenAIForwardResult) (*service.OpenAIRecordUsageInput, error) {
	sc := v.GetContext()
	apiKey, err := s.deps.APIKeys.GetByID(ctx, v.GetApiKeyId())
	if err != nil {
		if errors.Is(err, service.ErrAPIKeyNotFound) {
			return nil, reject("api key %d not found", v.GetApiKeyId())
		}
		return nil, err
	}
	if apiKey.User == nil || apiKey.User.ID != v.GetUserId() {
		return nil, reject("api key %d does not belong to user %d", v.GetApiKeyId(), v.GetUserId())
	}
	// 分组以选号时的为准（Key 之后换了分组也按请求时的分组计费，与单机一致）。
	if gid := v.GetGroupId(); gid > 0 && (apiKey.GroupID == nil || *apiKey.GroupID != gid || apiKey.Group == nil) {
		group, err := s.deps.Groups.GetByID(ctx, gid)
		if err != nil {
			return nil, err
		}
		key := *apiKey
		key.GroupID, key.Group = &gid, group
		apiKey = &key
	}
	account, err := s.deps.Accounts.GetByID(ctx, v.GetAccountId())
	if err != nil {
		return nil, err
	}
	account.ContributionRouteSource, account.ContributionRoomID = sc.GetContributionRouteSource(), sc.GetContributionRoomId()
	account.ContributionRateMultiplierOverride = nil
	if sc.GetHasContributionRateMultiplierOverride() {
		override := sc.GetContributionRateMultiplierOverride()
		account.ContributionRateMultiplierOverride = &override
	}
	var subscription *service.UserSubscription
	if id := sc.GetSubscriptionId(); id > 0 {
		if subscription, err = s.deps.Subscriptions.GetByID(ctx, id); err != nil {
			return nil, err
		}
	}
	mapping := service.ChannelMappingResult{
		ChannelID: sc.GetChannelId(), MappedModel: sc.GetChannelMappedModel(), Mapped: sc.GetChannelMapped(),
		BillingModelSource: sc.GetBillingModelSource(),
	}
	var pricingAt time.Time
	if ms := sc.GetPricingAtUnixMs(); ms > 0 {
		pricingAt = time.UnixMilli(ms)
	}
	return &service.OpenAIRecordUsageInput{
		Result:             result,
		APIKey:             apiKey,
		User:               apiKey.User,
		Account:            account,
		Subscription:       subscription,
		InboundEndpoint:    rec.GetInboundEndpoint(),
		UpstreamEndpoint:   rec.GetUpstreamEndpoint(),
		UserAgent:          rec.GetUserAgent(),
		IPAddress:          rec.GetIpAddress(),
		SessionID:          rec.GetSessionId(),
		RequestPayloadHash: rec.GetRequestPayloadHash(),
		APIKeyService:      s.deps.APIKeys,
		QuotaPlatform:      sc.GetQuotaPlatform(),
		PricingAt:          pricingAt,
		CyberBlocked:       rec.GetCyberBlocked(),
		NativeCompactionV2: rec.GetNativeCompactionV2(),
		ChannelUsageFields: mapping.ToUsageFields(v.GetRequestedModel(), result.UpstreamModel),
	}, nil
}

func uuidString(b []byte) string {
	if len(b) != 16 {
		return hex.EncodeToString(b)
	}
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
