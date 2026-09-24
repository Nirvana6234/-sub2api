package repository

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ListDueUpstreamBillingProbeAccounts 的候选条件必须镜像 service 侧的
// upstreamBillingProbeShouldRun。两者一旦分叉，被 Go 跳过的账号仍会被 SQL 选中，
// 每轮占满 LIMIT 名额却永远探不动——而且它们永远拿不到 probe 快照，于是
// probe_status 恒为 NULL，恒排在 ORDER BY 的优先级 0，把队首长期占死。
//
// 2026-09-14 生产就是这个形态：37 个候选里 21 个填了手工倍率（Go 侧直接跳过），
// 真正该刷新的账号被饿了三周，页面上一片"已过期"。
//
// SQL 走的是 Postgres 方言，只能靠集成测试真跑（见同目录 integration 标签的
// 用例）。这里做一层无需数据库的源文本守卫：改动 WHERE 条件时必须同步这里，
// 避免镜像关系被无声删掉。
func TestListDueUpstreamBillingProbeAccountsMirrorsShouldRun(t *testing.T) {
	source, err := os.ReadFile("account_repo.go")
	require.NoError(t, err)

	start := strings.Index(string(source), "func (r *accountRepository) ListDueUpstreamBillingProbeAccounts")
	require.Positive(t, start, "找不到目标函数，测试需要跟着改名")
	body := string(source)[start:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}

	t.Run("填了手工倍率的账号不得成为候选", func(t *testing.T) {
		// 对应 upstreamBillingProbeShouldRun 里的：
		//   if _, manual := upstreamBillingManualRateMultiplier(account.Extra); manual { return false }
		require.Contains(t, body, "upstream_billing_manual_rate_multiplier",
			"SQL 必须排除已声明手工倍率的账号，否则它们会把探测队首占死")
		require.Contains(t, body, "AND NOT COALESCE(",
			"手工倍率必须是排除条件而不是筛选条件，且必须 COALESCE 兜住 NULL")
	})

	t.Run("从没声明过成本的账号必须是候选", func(t *testing.T) {
		// 对应 upstreamBillingProbeShouldRun 的返回值：
		//   return account.RateMultiplierUndeclared || upstreamBillingProbeEnabled(account)
		// 只认 probe_enabled 会让"未声明"的账号永远得不到成本，
		// 进而在记账里按 1.0 计、在兜底取号时被当成未声明直接拒绝。
		require.Contains(t, body, "rate_multiplier_undeclared",
			"SQL 必须把 rate_multiplier_undeclared 的账号纳入候选")
		require.Contains(t, body, `extra @> '{"upstream_billing_probe_enabled": true}'::jsonb`,
			"账号级开关仍是候选条件之一")
	})

	t.Run("排除条件必须用COALESCE兜住NULL", func(t *testing.T) {
		// 2026-09-14 的真实事故：排除条件写成了裸 NOT (...)，而键不存在时
		//   extra->'upstream_billing_manual_rate_multiplier'  → NULL
		//   jsonb_typeof(NULL) = 'number'                     → NULL
		//   NULL AND x                                        → NULL
		//   NOT NULL                                          → NULL（不是 TRUE）
		// WHERE 只保留 TRUE，于是"没填手工倍率"的账号被整批排除——恰恰是唯一
		// 需要探测的那批。生产候选数从 45 掉到 0，自动探测静默停摆一小时，
		// 页面上全是"已过期"，只能手动点刷新。
		//
		// 这是本文件其余断言抓不到的一类错误：字符串都在，语义是反的。
		idx := strings.Index(body, "upstream_billing_manual_rate_multiplier')")
		require.Positive(t, idx)
		window := body[max(0, idx-260):idx]
		require.Contains(t, window, "COALESCE(",
			"排除手工倍率的条件必须用 COALESCE 把 NULL 归一为 false，否则会把没填倍率的账号全部排除")
	})

	t.Run("排除条件必须窄于Go侧判定", func(t *testing.T) {
		// Go 的 upstreamBillingManualRateMultiplier 会拒绝负数/NaN/Inf 和非数字写法，
		// 那些情况下 shouldRun 仍为 true。SQL 只排除"明确是合法数字"的情形，
		// 保证候选集始终是 shouldRun 的超集：宁可多选一个由 Go 跳过，
		// 也不能少选一个该探测的。
		require.Contains(t, body, "jsonb_typeof(extra->'upstream_billing_manual_rate_multiplier') = 'number'",
			"必须先确认是 JSON 数字再排除，否则异常写法会被误排除")
		require.Contains(t, body, ">= 0",
			"负数手工倍率在 Go 侧不算声明，SQL 不得据此排除")
	})
}
