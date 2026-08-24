package admin

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type globalBlacklistEntryRequest struct {
	Kind      string     `json:"kind" binding:"required"`
	Value     string     `json:"value" binding:"required"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expires_at"`
	Enabled   *bool      `json:"enabled"`
}

// nonNilBlacklistEntries 保证空列表序列化为 []，而不是 nil slice 的 null。
// 前端按数组消费该字段，null 会在渲染期抛错。
func nonNilBlacklistEntries(entries []service.GlobalBlacklistEntry) []service.GlobalBlacklistEntry {
	if entries == nil {
		return []service.GlobalBlacklistEntry{}
	}
	return entries
}

// GetGlobalBlacklist lists administrator-managed account/IP deny rules.
func (h *SettingHandler) GetGlobalBlacklist(c *gin.Context) {
	entries, err := h.settingService.GlobalBlacklistSnapshot(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"entries": nonNilBlacklistEntries(entries)})
}

// AddGlobalBlacklist adds one account or IP rule.
func (h *SettingHandler) AddGlobalBlacklist(c *gin.Context) {
	var req globalBlacklistEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid blacklist entry: "+err.Error())
		return
	}
	entry := service.GlobalBlacklistEntry{Kind: req.Kind, Value: req.Value, Reason: req.Reason, ExpiresAt: req.ExpiresAt, Enabled: true}
	if req.Enabled != nil {
		entry.Enabled = *req.Enabled
	}
	created, err := h.settingService.AddGlobalBlacklistEntry(c.Request.Context(), entry)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Created(c, created)
}

// ReplaceGlobalBlacklist replaces the complete rule set atomically.
func (h *SettingHandler) ReplaceGlobalBlacklist(c *gin.Context) {
	var req struct {
		Entries []service.GlobalBlacklistEntry `json:"entries"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid blacklist payload: "+err.Error())
		return
	}
	entries, err := h.settingService.ReplaceGlobalBlacklist(c.Request.Context(), req.Entries)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"entries": nonNilBlacklistEntries(entries)})
}

// DeleteGlobalBlacklist removes a rule by its generated ID.
func (h *SettingHandler) DeleteGlobalBlacklist(c *gin.Context) {
	if err := h.settingService.DeleteGlobalBlacklistEntry(c.Request.Context(), c.Param("id")); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
