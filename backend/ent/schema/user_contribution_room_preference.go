package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

// UserContributionRoomPreference stores the user's selected contribution room and fallback policy.
type UserContributionRoomPreference struct {
	ent.Schema
}

func (UserContributionRoomPreference) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "user_contribution_room_preferences"},
	}
}

func (UserContributionRoomPreference) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (UserContributionRoomPreference) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.Int64("api_key_id"),
		field.Int64("room_id"),
		field.Bool("allow_pool_fallback").
			Default(false),
		field.Int64("fallback_group_id").
			Optional().
			Nillable(),
	}
}

func (UserContributionRoomPreference) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("user", User.Type).
			Field("user_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("api_key", APIKey.Type).
			Ref("contribution_room_preferences").
			Field("api_key_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("room", ContributionRoom.Type).
			Ref("preferences").
			Field("room_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (UserContributionRoomPreference) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("room_id"),
		index.Fields("user_id"),
		index.Fields("api_key_id", "room_id").Unique(),
	}
}
