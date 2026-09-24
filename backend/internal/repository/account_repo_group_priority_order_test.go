package repository

import (
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func accountListOrderSQL(params pagination.PaginationParams, groupID int64) string {
	selector := entsql.Dialect(dialect.Postgres).Select("*").From(entsql.Table("accounts"))
	for _, order := range accountListOrder(params, groupID) {
		order(selector)
	}
	query, _ := selector.Query()
	return query
}

func TestAccountListOrderUsesInGroupPriorityWhenGroupFiltered(t *testing.T) {
	query := accountListOrderSQL(pagination.PaginationParams{SortBy: "priority", SortOrder: "desc"}, 23)
	require.Contains(t, query, `(SELECT ag.priority FROM account_groups ag WHERE ag.account_id = "accounts"."id" AND ag.group_id = 23) DESC NULLS LAST`)
	require.Contains(t, query, `"accounts"."id" DESC`)

	asc := accountListOrderSQL(pagination.PaginationParams{SortBy: "priority", SortOrder: "asc"}, 23)
	require.Contains(t, asc, `ag.group_id = 23) ASC NULLS LAST`)
}

func TestAccountListOrderKeepsAccountPriorityWithoutGroupFilter(t *testing.T) {
	query := accountListOrderSQL(pagination.PaginationParams{SortBy: "priority", SortOrder: "desc"}, 0)
	require.NotContains(t, query, "account_groups")
	require.Contains(t, query, `"priority" DESC`)
}
