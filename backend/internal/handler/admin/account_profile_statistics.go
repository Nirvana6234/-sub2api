package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// SetAccountProfileStatisticsService attaches the optional Codex profile
// statistics lookup service.
func (h *AccountHandler) SetAccountProfileStatisticsService(svc *service.AccountProfileStatisticsService) {
	h.profileStatistics = svc
}

// GetAccountProfileStatistics 返回指定 OpenAI OAuth 账号的 Codex 用户画像
// （profiles/me：昵称、头像、累计 token、连续使用天数、每日用量、常用推理强度等）。
// 只读、按需拉取，命中缓存直接返回；?refresh=true 强制绕过缓存重新拉取。
// 凭据本身永远不出现在返回体或日志里。
func (h *AccountHandler) GetAccountProfileStatistics(c *gin.Context) {
	if h == nil || h.profileStatistics == nil {
		response.ErrorFrom(c, service.ErrCodexProfileStatisticsUnavailable)
		return
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	forceRefresh := c.Query("refresh") == "true"
	stats, err := h.profileStatistics.GetProfileStatistics(c.Request.Context(), accountID, forceRefresh)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stats)
}
