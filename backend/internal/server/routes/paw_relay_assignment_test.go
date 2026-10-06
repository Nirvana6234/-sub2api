package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type relayUserAssignerStub struct {
	result      *service.RelayUserAssignment
	err         error
	gotUser     int64
	gotReported int64
}

func (s *relayUserAssignerStub) AssignUser(_ context.Context, userID, unreachable int64) (*service.RelayUserAssignment, error) {
	s.gotUser, s.gotReported = userID, unreachable
	return s.result, s.err
}

func relayAssignmentCall(t *testing.T, method, body string, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if authed {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		}
		c.Next()
	})
	r.GET("/api/v1/paw/relay/assignment", pawRelayAssignmentHandler(false))
	r.POST("/api/v1/paw/relay/assignment", pawRelayAssignmentHandler(true))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/v1/paw/relay/assignment", strings.NewReader(body))
	req.Host = "api.example.com"
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// 设计 10.9（小白端）：GET 返回当前分配，POST 带 unreachable_node_id 报告连不上并拿新的分配；从节点带票据，主节点没配置
// api_base_url 时用当前请求的主机；没有可用中转时 503 RELAY_UNAVAILABLE；主从分流没开 404。
func TestPawRelayAssignmentEndpoint(t *testing.T) {
	exp := time.Now().Add(10 * time.Minute)
	stub := &relayUserAssignerStub{result: &service.RelayUserAssignment{Role: service.RelayRoleRelay, NodeID: 12, BaseURL: "https://r1.example.com",
		Ticket: "srt1.abc", TicketExpiresAt: &exp, RefreshAfter: 60}}
	service.SetRelayUserAssigner(stub)
	t.Cleanup(func() { service.SetRelayUserAssigner(nil) })

	w := relayAssignmentCall(t, http.MethodGet, "", true)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "relay", body["role"])
	require.EqualValues(t, 12, body["node_id"])
	require.Equal(t, "https://r1.example.com", body["base_url"])
	require.Equal(t, "srt1.abc", body["ticket"])
	require.EqualValues(t, 60, body["refresh_after"])
	require.EqualValues(t, 7, stub.gotUser)
	require.Zero(t, stub.gotReported)

	// POST：报告连不上哪台。
	w = relayAssignmentCall(t, http.MethodPost, `{"unreachable_node_id": 12}`, true)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.EqualValues(t, 12, stub.gotReported)
	w = relayAssignmentCall(t, http.MethodPost, `{}`, true)
	require.Equal(t, http.StatusBadRequest, w.Code, "the report must name a node")
	w = relayAssignmentCall(t, http.MethodPost, `not json`, true)
	require.Equal(t, http.StatusBadRequest, w.Code)

	// 分到主节点：没有票据，没配置 api_base_url 时用请求的主机。
	stub.result = &service.RelayUserAssignment{Role: service.RelayRoleMaster, RefreshAfter: 60}
	w = relayAssignmentCall(t, http.MethodGet, "", true)
	require.Equal(t, http.StatusOK, w.Code)
	body = map[string]any{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "master", body["role"])
	require.Equal(t, "http://api.example.com", body["base_url"])
	require.NotContains(t, body, "ticket")

	// 错误：没有可用中转 → 503 RELAY_UNAVAILABLE；用户停用 → 401。
	stub.err = service.ErrRelayUnavailable
	w = relayAssignmentCall(t, http.MethodGet, "", true)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "RELAY_UNAVAILABLE")
	require.NotEmpty(t, w.Header().Get("Retry-After"))
	stub.err = service.ErrUserNotActive
	require.Equal(t, http.StatusUnauthorized, relayAssignmentCall(t, http.MethodGet, "", true).Code)

	// 没有登录态：401。
	require.Equal(t, http.StatusUnauthorized, relayAssignmentCall(t, http.MethodGet, "", false).Code)

	// 主从分流没开：404，客户端沿用现在的地址。
	service.SetRelayUserAssigner(nil)
	w = relayAssignmentCall(t, http.MethodGet, "", true)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "RELAY_NOT_ENABLED")
}
