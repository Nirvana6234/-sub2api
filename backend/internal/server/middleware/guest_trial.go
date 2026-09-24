package middleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GuestTrialDeviceHeader 试用页在浏览器本地生成的随机设备标识，用于按访客计数。
const GuestTrialDeviceHeader = "X-Guest-Trial-Device"

// GuestTrialRemainingHeader 放行后回写访客当天剩余次数，试用页据此更新提示。
const GuestTrialRemainingHeader = "X-Guest-Trial-Remaining"

// GuestTrialChatGate 未注册访客的试用聊天入口：
// 校验并清洗请求体、扣减额度，再以管理员指定的试用密钥身份交给网关聊天处理器。
// 访客自己带来的任何凭据头都会被清掉，只能用试用密钥。
func GuestTrialChatGate(trial *service.GuestTrialService, apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if trial == nil || apiKeyService == nil {
			response.ErrorFrom(c, service.ErrGuestTrialUnavailable)
			c.Abort()
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, NewErrorResponse("GUEST_TRIAL_INPUT_TOO_LONG", pkghttputil.BodyTooLargeMessage(maxErr.Limit)))
				return
			}
			response.ErrorFrom(c, service.ErrGuestTrialInvalidRequest)
			c.Abort()
			return
		}

		device := strings.TrimSpace(c.GetHeader(GuestTrialDeviceHeader))
		prepared, err := trial.PrepareChat(c.Request.Context(), device, SecurityClientIP(c), body)
		if err != nil {
			response.ErrorFrom(c, err)
			c.Abort()
			return
		}

		for _, header := range []string{GuestTrialDeviceHeader, "Authorization", "x-api-key", "x-goog-api-key", PlaygroundKeyIDHeader} {
			c.Request.Header.Del(header)
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(prepared.Body))
		c.Request.ContentLength = int64(len(prepared.Body))
		c.Header(GuestTrialRemainingHeader, strconv.Itoa(prepared.Remaining))

		apiKey, err := apiKeyService.GetByIDForAuth(c.Request.Context(), prepared.APIKeyID)
		if err != nil || apiKey == nil {
			response.ErrorFrom(c, service.ErrGuestTrialUnavailable)
			c.Abort()
			return
		}
		SetOpsFallbackAPIKey(c, apiKey)
		authenticateResolvedAPIKey(c, apiKey, apiKeyService, subscriptionService, cfg)
	}
}
