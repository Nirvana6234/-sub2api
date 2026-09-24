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

// ContributionRoom holds an independently managed pool of contributed accounts.
type ContributionRoom struct {
	ent.Schema
}

func (ContributionRoom) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "contribution_rooms"},
	}
}

func (ContributionRoom) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (ContributionRoom) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("owner_user_id"),
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		field.Float("consumer_rate_multiplier").
			Default(1.0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(10,4)"}),
		field.String("status").
			MaxLen(20).
			NotEmpty().
			Default("active"),
		field.String("visibility").
			MaxLen(20).
			NotEmpty().
			Default("private"),
	}
}

func (ContributionRoom) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("owner", User.Type).
			Field("owner_user_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("accounts", ContributionRoomAccount.Type),
		edge.To("preferences", UserContributionRoomPreference.Type),
	}
}

func (ContributionRoom) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("owner_user_id"),
		index.Fields("visibility", "status"),
	}
}
