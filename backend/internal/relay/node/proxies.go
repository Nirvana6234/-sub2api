package node

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// sealedSectionProxies 与 master.SealedSectionProxies 相同。
const sealedSectionProxies = "proxies"

// SealedProxies 是从节点上的代理仓储：只有加密下发的配置（内容审核、联网搜索）引用的代理，
// 来自配置快照里加密下发的部分（设计 6 第二类）。上游账号的代理随选号下发，不经这里。
// 只实现按 ID 查询；其余方法属于后台管理，从节点上不会调用（调到会 panic）。
type SealedProxies struct {
	service.ProxyRepository
	cache *ConfigCache
}

var _ service.ProxyRepository = (*SealedProxies)(nil)

// NewSealedProxies 创建代理仓储。
func NewSealedProxies(cache *ConfigCache) *SealedProxies { return &SealedProxies{cache: cache} }

func (p *SealedProxies) all() ([]service.Proxy, error) {
	raw, ok := p.cache.SealedSection(sealedSectionProxies)
	if !ok {
		return nil, nil
	}
	var list []service.Proxy
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// GetByID 按 ID 取代理；不在下发范围内时返回 service.ErrProxyNotFound。
func (p *SealedProxies) GetByID(_ context.Context, id int64) (*service.Proxy, error) {
	list, err := p.all()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			px := list[i]
			return &px, nil
		}
	}
	return nil, service.ErrProxyNotFound
}

// ListByIDs 按 ID 取多个代理（不在下发范围内的跳过，与仓储查不到时一样）。
func (p *SealedProxies) ListByIDs(_ context.Context, ids []int64) ([]service.Proxy, error) {
	list, err := p.all()
	if err != nil {
		return nil, err
	}
	want := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	var out []service.Proxy
	for _, px := range list {
		if _, ok := want[px.ID]; ok {
			out = append(out, px)
		}
	}
	return out, nil
}
