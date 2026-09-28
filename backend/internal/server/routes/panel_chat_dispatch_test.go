package routes

import (
	"net/http/httptest"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 试用 / Playground 面板的聊天入口与 /v1/chat/completions 共用平台判断：
// 生产上 deepseek 分组的试用请求曾被送进 Anthropic 网关，拼出 /v1/v1/messages 而 404。
func TestOpenAIGatewayPlatformDispatchCoversCNProviders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := map[string]bool{
		service.PlatformOpenAI:     true,
		service.PlatformGrok:       true,
		service.PlatformDeepseek:   true,
		service.PlatformKimi:       true,
		service.PlatformZhipu:      true,
		service.PlatformMiniMax:    true,
		service.PlatformOpenCodeGo: true,
		service.PlatformAnthropic:  false,
		service.PlatformGemini:     false,
	}
	for platform, want := range cases {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		groupID := int64(44)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: platform},
		})
		require.Equal(t, want, isOpenAIResponsesCompatibleGatewayPlatform(c), platform)
	}
}
