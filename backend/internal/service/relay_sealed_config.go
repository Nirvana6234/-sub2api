package service

import (
	"encoding/json"
	"sort"
)

// SealedConfigProxyIDs 返回主从分流时加密下发给从节点的配置引用的代理 ID（设计 6 第二类）：
// 内容审核配置的 proxy_id、联网搜索各服务的 proxy_id。从节点在本地执行这些功能，代理（含密码）随之下发。
func SealedConfigProxyIDs(moderationRaw, webSearchRaw string) []int64 {
	seen := map[int64]struct{}{}
	add := func(id *int64) {
		if id != nil && *id > 0 {
			seen[*id] = struct{}{}
		}
	}
	if moderationRaw != "" {
		var cfg struct {
			ProxyID *int64 `json:"proxy_id"`
		}
		if json.Unmarshal([]byte(moderationRaw), &cfg) == nil {
			add(cfg.ProxyID)
		}
	}
	if webSearchRaw != "" {
		var cfg WebSearchEmulationConfig
		if json.Unmarshal([]byte(webSearchRaw), &cfg) == nil {
			for _, p := range cfg.Providers {
				add(p.ProxyID)
			}
		}
	}
	ids := make([]int64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
