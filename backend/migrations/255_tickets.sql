-- User support tickets and their conversation messages.
-- Keep this migration idempotent because the migration runner records files
-- but deployments can also be initialized from an existing partial schema.
CREATE TABLE IF NOT EXISTS tickets (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject VARCHAR(200) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'open',
    user_unread_count INTEGER NOT NULL DEFAULT 0,
    admin_unread_count INTEGER NOT NULL DEFAULT 0,
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_tickets_user_unread_count_nonnegative
        CHECK (user_unread_count >= 0),
    CONSTRAINT chk_tickets_admin_unread_count_nonnegative
        CHECK (admin_unread_count >= 0)
);

CREATE TABLE IF NOT EXISTS ticket_messages (
    id BIGSERIAL PRIMARY KEY,
    ticket_id BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    sender_user_id BIGINT NOT NULL,
    sender_role VARCHAR(20) NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tickets_user_id
    ON tickets (user_id);
CREATE INDEX IF NOT EXISTS idx_tickets_status
    ON tickets (status);
CREATE INDEX IF NOT EXISTS idx_tickets_last_message_at
    ON tickets (last_message_at);
CREATE INDEX IF NOT EXISTS idx_tickets_user_last_message_at
    ON tickets (user_id, last_message_at);
CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket_id
    ON ticket_messages (ticket_id);
CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket_created_at
    ON ticket_messages (ticket_id, created_at);
