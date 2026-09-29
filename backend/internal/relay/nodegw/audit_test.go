package nodegw

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fakeModeration struct {
	calls []*relayv1.SecurityAuditRequest
	resp  *relayv1.SecurityAuditResponse
	err   error
}

func (f *fakeModeration) SecurityAudit(_ context.Context, in *relayv1.SecurityAuditRequest, _ ...grpc.CallOption) (*relayv1.SecurityAuditResponse, error) {
	f.calls = append(f.calls, in)
	return f.resp, f.err
}

func auditContext(policy relayv1.AuditPolicy) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	stateOf(c).auditPolicy.Store(int32(policy))
	return c
}

// 从节点的安全审计：按主节点给的策略决定调不调；调不通时与单机审计服务出错一样（阻断模式 503，其余放行）。
func TestNodeSecurityAuditPolicy(t *testing.T) {
	key := &service.APIKey{ID: 11, Key: "sk-a"}
	request := securityaudit.Request{RequestID: "req-1", UserID: 999, Protocol: "openai_responses", Model: "gpt-5", Stage: "http",
		Endpoint: "/v1/responses", Body: []byte(`{"input":"hello"}`)}
	block := securityaudit.Decision{Kind: securityaudit.DecisionBlock, HTTPStatus: http.StatusForbidden, ClientMessage: "no"}
	blockJSON, err := json.Marshal(block)
	require.NoError(t, err)

	// 不审计：不调。
	m := &fakeModeration{}
	d := NewDispatcher(Deps{Moderation: m})
	require.True(t, d.SecurityAudit(auditContext(relayv1.AuditPolicy_AUDIT_POLICY_SKIP), key, request).AllowNextStage)
	require.Empty(t, m.calls)

	// 判定原样带回；请求里是 Key 原文和预先抽好的输入，不带请求体，也不带从节点这边的用户 ID。
	m.resp = &relayv1.SecurityAuditResponse{Decision: blockJSON}
	got := d.SecurityAudit(auditContext(relayv1.AuditPolicy_AUDIT_POLICY_FAIL_OPEN), key, request)
	require.Equal(t, block, got)
	require.Len(t, m.calls, 1)
	call := m.calls[0]
	require.Equal(t, "sk-a", call.GetApiKey())
	require.Equal(t, "req-1", call.GetRequestId())
	require.Equal(t, "http", call.GetStage())
	require.NotContains(t, string(call.GetPrepared()), `"input"`)
	var prepared securityaudit.PreparedInput
	require.NoError(t, json.Unmarshal(call.GetPrepared(), &prepared))
	require.Equal(t, "hello", prepared.Moderation.Text)

	// 复查不通过：放行（选号时拒绝）。
	m.resp = &relayv1.SecurityAuditResponse{Skipped: true}
	require.True(t, d.SecurityAudit(auditContext(relayv1.AuditPolicy_AUDIT_POLICY_FAIL_CLOSED), key, request).AllowNextStage)

	// 调不通。
	m.resp, m.err = nil, errors.New("unavailable")
	require.True(t, d.SecurityAudit(auditContext(relayv1.AuditPolicy_AUDIT_POLICY_FAIL_OPEN), key, request).AllowNextStage)
	require.True(t, d.SecurityAudit(auditContext(relayv1.AuditPolicy_AUDIT_POLICY_UNSPECIFIED), key, request).AllowNextStage)
	closed := d.SecurityAudit(auditContext(relayv1.AuditPolicy_AUDIT_POLICY_FAIL_CLOSED), key, request)
	require.Equal(t, securityaudit.UnavailableDecision(), closed)
	require.False(t, closed.AllowNextStage)
	require.Equal(t, http.StatusServiceUnavailable, closed.HTTPStatus)

	// 主节点回的判定解不开、没有审核连接：同样按调不通处理。
	m.resp, m.err = &relayv1.SecurityAuditResponse{Decision: []byte("{")}, nil
	require.False(t, d.SecurityAudit(auditContext(relayv1.AuditPolicy_AUDIT_POLICY_FAIL_CLOSED), key, request).AllowNextStage)
	require.False(t, NewDispatcher(Deps{}).SecurityAudit(auditContext(relayv1.AuditPolicy_AUDIT_POLICY_FAIL_CLOSED), key, request).AllowNextStage)

	// 审核输入超过审核连接的上限：不发，按调不通处理。
	calls := len(m.calls)
	huge := request
	huge.Body = []byte(`{"input":"` + strings.Repeat("a ", maxAuditInputBytes/2+16) + `"}`)
	require.False(t, d.SecurityAudit(auditContext(relayv1.AuditPolicy_AUDIT_POLICY_FAIL_CLOSED), key, huge).AllowNextStage)
	require.True(t, d.SecurityAudit(auditContext(relayv1.AuditPolicy_AUDIT_POLICY_FAIL_OPEN), key, huge).AllowNextStage)
	require.Len(t, m.calls, calls)
}
