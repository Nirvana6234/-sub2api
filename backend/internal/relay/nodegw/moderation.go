package nodegw

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodestore"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Moderation 是从节点上的安全审计（设计 3.4）：与单机同一个审核服务和协调器，判定在本机；
// 依赖换成从节点的实现——配置来自配置快照（审核配置和代理在加密下发的部分）、记录写本机存储、
// 命中过的输入名单查本机副本、累计违规和封号经主节点。
type Moderation struct {
	Service     *service.ContentModerationService
	Coordinator *securityaudit.Coordinator
	Hashes      *node.FlaggedHashReplica
}

// NewModeration 组装从节点上的安全审计。ctx 结束时后台的违规上报重试停止。
func NewModeration(ctx context.Context, cache *node.ConfigCache, store *nodestore.Store, client *transport.Client) *Moderation {
	hashes := node.NewFlaggedHashReplica(client)
	svc := service.NewContentModerationService(cache, node.NewModerationStore(store), hashes, nil, nil, node.NewSealedProxies(cache), nil, nil)
	svc.SetAccountActions(node.NewRemoteModerationActions(ctx, client))
	return &Moderation{
		Service:     svc,
		Coordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(svc), nil),
		Hashes:      hashes,
	}
}
