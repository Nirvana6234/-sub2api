package node

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

// EventHandlers 处理主节点推来的事件。
type EventHandlers struct {
	// OnInvalidation 清掉对应的 Key 缓存、票据、额度等（设计 6 第三类）。
	OnInvalidation func(*relayv1.Invalidation)
	// OnTicketRevocations 合并进票据吊销表（sign.RevocationList.Apply，设计 8.1）。
	OnTicketRevocations func(*relayv1.TicketRevocations)
	// OnQuotaRecall 响应额度收回（QuotaSync.HandleRecall，在自己的协程里做，不阻塞事件流）。
	OnQuotaRecall func(*relayv1.QuotaRecall)
}

// RunEvents 维持到主节点的事件流，直到 ctx 结束：断开后按退避重连；
// 每次连上先同步拉一次配置（断线期间可能错过了变更），之后按推送拉取新版本。
func RunEvents(ctx context.Context, client *transport.Client, syncer *ConfigSyncer, handlers EventHandlers) {
	events := relayv1.NewRelayEventsClient(client.Conn(transport.TierEvents))
	transport.RunStreamLoop(ctx, transport.DefaultBackoff(), 30*time.Second, func(ctx context.Context) error {
		stream, err := events.Stream(ctx)
		if err != nil {
			return err
		}
		if err := syncer.Sync(ctx); err != nil {
			slog.Warn("relay config sync after reconnect failed", "error", err)
		}
		for {
			env, err := stream.Recv()
			if err != nil {
				return err
			}
			switch body := env.Body.(type) {
			case *relayv1.MasterEnvelope_ConfigChanged:
				if body.ConfigChanged.GetVersion() != syncer.cache.Version() {
					go func() {
						if err := syncer.Sync(ctx); err != nil {
							slog.Warn("relay config sync failed", "error", err)
						}
					}()
				}
			case *relayv1.MasterEnvelope_Invalidation:
				if handlers.OnInvalidation != nil {
					handlers.OnInvalidation(body.Invalidation)
				}
			case *relayv1.MasterEnvelope_TicketRevocations:
				if handlers.OnTicketRevocations != nil {
					handlers.OnTicketRevocations(body.TicketRevocations)
				}
			case *relayv1.MasterEnvelope_QuotaRecall:
				if handlers.OnQuotaRecall != nil {
					go handlers.OnQuotaRecall(body.QuotaRecall)
				}
			}
		}
	}, func(err error) {
		slog.Warn("relay event stream disconnected", "error", err)
	})
}
