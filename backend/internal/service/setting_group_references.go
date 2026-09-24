package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// GroupReferenceCleaner 清除系统设置中对某个分组的引用。
//
// 分组删除后必须调用它，否则设置里会留下悬挂 ID。悬挂 ID 的后果远不止"那一项
// 配置失效"：系统设置保存走整文档校验，一个指向已删除分组的 playground 候选项
// 会让**整个设置页面再也保存不了任何东西**——包括与 playground 毫无关系的邀请
// 返利。而管理台的候选列表只渲染活跃分组，用户在界面上看不到、也删不掉那个 ID，
// 于是形成死锁，只能改库解开。
//
// 由 SettingService 实现，adminService 在删除分组时调用。
type GroupReferenceCleaner interface {
	RemoveGroupReferences(ctx context.Context, groupID int64) error
}

// playgroundGroupReferenceKeys 是所有以「分组 ID 列表」形式引用分组的设置键。
// 新增同类设置时必须登记到这里，否则该设置又会成为新的悬挂引用来源。
var playgroundGroupReferenceKeys = []string{
	SettingKeyPlaygroundDefaultChatGroupIDs,
	SettingKeyPlaygroundDefaultImageGroupIDs,
}

// RemoveGroupReferences 从所有分组引用型设置中剔除 groupID。
//
// 只在确实发生变更时写库，因此对未引用该分组的系统是零开销的空操作。
// 单个键解析失败不会中断其余键的清理：清理是尽力而为的收尾动作，不应该因为
// 一项历史脏数据把其他项一起卡住。
func (s *SettingService) RemoveGroupReferences(ctx context.Context, groupID int64) error {
	if s == nil || s.settingRepo == nil || groupID <= 0 {
		return nil
	}
	stored, err := s.settingRepo.GetMultiple(ctx, playgroundGroupReferenceKeys)
	if err != nil {
		return fmt.Errorf("load group reference settings: %w", err)
	}

	updates := make(map[string]string, len(playgroundGroupReferenceKeys))
	for _, key := range playgroundGroupReferenceKeys {
		raw, ok := stored[key]
		if !ok {
			continue
		}
		remaining, changed := removeGroupIDFromJSONList(raw, groupID)
		if !changed {
			continue
		}
		encoded, err := json.Marshal(remaining)
		if err != nil {
			slog.Warn("encode group reference setting failed", "key", key, "group_id", groupID, "error", err)
			continue
		}
		updates[key] = string(encoded)
	}
	if len(updates) == 0 {
		return nil
	}
	if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
		return fmt.Errorf("persist group reference cleanup: %w", err)
	}
	slog.Info("removed deleted group from settings references", "group_id", groupID, "keys", len(updates))

	// 走与常规设置写入相同的失效回调：缓存里仍是清理前的列表，不失效的话
	// playground 会继续把已删除的分组当作候选。这里刻意不自行调用
	// GetAllSettings 重建缓存——那需要 SettingService 的全部依赖就绪，而清理是
	// 删除分组的收尾动作，不该因为缓存重建路径的依赖缺失而崩溃。
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return nil
}

// removeGroupIDFromJSONList 从 JSON 数组字符串里剔除指定分组 ID。
// 第二个返回值表示是否真的发生了变更，供调用方跳过无谓的写库。
func removeGroupIDFromJSONList(raw string, groupID int64) ([]int64, bool) {
	current := parsePlaygroundGroupIDs(raw)
	remaining := make([]int64, 0, len(current))
	for _, id := range current {
		if id == groupID {
			continue
		}
		remaining = append(remaining, id)
	}
	return remaining, len(remaining) != len(current)
}
