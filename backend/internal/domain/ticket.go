package domain

import (
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 工单状态。
//
// 流转：用户创建 -> open；管理员回复 -> answered；用户追问 -> open；任一方关闭 -> closed。
// closed 的工单不再接受新消息，需要重新开单。
const (
	TicketStatusOpen     = "open"
	TicketStatusAnswered = "answered"
	TicketStatusClosed   = "closed"
)

// 消息发送方角色。
const (
	TicketSenderRoleUser  = "user"
	TicketSenderRoleAdmin = "admin"
)

// 内容长度限制。标题与正文都按 rune 计数，避免中文被按字节截断。
const (
	TicketSubjectMaxRunes = 200
	TicketContentMaxRunes = 10000
)

var (
	ErrTicketNotFound = infraerrors.NotFound("TICKET_NOT_FOUND", "ticket not found")
	// ErrTicketForbidden 用于用户访问不属于自己的工单。对外与 NotFound 同义，
	// 但保留独立 code 便于审计区分「不存在」与「越权」。
	ErrTicketForbidden       = infraerrors.NotFound("TICKET_NOT_FOUND", "ticket not found")
	ErrTicketClosed          = infraerrors.BadRequest("TICKET_CLOSED", "ticket is closed and no longer accepts replies")
	ErrTicketSubjectRequired = infraerrors.BadRequest("TICKET_SUBJECT_REQUIRED", "ticket subject is required")
	ErrTicketSubjectTooLong  = infraerrors.BadRequest("TICKET_SUBJECT_TOO_LONG", "ticket subject is too long")
	ErrTicketContentRequired = infraerrors.BadRequest("TICKET_CONTENT_REQUIRED", "ticket content is required")
	ErrTicketContentTooLong  = infraerrors.BadRequest("TICKET_CONTENT_TOO_LONG", "ticket content is too long")
	ErrTicketInvalidStatus   = infraerrors.BadRequest("TICKET_STATUS_INVALID", "ticket status is invalid")
)

// Ticket 是工单主体。
type Ticket struct {
	ID               int64      `json:"id"`
	UserID           int64      `json:"user_id"`
	Subject          string     `json:"subject"`
	Status           string     `json:"status"`
	UserUnreadCount  int        `json:"user_unread_count"`
	AdminUnreadCount int        `json:"admin_unread_count"`
	LastMessageAt    time.Time  `json:"last_message_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`

	// 以下字段不落库，由 service 组装后返回。
	Messages []TicketMessage `json:"messages,omitempty"`
	// UserEmail 仅在管理员侧列表/详情中填充。
	UserEmail string `json:"user_email,omitempty"`
	// LastMessagePreview 列表页展示用的最后一条消息摘要。
	LastMessagePreview string `json:"last_message_preview,omitempty"`
}

// TicketMessage 是工单下的一条消息。
type TicketMessage struct {
	ID           int64     `json:"id"`
	TicketID     int64     `json:"ticket_id"`
	SenderUserID int64     `json:"sender_user_id"`
	SenderRole   string    `json:"sender_role"`
	Content      string    `json:"content"`
	CreatedAt    time.Time `json:"created_at"`
}

// IsValidTicketStatus 校验状态取值。
func IsValidTicketStatus(status string) bool {
	switch status {
	case TicketStatusOpen, TicketStatusAnswered, TicketStatusClosed:
		return true
	default:
		return false
	}
}

// NormalizeTicketSubject 去除首尾空白并校验长度。
func NormalizeTicketSubject(subject string) (string, error) {
	trimmed := strings.TrimSpace(subject)
	if trimmed == "" {
		return "", ErrTicketSubjectRequired
	}
	if len([]rune(trimmed)) > TicketSubjectMaxRunes {
		return "", ErrTicketSubjectTooLong
	}
	return trimmed, nil
}

// NormalizeTicketContent 去除首尾空白并校验长度。
func NormalizeTicketContent(content string) (string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", ErrTicketContentRequired
	}
	if len([]rune(trimmed)) > TicketContentMaxRunes {
		return "", ErrTicketContentTooLong
	}
	return trimmed, nil
}

// TicketMessagePreview 截取消息摘要用于列表展示。
func TicketMessagePreview(content string, maxRunes int) string {
	flattened := strings.Join(strings.Fields(content), " ")
	runes := []rune(flattened)
	if len(runes) <= maxRunes {
		return flattened
	}
	return string(runes[:maxRunes]) + "…"
}
