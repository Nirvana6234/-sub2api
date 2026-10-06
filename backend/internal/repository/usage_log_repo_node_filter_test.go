package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 使用记录按转发节点筛选：0 是主节点自己转发的（node_id 为 NULL），正数是那台从节点，nil 不限。
func TestAppendUsageLogNodeWhereCondition(t *testing.T) {
	conds, args := appendUsageLogNodeWhereCondition(nil, nil, nil)
	require.Empty(t, conds)
	require.Empty(t, args)

	zero := int64(0)
	conds, args = appendUsageLogNodeWhereCondition([]string{"user_id = $1"}, []any{int64(5)}, &zero)
	require.Equal(t, []string{"user_id = $1", "node_id IS NULL"}, conds)
	require.Equal(t, []any{int64(5)}, args, "no placeholder for the master filter")

	node := int64(12)
	conds, args = appendUsageLogNodeWhereCondition([]string{"user_id = $1"}, []any{int64(5)}, &node)
	require.Equal(t, []string{"user_id = $1", "node_id = $2"}, conds)
	require.Equal(t, []any{int64(5), int64(12)}, args)
}
