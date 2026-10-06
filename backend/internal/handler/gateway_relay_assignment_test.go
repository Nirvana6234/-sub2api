package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type relayAddressStub map[int64]service.RelayAddress

func (s relayAddressStub) ResolveRelayAddress(_ context.Context, nodeID int64) (service.RelayAddress, bool) {
	a, ok := s[nodeID]
	return a, ok
}

func relayAssignmentRequest(t *testing.T, key *service.APIKey) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/relay/assignment", func(c *gin.Context) {
		if key != nil {
			c.Set(string(middleware2.ContextKeyAPIKey), key)
		}
		(&GatewayHandler{}).RelayAssignment(c)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/relay/assignment", nil)
	req.Host = "api.example.com"
	r.ServeHTTP(w, req)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w.Code, body
}

// 设计 10.9（API Key 版本）：返回这把 Key 的 role、node_id、base_url，不返回票据；Key 重新分配后工具用它发现新地址。
func TestRelayAssignmentReturnsTheAssignedAddress(t *testing.T) {
	node, master := int64(12), int64(0)
	service.SetRelayAddressResolver(relayAddressStub{
		12: {Role: service.RelayRoleRelay, NodeID: 12, BaseURL: "https://r1.example.com"},
		0:  {Role: service.RelayRoleMaster},
	})
	t.Cleanup(func() { service.SetRelayAddressResolver(nil) })

	status, body := relayAssignmentRequest(t, &service.APIKey{ID: 1, RelayNodeID: &node})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, map[string]any{"role": "relay", "node_id": float64(12), "base_url": "https://r1.example.com"}, body)

	// 主节点没配置 api_base_url：用当前请求的站点地址。
	status, body = relayAssignmentRequest(t, &service.APIKey{ID: 2, RelayNodeID: &master})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "master", body["role"])
	require.Equal(t, "http://api.example.com", body["base_url"])

	// 没分配：沿用现在的地址。
	status, body = relayAssignmentRequest(t, &service.APIKey{ID: 3})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "unassigned", body["role"])

	// 分配的节点已经没有地址（不存在、没填域名）：503。
	gone := int64(99)
	status, _ = relayAssignmentRequest(t, &service.APIKey{ID: 4, RelayNodeID: &gone})
	require.Equal(t, http.StatusServiceUnavailable, status)

	// 没有鉴权的 Key：401。
	status, _ = relayAssignmentRequest(t, nil)
	require.Equal(t, http.StatusUnauthorized, status)
}
