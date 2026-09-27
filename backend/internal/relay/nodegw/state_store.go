package nodegw

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// StateStore 是从节点上的响应/会话状态存储：只在本进程内存里（不连 Redis），记响应账号时把 response id
// 记到这次尝试上，释放时带回主节点，由主节点写响应归属（设计 3.1，开发计划 WP7 的 BindRelayHTTPResponse）。
type StateStore struct {
	service.OpenAIWSStateStore
}

// NewStateStore 创建状态存储；装在从节点的 OpenAIGatewayService 上（SetOpenAIWSStateStore）。
func NewStateStore() *StateStore {
	return &StateStore{OpenAIWSStateStore: service.NewOpenAIWSStateStore(nil)}
}

// BindResponseAccount 记本机内存，并把 response id 记到这次尝试上。
func (s *StateStore) BindResponseAccount(ctx context.Context, groupID int64, responseID string, accountID int64, ttl time.Duration) error {
	if a := attemptFrom(ctx); a != nil {
		a.addResponseID(responseID)
	}
	return s.OpenAIWSStateStore.BindResponseAccount(ctx, groupID, responseID, accountID, ttl)
}
