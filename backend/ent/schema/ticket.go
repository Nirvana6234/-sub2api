package schema

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Ticket holds the schema definition for the Ticket entity.
//
// 用户提交的工单。用户只能看到自己的工单，管理员可见全部。
// 删除策略：硬删除（消息通过外键级联删除）。
type Ticket struct {
	ent.Schema
}

func (Ticket) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "tickets"},
	}
}

func (Ticket) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id").
			Comment("提交工单的用户ID"),
		field.String("subject").
			MaxLen(200).
			NotEmpty().
			Comment("工单标题"),
		field.String("status").
			MaxLen(20).
			Default(domain.TicketStatusOpen).
			Comment("状态: open(待管理员回复), answered(已回复), closed(已关闭)"),
		field.Int("user_unread_count").
			Default(0).
			NonNegative().
			Comment("用户侧未读消息数（管理员回复时递增，用户查看后清零）"),
		field.Int("admin_unread_count").
			Default(0).
			NonNegative().
			Comment("管理员侧未读消息数（用户发言时递增，管理员查看后清零）"),
		field.Time("last_message_at").
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).
			Comment("最后一条消息时间，用于列表排序"),
		field.Time("closed_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).
			Comment("关闭时间"),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (Ticket) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("messages", TicketMessage.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("user", User.Type).
			Ref("tickets").
			Field("user_id").
			Unique().
			Required(),
	}
}

func (Ticket) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id"),
		index.Fields("status"),
		index.Fields("last_message_at"),
		// 用户侧列表：按用户过滤 + 按最后消息时间倒序
		index.Fields("user_id", "last_message_at"),
	}
}
