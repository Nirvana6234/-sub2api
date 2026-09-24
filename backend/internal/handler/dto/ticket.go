package dto

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// TicketMessage 是工单下的一条消息。
type TicketMessage struct {
	ID         int64     `json:"id"`
	TicketID   int64     `json:"ticket_id"`
	SenderRole string    `json:"sender_role"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

// Ticket 是用户侧的工单视图。
//
// 不含 admin_unread_count：那是管理端的内部状态，用户不需要也不该看到。
type Ticket struct {
	ID                 int64           `json:"id"`
	Subject            string          `json:"subject"`
	Status             string          `json:"status"`
	UnreadCount        int             `json:"unread_count"`
	LastMessageAt      time.Time       `json:"last_message_at"`
	LastMessagePreview string          `json:"last_message_preview,omitempty"`
	ClosedAt           *time.Time      `json:"closed_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	Messages           []TicketMessage `json:"messages,omitempty"`
}

// AdminTicket 是管理端的工单视图，额外带上提交人信息与管理员侧未读数。
type AdminTicket struct {
	ID                 int64           `json:"id"`
	UserID             int64           `json:"user_id"`
	UserEmail          string          `json:"user_email,omitempty"`
	Subject            string          `json:"subject"`
	Status             string          `json:"status"`
	UnreadCount        int             `json:"unread_count"`
	LastMessageAt      time.Time       `json:"last_message_at"`
	LastMessagePreview string          `json:"last_message_preview,omitempty"`
	ClosedAt           *time.Time      `json:"closed_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	Messages           []TicketMessage `json:"messages,omitempty"`
}

func TicketMessageFromService(m *service.TicketMessage) *TicketMessage {
	if m == nil {
		return nil
	}
	return &TicketMessage{
		ID:         m.ID,
		TicketID:   m.TicketID,
		SenderRole: m.SenderRole,
		Content:    m.Content,
		CreatedAt:  m.CreatedAt,
	}
}

func ticketMessagesFromService(items []service.TicketMessage) []TicketMessage {
	if len(items) == 0 {
		return nil
	}
	out := make([]TicketMessage, 0, len(items))
	for i := range items {
		out = append(out, *TicketMessageFromService(&items[i]))
	}
	return out
}

func TicketFromService(t *service.Ticket) *Ticket {
	if t == nil {
		return nil
	}
	return &Ticket{
		ID:                 t.ID,
		Subject:            t.Subject,
		Status:             t.Status,
		UnreadCount:        t.UserUnreadCount,
		LastMessageAt:      t.LastMessageAt,
		LastMessagePreview: t.LastMessagePreview,
		ClosedAt:           t.ClosedAt,
		CreatedAt:          t.CreatedAt,
		UpdatedAt:          t.UpdatedAt,
		Messages:           ticketMessagesFromService(t.Messages),
	}
}

func AdminTicketFromService(t *service.Ticket) *AdminTicket {
	if t == nil {
		return nil
	}
	return &AdminTicket{
		ID:                 t.ID,
		UserID:             t.UserID,
		UserEmail:          t.UserEmail,
		Subject:            t.Subject,
		Status:             t.Status,
		UnreadCount:        t.AdminUnreadCount,
		LastMessageAt:      t.LastMessageAt,
		LastMessagePreview: t.LastMessagePreview,
		ClosedAt:           t.ClosedAt,
		CreatedAt:          t.CreatedAt,
		UpdatedAt:          t.UpdatedAt,
		Messages:           ticketMessagesFromService(t.Messages),
	}
}
