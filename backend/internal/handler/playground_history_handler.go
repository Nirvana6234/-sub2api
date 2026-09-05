package handler

import (
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// PlaygroundHistoryHandler keeps saved conversations scoped to the authenticated
// user, shared by chat and image workspaces.
type PlaygroundHistoryHandler struct {
	service *service.PlaygroundHistoryService
}

func NewPlaygroundHistoryHandler(service *service.PlaygroundHistoryService) *PlaygroundHistoryHandler {
	return &PlaygroundHistoryHandler{service: service}
}

func (h *PlaygroundHistoryHandler) Get(c *gin.Context) {
	userID, ok := h.historyScope(c)
	if !ok {
		return
	}

	state, err := h.service.Get(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if len(state) == 0 {
		response.Success(c, nil)
		return
	}
	response.Success(c, state)
}

func (h *PlaygroundHistoryHandler) Save(c *gin.Context) {
	userID, ok := h.historyScope(c)
	if !ok {
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, service.PlaygroundHistoryMaxBytes+1))
	if err != nil {
		response.BadRequest(c, "Failed to read Playground history")
		return
	}
	if len(body) > service.PlaygroundHistoryMaxBytes {
		response.Error(c, http.StatusRequestEntityTooLarge, "Playground history exceeds the allowed size")
		return
	}
	if err := h.service.Save(c.Request.Context(), userID, body); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"saved": true})
}

func (h *PlaygroundHistoryHandler) historyScope(c *gin.Context) (int64, bool) {
	if h == nil || h.service == nil {
		response.ErrorFrom(c, service.ErrPlaygroundHistoryUnavailable)
		return 0, false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	return subject.UserID, true
}
