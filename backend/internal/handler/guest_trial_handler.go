package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// guestTrialDeviceHeader 与 middleware.GuestTrialDeviceHeader 一致（handler 包不依赖 middleware）。
const guestTrialDeviceHeader = "X-Guest-Trial-Device"

// GuestTrialHandler 未注册访客试用的公开接口：查询状态、完成人机验证。
// 聊天本身走 middleware.GuestTrialChatGate + 网关聊天处理器。
type GuestTrialHandler struct {
	trial *service.GuestTrialService
}

func NewGuestTrialHandler(trial *service.GuestTrialService) *GuestTrialHandler {
	return &GuestTrialHandler{trial: trial}
}

// Service 试用聊天入口中间件复用同一个服务实例做校验和扣额度。
func (h *GuestTrialHandler) Service() *service.GuestTrialService {
	if h == nil {
		return nil
	}
	return h.trial
}

// guestTrialClientIP 与聊天入口使用同一个 IP 来源（匿名请求没有会话绑定，即可信代理链解析结果），
// 否则「已验证」标记按设备 + IP 绑定时会对不上。
func guestTrialClientIP(c *gin.Context) string {
	return ip.GetTrustedClientIP(c)
}

// Config GET /api/v1/trial/config
func (h *GuestTrialHandler) Config(c *gin.Context) {
	device := strings.TrimSpace(c.GetHeader(guestTrialDeviceHeader))
	state, err := h.trial.State(c.Request.Context(), device, guestTrialClientIP(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

type guestTrialVerifyRequest struct {
	TurnstileToken string `json:"turnstile_token"`
	TencentTicket  string `json:"tencent_ticket"`
	TencentRandstr string `json:"tencent_randstr"`
}

// Verify POST /api/v1/trial/verify
func (h *GuestTrialHandler) Verify(c *gin.Context) {
	var req guestTrialVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, service.ErrGuestTrialInvalidRequest)
		return
	}
	device := strings.TrimSpace(c.GetHeader(guestTrialDeviceHeader))
	proof := service.CaptchaProof{
		TurnstileToken: req.TurnstileToken,
		TencentTicket:  req.TencentTicket,
		TencentRandstr: req.TencentRandstr,
	}
	if err := h.trial.Verify(c.Request.Context(), device, guestTrialClientIP(c), proof); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"verified": true})
}
