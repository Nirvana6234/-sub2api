package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const (
	TicketStatusOpen     = domain.TicketStatusOpen
	TicketStatusAnswered = domain.TicketStatusAnswered
	TicketStatusClosed   = domain.TicketStatusClosed
)

const (
	TicketSenderRoleUser  = domain.TicketSenderRoleUser
	TicketSenderRoleAdmin = domain.TicketSenderRoleAdmin
)

var (
	ErrTicketNotFound        = domain.ErrTicketNotFound
	ErrTicketForbidden       = domain.ErrTicketForbidden
	ErrTicketClosed          = domain.ErrTicketClosed
	ErrTicketSubjectRequired = domain.ErrTicketSubjectRequired
	ErrTicketSubjectTooLong  = domain.ErrTicketSubjectTooLong
	ErrTicketContentRequired = domain.ErrTicketContentRequired
	ErrTicketContentTooLong  = domain.ErrTicketContentTooLong
	ErrTicketInvalidStatus   = domain.ErrTicketInvalidStatus
)

type Ticket = domain.Ticket

type TicketMessage = domain.TicketMessage

// TicketListFilters 是工单列表的过滤条件。
type TicketListFilters struct {
	// Status 为空表示不过滤。
	Status string
	// Search 匹配工单标题；管理员侧同时匹配提交人邮箱。
	Search string
	// UnreadOnly 只返回该侧有未读消息的工单。
	UnreadOnly bool
}

// TicketRepository 是工单的数据访问契约。
//
// 注意：所有按用户维度的查询都必须由调用方传入 userID 并在 SQL 层过滤，
// 不允许先取全量再在内存里筛——那样越权数据会经过进程内存，且分页会算错。
type TicketRepository interface {
	// CreateWithFirstMessage 在同一事务内创建工单及其首条消息。
	CreateWithFirstMessage(ctx context.Context, t *Ticket, firstMessage *TicketMessage) error

	// GetByID 返回工单本身，不含消息。调用方负责校验归属。
	GetByID(ctx context.Context, id int64) (*Ticket, error)

	// GetByIDForUser 仅在工单属于该用户时返回，否则返回 ErrTicketNotFound。
	GetByIDForUser(ctx context.Context, id, userID int64) (*Ticket, error)

	// ListByUser 返回该用户的工单分页。
	ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams, filters TicketListFilters) ([]Ticket, *pagination.PaginationResult, error)

	// ListAll 返回全部工单分页，并填充提交人邮箱。仅管理员侧使用。
	ListAll(ctx context.Context, params pagination.PaginationParams, filters TicketListFilters) ([]Ticket, *pagination.PaginationResult, error)

	// ListMessages 按时间正序返回工单下的全部消息。
	ListMessages(ctx context.Context, ticketID int64) ([]TicketMessage, error)

	// AppendMessage 在同一事务内追加消息、更新状态、未读计数与最后消息时间。
	// 对端未读数 +1，发送方自己的未读数清零。
	AppendMessage(ctx context.Context, msg *TicketMessage, nextStatus string) error

	// ClearUnread 将指定一侧的未读计数清零。role 取 TicketSenderRoleUser / TicketSenderRoleAdmin。
	ClearUnread(ctx context.Context, ticketID int64, role string) error

	// UpdateStatus 更新状态；closedAt 仅在关闭时写入，重开时传 nil 清空。
	UpdateStatus(ctx context.Context, ticketID int64, status string, closedAt *time.Time) error

	// CountUnreadForUser 返回该用户有未读回复的工单数，用于侧栏角标。
	CountUnreadForUser(ctx context.Context, userID int64) (int64, error)

	// CountUnreadForAdmin 返回有用户新消息待处理的工单数，用于管理员角标。
	CountUnreadForAdmin(ctx context.Context) (int64, error)

	// GetUserEmail 取提交人邮箱，用于管理员侧详情展示。
	GetUserEmail(ctx context.Context, userID int64) (string, error)
}
