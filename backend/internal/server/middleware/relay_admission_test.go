//go:build unit

package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type relayAdmissionCase struct {
	name     string
	key      *service.APIKey // nil：Key 不存在
	model    string
	path     string
	settings map[string]string
}

func relayAdmissionKey(mutate func(k *service.APIKey)) *service.APIKey {
	groupID := int64(5)
	k := &service.APIKey{
		ID: 11, Key: "sk-relay", UserID: 3, Status: service.StatusActive, GroupID: &groupID,
		User:  &service.User{ID: 3, Status: service.StatusActive, Balance: 10, Concurrency: 5},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1},
	}
	if mutate != nil {
		mutate(k)
	}
	return k
}

func blacklistSetting(t *testing.T, kind, value string) map[string]string {
	raw, err := json.Marshal([]service.GlobalBlacklistEntry{{ID: "b1", Kind: kind, Value: value, Enabled: true, CreatedAt: time.Now()}})
	require.NoError(t, err)
	return map[string]string{service.SettingKeyGlobalBlacklist: string(raw)}
}

type relayAdmissionResult struct {
	status  int
	body    string
	retry   string
	ingress string
	ops     string
}

// 主节点选号时复查 API Key 的结果必须与本地中间件链（routes/gateway.go 的 /v1 链：全局 IP 黑名单 →
// apiKeyAuth → 全局用户黑名单 → 分组模型白名单 → 未分组拦截）逐字节一致，含运维标记。
func TestRelayAPIKeyAdmissionMatchesTheLocalMiddlewareChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	past := time.Now().Add(-time.Hour)
	const clientIP = "5.6.7.8"
	cases := []relayAdmissionCase{
		{name: "passes", key: relayAdmissionKey(nil)},
		{name: "unknown key"},
		{name: "disabled key", key: relayAdmissionKey(func(k *service.APIKey) { k.Status = "disabled" })},
		{name: "ip not whitelisted", key: relayAdmissionKey(func(k *service.APIKey) {
			k.IPWhitelist = []string{"1.2.3.4"}
			k.CompiledIPWhitelist = ip.CompileIPRules(k.IPWhitelist)
		})},
		{name: "inactive user", key: relayAdmissionKey(func(k *service.APIKey) { k.User.Status = "disabled" })},
		{name: "no balance", key: relayAdmissionKey(func(k *service.APIKey) { k.User.Balance = 0 })},
		{name: "expired key", key: relayAdmissionKey(func(k *service.APIKey) { k.ExpiresAt = &past })},
		{name: "key quota used up on responses", key: relayAdmissionKey(func(k *service.APIKey) { k.Status = service.StatusAPIKeyQuotaExhausted })},
		{name: "key quota used up on messages", path: "/v1/messages", key: relayAdmissionKey(func(k *service.APIKey) { k.Status = service.StatusAPIKeyQuotaExhausted })},
		{name: "user blacklisted", key: relayAdmissionKey(nil), settings: blacklistSetting(t, service.GlobalBlacklistKindAccount, "3")},
		{name: "ip blacklisted", key: relayAdmissionKey(nil), settings: blacklistSetting(t, service.GlobalBlacklistKindIP, clientIP)},
		{name: "model not allowed", key: relayAdmissionKey(func(k *service.APIKey) {
			k.Group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-4o"}}
		})},
		{name: "model allowed", key: relayAdmissionKey(func(k *service.APIKey) {
			k.Group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5"}}
		})},
		{name: "ungrouped key", key: relayAdmissionKey(func(k *service.APIKey) { k.GroupID, k.Group = nil, nil }),
			settings: map[string]string{service.SettingKeyAllowUngroupedKeyScheduling: "false"}},
		{name: "ungrouped key allowed", key: relayAdmissionKey(func(k *service.APIKey) { k.GroupID, k.Group = nil, nil }),
			settings: map[string]string{service.SettingKeyAllowUngroupedKeyScheduling: "true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.model == "" {
				tc.model = "gpt-5"
			}
			if tc.path == "" {
				tc.path = "/v1/responses"
			}
			cfg := &config.Config{}
			newKeys := func() *service.APIKeyService {
				return service.NewAPIKeyService(&stubApiKeyRepo{
					getByKey: func(context.Context, string) (*service.APIKey, error) {
						if tc.key == nil {
							return nil, service.ErrAPIKeyNotFound
						}
						clone := *tc.key
						return &clone, nil
					},
					updateLastUsed: func(context.Context, int64, time.Time) error { return nil },
				}, nil, nil, nil, nil, nil, cfg)
			}
			newSettings := func() *service.SettingService {
				return service.NewSettingService(fakeSettingRepo{values: tc.settings}, cfg)
			}

			// 本地：真实的中间件链。
			settings := newSettings()
			var local relayAdmissionResult
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Next()
				if reason, ok := GetIngressRejectReason(c); ok {
					local.ingress = string(reason)
				}
				local.ops = service.OpsClientBusinessLimitedReason(c)
			})
			r.Use(GlobalBlacklistIP(settings, cfg), gin.HandlerFunc(NewAPIKeyAuthMiddleware(newKeys(), nil, cfg)), GlobalBlacklistAccount(settings, cfg),
				GroupModelAllowlist(), RequireGroupAssignment(settings, AnthropicErrorWriter))
			r.POST(tc.path, func(c *gin.Context) { c.Status(http.StatusOK) })
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"model":"`+tc.model+`"}`))
			req.Header.Set("Authorization", "Bearer sk-relay")
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = clientIP + ":4321"
			r.ServeHTTP(w, req)
			local.status, local.body, local.retry = w.Code, w.Body.String(), w.Header().Get("Retry-After")

			// 主节点：同样的输入，Key 原文和客户端 IP 由从节点转来。
			adm, rej, err := EvaluateRelayAPIKeyAdmission(context.Background(), RelayAPIKeyAdmissionInput{
				APIKeyAuthInput: APIKeyAuthInput{APIKeys: newKeys(), Config: cfg, ClientIP: clientIP, Method: http.MethodPost, Path: tc.path},
				RawKey:          "sk-relay",
				Settings:        newSettings(),
				Models:          []string{tc.model},
			})
			require.NoError(t, err)
			if local.status == http.StatusOK {
				require.Nil(t, rej, "the master must not reject what the local chain lets through: %+v", rej)
				require.NotNil(t, adm.APIKey)
				return
			}
			require.NotNil(t, rej, "the master must reject what the local chain rejects (%d %s)", local.status, local.body)
			got := relayAdmissionResult{status: rej.Status, body: string(rej.Body), retry: rej.Header.Get("Retry-After"), ingress: rej.IngressReason, ops: rej.OpsReason}
			require.Equal(t, local, got)
			require.Equal(t, w.Header().Get("Content-Type"), rej.Header.Get("Content-Type"))
		})
	}
}

func TestRelayAPIKeyAdmissionLeavesUnsupportedKeysToTheMaster(t *testing.T) {
	cfg := &config.Config{}
	for name, mutate := range map[string]func(k *service.APIKey){
		"composite": func(k *service.APIKey) { k.Group.Platform = service.PlatformComposite },
	} {
		t.Run(name, func(t *testing.T) {
			key := relayAdmissionKey(mutate)
			keys := service.NewAPIKeyService(&stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { return key, nil }}, nil, nil, nil, nil, nil, cfg)
			_, rej, err := EvaluateRelayAPIKeyAdmission(context.Background(), RelayAPIKeyAdmissionInput{
				APIKeyAuthInput: APIKeyAuthInput{APIKeys: keys, Config: cfg, Method: http.MethodPost, Path: "/v1/responses"},
				RawKey:          "sk-relay",
				Settings:        service.NewSettingService(fakeSettingRepo{}, cfg),
				Models:          []string{"gpt-5"},
			})
			require.Nil(t, rej)
			require.ErrorIs(t, err, ErrRelayAdmissionUnsupported)
		})
	}
}
