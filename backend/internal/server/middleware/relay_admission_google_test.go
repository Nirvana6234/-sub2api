//go:build unit

package middleware

import (
	"context"
	"errors"
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

// Gemini 原生入口（/v1beta）：主节点复查的结果与本地 Google 格式的中间件链（全局 IP 黑名单 → Google 鉴权 →
// 全局用户黑名单 → 分组模型白名单 → Google 格式的未分组拦截）逐字节一致，含运维标记。
func TestRelayGoogleAdmissionMatchesTheLocalMiddlewareChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	past := time.Now().Add(-time.Hour)
	const clientIP = "5.6.7.8"
	const urlModel = "gemini-2.5-pro"
	geminiKey := func(mutate func(k *service.APIKey)) *service.APIKey {
		return relayAdmissionKey(func(k *service.APIKey) {
			k.Group.Platform = service.PlatformGemini
			if mutate != nil {
				mutate(k)
			}
		})
	}
	ungrouped := func(k *service.APIKey) { k.GroupID, k.Group = nil, nil }
	cases := []struct {
		name     string
		key      *service.APIKey
		settings map[string]string
		runMode  string
		keyErr   error
		bearer   bool
	}{
		{name: "passes", key: geminiKey(nil)},
		{name: "passes with bearer", key: geminiKey(nil), bearer: true},
		{name: "unknown key"},
		{name: "auth overloaded", keyErr: service.ErrAPIKeyAuthOverloaded},
		{name: "auth failed", keyErr: errors.New("db down")},
		{name: "disabled key", key: geminiKey(func(k *service.APIKey) { k.Status = "disabled" })},
		{name: "ip not whitelisted", key: geminiKey(func(k *service.APIKey) {
			k.IPWhitelist = []string{"1.2.3.4"}
			k.CompiledIPWhitelist = ip.CompileIPRules(k.IPWhitelist)
		})},
		{name: "inactive user", key: geminiKey(func(k *service.APIKey) { k.User.Status = "disabled" })},
		{name: "no balance", key: geminiKey(func(k *service.APIKey) { k.User.Balance = 0 })},
		{name: "no balance in simple mode", key: geminiKey(func(k *service.APIKey) { k.User.Balance = 0 }), runMode: config.RunModeSimple},
		{name: "expired key", key: geminiKey(func(k *service.APIKey) { k.ExpiresAt = &past })},
		{name: "key quota used up", key: geminiKey(func(k *service.APIKey) { k.Status = service.StatusAPIKeyQuotaExhausted })},
		{name: "user blacklisted", key: geminiKey(nil), settings: blacklistSetting(t, service.GlobalBlacklistKindAccount, "3")},
		{name: "ip blacklisted", key: geminiKey(nil), settings: blacklistSetting(t, service.GlobalBlacklistKindIP, clientIP)},
		{name: "model not allowed", key: geminiKey(func(k *service.APIKey) {
			k.Group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gemini-2.5-flash"}}
		})},
		{name: "model allowed", key: geminiKey(func(k *service.APIKey) {
			k.Group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{urlModel}}
		})},
		{name: "ungrouped key", key: geminiKey(ungrouped),
			settings: map[string]string{service.SettingKeyAllowUngroupedKeyScheduling: "false"}},
		{name: "ungrouped key allowed", key: geminiKey(ungrouped),
			settings: map[string]string{service.SettingKeyAllowUngroupedKeyScheduling: "true"}},
	}
	path := "/v1beta/models/" + urlModel + ":generateContent"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{RunMode: tc.runMode}
			newKeys := func() *service.APIKeyService {
				return service.NewAPIKeyService(&stubApiKeyRepo{
					getByKey: func(context.Context, string) (*service.APIKey, error) {
						if tc.keyErr != nil {
							return nil, tc.keyErr
						}
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
			r.Use(GlobalBlacklistIP(settings, cfg), APIKeyAuthWithSubscriptionGoogle(newKeys(), nil, cfg), GlobalBlacklistAccount(settings, cfg),
				GroupModelAllowlist(), RequireGroupAssignment(settings, GoogleErrorWriter))
			r.POST("/v1beta/models/*modelAction", func(c *gin.Context) { c.Status(http.StatusOK) })
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"contents":[]}`))
			if tc.bearer {
				req.Header.Set("Authorization", "Bearer sk-relay")
			} else {
				req.Header.Set("x-goog-api-key", "sk-relay")
			}
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = clientIP + ":4321"
			r.ServeHTTP(w, req)
			local.status, local.body, local.retry = w.Code, w.Body.String(), w.Header().Get("Retry-After")

			adm, rej, err := EvaluateRelayAPIKeyAdmission(context.Background(), RelayAPIKeyAdmissionInput{
				APIKeyAuthInput: APIKeyAuthInput{APIKeys: newKeys(), Config: cfg, ClientIP: clientIP, Method: http.MethodPost, Path: path},
				RawKey:          "sk-relay",
				Settings:        newSettings(),
				Models:          []string{urlModel},
				Google:          true,
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

// 从节点取 Google 入口的 Key：顺序与错误和本地 Google 鉴权中间件一致（x-goog-api-key 优先于 Bearer，查询参数 key
// 可用，api_key 被拒）。
func TestExtractGoogleAPIKeyCredentialMatchesTheLocalMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	for _, tc := range []struct {
		name    string
		path    string
		headers map[string]string
		want    string
		status  int
	}{
		{name: "goog header wins over bearer", path: "/v1beta/models/m:generateContent", headers: map[string]string{"x-goog-api-key": "sk-goog", "Authorization": "Bearer sk-bearer"}, want: "sk-goog"},
		{name: "bearer", path: "/v1beta/models/m:generateContent", headers: map[string]string{"Authorization": "Bearer sk-bearer"}, want: "sk-bearer"},
		{name: "x-api-key", path: "/v1beta/models/m:generateContent", headers: map[string]string{"x-api-key": "sk-x"}, want: "sk-x"},
		{name: "query key on v1beta", path: "/v1beta/models/m:generateContent?key=sk-q", want: "sk-q"},
		{name: "query key on antigravity v1beta", path: "/antigravity/v1beta/models/m:generateContent?key=sk-q", want: "sk-q"},
		{name: "deprecated api_key query", path: "/v1beta/models/m:generateContent?api_key=sk-q", status: http.StatusBadRequest},
		{name: "missing", path: "/v1beta/models/m:generateContent", status: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys := service.NewAPIKeyService(&stubApiKeyRepo{getByKey: func(_ context.Context, raw string) (*service.APIKey, error) {
				k := relayAdmissionKey(func(k *service.APIKey) { k.Group.Platform = service.PlatformGemini })
				k.Key = raw
				return k, nil
			}, updateLastUsed: func(context.Context, int64, time.Time) error { return nil }}, nil, nil, nil, nil, nil, cfg)
			var localKey string
			r := gin.New()
			r.Use(APIKeyAuthWithSubscriptionGoogle(keys, nil, cfg))
			handler := func(c *gin.Context) {
				k, _ := GetAPIKeyFromContext(c)
				localKey = k.Key
				c.Status(http.StatusOK)
			}
			r.POST("/v1beta/models/*modelAction", handler)
			r.POST("/antigravity/v1beta/models/*modelAction", handler)
			mk := func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{}`))
				for k, v := range tc.headers {
					req.Header.Set(k, v)
				}
				return req
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, mk())

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = mk()
			got, ok := ExtractGoogleAPIKeyCredential(c, nil)
			if tc.status != 0 {
				require.False(t, ok)
				require.Equal(t, tc.status, w.Code)
				require.Equal(t, w.Code, rec.Code)
				require.Equal(t, w.Body.String(), rec.Body.String())
				return
			}
			require.True(t, ok)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.want, localKey, "the same key authenticates locally")
		})
	}
}
