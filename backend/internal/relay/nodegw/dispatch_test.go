package nodegw

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 处理函数放进请求 ctx 的 Codex 审查子代理父会话哈希随选号发给主节点（选号在主节点）。
func TestSelectRequestCarriesTheGuardianParentSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request = c.Request.WithContext(service.WithOpenAIGuardianParentSessionHashes(c.Request.Context(), "cur", "legacy"))
	d := NewDispatcher(Deps{})
	key := &service.APIKey{ID: 1, Key: "sk-a"}

	sreq := d.selectRequest(c, stateOf(c), handler.OpenAIRelaySelectRequest{APIKey: key, Model: "codex-auto-review"})
	require.Equal(t, "cur", sreq.GetGuardianParentSessionHash())
	require.Equal(t, "legacy", sreq.GetGuardianParentLegacySessionHash())

	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	sreq = d.selectRequest(c, stateOf(c), handler.OpenAIRelaySelectRequest{APIKey: key, Model: "gpt-5"})
	require.Empty(t, sreq.GetGuardianParentSessionHash())
}
