//go:build unit

package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 主从分流主节点复查鉴权时没有 gin：IP 由从节点上送，路径决定额度错误格式。
func TestEvaluateAPIKeyAuthWithoutGin(t *testing.T) {
	user := &service.User{ID: 1, Status: service.StatusActive, Balance: 10}
	key := &service.APIKey{ID: 2, Status: service.StatusActive, User: user, IPWhitelist: []string{"1.2.3.4"}}
	key.CompiledIPWhitelist = ip.CompileIPRules(key.IPWhitelist)

	in := APIKeyAuthInput{APIKey: key, Method: "POST", Path: "/v1/responses", ClientIP: "1.2.3.4"}
	require.Nil(t, EvaluateAPIKeyAuthentication(in))
	in.ClientIP = "5.6.7.8"
	r := EvaluateAPIKeyAuthentication(in)
	require.NotNil(t, r)
	require.Equal(t, "ACCESS_DENIED", r.Code)
	require.Equal(t, IngressRejectIPRestricted, r.IngressReason)
	require.Contains(t, r.Message, "5.6.7.8")

	in.ClientIP = "1.2.3.4"
	inactive := *user
	inactive.Status = "disabled"
	in.APIKey = &service.APIKey{ID: 2, Status: service.StatusActive, User: &inactive}
	require.Equal(t, "USER_INACTIVE", EvaluateAPIKeyAuthentication(in).Code)

	past := time.Now().Add(-time.Hour)
	in.APIKey = &service.APIKey{ID: 2, Status: service.StatusActive, User: user, ExpiresAt: &past}
	_, r = EvaluateAPIKeyBilling(context.Background(), in)
	require.Equal(t, "API_KEY_EXPIRED", r.Code)

	in.APIKey = &service.APIKey{ID: 2, Status: service.StatusAPIKeyQuotaExhausted, User: user}
	_, r = EvaluateAPIKeyBilling(context.Background(), in)
	require.Equal(t, 429, r.Status)
	require.True(t, r.OpenAIQuotaFormat, "Responses paths use the OpenAI error format")
	in.Path = "/v1/messages"
	_, r = EvaluateAPIKeyBilling(context.Background(), in)
	require.False(t, r.OpenAIQuotaFormat)

	broke := *user
	broke.Balance = 0
	in.APIKey = &service.APIKey{ID: 2, Status: service.StatusActive, User: &broke}
	_, r = EvaluateAPIKeyBilling(context.Background(), in)
	require.Equal(t, "INSUFFICIENT_BALANCE", r.Code)
	in.Path = "/v1/usage"
	d, r := EvaluateAPIKeyBilling(context.Background(), in)
	require.Nil(t, r, "usage queries skip billing checks")
	require.True(t, d.TouchLastUsed)
	in.Path = "/v1/sub2api/billing"
	d, _ = EvaluateAPIKeyBilling(context.Background(), in)
	require.False(t, d.TouchLastUsed)
}
