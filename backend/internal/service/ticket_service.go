package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// ticketPreviewRunes 是列表页最后一条消息摘要的长度上限。
const ticketPreviewRunes = 80

type TicketService struct {
	ticketRepo TicketRepository
}

func NewTicketService(ticketRepo TicketRepository) *TicketService {
	return &TicketService{ticketRepo: ticketRepo}
}

type CreateTicketInput struct {
	UserID  int64
	Subject string
	Content string
}

// CreateTicket 由用户创建工单，首条消息即为提问内容。
func (s *TicketService) CreateTicket(ctx context.Context, input CreateTicketInput) (*Ticket, error) {
	subject, err := domain.NormalizeTicketSubject(input.Subject)
	if err != nil {
		return nil, err
	}
	content, err := domain.NormalizeTicketContent(input.Content)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	ticket := &Ticket{
		UserID:  input.UserID,
		Subject: subject,
		Status:  TicketStatusOpen,
		// 新工单对用户自己没有未读，但管理员有一条待处理。
		UserUnreadCount:  0,
		AdminUnreadCount: 1,
		LastMessageAt:    now,
	}
	message := &TicketMessage{
		SenderUserID: input.UserID,
		SenderRole:   TicketSenderRoleUser,
		Content:      content,
		CreatedAt:    now,
	}

	if err := s.ticketRepo.CreateWithFirstMessage(ctx, ticket, message); err != nil {
		return nil, err
	}

	ticket.Messages = []TicketMessage{*message}
	ticket.LastMessagePreview = domain.TicketMessagePreview(content, ticketPreviewRunes)
	return ticket, nil
}

// ListForUser 返回当前用户自己的工单列表。
func (s *TicketService) ListForUser(
	ctx context.Context,
	userID int64,
	params pagination.PaginationParams,
	filters TicketListFilters,
) ([]Ticket, *pagination.PaginationResult, error) {
	return s.ticketRepo.ListByUser(ctx, userID, params, filters)
}

// ListForAdmin 返回全部用户的工单列表。
func (s *TicketService) ListForAdmin(
	ctx context.Context,
	params pagination.PaginationParams,
	filters TicketListFilters,
) ([]Ticket, *pagination.PaginationResult, error) {
	return s.ticketRepo.ListAll(ctx, params, filters)
}

// GetForUser 返回用户自己的工单详情，并把用户侧未读清零。
func (s *TicketService) GetForUser(ctx context.Context, userID, ticketID int64) (*Ticket, error) {
	ticket, err := s.ticketRepo.GetByIDForUser(ctx, ticketID, userID)
	if err != nil {
		return nil, err
	}
	return s.loadDetail(ctx, ticket, TicketSenderRoleUser)
}

// GetForAdmin 返回任意工单详情，并把管理员侧未读清零。
func (s *TicketService) GetForAdmin(ctx context.Context, ticketID int64) (*Ticket, error) {
	ticket, err := s.ticketRepo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if email, err := s.ticketRepo.GetUserEmail(ctx, ticket.UserID); err == nil {
		ticket.UserEmail = email
	}
	return s.loadDetail(ctx, ticket, TicketSenderRoleAdmin)
}

// SetReadStatusByAdmin 批量设置管理员侧的已读/未读状态。
func (s *TicketService) SetReadStatusByAdmin(ctx context.Context, ticketIDs []int64, read bool) error {
	return s.ticketRepo.SetUnreadStatus(ctx, ticketIDs, TicketSenderRoleAdmin, !read)
}

// DeleteByAdmin 批量删除工单及其全部消息。
func (s *TicketService) DeleteByAdmin(ctx context.Context, ticketIDs []int64) error {
	return s.ticketRepo.DeleteByIDs(ctx, ticketIDs)
}

// loadDetail 装载消息并清除指定一侧的未读标记。
func (s *TicketService) loadDetail(ctx context.Context, ticket *Ticket, viewerRole string) (*Ticket, error) {
	messages, err := s.ticketRepo.ListMessages(ctx, ticket.ID)
	if err != nil {
		return nil, err
	}
	ticket.Messages = messages

	needClear := (viewerRole == TicketSenderRoleUser && ticket.UserUnreadCount > 0) ||
		(viewerRole == TicketSenderRoleAdmin && ticket.AdminUnreadCount > 0)
	if needClear {
		if err := s.ticketRepo.ClearUnread(ctx, ticket.ID, viewerRole); err != nil {
			return nil, err
		}
		if viewerRole == TicketSenderRoleUser {
			ticket.UserUnreadCount = 0
		} else {
			ticket.AdminUnreadCount = 0
		}
	}
	return ticket, nil
}

// ReplyAsUser 由用户追问。工单回到 open 等待管理员处理。
func (s *TicketService) ReplyAsUser(ctx context.Context, userID, ticketID int64, content string) (*TicketMessage, error) {
	ticket, err := s.ticketRepo.GetByIDForUser(ctx, ticketID, userID)
	if err != nil {
		return nil, err
	}
	return s.appendMessage(ctx, ticket, userID, TicketSenderRoleUser, content, TicketStatusOpen)
}

// ReplyAsAdmin 由管理员回复。工单转为 answered。
func (s *TicketService) ReplyAsAdmin(ctx context.Context, adminUserID, ticketID int64, content string) (*TicketMessage, error) {
	ticket, err := s.ticketRepo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	return s.appendMessage(ctx, ticket, adminUserID, TicketSenderRoleAdmin, content, TicketStatusAnswered)
}

func (s *TicketService) appendMessage(
	ctx context.Context,
	ticket *Ticket,
	senderUserID int64,
	senderRole string,
	content string,
	nextStatus string,
) (*TicketMessage, error) {
	if ticket.Status == TicketStatusClosed {
		return nil, ErrTicketClosed
	}
	normalized, err := domain.NormalizeTicketContent(content)
	if err != nil {
		return nil, err
	}

	message := &TicketMessage{
		TicketID:     ticket.ID,
		SenderUserID: senderUserID,
		SenderRole:   senderRole,
		Content:      normalized,
		CreatedAt:    time.Now(),
	}
	if err := s.ticketRepo.AppendMessage(ctx, message, nextStatus); err != nil {
		return nil, err
	}
	return message, nil
}

// CloseByUser 允许用户关闭自己的工单。
func (s *TicketService) CloseByUser(ctx context.Context, userID, ticketID int64) error {
	ticket, err := s.ticketRepo.GetByIDForUser(ctx, ticketID, userID)
	if err != nil {
		return err
	}
	if ticket.Status == TicketStatusClosed {
		return nil
	}
	now := time.Now()
	return s.ticketRepo.UpdateStatus(ctx, ticket.ID, TicketStatusClosed, &now)
}

// UpdateStatusByAdmin 允许管理员改状态（关闭或重开）。
func (s *TicketService) UpdateStatusByAdmin(ctx context.Context, ticketID int64, status string) error {
	if !domain.IsValidTicketStatus(status) {
		return ErrTicketInvalidStatus
	}
	if _, err := s.ticketRepo.GetByID(ctx, ticketID); err != nil {
		return err
	}

	var closedAt *time.Time
	if status == TicketStatusClosed {
		now := time.Now()
		closedAt = &now
	}
	return s.ticketRepo.UpdateStatus(ctx, ticketID, status, closedAt)
}

// CountUnreadForUser 返回用户有未读回复的工单数。
func (s *TicketService) CountUnreadForUser(ctx context.Context, userID int64) (int64, error) {
	return s.ticketRepo.CountUnreadForUser(ctx, userID)
}

// CountUnreadForAdmin 返回待管理员处理的工单数。
func (s *TicketService) CountUnreadForAdmin(ctx context.Context) (int64, error) {
	return s.ticketRepo.CountUnreadForAdmin(ctx)
}
