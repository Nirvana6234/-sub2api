-- 工单表：用户提交问题，管理员回复
CREATE TABLE IF NOT EXISTS tickets (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject VARCHAR(200) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'open',
    user_unread_count INTEGER NOT NULL DEFAULT 0,
    admin_unread_count INTEGER NOT NULL DEFAULT 0,
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 工单消息表：用户与管理员的往返对话
CREATE TABLE IF NOT EXISTS ticket_messages (
    id BIGSERIAL PRIMARY KEY,
    ticket_id BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    sender_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sender_role VARCHAR(20) NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_tickets_user_id ON tickets(user_id);
CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets(status);
CREATE INDEX IF NOT EXISTS idx_tickets_last_message_at ON tickets(last_message_at);
-- 用户侧列表：按用户过滤 + 按最后消息时间倒序
CREATE INDEX IF NOT EXISTS idx_tickets_user_id_last_message_at ON tickets(user_id, last_message_at);

CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket_id ON ticket_messages(ticket_id);
CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket_id_created_at ON ticket_messages(ticket_id, created_at);

COMMENT ON TABLE tickets IS '用户工单';
COMMENT ON COLUMN tickets.status IS '状态: open(待管理员回复), answered(已回复), closed(已关闭)';
COMMENT ON COLUMN tickets.user_unread_count IS '用户侧未读消息数（管理员回复时递增，用户查看后清零）';
COMMENT ON COLUMN tickets.admin_unread_count IS '管理员侧未读消息数（用户发言时递增，管理员查看后清零）';
COMMENT ON COLUMN tickets.last_message_at IS '最后一条消息时间，用于列表排序';

COMMENT ON TABLE ticket_messages IS '工单消息';
COMMENT ON COLUMN ticket_messages.sender_role IS '发言人角色: user, admin';
COMMENT ON COLUMN ticket_messages.sender_user_id IS '发言人用户ID（管理员回复时为管理员自己的用户ID）';
