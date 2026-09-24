package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

// ContributionRoomAccount assigns one account to one contribution room.
type ContributionRoomAccount struct {
	ent.Schema
}

func (ContributionRoomAccount) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "contribution_room_accounts"},
	}
}

func (ContributionRoomAccount) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (ContributionRoomAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("room_id"),
		field.Int64("account_id").
			Unique(),
		field.Bool("enabled").
			Default(true),
		field.Int("share_concurrency").
			Default(1).
			Comment("Maximum concurrent requests that this room may route to the contributed account."),
		field.Float("share_budget_usd").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("Maximum raw token cost this room may consume from the contributed account."),
		field.Float("share_used_usd").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("Raw token cost already consumed through this room membership."),
		field.Time("verified_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (ContributionRoomAccount) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("room", ContributionRoom.Type).
			Ref("accounts").
			Field("room_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("account", Account.Type).
			Field("account_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (ContributionRoomAccount) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("room_id", "enabled"),
	}
}
