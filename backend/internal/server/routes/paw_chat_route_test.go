package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newPawChatRouteEngine(dispatch gin.HandlerFunc, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	setting := (*service.SettingService)(nil)
	config := service.NewPawConfigService(
		pawChatRouteGroups{},
		pawChatRouteUsers{},
		pawChatRouteChannels{},
		&pawRouteStore{},
	)
	keySource := &pawChatRouteKeySource{apiKey: &service.APIKey{
		ID:     99,
		UserID: userID,
		Status: service.StatusActive,
		User:   &service.User{ID: userID, Status: service.StatusActive},
	}}
	chat := service.NewPawChatService(config, keySource)
	auth := func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID})
		c.Next()
	}
	RegisterPawRoutes(r.Group("/api/v1"), config, auth, setting, servermiddleware.NewPanelRateLimiter(nil, setting), PawRouteDependencies{
		ChatService: chat,
		OpenAIChat:  dispatch,
	})
	return r
}

type pawChatRouteGroups struct{}

func (pawChatRouteGroups) AvailableGroups(context.Context, int64) ([]service.Group, error) {
	return []service.Group{{ID: 7, Name: "OpenAI", Platform: service.PlatformOpenAI, Status: service.StatusActive}}, nil
}

type pawChatRouteUsers struct{}

func (pawChatRouteUsers) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 42, Username: "user", Email: "user@example.com", Status: service.StatusActive}, nil
}

type pawChatRouteChannels struct{}

func (pawChatRouteChannels) GetChannelForGroup(context.Context, int64) (*service.Channel, error) {
	return &service.Channel{Status: service.StatusActive, ModelPricing: []service.ChannelModelPricing{{Platform: service.PlatformOpenAI, Models: []string{"gpt-5"}}}}, nil
}

type pawChatRouteKeySource struct {
	apiKey *service.APIKey
}

func (s *pawChatRouteKeySource) ResolvePawAPIKey(context.Context, int64, int64) (*service.APIKey, *service.UserSubscription, error) {
	return s.apiKey, nil, nil
}

func TestPawChatRouteRejectsProviderCredentialHeaders(t *testing.T) {
	r := newPawChatRouteEngine(func(c *gin.Context) {
		t.Fatal("chat handler must not run")
	}, 42)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/paw/chat/completions", strings.NewReader(`{"group_id":7,"model_id":"gpt-5","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("x-api-key", "provider-secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), PawErrorCodeAuthRequired)
}

func TestPawChatRouteBindsSelectedGroupAndPreservesOpenAISSE(t *testing.T) {
	r := newPawChatRouteEngine(func(c *gin.Context) {
		key, ok := servermiddleware.GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.NotNil(t, key.GroupID)
		require.Equal(t, int64(7), *key.GroupID)
		require.Equal(t, service.PlatformOpenAI, key.Group.Platform)
		require.Equal(t, `{"model":"gpt-5","messages":[{"role":"user","content":"hello"}],"stream":true}`, readRequestBody(t, c))
		c.Data(http.StatusOK, "text/event-stream", []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
	}, 42)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/paw/chat/completions", strings.NewReader(`{"group_id":7,"model_id":"gpt-5","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	require.Contains(t, w.Body.String(), "[DONE]")
}

func readRequestBody(t *testing.T, c *gin.Context) string {
	t.Helper()
	body, err := c.GetRawData()
	require.NoError(t, err)
	return string(body)
}
