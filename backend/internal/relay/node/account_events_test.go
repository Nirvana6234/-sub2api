package node

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 会话窗口：只有响应头里带 anthropic-ratelimit-unified-* 时才发账号事件，而且只带这几个头。
func TestRemoteReporterSendsSessionWindowHeadersOnly(t *testing.T) {
	outbox := NewEventOutbox(0)
	r := NewRemoteAccountReporter(outbox)
	account := &service.Account{ID: 7}

	r.UpdateSessionWindow(context.Background(), account, http.Header{"Content-Type": {"application/json"}})
	require.Zero(t, outbox.Len(), "no window headers, nothing to report")

	r.UpdateSessionWindow(context.Background(), account, http.Header{
		"Anthropic-Ratelimit-Unified-5h-Status": {"allowed"},
		"Anthropic-Ratelimit-Unified-5h-Reset":  {"1790000000"},
		"Set-Cookie":                            {"secret"},
	})
	require.Equal(t, 1, outbox.Len())
	ev := outbox.peek().GetAccountEvent()
	require.Equal(t, int64(7), ev.GetAccountId())
	names := map[string]bool{}
	for _, h := range ev.GetSessionWindow().GetHeaders() {
		names[h.GetName()] = true
	}
	require.Equal(t, map[string]bool{"Anthropic-Ratelimit-Unified-5h-Status": true, "Anthropic-Ratelimit-Unified-5h-Reset": true}, names)
}
