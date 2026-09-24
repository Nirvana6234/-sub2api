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

// ContributionAccountVerification records the latest safe-to-display test result for an account.
type ContributionAccountVerification struct {
	ent.Schema
}

func (ContributionAccountVerification) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "contribution_account_verifications"},
	}
}

func (ContributionAccountVerification) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (ContributionAccountVerification) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("account_id").
			Unique(),
		field.String("platform").
			MaxLen(50).
			NotEmpty(),
		field.String("status").
			MaxLen(20).
			NotEmpty().
			Default("pending"),
		field.String("model_family").
			MaxLen(32).
			NotEmpty().
			Default("unknown").
			Comment("Model family proven by the canonical contribution probe, for example gpt or claude."),
		field.String("source_kind").
			MaxLen(32).
			NotEmpty().
			Default("unknown").
			Comment("Credential provenance classification such as official_openai or openai_compatible."),
		field.String("tested_model").
			MaxLen(200).
			Optional().
			Nillable(),
		field.Time("tested_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("redacted_error_summary").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Comment("Redacted summary only; credentials and raw upstream errors must not be stored here."),
	}
}

func (ContributionAccountVerification) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("account", Account.Type).
			Field("account_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (ContributionAccountVerification) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("platform", "status"),
	}
}
