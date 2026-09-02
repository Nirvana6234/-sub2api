package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/ticket"
	"github.com/Wei-Shaw/sub2api/ent/ticketmessage"
	entuser "github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type ticketRepository struct {
	client *dbent.Client
}

func NewTicketRepository(client *dbent.Client) service.TicketRepository {
	return &ticketRepository{client: client}
}

func (r *ticketRepository) CreateWithFirstMessage(
	ctx context.Context,
	t *service.Ticket,
	firstMessage *service.TicketMessage,
) error {
	return r.withTx(ctx, func(txCtx context.Context, txClient *dbent.Client) error {
		created, err := txClient.Ticket.Create().
			SetUserID(t.UserID).
			SetSubject(t.Subject).
			SetStatus(t.Status).
			SetUserUnreadCount(t.UserUnreadCount).
			SetAdminUnreadCount(t.AdminUnreadCount).
			SetLastMessageAt(t.LastMessageAt).
			Save(txCtx)
		if err != nil {
			return translatePersistenceError(err, service.ErrTicketNotFound, nil)
		}

		msg, err := txClient.TicketMessage.Create().
			SetTicketID(created.ID).
			SetSenderUserID(firstMessage.SenderUserID).
			SetSenderRole(firstMessage.SenderRole).
			SetContent(firstMessage.Content).
			SetCreatedAt(firstMessage.CreatedAt).
			Save(txCtx)
		if err != nil {
			return translatePersistenceError(err, service.ErrTicketNotFound, nil)
		}

		t.ID = created.ID
		t.CreatedAt = created.CreatedAt
		t.UpdatedAt = created.UpdatedAt
		firstMessage.ID = msg.ID
		firstMessage.TicketID = created.ID
		return nil
	})
}

func (r *ticketRepository) GetByID(ctx context.Context, id int64) (*service.Ticket, error) {
	row, err := r.client.Ticket.Query().
		Where(ticket.IDEQ(id)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return ticketEntityToService(row), nil
}

func (r *ticketRepository) GetByIDForUser(ctx context.Context, id, userID int64) (*service.Ticket, error) {
	row, err := r.client.Ticket.Query().
		Where(
			ticket.IDEQ(id),
			ticket.UserIDEQ(userID),
		).
		Only(ctx)
	if err != nil {
		// user_id 是查询条件的一部分，所以「别人的工单」和「不存在的工单」
		// 都会落到 NotFound，对外表现一致，无法通过响应差异探测他人工单是否存在。
		return nil, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return ticketEntityToService(row), nil
}

func (r *ticketRepository) ListByUser(
	ctx context.Context,
	userID int64,
	params pagination.PaginationParams,
	filters service.TicketListFilters,
) ([]service.Ticket, *pagination.PaginationResult, error) {
	q := r.client.Ticket.Query().Where(ticket.UserIDEQ(userID))
	q = applyTicketFilters(q, filters, false)
	if filters.UnreadOnly {
		q = q.Where(ticket.UserUnreadCountGT(0))
	}
	return r.listTickets(ctx, q, params, false)
}

func (r *ticketRepository) ListAll(
	ctx context.Context,
	params pagination.PaginationParams,
	filters service.TicketListFilters,
) ([]service.Ticket, *pagination.PaginationResult, error) {
	q := r.client.Ticket.Query()
	q = applyTicketFilters(q, filters, true)
	if filters.UnreadOnly {
		q = q.Where(ticket.AdminUnreadCountGT(0))
	}
	return r.listTickets(ctx, q, params, true)
}

func (r *ticketRepository) listTickets(
	ctx context.Context,
	q *dbent.TicketQuery,
	params pagination.PaginationParams,
	withUserEmail bool,
) ([]service.Ticket, *pagination.PaginationResult, error) {
	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}

	itemsQuery := q.
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range ticketListOrders(params) {
		itemsQuery = itemsQuery.Order(order)
	}
	if withUserEmail {
		itemsQuery = itemsQuery.WithUser()
	}

	rows, err := itemsQuery.All(ctx)
	if err != nil {
		return nil, nil, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}

	out := make([]service.Ticket, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		item := ticketEntityToService(row)
		if withUserEmail && row.Edges.User != nil {
			item.UserEmail = row.Edges.User.Email
		}
		out = append(out, *item)
		ids = append(ids, row.ID)
	}

	previews, err := r.lastMessagePreviews(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	for i := range out {
		out[i].LastMessagePreview = previews[out[i].ID]
	}

	return out, paginationResultFromTotal(int64(total), params), nil
}

// lastMessagePreviews 取每个工单的最后一条消息内容。
//
// 一次查出这批工单的全部消息再在内存里取最后一条会把长正文全load进来，
// 这里改为按 ticket_id 倒序取，遍历时只保留首次出现的那条。
func (r *ticketRepository) lastMessagePreviews(ctx context.Context, ticketIDs []int64) (map[int64]string, error) {
	if len(ticketIDs) == 0 {
		return map[int64]string{}, nil
	}

	rows, err := r.client.TicketMessage.Query().
		Where(ticketmessage.TicketIDIn(ticketIDs...)).
		Order(
			dbent.Asc(ticketmessage.FieldTicketID),
			dbent.Desc(ticketmessage.FieldCreatedAt),
			dbent.Desc(ticketmessage.FieldID),
		).
		All(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}

	out := make(map[int64]string, len(ticketIDs))
	for _, row := range rows {
		if _, seen := out[row.TicketID]; seen {
			continue
		}
		out[row.TicketID] = row.Content
	}
	return out, nil
}

func (r *ticketRepository) ListMessages(ctx context.Context, ticketID int64) ([]service.TicketMessage, error) {
	rows, err := r.client.TicketMessage.Query().
		Where(ticketmessage.TicketIDEQ(ticketID)).
		Order(
			dbent.Asc(ticketmessage.FieldCreatedAt),
			dbent.Asc(ticketmessage.FieldID),
		).
		All(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}

	out := make([]service.TicketMessage, 0, len(rows))
	for _, row := range rows {
		out = append(out, service.TicketMessage{
			ID:           row.ID,
			TicketID:     row.TicketID,
			SenderUserID: row.SenderUserID,
			SenderRole:   row.SenderRole,
			Content:      row.Content,
			CreatedAt:    row.CreatedAt,
		})
	}
	return out, nil
}

func (r *ticketRepository) AppendMessage(ctx context.Context, msg *service.TicketMessage, nextStatus string) error {
	return r.withTx(ctx, func(txCtx context.Context, txClient *dbent.Client) error {
		created, err := txClient.TicketMessage.Create().
			SetTicketID(msg.TicketID).
			SetSenderUserID(msg.SenderUserID).
			SetSenderRole(msg.SenderRole).
			SetContent(msg.Content).
			SetCreatedAt(msg.CreatedAt).
			Save(txCtx)
		if err != nil {
			return translatePersistenceError(err, service.ErrTicketNotFound, nil)
		}

		update := txClient.Ticket.UpdateOneID(msg.TicketID).
			SetStatus(nextStatus).
			SetLastMessageAt(msg.CreatedAt)

		// 对端未读 +1，自己这侧清零（发言即代表已读）。
		if msg.SenderRole == service.TicketSenderRoleUser {
			update = update.AddAdminUnreadCount(1).SetUserUnreadCount(0)
		} else {
			update = update.AddUserUnreadCount(1).SetAdminUnreadCount(0)
		}

		if _, err := update.Save(txCtx); err != nil {
			return translatePersistenceError(err, service.ErrTicketNotFound, nil)
		}

		msg.ID = created.ID
		return nil
	})
}

func (r *ticketRepository) ClearUnread(ctx context.Context, ticketID int64, role string) error {
	client := clientFromContext(ctx, r.client)
	update := client.Ticket.UpdateOneID(ticketID)
	if role == service.TicketSenderRoleUser {
		update = update.SetUserUnreadCount(0)
	} else {
		update = update.SetAdminUnreadCount(0)
	}

	if _, err := update.Save(ctx); err != nil {
		return translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return nil
}

func (r *ticketRepository) SetUnreadStatus(
	ctx context.Context,
	ticketIDs []int64,
	role string,
	unread bool,
) error {
	if len(ticketIDs) == 0 {
		return nil
	}

	uniqueIDs := make([]int64, 0, len(ticketIDs))
	seen := make(map[int64]struct{}, len(ticketIDs))
	for _, id := range ticketIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return nil
	}

	count := 0
	if unread {
		count = 1
	}
	update := clientFromContext(ctx, r.client).Ticket.Update().
		Where(ticket.IDIn(uniqueIDs...))
	if role == service.TicketSenderRoleUser {
		update = update.SetUserUnreadCount(count)
	} else {
		update = update.SetAdminUnreadCount(count)
	}
	if _, err := update.Save(ctx); err != nil {
		return translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return nil
}

func (r *ticketRepository) DeleteByIDs(ctx context.Context, ticketIDs []int64) error {
	if len(ticketIDs) == 0 {
		return nil
	}

	uniqueIDs := make([]int64, 0, len(ticketIDs))
	seen := make(map[int64]struct{}, len(ticketIDs))
	for _, id := range ticketIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return nil
	}

	if _, err := clientFromContext(ctx, r.client).Ticket.Delete().
		Where(ticket.IDIn(uniqueIDs...)).
		Exec(ctx); err != nil {
		return translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return nil
}

func (r *ticketRepository) UpdateStatus(ctx context.Context, ticketID int64, status string, closedAt *time.Time) error {
	client := clientFromContext(ctx, r.client)
	update := client.Ticket.UpdateOneID(ticketID).SetStatus(status)
	if closedAt != nil {
		update = update.SetClosedAt(*closedAt)
	} else {
		update = update.ClearClosedAt()
	}

	if _, err := update.Save(ctx); err != nil {
		return translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return nil
}

func (r *ticketRepository) CountUnreadForUser(ctx context.Context, userID int64) (int64, error) {
	count, err := r.client.Ticket.Query().
		Where(
			ticket.UserIDEQ(userID),
			ticket.UserUnreadCountGT(0),
		).
		Count(ctx)
	if err != nil {
		return 0, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return int64(count), nil
}

func (r *ticketRepository) CountUnreadForAdmin(ctx context.Context) (int64, error) {
	count, err := r.client.Ticket.Query().
		Where(ticket.AdminUnreadCountGT(0)).
		Count(ctx)
	if err != nil {
		return 0, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return int64(count), nil
}

func (r *ticketRepository) GetUserEmail(ctx context.Context, userID int64) (string, error) {
	row, err := r.client.User.Query().
		Where(entuser.IDEQ(userID)).
		Select(entuser.FieldEmail).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return "", nil
		}
		return "", translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return row.Email, nil
}

func applyTicketFilters(q *dbent.TicketQuery, filters service.TicketListFilters, isAdmin bool) *dbent.TicketQuery {
	if filters.Status != "" {
		q = q.Where(ticket.StatusEQ(filters.Status))
	}
	if filters.Search != "" {
		if isAdmin {
			// 管理员侧允许按提交人邮箱搜索。
			q = q.Where(
				ticket.Or(
					ticket.SubjectContainsFold(filters.Search),
					ticket.HasUserWith(entuser.EmailContainsFold(filters.Search)),
				),
			)
		} else {
			q = q.Where(ticket.SubjectContainsFold(filters.Search))
		}
	}
	return q
}

// ticketListOrders 返回列表排序项。始终以 id 兜底，保证分页在同值行上稳定。
func ticketListOrders(params pagination.PaginationParams) []ticket.OrderOption {
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)

	field := ticket.FieldLastMessageAt
	switch strings.ToLower(strings.TrimSpace(params.SortBy)) {
	case "created_at":
		field = ticket.FieldCreatedAt
	case "status":
		field = ticket.FieldStatus
	case "subject":
		field = ticket.FieldSubject
	}

	if sortOrder == pagination.SortOrderAsc {
		return []ticket.OrderOption{dbent.Asc(field), dbent.Asc(ticket.FieldID)}
	}
	return []ticket.OrderOption{dbent.Desc(field), dbent.Desc(ticket.FieldID)}
}

func ticketEntityToService(row *dbent.Ticket) *service.Ticket {
	return &service.Ticket{
		ID:               row.ID,
		UserID:           row.UserID,
		Subject:          row.Subject,
		Status:           row.Status,
		UserUnreadCount:  row.UserUnreadCount,
		AdminUnreadCount: row.AdminUnreadCount,
		LastMessageAt:    row.LastMessageAt,
		ClosedAt:         row.ClosedAt,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

func (r *ticketRepository) withTx(ctx context.Context, fn func(txCtx context.Context, txClient *dbent.Client) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin ticket transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx, tx.Client()); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ticket transaction: %w", err)
	}
	return nil
}
