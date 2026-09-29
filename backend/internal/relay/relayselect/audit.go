package relayselect

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// auditPolicy 是这个分组的请求的安全审计策略（从节点据此决定调不调 SecurityAudit、调不通时怎么办）。
// 与单机一致：提示词审计阻断模式下审计服务出错回 503；内容审核出错放行；异步提示词审计不影响请求。
// 内容审核按分组判断（还不知道模型），比按分组加模型宽：多调一次，主节点照单机判定不管的请求。
func (s *selector) auditPolicy(ctx context.Context, groupID *int64) relayv1.AuditPolicy {
	if p := s.deps.PromptAudit; p != nil {
		switch p.EffectiveMode() {
		case securityaudit.ModeBlocking:
			return relayv1.AuditPolicy_AUDIT_POLICY_FAIL_CLOSED
		case securityaudit.ModeAsync:
			return relayv1.AuditPolicy_AUDIT_POLICY_FAIL_OPEN
		}
	}
	if s.deps.Moderation.AppliesToGroup(ctx, groupID) {
		return relayv1.AuditPolicy_AUDIT_POLICY_FAIL_OPEN
	}
	return relayv1.AuditPolicy_AUDIT_POLICY_SKIP
}

// SecurityAudit 转发前的安全审计（设计 3.4）：用户、Key、分组按 Key 原文复查（与准入同一段），请求上的事实和
// 预先抽好的审核输入取自从节点，交给单机同一个审计协调器判定。复查不通过时回 skipped（选号时按同一复查拒绝）。
func (s *selector) SecurityAudit(ctx context.Context, _ int64, req *relayv1.SecurityAuditRequest) (*relayv1.SecurityAuditResponse, error) {
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), nil)
	if err != nil {
		return nil, err
	}
	if rej != nil || s.deps.Audit == nil {
		return &relayv1.SecurityAuditResponse{Skipped: true}, nil
	}
	var prepared securityaudit.PreparedInput
	if err := json.Unmarshal(req.GetPrepared(), &prepared); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid prepared audit input")
	}
	request := securityaudit.Request{
		RequestID: req.GetRequestId(), Endpoint: req.GetEndpoint(), Provider: req.GetProvider(),
		Protocol: req.GetProtocol(), Model: req.GetModel(), Stage: req.GetStage(), Prepared: &prepared,
	}
	apiKey := adm.APIKey
	handler.ApplySecurityAuditIdentity(&request, apiKey, apiKey.User.ID)
	decision := s.deps.Audit.Check(ctx, request)
	raw, err := json.Marshal(decision)
	if err != nil {
		return nil, err
	}
	return &relayv1.SecurityAuditResponse{Decision: raw}, nil
}
