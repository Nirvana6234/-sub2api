package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newPawSystemOneEngine 装上与 RegisterPawRoutes 相同的 /paw/systemone 链，只把最后
// 一步的网关换成记录上下文里的 key。
func newPawSystemOneEngine(t *testing.T) (*gin.Engine, *pawMessagesObservation) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	jev := service.Group{
		ID: 9, Name: "Jev 意图判断", Platform: service.PlatformTypeSafe, Status: service.StatusActive,
		ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"jev-1.13.0", "jev-latest"}},
	}
	claude := service.Group{ID: 5, Name: "claude", Platform: service.PlatformAnthropic, Status: service.StatusActive}
	source := &pawMessagesKeySource{
		groups: map[int64]service.Group{5: claude, 9: jev},
		key: &service.APIKey{
			ID: 99, UserID: 42, Status: service.StatusActive,
			User:      &service.User{ID: 42, Status: service.StatusActive},
			AutoGroup: true, AutoGroupIDs: []int64{5},
		},
	}
	chat := service.NewPawChatService(nil, source)
	observed := &pawMessagesObservation{}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	})
	r.POST("/api/v1/paw/systemone", pawSystemOnePrepareHandler(chat), middleware.GroupModelAllowlist(), func(c *gin.Context) {
		observed.key, _ = middleware.GetAPIKeyFromContext(c)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r, observed
}

func decodePawError(t *testing.T, w *httptest.ResponseRecorder) PawErrorResponse {
	t.Helper()
	var decoded PawErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &decoded), w.Body.String())
	return decoded
}

const jevBody = `{"model":"jev-1.13.0","state":{"conversation":[]},"questions":{}}`

func TestPawSystemOneRequiresANamedGroup(t *testing.T) {
	r, observed := newPawSystemOneEngine(t)
	for _, header := range []string{"", "0", "auto"} {
		w := postPawMessages(r, "/api/v1/paw/systemone", map[string]string{PawGroupHeader: header}, jevBody)
		require.Equal(t, http.StatusBadRequest, w.Code, "header=%q", header)
		require.Equal(t, "INVALID_REQUEST", decodePawError(t, w).Error.Code)
	}
	require.Nil(t, observed.key)
}

func TestPawSystemOneRejectsGroupsThatAreNotTypeSafe(t *testing.T) {
	r, observed := newPawSystemOneEngine(t)

	w := postPawMessages(r, "/api/v1/paw/systemone", map[string]string{PawGroupHeader: "5"}, jevBody)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, "GROUP_FORBIDDEN", decodePawError(t, w).Error.Code)

	w = postPawMessages(r, "/api/v1/paw/systemone", map[string]string{PawGroupHeader: "404"}, jevBody)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, "GROUP_FORBIDDEN", decodePawError(t, w).Error.Code)
	require.Nil(t, observed.key)
}

// 转发和计费都靠 key 上的分组：定价按 apiKey.Group 解析，内部 key 本身不带分组。
func TestPawSystemOneHandsOnTheKeyBoundToTheNamedGroup(t *testing.T) {
	r, observed := newPawSystemOneEngine(t)

	w := postPawMessages(r, "/api/v1/paw/systemone", map[string]string{PawGroupHeader: "9"}, jevBody)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, observed.key)
	require.NotNil(t, observed.key.Group)
	require.Equal(t, service.PlatformTypeSafe, observed.key.Group.Platform)
	require.NotNil(t, observed.key.GroupID)
	require.Equal(t, int64(9), *observed.key.GroupID)
}

// /paw 没有全局的分组模型白名单中间件，/paw/systemone 自己挂了一次。
func TestPawSystemOneEnforcesTheGroupModelAllowlist(t *testing.T) {
	r, observed := newPawSystemOneEngine(t)

	w := postPawMessages(r, "/api/v1/paw/systemone", map[string]string{PawGroupHeader: "9"}, `{"model":"gpt-5.5","state":{},"questions":{}}`)

	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	require.Nil(t, observed.key)
}

func TestPawMessagesRejectsTypeSafeGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jev := service.Group{ID: 9, Platform: service.PlatformTypeSafe, Status: service.StatusActive}
	source := &pawMessagesKeySource{
		groups: map[int64]service.Group{9: jev},
		key:    &service.APIKey{ID: 99, UserID: 42, Status: service.StatusActive, User: &service.User{ID: 42, Status: service.StatusActive}},
	}
	chat := service.NewPawChatService(nil, source)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	})
	r.POST("/api/v1/paw/messages", pawMessagesHandler(chat, PawRouteDependencies{}, false))

	w := postPawMessages(r, "/api/v1/paw/messages", map[string]string{PawGroupHeader: "9"}, `{"model":"jev-1.13.0"}`)

	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

func TestTypeSafeProtocolGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newEngine := func(platform string) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			gid := int64(9)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1, GroupID: &gid, Group: &service.Group{ID: gid, Platform: platform}})
			c.Next()
		})
		r.Use(typeSafeProtocolGuardMiddleware())
		ok := func(c *gin.Context) { c.Status(http.StatusNoContent) }
		r.POST("/v1/systemone", ok)
		r.POST("/v1/messages", ok)
		r.POST("/v1/chat/completions", ok)
		r.POST("/responses", ok)
		r.GET("/v1/models", ok)
		r.GET("/v1/usage", ok)
		return r
	}
	serve := func(r *gin.Engine, method, path string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	jev := newEngine(service.PlatformTypeSafe)
	require.Equal(t, http.StatusNoContent, serve(jev, http.MethodPost, "/v1/systemone"))
	require.Equal(t, http.StatusNoContent, serve(jev, http.MethodGet, "/v1/models"))
	require.Equal(t, http.StatusNoContent, serve(jev, http.MethodGet, "/v1/usage"))
	require.Equal(t, http.StatusNotFound, serve(jev, http.MethodPost, "/v1/messages"))
	require.Equal(t, http.StatusNotFound, serve(jev, http.MethodPost, "/v1/chat/completions"))
	require.Equal(t, http.StatusNotFound, serve(jev, http.MethodPost, "/responses"))

	openai := newEngine(service.PlatformOpenAI)
	require.Equal(t, http.StatusNoContent, serve(openai, http.MethodPost, "/v1/messages"))
	require.Equal(t, http.StatusNoContent, serve(openai, http.MethodPost, "/v1/systemone"))
}
