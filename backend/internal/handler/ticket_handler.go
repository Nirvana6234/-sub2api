package handler

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

// TicketHandler handles user ticket operations
type TicketHandler struct {
	ticketService *service.TicketService
}

func NewTicketHandler(ticketService *service.TicketService) *TicketHandler {
	return &TicketHandler{ticketService: ticketService}
}

type createTicketRequest struct {
	Subject string `json:"subject" binding:"required"`
	Content string `json:"content" binding:"required"`
}

type replyTicketRequest struct {
	Content string `json:"content" binding:"required"`
}

// List handles listing the current user's own tickets
// GET /api/v1/tickets
func (h *TicketHandler) List(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "last_message_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}

	items, result, err := h.ticketService.ListForUser(
		c.Request.Context(),
		subject.UserID,
		params,
		service.TicketListFilters{
			Status:     strings.TrimSpace(c.Query("status")),
			Search:     trimSearchQuery(c.Query("search")),
			UnreadOnly: parseBoolQuery(c.Query("unread_only")),
		},
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.Ticket, 0, len(items))
	for i := range items {
		out = append(out, *dto.TicketFromService(&items[i]))
	}
	response.Paginated(c, out, result.Total, page, pageSize)
}

// Create handles creating a new ticket
// POST /api/v1/tickets
func (h *TicketHandler) Create(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	var req createTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	ticket, err := h.ticketService.CreateTicket(c.Request.Context(), service.CreateTicketInput{
		UserID:  subject.UserID,
		Subject: req.Subject,
		Content: req.Content,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.TicketFromService(ticket))
}

// GetByID handles getting one of the current user's tickets with its messages
// GET /api/v1/tickets/:id
func (h *TicketHandler) GetByID(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	ticketID, ok := parseTicketIDParam(c)
	if !ok {
		return
	}

	ticket, err := h.ticketService.GetForUser(c.Request.Context(), subject.UserID, ticketID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.TicketFromService(ticket))
}

// Reply handles the user adding a follow-up message to their own ticket
// POST /api/v1/tickets/:id/messages
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

	message, err := h.ticketService.ReplyAsUser(c.Request.Context(), subject.UserID, ticketID, req.Content)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.TicketMessageFromService(message))
}

// Close handles the user closing their own ticket
// POST /api/v1/tickets/:id/close
func (h *TicketHandler) Close(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	ticketID, ok := parseTicketIDParam(c)
	if !ok {
		return
	}

	if err := h.ticketService.CloseByUser(c.Request.Context(), subject.UserID, ticketID); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "ok"})
}

// UnreadCount returns how many of the user's tickets have unread admin replies
// GET /api/v1/tickets/unread-count
func (h *TicketHandler) UnreadCount(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	count, err := h.ticketService.CountUnreadForUser(c.Request.Context(), subject.UserID)
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

// trimSearchQuery 限制搜索词长度，避免超长输入打到数据库。
func trimSearchQuery(v string) string {
	search := strings.TrimSpace(v)
	if len(search) > 200 {
		return search[:200]
	}
	return search
}
