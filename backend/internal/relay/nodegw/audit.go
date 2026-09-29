package nodegw

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// maxAuditInputBytes 是一次审计送给主节点的审核输入上限（审核连接单条消息 16 MiB，留出其余字段的余量）。
const maxAuditInputBytes = 15 << 20

var errAuditInputTooLarge = errors.New("prepared audit input is too large")

// SecurityAudit 转发前的安全审计（handler.OpenAIRelayDispatcher，设计 3.4）：按主节点给的审计策略（准入时给，
// WebSocket 每一轮 BeginTurn 更新），不会被审计时直接放行；否则把预先抽好的审核输入（不带请求体）经审核连接
// 交给主节点判定。调不通（主节点不可达、过载、超时、输入超限）时与单机审计服务出错一样：提示词审计阻断模式回
// 审计不可用，其余放行。
func (d *Dispatcher) SecurityAudit(c *gin.Context, apiKey *service.APIKey, request securityaudit.Request) securityaudit.Decision {
	policy := relayv1.AuditPolicy(stateOf(c).auditPolicy.Load())
	if policy == relayv1.AuditPolicy_AUDIT_POLICY_SKIP {
		return securityaudit.AllowDecision()
	}
	fail := func(err error) securityaudit.Decision {
		if c.Request.Context().Err() == nil {
			slog.Warn("relay security audit unavailable", "request_id", request.RequestID, "stage", request.Stage, "policy", policy.String(), "error", err)
		}
		if policy == relayv1.AuditPolicy_AUDIT_POLICY_FAIL_CLOSED {
			return securityaudit.UnavailableDecision()
		}
		return securityaudit.AllowDecision()
	}
	if d.deps.Moderation == nil || apiKey == nil {
		return fail(errors.New("no moderation connection"))
	}
	prepared := securityaudit.PrepareRequest(request)
	raw, err := json.Marshal(prepared.Prepared)
	if err != nil {
		return fail(err)
	}
	if len(raw) > maxAuditInputBytes {
		return fail(errAuditInputTooLarge)
	}
	resp, err := d.deps.Moderation.SecurityAudit(c.Request.Context(), &relayv1.SecurityAuditRequest{
		ApiKey: apiKey.Key, ClientIp: strings.TrimSpace(ip.GetClientIP(c)), Method: c.Request.Method, Path: c.Request.URL.Path,
		RequestId: request.RequestID, Endpoint: request.Endpoint, Provider: request.Provider, Protocol: request.Protocol,
		Model: request.Model, Stage: request.Stage, Prepared: raw,
	})
	if err != nil {
		return fail(err)
	}
	if resp.GetSkipped() {
		// Key 复查没通过：选号时按同一复查拒绝。
		return securityaudit.AllowDecision()
	}
	var decision securityaudit.Decision
	if err := json.Unmarshal(resp.GetDecision(), &decision); err != nil {
		return fail(err)
	}
	return decision
}
