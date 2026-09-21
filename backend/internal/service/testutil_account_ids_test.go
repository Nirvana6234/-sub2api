//go:build unit

package service

// accountIDs 是测试专用的 []Account -> []int64 辅助函数，供 grok_free_quota_gate_test.go
// 等多个测试文件共用。贡献房间功能迁移完成后 account_contributor_preference_test.go
// 会带来同名函数，届时删除这个临时文件即可。
func accountIDs(accounts []Account) []int64 {
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	return ids
}
