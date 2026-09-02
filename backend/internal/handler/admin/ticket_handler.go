package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// TicketHandler handles admin ticket operations
type TicketHandler struct {
	ticketService *service.TicketService
}

func NewTicketHandler(ticketService *service.TicketService) *TicketHandler {
	return &TicketHandler{ticketService: ticketService}
}

type replyTicketRequest struct {
	Content string `json:"content" binding:"required"`
}

type updateTicketStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=open answered closed"`
}

type batchTicketReadStatusRequest struct {
	IDs  []int64 `json:"ids"`
	Read *bool   `json:"read"`
}

type batchTicketIDsRequest struct {
	IDs []int64 `json:"ids"`
}

const maxBatchTicketIDs = 500

// List handles listing all users' tickets
// GET /api/v1/admin/tickets
func (h *TicketHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 200 {
		search = search[:200]
	}

	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "last_message_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}

	items, result, err := h.ticketService.ListForAdmin(
		c.Request.Context(),
		params,
		service.TicketListFilters{
			Status:     strings.TrimSpace(c.Query("status")),
			Search:     search,
			UnreadOnly: parseBoolQuery(c.Query("unread_only")),
		},
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.AdminTicket, 0, len(items))
	for i := range items {
		out = append(out, *dto.AdminTicketFromService(&items[i]))
	}
	response.Paginated(c, out, result.Total, page, pageSize)
}

// GetByID handles getting any ticket with its messages
// GET /api/v1/admin/tickets/:id
func (h *TicketHandler) GetByID(c *gin.Context) {
	ticketID, ok := parseTicketIDParam(c)
	if !ok {
		return
	}

	ticket, err := h.ticketService.GetForAdmin(c.Request.Context(), ticketID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.AdminTicketFromService(ticket))
}

// BatchReadStatus handles marking selected tickets as read or unread.
// POST /api/v1/admin/tickets/batch-read-status
func (h *TicketHandler) BatchReadStatus(c *gin.Context) {
	var req batchTicketReadStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Read == nil || !validBatchTicketIDs(req.IDs) {
		response.BadRequest(c, "Invalid ticket IDs")
		return
	}

	if err := h.ticketService.SetReadStatusByAdmin(c.Request.Context(), req.IDs, *req.Read); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "ok"})
}

// BatchDelete handles deleting selected tickets and their messages.
// POST /api/v1/admin/tickets/batch-delete
func (h *TicketHandler) BatchDelete(c *gin.Context) {
	var req batchTicketIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validBatchTicketIDs(req.IDs) {
		response.BadRequest(c, "Invalid ticket IDs")
		return
	}

	if err := h.ticketService.DeleteByAdmin(c.Request.Context(), req.IDs); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "ok"})
}

// Reply handles an admin replying to a ticket
// POST /api/v1/admin/tickets/:id/messages
func (h *TicketHandler) Reply(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	ticketID, ok := parseTicketIDParam(c)
	if !ok {
		return
	}

	var req replyTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	message, err := h.ticketService.ReplyAsAdmin(c.Request.Context(), subject.UserID, ticketID, req.Content)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.TicketMessageFromService(message))
}

// UpdateStatus handles an admin closing or reopening a ticket
// PUT /api/v1/admin/tickets/:id/status
func (h *TicketHandler) UpdateStatus(c *gin.Context) {
	ticketID, ok := parseTicketIDParam(c)
	if !ok {
		return
	}

	var req updateTicketStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	if err := h.ticketService.UpdateStatusByAdmin(c.Request.Context(), ticketID, req.Status); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "ok"})
}

// UnreadCount returns how many tickets are waiting on an admin reply
// GET /api/v1/admin/tickets/unread-count
func (h *TicketHandler) UnreadCount(c *gin.Context) {
	count, err := h.ticketService.CountUnreadForAdmin(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"count": count})
}

func parseTicketIDParam(c *gin.Context) (int64, bool) {
	ticketID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || ticketID <= 0 {
		response.BadRequest(c, "Invalid ticket ID")
		return 0, false
	}
	return ticketID, true
}

func parseBoolQuery(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func validBatchTicketIDs(ids []int64) bool {
	if len(ids) == 0 || len(ids) > maxBatchTicketIDs {
		return false
	}
	for _, id := range ids {
		if id <= 0 {
			return false
		}
	}
	return true
}
